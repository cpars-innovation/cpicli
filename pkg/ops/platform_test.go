package ops

import (
	"archive/zip"
	"bytes"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectionsArchivesSettings(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(mock, "dev", time.Now()))
	exe := mock.Executer()

	res, err := ListConnections(exe, false, "")
	require.NoError(t, err)
	require.NotEmpty(t, res.Connections)
	hosts := map[string][]string{}
	for _, c := range res.Connections {
		for _, f := range c.Flows {
			hosts[f.ID] = append(hosts[f.ID], c.ReceiverHost)
		}
	}
	assert.Contains(t, hosts, "Partner_Notify", "SFTP receiver")
	assert.NotContains(t, hosts, "Orders_In", "ProcessDirect stays inside the tenant")
	filtered, err := ListConnections(exe, true, "partner_notify")
	require.NoError(t, err)
	require.Len(t, filtered.Connections, 1)
	assert.Equal(t, "sftp.partner.example.com", filtered.Connections[0].ReceiverHost)

	file := filepath.Join(t.TempDir(), "logs", "trace.zip")
	ar, err := DownloadLogArchive(exe, "trace", file)
	require.NoError(t, err)
	assert.Equal(t, "trace", ar.Archive.LogFileType)
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	_, err = zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	_, err = DownloadLogArchive(exe, "system", file)
	assert.ErrorContains(t, err, "available: http, trace")

	s, err := GetTenantSettings(exe)
	require.NoError(t, err)
	require.NotNil(t, s.ExternalLogging)
	assert.False(t, *s.ExternalLogging)
	assert.Empty(t, s.Warnings)
	require.NoError(t, cpi.SetExternalLogging(exe, true))
	require.NoError(t, cpi.ActivateArchiving(exe))
	s, err = GetTenantSettings(exe)
	require.NoError(t, err)
	assert.True(t, *s.ExternalLogging)
	assert.True(t, *s.Archiving)
}

// A gateway (or a missing role) refusing every part: a refusal, not a
// result with warnings; the message says the gateway refused.
func TestTenantSettingsAllRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"scope","message":{"lang":"en","value":"scope: settings:read is not granted"}}}`))
	}))
	defer srv.Close()
	exe := cpi.InitHTTPExecuter(&cpi.ServiceDetails{Gateway: srv.URL + "/t/dev", GatewayToken: "t"})
	_, err := GetTenantSettings(exe)
	require.Error(t, err)
	assert.True(t, httpclnt.IsGatewayRefusal(err))
	assert.Contains(t, err.Error(), "refused by the cpictl gateway: scope: settings:read is not granted")
	assert.NotContains(t, WithTenantText(err).Error(), "the tenant says")
}
