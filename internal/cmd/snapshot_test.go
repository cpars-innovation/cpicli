package cmd

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func flowZip(t *testing.T, id, script string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range map[string]string{
		"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\nBundle-SymbolicName: " + id + "\nBundle-Version: 1.0.0\nSAP-BundleType: IntegrationFlow\n",
		"src/main/resources/scenarioflows/integrationflow/" + id + ".iflw": "<bpmn2:definitions/>",
		"src/main/resources/script/s.groovy":                               script,
	} {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, _ = w.Write([]byte(content))
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestSnapshotIncrementalAndParallel(t *testing.T) {
	modified := time.Now().Add(-time.Hour).Truncate(time.Second)
	flow := func(pkg, id string) *cpitest.Artifact {
		return &cpitest.Artifact{Type: "Integration", DesignVersion: "1.0.5", Package: pkg, Name: id, ModifiedAt: modified,
			Zip: flowZip(t, id, "v1"), Parameters: map[string]string{"Host": "a"}}
	}
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"A1": flow("PkgA", "A1"), "A2": flow("PkgA", "A2"), "B1": flow("PkgB", "B1"),
		"NoTime": {Type: "Integration", DesignVersion: "1.0.0", Package: "PkgB", Name: "NoTime", Zip: flowZip(t, "NoTime", "v1")},
	})
	mock.Packages = []cpitest.Package{{ID: "PkgA", Version: "1.0.0"}, {ID: "PkgB", Version: "1.0.0"}}
	repo := t.TempDir()
	run := func(extra ...string) cliRun {
		args := append([]string{"snapshot", "--dir-git-repo", repo, "--dir-work", t.TempDir(), "--git-skip-commit", "--sync-package-details=false", "--output", "json"}, extra...)
		return runMain(t, append(args, basicAuth(mock)...)...)
	}
	downloads := func() int { return mock.Count("GET /api/v1/IntegrationDesigntimeArtifacts(Id='") }
	value := func(id string) int {
		return mock.Count("GET /api/v1/IntegrationDesigntimeArtifacts(Id='" + id + "',Version='active')/$value")
	}

	r := run("--parallel", "2")
	require.Equal(t, 0, r.code, r.stderr)
	for _, id := range []string{"A1", "A2", "B1", "NoTime"} {
		assert.Equal(t, 1, value(id), id)
	}
	assert.FileExists(t, filepath.Join(repo, ".cpi", "snapshot-state.json"))
	assert.FileExists(t, filepath.Join(repo, "PkgA", "A1", "META-INF", "MANIFEST.MF"))

	// nothing changed: only NoTime (no ModifiedAt) is downloaded again
	r = run("--incremental")
	require.Equal(t, 0, r.code, r.stderr)
	for _, id := range []string{"A1", "A2", "B1"} {
		assert.Equal(t, 1, value(id), "%s skipped", id)
	}
	assert.Equal(t, 2, value("NoTime"), "never skipped without a modification time")
	assert.Contains(t, r.stdout, `"artifactsSkipped": 3`)

	// each signal on its own triggers a download
	mock.Artifacts["A1"].ModifiedAt = time.Now()                        // edited in the Web UI
	mock.Artifacts["A2"].Parameters["Host"] = "b"                       // Configure
	require.NoError(t, os.RemoveAll(filepath.Join(repo, "PkgB", "B1"))) // local copy gone
	r = run("--incremental")
	require.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, 2, value("A1"), "ModifiedAt changed")
	assert.Equal(t, 2, value("A2"), "configuration changed")
	assert.Equal(t, 2, value("B1"), "local copy changed")
	assert.FileExists(t, filepath.Join(repo, "PkgB", "B1", "META-INF", "MANIFEST.MF"))

	// a full run downloads everything again
	before := downloads()
	r = run()
	require.Equal(t, 0, r.code, r.stderr)
	assert.Greater(t, downloads(), before)
	assert.Equal(t, 3, value("A1"))

	// a failing package does not stop the others: partial, state saved
	mock.Artifacts["B1"].Zip = nil // download fails
	mock.Artifacts["B1"].ModifiedAt = time.Now().Add(time.Minute)
	mock.Artifacts["A1"].ModifiedAt = time.Now().Add(time.Minute)
	r = run("--incremental")
	assert.Equal(t, 7, r.code, r.stderr)
	assert.Equal(t, 4, value("A1"), "PkgA still processed")
	assert.Contains(t, r.stdout, `"failed"`)
}

// One large package must not run alone: artifacts of all packages share the
// download slots.
func TestSnapshotParallelArtifacts(t *testing.T) {
	arts := map[string]*cpitest.Artifact{}
	for i := 0; i < 24; i++ { // one large package
		id := fmt.Sprintf("Big%02d", i)
		arts[id] = &cpitest.Artifact{Type: "Integration", DesignVersion: "1.0.0", Package: "Big", Name: id, Zip: flowZip(t, id, "v1")}
	}
	for i := 0; i < 4; i++ { // a few small ones
		id := fmt.Sprintf("Small%d", i)
		arts[id] = &cpitest.Artifact{Type: "Integration", DesignVersion: "1.0.0", Package: fmt.Sprintf("S%d", i), Name: id, Zip: flowZip(t, id, "v1")}
	}
	mock := cpitest.NewTenant(t, arts)
	mock.Packages = []cpitest.Package{{ID: "Big"}, {ID: "S0"}, {ID: "S1"}, {ID: "S2"}, {ID: "S3"}}
	mock.DownloadLatency = 100 * time.Millisecond

	r := runMain(t, append([]string{"snapshot", "--dir-git-repo", t.TempDir(), "--dir-work", t.TempDir(), "--git-skip-commit",
		"--sync-package-details=false", "--parallel", "8", "--output", "json"}, basicAuth(mock)...)...)
	require.Equal(t, 0, r.code, r.stderr)
	assert.Contains(t, r.stdout, `"artifactsDownloaded": 28`)
	// The peak proves the parallelism across packages;
	// no wall-clock bound, which flakes on slow CI runners.
	assert.Equal(t, int64(8), mock.PeakDownloads(), "8 downloads at a time, also within the large package")
}
