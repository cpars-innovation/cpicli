package iflow

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Changes of a model diff.
const (
	ChangeAdded   = "added"
	ChangeRemoved = "removed"
	ChangeChanged = "changed"
)

// MaskedValue replaces the values of sensitive properties in a diff.
const MaskedValue = "***"

// ModelDiff is the semantic difference between two .iflw models: per
// element and per property instead of per line.
type ModelDiff struct {
	// Elements are the changed steps, events, gateways, (sub)processes,
	// participants (pools) and message flows (adapters), sorted by ID.
	Elements []ElementChange `json:"elements,omitempty"`
	// Flows are the changed sequence flows, sorted by ID.
	Flows []FlowChange `json:"flows,omitempty"`
	// Collaboration are the changed properties of the integration flow
	// itself (collaboration), sorted by key.
	Collaboration []PropertyChange `json:"collaboration,omitempty"`
}

// ElementChange is an added, removed or changed element.
type ElementChange struct {
	ID string `json:"id"`
	// Type is the BPMN tag: callActivity, serviceTask, startEvent,
	// exclusiveGateway, subProcess, process, participant, messageFlow, ...
	Type string `json:"type"`
	// Kind is the component (Element.Kind: GroovyScript, Enricher, ...);
	// for a participant its ifl:type, for a message flow the adapter type.
	Kind string `json:"kind,omitempty"`
	// Name is the name in B (in A for a removed element).
	Name string `json:"name,omitempty"`
	// Change: added (only in B), removed (only in A), changed.
	Change string `json:"change"`
	// Properties are the changed ifl:property values and attributes
	// (keys starting with @: @container, @sourceRef, @targetRef,
	// @processRef, @type, @triggeredByEvent), sorted by key. For an added
	// or removed element all its values (without @type).
	Properties []PropertyChange `json:"properties,omitempty"`
	// Renamed is the name in A and in B when it changed.
	Renamed *[2]string `json:"renamed,omitempty"`
	// Moved: only the drawing (BPMN DI bounds or waypoints) changed, or
	// changed too.
	Moved bool `json:"moved,omitempty"`
}

// FlowChange is an added, removed or changed sequence flow.
type FlowChange struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Source and Target in B (in A for a removed flow).
	Source string `json:"source"`
	Target string `json:"target"`
	// Change: added, removed, changed.
	Change string `json:"change"`
	// Properties are the changed values: @sourceRef, @targetRef,
	// @condition (the condition expression), @container and the
	// ifl:property values, sorted by key. For an added or removed flow all
	// its values (without @sourceRef and @targetRef).
	Properties []PropertyChange `json:"properties,omitempty"`
	Renamed    *[2]string       `json:"renamed,omitempty"`
	Moved      bool             `json:"moved,omitempty"`
}

// PropertyChange is one changed property or attribute. Values of keys
// that look sensitive (SensitiveName) are MaskedValue.
type PropertyChange struct {
	Key string `json:"key"`
	// Change: added, removed, changed.
	Change string `json:"change"`
	A      string `json:"a,omitempty"`
	B      string `json:"b,omitempty"`
}

// DiffOptions shape DiffModelsWith.
type DiffOptions struct {
	// IgnoreGeometry leaves out elements whose drawing alone changed and
	// never sets Moved; the diagram is not read.
	IgnoreGeometry bool
}

// Empty reports whether the models are the same.
func (d *ModelDiff) Empty() bool {
	return d == nil || len(d.Elements) == 0 && len(d.Flows) == 0 && len(d.Collaboration) == 0
}

var sensitiveName = regexp.MustCompile(`(?i)authorization|cookie|password|passwd|secret|token|credential|apikey|api-key`)

// SensitiveName reports whether the value of a property, header or
// parameter with this name is masked in results: authorization, cookies,
// tokens, passwords, secrets, credentials and API keys.
func SensitiveName(name string) bool { return sensitiveName.MatchString(name) }

// DiffModels compares two .iflw models per element and per property; see
// DiffModelsWith.
func DiffModels(a, b []byte) (*ModelDiff, error) {
	return DiffModelsWith(a, b, DiffOptions{})
}

