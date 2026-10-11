package ops

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/sync"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/manifest"
	"github.com/cpars-innovation/cpicli/pkg/versioning"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// versionedTenant has OrderIntake on the tenant (designtime and runtime
// versions as given, content "v1") and returns the local directory with the
// repository version and content.
func versionedTenant(t *testing.T, designtime, runtime, repoVersion, script string) (*cpitest.Tenant, string) {
	t.Helper()
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{})
	seed := filepath.Join(t.TempDir(), "OrderIntake")
	writeIFlow(t, seed, "v1")
	_, err := UploadArtifact(mock.Executer(), UploadRequest{ID: "OrderIntake", Type: "Integration", PackageID: "Orders", Dir: seed})
	require.NoError(t, err)
	a := mock.Artifacts["OrderIntake"]
	a.DesignVersion = designtime
	a.Runtime = &cpitest.Runtime{Version: runtime, Status: "STARTED", DeployedOn: time.Now().Add(-time.Hour)}
	a.TaskStatuses = []string{"SUCCESS"}

	dir := filepath.Join(t.TempDir(), "OrderIntake")
	writeIFlow(t, dir, script)
	require.NoError(t, manifest.SetVersion(dir, repoVersion))
	return mock, dir
}

func uploadAndDeploy(t *testing.T, mock *cpitest.Tenant, dir string, mode versioning.Mode) (*UploadResult, Result) {
	t.Helper()
	up, err := UploadArtifact(mock.Executer(), UploadRequest{ID: "OrderIntake", Type: "Integration", PackageID: "Orders", Dir: dir, Versioning: mode})
	require.NoError(t, err)
	a := mock.Artifacts["OrderIntake"]
	a.AfterDeploy = []*cpitest.Runtime{{Version: a.DesignVersion, Status: "STARTED", DeployedOn: time.Now().Add(time.Minute)}}
	opts := fastOpts()
	opts.Versioning = mode
	res := Deploy(context.Background(), NewTenant(mock.Executer()), []Artifact{{ID: "OrderIntake", Type: "Integration"}}, opts)
	require.Len(t, res, 1)
	return up, res[0]
}

func TestVersioningManifest(t *testing.T) {
	t.Run("1.0.16 from git over runtime 1.0.15", func(t *testing.T) {
		mock, dir := versionedTenant(t, "1.0.15", "1.0.15", "1.0.16", "v2")
		up, res := uploadAndDeploy(t, mock, dir, versioning.Manifest)
		assert.Equal(t, "UPDATED", up.Action)
		assert.Equal(t, "1.0.16", up.Version)
		assert.True(t, up.VersionSet, "the tenant kept its own version: set explicitly")
		assert.Contains(t, up.VersionReason, "manifest")
		assert.Equal(t, []string{"1.0.16"}, mock.Artifacts["OrderIntake"].SavedVersions)
		assert.False(t, up.RuntimeUndeployed)
		assert.Equal(t, StatusDeployed, res.Status, res.Error)
		assert.Equal(t, "1.0.16", res.Version)
		assert.Equal(t, "manifest", res.Versioning)
	})
	t.Run("1.0.13 from git over runtime 1.0.15 fails with the guard", func(t *testing.T) {
		mock, dir := versionedTenant(t, "1.0.15", "1.0.15", "1.0.13", "v2")
		up, res := uploadAndDeploy(t, mock, dir, versioning.Manifest)
		assert.Equal(t, "1.0.13", up.Version)
		// the upload made the designtime newer than the deployment: in manifest
		// mode that does not count, the version decides
		assert.True(t, mock.Artifacts["OrderIntake"].ModifiedAt.After(mock.Artifacts["OrderIntake"].Runtime.DeployedOn))
		assert.Equal(t, StatusFailed, res.Status)
		assert.Contains(t, res.Error, "designtime version 1.0.13 is lower than running 1.0.15 (versioning manifest")
		assert.Equal(t, 0, mock.Count("POST /api/v1/DeployIntegrationDesigntimeArtifact"))
	})
	t.Run("only the version differs: no upload, version set", func(t *testing.T) {
		mock, dir := versionedTenant(t, "1.0.15", "1.0.15", "1.0.16", "v1")
		up, err := UploadArtifact(mock.Executer(), UploadRequest{ID: "OrderIntake", Type: "Integration", PackageID: "Orders", Dir: dir, Versioning: versioning.Manifest})
		require.NoError(t, err)
		assert.Equal(t, "UNCHANGED", up.Action, "the content comparison ignores Bundle-Version")
		assert.Equal(t, "1.0.16", mock.Artifacts["OrderIntake"].DesignVersion)
		assert.Equal(t, 1, mock.Artifacts["OrderIntake"].Uploads, "only the seed upload")
	})
	t.Run("no Bundle-Version", func(t *testing.T) {
		mock, dir := versionedTenant(t, "1.0.15", "1.0.15", "", "v2")
		_, err := UploadArtifact(mock.Executer(), UploadRequest{ID: "OrderIntake", Type: "Integration", PackageID: "Orders", Dir: dir, Versioning: versioning.Manifest})
		assert.ErrorContains(t, err, "no Bundle-Version")
	})
}

