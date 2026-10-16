//go:build !windows

package file

import "syscall"

// syscallNoFollow refuses to open a symlink in place of the file.
const syscallNoFollow = syscall.O_NOFOLLOW
