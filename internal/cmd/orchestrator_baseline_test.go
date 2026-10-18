package cmd

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/file"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// namedFlowZip is a flow as the tenant exports it (with Bundle-Name).
func namedFlowZip(t *testing.T, id, script string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range map[string]string{
		"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\r\nBundle-SymbolicName: " + id + "; singleton:=true\r\nBundle-Name: " + id + "\r\nBundle-Version: 1.0.0\r\nSAP-BundleType: IntegrationFlow\r\n\r\n",
		"src/main/resources/scenarioflows/integrationflow/" + id + ".iflw": "<bpmn2:definitions/>",
		"src/main/resources/script/s.groovy":                               script,
		"src/main/resources/parameters.prop":                               "Host=tenant\n",
	} {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, _ = w.Write([]byte(content))
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// The pipeline: snapshot, copy the managed parts over, orchestrator. The
// orchestrator compares with the snapshot state instead of downloading.
func TestOrchestratorComparesWithSnapshot(t *testing.T) {
	modified := time.Now().Add(-time.Hour).Truncate(time.Second)
	flow := func(id string) *cpitest.Artifact {
		return &cpitest.Artifact{Type: "Integration", DesignVersion: "1.0.5", Package: "Pkg", Name: id, ModifiedAt: modified,
			Zip: namedFlowZip(t, id, "v1"), Parameters: map[string]string{"Host": "tenant"}}
	}
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A1": flow("A1"), "A2": flow("A2"), "A3": flow("A3")})
	mock.Packages = []cpitest.Package{{ID: "Pkg", Version: "1.0.0"}}
	repo := t.TempDir()

	r := runMain(t, append([]string{"snapshot", "--dir-git-repo", repo, "--dir-work", t.TempDir(), "--git-skip-commit",
		"--sync-package-details=false"}, basicAuth(mock)...)...)
	require.Equal(t, 0, r.code, r.stderr)
	stateFile := filepath.Join(repo, ".cpi", "snapshot-state.json")
	require.FileExists(t, stateFile)

	// managed part changed in the repository: A2's script
	require.NoError(t, os.WriteFile(filepath.Join(repo, "Pkg", "A2", "src", "main", "resources", "script", "s.groovy"), []byte("v2"), 0o644))
	// parameters.prop differs from the tenant: not part of the comparison
	require.NoError(t, os.WriteFile(filepath.Join(repo, "Pkg", "A1", "src", "main", "resources", "parameters.prop"), []byte("Host=repo\n"), 0o644))

	cfg := filepath.Join(t.TempDir(), "deploy.yml")
	require.NoError(t, os.WriteFile(cfg, []byte(`packages:
  - integrationSuiteId: Pkg
    packageDir: Pkg
    artifacts:
      - {artifactId: A1, artifactDir: A1, type: IntegrationFlow}
      - {artifactId: A2, artifactDir: A2, type: IntegrationFlow}
      - {artifactId: A3, artifactDir: A3, type: IntegrationFlow}
`), 0o644))
	downloads := func(id string) int {
		return mock.Count("GET /api/v1/IntegrationDesigntimeArtifacts(Id='" + id + "',Version='active')/$value")
	}
	orchestrate := func(extra ...string) orchestratorResult {
		t.Helper()
		args := append([]string{"orchestrator", "--packages-dir", repo, "--deploy-config", cfg, "--update-only", "--output", "json"}, extra...)
		r := runMain(t, append(args, basicAuth(mock)...)...)
		require.Equal(t, 0, r.code, r.stderr)
		var env struct {
			Result orchestratorResult `json:"result"`
		}
		require.NoError(t, json.Unmarshal([]byte(r.stdout), &env))
		return env.Result
	}
	for _, id := range []string{"A1", "A2", "A3"} {
		require.Equal(t, 1, downloads(id), "snapshot downloaded %s", id)
	}

	res := orchestrate()
	for _, id := range []string{"A1", "A2", "A3"} {
		assert.Equal(t, 1, downloads(id), "%s not downloaded again", id)
	}
	assert.Equal(t, 0, mock.Artifacts["A1"].Uploads, "A1 unchanged (parameters.prop is not compared)")
	assert.Equal(t, 1, mock.Artifacts["A2"].Uploads, "A2 changed")
	assert.Equal(t, 0, mock.Artifacts["A3"].Uploads)
	assert.Equal(t, 3, res.Stats.ComparedWithSnapshot)
	assert.Equal(t, 0, res.Stats.DownloadedForComparison)
	assert.Equal(t, 1, res.Stats.ArtifactsChanged)
	assert.Equal(t, 2, res.Stats.ArtifactsUnchanged)

	// A2 changed on the tenant (our upload), A3 edited in the Web UI: both
	// downloaded; A1 still compared with the snapshot
	mock.Artifacts["A3"].ModifiedAt = time.Now()
	res = orchestrate()
	assert.Equal(t, 1, downloads("A1"))
	assert.Equal(t, 2, downloads("A2"), "changed since the snapshot (by the upload)")
	assert.Equal(t, 2, downloads("A3"), "edited since the snapshot")
	assert.Equal(t, 1, mock.Artifacts["A2"].Uploads, "the download shows A2 has the content")
	assert.Equal(t, 0, mock.Artifacts["A3"].Uploads)
	assert.Equal(t, 1, res.Stats.ComparedWithSnapshot)
	assert.Equal(t, 2, res.Stats.DownloadedForComparison)

	// --verify-download and --snapshot-state off always download
	orchestrate("--verify-download")
	assert.Equal(t, 2, downloads("A1"))
	orchestrate("--snapshot-state", "off")
	assert.Equal(t, 3, downloads("A1"))

	// a state of another tenant is not used
	data, err := os.ReadFile(stateFile)
	require.NoError(t, err)
	var state map[string]any
	require.NoError(t, json.Unmarshal(data, &state))
	assert.NotEmpty(t, state["tenant"])
	state["tenant"] = "other.example.com"
	data, _ = json.Marshal(state)
	require.NoError(t, os.WriteFile(stateFile, data, 0o644))
	res = orchestrate()
	assert.Equal(t, 4, downloads("A1"))
	assert.Equal(t, 0, res.Stats.ComparedWithSnapshot)

	// an explicit state file that does not exist is a usage error
	r = runMain(t, append([]string{"orchestrator", "--packages-dir", repo, "--deploy-config", cfg, "--update-only",
		"--snapshot-state", filepath.Join(repo, "nope.json")}, basicAuth(mock)...)...)
	assert.Equal(t, 2, r.code)
}

// --plan writes nothing and says per artifact what would be uploaded and
// deployed, and why.
func TestOrchestratorPlan(t *testing.T) {
	modified := time.Now().Add(-time.Hour).Truncate(time.Second)
	flow := func(id string, rt *cpitest.Runtime) *cpitest.Artifact {
		return &cpitest.Artifact{Type: "Integration", DesignVersion: "1.0.5", Package: "Pkg", Name: id, ModifiedAt: modified,
			Zip: namedFlowZip(t, id, "v1"), Runtime: rt}
	}
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"Same":    flow("Same", &cpitest.Runtime{Version: "1.0.5", Status: "STARTED"}),
		"Changed": flow("Changed", &cpitest.Runtime{Version: "1.0.5", Status: "STARTED"}),
		"Older":   flow("Older", &cpitest.Runtime{Version: "1.0.4", Status: "STARTED"}),
		"Error":   flow("Error", &cpitest.Runtime{Version: "1.0.5", Status: "ERROR"}),
		"Idle":    flow("Idle", nil),
		"New":     {Type: "Integration"},
	})
	mock.Packages = []cpitest.Package{{ID: "Pkg", Version: "1.0.0"}}
	repo := t.TempDir()
	r := runMain(t, append([]string{"snapshot", "--dir-git-repo", repo, "--dir-work", t.TempDir(), "--git-skip-commit",
		"--sync-package-details=false"}, basicAuth(mock)...)...)
	require.Equal(t, 0, r.code, r.stderr)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "Pkg", "Changed", "src", "main", "resources", "script", "s.groovy"), []byte("v2"), 0o644))
	// a new flow, only in the repository
	newDir := filepath.Join(repo, "Pkg", "New")
	require.NoError(t, os.MkdirAll(newDir, 0o755))
	zipPath := filepath.Join(t.TempDir(), "new.zip")
	require.NoError(t, os.WriteFile(zipPath, namedFlowZip(t, "New", "v1"), 0o644))
	require.NoError(t, file.UnzipSource(zipPath, newDir))

	var artifacts string
	for _, id := range []string{"Same", "Changed", "Older", "Error", "Idle", "New"} {
		artifacts += "      - {artifactId: " + id + ", artifactDir: " + id + ", type: IntegrationFlow}\n"
	}
	cfg := filepath.Join(t.TempDir(), "deploy.yml")
	require.NoError(t, os.WriteFile(cfg, []byte("packages:\n  - integrationSuiteId: Pkg\n    packageDir: Pkg\n    artifacts:\n"+artifacts), 0o644))

	before := len(mock.Requests())
	r = runMain(t, append([]string{"orchestrator", "--packages-dir", repo, "--deploy-config", cfg, "--plan", "--output", "json"}, basicAuth(mock)...)...)
	require.Equal(t, 0, r.code, r.stderr)
	for _, req := range mock.Requests()[before:] {
		assert.Regexp(t, `^GET `, req, "plan only reads")
	}
	var env struct {
		Result orchestratorResult `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &env))
	plan := map[string]PlanItem{}
	for _, it := range env.Result.Plan {
		plan[it.Artifact] = it
	}
	require.Len(t, plan, 6)
	assert.Equal(t, "unchanged", plan["Same"].Upload)
	assert.False(t, plan["Same"].Deploy, plan["Same"].Reason)
	assert.Equal(t, "update", plan["Changed"].Upload)
	assert.True(t, plan["Changed"].Deploy)
	assert.Contains(t, plan["Changed"].Reason, "content changed, same version")
	assert.True(t, plan["Older"].Deploy)
	assert.Contains(t, plan["Older"].Reason, "running 1.0.4")
	assert.True(t, plan["Error"].Deploy)
	assert.Contains(t, plan["Error"].Reason, "ERROR")
	assert.True(t, plan["Idle"].Deploy)
	assert.Equal(t, "not deployed yet", plan["Idle"].Reason)
	assert.Equal(t, "create", plan["New"].Upload)
	assert.True(t, plan["New"].Deploy)
	for _, it := range plan {
		assert.Equal(t, 0, mock.Artifacts[it.Artifact].Uploads)
	}

	// versioning manifest: same Bundle-Version with new content is refused
	r = runMain(t, append([]string{"orchestrator", "--packages-dir", repo, "--deploy-config", cfg, "--plan", "--versioning", "manifest",
		"--artifact-filter", "Changed"}, basicAuth(mock)...)...)
	assert.Equal(t, 5, r.code, r.stderr)
	assert.Contains(t, r.stderr, "raise Bundle-Version")
}

// Uploads run --parallel at a time across all packages.
func TestOrchestratorParallelUploads(t *testing.T) {
	arts := map[string]*cpitest.Artifact{}
	var cfg strings.Builder
	cfg.WriteString("packages:\n")
	repo := t.TempDir()
	for p := 0; p < 4; p++ {
		pkg := fmt.Sprintf("P%d", p)
		fmt.Fprintf(&cfg, "  - integrationSuiteId: %s\n    packageDir: %s\n    artifacts:\n", pkg, pkg)
		for i := 0; i < 4; i++ {
			id := fmt.Sprintf("%s_F%d", pkg, i)
			arts[id] = &cpitest.Artifact{Type: "Integration", DesignVersion: "1.0.0", Package: pkg, Name: id, Zip: namedFlowZip(t, id, "v1")}
			fmt.Fprintf(&cfg, "      - {artifactId: %s, artifactDir: %s, type: IntegrationFlow}\n", id, id)
			zipPath := filepath.Join(t.TempDir(), id+".zip")
			require.NoError(t, os.WriteFile(zipPath, namedFlowZip(t, id, "v2"), 0o644))
			require.NoError(t, file.UnzipSource(zipPath, filepath.Join(repo, pkg, id)))
		}
	}
	mock := cpitest.NewTenant(t, arts)
	mock.DownloadLatency = 100 * time.Millisecond
	cfgPath := filepath.Join(t.TempDir(), "deploy.yml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfg.String()), 0o644))

	r := runMain(t, append([]string{"orchestrator", "--packages-dir", repo, "--deploy-config", cfgPath, "--update-only",
		"--snapshot-state", "off", "--parallel", "8", "--output", "json"}, basicAuth(mock)...)...)
	require.Equal(t, 0, r.code, r.stderr)
	assert.Contains(t, r.stdout, `"artifactsChanged": 16`)
	for id, a := range mock.Artifacts {
		assert.Equal(t, 1, a.Uploads, id)
	}
	// the peak proves the parallelism; a wall-clock bound would flake on a
	// loaded runner
	assert.Equal(t, int64(8), mock.PeakDownloads(), "8 at a time across packages")
}
