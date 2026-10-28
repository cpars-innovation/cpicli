package cmd

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
)

// A tenant from the environment never borrows the credentials of the active
// profile, and an explicit profile's secrets are never sent to another host
// (agent test of 0.34.0: CPICTL_TMN_HOST of a mock tenant plus the active
// profile's OAuth client sent a token request to the real tenant's host).
func TestEnvironmentTenantDoesNotMixWithProfile(t *testing.T) {
	real := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": {}})
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": {}})
	url := func(m *cpitest.Tenant) string {
		h, p := m.HostPort()
		return fmt.Sprintf("http://%s:%d", h, p)
	}
	for _, k := range append(append(credentialConfigKeys, hostConfigKeys...), "profile", "config", "gateway") {
		t.Setenv(envKey(k), "")
	}
	home := t.TempDir()
	h, p := real.HostPort()
	writeModeFile(t, filepath.Join(home, ".cpictl", "real.yaml"), "tmn-host: "+url(real)+fmt.Sprintf("\noauth-host: %s:%d\n", h, p)+
		"oauth-clientid: real-client\noauth-clientsecret: real-secret\n", 0o600)
	writeModeFile(t, filepath.Join(home, ".cpictl", "current"), "real\n", 0o600)

	// the active profile is not used for a tenant from the environment
	t.Setenv("CPICTL_TMN_HOST", url(mock))
	t.Setenv("CPICTL_TMN_USERID", "mock")
	t.Setenv("CPICTL_TMN_PASSWORD", "mock")
	code, stderr := projectRun(t, home, t.TempDir(), "undeploy", "--artifact-ids", "A")
	assert.Equal(t, 0, code, stderr)
	assert.Empty(t, real.Requests(), "nothing goes to the profile's tenant")
	assert.NotEmpty(t, mock.Requests())

	// an explicit profile with another host: refused before any request
	t.Setenv("CPICTL_TMN_USERID", "")
	t.Setenv("CPICTL_TMN_PASSWORD", "")
	before := len(mock.Requests())
	code, stderr = projectRun(t, home, t.TempDir(), "--profile", "real", "undeploy", "--artifact-ids", "A")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "holds the credentials for")
	assert.NotContains(t, stderr, "real-secret")
	assert.Empty(t, real.Requests())
	assert.Len(t, mock.Requests(), before)

	// the same host as the profile: not refused (the test tenant has no
	// token endpoint, so the call itself fails later)
	t.Setenv("CPICTL_TMN_HOST", url(real))
	code, stderr = projectRun(t, home, t.TempDir(), "--profile", "real", "undeploy", "--artifact-ids", "A")
	assert.NotEqual(t, 2, code, stderr)
	assert.NotContains(t, stderr, "holds the credentials for")
}
