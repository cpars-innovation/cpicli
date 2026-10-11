package iflow

import (
	"fmt"
	"sort"
	"strings"

	"github.com/beevik/etree"
)

// Diagram is the drawing of an .iflw model (BPMNDiagram) with what a viewer
// needs to render it: the bounds of each shape, the waypoints of each line
// and, per element, its BPMN type, name and component.
type Diagram struct {
	// Shapes are the bounds of the shapes (BPMNShape) by BPMN element ID
	// (bpmnElement): steps, events, gateways, subprocesses, participants
	// and lanes.
	Shapes map[string]Rect `json:"shapes"`
	// Edges are the waypoints of the lines (BPMNEdge) by BPMN element ID:
	// sequence flows and message flows.
	Edges map[string][]Point `json:"edges"`
	// Pools are the participants of the collaboration in document order:
	// the integration processes (ProcessRef set) and the senders and
	// receivers.
	Pools []Pool `json:"pools"`
	// Lanes of the processes, if any.
	Lanes []Lane `json:"lanes,omitempty"`
	// Nodes are the flow nodes of the processes and subprocesses in
	// document order (a subprocess before its content).
	Nodes []DiagramNode `json:"nodes"`
	// SequenceFlows connect the nodes of a process.
	SequenceFlows []DiagramFlow `json:"sequenceFlows"`
	// MessageFlows are the adapters (sender and receiver channels) between
	// the participants and the process.
	MessageFlows []DiagramFlow `json:"messageFlows"`
	// Undrawn lists the IDs of the pools, lanes, nodes and flows that have
	// no shape or line in the diagram (sorted). Their Drawn is false.
	Undrawn []string `json:"undrawn,omitempty"`
	// Model is the flow model the diagram was read with (Parse).
	Model *Model `json:"-"`
}

// Rect is the bounds of a shape.
type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Point is a waypoint of a line.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Pool is a participant of the collaboration: an integration process (a
// pool with ProcessRef) or a sender or receiver system.
type Pool struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	ProcessRef string `json:"processRef,omitempty"`
	// Type is ifl:type: IntegrationProcess, EndpointSender,
	// EndpointRecevier (sic, as SAP writes it).
	Type   string `json:"type,omitempty"`
	Bounds *Rect  `json:"bounds,omitempty"`
	Drawn  bool   `json:"drawn"`
}

// Lane is a lane of a process.
type Lane struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Process string `json:"process"`
	// FlowNodes are the IDs of the flow nodes in the lane (flowNodeRef).
	FlowNodes []string `json:"flowNodes,omitempty"`
	Bounds    *Rect    `json:"bounds,omitempty"`
	Drawn     bool     `json:"drawn"`
}

// DiagramNode is a flow node: a step, event, gateway or subprocess.
type DiagramNode struct {
	ID string `json:"id"`
	// Type is the BPMN tag: callActivity, serviceTask, startEvent,
	// exclusiveGateway, subProcess, ...
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
	// Kind is the component (Element.Kind): the cname of cmdVariantUri, else
	// activityType, else Type.
	Kind          string `json:"kind"`
	ActivityType  string `json:"activityType,omitempty"`
	CmdVariantURI string `json:"cmdVariantUri,omitempty"`
	// Container is the ID of the process or subprocess that holds it.
	Container string `json:"container"`
	// EventSubProcess marks an event (exception) subprocess.
	EventSubProcess bool `json:"eventSubProcess,omitempty"`
	// Drawn: the node has a shape (Diagram.Shapes[ID]).
	Drawn bool `json:"drawn"`
}

// DiagramFlow is a sequence flow or a message flow (adapter).
type DiagramFlow struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Source string `json:"source"`
	Target string `json:"target"`
	// Container is the process or subprocess of a sequence flow.
	Container string `json:"container,omitempty"`
	// Condition is the condition expression of a sequence flow.
	Condition string `json:"condition,omitempty"`
	// Adapter is the adapter type of a message flow (ComponentType: HTTPS,
	// SOAP, ProcessDirect, ...), Direction Sender or Receiver.
	Adapter       string `json:"adapter,omitempty"`
	Direction     string `json:"direction,omitempty"`
	CmdVariantURI string `json:"cmdVariantUri,omitempty"`
	// Drawn: the flow has a line (Diagram.Edges[ID]).
	Drawn bool `json:"drawn"`
}

