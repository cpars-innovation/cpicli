package iflow

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// tNode is a flow node of a test model; r is its current bounds (nil: not
// drawn), sub the content of a subprocess.
type tNode struct {
	id, tag, name string
	r             *rect
	sub           *tProc
	event         bool
}

type tFlow struct {
	id, src, tgt string
	pts          []point // nil: not drawn
}

type tProc struct {
	nodes []tNode
	flows []tFlow
}

// tModel writes an .iflw with a sender (to start), a receiver (from
// "call", if present) and one integration process.
func tModel(p tProc, pool *rect) string {
	var proc, di strings.Builder
	var writeProc func(p tProc)
	writeProc = func(p tProc) {
		for _, n := range p.nodes {
			attrs := fmt.Sprintf(`id="%s" name="%s"`, n.id, n.name)
			if n.event {
				attrs += ` triggeredByEvent="true"`
			}
			if n.sub != nil {
				fmt.Fprintf(&proc, "        <bpmn2:%s %s>\n", n.tag, attrs)
				writeProc(*n.sub)
				fmt.Fprintf(&proc, "        </bpmn2:%s>\n", n.tag)
			} else {
				fmt.Fprintf(&proc, "        <bpmn2:%s %s/>\n", n.tag, attrs)
			}
			if n.r != nil {
				fmt.Fprintf(&di, `            <bpmndi:BPMNShape bpmnElement="%s" id="BPMNShape_%s">
                <dc:Bounds height="%s" width="%s" x="%s" y="%s"/>
            </bpmndi:BPMNShape>
`, n.id, n.id, fmtNum(n.r.h), fmtNum(n.r.w), fmtNum(n.r.x), fmtNum(n.r.y))
			}
		}
		for _, f := range p.flows {
			fmt.Fprintf(&proc, "        <bpmn2:sequenceFlow id=\"%s\" sourceRef=\"%s\" targetRef=\"%s\"/>\n", f.id, f.src, f.tgt)
			if f.pts != nil {
				fmt.Fprintf(&di, "            <bpmndi:BPMNEdge bpmnElement=\"%s\" id=\"BPMNEdge_%s\" sourceElement=\"BPMNShape_%s\" targetElement=\"BPMNShape_%s\">\n", f.id, f.id, f.src, f.tgt)
				for _, pt := range f.pts {
					fmt.Fprintf(&di, "                <di:waypoint x=\"%s\" xsi:type=\"dc:Point\" y=\"%s\"/>\n", fmtNum(pt.x), fmtNum(pt.y))
				}
				di.WriteString("            </bpmndi:BPMNEdge>\n")
			}
		}
	}
	writeProc(p)
	poolShape := ""
	if pool != nil {
		poolShape = fmt.Sprintf(`            <bpmndi:BPMNShape bpmnElement="Participant_Process_1" id="BPMNShape_Participant_Process_1">
                <dc:Bounds height="%s" width="%s" x="%s" y="%s"/>
            </bpmndi:BPMNShape>
`, fmtNum(pool.h), fmtNum(pool.w), fmtNum(pool.x), fmtNum(pool.y))
	}
	receiver := ""
	if strings.Contains(proc.String(), `id="call"`) {
		receiver = `        <bpmn2:messageFlow id="MessageFlow_out" name="HTTP" sourceRef="call" targetRef="Participant_2"/>
`
	}
	return `<?xml version="1.0" encoding="UTF-8"?><bpmn2:definitions xmlns:bpmn2="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" xmlns:dc="http://www.omg.org/spec/DD/20100524/DC" xmlns:di="http://www.omg.org/spec/DD/20100524/DI" xmlns:ifl="http:///com.sap.ifl.model/Ifl.xsd" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" id="Definitions_1">
    <bpmn2:collaboration id="Collaboration_1" name="Default Collaboration">
        <bpmn2:participant id="Participant_1" ifl:type="EndpointSender" name="Sender"/>
        <bpmn2:participant id="Participant_2" ifl:type="EndpointRecevier" name="Receiver"/>
        <bpmn2:participant id="Participant_Process_1" ifl:type="IntegrationProcess" name="Integration Process" processRef="Process_1"/>
        <bpmn2:messageFlow id="MessageFlow_in" name="HTTPS" sourceRef="Participant_1" targetRef="start"/>
` + receiver + `    </bpmn2:collaboration>
    <bpmn2:process id="Process_1" name="Integration Process">
` + proc.String() + `    </bpmn2:process>
    <bpmndi:BPMNDiagram id="BPMNDiagram_1" name="Default Collaboration Diagram">
        <bpmndi:BPMNPlane bpmnElement="Collaboration_1" id="BPMNPlane_1">
` + poolShape + di.String() + `        </bpmndi:BPMNPlane>
    </bpmndi:BPMNDiagram>
</bpmn2:definitions>
`
}