// DiffModelsWith compares two .iflw models per element and per property:
// steps, events, gateways, (sub)processes, participants and message flows
// (adapters) by ID with their ifl:property values, names and attributes;
// sequence flows with source, target and condition; the integration flow's
// own properties. A changed drawing sets Moved and is no property change.
// The result is sorted by ID, then key; sensitive values are masked.
func DiffModelsWith(a, b []byte, o DiffOptions) (*ModelDiff, error) {
	sides := [2]*diffSide{}
	for i, data := range [][]byte{a, b} {
		s := &diffSide{}
		if o.IgnoreGeometry {
			m, err := Parse(data)
			if err != nil {
				return nil, err
			}
			s.model = m
		} else {
			d, err := ReadDiagram(data)
			if err != nil {
				return nil, err
			}
			s.model, s.diagram = d.Model, d
		}
		sides[i] = s
	}
	sa, sb := sides[0], sides[1]
	ea, eb := sa.elements(), sb.elements()
	d := &ModelDiff{}
	for _, id := range unionKeys(ea, eb) {
		x, y := ea[id], eb[id]
		c := ElementChange{ID: id}
		switch {
		case y == nil:
			c.Change, c.Type, c.Kind, c.Name = ChangeRemoved, x.Tag, diffKind(x), x.Name
		case x == nil:
			c.Change, c.Type, c.Kind, c.Name = ChangeAdded, y.Tag, diffKind(y), y.Name
		default:
			c.Change, c.Type, c.Kind, c.Name = ChangeChanged, y.Tag, diffKind(y), y.Name
			if x.Name != y.Name {
				c.Renamed = &[2]string{x.Name, y.Name}
			}
			c.Moved = !o.IgnoreGeometry && sa.drawing(id) != sb.drawing(id)
		}
		c.Properties = propertyChanges(elementValues(x, c.Change == ChangeChanged), elementValues(y, c.Change == ChangeChanged))
		if c.Change == ChangeChanged && len(c.Properties) == 0 && c.Renamed == nil && !c.Moved {
			continue
		}
		d.Elements = append(d.Elements, c)
	}
	fa, fb := sa.flows(), sb.flows()
	for _, id := range unionKeys(fa, fb) {
		x, y := fa[id], fb[id]
		c := FlowChange{ID: id}
		switch {
		case y == nil:
			c.Change, c.Name, c.Source, c.Target = ChangeRemoved, x.Name, x.Source, x.Target
		case x == nil:
			c.Change, c.Name, c.Source, c.Target = ChangeAdded, y.Name, y.Source, y.Target
		default:
			c.Change, c.Name, c.Source, c.Target = ChangeChanged, y.Name, y.Source, y.Target
			if x.Name != y.Name {
				c.Renamed = &[2]string{x.Name, y.Name}
			}
			c.Moved = !o.IgnoreGeometry && sa.drawing(id) != sb.drawing(id)
		}
		c.Properties = propertyChanges(flowValues(x, c.Change == ChangeChanged), flowValues(y, c.Change == ChangeChanged))
		if c.Change == ChangeChanged && len(c.Properties) == 0 && c.Renamed == nil && !c.Moved {
			continue
		}
		d.Flows = append(d.Flows, c)
	}
	d.Collaboration = propertyChanges(collaborationValues(sa.model.Collaboration), collaborationValues(sb.model.Collaboration))
	return d, nil
}

type diffSide struct {
	model   *Model
	diagram *Diagram
}

// elements are the flow nodes, processes, participants and message flows
// by ID.
func (s *diffSide) elements() map[string]*Element {
	out := map[string]*Element{}
	for id, el := range s.model.Elements {
		if !nonNodes[el.Tag] {
			out[id] = el
		}
	}
	for _, el := range s.model.Participants {
		out[el.ID] = el
	}
	for _, el := range s.model.Adapters {
		out[el.ID] = el
	}
	return out
}

func (s *diffSide) flows() map[string]*SequenceFlow {
	out := map[string]*SequenceFlow{}
	for _, f := range s.model.Flows {
		out[f.ID] = f
	}
	return out
}

// drawing is the shape bounds and line waypoints of an element as a
// comparable string ("" when not drawn).
func (s *diffSide) drawing(id string) string {
	if s.diagram == nil {
		return ""
	}
	r, shape := s.diagram.Shapes[id]
	pts, line := s.diagram.Edges[id]
	if !shape && !line {
		return ""
	}
	return fmt.Sprint(shape, r, pts)
}

// diffKind is the component of an element: the ifl:type of a participant,
// the adapter type of a message flow, else Element.Kind.
func diffKind(el *Element) string {
	switch el.Tag {
	case "participant":
		if t := el.Props["ifl:type"]; t != "" {
			return t
		}
		return el.Tag
	case "messageFlow":
		return adapterType(el)
	}
	return el.Kind()
}

// elementValues are the compared values of an element: its ifl:property
// values and the attributes that matter (keys with @); the tag only when
// both sides have the element (else Type says it).
func elementValues(el *Element, withType bool) map[string]string {
	if el == nil {
		return nil
	}
	out := copyProps(el.Props)
	if withType {
		set(out, "@type", el.Tag)
	}
	set(out, "@container", el.Container)
	set(out, "@sourceRef", el.Source)
	set(out, "@targetRef", el.Target)
	set(out, "@processRef", el.ProcessRef)
	if el.TriggeredByEvent {
		out["@triggeredByEvent"] = "true"
	}
	return out
}

// flowValues are the compared values of a sequence flow; source and target
// only when both sides have the flow (else Source and Target say them).
func flowValues(f *SequenceFlow, withEnds bool) map[string]string {
	if f == nil {
		return nil
	}
	out := copyProps(f.Props)
	if withEnds {
		set(out, "@sourceRef", f.Source)
		set(out, "@targetRef", f.Target)
	}
	set(out, "@condition", f.Condition)
	set(out, "@container", f.Container)
	return out
}

func collaborationValues(el *Element) map[string]string {
	if el == nil {
		return nil
	}
	out := copyProps(el.Props)
	set(out, "@name", el.Name)
	return out
}

