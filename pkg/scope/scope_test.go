package scope

import (
	"encoding/json"
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

// A gateway policy: deploy on SD_*, only read on FI_Shared, HR_* denied
// whatever a broad grant says.
const policyScope = `
version: 1
grants:
  - packages: ["SD_*"]
    artifacts: ["!*_Legacy"]
    actions: [read, download, deploy]
  - packages: ["FI_Shared"]
    actions: [read]
  - packages: ["*"]
    actions: [read]
deny:
  - packages: ["HR_*"]
  - packages: ["SD_*"]
    artifacts: ["*_Secret"]
    actions: [download]
`

func TestGrantsAndDeny(t *testing.T) {
	sc := mustParse(t, policyScope)
	art := func(id, pkg string) Resource { return Resource{Kind: KindArtifact, ID: id, Package: pkg} }
	pkg := func(id string) Resource { return Resource{Kind: KindPackage, ID: id} }

	assert.True(t, sc.Allows(art("SD_In", "SD_Orders"), ActionDeploy))
	assert.False(t, sc.Allows(art("SD_In_Legacy", "SD_Orders"), ActionDeploy), "the grant's artifact patterns")
	assert.True(t, sc.Allows(art("SD_In_Legacy", "SD_Orders"), ActionRead), "another grant reads every package")
	assert.True(t, sc.Allows(art("FI_Post", "FI_Shared"), ActionRead))
	assert.False(t, sc.Allows(art("FI_Post", "FI_Shared"), ActionDeploy), "actions are per grant")
	assert.False(t, sc.Allows(art("FI_Post", "FI_Shared"), ActionDownload))

	// deny wins over every grant, packages and their artifacts alike
	assert.False(t, sc.Allows(pkg("HR_Payroll"), ActionRead))
	assert.False(t, sc.Allows(art("HR_In", "HR_Payroll"), ActionRead))
	assert.False(t, sc.SeesPackage("HR_Payroll"))
	// a deny with artifacts and actions: only those
	assert.False(t, sc.Allows(art("SD_Key_Secret", "SD_Orders"), ActionDownload))
	assert.True(t, sc.Allows(art("SD_Key_Secret", "SD_Orders"), ActionRead))
	assert.True(t, sc.Allows(pkg("SD_Orders"), ActionDownload), "an artifact deny leaves the package alone")

	assert.True(t, sc.Grants(ActionDeploy), "granted somewhere")
	assert.False(t, sc.Grants(ActionUpload))
	assert.False(t, sc.Allows(art("SD_In", ""), ActionRead), "unknown package: refused")
	assert.False(t, sc.Allows(Resource{Kind: KindPartner, ID: "SD_X"}, ActionRead), "partners only through the top level")

	// top level and grants add up
	both := mustParse(t, `{"version":1,"packages":["A"],"actions":["deploy"],"grants":[{"packages":["B"],"actions":["read"]}]}`)
	assert.True(t, both.Allows(pkg("A"), ActionDeploy))
	assert.True(t, both.Allows(pkg("B"), ActionRead))
	assert.False(t, both.Allows(pkg("B"), ActionDeploy))
	assert.False(t, both.Allows(pkg("A"), ActionRead))
}

func TestParseGrants(t *testing.T) {
	for in, msg := range map[string]string{
		`{"version":1,"grants":[{"actions":["read"]}]}`:                         "grants[0]: packages and actions are required",
		`{"version":1,"grants":[{"packages":["A"]}]}`:                           "grants[0]: packages and actions are required",
		`{"version":1,"grants":[{"packages":["A/B"],"actions":["read"]}]}`:      "only letters",
		`{"version":1,"grants":[{"packages":["A"],"actions":["publish"]}]}`:     `unknown action "publish"`,
		`{"version":1,"grants":[{"packages":["A"],"actions":["read"],"x":1}]}`:  "field x not found",
		`{"version":1,"deny":[{"actions":["read"]}]}`:                           "deny[0]: packages or artifacts are required",
		`{"version":1,"deny":[{"packages":["HR_*"],"artifacts":["Ä"]}]}`:        "only letters",
		`{"version":1,"deny":[{"packages":["HR_*"],"except":{"roles":["x"]}}]}`: "field except not found",
	} {
		_, err := Parse([]byte(in))
		assert.ErrorContains(t, err, msg, in)
	}
	a := mustParse(t, policyScope)
	b := mustParse(t, `{"version":1,"grants":[{"packages":["SD_*"],"actions":["read"]}]}`)
	assert.NotEqual(t, a.Hash(), b.Hash(), "grants and deny are part of the hash")
	flat := mustParse(t, `{"version":1,"packages":["A"],"actions":["read"]}`)
	assert.Equal(t, "{\"version\":1,\"packages\":[\"A\"],\"actions\":[\"read\"]}", mustJSON(t, flat), "flat scopes hash as before")
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}