func r(x, y, w, h float64) *rect { return &rect{x, y, w, h} }

func step(id string, x, y float64) tNode {
	return tNode{id: id, tag: "callActivity", name: "Step " + id, r: r(x, y, 100, 60)}
}

// layout runs Layout and checks the result: no issues, the model part
// unchanged, a second run changes nothing.
func layout(t *testing.T, in string, o LayoutOptions) (string, *LayoutResult) {
	t.Helper()
	out, res, err := Layout([]byte(in), o)
	if err != nil {
		t.Fatal(err)
	}
	issues, err := CheckLayout(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) > 0 {
		t.Errorf("issues after layout: %+v\n%s", issues, out)
	}
	if model(in) != model(string(out)) {
		t.Errorf("layout changed the model:\n%s", out)
	}
	again, res2, err := Layout(out, o)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Changed || string(again) != string(out) {
		t.Errorf("second layout changed the diagram: %+v\n%s", res2, again)
	}
	return string(out), res
}

var reDiagram = regexp.MustCompile(`(?s)<bpmndi:BPMNDiagram.*</bpmndi:BPMNDiagram>`)

func model(s string) string { return reDiagram.ReplaceAllString(s, "") }

func bounds(t *testing.T, xml, id string) rect {
	t.Helper()
	m := regexp.MustCompile(`bpmnElement="` + regexp.QuoteMeta(id) + `" id="[^"]*">\s*<dc:Bounds height="([\d.]+)" width="([\d.]+)" x="([-\d.]+)" y="([-\d.]+)"`).FindStringSubmatch(xml)
	if m == nil {
		t.Fatalf("no bounds for %s", id)
	}
	var v [4]float64
	for i := range v {
		fmt.Sscan(m[i+1], &v[i])
	}
	return rect{v[2], v[3], v[1], v[0]}
}

