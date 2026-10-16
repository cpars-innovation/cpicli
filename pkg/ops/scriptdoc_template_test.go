package ops

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cpars-innovation/cpicli/pkg/catalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const documentedScript = `import com.sap.gateway.ip.core.customdev.util.Message

/**
 * Formats the SAP date in header SAP_Date as ISO 8601 into header ISO_Date.
 *
 * @reads    header SAP_Date (yyyyMMdd)
 * @writes   header ISO_Date
 * @body     unchanged
 * @keywords date, format,
 *           ISO 8601
 */
def Message formatSapDate(Message message) {
    def d = message.getHeader("SAP_Date", String)
    message.setHeader("ISO_Date", d)
    message
}

private String pad(String s) { s }
`

func TestScriptDocTags(t *testing.T) {
	doc := ParseScriptDoc([]byte(documentedScript))
	require.Len(t, doc.Functions, 2)
	f := doc.Functions[0]
	assert.Equal(t, "Formats the SAP date in header SAP_Date as ISO 8601 into header ISO_Date.", f.Summary)
	assert.Equal(t, []string{"header SAP_Date (yyyyMMdd)"}, f.Tags["reads"])
	assert.Equal(t, []string{"date, format, ISO 8601"}, f.Tags["keywords"], "a tag continues on the next line")
	assert.Equal(t, []string{"date", "format", "ISO 8601"}, doc.Keywords())

	reads, writes := ScriptAccess([]byte(documentedScript))
	assert.Equal(t, []string{"header SAP_Date"}, reads)
	assert.Equal(t, []string{"header ISO_Date"}, writes)

	assert.Empty(t, CheckScriptDoc([]byte(documentedScript), true), "the private helper needs no comment")
	assert.Empty(t, CheckScriptDoc([]byte(documentedScript), false))
}

func TestCheckScriptDoc(t *testing.T) {
	poor := `/** TODO */
def Message formatDate(Message message) {
    message.setProperty("isoDate", message.getHeader("SAP_Date", String))
}

def Message other(Message message) { message }
`
	issues := CheckScriptDoc([]byte(poor), true)
	kinds := map[string]string{}
	for _, i := range issues {
		kinds[i.Kind+"/"+i.Function] = i.Message
	}
	assert.Contains(t, kinds, "placeholder/formatDate")
	assert.Contains(t, kinds, "missing/other")
	assert.Equal(t, "the comments do not name header SAP_Date, property isoDate (use @reads / @writes)", kinds["undocumented/"])
	assert.Contains(t, kinds, "no-keywords/")
	assert.Equal(t, DocIncomplete, DocStatusOf(issues, 2))

	none := "def Message a(Message m) { m }\ndef Message b(Message m) { m }\n"
	assert.Equal(t, DocMissing, DocStatusOf(CheckScriptDoc([]byte(none), true), 2))
	assert.Equal(t, DocMissing, DocStatusOf(CheckScriptDoc([]byte(none), false), 1))

	flow := CheckScriptDoc([]byte("// Script.\ndef Message processData(Message m) { m }\n"), false)
	require.Len(t, flow, 1)
	assert.Equal(t, DocIssuePlaceholder, flow[0].Kind)
	assert.Empty(t, CheckScriptDoc([]byte("// Writes the order number to the log.\ndef Message processData(Message m) { m }\n"), false),
		"flow scripts need only a first sentence")
}

func TestPoorSummary(t *testing.T) {
	for _, s := range []string{"", "TODO", "Script.", "Groovy script", "Does stuff.", "Format date.", "This is a TODO for later.", "..."} {
		assert.True(t, PoorSummary(s, ""), s)
	}
	assert.True(t, PoorSummary("format sap date", "formatSapDate"), "only the function name")
	assert.False(t, PoorSummary("Formats the SAP date as ISO 8601.", "formatSapDate"))
}

