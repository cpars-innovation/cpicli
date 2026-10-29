// Package depcheck keeps library packages small: a test lists the non-standard
// packages a package may depend on, and fails on anything else (cpicli#62).
package depcheck

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Allow fails t when the package in dir (built for linux, so the list does
// not depend on the host) depends on a non-standard package that is not
// covered by allowed. An entry covers the package of that path and the
// packages below it.
func Allow(t *testing.T, dir string, allowed ...string) {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if !covered(dep, allowed) {
			t.Errorf("new dependency %s: review it, then add it to this package's allowlist", dep)
		}
	}
}

func covered(dep string, allowed []string) bool {
	for _, a := range allowed {
		if dep == a || strings.HasPrefix(dep, strings.TrimSuffix(a, "/")+"/") {
			return true
		}
	}
	return false
}