func TestLayoutLinearCramped(t *testing.T) {
	// everything piled up at the same place
	in := tModel(tProc{
		nodes: []tNode{
			{id: "start", tag: "startEvent", name: "Start", r: r(300, 100, 32, 32)},
			step("a", 310, 90), step("b", 320, 95),
			{id: "end", tag: "endEvent", name: "End", r: r(330, 100, 32, 32)},
		},
		flows: []tFlow{{"f1", "start", "a", []point{{0, 0}, {1, 1}}}, {"f2", "a", "b", []point{{0, 0}, {1, 1}}}, {"f3", "b", "end", []point{{0, 0}, {1, 1}}}},
	}, r(250, 60, 300, 150))
	issues, _ := CheckLayout([]byte(in))
	if len(issues) == 0 {
		t.Fatal("expected issues before the layout")
	}
	out, res := layout(t, in, LayoutOptions{})
	if !res.Changed || res.Moved == 0 || res.Rerouted == 0 {
		t.Errorf("result %+v", res)
	}
	ids := []string{"start", "a", "b", "end"}
	for i := 1; i < len(ids); i++ {
		p, c := bounds(t, out, ids[i-1]), bounds(t, out, ids[i])
		if c.x-p.right() < DefaultHGap {
			t.Errorf("%s -> %s: gap %.0f", ids[i-1], ids[i], c.x-p.right())
		}
		if p.cy() != c.cy() {
			t.Errorf("%s and %s not on one line: %.0f %.0f", ids[i-1], ids[i], p.cy(), c.cy())
		}
	}
	// straight lines centre to centre
	if !strings.Contains(out, `<bpmndi:BPMNEdge bpmnElement="f2" id="BPMNEdge_f2" sourceElement="BPMNShape_a" targetElement="BPMNShape_b">
                <di:waypoint x=`) {
		t.Errorf("edge f2 not indented like the file:\n%s", out)
	}
	pool := bounds(t, out, "Participant_Process_1")
	for _, id := range ids {
		if !inside(bounds(t, out, id), pool) {
			t.Errorf("%s outside the pool", id)
		}
	}
	sender, receiver := bounds(t, out, "Participant_1"), bounds(t, out, "Participant_2")
	if sender.right() >= pool.x || receiver.x <= pool.right() {
		t.Errorf("sender %v / receiver %v not beside the pool %v", sender, receiver, pool)
	}
	if start := bounds(t, out, "start"); start.cy() <= sender.y || start.cy() >= sender.bottom() {
		t.Errorf("sender %v not level with the start event %v", sender, start)
	}
}

func routerModel(bAbove bool) string {
	ya, yb := 300.0, 100.0
	if !bAbove {
		ya, yb = yb, ya
	}
	return tModel(tProc{
		nodes: []tNode{
			{id: "start", tag: "startEvent", name: "Start", r: r(100, 200, 32, 32)},
			{id: "router", tag: "exclusiveGateway", name: "Router", r: r(200, 195, 40, 40)},
			step("a1", 300, ya), step("a2", 420, ya), step("b1", 300, yb),
			{id: "join", tag: "exclusiveGateway", name: "Join", r: r(560, 195, 40, 40)},
			{id: "end", tag: "endEvent", name: "End", r: r(660, 200, 32, 32)},
		},
		flows: []tFlow{
			{"f1", "start", "router", nil}, {"fa", "router", "a1", nil}, {"fa2", "a1", "a2", nil}, {"fa3", "a2", "join", nil},
			{"fb", "router", "b1", nil}, {"fb2", "b1", "join", nil}, {"fskip", "router", "join", nil}, {"fend", "join", "end", nil},
		},
	}, r(50, 50, 700, 400))
}

func TestLayoutRouterBranches(t *testing.T) {
	for _, bAbove := range []bool{true, false} {
		out, res := layout(t, routerModel(bAbove), LayoutOptions{})
		if res.Added != 11 { // 8 lines, sender, receiver, message flow
			t.Errorf("added %d, want 11", res.Added)
		}
		a, b := bounds(t, out, "a1"), bounds(t, out, "b1")
		if (b.cy() < a.cy()) != bAbove {
			t.Errorf("tidy changed the order of the branches (b above: %v): a1 %v b1 %v", bAbove, a, b)
		}
		if a2 := bounds(t, out, "a2"); a2.cy() != a.cy() {
			t.Errorf("branch a not on one line")
		}
	}
	layout(t, routerModel(true), LayoutOptions{Mode: LayoutFull})
}

