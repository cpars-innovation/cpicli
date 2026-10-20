package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTransportCommands(t *testing.T) {
	dev := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"Orders": {Type: "Integration", DesignVersion: "1.0.6", Package: "Pkg", Name: "Orders", Zip: namedFlowZip(t, "Orders", "v2")},
	})
	dev.Packages = []cpitest.Package{{ID: "Pkg", Version: "1.0.0"}}
	prod := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"Orders": {Type: "Integration", DesignVersion: "Active", Package: "Pkg", Name: "Orders"},
	})
	prod.Packages = []cpitest.Package{{ID: "Pkg", Version: "1.0.0"}}

	// the dev tier's content, as snapshot writes it, committed in Git
	repo := t.TempDir()
	r := runMain(t, append([]string{"snapshot", "--dir-git-repo", filepath.Join(repo, "packages"), "--dir-work", t.TempDir(),
		"--git-skip-commit", "--sync-package-details=false"}, basicAuth(dev)...)...)
	require.Equal(t, 0, r.code, r.stderr)
	for _, args := range [][]string{{"init", "-q", "-b", "dev"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@x", "commit", "-q", "-m", "dev"}} {
		// no detached auto maintenance writing while t.TempDir cleans up
		out, err := exec.Command("git", append([]string{"-C", repo, "-c", "maintenance.auto=false", "-c", "gc.auto=0"}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}

	run := func(code int, v any, args ...string) cliRun {
		t.Helper()
		r := runMainWith(t, func() { writeProfile(t, "prod", prod) }, append(args, "--output", "json")...)
		require.Equal(t, code, r.code, r.stderr)
		if v != nil {
			var env struct {
				Result json.RawMessage `json:"result"`
			}
			require.NoError(t, json.Unmarshal([]byte(r.stdout), &env), r.stdout)
			require.NoError(t, json.Unmarshal(env.Result, v))
		}
		return r
	}
	var set ops.TransportSet
	run(0, &set, "transport", "deps", "Orders", "--dir", filepath.Join(repo, "packages"))
	require.Len(t, set.Artifacts, 1)
	assert.Equal(t, "Pkg/Orders", set.Artifacts[0].Path)

	// the target has a draft: the check fails (exit 5), nothing is written
	var res ops.TransportCheckResult
	run(5, &res, "transport", "check", "Orders", "--dir", filepath.Join(repo, "packages"), "--target", "tenant:prod")
	assert.Equal(t, 1, res.Summary[ops.CheckFail])
	assert.Equal(t, 0, prod.Artifacts["Orders"].Uploads)

	// copy from the dev branch into the prod tree (a branch per tier)
	prodTree := t.TempDir()
	var copied ops.CopyResult
	run(0, &copied, "transport", "copy", "Orders", "--from", "git:dev:packages", "--to", prodTree, "--repo", repo)
	require.Len(t, copied.Artifacts, 1)
	assert.Equal(t, "created", copied.Artifacts[0].Action)
	assert.FileExists(t, filepath.Join(prodTree, "Pkg", "Orders", "META-INF", "MANIFEST.MF"))
	_, err := os.Stat(filepath.Join(prodTree, "Pkg", "Orders", "src", "main", "resources", "script", "s.groovy"))
	assert.NoError(t, err)
}
