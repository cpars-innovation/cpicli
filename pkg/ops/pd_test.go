package ops

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/pdrepo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pdMockTenant serves the Partner Directory endpoints used by pd-deploy and
// records every DELETE it receives.
type pdMockTenant struct {
	mu           sync.Mutex
	strings      []cpi.StringParameter
	deletes      []string
	csrfRejected int
}

func (m *pdMockTenant) handler(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	path := r.URL.Path
	if path == "/api/v1/" && strings.EqualFold(r.Header.Get("X-CSRF-Token"), "fetch") {
		w.Header().Set("X-CSRF-Token", "pd-token")
		return
	}
	if r.Method != http.MethodGet && r.Header.Get("X-CSRF-Token") != "pd-token" {
		m.csrfRejected++
		w.Header().Set("X-CSRF-Token", "Required")
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodDelete:
		m.deletes = append(m.deletes, path)
		w.WriteHeader(http.StatusNoContent)
	case path == "/api/v1/StringParameters":
		_ = json.NewEncoder(w).Encode(map[string]any{"d": map[string]any{"results": m.strings}})
	case path == "/api/v1/BinaryParameters":
		_ = json.NewEncoder(w).Encode(map[string]any{"d": map[string]any{"results": []any{}}})
	case strings.HasPrefix(path, "/api/v1/StringParameters("):
		for _, p := range m.strings {
			if path == "/api/v1/StringParameters(Pid='"+p.Pid+"',Id='"+p.ID+"')" {
				_ = json.NewEncoder(w).Encode(map[string]any{"d": p})
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newPDMock(t *testing.T, params []cpi.StringParameter) (*pdMockTenant, *cpi.PartnerDirectory) {
	t.Helper()
	m := &pdMockTenant{strings: params}
	svr := httptest.NewServer(http.HandlerFunc(m.handler))
	t.Cleanup(svr.Close)
	host, port := httpclnt.GetHostPort(svr.URL)
	exe := httpclnt.New("", "", "", "", "dummy", "dummy", host, "http", port, false)
	return m, cpi.NewPartnerDirectory(exe)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
}

// TestFullSyncAbortsOnLocalReadError guards against the data-loss bug where a
// failed local read made full sync delete ALL remote parameters of that PID.
func TestFullSyncAbortsOnLocalReadError(t *testing.T) {
	remote := []cpi.StringParameter{
		{Pid: "BROKEN", ID: "A", Value: "1"},
		{Pid: "BROKEN", ID: "B", Value: "2"},
		{Pid: "HEALTHY", ID: "KEEP", Value: "x"},
		{Pid: "HEALTHY", ID: "STALE", Value: "y"},
		{Pid: "UNMANAGED", ID: "Z", Value: "z"},
	}

	cases := map[string]func(t *testing.T, dir string){
		// String.properties exists but cannot be read as a file
		"unreadable string properties": func(t *testing.T, dir string) {
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "BROKEN", "String.properties"), 0755))
		},
		// A binary parameter file that cannot be read (dangling symlink)
		"unreadable binary file": func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(dir, "BROKEN", "String.properties"), "A=1\nB=2\n")
			binDir := filepath.Join(dir, "BROKEN", "Binary")
			require.NoError(t, os.MkdirAll(binDir, 0755))
			require.NoError(t, os.Symlink(filepath.Join(dir, "does-not-exist"), filepath.Join(binDir, "cert.crt")))
		},
	}

	for name, breakLocal := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "HEALTHY", "String.properties"), "KEEP=x\n")
			breakLocal(t, dir)

			mock, pdAPI := newPDMock(t, remote)
			_, err := PDDeploy(pdAPI, pdrepo.New(dir), PDDeployOptions{Replace: true, FullSync: true, DryRun: false})

			// The run must be reported as failed ...
			require.Error(t, err)
			assert.Contains(t, err.Error(), "Full sync aborted for PID BROKEN")
			// ... nothing of the unreadable PID may be deleted, while full sync
			// still works for readable PIDs and never touches unmanaged ones.
			assert.Equal(t, []string{"/api/v1/StringParameters(Pid='HEALTHY',Id='STALE')"}, mock.deletes)
		})
	}
}

func TestPDDeployReturnsErrorOnPartialFailure(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "P1", "String.properties"), "OK=1\n")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "P2", "String.properties"), 0755)) // unreadable

	_, pdAPI := newPDMock(t, []cpi.StringParameter{{Pid: "P1", ID: "OK", Value: "1"}})
	_, err := PDDeploy(pdAPI, pdrepo.New(dir), PDDeployOptions{Replace: true, FullSync: false, DryRun: false})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "P2")
}

func TestPDDeploySucceedsWhenNothingFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "P1", "String.properties"), "OK=1\n")

	mock, pdAPI := newPDMock(t, []cpi.StringParameter{{Pid: "P1", ID: "OK", Value: "1"}})
	_, err := PDDeploy(pdAPI, pdrepo.New(dir), PDDeployOptions{Replace: true, FullSync: true, DryRun: false})
	require.NoError(t, err)
	assert.Empty(t, mock.deletes)
}
