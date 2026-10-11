package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// draftRepo is a repository with the flows A1 (also deployed as the copy
// A1_Copy) and D, and a mock tenant where D and the copy are in draft.
func draftRepo(t *testing.T) (*cpitest.Tenant, string, string) {
	t.Helper()
	flow := func(id, version string) *cpitest.Artifact {
		return &cpitest.Artifact{Type: "Integration", DesignVersion: version, Package: "Pkg", Name: id,
			Zip: namedFlowZip(t, id, "v1"), Parameters: map[string]string{"Host": "tenant"},
			Runtime: &cpitest.Runtime{Version: "1.0.5", Status: "STARTED"}}
	}
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"A1":      flow("A1", "1.0.5"),
		"A1_Copy": flow("A1_Copy", "Active"), // edited in the Web UI
		"D":       flow("D", "Active"),
	})
	mock.Artifacts["A1"].AfterDeploy = []*cpitest.Runtime{{Version: "1.0.6", Status: "STARTED"}}
	mock.Packages = []cpitest.Package{{ID: "Pkg", Version: "1.0.0"}}
	repo := t.TempDir()
	for _, id := range []string{"A1", "D"} {
		dir := filepath.Join(repo, "Pkg", id)
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "META-INF"), 0o755))
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "src", "main", "resources", "scenarioflows", "integrationflow"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "META-INF", "MANIFEST.MF"),
			[]byte("Manifest-Version: 1.0\nBundle-SymbolicName: "+id+"; singleton:=true\nBundle-Name: "+id+"\nBundle-Version: 1.0.6\nSAP-BundleType: IntegrationFlow\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "main", "resources", "scenarioflows", "integrationflow", id+".iflw"), []byte("<bpmn2:definitions/>"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "main", "resources", "parameters.prop"), []byte("Host=repo\n"), 0o644))
	}
	cfg := filepath.Join(t.TempDir(), "deploy.yml")
	require.NoError(t, os.WriteFile(cfg, []byte(`packages:
  - integrationSuiteId: Pkg
    packageDir: Pkg
    artifacts:
      - {artifactId: A1, artifactDir: A1, type: IntegrationFlow}
      - {artifactId: A1_Copy, artifactDir: A1, type: IntegrationFlow, configOverrides: {Host: partner}}
      - {artifactId: D, artifactDir: D, type: IntegrationFlow}
`), 0o644))
	return mock, repo, cfg
}

