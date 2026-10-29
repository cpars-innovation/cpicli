//go:build !windows

package mcp

import (
	"os"
	"syscall"
)

// ownedByOther reports whether path exists and belongs to another user.
func ownedByOther(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) != os.Getuid()
}
