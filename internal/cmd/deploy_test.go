package cmd

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeployCommand_MultipleArtifactIDs(t *testing.T) {
	started := []*cpitest.Runtime{{Version: "1", Status: "STARTED", DeployedOn: time.Now()}}
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", DesignVersion: "1", TaskStatuses: []string{"SUCCESS"}, AfterDeploy: started},
		"B": {Type: "Integration", DesignVersion: "1", TaskStatuses: []string{"SUCCESS"}, AfterDeploy: started},
	})

	_, _, err := runCLI(t, mock, "deploy", "--artifact-ids", "A, B", "--delay-length", "0", "--max-check-limit", "3")
	require.NoError(t, err)
	assert.Equal(t, 2, mock.Count("POST /api/v1/DeployIntegrationDesigntimeArtifact"))
}

func TestDeployCommand_PartialFailureReturnsError(t *testing.T) {
	started := []*cpitest.Runtime{{Version: "1", Status: "STARTED", DeployedOn: time.Now()}}
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", DesignVersion: "1", AfterDeploy: started},
		"B": {Type: "Integration"},
	})
	_, _, err := runCLI(t, mock, "deploy", "--artifact-ids", "A,B", "--delay-length", "0", "--max-check-limit", "3")
	var depErr *ops.Error
	require.ErrorAs(t, err, &depErr)
	assert.Len(t, depErr.Failed(), 1)
	assert.Equal(t, "B", depErr.Failed()[0].ID)
	assert.Equal(t, 1, mock.Count("POST /api/v1/DeployIntegrationDesigntimeArtifact"))
}

// Multi-deploy: one artifact directory deployed as two artifact IDs (with a
// deployment prefix). In manifest mode both variants get the directory's
// Bundle-Version, keep "; singleton:=true" and deploy exactly that version.
func TestOrchestratorMultiDeployManifest(t *testing.T) {
	// not on the tenant yet (no designtime version); after the deployment the
	// runtime runs 1.0.16
	after := func() *cpitest.Artifact {
		return &cpitest.Artifact{TaskStatuses: []string{"SUCCESS"},
			AfterDeploy: []*cpitest.Runtime{{Version: "1.0.16", Status: "STARTED", DeployedOn: time.Now().Add(time.Minute)}}}
	}
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"DEV_Flow": after(), "DEV_Flow_Herrenberg": after()})
	root := t.TempDir()
	flow := filepath.Join(root, "packages", "EDM", "Flow")
	for name, content := range map[string]string{
		"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\r\nBundle-SymbolicName: Flow; singleton:=true\r\nBundle-Name: Flow\r\nBundle-Version: 1.0.16\r\nSAP-BundleType: IntegrationFlow\r\n\r\n",
		"src/main/resources/scenarioflows/integrationflow/Flow.iflw": "<bpmn2:definitions/>",
		"src/main/resources/parameters.prop":                         "Host=h\n",
	} {
		p := filepath.Join(flow, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	cfg := filepath.Join(root, "deploy.yml")
	require.NoError(t, os.WriteFile(cfg, []byte(`deploymentPrefix: DEV
packages:
  - integrationSuiteId: EDM
    packageDir: EDM
    artifacts:
      - {artifactId: Flow, artifactDir: Flow, type: IntegrationFlow}
      - {artifactId: Flow_Herrenberg, artifactDir: Flow, type: IntegrationFlow}
`), 0o644))
	r := runMain(t, append([]string{"orchestrator", "--packages-dir", filepath.Join(root, "packages"), "--deploy-config", cfg,
		"--versioning", "manifest", "--deploy-retries", "3", "--deploy-delay", "1", "--output", "json"}, basicAuth(mock)...)...)

	for _, id := range []string{"DEV_Flow", "DEV_Flow_Herrenberg"} {
		a := mock.Artifacts[id]
		require.NotNil(t, a, "%s created; stderr:\n%s", id, r.stderr)
		assert.Equal(t, "1.0.16", a.DesignVersion, "%s: the directory's Bundle-Version", id)
		assert.Equal(t, []string{"1.0.16"}, a.SavedVersions, id)
		mf := zipEntryCmd(t, a.Zip, "META-INF/MANIFEST.MF")
		assert.Contains(t, mf, "Bundle-SymbolicName: "+id+"; singleton:=true", id)
		assert.Contains(t, mf, "Bundle-Version: 1.0.16", id)
	}
	assert.Equal(t, 2, mock.Count("POST /api/v1/DeployIntegrationDesigntimeArtifact"), r.stderr)
	assert.Contains(t, r.stderr, `"rule":"manifest"`)
	assert.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, 2, strings.Count(r.stderr, `"status":"DEPLOYED","version":"1.0.16"`), r.stderr)
}

func zipEntryCmd(t *testing.T, data []byte, name string) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, name) {
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
