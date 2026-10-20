package ops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/cpars-innovation/cpicli/pkg/manifest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	// no auto maintenance: git runs it detached after a commit, and it can
	// still write to .git/objects when t.TempDir removes the repository
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false",
		"-c", "maintenance.auto=false", "-c", "gc.auto=0"}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func writeArtifact(t *testing.T, dir, id, version, script string) {
	t.Helper()
	writeFlow(t, dir, map[string]string{
		"META-INF/MANIFEST.MF":               "Manifest-Version: 1.0\r\nBundle-SymbolicName: " + id + "; singleton:=true\r\nBundle-Name: " + id + "\r\nBundle-Version: " + version + "\r\nSAP-BundleType: IntegrationFlow\r\n\r\n",
		"src/main/resources/script/s.groovy": script,
	})
}

func versionOf(t *testing.T, dir string) string {
	t.Helper()
	v, err := manifest.Version(dir)
	require.NoError(t, err)
	return v
}

func TestBumpVersionsChanged(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	content := filepath.Join(repo, "packages")
	changed := filepath.Join(content, "EDM", "Changed")
	unchanged := filepath.Join(content, "EDM", "Unchanged")
	other := filepath.Join(content, "Other", "Elsewhere")
	writeArtifact(t, changed, "Changed", "1.0.15", "v1")
	writeArtifact(t, unchanged, "Unchanged", "1.0.7", "v1")
	writeArtifact(t, other, "Elsewhere", "2.1.0", "v1")
	gitRun(t, repo, "init", "-q")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "versions")

	// a committed change and an uncommitted one
	writeArtifact(t, changed, "Changed", "1.0.15", "v2")
	gitRun(t, repo, "commit", "-q", "-am", "fix script")
	require.NoError(t, os.WriteFile(filepath.Join(other, "src/main/resources/script/new.groovy"), []byte("x"), 0o644))
	// a new artifact that was never committed
	writeArtifact(t, filepath.Join(content, "EDM", "Brand_New"), "Brand_New", "1.0.0", "v1")

	res, err := BumpVersions(context.Background(), BumpOptions{Dir: content, Changed: true})
	require.NoError(t, err)
	byID := map[string]BumpItem{}
	for _, it := range res.Items {
		byID[it.Artifact] = it
	}
	assert.Equal(t, BumpBumped, byID["Changed"].Status)
	assert.Equal(t, "1.0.16", byID["Changed"].New)
	assert.NotEmpty(t, byID["Changed"].Since)
	assert.Equal(t, BumpUnchanged, byID["Unchanged"].Status, "unchanged artifacts are left alone")
	assert.Equal(t, BumpBumped, byID["Elsewhere"].Status, "untracked files count as changes")
	assert.Equal(t, "2.1.1", byID["Elsewhere"].New)
	assert.Equal(t, BumpNew, byID["Brand_New"].Status)
	assert.Equal(t, "1.0.16", versionOf(t, changed))
	assert.Equal(t, "1.0.7", versionOf(t, unchanged))
	assert.Equal(t, "1.0.0", versionOf(t, filepath.Join(content, "EDM", "Brand_New")))
	mf, _ := os.ReadFile(filepath.Join(changed, "META-INF", "MANIFEST.MF"))
	assert.Contains(t, string(mf), "Bundle-SymbolicName: Changed; singleton:=true\r\nBundle-Name: Changed\r\nBundle-Version: 1.0.16\r\n", "only the version line changes")

	// a second run before committing does not bump again
	res, err = BumpVersions(context.Background(), BumpOptions{Dir: content, Changed: true})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Counts[BumpAlreadyBumped])
	assert.Equal(t, "1.0.16", versionOf(t, changed))

	// after the commit the bumped artifacts are unchanged
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "bump")
	res, err = BumpVersions(context.Background(), BumpOptions{Dir: content, Changed: true})
	require.NoError(t, err)
	assert.Equal(t, 4, res.Counts[BumpUnchanged], "Brand_New is committed now as well")

	// filters, levels, dry run, without --changed
	res, err = BumpVersions(context.Background(), BumpOptions{Dir: content, Packages: []string{"EDM"}, Artifacts: []string{"Un*"}, Level: "minor", DryRun: true})
	require.NoError(t, err)
	require.Len(t, res.Items, 1)
	assert.Equal(t, "1.1.0", res.Items[0].New)
	assert.Equal(t, "1.0.7", versionOf(t, unchanged), "dry run")
	_, err = BumpVersions(context.Background(), BumpOptions{Dir: content, Artifacts: []string{"Nope"}})
	assert.Error(t, err)
	_, err = BumpVersions(context.Background(), BumpOptions{Dir: content, Level: "build"})
	assert.Error(t, err)
	_, err = BumpVersions(context.Background(), BumpOptions{Dir: t.TempDir(), Changed: true})
	assert.ErrorContains(t, err, "Git repository")
}

// Revisions come from tool arguments: one that git reads as an option
// (git diff --output=FILE writes a file) is refused before git runs.
func TestCheckGitRef(t *testing.T) {
	for _, ok := range []string{"HEAD", "main", "v1.2.0", "origin/main~3", "abc123"} {
		assert.NoError(t, CheckGitRef(ok), ok)
	}
	for _, bad := range []string{"", "--output=/tmp/x", "-p", "--remote=x", "main\nx"} {
		assert.Error(t, CheckGitRef(bad), bad)
	}
}