func TestVersioningKeepAndTenantBump(t *testing.T) {
	t.Run("keep: tenant version, no guard", func(t *testing.T) {
		mock, dir := versionedTenant(t, "1.0.13", "1.0.15", "1.0.0", "v2")
		up, res := uploadAndDeploy(t, mock, dir, versioning.Keep)
		assert.Equal(t, "1.0.13", up.Version)
		assert.Empty(t, mock.Artifacts["OrderIntake"].SavedVersions)
		assert.Equal(t, StatusDeployed, res.Status, res.Error)
		assert.Equal(t, RuleKeep, res.Rule)
	})
	t.Run("tenant-bump: max(designtime, runtime)+1", func(t *testing.T) {
		mock, dir := versionedTenant(t, "1.0.14", "1.0.15", "1.0.0", "v2")
		up, res := uploadAndDeploy(t, mock, dir, versioning.TenantBump)
		assert.Equal(t, "1.0.16", up.Version)
		assert.Contains(t, up.VersionReason, "max(designtime 1.0.14, runtime 1.0.15)+1")
		assert.Equal(t, StatusDeployed, res.Status, res.Error)
		assert.Equal(t, "1.0.16", res.Version)
	})
	t.Run("tenant-bump: unchanged content keeps the version", func(t *testing.T) {
		mock, dir := versionedTenant(t, "1.0.14", "1.0.15", "1.0.0", "v1")
		up, err := UploadArtifact(mock.Executer(), UploadRequest{ID: "OrderIntake", Type: "Integration", PackageID: "Orders", Dir: dir, Versioning: versioning.TenantBump})
		require.NoError(t, err)
		assert.Equal(t, "UNCHANGED", up.Action)
		assert.Equal(t, "1.0.14", up.Version)
	})
	t.Run("not an integration flow: the version cannot be set", func(t *testing.T) {
		mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"SC": {Type: "ScriptCollection", DesignVersion: "1.0.0"}})
		err := cpi.SaveAsVersion(mock.Executer(), "ScriptCollection", "SC", "1.0.1")
		assert.ErrorContains(t, err, "only integration flows")
		err = cpi.SaveAsVersion(mock.Executer(), "Integration", "Unknown", "1.0.1")
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "only integration flows", "a missing flow is a plain API error")
	})
}

// Export (sync to Git) keeps the repository's Bundle-Version: the tenant's
// download says 1.0.0. A higher designtime version (saved in the Web UI) wins.
func TestExportKeepsRepositoryVersion(t *testing.T) {
	files := testIFlowFiles("Orders_In") // Bundle-Version 1.0.3 in the zip
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"Orders_In": {Type: "Integration", DesignVersion: "1.0.15", Package: "Sales", Zip: zipFiles(t, files)},
	})
	mock.Packages = []cpitest.Package{{ID: "Sales"}}
	repo := t.TempDir()
	art := filepath.Join(repo, "Orders_In")
	writeFlow(t, art, files)
	require.NoError(t, manifest.SetVersion(art, "1.0.15"))

	syncer := sync.New(mock.Executer())
	require.NoError(t, syncer.ArtifactsToGit("Sales", t.TempDir(), repo, nil, nil, "SKIP", "ID", nil))
	assert.Equal(t, "1.0.15", versionOf(t, art), "not overwritten with the download's version")

	mock.Artifacts["Orders_In"].DesignVersion = "1.0.17"
	require.NoError(t, syncer.ArtifactsToGit("Sales", t.TempDir(), repo, nil, nil, "SKIP", "ID", nil))
	assert.Equal(t, "1.0.17", versionOf(t, art), "a higher designtime version is taken")

	// download writes the designtime version as well
	dl := filepath.Join(t.TempDir(), "dl")
	_, err := DownloadArtifactToDir(mock.Executer(), "Integration", "Orders_In", "", dl, false)
	require.NoError(t, err)
	assert.Equal(t, "1.0.17", versionOf(t, dl))
}

