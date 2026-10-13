package mocktenant_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/mocktenant"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDemo(t *testing.T) {
	m := mocktenant.Start()
	defer m.Close()
	require.NoError(t, mocktenant.SeedDemo(m, "prod", time.Now()))
	pkgs, err := ops.ListPackages(m.Executer())
	require.NoError(t, err)
	assert.Len(t, pkgs, 3)
	assert.NotEmpty(t, m.MessageLogs())
}

func TestSeedDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "packages", "Empty"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "landscape.yaml"), []byte("name: x\ntiers: [{name: qa}]\n"), 0o644))
	m := mocktenant.Start()
	defer m.Close()
	require.NoError(t, mocktenant.SeedDir(m, dir, "qa", time.Now()))
	assert.Empty(t, m.MessageLogs())
	assert.Error(t, mocktenant.SeedDir(m, dir, "prod", time.Now()))
}

func TestAuth(t *testing.T) {
	m := mocktenant.Start()
	defer m.Close()
	m.Auth = &mocktenant.Auth{APIUser: "user", APIPassword: "secret", Runtime: mocktenant.Client{ID: "rt", Secret: "rt"}}
	_, err := ops.ListPackages(m.Executer())
	require.NoError(t, err, "the API user")
	m.Auth.APIPassword = "other"
	_, err = ops.ListPackages(m.Executer())
	assert.Error(t, err, "a wrong password")
}

// The demo has flows with IDoc and OData senders; their endpoints are listed
// like the others.
func TestDemoSAPEndpoints(t *testing.T) {
	m := mocktenant.Start()
	defer m.Close()
	require.NoError(t, mocktenant.SeedDemo(m, "dev", time.Now()))
	idoc, err := ops.ResolveEndpoint(m.Executer(), "Orders_IDoc_In", "")
	require.NoError(t, err)
	assert.Equal(t, m.URL()+"/cxf/orders/idoc", idoc)
	odata, err := ops.ResolveEndpoint(m.Executer(), "Orders_OData_Service", "")
	require.NoError(t, err)
	assert.Equal(t, m.URL()+"/gw/odata/Demo/Orders;v=1", odata)
}