func TestScriptShape(t *testing.T) {
	a := "def x() {\n  return 1 // one\n}\n"
	b := "/* header */\ndef x() { return 1 }"
	assert.Equal(t, ScriptShape([]byte(a)), ScriptShape([]byte(b)))
	assert.NotEqual(t, ScriptShape([]byte(a)), ScriptShape([]byte("def x() { return 2 }")))
	assert.Len(t, ScriptShape([]byte(a)), 12)
}

func TestCheckScriptFile(t *testing.T) {
	dir := t.TempDir()
	coll := filepath.Join(dir, "Pkg", "Coll")
	require.NoError(t, os.MkdirAll(filepath.Join(coll, "META-INF"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(coll, ResourcesDir, "script"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(coll, "META-INF", "MANIFEST.MF"), []byte("SAP-BundleType: ScriptCollection\n"), 0o644))
	file := filepath.Join(coll, ResourcesDir, "script", "Dates.groovy")
	require.NoError(t, os.WriteFile(file, []byte(documentedScript), 0o644))

	r, err := CheckScriptFile(file)
	require.NoError(t, err)
	assert.True(t, r.Collection)
	assert.Equal(t, DocOK, r.Status)
	assert.Equal(t, []string{"date", "format", "ISO 8601"}, r.Keywords)

	loose := filepath.Join(dir, "loose.groovy")
	require.NoError(t, os.WriteFile(loose, []byte("def Message processData(Message m) { m }\n"), 0o644))
	r, err = CheckScriptFile(loose)
	require.NoError(t, err)
	assert.False(t, r.Collection)
	assert.Equal(t, DocMissing, r.Status)
	assert.True(t, IsGroovyFile(loose))
	assert.False(t, IsGroovyFile("x.js"))
}

// Script nodes say what reuse needs: collection scripts are reusable, a
// flow script used by two flows has usages 2, multi-word search finds the
// summary's words.
func TestGraphScriptReuse(t *testing.T) {
	coll := analyzeScript("Dates.groovy", []byte(documentedScript), true)
	local := analyzeScript("Map.groovy", []byte("// Maps the order to the S/4 format.\ndef Message processData(Message m) { m }\n"), false)
	flow := func(id string) IFlowFacts {
		return IFlowFacts{ID: id, PackageID: "Sales", Triggers: []Trigger{}, Receivers: []Trigger{}, Scripts: []ScriptFacts{local},
			CollectionScripts: []CollectionScriptRef{{Collection: "Common", Script: "Dates.groovy", Function: "formatSapDate"}}}
	}
	g := BuildGraph(&Discovery{IFlows: []IFlowFacts{flow("A"), flow("B")},
		ScriptCollections: []ScriptCollectionFacts{{ID: "Common", PackageID: "Shared", Scripts: []ScriptFacts{coll}}}})
	g.buildIndex()

	n, ok := g.Node("script:Common/Dates.groovy")
	require.True(t, ok)
	assert.Equal(t, "true", n.Attrs["reusable"])
	assert.Equal(t, "ok", n.Attrs["docStatus"])
	assert.Equal(t, "date, format, ISO 8601", n.Attrs["keywords"])
	assert.Equal(t, "header SAP_Date", n.Attrs["reads"])
	assert.Equal(t, "header ISO_Date", n.Attrs["writes"])
	assert.Equal(t, "2", n.Attrs["usages"])
	assert.Equal(t, ScriptShape([]byte(documentedScript)), n.Attrs["shape"])

	m, ok := g.Node("script:" + local.Hash)
	require.True(t, ok)
	assert.Equal(t, "false", m.Attrs["reusable"])
	assert.Equal(t, "2", m.Attrs["usages"], "the same script in two flows is one node")

	res, err := g.Search("format date", []string{NodeScript}, 0)
	require.NoError(t, err)
	require.Equal(t, 1, res.Total)
	assert.Equal(t, "script:Common/Dates.groovy", res.Matches[0].ID)
	res, err = g.Search("format nothing", nil, 0)
	require.NoError(t, err)
	assert.Zero(t, res.Total, "every word has to match")
}

// Adapters and steps are component nodes (all versions in one node), the
// edge carries the flow's version; the catalogue's editor versions mark
// outdated flows. Mappings and value mappings are nodes too.
func TestGraphComponentsAndMappings(t *testing.T) {
	http := func(v string) string {
		return "ctype::AdapterVariant/cname::sap:HTTP/tp::HTTP/mp::None/direction::Receiver/version::" + v
	}
	odata := "ctype::AdapterVariant/cname::sap:OData/tp::HTTP/mp::OData V2/direction::Receiver/version::1.25.0"
	groovy := "ctype::FlowstepVariant/cname::GroovyScript/version::1.1.2"
	flow := func(id string, comps ...string) IFlowFacts {
		return IFlowFacts{ID: id, PackageID: "Sales", Triggers: []Trigger{}, Receivers: []Trigger{}, Components: comps}
	}
	a := flow("A", http("1.17.1"), odata, groovy)
	a.Mappings = []MappingRef{{Name: "Orders.mmap", Kind: "message mapping", Hash: "abc123"}, {Artifact: "Shared_OrderMapping", Step: "Map order"}}
	a.ValueMappings = []string{"SAP/Country"}
	b := flow("B", http("1.14.0"))
	b.Mappings = []MappingRef{{Name: "Orders.mmap", Kind: "message mapping", Hash: "abc123"}}
	g := BuildGraph(&Discovery{IFlows: []IFlowFacts{a, b}})
	edges := edgeSet(g.Edges)

	assert.Contains(t, edges, "iflow:A -uses_component-> component:adapter/HTTP/Receiver")
	assert.Contains(t, edges, "iflow:A -uses_component-> component:adapter/OData/OData V2/Receiver")
	assert.Contains(t, edges, "iflow:A -uses_component-> component:step/GroovyScript")
	assert.Contains(t, edges, "iflow:A -uses_mapping-> mapping:abc123")
	assert.Contains(t, edges, "iflow:B -uses_mapping-> mapping:abc123", "the same mapping file in two flows is one node")
	assert.Contains(t, edges, "iflow:A -uses_mapping-> mapping:Shared_OrderMapping")
	assert.Contains(t, edges, "iflow:A -uses_value_mapping-> value_mapping:SAP/Country")

	n, ok := g.Node("component:adapter/HTTP/Receiver")
	require.True(t, ok)
	assert.Equal(t, "HTTP receiver", n.Name)
	assert.Equal(t, "1.17.1: 1, 1.14.0: 1", n.Attrs["versions"])
	assert.Equal(t, "1.17.1", n.Attrs["newest"])
	assert.Equal(t, "1", n.Attrs["behind"])
	for _, e := range g.Edges {
		if e.From == "iflow:B" && e.Type == EdgeUsesComponent {
			assert.Equal(t, "1.14.0", e.Version)
		}
	}
	o, _ := g.Node("component:adapter/OData/OData V2/Receiver")
	assert.Equal(t, "OData receiver (OData V2)", o.Name)

	g.MarkOfferedVersions(&catalog.Catalog{Adapters: []catalog.Adapter{
		{Name: "HTTP", Direction: "Receiver", VariantURI: http("1.18.0"), Version: "1.18.0", Source: catalog.SourceEditor}}})
	n, _ = g.Node("component:adapter/HTTP/Receiver")
	assert.Equal(t, "1.18.0", n.Attrs["offered"])
	assert.Equal(t, "2", n.Attrs["outdated"])

	res, err := g.Search("http receiver", []string{NodeComponent}, 0)
	require.NoError(t, err)
	require.NotZero(t, res.Total)
	assert.Equal(t, "component:adapter/HTTP/Receiver", res.Matches[0].ID)
}
