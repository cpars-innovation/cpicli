package iflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func readDemoDiagram(t *testing.T) *Diagram {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "cpitest", "landscapes", "demo", "packages", "Orders", "Orders_Route",
		"src", "main", "resources", "scenarioflows", "integrationflow", "Orders_Route.iflw"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := ReadDiagram(data)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestReadDiagram(t *testing.T) {
	d := readDemoDiagram(t)

	if got, want := d.Shapes["ServiceTask_0"], (Rect{X: 362, Y: 110, Width: 100, Height: 60}); got != want {
		t.Errorf("bounds of ServiceTask_0: %+v, want %+v", got, want)
	}
	if got, want := d.Edges["SequenceFlow_0"], []Point{{286, 140}, {412, 140}}; !reflect.DeepEqual(got, want) {
		t.Errorf("waypoints of SequenceFlow_0: %+v, want %+v", got, want)
	}
	if got := len(d.Edges["MessageFlow_R0"]); got != 5 {
		t.Errorf("waypoints of MessageFlow_R0: %d, want 5", got)
	}

	if len(d.Pools) != 4 {
		t.Fatalf("pools: %+v, want 4", d.Pools)
	}
	var processes []string
	for _, p := range d.Pools {
		if p.ProcessRef != "" {
			processes = append(processes, p.ID+"->"+p.ProcessRef+":"+p.Type)
		}
		if !p.Drawn || p.Bounds == nil || *p.Bounds != d.Shapes[p.ID] {
			t.Errorf("pool %s: bounds %+v", p.ID, p.Bounds)
		}
	}
	if want := []string{"Participant_Process->Process_1:IntegrationProcess"}; !reflect.DeepEqual(processes, want) {
		t.Errorf("integration processes: %v, want %v", processes, want)
	}
	if d.Pools[0].Type != "EndpointSender" {
		t.Errorf("type of %s: %q", d.Pools[0].ID, d.Pools[0].Type)
	}

	nodes := map[string]DiagramNode{}
	for _, n := range d.Nodes {
		nodes[n.ID] = n
	}
	if len(d.Nodes) != 3 {
		t.Errorf("nodes: %+v, want 3", d.Nodes)
	}
	call := nodes["ServiceTask_0"]
	if call.Type != "serviceTask" || call.Name != "Call ERP" || call.Kind != "ExternalCall" || call.ActivityType != "ExternalCall" ||
		!strings.Contains(call.CmdVariantURI, "cname::ExternalCall") || call.Container != "Process_1" || !call.Drawn {
		t.Errorf("ServiceTask_0: %+v", call)
	}
	if n := nodes["StartEvent_1"]; n.Type != "startEvent" || n.Kind != "MessageStartEvent" {
		t.Errorf("StartEvent_1: %+v", n)
	}
	if len(d.SequenceFlows) != 2 || d.SequenceFlows[0].Source != "StartEvent_1" || !d.SequenceFlows[0].Drawn {
		t.Errorf("sequence flows: %+v", d.SequenceFlows)
	}
	adapters := map[string]string{}
	for _, f := range d.MessageFlows {
		adapters[f.ID] = f.Adapter + "/" + f.Direction
		if !f.Drawn {
			t.Errorf("%s not drawn", f.ID)
		}
	}
	if want := map[string]string{"MessageFlow_Sender": "ProcessDirect/Sender", "MessageFlow_R0": "HTTP/Receiver", "MessageFlow_R1": "JMS/Receiver"}; !reflect.DeepEqual(adapters, want) {
		t.Errorf("adapters: %v, want %v", adapters, want)
	}
	if len(d.Undrawn) != 0 || len(d.Lanes) != 0 {
		t.Errorf("undrawn %v, lanes %v", d.Undrawn, d.Lanes)
	}
	if d.Model == nil || d.Model.Elements["ServiceTask_0"] == nil {
		t.Error("no model")
	}

	out, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{`"shapes":{`, `"ServiceTask_0":{"x":362,"y":110,"width":100,"height":60}`, `"processRef":"Process_1"`,
		`"messageFlows":[`, `"cmdVariantUri":`, `"adapter":"HTTP"`} {
		if !strings.Contains(string(out), s) {
			t.Errorf("JSON has no %s: %s", s, out)
		}
	}
	if strings.Contains(string(out), `"Model"`) {
		t.Error("the model is in the JSON")
	}
}

func TestReadDiagramUndrawnAndLanes(t *testing.T) {
	in := tModel(tProc{
		nodes: []tNode{{id: "start", tag: "startEvent", name: "Start", r: r(100, 100, 32, 32)}, step("a", 200, 90), {id: "b", tag: "callActivity", name: "B"}},
		flows: []tFlow{{id: "f1", src: "start", tgt: "a", pts: []point{{116, 116}, {250, 116}}}, {id: "f2", src: "a", tgt: "b"}},
	}, nil)
	in = strings.Replace(in, `<bpmn2:process id="Process_1" name="Integration Process">`, `<bpmn2:process id="Process_1" name="Integration Process">
        <bpmn2:laneSet id="LaneSet_1"><bpmn2:lane id="Lane_1" name="Main"><bpmn2:flowNodeRef>start</bpmn2:flowNodeRef><bpmn2:flowNodeRef>a</bpmn2:flowNodeRef></bpmn2:lane></bpmn2:laneSet>`, 1)
	in = strings.Replace(in, `</bpmndi:BPMNPlane>`, `<bpmndi:BPMNShape bpmnElement="Lane_1" id="BPMNShape_Lane_1"><dc:Bounds height="200.0" width="600.0" x="80.0" y="40.0"/></bpmndi:BPMNShape></bpmndi:BPMNPlane>`, 1)
	d, err := ReadDiagram([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"MessageFlow_in", "Participant_1", "Participant_2", "Participant_Process_1", "b", "f2"}; !reflect.DeepEqual(d.Undrawn, want) {
		t.Errorf("undrawn: %v, want %v", d.Undrawn, want)
	}
	var ids []string
	for _, n := range d.Nodes {
		ids = append(ids, n.ID)
		if n.ID == "b" && n.Drawn {
			t.Error("b is drawn")
		}
	}
	if want := []string{"start", "a", "b"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("nodes: %v, want %v (laneSet is no node)", ids, want)
	}
	if len(d.Lanes) != 1 || d.Lanes[0].Process != "Process_1" || !reflect.DeepEqual(d.Lanes[0].FlowNodes, []string{"start", "a"}) ||
		d.Lanes[0].Bounds == nil || d.Lanes[0].Bounds.Width != 600 {
		t.Errorf("lanes: %+v", d.Lanes)
	}

	// no diagram at all: everything undrawn
	d, err = ReadDiagram([]byte(model(in)))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Shapes) != 0 || len(d.Undrawn) != 10 {
		t.Errorf("without a diagram: shapes %v, undrawn %v", d.Shapes, d.Undrawn)
	}
}

func TestReadDiagramInvalid(t *testing.T) {
	for _, in := range []string{`<bpmn2:definitions><bpmn2:process id="P"></bpmn2:definitions>`, ``, `<foo/>`} {
		if _, err := ReadDiagram([]byte(in)); err == nil {
			t.Errorf("%q: no error", in)
		}
	}
}
