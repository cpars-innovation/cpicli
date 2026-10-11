package ops

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/models"
	"github.com/cpars-innovation/cpicli/pkg/versioning"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestLoadDeployConfig(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
		return p
	}
	single := write("010-partners.yml", `packages:
  - integrationSuiteId: Pkg
    artifacts:
      - {artifactId: Flow, artifactDir: Flow, type: IntegrationFlow}
      - {artifactId: Flow_B, artifactDir: Flow, type: IntegrationFlow, configOverrides: {Host: b, Port: 1}}
`)
	cfg, err := LoadDeployConfig(single)
	require.NoError(t, err)
	require.Len(t, cfg.Packages, 1)
	assert.True(t, cfg.Packages[0].Sync, "defaults apply")
	assert.True(t, cfg.Packages[0].Artifacts[0].Deploy)
	assert.Equal(t, "Flow_B", cfg.Packages[0].Artifacts[1].Id)

	// a folder is merged: each file's prefix goes into its IDs
	write("020-dev.yml", `deploymentPrefix: DEV
packages:
  - integrationSuiteId: Pkg
    artifacts:
      - {artifactId: Flow, artifactDir: Flow}
`)
	merged, err := LoadDeployConfig(dir)
	require.NoError(t, err)
	require.Len(t, merged.Packages, 2)
	assert.Equal(t, "Pkg", merged.Packages[0].ID)
	assert.Equal(t, "DEVPkg", merged.Packages[1].ID)
	assert.Equal(t, "DEV", merged.Packages[1].MergedPrefix)
	assert.Equal(t, "DEV_Flow", merged.Packages[1].Artifacts[0].Id)

	_, err = LoadDeployConfig(filepath.Join(dir, "nope.yml"))
	assert.Error(t, err)
}

// PlanOrchestration is orchestrator --plan: read only, per artifact what
// would be uploaded and deployed.
func TestPlanOrchestration(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{})
	seed := filepath.Join(t.TempDir(), "OrderIntake")
	writeIFlow(t, seed, "v1")
	_, err := UploadArtifact(mock.Executer(), UploadRequest{ID: "OrderIntake", Type: "Integration", PackageID: "Pkg", Dir: seed})
	require.NoError(t, err)
	mock.Artifacts["OrderIntake"].Runtime = &cpitest.Runtime{Version: "1.0.0", Status: "STARTED", DeployedOn: time.Now().Add(-time.Hour)}

	repo := t.TempDir()
	writeIFlow(t, filepath.Join(repo, "Pkg", "OrderIntake"), "v2")
	writeIFlow(t, filepath.Join(repo, "Pkg", "New"), "v1")
	var cfg DeployConfig
	require.NoError(t, yaml.Unmarshal([]byte(`packages:
  - integrationSuiteId: Pkg
    packageDir: Pkg
    artifacts:
      - {artifactId: OrderIntake, artifactDir: OrderIntake, type: IntegrationFlow}
      - {artifactId: New, artifactDir: New, type: IntegrationFlow}
      - {artifactId: Filtered, artifactDir: Filtered, type: IntegrationFlow}
`), &cfg))

	before := len(mock.Requests())
	plan, err := PlanOrchestration(context.Background(), mock.Executer(), repo, &cfg,
		OrchestratorOptions{ArtifactFilter: []string{"OrderIntake", "New"}})
	require.NoError(t, err)
	for _, req := range mock.Requests()[before:] {
		assert.Regexp(t, `^GET `, req, "plan only reads")
	}
	assert.Equal(t, 1, mock.Artifacts["OrderIntake"].Uploads, "only the seed")

	items := map[string]PlanItem{}
	for _, it := range plan {
		items[it.Artifact] = it
	}
	require.Len(t, items, 2)
	assert.Equal(t, "update", items["OrderIntake"].Upload)
	assert.True(t, items["OrderIntake"].Deploy, items["OrderIntake"].Reason)
	assert.Equal(t, "Pkg", items["OrderIntake"].Package)
	assert.Equal(t, "create", items["New"].Upload)
	assert.True(t, items["New"].Deploy)
	assert.Equal(t, "not deployed yet", items["New"].Reason)

	// update only: no deployment predicted
	plan, err = PlanOrchestration(context.Background(), mock.Executer(), repo, &cfg,
		OrchestratorOptions{Mode: OrchestratorUpdateOnly, ArtifactFilter: []string{"New"}})
	require.NoError(t, err)
	require.Len(t, plan, 1)
	assert.False(t, plan[0].Deploy)

	// a deployment prefix goes into the IDs
	plan, err = PlanOrchestration(context.Background(), mock.Executer(), repo, &cfg,
		OrchestratorOptions{DeploymentPrefix: "DEV", ArtifactFilter: []string{"New"}})
	require.NoError(t, err)
	require.Len(t, plan, 1)
	assert.Equal(t, "DEV_New", plan[0].Artifact)
	assert.Equal(t, "DEVPkg", plan[0].Package)

	_, err = PlanOrchestration(context.Background(), mock.Executer(), repo, &cfg, OrchestratorOptions{DraftHandling: "maybe"})
	assert.ErrorContains(t, err, "SKIP or ERROR")
}

