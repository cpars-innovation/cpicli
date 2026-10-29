//go:build windows

package mcp

// ownedByOther: Windows has no single owning user ID to compare; the store
// stays under the root (one profile per user is the norm there).
func ownedByOther(string) bool { return false }
