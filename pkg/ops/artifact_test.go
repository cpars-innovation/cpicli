package ops

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeIFlow(t *testing.T, dir, script string) {
	t.Helper()
	files := map[string]string{
		"META-INF/MANIFEST.MF":                                                    "Manifest-Version: 1.0\nBundle-SymbolicName: OrderIntake\nBundle-Name: Order Intake\nBundle-Version: 1.0.0\n",
		"src/main/resources/parameters.prop":                                      "Host=example.com\n",
		"src/main/resources/script/script1.groovy":                                script,
		"src/main/resources/scenarioflows/integrationflow/OrderIntake.iflw":       "<bpmn2:definitions/>\n",
		"src/main/resources/scenarioflows/integrationflow/OrderIntake.propdef.xx": "",
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0644))
	}
}

func zipEntry(t *testing.T, data []byte, name string) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			require.NoError(t, err)
			b, _ := io.ReadAll(rc)
			rc.Close()
			return string(b)
		}
	}
	t.Fatalf("%s not in zip", name)
	return ""
}

// The upload path behind upload_artifact, `update artifact` and the
// orchestrator: create, detect unchanged content, update, and undeploy the
// running artifact when the content changed but the version did not.
func TestUploadArtifactLifecycle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "OrderIntake")
	writeIFlow(t, dir, "println 'v1'")
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{})
	req := UploadRequest{ID: "OrderIntake", Type: "Integration", PackageID: "Orders", Dir: dir}

	res, err := UploadArtifact(mock.Executer(), req)
	require.NoError(t, err)
	assert.Equal(t, "CREATED", res.Action)
	a := mock.Artifacts["OrderIntake"]
	require.NotNil(t, a)
	assert.Equal(t, "Orders", a.Package)
	assert.Equal(t, "Order Intake", a.Name, "without a name the manifest's Bundle-Name is used, as in the CLI")
	assert.Equal(t, "println 'v1'", zipEntry(t, a.Zip, "src/main/resources/script/script1.groovy"))

	res, err = UploadArtifact(mock.Executer(), req)
	require.NoError(t, err)
	assert.Equal(t, "UNCHANGED", res.Action, "same content is not uploaded again")
	assert.Equal(t, 1, a.Uploads)

	// deployed with the same version, then the content changes
	a.Runtime = &cpitest.Runtime{Version: "1.0.0", Status: "STARTED"}
	writeIFlow(t, dir, "println 'v2'")
	res, err = UploadArtifact(mock.Executer(), req)
	require.NoError(t, err)
	assert.Equal(t, "UPDATED", res.Action)
	assert.True(t, res.RuntimeUndeployed, "same-version runtime is undeployed so the new content gets deployed")
	assert.Equal(t, 2, a.Uploads)
	assert.Equal(t, "println 'v2'", zipEntry(t, a.Zip, "src/main/resources/script/script1.groovy"))
	assert.Equal(t, 1, mock.Count("DELETE /api/v1/IntegrationRuntimeArtifacts('OrderIntake')"))
	assert.GreaterOrEqual(t, mock.CSRFFetches(), 1)
}

func TestUploadArtifactRejectsDraftAndWrongPackage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "OrderIntake")
	writeIFlow(t, dir, "x")
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		// exists in another package
		"OrderIntake": {Type: "Integration", DesignVersion: "1.0.0", Package: "Other"},
	})
	_, err := UploadArtifact(mock.Executer(), UploadRequest{ID: "OrderIntake", Type: "Integration", PackageID: "Orders", Dir: dir})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OrderIntake already exists in package Other, not in Orders")
	assert.Equal(t, exitcode.Error, output.ExitCode(err))
	assert.Zero(t, mock.Artifacts["OrderIntake"].Uploads)
}

// An update of an artifact open in the Web UI is refused with a bare 400:
// the error names who holds the lock and keeps the tenant's message.
func TestUploadArtifactLocked(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "OrderIntake")
	writeIFlow(t, dir, "println 'v1'")
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{})
	req := UploadRequest{ID: "OrderIntake", Type: "Integration", PackageID: "Orders", Dir: dir}
	_, err := UploadArtifact(mock.Executer(), req)
	require.NoError(t, err)

	since := time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)
	mock.Locks = append(mock.Locks, cpitest.Lock{ResourceID: "r1", ArtifactID: "OrderIntake", CreatedBy: "anna@customer.example", CreatedAt: since})
	writeIFlow(t, dir, "println 'v2'")
	_, err = UploadArtifact(mock.Executer(), req)
	require.Error(t, err)
	assert.Equal(t, exitcode.TenantHTTP, output.ExitCode(err), "still a tenant error")
	assert.Contains(t, err.Error(), "the tenant says: Artifact OrderIntake is locked")
	assert.Contains(t, err.Error(), "OrderIntake is locked by anna@customer.example since 2026-10-14T09:30:00Z")
	assert.Contains(t, err.Error(), "cpictl locks unlock --artifact-id OrderIntake --confirm")
}

func TestTenantErrorText(t *testing.T) {
	assert.Equal(t, "bad", tenantErrorText([]byte(`{"error":{"code":"x","message":{"lang":"en","value":"bad"}}}`)))
	assert.Equal(t, "plain", tenantErrorText([]byte(`{"error":{"message":"plain"}}`)))
	assert.Equal(t, "some text", tenantErrorText([]byte("  some\n text ")))
	assert.Empty(t, tenantErrorText([]byte("<html>login</html>")))
	assert.Empty(t, tenantErrorText(nil))
}