func copyProps(props map[string]string) map[string]string {
	out := make(map[string]string, len(props)+4)
	for k, v := range props {
		out[k] = v
	}
	return out
}

func set(m map[string]string, k, v string) {
	if v != "" {
		m[k] = v
	}
}

// propertyChanges compares two value sets by key (sorted); values of
// sensitive keys are masked.
func propertyChanges(a, b map[string]string) []PropertyChange {
	var out []PropertyChange
	for _, k := range unionKeys(a, b) {
		va, okA := a[k]
		vb, okB := b[k]
		c := PropertyChange{Key: k, A: va, B: vb}
		switch {
		case !okB:
			c.Change = ChangeRemoved
		case !okA:
			c.Change = ChangeAdded
		case va != vb:
			c.Change = ChangeChanged
		default:
			continue
		}
		if SensitiveName(k) && !referenceName(k) {
			c.A, c.B = mask(c.A), mask(c.B)
		}
		out = append(out, c)
	}
	return out
}

// referenceName tells whether a property names a deployed artifact (a
// credential or key alias such as credentialName, privateKeyAlias) rather
// than holding a secret: its value is shown.
func referenceName(key string) bool {
	k := strings.ToLower(key)
	return strings.HasSuffix(k, "name") || strings.HasSuffix(k, "alias")
}

func mask(v string) string {
	if v == "" {
		return ""
	}
	return MaskedValue
}

func unionKeys[V any](a, b map[string]V) []string {
	keys := make([]string, 0, len(a)+len(b))
	for k := range a {
		keys = append(keys, k)
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return slices.Compact(keys)
}

// Summary is the diff as readable lines, one per change, for text output:
//
//	~ step CallActivity_1 (GroovyScript 'Map'): script: a.groovy -> b.groovy
//	+ flow SequenceFlow_2 (ServiceTask_0 -> CallActivity_1)
//
// An element with several changes has a header line and an indented line
// per change. Added and removed elements are listed without their values;
// long values are shortened.
func (d *ModelDiff) Summary() []string {
	if d == nil {
		return nil
	}
	var out []string
	add := func(change, head string, details []string) {
		head = changeMark(change) + " " + head
		switch {
		case change != ChangeChanged || len(details) == 0:
			out = append(out, head)
		case len(details) == 1:
			out = append(out, head+": "+details[0])
		default:
			out = append(out, head)
			for _, s := range details {
				out = append(out, "    "+s)
			}
		}
	}
	for _, c := range d.Collaboration {
		out = append(out, changeMark(c.Change)+" integration flow: "+propertyDetail(c))
	}
	for _, e := range d.Elements {
		label := e.Kind
		if e.Name != "" && e.Name != e.Kind {
			label += " '" + e.Name + "'"
		}
		add(e.Change, fmt.Sprintf("%s %s (%s)", elementCategory(e.Type), e.ID, label), details(e.Renamed, e.Moved, e.Properties))
	}
	for _, f := range d.Flows {
		head := fmt.Sprintf("flow %s (%s -> %s)", f.ID, f.Source, f.Target)
		if f.Name != "" {
			head = fmt.Sprintf("flow %s '%s' (%s -> %s)", f.ID, f.Name, f.Source, f.Target)
		}
		add(f.Change, head, details(f.Renamed, f.Moved, f.Properties))
	}
	return out
}

func details(renamed *[2]string, moved bool, props []PropertyChange) []string {
	var out []string
	if renamed != nil {
		out = append(out, fmt.Sprintf("renamed '%s' -> '%s'", renamed[0], renamed[1]))
	}
	for _, p := range props {
		out = append(out, propertyDetail(p))
	}
	if moved {
		out = append(out, "moved")
	}
	return out
}

func propertyDetail(p PropertyChange) string {
	switch p.Change {
	case ChangeAdded:
		return fmt.Sprintf("%s: + %s", p.Key, shortValue(p.B))
	case ChangeRemoved:
		return fmt.Sprintf("%s: - %s", p.Key, shortValue(p.A))
	}
	return fmt.Sprintf("%s: %s -> %s", p.Key, shortValue(p.A), shortValue(p.B))
}

func shortValue(v string) string {
	const max = 80
	v = strings.Join(strings.Fields(v), " ")
	if v == "" {
		return `""`
	}
	if r := []rune(v); len(r) > max {
		return string(r[:max-3]) + "..."
	}
	return v
}

func changeMark(change string) string {
	switch change {
	case ChangeAdded:
		return "+"
	case ChangeRemoved:
		return "-"
	}
	return "~"
}

// elementCategory names an element type for people: step, event, gateway,
// subprocess, process, participant, adapter.
func elementCategory(tag string) string {
	switch {
	case tag == "messageFlow":
		return "adapter"
	case tag == "participant", tag == "process":
		return tag
	case tag == "subProcess":
		return "subprocess"
	case strings.HasSuffix(tag, "Event"):
		return "event"
	case strings.HasSuffix(tag, "Gateway"):
		return "gateway"
	}
	return "step"
}
