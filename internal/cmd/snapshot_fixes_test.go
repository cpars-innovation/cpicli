package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/snapshot"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// flowWith is a tenant export of a flow with the given manifest version,
// parameters.prop and parameters.propdef.
func flowWith(t *testing.T, id, version, props, propdef string) []byte {
	files := map[string]string{
		"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\r\nBundle-SymbolicName: " + id + "; singleton:=true\r\nBundle-Name: " + id + "\r\nBundle-Version: " + version + "\r\nSAP-BundleType: IntegrationFlow\r\n\r\n",
		"metainfo.prop":        "#Fri Oct 09 09:34:25 UTC 2026\r\ndescription=" + id + "\r\nauthor=x\r\n",
		"src/main/resources/scenarioflows/integrationflow/" + id + ".iflw": "<bpmn2:definitions/>",
		"src/main/resources/parameters.prop":                               props,
	}
	if propdef != "" {
		files["src/main/resources/parameters.propdef"] = propdef
	}
	return zipOf(t, files)
}

func propdefOf(names ...string) string {
	s := `<?xml version="1.0" encoding="UTF-8"?><parameters>`
	for _, n := range names {
		s += "<parameter><key/><name>" + n + "</name><type>xsd:string</type></parameter>"
	}
	return s + "</parameters>"
}

