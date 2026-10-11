package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A tenant:<profile> side uses the default OAuth path like --oauth-path does
// (found on a real tenant: the token request went to the host root).
func TestProfileServiceDetailsOAuthPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".cpictl"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".cpictl", "trial.yaml"),
		[]byte("tmn-host: tenant.example.com\noauth-host: auth.example.com\noauth-clientid: id\noauth-clientsecret: secret-value-123\n"), 0o600))
	sd, err := profileServiceDetails("trial")
	require.NoError(t, err)
	assert.Equal(t, "/oauth/token", sd.OauthPath)
}
