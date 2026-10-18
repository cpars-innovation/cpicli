package cpitest

import (
	"os"
	"testing"
)

// SetHome makes dir the user's home directory for the test: HOME and, for
// os.UserHomeDir on Windows, USERPROFILE.
func SetHome(t testing.TB, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

// Symlink creates a symbolic link, or skips the test where the system does
// not allow it (Windows without developer mode).
func Symlink(t testing.TB, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}
