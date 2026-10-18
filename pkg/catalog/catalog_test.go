package catalog

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseURI(t *testing.T) {
	p := ParseURI("ctype::AdapterVariant/cname::sap:HTTP/tp::HTTP/mp::None/direction::Receiver/version::1.22.0")
	assert.Equal(t, map[string]string{"ctype": "AdapterVariant", "cname": "sap:HTTP", "tp": "HTTP", "mp": "None", "direction": "Receiver", "version": "1.22.0"}, p)
	// values may contain "/"
	p = ParseURI("ctype::FlowstepVariant/cname::PKCS#7/CMS Signature-Content Splitter/version::1.1.1")
	assert.Equal(t, "PKCS#7/CMS Signature-Content Splitter", p["cname"])
	assert.Equal(t, "1.1.1", p["version"])
	assert.Equal(t, "ctype::FlowstepVariant/cname::EndEvent", Key("ctype::FlowstepVariant/cname::EndEvent"))
	assert.Equal(t, "", Version("ctype::FlowstepVariant/cname::EndEvent"))
}

// testdata/editor-metadata.json is a part of a real tenant's editor metadata.
func importFixture(t *testing.T) *Catalog {
	data, err := os.ReadFile(filepath.Join("testdata", "editor-metadata.json"))
	require.NoError(t, err)
	c, err := Import(data)
	require.NoError(t, err)
	return c
}

func TestImportEditorMetadata(t *testing.T) {
	c := importFixture(t)
	find := func(name, dir string) Adapter {
		for _, a := range c.Adapters {
			if a.Name == name && a.Direction == dir {
				return a
			}
		}
		t.Fatalf("no %s %s", name, dir)
		return Adapter{}
	}
	http := find("HTTP", "Receiver")
	assert.Equal(t, "1.22.0", http.Version)
	assert.Equal(t, "ctype::AdapterVariant/cname::sap:HTTP/tp::HTTP/mp::None/direction::Receiver/version::1.22.0", http.VariantURI)
	assert.Equal(t, SourceEditor, http.Source)
	assert.False(t, http.Custom)
	assert.Equal(t, "1.5.0", find("JMS", "Sender").Version, "the variant's version, not the adapter's 1.6.3")
	assert.Equal(t, "1.6.3", find("JMS", "Sender").AdapterVersion)
	assert.Equal(t, "1.1.2", find("ProcessDirect", "Sender").Version)
	assert.True(t, find("GoogleWorkspace", "Receiver").Custom, "installed from a package")

	v, ok := c.Offered("ctype::FlowstepVariant/cname::GroovyScript/version::1.1.2")
	assert.True(t, ok)
	assert.Equal(t, "2.0.0", v)
	v, _ = c.Offered("ctype::FlowstepVariant/cname::ExternalCall/version::1.0.0")
	assert.Equal(t, "1.0.4", v, "the cmdURI's version, not the shape's version attribute")
	_, ok = c.Offered("ctype::FlowstepVariant/cname::Unknown/version::1.0.0")
	assert.False(t, ok)
	// older flows write cname::JMS where the editor writes cname::sap:JMS
	v, ok = c.Offered("ctype::AdapterVariant/cname::JMS/tp::Not Applicable/mp::Not Applicable/direction::Receiver/version::1.1.0")
	assert.True(t, ok)
	assert.Equal(t, "1.6.3", v)
	_, uri, _ := c.OfferedURI("ctype::AdapterVariant/cname::ProcessDirect/tp::Not Applicable/mp::Not Applicable/direction::Sender/version::1.0.0")
	assert.Equal(t, "ctype::AdapterVariant/cname::ProcessDirect/vendor::SAP/tp::Not Applicable/mp::Not Applicable/direction::Sender/version::1.1.2", uri, "vendor SAP is optional")

	_, err := Import([]byte(`{"something":"else"}`))
	assert.Error(t, err)
}

