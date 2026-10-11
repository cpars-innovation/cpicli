package cpitest

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The demo flows pass the Check (they pass it on a real tenant too).
func TestCheckDemoFlowsPass(t *testing.T) {
	for _, tier := range DemoTiers {
		m := NewTenant(t, nil)
		require.NoError(t, SeedDemo(m, tier, time.Now()))
		n := 0
		for id, a := range m.Artifacts {
			if a.Type != "Integration" || len(a.Zip) == 0 {
				continue
			}
			c, err := readArtifactZip(a.Zip)
			require.NoError(t, err)
			assert.Empty(t, checkFlow(c, DefaultComponents()), "%s on %s", id, tier)
			n++
		}
		assert.Positive(t, n)
	}
}

// rewriteZip replaces text in the .iflw of an archive and drops files.
func rewriteZip(t *testing.T, data []byte, replace map[string]string, drop string) []byte {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		if drop != "" && strings.HasSuffix(f.Name, drop) {
			continue
		}
		rc, err := f.Open()
		require.NoError(t, err)
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		if strings.HasSuffix(f.Name, ".iflw") {
			s := string(b)
			for old, nw := range replace {
				require.Contains(t, s, old)
				s = strings.ReplaceAll(s, old, nw)
			}
			b = []byte(s)
		}
		w, err := zw.Create(f.Name)
		require.NoError(t, err)
		_, _ = w.Write(b)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestCheckFindsStructuralProblems(t *testing.T) {
	m := NewTenant(t, nil)
	require.NoError(t, SeedDemo(m, "dev", time.Now()))

	notify := rewriteZip(t, m.Artifacts["Partner_Notify"].Zip, map[string]string{
		"<key>host</key><value>sftp.partner.example.com</value>": "<key>host</key><value></value>",
		"<key>credential_name</key><value>Partner_SFTP</value>":  "<key>credential_name</key><value></value>",
	}, "")
	c, err := readArtifactZip(notify)
	require.NoError(t, err)
	issues := checkFlow(c, DefaultComponents())
	require.Len(t, issues, 2)
	assert.Equal(t, "Enter a value for the 'host' attribute", issues[0].Message)
	assert.Equal(t, "ComponentMetadataEditor/eid:MessageFlow_R0/etype:MessageFlow/location:SFTP/pid:host", issues[0].SourceObject)
	assert.Equal(t, "src/main/resources/scenarioflows/integrationflow", issues[0].ResourcePath)
	assert.Equal(t, "Partner_Notify.iflw", issues[0].ResourceName)
	assert.Equal(t, "Enter a value for the 'Credential Name' attribute", issues[1].Message)

	post := rewriteZip(t, m.Artifacts["Billing_Post"].Zip, nil, "script/v2/toInvoice.groovy")
	c, err = readArtifactZip(post)
	require.NoError(t, err)
	issues = checkFlow(c, DefaultComponents())
	require.Len(t, issues, 1)
	assert.Equal(t, "The script file 'v2/toInvoice.groovy' does not exist", issues[0].Message)

	text := checkResult(issues)
	assert.True(t, strings.HasPrefix(text, "Check execution result: Failed\n[\n  {\n    \"severity\": \"Error\","), text)
	assert.Equal(t, "Check execution result: Passed", checkResult(nil))
}

// The Check rejects component versions the runtime profile does not support,
// with the messages of a real integrationcell tenant.
func TestCheckComponentVersions(t *testing.T) {
	m := NewTenant(t, nil)
	require.NoError(t, SeedDemo(m, "dev", time.Now()))
	check := func(data []byte) []checkIssue {
		c, err := readArtifactZip(data)
		require.NoError(t, err)
		return checkFlow(c, DefaultComponents())
	}
	messages := func(issues []checkIssue) []string {
		var out []string
		for _, i := range issues {
			out = append(out, i.SourceObject+" "+i.Message)
		}
		return out
	}

	// adapter version 1.0 (and a channel in the old shape: unknown variant,
	// no componentVersion, which the tenant reads as 1.0)
	route := rewriteZip(t, m.Artifacts["Orders_Route"].Zip, map[string]string{
		"<key>componentVersion</key><value>1.4</value>":                                                             "<key>componentVersion</key><value>1.0</value>",
		"ctype::AdapterVariant/cname::JMS/tp::Not Applicable/mp::Not Applicable/direction::Receiver/version::1.6.3": "ctype::AdapterVariant/cname::sap:JMS/tp::JMS/direction::Receiver",
		"<ifl:property><key>componentVersion</key><value>1.6</value></ifl:property>":                                "",
	}, "")
	assert.Equal(t, []string{
		"ComponentMetadataEditor/eid:MessageFlow_R0/etype:MessageFlow/location:HTTP This component HTTP with version 1.0 is not supported in integrationcell profile.",
		"ComponentMetadataEditor/eid:MessageFlow_R1/etype:MessageFlow/location:JMS Enter adapter details for channel",
		"ComponentMetadataEditor/eid:MessageFlow_R1/etype:MessageFlow/location:JMS This component JMS with version 1.0 is not supported in integrationcell profile.",
	}, messages(check(route)))

	// newer than the profile offers: an adapter and a step
	post := rewriteZip(t, m.Artifacts["Billing_Post"].Zip, map[string]string{
		"<key>componentVersion</key><value>1.4</value>": "<key>componentVersion</key><value>1.99</value>",
		"GroovyScript/version::2.0.0":                   "GroovyScript/version::2.1.0",
	}, "")
	got := messages(check(post))
	require.Len(t, got, 2, got)
	assert.Contains(t, got[0], "location:HTTP This component HTTP with version 1.99 is not supported in integrationcell profile.")
	assert.Contains(t, got[1], "etype:CallActivity/location:")
	assert.Contains(t, got[1], "This component GroovyScript with version 2.1.0 is not supported in integrationcell profile.")

	// through the API: validate fails; without the version check it passes
	m.Artifacts["Orders_Route"].applyContent(route)
	m.Artifacts["Orders_Route"].Zip = route
	exe := m.Executer()
	validate := func() string {
		resp, err := exe.Exec("POST", "/api/v1/ValidateIntegrationDesigntimeArtifact?Id='Orders_Route'", nil, nil)
		require.NoError(t, err)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	assert.Contains(t, validate(), "Check execution result: Failed")
	assert.Contains(t, validate(), "This component HTTP with version 1.0 is not supported in integrationcell profile.")
	m.NoComponentCheck = true
	assert.Equal(t, "Check execution result: Passed", validate())
}

// A landscape (or one of its tiers) brings its own component catalogue.
func TestLandscapeComponents(t *testing.T) {
	old := `{"schema":"cpictl.components/v1","sources":["editor"],"adapters":[
{"name":"HTTPS","direction":"Sender","variantUri":"ctype::AdapterVariant/cname::sap:HTTPS/tp::HTTPS/mp::None/direction::Sender/version::1.4.0","version":"1.4.0","source":"editor"}],"steps":[]}`
	fsys := fstest.MapFS{
		"landscape.yaml":     {Data: []byte("name: l\ncomponents: cat/old.json\ntiers: [{name: dev}, {name: prod, components: ./cat/default.json}]\n")},
		"packages/.keep":     {Data: nil},
		"cat/old.json":       {Data: []byte(old)},
		"cat/default.json":   {Data: defaultComponentsJSON},
		"cat/not-a-cat.json": {Data: []byte(`{"schema":"x"}`)},
	}
	l, err := LoadLandscape(fsys)
	require.NoError(t, err)
	m := NewTenant(t, nil)
	require.NoError(t, SeedLandscape(m, l, "dev", time.Now()))
	v, ok := m.components().Offered("ctype::AdapterVariant/cname::sap:HTTPS/tp::HTTPS/mp::None/direction::Sender/version::1.5.0")
	assert.True(t, ok)
	assert.Equal(t, "1.4.0", v, "the landscape's catalogue")
	require.NoError(t, SeedLandscape(m, l, "prod", time.Now()))
	v, _ = m.components().Offered("ctype::AdapterVariant/cname::sap:HTTPS/tp::HTTPS/mp::None/direction::Sender/version::1.5.0")
	assert.Equal(t, "1.5.3", v, "the tier's catalogue")

	fsys["landscape.yaml"] = &fstest.MapFile{Data: []byte("name: l\ncomponents: cat/not-a-cat.json\ntiers: [{name: dev}]\n")}
	_, err = LoadLandscape(fsys)
	assert.ErrorContains(t, err, "not a component catalogue")
	fsys["landscape.yaml"] = &fstest.MapFile{Data: []byte("name: l\ncomponents: cat/missing.json\ntiers: [{name: dev}]\n")}
	_, err = LoadLandscape(fsys)
	assert.ErrorContains(t, err, "components")
}
