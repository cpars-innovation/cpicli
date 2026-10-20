package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writePolicy(t *testing.T, content string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "branch-policy.json")
	require.NoError(t, os.WriteFile(file, []byte(content), 0o644))
	return file
}

func TestBranchPolicyDecide(t *testing.T) {
	file := writePolicy(t, `{"_comment": "x", "default": "readwrite", "readonlyBranches": ["main", "release/*"],
		"repos": [".", "sub", "missing"], "writableEnvironments": ["dev"]}`)
	root, sub := filepath.FromSlash("/work"), filepath.Join(filepath.FromSlash("/work"), "sub")
	p, err := LoadBranchPolicy(file, root, "dev")
	require.NoError(t, err)
	branches := map[string]string{root: "feature/x", sub: "HEAD"}
	p.branchOf = func(dir string) (string, error) {
		if b, ok := branches[dir]; ok {
			return b, nil
		}
		return "", errors.New("not a git repository")
	}
	d := p.Decide()
	assert.False(t, d.ReadOnly, "feature branch; a detached submodule and a missing repo are not judged: %v", d.Reasons)

	branches[sub] = "release/2026.10"
	d = p.Decide()
	assert.True(t, d.ReadOnly)
	assert.Equal(t, []string{"sub is on the read-only branch release/2026.10"}, d.Reasons)

	branches[sub], branches[root] = "HEAD", "HEAD"
	assert.Equal(t, []string{". is on a detached HEAD"}, p.Decide().Reasons)

	delete(branches, root)
	assert.Contains(t, p.Decide().Reasons[0], "cannot be read", "the root checkout must be judged: fail closed")

	branches[root] = "feature/x"
	p.tier = "prod"
	assert.Equal(t, []string{"tier prod is never writable (writableEnvironments)"}, p.Decide().Reasons)
	p.tier, p.Default = "", "readonly"
	assert.Equal(t, []string{"the policy default is read-only"}, p.Decide().Reasons)

	_, err = LoadBranchPolicy(writePolicy(t, `default: maybe`), "/work", "")
	assert.ErrorContains(t, err, "default must be readonly or readwrite")
	_, err = LoadBranchPolicy(filepath.Join(t.TempDir(), "none.yaml"), "/work", "")
	assert.Error(t, err)
}

// The policy follows a checkout during the session: tenant tools are
// refused on main and work again on a feature branch; read tools always
// work.
func TestBranchPolicyWrap(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		// no detached auto maintenance writing while t.TempDir cleans up
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false",
			"-c", "maintenance.auto=false", "-c", "gc.auto=0"}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "init")
	p, err := LoadBranchPolicy(writePolicy(t, "readonlyBranches: [main]\n"), root, "")
	require.NoError(t, err)

	ran := 0
	handler := func(context.Context, json.RawMessage) (any, error) { ran++; return "ok", nil }
	tools := p.Wrap([]Tool{{Name: "deploy", Handler: handler}, {Name: "list_packages", Handler: handler}})

	_, err = tools[0].Handler(context.Background(), nil)
	var ro *BranchReadOnlyError
	require.ErrorAs(t, err, &ro)
	assert.Equal(t, "branch_readonly", errorCategory(err))
	assert.Contains(t, err.Error(), ". is on the read-only branch main")
	_, err = tools[1].Handler(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, 1, ran, "only the read tool ran")
	assert.Contains(t, p.Instructions(), "read-only now")

	git("checkout", "-q", "-b", "feature/x")
	_, err = tools[0].Handler(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, 2, ran)
	assert.Contains(t, p.Instructions(), "tenant changes are allowed now")
}
