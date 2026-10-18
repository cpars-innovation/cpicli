package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMockTenantCommand(t *testing.T) {
	cpitest.SetHome(t, t.TempDir())
	ca := filepath.Join(t.TempDir(), "ca.pem")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var stdout, stderr bytes.Buffer
	code := Run(ctx, []string{"mock-tenant", "--addr", "127.0.0.1:0", "--tier", "prod", "--tls", "--ca-out", ca}, &stdout, &stderr, "test", "test")
	require.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "CPICTL_TMN_HOST=https://127.0.0.1:")
	assert.Contains(t, stdout.String(), "SSL_CERT_FILE="+ca)
	assert.Regexp(t, `CPICTL_MOCK_ADMIN_TOKEN=[0-9a-f]{48}
`, stdout.String())
	pemData, err := os.ReadFile(ca)
	require.NoError(t, err)
	assert.Contains(t, string(pemData), "BEGIN CERTIFICATE")

	assert.Equal(t, 2, runMain(t, "mock-tenant", "--tls").code, "--tls needs --ca-out")
	assert.Equal(t, 2, runMain(t, "mock-tenant", "--seed", "full").code)
	assert.Equal(t, 2, runMain(t, "mock-tenant", "--addr", "127.0.0.1:0", "--tier", "qa").code)
}

func TestMockTenantSeedDir(t *testing.T) {
	cpitest.SetHome(t, t.TempDir())
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "packages"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "landscape.yaml"), []byte("name: customer-b\ntiers: [{name: qa}, {name: prod-eu}]\n"), 0o644))
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var stdout, stderr bytes.Buffer
	code := Run(ctx, []string{"mock-tenant", "--addr", "127.0.0.1:0", "--seed-dir", dir, "--tier", "prod-eu"}, &stdout, &stderr, "test", "test")
	require.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "Mock CPI tenant (prod-eu, seed customer-b)")

	assert.Equal(t, 2, runMain(t, "mock-tenant", "--addr", "127.0.0.1:0", "--seed-dir", dir, "--tier", "dev").code, "not a tier of the landscape")
	assert.Equal(t, 2, runMain(t, "mock-tenant", "--addr", "127.0.0.1:0", "--seed-dir", t.TempDir()).code, "no landscape.yaml")
}

func TestMockTenantAdminTokenFlag(t *testing.T) {
	cpitest.SetHome(t, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var stdout, stderr bytes.Buffer
	code := Run(ctx, []string{"mock-tenant", "--addr", "127.0.0.1:0", "--admin-token", "my-token"}, &stdout, &stderr, "test", "test")
	require.Equal(t, 0, code, stderr.String())
	assert.NotContains(t, stdout.String(), "CPICTL_MOCK_ADMIN_TOKEN", "a token given by the user is not echoed")
	assert.NotContains(t, stdout.String(), "my-token")
}

func TestMockTenantCredentialsFlags(t *testing.T) {
	cpitest.SetHome(t, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var stdout, stderr bytes.Buffer
	code := Run(ctx, []string{"mock-tenant", "--addr", "127.0.0.1:0", "--seed", "empty", "--credentials", "strict", "--runtime-client-id", "rt"}, &stdout, &stderr, "test", "test")
	require.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "CPICTL_TMN_USERID=mock\n")
	assert.Contains(t, stdout.String(), "CPICTL_RUNTIME_OAUTH_CLIENTID=rt\nCPICTL_RUNTIME_OAUTH_CLIENTSECRET=mock-rt\n")
	assert.Contains(t, stdout.String(), "# credentials strict")

	assert.Equal(t, 2, runMain(t, "mock-tenant", "--credentials", "none").code)
	assert.Equal(t, 2, runMain(t, "mock-tenant", "--credentials", "strict", "--runtime-client-id", "mock").code, "the runtime client must differ")
}

// cpictl send against a mock that checks credentials like a tenant: the
// runtime client (CPICTL_RUNTIME_OAUTH_*) calls the endpoint, the API
// credentials alone are refused there.
func TestSendWithRuntimeCredentials(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(mock, "dev", time.Now()))
	mock.OAuth = true
	mock.Auth = &cpitest.Auth{APIUser: "user", APIPassword: "secret", APIClient: cpitest.Client{ID: "api", Secret: "api-secret"},
		Runtime: cpitest.Client{ID: "sb-rt|it-rt-b1", Secret: "rt-secret"}}
	host, port := mock.HostPort()
	hostPort := host + ":" + strconv.Itoa(port)

	r := runMain(t, append([]string{"send", "--artifact-id", "Orders_In", "--body", "<Order/>", "--output", "json"}, basicAuth(mock)...)...)
	assert.NotEqual(t, 0, r.code, "the API credentials cannot call a flow")
	assert.Contains(t, r.stdout+r.stderr, "403")

	r = runMainWith(t, func() {
		t.Setenv("CPICTL_RUNTIME_OAUTH_HOST", hostPort)
		t.Setenv("CPICTL_RUNTIME_OAUTH_CLIENTID", "sb-rt|it-rt-b1")
		t.Setenv("CPICTL_RUNTIME_OAUTH_CLIENTSECRET", "rt-secret")
	}, append([]string{"send", "--artifact-id", "Orders_In", "--body", "<Order/>", "--output", "json"}, basicAuth(mock)...)...)
	require.Equal(t, 0, r.code, r.stdout+r.stderr)
	var env struct {
		Result struct {
			HTTPStatus  int
			MessageGuid string
		}
	}
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &env), r.stdout)
	assert.Equal(t, 200, env.Result.HTTPStatus)
	assert.NotEmpty(t, env.Result.MessageGuid)
}
