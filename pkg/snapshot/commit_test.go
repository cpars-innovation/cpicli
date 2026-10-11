package snapshot

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func committedFiles(t *testing.T, repo *git.Repository, hash string) []string {
	t.Helper()
	c, err := repo.CommitObject(plumbing.NewHash(hash))
	require.NoError(t, err)
	tree, err := c.Tree()
	require.NoError(t, err)
	var files []string
	require.NoError(t, tree.Files().ForEach(func(f *object.File) error {
		files = append(files, f.Name)
		return nil
	}))
	return files
}

// The snapshot folder is a sub-folder of the repository: the repository is
// found above it, and only changes below it are committed.
func TestCommitInParentRepository(t *testing.T) {
	m := demoTenant(t)
	root := t.TempDir()
	repo, err := git.PlainInit(root, false)
	require.NoError(t, err)
	dir := filepath.Join(root, "tenants", "dev")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "README.md"), []byte("outside\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not from the tenant\n"), 0o644))

	res, err := Take(context.Background(), m.Executer(), dir, Options{Packages: []string{"Billing"}})
	require.NoError(t, err)

	// only what the snapshot wrote, with another author
	got, err := Commit(dir, CommitOptions{Message: "sync", AuthorName: "Studio", AuthorEmail: "studio@example.com", Paths: res.Paths})
	require.NoError(t, err)
	require.NotEmpty(t, got.Hash)
	assert.Equal(t, mustReal(t, root), got.Root)
	files := committedFiles(t, repo, got.Hash)
	assert.Contains(t, files, "tenants/dev/.cpi/snapshot-state.json")
	assert.Contains(t, files, "tenants/dev/Billing/Billing.json")
	assert.Contains(t, files, "tenants/dev/Billing/Billing_In/META-INF/MANIFEST.MF")
	assert.NotContains(t, files, "tenants/dev/notes.txt", "not written by the snapshot")
	assert.NotContains(t, files, "README.md", "outside the snapshot folder")
	c, _ := repo.CommitObject(plumbing.NewHash(got.Hash))
	assert.Equal(t, "Studio", c.Author.Name)
	assert.Equal(t, "studio@example.com", c.Author.Email)
	assert.Equal(t, "sync", c.Message)

	// nothing changed since: nothing to commit
	got, err = Commit(dir, CommitOptions{Paths: res.Paths})
	require.NoError(t, err)
	assert.Empty(t, got.Hash)

	// without Paths: everything below the folder, nothing above it; the bot by default
	got, err = Commit(dir, CommitOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, got.Hash)
	files = committedFiles(t, repo, got.Hash)
	assert.Contains(t, files, "tenants/dev/notes.txt")
	assert.NotContains(t, files, "README.md")
	c, _ = repo.CommitObject(plumbing.NewHash(got.Hash))
	assert.Equal(t, DefaultAuthorName, c.Author.Name)

	// staged changes outside the folder are never committed with it
	w, err := repo.Worktree()
	require.NoError(t, err)
	_, err = w.Add("README.md")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("changed\n"), 0o644))
	_, err = Commit(dir, CommitOptions{})
	assert.ErrorContains(t, err, "README.md")

	// paths must be inside the folder
	_, err = Commit(dir, CommitOptions{Paths: []string{filepath.Join(root, "README.md")}})
	assert.ErrorContains(t, err, "outside")

	// no repository at all
	_, err = Commit(t.TempDir(), CommitOptions{})
	assert.ErrorIs(t, err, git.ErrRepositoryNotExists)
}

func mustReal(t *testing.T, p string) string {
	t.Helper()
	r, err := realPath(p)
	require.NoError(t, err)
	return r
}
