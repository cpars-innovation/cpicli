package cmd

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A stand-in for the cpictl gateway: it checks the bearer token and the
// /t/dev prefix and forwards to the mock tenant.
func fakeGateway(t *testing.T, mock *cpitest.Tenant) (*httptest.Server, func() []string) {
	t.Helper()
	target, err := url.Parse(mock.URL())
	require.NoError(t, err)
	proxy := httputil.NewSingleHostReverseProxy(target)
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization")+" "+r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer user-token" || !strings.HasPrefix(r.URL.Path, "/t/dev/") {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/t/dev")
		r.URL.RawPath = ""
		r.Header.Del("Authorization")
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), seen...) }
}

func TestGatewayMode(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": {}})
	gw, seen := fakeGateway(t, mock)
	for _, k := range append(credentialConfigKeys, hostConfigKeys...) {
		t.Setenv(envKey(k), "")
	}
	t.Setenv("CPICTL_GATEWAY", "")

	t.Run("requests go to the gateway with the user's token", func(t *testing.T) {
		home := t.TempDir()
		writeModeFile(t, filepath.Join(home, ".cpictl", "gw.yaml"), "gateway: "+gw.URL+"/t/dev\n", 0o600)
		t.Setenv(gatewayTokenEnv, "user-token")
		code, stderr := projectRun(t, home, t.TempDir(), "--profile", "gw", "undeploy", "--artifact-ids", "A")
		assert.Equal(t, 0, code, stderr)
		require.NotEmpty(t, seen())
		for _, s := range seen() {
			assert.True(t, strings.HasPrefix(s, "Bearer user-token "), s)
			assert.Contains(t, s, " /t/dev/")
		}
		assert.NotContains(t, stderr, "user-token", "the token is never logged")
	})

	t.Run("no token", func(t *testing.T) {
		t.Setenv(gatewayTokenEnv, "")
		code, stderr := projectRun(t, t.TempDir(), t.TempDir(), "--gateway", gw.URL+"/t/dev", "undeploy", "--artifact-ids", "A")
		assert.NotEqual(t, 0, code)
		assert.Contains(t, stderr, gatewayTokenEnv)
	})

	t.Run("not together with SAP credentials", func(t *testing.T) {
		t.Setenv(gatewayTokenEnv, "user-token")
		t.Setenv("CPICTL_TMN_PASSWORD", "secret")
		code, stderr := projectRun(t, t.TempDir(), t.TempDir(), "--gateway", gw.URL+"/t/dev", "--tmn-userid", "u", "undeploy", "--artifact-ids", "A")
		assert.NotEqual(t, 0, code)
		assert.Contains(t, stderr, "exclude each other")
	})

	t.Run("without the SAP tenant", func(t *testing.T) {
		t.Setenv(gatewayTokenEnv, "user-token")
		code, stderr := projectRun(t, t.TempDir(), t.TempDir(), "--gateway", gw.URL, "undeploy", "--artifact-ids", "A")
		assert.NotEqual(t, 0, code)
		assert.Contains(t, stderr, "with the SAP tenant")
	})

	t.Run("a repository file cannot choose the gateway", func(t *testing.T) {
		home, repo := t.TempDir(), t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(repo, ".git"), 0o755))
		writeModeFile(t, filepath.Join(home, "cpictl.yaml"), "gateway: "+gw.URL+"/t/dev\n", 0o600)
		writeModeFile(t, filepath.Join(repo, "cpictl.yaml"), "gateway: https://attacker.example.com/t/dev\n", 0o644)
		t.Setenv(gatewayTokenEnv, "user-token")
		code, stderr := projectRun(t, home, repo, "undeploy", "--artifact-ids", "A")
		assert.NotEqual(t, 0, code)
		assert.Contains(t, stderr, "must not choose where your token is sent")
	})
}