func snapshotJSON(t *testing.T, mock *cpitest.Tenant, repo string, extra ...string) (cliRun, snapshotResult) {
	t.Helper()
	args := append([]string{"snapshot", "--dir-git-repo", repo, "--dir-work", t.TempDir(), "--git-skip-commit",
		"--sync-package-details=false", "--output", "json"}, extra...)
	r := runMain(t, append(args, basicAuth(mock)...)...)
	require.Equal(t, 0, r.code, r.stderr)
	var env struct {
		Result snapshotResult `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &env), r.stdout)
	return r, env.Result
}

func TestSnapshotFindings(t *testing.T) {
	modified := time.Now().Add(-time.Hour).Truncate(time.Second)
	props := "#Fri Oct 09 09:34:25 UTC 2026\r\nSTUB_HTTP_URL=http://old\r\nSTUB_HTTP_BASEURL=http://new\r\n"
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		// in draft: the export says Bundle-Version: Active
		"Draft": {Type: "Integration", DesignVersion: "Active", Package: "EDM", Name: "Draft", ModifiedAt: modified,
			Zip: flowWith(t, "Draft", "Active", "Host=h\r\n", ""), Runtime: &cpitest.Runtime{Version: "1.0.4", Status: "STARTED"}},
		"Renamed": {Type: "Integration", DesignVersion: "1.0.2", Package: "EDM", Name: "Renamed", ModifiedAt: modified,
			Zip: flowWith(t, "Renamed", "1.0.2", props, propdefOf("STUB_HTTP_BASEURL"))},
		"Elsewhere": {Type: "Integration", DesignVersion: "1.0.0", Package: "Other", Name: "Elsewhere", ModifiedAt: modified,
			Zip: flowWith(t, "Elsewhere", "1.0.0", "", "")},
	})
	mock.Packages = []cpitest.Package{{ID: "EDM", Version: "1.0.0"}, {ID: "Other", Version: "1.0.0"}, {ID: "Third", Version: "1.0.0"}}
	repo := t.TempDir()
	path := func(parts ...string) string { return filepath.Join(append([]string{repo, "EDM"}, parts...)...) }

	r, res := snapshotJSON(t, mock, repo, "--draft-handling", "ADD", "--ids-include", "EDM")
	st := statuses(res)

	// 8. a draft gets a version number (the running one here), and is marked
	mf, err := os.ReadFile(path("Draft", "META-INF", "MANIFEST.MF"))
	require.NoError(t, err)
	assert.Contains(t, string(mf), "Bundle-Version: 1.0.4")
	assert.NotContains(t, string(mf), "Active")
	assert.True(t, st["Draft"].Draft)
	assert.Equal(t, "new (draft)", st["Draft"].Label())
	assert.Contains(t, r.stderr, "new (draft)")
	state, err := snapshot.LoadState(filepath.Join(repo, ".cpi", "snapshot-state.json"))
	require.NoError(t, err)
	assert.True(t, state.Artifacts["EDM/Draft"].Draft)

	// 9. metainfo.prop without the timestamp, keys sorted
	meta, err := os.ReadFile(path("Renamed", "metainfo.prop"))
	require.NoError(t, err)
	assert.Equal(t, "author=x\ndescription=Renamed\n", string(meta))

	// 10. a parameter the propdef no longer declares is reported, not written
	assert.Equal(t, []string{"STUB_HTTP_URL"}, st["Renamed"].OrphanParameters)
	p, err := os.ReadFile(path("Renamed", "src", "main", "resources", "parameters.prop"))
	require.NoError(t, err)
	assert.Equal(t, "STUB_HTTP_BASEURL=http://new\n", string(p))

	// 11. one line for the packages filtered out, no warning per package
	assert.Contains(t, r.stderr, "2 package(s) not in --ids-include")
	assert.NotContains(t, r.stderr, "Skipping Other")

	// a second snapshot changes nothing (also with a repository version
	// higher than the running one, which wins)
	require.NoError(t, os.WriteFile(path("Draft", "META-INF", "MANIFEST.MF"),
		[]byte("Manifest-Version: 1.0\nBundle-SymbolicName: Draft; singleton:=true\nBundle-Name: Draft\nBundle-Version: 1.0.9\nSAP-BundleType: IntegrationFlow\n"), 0o644))
	_, res = snapshotJSON(t, mock, repo, "--draft-handling", "ADD", "--ids-include", "EDM", "--overwrite-local")
	mf, _ = os.ReadFile(path("Draft", "META-INF", "MANIFEST.MF"))
	assert.Contains(t, string(mf), "Bundle-Version: 1.0.9", "the repository's version when higher")
	assert.Equal(t, "unchanged (draft)", statuses(res)["Draft"].Label(), "the repository version is kept: nothing to write")
	before := treeOf(t, filepath.Join(repo, "EDM"))
	_, res = snapshotJSON(t, mock, repo, "--draft-handling", "ADD", "--ids-include", "EDM")
	assert.Equal(t, before, treeOf(t, filepath.Join(repo, "EDM")), "stable")
	assert.Equal(t, snapshot.StatusUnchanged, statuses(res)["Renamed"].Status)

	// --keep-orphan-parameters writes them (and still reports them)
	_, res = snapshotJSON(t, mock, repo, "--ids-include", "EDM", "--keep-orphan-parameters", "--overwrite-local")
	p, _ = os.ReadFile(path("Renamed", "src", "main", "resources", "parameters.prop"))
	assert.Equal(t, "STUB_HTTP_BASEURL=http://new\nSTUB_HTTP_URL=http://old\n", string(p))
	assert.Equal(t, []string{"STUB_HTTP_URL"}, statuses(res)["Renamed"].OrphanParameters)
}

func TestSnapshotDirectories(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"Flow": {Type: "Integration", DesignVersion: "1.0.0", Package: "EDM", Name: "Flow", Zip: flowWith(t, "Flow", "1.0.0", "", "")},
	})
	mock.Packages = []cpitest.Package{{ID: "EDM", Version: "1.0.0"}}

	// without a repository nothing is downloaded (it used to write next to /)
	r := runMain(t, append([]string{"snapshot", "--git-skip-commit"}, basicAuth(mock)...)...)
	assert.Equal(t, 2, r.code, r.stderr)
	assert.Contains(t, r.stderr, "dir-git-repo")
	assert.Zero(t, mock.Count("GET /api/v1/IntegrationPackages"))

	// relative directories
	repo := t.TempDir()
	t.Chdir(repo)
	r = runMain(t, append([]string{"snapshot", "--dir-git-repo", ".", "--dir-artifacts", "packages", "--dir-work", t.TempDir(),
		"--git-skip-commit", "--sync-package-details=false"}, basicAuth(mock)...)...)
	require.Equal(t, 0, r.code, r.stderr)
	assert.FileExists(t, filepath.Join(repo, "packages", "EDM", "Flow", "META-INF", "MANIFEST.MF"))

	r = runMain(t, append([]string{"snapshot", "--dir-git-repo", "repo", "--dir-artifacts", "other/packages", "--git-skip-commit"}, basicAuth(mock)...)...)
	assert.Equal(t, 2, r.code, "artifacts outside the repository")
}

// C3: the snapshot folder may be a sub-folder of the repository; only what
// the snapshot changed is committed (--git-commit-changed-only), with the
// given author; the run's work folder is removed.
func TestSnapshotCommitOptions(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"Flow": {Type: "Integration", DesignVersion: "1.0.0", Package: "EDM", Name: "Flow", Zip: flowWith(t, "Flow", "1.0.0", "", "")},
	})
	mock.Packages = []cpitest.Package{{ID: "EDM", Version: "1.0.0"}}
	root := t.TempDir()
	repo, err := git.PlainInit(root, false)
	require.NoError(t, err)
	dir := filepath.Join(root, "tenant")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "other.txt"), []byte("outside\n"), 0o644))
	work := t.TempDir()

	r := runMain(t, append([]string{"snapshot", "--dir-git-repo", dir, "--dir-work", work, "--git-commit-changed-only",
		"--git-commit-user", "Studio", "--git-commit-email", "studio@example.com", "--git-commit-msg", "import"}, basicAuth(mock)...)...)
	require.Equal(t, 0, r.code, r.stderr)
	head, err := repo.Head()
	require.NoError(t, err)
	c, err := repo.CommitObject(head.Hash())
	require.NoError(t, err)
	assert.Equal(t, "Studio", c.Author.Name)
	assert.Equal(t, "import", c.Message)
	tree, err := c.Tree()
	require.NoError(t, err)
	var files []string
	require.NoError(t, tree.Files().ForEach(func(f *object.File) error {
		files = append(files, f.Name)
		return nil
	}))
	assert.Contains(t, files, "tenant/EDM/Flow/META-INF/MANIFEST.MF")
	assert.Contains(t, files, "tenant/EDM/EDM.json")
	assert.Contains(t, files, "tenant/.cpi/snapshot-state.json")
	assert.NotContains(t, files, "tenant/notes.txt", "not written by the snapshot")
	assert.NotContains(t, files, "other.txt", "outside --dir-git-repo")
	left, _ := os.ReadDir(work)
	assert.Empty(t, left, "the work folder of the run is removed")
}
