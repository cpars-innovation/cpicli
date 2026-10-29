//go:build !windows

package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A root (or store) of another user is shared: the store goes to this user's
// cache, one per root (cpicli#64).
func TestContentStoreOfASharedRoot(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	own := t.TempDir()
	assert.Equal(t, filepath.Join(own, ".cpi", "cache", "content"), newRootStore(own).dir, "an own root keeps its store")

	shared := t.TempDir()
	if err := os.Chown(shared, os.Getuid()+4242, -1); err != nil {
		t.Skip("cannot give a folder to another user here:", err)
	}
	s := newRootStore(shared)
	assert.False(t, strings.HasPrefix(s.dir, shared), "not in the shared root: %s", s.dir)
	assert.Empty(t, s.root)
	other := newRootStore(t.TempDir())
	assert.NotEqual(t, s.dir, other.dir)

	// an own root whose store another user created
	mixed := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(mixed, ".cpi", "cache", "content"), 0o700))
	require.NoError(t, os.Chown(filepath.Join(mixed, ".cpi", "cache", "content"), os.Getuid()+4242, -1))
	assert.False(t, strings.HasPrefix(newRootStore(mixed).dir, mixed))

	ref, _, err := s.Put([]byte("payload"))
	require.NoError(t, err)
	got, err := s.Get(ref)
	require.NoError(t, err)
	assert.Equal(t, "payload", string(got))
}