func TestLearnKeepsEditorEntries(t *testing.T) {
	c := importFixture(t)
	n := len(c.Steps)
	c.Learn([]Use{
		{URI: "ctype::FlowstepVariant/cname::GroovyScript/version::1.1.2"}, // editor knows it: ignored
		{URI: "ctype::FlowstepVariant/cname::XmlModifier/version::1.0.0"},  // new
		{URI: "ctype::FlowstepVariant/cname::XmlModifier/version::1.1.0"},  // newer wins
		{URI: "ctype::AdapterVariant/cname::sap:Mail/tp::SMTP/mp::None/direction::Receiver/version::1.13.0"},
		{URI: "ctype::AdapterVariant/cname::JMS/tp::Not Applicable/mp::Not Applicable/direction::Receiver/version::1.6.3"}, // the editor's sap:JMS
		{URI: "ctype::AdapterVariant/cname::sap:HTTP/tp::HTTP/direction::Receiver"},                                        // no version
	})
	assert.Len(t, c.Steps, n+1)
	assert.Equal(t, []string{SourceEditor, SourceLearned}, c.Sources)
	v, ok := c.Latest("ctype::FlowstepVariant/cname::XmlModifier/version::1.0.0")
	assert.True(t, ok)
	assert.Equal(t, "1.1.0", v)
	_, ok = c.Offered("ctype::FlowstepVariant/cname::XmlModifier")
	assert.False(t, ok, "learned versions are no upper bound")
	v, _ = c.Latest("ctype::FlowstepVariant/cname::GroovyScript")
	assert.Equal(t, "2.0.0", v)
	var mail Adapter
	for _, a := range c.Adapters {
		if a.Name == "Mail" {
			mail = a
		}
	}
	assert.Equal(t, Adapter{Name: "Mail", Direction: "Receiver", TransportProtocol: "SMTP", MessageProtocol: "None",
		VariantURI: "ctype::AdapterVariant/cname::sap:Mail/tp::SMTP/mp::None/direction::Receiver/version::1.13.0", Version: "1.13.0", Source: SourceLearned}, mail)

	file := filepath.Join(t.TempDir(), "components.json")
	require.NoError(t, c.Save(file))
	back, err := Load(file)
	require.NoError(t, err)
	assert.Equal(t, c.Adapters, back.Adapters)
	assert.Equal(t, c.Steps, back.Steps)

	require.NoError(t, os.WriteFile(file, []byte(`{"schema":"other"}`), 0o644))
	_, err = Load(file)
	assert.ErrorContains(t, err, "not a component catalogue")
}

func TestCompareVersions(t *testing.T) {
	assert.Equal(t, 1, CompareVersions("1.22.0", "1.4.1"))
	assert.Equal(t, -1, CompareVersions("1.13", "1.14.0"))
	assert.Equal(t, 0, CompareVersions("2.0", "2.0.0"))
	assert.Equal(t, -1, CompareVersions("", "1.0"))
}

// Each source keeps its own date: learning later does not make the editor
// metadata look fresh.
func TestSourceDates(t *testing.T) {
	day1 := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	day2 := day1.AddDate(0, 0, 9)
	defer func(f func() time.Time) { now = f }(now)

	now = func() time.Time { return day1 }
	c := importFixture(t)
	assert.Equal(t, map[string]time.Time{SourceEditor: day1}, c.SourceDates)

	now = func() time.Time { return day2 }
	c.Learn([]Use{{URI: "ctype::FlowstepVariant/cname::XmlModifier/version::1.0.0"}})
	assert.Equal(t, day1, c.SourceDates[SourceEditor], "unchanged by learning")
	assert.Equal(t, day2, c.SourceDates[SourceLearned])

	c.Learn([]Use{{URI: "ctype::FlowstepVariant/cname::GroovyScript/version::1.1.2"}}) // known to the editor: nothing learned
	assert.Equal(t, day2, c.SourceDates[SourceLearned])

	file := filepath.Join(t.TempDir(), "components.json")
	require.NoError(t, c.Save(file))
	back, err := Load(file)
	require.NoError(t, err)
	assert.Equal(t, c.SourceDates, back.SourceDates)
}

func TestFind(t *testing.T) {
	c := importFixture(t)
	c.Learn([]Use{
		{URI: "ctype::FlowstepVariant/cname::XmlModifier/version::1.1.0"},
		{URI: "ctype::AdapterVariant/cname::JMS/tp::Not Applicable/mp::Not Applicable/direction::Receiver/version::1.0.0"},
	})
	m, ok := c.Find("ctype::AdapterVariant/cname::HTTP/tp::HTTP/mp::None/direction::Receiver/version::1.2.0")
	require.True(t, ok)
	require.NotNil(t, m.Adapter)
	assert.Equal(t, "1.22.0", m.Version(), "any version, without sap:")
	m, ok = c.Find("ctype::AdapterVariant/cname::sap:JMS/tp::Not Applicable/mp::Not Applicable/direction::Receiver")
	require.True(t, ok)
	assert.Equal(t, SourceEditor, m.Adapter.Source, "the editor entry wins")
	m, ok = c.Find("ctype::FlowstepVariant/cname::XmlModifier")
	require.True(t, ok)
	require.NotNil(t, m.Step)
	assert.Equal(t, SourceLearned, m.Step.Source)
	assert.Equal(t, "1.1.0", m.Version())
	_, ok = c.Find("ctype::FlowstepVariant/cname::Unknown")
	assert.False(t, ok)
	assert.Equal(t, "", Match{}.Version())
}
