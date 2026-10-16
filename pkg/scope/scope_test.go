package scope

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustParse(t *testing.T, s string) *Scope {
	t.Helper()
	sc, err := Parse([]byte(s))
	require.NoError(t, err)
	return sc
}

func TestParse(t *testing.T) {
	sc := mustParse(t, `{"version":1,"packages":["SD_*","Shared_*"],"artifacts":["!*_Legacy"],"partners":["SD_*"],
		"tenant":["variables:read"],"actions":["read","download","upload","deploy","test"]}`)
	assert.Equal(t, []string{"SD_*", "Shared_*"}, sc.Packages)
	yml := mustParse(t, "version: 1\npackages: [SD_*]\nactions: [read]\n")
	assert.Equal(t, []string{"read"}, yml.Actions)

	for in, msg := range map[string]string{
		`{"version":2}`:                           "version must be 1",
		`{"version":1,"packges":["x"]}`:           "packges",
		`{"version":1,"actions":["publish"]}`:     `unknown action "publish"`,
		`{"version":1,"tenant":["keystore"]}`:     "<area>:read",
		`{"version":1,"tenant":["secrets:read"]}`: "<area>:read",
		`{"version":1,"packages":["SD/*"]}`:       "only letters",
		`{"version":1,"packages":["[a-z]*"]}`:     "only letters",
		`{"version":1,"artifacts":["!"]}`:         "empty pattern",
		`{"version":1,"partners":["Ä*"]}`:         "only letters",
	} {
		_, err := Parse([]byte(in))
		assert.ErrorContains(t, err, msg, in)
	}
}

func TestMatching(t *testing.T) {
	for _, c := range []struct {
		patterns []string
		id       string
		want     bool
	}{
		{nil, "X", false},
		{[]string{"*"}, "X", true},
		{[]string{"SD_*"}, "SD_Orders", true},
		{[]string{"SD_*"}, "sd_Orders", false}, // case-sensitive
		{[]string{"SD_*"}, "XSD_Orders", false},
		{[]string{"*_In"}, "Orders_In", true},
		{[]string{"A*B*C"}, "AxxBxxC", true},
		{[]string{"A*B*C"}, "AxxCxxB", false},
		{[]string{"!*_Legacy"}, "SD_Order", true}, // leading exclusion implies "*"
		{[]string{"!*_Legacy"}, "SD_Order_Legacy", false},
		{[]string{"SD_*", "!SD_Secret*", "SD_Secret_Public"}, "SD_Secret_Public", true}, // last match wins
		{[]string{"SD_*", "!SD_Secret*", "SD_Secret_Public"}, "SD_Secret_X", false},
		{[]string{"Orders"}, "Orders", true},
		{[]string{"Orders"}, "Orders2", false},
	} {
		assert.Equal(t, c.want, matches(c.patterns, c.id), "%v %s", c.patterns, c.id)
	}
}

func TestAllows(t *testing.T) {
	sc := mustParse(t, `{"version":1,"packages":["SD_*"],"artifacts":["!*_Legacy"],"partners":["SD_*"],
		"tenant":["variables:read","jms:write"],"actions":["read","deploy"]}`)
	art := func(id, pkg string) Resource { return Resource{Kind: KindArtifact, ID: id, Package: pkg} }
	assert.True(t, sc.Allows(art("SD_Order_In", "SD_Orders"), ActionDeploy))
	assert.False(t, sc.Allows(art("SD_Order_In", "SD_Orders"), ActionUpload), "action not granted")
	assert.False(t, sc.Allows(art("FI_Post", "FI_Billing"), ActionRead), "package out of scope")
	assert.False(t, sc.Allows(art("SD_Order_Legacy", "SD_Orders"), ActionRead), "artifact excluded")
	assert.False(t, sc.Allows(art("SD_Order_In", ""), ActionRead), "unknown package: refused")
	assert.True(t, sc.Allows(Resource{Kind: KindPartner, ID: "SD_Customer1"}, ActionRead))
	assert.False(t, sc.Allows(Resource{Kind: KindPartner, ID: "FI_X"}, ActionRead))
	assert.True(t, sc.Allows(Resource{Kind: KindTenant, ID: "variables"}, ActionRead))
	assert.False(t, sc.Allows(Resource{Kind: KindTenant, ID: "variables"}, ActionDelete))
	assert.True(t, sc.Allows(Resource{Kind: KindTenant, ID: "jms"}, ActionRead), "write implies read")
	assert.False(t, sc.Allows(Resource{Kind: KindTenant, ID: "keystore"}, ActionRead), "no grant: none")
	assert.False(t, sc.Allows(Resource{Kind: "unknown", ID: "x"}, ActionRead))
	var none *Scope
	assert.True(t, none.Allows(art("anything", ""), ActionDelete), "no scope: unrestricted")
}

func TestHashIgnoresOrderOfSets(t *testing.T) {
	a := mustParse(t, `{"version":1,"packages":["A"],"actions":["read","deploy"],"tenant":["jms:read","keystore:read"]}`)
	b := mustParse(t, `{"version":1,"packages":["A"],"actions":["deploy","read"],"tenant":["keystore:read","jms:read"]}`)
	c := mustParse(t, `{"version":1,"packages":["B"],"actions":["read","deploy"]}`)
	assert.Equal(t, a.Hash(), b.Hash())
	assert.NotEqual(t, a.Hash(), c.Hash())
	assert.Len(t, a.Hash(), 64)
}

func TestValidID(t *testing.T) {
	for _, ok := range []string{"SD_Order_In", "a.b-c", "X1"} {
		assert.True(t, ValidID(ok), ok)
	}
	for _, bad := range []string{"", "X')/Y('Z", "a b", "a/b", "Ä", "x*"} {
		assert.False(t, ValidID(bad), bad)
	}
}

// glob never panics and "*" alone matches everything.
func FuzzGlob(f *testing.F) {
	for _, s := range []string{"", "*", "a*b", "**", "a**b*"} {
		f.Add(s, "ab")
	}
	f.Fuzz(func(t *testing.T, pattern, s string) {
		_ = glob(pattern, s)
		if !glob("*", s) {
			t.Fatalf("* must match %q", s)
		}
		if pattern != "" && !containsStar(pattern) && glob(pattern, s) != (pattern == s) {
			t.Fatalf("literal %q vs %q", pattern, s)
		}
	})
}

func containsStar(s string) bool {
	for _, r := range s {
		if r == '*' {
			return true
		}
	}
	return false
}