func TestOrchestratorVersioningPerArtifact(t *testing.T) {
	var cfg models.DeployConfig
	require.NoError(t, yaml.Unmarshal([]byte(`packages:
  - integrationSuiteId: EDM
    deploy: true
    versioning: keep
    artifacts:
      - {artifactId: A, type: Integration, deploy: true}
      - {artifactId: B, artifactDir: B, type: Integration, deploy: true, versioning: manifest}
      - {artifactId: C, type: Integration, deploy: true, versioning: wrong}
`), &cfg))
	pkgDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(pkgDir, "B", "META-INF"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "B", "META-INF", "MANIFEST.MF"), []byte("Bundle-SymbolicName: B\nBundle-Version: 1.0.16\n"), 0o644))
	orc := &Orchestrator{Stats: NewOrchestratorStats(), opts: OrchestratorOptions{Versioning: versioning.TenantBump}}
	tasks := orc.collectDeploymentTasks(&cfg.Packages[0], pkgDir, "EDM", "")
	require.Len(t, tasks, 2)
	assert.Equal(t, versioning.Keep, tasks[0].Versioning, "package wins over the global mode")
	assert.Equal(t, versioning.Manifest, tasks[1].Versioning, "artifact wins over the package")
	assert.Equal(t, "1.0.16", tasks[1].ExpectedVersion, "manifest: deploy exactly the repository's version")
	assert.Empty(t, tasks[0].ExpectedVersion)
	assert.True(t, orc.Stats.FailedArtifactDeploys["C"])
	assert.Error(t, orc.Stats.Err())
}

func TestArtifactFilterSelectsCopies(t *testing.T) {
	pkgDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(pkgDir, "Folder", "META-INF"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "Folder", "META-INF", "MANIFEST.MF"),
		[]byte("Bundle-SymbolicName: SourceID; singleton:=true\nBundle-Version: 1.0.0\n"), 0o644))
	source := &models.Artifact{Id: "SourceID", ArtifactDir: "Folder"}
	copyA := &models.Artifact{Id: "Copy_A", ArtifactDir: "Folder"}
	other := &models.Artifact{Id: "Other", ArtifactDir: "OtherDir"}
	for _, tc := range []struct {
		filter []string
		want   [3]bool // source, copy, other
	}{
		{nil, [3]bool{true, true, true}},
		{[]string{"Folder"}, [3]bool{true, true, false}},
		{[]string{"SourceID"}, [3]bool{true, true, false}},
		{[]string{"Copy_A"}, [3]bool{false, true, false}},
		{[]string{"Other"}, [3]bool{false, false, true}},
		{[]string{"OtherDir"}, [3]bool{false, false, true}},
	} {
		got := [3]bool{artifactSelected(source, pkgDir, tc.filter), artifactSelected(copyA, pkgDir, tc.filter), artifactSelected(other, pkgDir, tc.filter)}
		assert.Equal(t, tc.want, got, "filter %v", tc.filter)
	}
}

func TestDesigntimeType(t *testing.T) {
	for in, want := range map[string]string{"IntegrationFlow": "Integration", "Value Mapping": "ValueMapping",
		"messagemapping": "MessageMapping", "ScriptCollection": "ScriptCollection", "": "Integration"} {
		assert.Equal(t, want, DesigntimeType(in), in)
	}
}

// A configure config built in memory (pkg/models) works with ConfigDiff.
func TestConfigDiffInMemory(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", DesignVersion: "1", Package: "Pkg", Parameters: map[string]string{"Host": "old", "Port": "443"}},
	})
	cfg := &models.ConfigureConfig{Packages: []models.ConfigurePackage{{ID: "Pkg", Artifacts: []models.ConfigureArtifact{{
		ID: "A", Type: "Integration", Version: "active",
		Parameters: []models.ConfigurationParameter{{Key: "Host", Value: "new"}, {Key: "Port", Value: "443"}},
	}}}}}
	res, err := ConfigDiff(mock.Executer(), cfg, ConfigFilter{})
	require.NoError(t, err)
	changes := map[string]string{}
	for _, it := range res.Items {
		changes[it.Key] = it.Change
	}
	assert.Equal(t, map[string]string{"Host": ConfigUpdate, "Port": ConfigUnchanged}, changes)
}
