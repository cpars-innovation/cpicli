package file

import (
	"archive/zip"
	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type entry struct {
	name string
	mode os.FileMode
	body string
}

func writeZip(t *testing.T, entries ...entry) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "a.zip")
	f, err := os.Create(p)
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		h.SetMode(e.mode)
		w, err := zw.CreateHeader(h)
		require.NoError(t, err)
		_, _ = w.Write([]byte(e.body))
	}
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())
	return p
}

func TestUnzipMasksModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix modes")
	}
	dst := t.TempDir()
	require.NoError(t, UnzipSource(writeZip(t, entry{"a.sh", 0o4777, "x"}, entry{"b.txt", 0o666, "y"}), dst))
	fi, err := os.Stat(filepath.Join(dst, "a.sh"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), fi.Mode().Perm(), "no setuid, not world-writable")
	assert.Zero(t, fi.Mode()&os.ModeSetuid)
	fi, err = os.Stat(filepath.Join(dst, "b.txt"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), fi.Mode().Perm())
}

func TestUnzipRefusesSymlinksAndBombs(t *testing.T) {
	err := UnzipSource(writeZip(t, entry{"link", os.ModeSymlink | 0o777, "/etc/passwd"}), t.TempDir())
	assert.ErrorContains(t, err, "not a regular file")

	defer func(n int64) { MaxZipBytes = n }(MaxZipBytes)
	MaxZipBytes = 10
	err = UnzipSource(writeZip(t, entry{"big", 0o644, strings.Repeat("x", 11)}), t.TempDir())
	assert.ErrorContains(t, err, "larger than 10 bytes")
	require.NoError(t, UnzipSource(writeZip(t, entry{"ok", 0o644, strings.Repeat("x", 10)}), t.TempDir()))
}

func TestUnzipRefusesSymlinkedDirectoryLeavingTheDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dst, outside := t.TempDir(), t.TempDir()
	cpitest.Symlink(t, outside, filepath.Join(dst, "sub"))
	err := UnzipSource(writeZip(t, entry{"sub/evil.txt", 0o644, "x"}), dst)
	assert.ErrorContains(t, err, "leaves the destination")
	_, statErr := os.Stat(filepath.Join(outside, "evil.txt"))
	assert.True(t, os.IsNotExist(statErr))
}
