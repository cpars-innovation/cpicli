package iflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func readDemoFlow(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "cpitest", "landscapes", "demo", "packages", "Orders", "Orders_Route",
		"src", "main", "resources", "scenarioflows", "integrationflow", "Orders_Route.iflw"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// replaceOnce replaces old, which must occur exactly once.
func replaceOnce(t *testing.T, s, old, new string) string {
	t.Helper()
	if n := strings.Count(s, old); n != 1 {
		t.Fatalf("%q occurs %d times", old, n)
	}
	return strings.Replace(s, old, new, 1)
}

// demoVariantB is the demo flow with an adapter address and credential
// changed, a step renamed, a script step and two sequence flows added, a
// sequence flow removed, the end event moved and the log level changed.
func demoVariantB(t *testing.T, a string) string {
	b := replaceOnce(t, a, "<value>/orders/route</value>", "<value>/orders/route/v2</value>")
	b = replaceOnce(t, b, "<value>ERP_User</value>", "<value>ERP_User_2</value>")
	b = replaceOnce(t, b, `name="Call ERP"`, `name="Call S/4HANA"`)
	b = replaceOnce(t, b, "<value>All events</value>", "<value>Errors only</value>")
	b = replaceOnce(t, b, `<bpmn2:sequenceFlow id="SequenceFlow_1" sourceRef="ServiceTask_0" targetRef="EndEvent_1"/>`,
		`<bpmn2:callActivity id="CallActivity_1" name="Map"><bpmn2:extensionElements><ifl:property><key>activityType</key><value>Script</value></ifl:property><ifl:property><key>cmdVariantUri</key><value>ctype::FlowstepVariant/cname::GroovyScript/version::1.1.2</value></ifl:property><ifl:property><key>script</key><value>map.groovy</value></ifl:property></bpmn2:extensionElements></bpmn2:callActivity>`+
			`<bpmn2:sequenceFlow id="SequenceFlow_2" sourceRef="ServiceTask_0" targetRef="CallActivity_1"/>`+
			`<bpmn2:sequenceFlow id="SequenceFlow_3" sourceRef="CallActivity_1" targetRef="EndEvent_1"/>`)
	b = replaceOnce(t, b, `<dc:Bounds height="32.0" width="32.0" x="522.0" y="124.0"/>`, `<dc:Bounds height="32.0" width="32.0" x="642.0" y="124.0"/>`)
	b = replaceOnce(t, b, `<bpmndi:BPMNEdge bpmnElement="SequenceFlow_1" id="BPMNEdge_SequenceFlow_1" sourceElement="BPMNShape_ServiceTask_0" targetElement="BPMNShape_EndEvent_1"><di:waypoint x="412.0" xsi:type="dc:Point" y="140.0"/><di:waypoint x="538.0" xsi:type="dc:Point" y="140.0"/></bpmndi:BPMNEdge>`,
		`<bpmndi:BPMNShape bpmnElement="CallActivity_1" id="BPMNShape_CallActivity_1"><dc:Bounds height="60.0" width="100.0" x="502.0" y="110.0"/></bpmndi:BPMNShape>`)
	return b
}

func TestDiffModels(t *testing.T) {
	a := readDemoFlow(t)
	b := demoVariantB(t, a)
	d, err := DiffModels([]byte(a), []byte(b))
	if err != nil {
		t.Fatal(err)
	}
	want := &ModelDiff{
		Elements: []ElementChange{
			{ID: "CallActivity_1", Type: "callActivity", Kind: "GroovyScript", Name: "Map", Change: ChangeAdded, Properties: []PropertyChange{
				{Key: "@container", Change: ChangeAdded, B: "Process_1"},
				{Key: "activityType", Change: ChangeAdded, B: "Script"},
				{Key: "cmdVariantUri", Change: ChangeAdded, B: "ctype::FlowstepVariant/cname::GroovyScript/version::1.1.2"},
				{Key: "script", Change: ChangeAdded, B: "map.groovy"},
			}},
			{ID: "EndEvent_1", Type: "endEvent", Kind: "MessageEndEvent", Name: "End", Change: ChangeChanged, Moved: true},
			{ID: "MessageFlow_R0", Type: "messageFlow", Kind: "HTTP", Name: "HTTP", Change: ChangeChanged, Properties: []PropertyChange{
				{Key: "credentialName", Change: ChangeChanged, A: MaskedValue, B: MaskedValue},
			}},
			{ID: "MessageFlow_Sender", Type: "messageFlow", Kind: "ProcessDirect", Name: "ProcessDirect", Change: ChangeChanged, Properties: []PropertyChange{
				{Key: "address", Change: ChangeChanged, A: "/orders/route", B: "/orders/route/v2"},
			}},
			{ID: "ServiceTask_0", Type: "serviceTask", Kind: "ExternalCall", Name: "Call S/4HANA", Change: ChangeChanged, Renamed: &[2]string{"Call ERP", "Call S/4HANA"}},
		},
		Flows: []FlowChange{
			{ID: "SequenceFlow_1", Source: "ServiceTask_0", Target: "EndEvent_1", Change: ChangeRemoved, Properties: []PropertyChange{
				{Key: "@container", Change: ChangeRemoved, A: "Process_1"},
			}},
			{ID: "SequenceFlow_2", Source: "ServiceTask_0", Target: "CallActivity_1", Change: ChangeAdded, Properties: []PropertyChange{
				{Key: "@container", Change: ChangeAdded, B: "Process_1"},
			}},
			{ID: "SequenceFlow_3", Source: "CallActivity_1", Target: "EndEvent_1", Change: ChangeAdded, Properties: []PropertyChange{
				{Key: "@container", Change: ChangeAdded, B: "Process_1"},
			}},
		},
		Collaboration: []PropertyChange{{Key: "log", Change: ChangeChanged, A: "All events", B: "Errors only"}},
	}
	assertDiff(t, d, want)

	// without geometry the moved end event is no change
	d, err = DiffModelsWith([]byte(a), []byte(b), DiffOptions{IgnoreGeometry: true})
	if err != nil {
		t.Fatal(err)
	}
	want.Elements = append(want.Elements[:1], want.Elements[2:]...)
	assertDiff(t, d, want)

	// the same model, and one that differs only in formatting
	d, err = DiffModels([]byte(a), []byte(strings.ReplaceAll(a, "><bpmn2:", ">\n  <bpmn2:")))
	if err != nil {
		t.Fatal(err)
	}
	if !d.Empty() {
		t.Errorf("formatting only: %+v, want no changes", d)
	}
}

func TestDiffModelsFlowsAndAttributes(t *testing.T) {
	a := readDemoFlow(t)
	b := replaceOnce(t, a, `<bpmn2:sequenceFlow id="SequenceFlow_0" sourceRef="StartEvent_1" targetRef="ServiceTask_0"/>`,
		`<bpmn2:sequenceFlow id="SequenceFlow_0" name="go" sourceRef="StartEvent_1" targetRef="EndEvent_1"><bpmn2:conditionExpression xsi:type="bpmn2:tFormalExpression">${header.ok} = 'true'</bpmn2:conditionExpression></bpmn2:sequenceFlow>`)
	b = replaceOnce(t, b, `targetRef="Participant_R0"`, `targetRef="Participant_R1"`)
	b = replaceOnce(t, b, `<ifl:property><key>enableBasicAuthentication</key><value>false</value></ifl:property>`,
		`<ifl:property><key>enableBasicAuthentication</key><value>false</value></ifl:property><ifl:property><key>clientSecret</key><value>s3cr3t</value></ifl:property>`)
	d, err := DiffModelsWith([]byte(a), []byte(b), DiffOptions{IgnoreGeometry: true})
	if err != nil {
		t.Fatal(err)
	}
	want := &ModelDiff{
		Elements: []ElementChange{
			{ID: "MessageFlow_R0", Type: "messageFlow", Kind: "HTTP", Name: "HTTP", Change: ChangeChanged, Properties: []PropertyChange{
				{Key: "@targetRef", Change: ChangeChanged, A: "Participant_R0", B: "Participant_R1"},
			}},
			{ID: "Participant_Sender", Type: "participant", Kind: "EndpointSender", Name: "Sender", Change: ChangeChanged, Properties: []PropertyChange{
				{Key: "clientSecret", Change: ChangeAdded, B: MaskedValue},
			}},
		},
		Flows: []FlowChange{
			{ID: "SequenceFlow_0", Name: "go", Source: "StartEvent_1", Target: "EndEvent_1", Change: ChangeChanged, Renamed: &[2]string{"", "go"}, Properties: []PropertyChange{
				{Key: "@condition", Change: ChangeAdded, B: "${header.ok} = 'true'"},
				{Key: "@targetRef", Change: ChangeChanged, A: "ServiceTask_0", B: "EndEvent_1"},
			}},
		},
	}
	assertDiff(t, d, want)
	if strings.Contains(mustJSON(t, d), "s3cr3t") {
		t.Error("a sensitive value is in the diff")
	}

	if _, err := DiffModels([]byte(a), []byte("<bpmn2:definitions")); err == nil {
		t.Error("broken XML: no error")
	}
}

func assertDiff(t *testing.T, got, want *ModelDiff) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("diff:\n%s\nwant:\n%s", mustJSON(t, got), mustJSON(t, want))
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestModelDiffSummary(t *testing.T) {
	a := readDemoFlow(t)
	d, err := DiffModels([]byte(a), []byte(demoVariantB(t, a)))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"~ integration flow: log: All events -> Errors only",
		"+ step CallActivity_1 (GroovyScript 'Map')",
		"~ event EndEvent_1 (MessageEndEvent 'End'): moved",
		"~ adapter MessageFlow_R0 (HTTP): credentialName: *** -> ***",
		"~ adapter MessageFlow_Sender (ProcessDirect): address: /orders/route -> /orders/route/v2",
		"~ step ServiceTask_0 (ExternalCall 'Call S/4HANA'): renamed 'Call ERP' -> 'Call S/4HANA'",
		"- flow SequenceFlow_1 (ServiceTask_0 -> EndEvent_1)",
		"+ flow SequenceFlow_2 (ServiceTask_0 -> CallActivity_1)",
		"+ flow SequenceFlow_3 (CallActivity_1 -> EndEvent_1)",
	}
	if got := d.Summary(); !reflect.DeepEqual(got, want) {
		t.Errorf("summary:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	d = &ModelDiff{Elements: []ElementChange{{ID: "S", Type: "callActivity", Kind: "GroovyScript", Change: ChangeChanged, Moved: true,
		Properties: []PropertyChange{{Key: "a", Change: ChangeAdded, B: "x"}, {Key: "b", Change: ChangeRemoved, A: ""}}}}}
	want = []string{"~ step S (GroovyScript)", `    a: + x`, `    b: - ""`, "    moved"}
	if got := d.Summary(); !reflect.DeepEqual(got, want) {
		t.Errorf("summary:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