func orchestrateJSON(t *testing.T, mock *cpitest.Tenant, repo, cfg string, wantCode int, extra ...string) (orchestratorResult, cliRun) {
	t.Helper()
	args := append([]string{"orchestrator", "--packages-dir", repo, "--deploy-config", cfg, "--snapshot-state", "off",
		"--versioning", "manifest", "--deploy-delay", "1", "--deploy-retries", "2", "--output", "json"}, extra...)
	r := runMainKeepEnv(t, append(args, basicAuth(mock)...)...)
	require.Equal(t, wantCode, r.code, r.stderr)
	var env struct {
		Result orchestratorResult `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &env), r.stdout)
	return env.Result, r
}

func statusOf(res orchestratorResult, id string) ArtifactStatus {
	for _, a := range res.Artifacts {
		if a.ID == id {
			return a
		}
	}
	return ArtifactStatus{}
}

func TestOrchestratorSkipsDrafts(t *testing.T) {
	mock, repo, cfg := draftRepo(t)

	// the plan predicts the skips and does not fail
	res, r := orchestrateJSON(t, mock, repo, cfg, 0, "--plan")
	plan := map[string]PlanItem{}
	for _, it := range res.Plan {
		plan[it.Artifact] = it
	}
	for _, id := range []string{"D", "A1_Copy"} {
		assert.Equal(t, StatusSkippedDraft, plan[id].Upload, id)
		assert.False(t, plan[id].Deploy, id)
		assert.Empty(t, plan[id].Error, id)
	}
	assert.Equal(t, "update", plan["A1"].Upload, "the source is handled on its own")
	assert.Contains(t, r.stderr, "upload: skipped-draft no deploy")
	assert.Contains(t, r.stderr, "D is in draft on the tenant: not uploaded, not deployed")
	assert.Equal(t, 2, res.Counts["skippedDraft"])
	assert.Equal(t, 0, mock.Artifacts["A1"].Uploads, "plan writes nothing")

	// update and deploy: the drafts are left alone, the run succeeds
	summary := filepath.Join(t.TempDir(), "summary.md")
	res, r = orchestrateJSON(t, mock, repo, cfg, 0, "--summary", summary)
	for _, id := range []string{"D", "A1_Copy"} {
		assert.Equal(t, 0, mock.Artifacts[id].Uploads, id)
		assert.Equal(t, 0, mock.Artifacts[id].Deploys, id)
		st := statusOf(res, id)
		assert.Equal(t, StatusSkippedDraft, st.Status, id)
		assert.Equal(t, "1.0.6", st.LocalVersion, id)
		assert.Empty(t, st.Deploy, id)
	}
	assert.Equal(t, 1, mock.Artifacts["A1"].Uploads)
	assert.Equal(t, 1, mock.Artifacts["A1"].Deploys)
	assert.Equal(t, StatusUpdated, statusOf(res, "A1").Status)
	assert.Equal(t, "DEPLOYED", statusOf(res, "A1").Deploy)
	assert.Equal(t, 2, res.Counts["skippedDraft"])
	assert.Equal(t, 1, res.Counts["updated"])
	assert.Equal(t, 1, res.Counts["deployed"])
	assert.Equal(t, 2, res.Stats.ArtifactsSkippedDraft)
	require.Len(t, res.Stats.SkippedDrafts, 2)
	assert.Equal(t, DraftSkip{ID: "A1_Copy", Package: "Pkg", Designtime: "Active", Running: "1.0.5", LocalVersion: "1.0.6"}, res.Stats.SkippedDrafts[0])
	assert.Contains(t, r.stderr, "Skipped (draft):         2")
	md, err := os.ReadFile(summary)
	require.NoError(t, err)
	assert.Contains(t, string(md), "**Skipped: in draft on the tenant** (2")
	assert.Contains(t, string(md), "| D | Pkg | Active | 1.0.5 | 1.0.6 |")

	// deploy only: checked without an upload
	res, _ = orchestrateJSON(t, mock, repo, cfg, 0, "--deploy-only")
	assert.Equal(t, StatusSkippedDraft, statusOf(res, "D").Status)
	assert.Equal(t, StatusNotUploaded, statusOf(res, "A1").Status)
	assert.Equal(t, 0, mock.Artifacts["D"].Deploys)

	// ERROR: today's behaviour, exit 5
	_, r = orchestrateJSON(t, mock, repo, cfg, 5, "--draft-handling", "ERROR")
	assert.Contains(t, r.stderr, "in draft on the tenant (--draft-handling ERROR)")
	_, _ = orchestrateJSON(t, mock, repo, cfg, 5, "--fail-on-draft", "--plan")
	_, _ = orchestrateJSON(t, mock, repo, cfg, 5, "--deploy-only", "--draft-handling", "ERROR")
	r = runMainWith(t, func() { t.Setenv("CPICTL_DRAFT_HANDLING", "ERROR") }, append([]string{"orchestrator", "--packages-dir", repo,
		"--deploy-config", cfg, "--snapshot-state", "off", "--plan"}, basicAuth(mock)...)...)
	assert.Equal(t, 5, r.code, "CPICTL_DRAFT_HANDLING")
	assert.Equal(t, 0, mock.Artifacts["D"].Uploads+mock.Artifacts["D"].Deploys, "never touched")
}

func TestOrchestratorDraftOnlyOnCopy(t *testing.T) {
	mock, repo, cfg := draftRepo(t)
	mock.Artifacts["D"].DesignVersion = "1.0.5" // saved again
	res, _ := orchestrateJSON(t, mock, repo, cfg, 0, "--artifact-filter", "A1")
	assert.Equal(t, StatusSkippedDraft, statusOf(res, "A1_Copy").Status, "the folder name selects the copy too")
	assert.Equal(t, StatusUpdated, statusOf(res, "A1").Status)
	assert.Empty(t, statusOf(res, "D").Status, "filtered")
	assert.Equal(t, 1, res.Counts["skippedDraft"])
}
