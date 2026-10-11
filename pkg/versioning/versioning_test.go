package versioning

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAndResolve(t *testing.T) {
	for _, s := range []string{"", "manifest", "keep", "tenant-bump", " keep "} {
		_, err := Parse(s)
		assert.NoError(t, err, s)
	}
	_, err := Parse("bump")
	assert.ErrorContains(t, err, "manifest, keep, tenant-bump")

	assert.Equal(t, Keep, Resolve("keep", "manifest", TenantBump), "artifact wins")
	assert.Equal(t, Manifest, Resolve("", "manifest", TenantBump), "then package")
	assert.Equal(t, TenantBump, Resolve("", "", TenantBump), "then global")
	assert.Equal(t, Unset, Resolve("", "", Unset))
}

func TestBumpAndMax(t *testing.T) {
	for in, want := range map[[2]string]string{
		{"1.0.15", "patch"}: "1.0.16", {"1.0.15", "minor"}: "1.1.0", {"1.0.15", "major"}: "2.0.0",
		{"1.0", "patch"}: "1.0.1", {"2", ""}: "2.0.1", {"1.0.9", "patch"}: "1.0.10",
	} {
		got, err := Bump(in[0], in[1])
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{"", "1.0.x", "1.0.0.1", "-1.0.0"} {
		_, err := Bump(bad, "patch")
		assert.Error(t, err, bad)
	}
	_, err := Bump("1.0.0", "build")
	assert.Error(t, err)

	assert.Equal(t, "1.0.15", Max("1.0.9", "", "1.0.15", "1.0.10"))
	assert.Equal(t, "", Max("", ""))
}

func TestCompare(t *testing.T) {
	assert.Equal(t, 1, Compare("1.0.10", "1.0.9"))
	assert.Equal(t, -1, Compare("1.0.9", "1.0.10"))
	assert.Equal(t, 0, Compare("1.0", "1.0.0"))
	assert.Equal(t, 1, Compare("1.0.x", "1.0.5"), "non-numeric sorts after numbers")
	for _, m := range Modes {
		got, err := Parse(string(m))
		require.NoError(t, err)
		assert.Equal(t, m, got)
	}
}