func TestLayoutDrawsMissingShapes(t *testing.T) {
	in := tModel(tProc{
		nodes: []tNode{
			{id: "start", tag: "startEvent", name: "Start", r: r(100, 100, 32, 32)},
			{id: "call", tag: "serviceTask", name: "Call"}, // not drawn
			{id: "end", tag: "endEvent", name: "End"},
		},
		flows: []tFlow{{"f1", "start", "call", nil}, {"f2", "call", "end", nil}},
	}, nil)
	issues, _ := CheckLayout([]byte(in))
	missing := 0
	for _, i := range issues {
		if i.Kind == IssueMissing {
			missing++
		}
	}
	if missing < 5 { // call, end, 2 lines, the pool, 2 endpoints, 2 message flows
		t.Errorf("missing issues: %+v", issues)
	}
	out, res := layout(t, in, LayoutOptions{})
	if res.Added < 9 {
		t.Errorf("added %d", res.Added)
	}
	call, recv, pool := bounds(t, out, "call"), bounds(t, out, "Participant_2"), bounds(t, out, "Participant_Process_1")
	if recv.cx() != call.cx() || recv.bottom() >= pool.y {
		t.Errorf("receiver %v not above the calling step %v (pool %v)", recv, call, pool)
	}
	if !strings.Contains(out, `bpmnElement="MessageFlow_out"`) {
		t.Error("message flow not drawn")
	}
}

func TestLayoutExceptionSubprocess(t *testing.T) {
	sub := &tProc{
		nodes: []tNode{
			{id: "errStart", tag: "startEvent", name: "Error Start", r: r(900, 900, 32, 32)},
			step("handle", 0, 0),
			{id: "errEnd", tag: "endEvent", name: "Error End", r: r(10, 10, 32, 32)},
		},
		flows: []tFlow{{"e1", "errStart", "handle", nil}, {"e2", "handle", "errEnd", nil}},
	}
	in := tModel(tProc{
		nodes: []tNode{
			{id: "start", tag: "startEvent", name: "Start", r: r(100, 100, 32, 32)},
			step("call", 200, 90),
			{id: "end", tag: "endEvent", name: "End", r: r(400, 100, 32, 32)},
			{id: "exc", tag: "subProcess", name: "Exception Subprocess", r: r(100, 90, 200, 100), sub: sub, event: true},
		},
		flows: []tFlow{{"f1", "start", "call", nil}, {"f2", "call", "end", nil}},
	}, r(50, 50, 500, 200))
	out, _ := layout(t, in, LayoutOptions{})
	exc, call := bounds(t, out, "exc"), bounds(t, out, "call")
	if exc.y < call.bottom() {
		t.Errorf("exception subprocess %v not below the main flow %v", exc, call)
	}
	for _, id := range []string{"errStart", "handle", "errEnd"} {
		if !inside(bounds(t, out, id), exc) {
			t.Errorf("%s outside the subprocess", id)
		}
	}
}

func TestLayoutTestdata(t *testing.T) {
	files, _ := filepath.Glob("../../test/testdata/artifacts/*/*/src/main/resources/scenarioflows/integrationflow/*.iflw")
	if len(files) == 0 {
		t.Fatal("no test models")
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{LayoutTidy, LayoutFull} {
			t.Run(filepath.Base(filepath.Dir(filepath.Dir(f)))+"/"+mode, func(t *testing.T) {
				layout(t, string(data), LayoutOptions{Mode: mode})
			})
		}
	}
}

func TestLayoutOptions(t *testing.T) {
	if _, _, err := Layout([]byte(routerModel(true)), LayoutOptions{Mode: "pretty"}); err == nil {
		t.Error("unknown mode accepted")
	}
	if _, _, err := Layout([]byte(`<definitions><process id="p"/></definitions>`), LayoutOptions{}); err == nil {
		t.Error("model without diagram accepted")
	}
	wide, _ := layout(t, routerModel(true), LayoutOptions{HGap: 120})
	narrow, _ := layout(t, routerModel(true), LayoutOptions{})
	if bounds(t, wide, "end").x <= bounds(t, narrow, "end").x {
		t.Error("hgap ignored")
	}
}

func TestIsotonic(t *testing.T) {
	got := isotonic([]float64{10, 0, 50}, []float64{20, 20})
	// 10 and 0 conflict: they settle around their mean, 50 stays
	want := []float64{-5, 15, 50}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
