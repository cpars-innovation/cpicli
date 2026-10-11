package ops

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/snapshot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An upload with the snapshot state as baseline compares without a
// download; KeepRuntime never undeploys the running artifact.
func TestUploadWithBaselineAndKeepRuntime(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{})
	seed := filepath.Join(t.TempDir(), "OrderIntake")
	writeIFlow(t, seed, "v1")
	_, err := UploadArtifact(mock.Executer(), UploadRequest{ID: "OrderIntake", Type: "Integration", PackageID: "Orders", Dir: seed})
	require.NoError(t, err)
	mock.Packages = []cpitest.Package{{ID: "Orders", Version: "1.0.0"}}
	a := mock.Artifacts["OrderIntake"]
	a.Package, a.Name, a.DesignVersion = "Orders", "OrderIntake", "1.0.0"
	a.ModifiedAt = time.Now().Add(-time.Hour).Truncate(time.Second)
	a.Runtime = &cpitest.Runtime{Version: "1.0.0", Status: "STARTED", DeployedOn: time.Now().Add(-time.Hour)}

	repo := t.TempDir()
	snap, err := snapshot.Take(context.Background(), mock.Executer(), repo, snapshot.Options{})
	require.NoError(t, err)
	dir := filepath.Join(repo, "Orders", "OrderIntake")
	downloads := func() int {
		return mock.Count("GET /api/v1/IntegrationDesigntimeArtifacts(Id='OrderIntake',Version='active')/$value")
	}
	before := downloads()

	// unchanged: compared with the state, nothing downloaded
	res, err := UploadArtifact(mock.Executer(), UploadRequest{ID: "OrderIntake", Type: "Integration", PackageID: "Orders", Dir: dir, Baseline: snap.State})
	require.NoError(t, err)
	assert.Equal(t, "UNCHANGED", res.Action)
	assert.Equal(t, "snapshot", res.Compared)
	assert.Equal(t, before, downloads())

	// changed with the running version: kept running, to be deployed again
	require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "main", "resources", "script", "script1.groovy"), []byte("v2"), 0o644))
	res, err = UploadArtifact(mock.Executer(), UploadRequest{ID: "OrderIntake", Type: "Integration", PackageID: "Orders", Dir: dir,
		Baseline: snap.State, KeepRuntime: true})
	require.NoError(t, err)
	assert.Equal(t, "UPDATED", res.Action)
	assert.Equal(t, "snapshot", res.Compared)
	assert.True(t, res.Redeploy)
	assert.False(t, res.RuntimeUndeployed)
	assert.Equal(t, before, downloads())
	assert.Zero(t, mock.Count("DELETE /api/v1/IntegrationRuntimeArtifacts"), "the runtime is not undeployed")

	// the tenant changed since the snapshot: downloaded; without KeepRuntime undeployed
	a.ModifiedAt = time.Now()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "main", "resources", "script", "script1.groovy"), []byte("v3"), 0o644))
	res, err = UploadArtifact(mock.Executer(), UploadRequest{ID: "OrderIntake", Type: "Integration", PackageID: "Orders", Dir: dir, Baseline: snap.State})
	require.NoError(t, err)
	assert.Equal(t, "download", res.Compared)
	assert.Contains(t, res.CompareReason, "changed on the tenant since the snapshot")
	assert.Equal(t, "UPDATED", res.Action)
	assert.True(t, res.RuntimeUndeployed)
	assert.Equal(t, 1, mock.Count("DELETE /api/v1/IntegrationRuntimeArtifacts"))
}
