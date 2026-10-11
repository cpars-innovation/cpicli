// Package versioning decides which version an artifact gets on upload and
// deploy (see docs/versioning.md).
package versioning

import (
	"fmt"
	"strconv"
	"strings"
)

// Mode is how artifact versions are chosen.
type Mode string

const (
	// Unset keeps the behaviour without a versioning mode: the tenant's
	// version is kept on upload; deploy refuses an older designtime version
	// unless the designtime artifact was changed after the running
	// deployment.
	Unset Mode = ""
	// Manifest uses Bundle-Version of the repository: upload sets the
	// designtime artifact to it, deploy refuses a version lower than the
	// running one (only allowDowngrade overrides it).
	Manifest Mode = "manifest"
	// Keep leaves versions to the tenant and turns the downgrade guard off.
	Keep Mode = "keep"
	// TenantBump sets max(designtime, runtime)+1 (patch) on every upload
	// that changes content, for repositories without versions.
	TenantBump Mode = "tenant-bump"
)

// Rules name what decided a version, in upload and deploy results and logs.
const (
	RuleManifest = "manifest" // the repository's Bundle-Version
	RuleBump     = "bump"     // tenant-bump: max(designtime, runtime)+1
	RuleKeep     = "keep"     // the tenant's version, no guard
	RuleTenant   = "tenant"   // no mode: the tenant's version
	RuleGuard    = "guard"    // refused
)

// Modes are the valid values.
var Modes = []Mode{Manifest, Keep, TenantBump}

// Parse validates a mode ("" is Unset).
func Parse(s string) (Mode, error) {
	m := Mode(strings.TrimSpace(s))
	if m == Unset {
		return Unset, nil
	}
	for _, v := range Modes {
		if m == v {
			return m, nil
		}
	}
	return Unset, fmt.Errorf("invalid versioning %q (manifest, keep, tenant-bump)", s)
}

// Resolve returns the artifact's mode, else the package's, else the global
// one. Values must have been validated with Parse.
func Resolve(artifact, pkg string, global Mode) Mode {
	switch {
	case artifact != "":
		return Mode(artifact)
	case pkg != "":
		return Mode(pkg)
	}
	return global
}

// Levels are the bump levels.
var Levels = []string{"patch", "minor", "major"}

// Bump increases a version of up to three numeric segments ("1.0.15" ->
// "1.0.16" for patch, "1.1.0" for minor, "2.0.0" for major). Missing
// segments count as 0.
func Bump(version, level string) (string, error) {
	parts := strings.Split(strings.TrimSpace(version), ".")
	if version == "" || len(parts) > 3 {
		return "", fmt.Errorf("version %q is not MAJOR.MINOR.PATCH", version)
	}
	n := [3]int{}
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 {
			return "", fmt.Errorf("version %q is not MAJOR.MINOR.PATCH", version)
		}
		n[i] = v
	}
	switch level {
	case "", "patch":
		n[2]++
	case "minor":
		n[1], n[2] = n[1]+1, 0
	case "major":
		n[0], n[1], n[2] = n[0]+1, 0, 0
	default:
		return "", fmt.Errorf("invalid level %q (patch, minor, major)", level)
	}
	return fmt.Sprintf("%d.%d.%d", n[0], n[1], n[2]), nil
}

// Max returns the highest of the versions ("" are ignored).
func Max(versions ...string) string {
	best := ""
	for _, v := range versions {
		if v != "" && (best == "" || Compare(v, best) > 0) {
			best = v
		}
	}
	return best
}

// Compare compares two artifact versions segment by segment
// ("1.0.10" > "1.0.9"). Numeric segments compare as numbers; a non-numeric
// segment sorts after any number and non-numeric segments compare as strings;
// missing segments count as 0 ("1.0" == "1.0.0"). It returns -1, 0 or 1.
func Compare(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(as), len(bs)); i++ {
		x, y := "0", "0"
		if i < len(as) {
			x = as[i]
		}
		if i < len(bs) {
			y = bs[i]
		}
		if c := compareSegment(x, y); c != 0 {
			return c
		}
	}
	return 0
}

func compareSegment(x, y string) int {
	xn, yn := isDigits(x), isDigits(y)
	switch {
	case xn && yn:
		// compare as numbers without overflow: strip leading zeros, then length
		x, y = strings.TrimLeft(x, "0"), strings.TrimLeft(y, "0")
		if len(x) != len(y) {
			return sign(len(x) - len(y))
		}
		return strings.Compare(x, y)
	case xn:
		return -1
	case yn:
		return 1
	}
	return strings.Compare(x, y)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