func TestVersioningManifestEdgeCases(t *testing.T) {
	upload := func(mock *cpitest.Tenant, dir string) (*UploadResult, error) {
		return UploadArtifact(mock.Executer(), UploadRequest{ID: "OrderIntake", Type: "Integration", PackageID: "Orders", Dir: dir, Versioning: versioning.Manifest})
	}
	t.Run("same version, different content: bump requested, nothing written", func(t *testing.T) {
		for _, tc := range []struct{ designtime, runtime string }{{"1.0.15", "1.0.14"}, {"1.0.14", "1.0.15"}} {
			mock, dir := versionedTenant(t, tc.designtime, tc.runtime, "1.0.15", "v2")
			_, err := upload(mock, dir)
			require.Error(t, err, tc)
			assert.Contains(t, err.Error(), "equals the tenant's version")
			assert.Contains(t, err.Error(), "cpictl version bump --changed")
			assert.Equal(t, 1, mock.Artifacts["OrderIntake"].Uploads, "only the seed upload")
			assert.Empty(t, mock.Artifacts["OrderIntake"].SavedVersions)
		}
	})
	t.Run("order: content, version, verification", func(t *testing.T) {
		mock, dir := versionedTenant(t, "1.0.13", "1.0.15", "1.0.16", "v2")
		before := len(mock.Requests())
		_, err := upload(mock, dir)
		require.NoError(t, err)
		var seq []string
		for _, r := range mock.Requests()[before:] {
			switch {
			case r == "PUT /api/v1/IntegrationDesigntimeArtifacts(Id='OrderIntake',Version='active')":
				seq = append(seq, "content")
			case r == "POST /api/v1/IntegrationDesigntimeArtifactSaveAsVersion":
				seq = append(seq, "version")
			case r == "GET /api/v1/IntegrationDesigntimeArtifacts(Id='OrderIntake',Version='active')" && len(seq) > 0 && seq[len(seq)-1] == "version":
				seq = append(seq, "verify")
			}
		}
		assert.Equal(t, []string{"content", "version", "verify"}, seq)
	})
	t.Run("lower than the designtime version: accepted and verified", func(t *testing.T) {
		mock, dir := versionedTenant(t, "1.0.20", "1.0.15", "1.0.16", "v2")
		up, err := upload(mock, dir)
		require.NoError(t, err)
		assert.Equal(t, "1.0.16", up.Version)
		assert.Equal(t, "manifest", up.VersionRule)
	})
	t.Run("lower than the designtime version: refused by the tenant", func(t *testing.T) {
		mock, dir := versionedTenant(t, "1.0.20", "1.0.15", "1.0.16", "v2")
		mock.Artifacts["OrderIntake"].SaveAsVersionRejectsLower = true
		up, err := upload(mock, dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "refused to set OrderIntake to 1.0.16, below its designtime version 1.0.20: raise Bundle-Version above 1.0.20")
		assert.Equal(t, "guard", up.VersionRule)
	})
	t.Run("SaveAsVersion without effect is caught by the verification", func(t *testing.T) {
		mock, dir := versionedTenant(t, "1.0.13", "1.0.15", "1.0.16", "v2")
		mock.Artifacts["OrderIntake"].SaveAsVersionIgnored = true
		_, err := upload(mock, dir)
		assert.ErrorContains(t, err, "was saved as version 1.0.16 but the tenant reports 1.0.13")
	})
	t.Run("deploy refuses a designtime version that is not the repository's", func(t *testing.T) {
		mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": {Type: "Integration", DesignVersion: "1.0.13"}})
		opts := fastOpts()
		opts.Versioning = versioning.Manifest
		res := Deploy(context.Background(), NewTenant(mock.Executer()), []Artifact{{ID: "A", Type: "Integration", ExpectedVersion: "1.0.16"}}, opts)
		assert.Equal(t, StatusFailed, res[0].Status)
		assert.Equal(t, RuleGuard, res[0].Rule)
		assert.Contains(t, res[0].Error, "is not the repository's Bundle-Version 1.0.16")
		assert.Equal(t, 0, mock.Count("POST /api/v1/DeployIntegrationDesigntimeArtifact"))
	})
}
