package lint

import (
	"context"
	"encoding/xml"
	"fmt"
	"github.com/cpars-innovation/cpicli/pkg/iflow"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tStep is a step of a test flow.
type tStep struct {
	id, name, kind string // kind: script, modifier, xml2json, json2xml, router
	props          map[string]string
	conditions     []string // router: one branch per condition
	unconnected    bool
}

type tFlow struct {
	pkg, id    string
	steps      []tStep
	scripts    map[string]string
	files      map[string]string // other files below src/main/resources
	params     string
	receiver   string // literal HTTP receiver address
	collection bool   // a script collection instead of a flow
	versionOf  map[string]string
}

func prop(k, v string) string {
	var esc strings.Builder
	_ = xml.EscapeText(&esc, []byte(v))
	return fmt.Sprintf("<ifl:property><key>%s</key><value>%s</value></ifl:property>", k, esc.String())
}

func props(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(prop(k, m[k]))
	}
	return "<bpmn2:extensionElements>" + b.String() + "</bpmn2:extensionElements>"
}

func (f tFlow) model() string {
	var els, flows, shapes strings.Builder
	prev := "Start"
	n := 0
	flow := func(src, tgt, cond string) string {
		n++
		id := fmt.Sprintf("SequenceFlow_%d", n)
		c := ""
		if cond != "" {
			c = `<bpmn2:conditionExpression xsi:type="bpmn2:tFormalExpression"><![CDATA[` + cond + `]]></bpmn2:conditionExpression>`
		}
		flows.WriteString(fmt.Sprintf(`<bpmn2:sequenceFlow id="%s" sourceRef="%s" targetRef="%s">%s</bpmn2:sequenceFlow>`, id, src, tgt, c))
		shapes.WriteString(fmt.Sprintf(`<bpmndi:BPMNEdge bpmnElement="%s" id="BPMNEdge_%s"/>`, id, id))
		return id
	}
	outOf := map[string][]string{}
	inOf := map[string][]string{}
	connect := func(src, tgt, cond string) {
		id := flow(src, tgt, cond)
		outOf[src] = append(outOf[src], id)
		inOf[tgt] = append(inOf[tgt], id)
	}
	for _, s := range f.steps {
		if s.unconnected {
			continue
		}
		if s.kind == "router" {
			connect(prev, s.id, "")
			for _, c := range s.conditions {
				connect(s.id, "End", c)
			}
			prev = ""
			continue
		}
		connect(prev, s.id, "")
		prev = s.id
	}
	if prev != "" {
		connect(prev, "End", "")
	}
	ref := func(id string) string {
		var b strings.Builder
		for _, x := range inOf[id] {
			b.WriteString("<bpmn2:incoming>" + x + "</bpmn2:incoming>")
		}
		for _, x := range outOf[id] {
			b.WriteString("<bpmn2:outgoing>" + x + "</bpmn2:outgoing>")
		}
		return b.String()
	}
	for _, s := range f.steps {
		p := map[string]string{}
		tag := "callActivity"
		switch s.kind {
		case "script":
			p = map[string]string{"activityType": "Script", "subActivityType": "GroovyScript", "scriptBundleId": "", "scriptFunction": "",
				"cmdVariantUri": "ctype::FlowstepVariant/cname::GroovyScript/version::1.1.2", "componentVersion": "1.1"}
		case "modifier":
			p = map[string]string{"activityType": "Enricher", "headerTable": "", "propertyTable": "", "wrapContent": "", "bodyType": "expression",
				"cmdVariantUri": "ctype::FlowstepVariant/cname::Enricher/version::1.5.1", "componentVersion": "1.5"}
		case "xml2json":
			p = map[string]string{"activityType": "XmlToJsonConverter", "cmdVariantUri": "ctype::FlowstepVariant/cname::XmlToJsonConverter/version::1.0.8", "componentVersion": "1.0"}
		case "json2xml":
			p = map[string]string{"activityType": "JsonToXmlConverter", "cmdVariantUri": "ctype::FlowstepVariant/cname::JsonToXmlConverter/version::1.0.9", "componentVersion": "1.0"}
		case "router":
			tag = "exclusiveGateway"
			p = map[string]string{"cmdVariantUri": "ctype::FlowstepVariant/cname::ExclusiveGateway/version::1.1.2", "componentVersion": "1.1"}
		}
		for k, v := range s.props {
			p[k] = v
		}
		els.WriteString(fmt.Sprintf(`<bpmn2:%s id="%s" name="%s">%s%s</bpmn2:%s>`, tag, s.id, s.name, props(p), ref(s.id), tag))
		shapes.WriteString(fmt.Sprintf(`<bpmndi:BPMNShape bpmnElement="%s" id="BPMNShape_%s"/>`, s.id, s.id))
	}
	receiver := ""
	if f.receiver != "" {
		receiver = `<bpmn2:messageFlow id="MessageFlow_R" name="HTTP" sourceRef="Start" targetRef="Participant_R">` + props(map[string]string{
			"ComponentType": "HTTP", "direction": "Receiver", "httpAddressWithoutQuery": f.receiver, "componentVersion": "1.9",
			"cmdVariantUri": "ctype::AdapterVariant/cname::sap:HTTP/tp::HTTP/mp::None/direction::Receiver/version::1.9.0"}) + `</bpmn2:messageFlow>`
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn2:definitions xmlns:bpmn2="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" xmlns:ifl="http:///com.sap.ifl.model/Ifl.xsd" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" id="Definitions_1">
<bpmn2:collaboration id="Collaboration_1" name="Default Collaboration">` + props(map[string]string{"log": "All events"}) + `
<bpmn2:participant id="Participant_R" name="Receiver"/>
<bpmn2:participant id="Participant_Process_1" name="Integration Process" processRef="Process_1"/>` + receiver + `
</bpmn2:collaboration>
<bpmn2:process id="Process_1" name="Integration Process">` + props(map[string]string{"cmdVariantUri": "ctype::FlowElementVariant/cname::IntegrationProcess/version::1.2.0"}) + `
<bpmn2:startEvent id="Start" name="Start">` + ref("Start") + `</bpmn2:startEvent>
` + els.String() + `
<bpmn2:endEvent id="End" name="End">` + ref("End") + `</bpmn2:endEvent>
` + flows.String() + `
</bpmn2:process>
<bpmndi:BPMNDiagram id="BPMNDiagram_1"><bpmndi:BPMNPlane bpmnElement="Collaboration_1" id="BPMNPlane_1">` + shapes.String() + `</bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn2:definitions>
`
}

func (f tFlow) write(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, f.pkg, f.id)
	typ := "IntegrationFlow"
	if f.collection {
		typ = "ScriptCollection"
	}
	files := map[string]string{
		"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\nBundle-SymbolicName: " + f.id + "; singleton:=true\nBundle-Version: 1.0.0\nSAP-BundleType: " + typ + "\n",
	}
	if !f.collection {
		files["src/main/resources/scenarioflows/integrationflow/"+f.id+".iflw"] = f.model()
	}
	for name, s := range f.scripts {
		files["src/main/resources/script/"+name] = s
	}
	for name, s := range f.files {
		files["src/main/resources/"+name] = s
	}
	if f.params != "" {
		files["src/main/resources/parameters.prop"] = f.params
	}
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	return dir
}

func tScript(id, name, script string) tStep {
	return tStep{id: id, name: name, kind: "script", props: map[string]string{"script": script}}
}

const logScript = `import com.sap.gateway.ip.core.customdev.util.Message
def Message processData(Message message) {
    def log = messageLogFactory.getMessageLog(message)
    log.setStringProperty("flow", message.getProperty("SAP_MessageProcessingLogID"))
    return message
}
`

func lintRepo(t *testing.T) string {
	root := t.TempDir()
	modifierWith := func(id, name string) tStep {
		return tStep{id: id, name: name, kind: "modifier", props: map[string]string{"headerTable": `<row><cell id='Name'>X</cell></row>`}}
	}
	lookup := "def map = [\n"
	for i := 0; i < 12; i++ {
		lookup += fmt.Sprintf("  \"K%d\": \"V%d\",\n", i, i)
	}
	lookup += "]\n"
	flows := []tFlow{
		{pkg: "P1", id: "FlowA", steps: []tStep{tScript("S1", "Log", "Log.groovy"), {id: "Noop", name: "Nothing", kind: "modifier"}, modifierWith("M2", "Set X"),
			{id: "Orphan", name: "Groovy Script 1", kind: "script", props: map[string]string{"script": "Log.groovy"}, unconnected: true}},
			scripts: map[string]string{"Log.groovy": logScript, "Old.groovy": "println 'old'\n"},
			files:   map[string]string{"mapping/Old.xsl": "<xsl:stylesheet/>"},
			params:  "Used=1\nUnused=2\n", receiver: "{{Used}}"},
		{pkg: "P1", id: "FlowB", steps: []tStep{tScript("S1", "Log", "Log.groovy"), tScript("S2", "Common", "Common.groovy"),
			{id: "R", name: "Route by type", kind: "router", conditions: []string{"${header.Type} = 'A'", "${header.Type} = 'B'", "${header.Type} = 'C'", "${header.Type} = 'D'", "${header.Type} = 'E'"}}},
			scripts: map[string]string{"Log.groovy": strings.ReplaceAll(logScript, "\n", "\r\n"), "Common.groovy": "def x = 1\n"}, receiver: "https://fixed.example/api"},
		{pkg: "P1", id: "FlowC", steps: []tStep{tScript("S1", "Log", "Log.groovy"), tScript("S2", "Bad", "Bad.groovy"), tScript("S3", "Set", "Set.groovy"), tScript("S4", "Lookup", "Lookup.groovy")},
			scripts: map[string]string{"Log.groovy": logScript + "\n\n", "Set.groovy": "import com.sap.gateway.ip.core.customdev.util.Message\ndef Message processData(Message message) {\n  message.setHeader(\"a\", \"b\")\n  return message\n}\n",
				"Bad.groovy":    "def body = message.getBody(String)\nprintln body\ntry { x() } catch (Exception e) { }\ndef password = \"s3cret!\"\ndef u = \"https://api.example.com/x\"\nmessageLog.addAttachmentAsString(\"payload\", body, \"text/plain\")\n",
				"Lookup.groovy": lookup}},
		{pkg: "P1", id: "P1_Existing", collection: true, scripts: map[string]string{"Common.groovy": "def x = 1\n"}},
		{pkg: "P2", id: "FlowD", steps: []tStep{tScript("S1", "Log", "Log.groovy"), {id: "J1", name: "To JSON", kind: "xml2json"}, {id: "J2", name: "To XML", kind: "json2xml"},
			{id: "S0", name: "Old version", kind: "script", props: map[string]string{"script": "Log.groovy", "componentVersion": "1.0"}}},
			scripts: map[string]string{"Log.groovy": logScript}},
	}
	for _, f := range flows {
		f.write(t, root)
	}
	return root
}

func findingsOf(res *Result, rule, artifact string) []Finding {
	var out []Finding
	for _, f := range res.Findings {
		if f.Rule == rule && f.Artifact == artifact {
			out = append(out, f)
		}
	}
	return out
}

func TestRules(t *testing.T) {
	root := lintRepo(t)
	res, err := Run(context.Background(), Options{Dir: root})
	require.NoError(t, err)
	assert.Equal(t, 5, res.Artifacts)
	has := func(rule, artifact string, n int) {
		t.Helper()
		assert.Len(t, findingsOf(res, rule, artifact), n, "%s in %s: %v", rule, artifact, findingsOf(res, rule, artifact))
	}
	// reuse: Log.groovy in four flows (line endings and trailing lines do not count)
	for _, id := range []string{"FlowA", "FlowB", "FlowC", "FlowD"} {
		has("duplicate-script", id, 1)
	}
	dup := findingsOf(res, "duplicate-script", "FlowD")[0]
	assert.Contains(t, dup.Suggestion, "P2_Scripts", "no cross-package references by default: the package's collection")
	assert.True(t, dup.Fixable)
	has("use-script-collection", "FlowB", 1)
	has("duplicate-script", "P1_Existing", 0)
	// partner directory
	has("router-literals", "FlowB", 1)
	has("lookup-table-in-script", "FlowC", 1)
	// dead weight
	has("unconnected-step", "FlowA", 1)
	has("unused-script", "FlowA", 1)
	has("unused-resource", "FlowA", 1)
	has("unused-parameter", "FlowA", 1)
	assert.Contains(t, findingsOf(res, "unused-parameter", "FlowA")[0].Message, "Unused")
	has("noop-content-modifier", "FlowA", 1)
	// simplify
	has("converter-roundtrip", "FlowD", 1)
	has("trivial-script", "FlowC", 1)
	has("consecutive-content-modifiers", "FlowA", 1)
	// robustness, performance, configuration, hygiene
	has("no-exception-subprocess", "FlowA", 1)
	has("swallowed-exception", "FlowC", 1)
	has("body-as-string", "FlowC", 1)
	has("payload-attachment", "FlowC", 1)
	has("println", "FlowC", 1)
	has("hardcoded-endpoint", "FlowB", 1)
	has("hardcoded-endpoint", "FlowA", 0)
	has("hardcoded-url-in-script", "FlowC", 1)
	has("hardcoded-secret", "FlowC", 1)
	assert.Equal(t, SevError, findingsOf(res, "hardcoded-secret", "FlowC")[0].Severity)
	has("outdated-component", "FlowD", 1)
	has("default-step-name", "FlowA", 1)
	lines := 0
	for _, f := range res.Findings {
		assert.NotEmpty(t, f.Fingerprint)
		assert.NotEmpty(t, f.Group)
		if f.Line == 0 {
			continue
		}
		lines++
		data, err := os.ReadFile(filepath.Join(root, f.Path, f.File))
		require.NoError(t, err)
		text := strings.Split(string(data), "\n")
		require.Less(t, f.Line-1, len(text))
		assert.Contains(t, text[f.Line-1], `id="`+f.Element+`"`, "%s: line %d", f.Rule, f.Line)
	}
	assert.NotZero(t, lines, "findings on flow steps carry their line")
	assert.Zero(t, findingsOf(res, "unused-resource", "FlowA")[0].Line, "no element: no line")
}

func TestConfigScopeAndBaseline(t *testing.T) {
	root := lintRepo(t)
	cfgPath := filepath.Join(t.TempDir(), "lint.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`rules:
  println: off
  router-literals: error
settings:
  routerMinValues: 6
scriptCollections:
  crossPackage: true
  sharedPackage: Shared
  sharedCollection: Shared_Logging
naming:
  iflowId: "^Flow[A-C]$"
ignore:
  - artifact: FlowC
    rule: hardcoded-*
`), 0o644))
	cfg, err := LoadConfig(cfgPath)
	require.NoError(t, err)
	res, err := Run(context.Background(), Options{Dir: root, Config: cfg})
	require.NoError(t, err)
	assert.Empty(t, findingsOf(res, "println", "FlowC"), "off")
	assert.Empty(t, findingsOf(res, "router-literals", "FlowB"), "5 values < 6")
	assert.Empty(t, findingsOf(res, "hardcoded-secret", "FlowC"), "ignored")
	assert.Len(t, findingsOf(res, "naming", "FlowD"), 1)
	assert.Contains(t, findingsOf(res, "duplicate-script", "FlowD")[0].Suggestion, "Shared_Logging", "cross-package: the shared collection")

	// unknown rule
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	require.NoError(t, os.WriteFile(bad, []byte("rules:\n  nope: off\n"), 0o644))
	_, err = LoadConfig(bad)
	assert.Error(t, err)

	// scope: only FlowA is reported, cross-flow rules still see every flow
	res, err = Run(context.Background(), Options{Dir: root, Artifacts: []string{"FlowA"}})
	require.NoError(t, err)
	for _, f := range res.Findings {
		assert.Equal(t, "FlowA", f.Artifact)
	}
	assert.Len(t, findingsOf(res, "duplicate-script", "FlowA"), 1)

	// baseline: known findings are not new
	baseline := filepath.Join(t.TempDir(), "baseline.json")
	res, err = Run(context.Background(), Options{Dir: root})
	require.NoError(t, err)
	require.NoError(t, WriteBaseline(baseline, res.Findings))
	again, err := Run(context.Background(), Options{Dir: root, Baseline: baseline})
	require.NoError(t, err)
	assert.Empty(t, again.New)
	assert.Equal(t, res.Counts, again.Counts)
	// a new finding is new
	require.NoError(t, os.WriteFile(filepath.Join(root, "P2", "FlowD", "src", "main", "resources", "script", "Log.groovy"), []byte(logScript+"println 'x'\n"), 0o644))
	again, err = Run(context.Background(), Options{Dir: root, Baseline: baseline})
	require.NoError(t, err)
	assert.Equal(t, 1, again.New[SevInfo], "println in FlowD")
}

func TestFix(t *testing.T) {
	root := lintRepo(t)
	before := readTree(t, root)
	res, err := Fix(context.Background(), Options{Dir: root}, FixOptions{DryRun: true})
	require.NoError(t, err)
	assert.NotEmpty(t, res.Changes)
	assert.Equal(t, before, readTree(t, root), "dry run")

	res, err = Fix(context.Background(), Options{Dir: root}, FixOptions{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"P1/P1_Scripts", "P2/P2_Scripts", "P1/P1_Existing"}, res.Collections)
	assert.Contains(t, res.NextSteps[0], "deploy config")

	// the package collection holds the script once, the flows reference it
	coll, err := os.ReadFile(filepath.Join(root, "P1", "P1_Scripts", "src", "main", "resources", "script", "Log.groovy"))
	require.NoError(t, err)
	assert.Equal(t, normalizeScript([]byte(logScript))+"\n", string(coll))
	mf, _ := os.ReadFile(filepath.Join(root, "P1", "P1_Scripts", "META-INF", "MANIFEST.MF"))
	assert.Contains(t, string(mf), "SAP-BundleType: ScriptCollection")
	for _, id := range []string{"FlowA", "FlowB", "FlowC"} {
		model, _ := os.ReadFile(filepath.Join(root, "P1", id, "src", "main", "resources", "scenarioflows", "integrationflow", id+".iflw"))
		assert.Contains(t, string(model), "<value>P1_Scripts</value>", id)
		assert.NoFileExists(t, filepath.Join(root, "P1", id, "src", "main", "resources", "script", "Log.groovy"), id)
	}
	modelB, _ := os.ReadFile(filepath.Join(root, "P1", "FlowB", "src", "main", "resources", "scenarioflows", "integrationflow", "FlowB.iflw"))
	assert.Contains(t, string(modelB), "<value>P1_Existing</value>", "the existing collection is used")
	assert.FileExists(t, filepath.Join(root, "P2", "P2_Scripts", "src", "main", "resources", "script", "Log.groovy"))
	// dead weight
	assert.NoFileExists(t, filepath.Join(root, "P1", "FlowA", "src", "main", "resources", "script", "Old.groovy"))
	modelA, _ := os.ReadFile(filepath.Join(root, "P1", "FlowA", "src", "main", "resources", "scenarioflows", "integrationflow", "FlowA.iflw"))
	assert.NotContains(t, string(modelA), `id="Orphan"`)
	assert.NotContains(t, string(modelA), `bpmnElement="Orphan"`, "the diagram shape goes too")
	assert.Contains(t, string(modelA), `id="Noop"`, "no-op modifiers only on request")

	// lint again: the fixed findings are gone, the models are intact
	after, err := Run(context.Background(), Options{Dir: root})
	require.NoError(t, err)
	for _, rule := range []string{"duplicate-script", "use-script-collection", "unused-script", "unconnected-step", "missing-script-collection"} {
		for _, f := range after.Findings {
			assert.NotEqual(t, rule, f.Rule, "%s left: %s", rule, f.Message)
		}
	}

	// on request: remove the no-op modifier and connect its neighbours
	_, err = Fix(context.Background(), Options{Dir: root, Artifacts: []string{"FlowA"}}, FixOptions{Rules: []string{"noop-content-modifier"}})
	require.NoError(t, err)
	modelA, _ = os.ReadFile(filepath.Join(root, "P1", "FlowA", "src", "main", "resources", "scenarioflows", "integrationflow", "FlowA.iflw"))
	assert.NotContains(t, string(modelA), `id="Noop"`)
	m, err := iflow.Parse(modelA)
	require.NoError(t, err)
	assert.Empty(t, m.Unreachable())
	next := m.Next(m.Elements["S1"])
	require.NotNil(t, next)
	assert.Equal(t, "M2", next.ID, "Log -> Set X")

	// the layout rule draws the diagram (the fixture's shapes have no bounds)
	before = readTree(t, root)
	res, err = Fix(context.Background(), Options{Dir: root, Artifacts: []string{"FlowA"}}, FixOptions{Rules: []string{"layout"}})
	require.NoError(t, err)
	require.Len(t, res.Changes, 1)
	assert.Equal(t, "layout", res.Changes[0].Rule)
	modelA, _ = os.ReadFile(filepath.Join(root, "P1", "FlowA", "src", "main", "resources", "scenarioflows", "integrationflow", "FlowA.iflw"))
	assert.Contains(t, string(modelA), "<dc:Bounds")
	assert.Contains(t, string(modelA), `xmlns:dc="http://www.omg.org/spec/DD/20100524/DC"`, "prefix declared")
	laid, err := iflow.Parse(modelA)
	require.NoError(t, err)
	assert.Equal(t, len(m.Elements), len(laid.Elements), "only the diagram changed")
	issues, err := iflow.CheckLayout(modelA)
	require.NoError(t, err)
	assert.Empty(t, issues)
	after, err = Run(context.Background(), Options{Dir: root, Artifacts: []string{"FlowA"}})
	require.NoError(t, err)
	for _, f := range after.Findings {
		assert.NotEqual(t, "layout", f.Rule, f.Message)
	}
	changed := 0
	for p, content := range readTree(t, root) {
		if before[p] != content {
			changed++
		}
	}
	assert.Equal(t, 1, changed, "only FlowA's model")

	_, err = Fix(context.Background(), Options{Dir: root}, FixOptions{Rules: []string{"println"}})
	assert.Error(t, err, "no fix for println")
}

func readTree(t *testing.T, root string) map[string]string {
	out := map[string]string{}
	require.NoError(t, filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		out[p] = string(data)
		return err
	}))
	return out
}