// ReadDiagram reads the diagram of an .iflw model. Elements without a shape
// or line are reported with Drawn false and in Undrawn; a model without a
// diagram (BPMNPlane) has everything undrawn.
func ReadDiagram(data []byte) (*Diagram, error) {
	m, err := Parse(data)
	if err != nil {
		return nil, err
	}
	doc := NewDocument()
	if err := doc.ReadFromBytes(data); err != nil {
		return nil, err
	}
	root := doc.Root()
	if root == nil || root.Tag != "definitions" {
		return nil, fmt.Errorf("not a BPMN model (no definitions element)")
	}
	l := newLayouter(LayoutOptions{}, doc)
	d := &Diagram{Shapes: map[string]Rect{}, Edges: map[string][]Point{}, Model: m,
		Pools: []Pool{}, Nodes: []DiagramNode{}, SequenceFlows: []DiagramFlow{}, MessageFlows: []DiagramFlow{}}
	for id, r := range l.old {
		d.Shapes[id] = Rect{r.x, r.y, r.w, r.h}
	}
	for id, edge := range l.edges {
		pts := []Point{}
		for _, p := range waypoints(edge) {
			pts = append(pts, Point{p.x, p.y})
		}
		d.Edges[id] = pts
	}
	bounds := func(id string) *Rect {
		if r, ok := d.Shapes[id]; ok {
			return &r
		}
		d.Undrawn = append(d.Undrawn, id)
		return nil
	}
	drawn := func(id string) bool {
		if _, ok := d.Edges[id]; ok {
			return true
		}
		d.Undrawn = append(d.Undrawn, id)
		return false
	}

	if collab := child(root, "collaboration"); collab != nil {
		for _, p := range collab.ChildElements() {
			if p.Tag != "participant" {
				continue
			}
			id := p.SelectAttrValue("id", "")
			pool := Pool{ID: id, Name: p.SelectAttrValue("name", ""), ProcessRef: p.SelectAttrValue("processRef", ""),
				Type: p.SelectAttrValue("ifl:type", ""), Bounds: bounds(id)}
			pool.Drawn = pool.Bounds != nil
			d.Pools = append(d.Pools, pool)
		}
	}
	for _, p := range root.ChildElements() {
		if p.Tag != "process" {
			continue
		}
		walk(p, func(el *etree.Element) {
			if el.Tag != "lane" {
				return
			}
			id := el.SelectAttrValue("id", "")
			lane := Lane{ID: id, Name: el.SelectAttrValue("name", ""), Process: p.SelectAttrValue("id", ""), Bounds: bounds(id)}
			lane.Drawn = lane.Bounds != nil
			for _, ref := range el.ChildElements() {
				if ref.Tag == "flowNodeRef" {
					lane.FlowNodes = append(lane.FlowNodes, strings.TrimSpace(ref.Text()))
				}
			}
			d.Lanes = append(d.Lanes, lane)
		})
	}

	for _, id := range m.Order {
		el := m.Elements[id]
		if el.Tag == "process" || nonNodes[el.Tag] {
			continue
		}
		n := DiagramNode{ID: id, Type: el.Tag, Name: el.Name, Kind: el.Kind(), ActivityType: el.Props["activityType"],
			CmdVariantURI: el.Props["cmdVariantUri"], Container: el.Container, EventSubProcess: el.TriggeredByEvent}
		n.Drawn = bounds(id) != nil
		d.Nodes = append(d.Nodes, n)
	}
	for _, f := range m.Flows {
		fl := DiagramFlow{ID: f.ID, Name: f.Name, Source: f.Source, Target: f.Target, Container: f.Container,
			Condition: f.Condition, Drawn: drawn(f.ID)}
		d.SequenceFlows = append(d.SequenceFlows, fl)
	}
	for _, a := range m.Adapters {
		fl := DiagramFlow{ID: a.ID, Name: a.Name, Source: a.Source, Target: a.Target, Adapter: adapterType(a),
			Direction: a.Props["direction"], CmdVariantURI: a.Props["cmdVariantUri"], Drawn: drawn(a.ID)}
		d.MessageFlows = append(d.MessageFlows, fl)
	}
	sort.Strings(d.Undrawn)
	return d, nil
}

// adapterType is the adapter of a message flow: ComponentType, else the
// cname of cmdVariantUri without the namespace (sap:HTTP).
func adapterType(a *Element) string {
	if t := a.Props["ComponentType"]; t != "" {
		return t
	}
	_, cname := Variant(a.Props["cmdVariantUri"])
	if _, n, ok := strings.Cut(cname, ":"); ok {
		return n
	}
	return cname
}
