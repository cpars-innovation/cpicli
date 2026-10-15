// Package iflow reads the BPMN model of an integration flow (.iflw).
package iflow

import (
	"bytes"
	"encoding/xml"
	"io"
	"sort"
	"strings"
)

// Model is the structure of an .iflw (BPMN) model as the linter needs
// it: the steps of each process with their configuration and connections.
type Model struct {
	// Elements by ID: steps, events, gateways, subprocesses (not the
	// collaboration's participants and message flows).
	Elements map[string]*Element
	// Flows are the sequence flows.
	Flows []*SequenceFlow
	// Adapters are the sender and receiver channels (message flows).
	Adapters []*Element
	// Participants are the pools of the collaboration: the integration
	// processes and the sender and receiver systems.
	Participants []*Element
	// Collaboration is the integration flow itself (its ifl:property
	// values: log level, namespace mapping, ...); nil without one.
	Collaboration *Element
	// Order is the document order of the element IDs.
	Order []string
	// MissingRefs are the sequence flows an element does not list as
	// incoming or outgoing (filled in from the flows). SAP's runtime builds
	// the route from those references: without them the process does not
	// run and the message completes unchanged.
	MissingRefs []FlowRef
}

// FlowRef is a sequence flow reference of an element.
type FlowRef struct {
	Element, Flow string
	Incoming      bool
}

// Element is one BPMN element with its ifl:property values.
type Element struct {
	ID, Tag, Name string
	// Container is the ID of the process or subprocess that holds it.
	Container string
	Props     map[string]string
	// Incoming and Outgoing are sequence flow IDs.
	Incoming, Outgoing []string
	// TriggeredByEvent marks an event subprocess (exception subprocess).
	TriggeredByEvent bool
	// Source and Target are the sourceRef and targetRef of a message flow
	// (adapter): the participant or the step / event it connects.
	Source, Target string
	// ProcessRef is the process of a participant (pool), if any.
	ProcessRef string
}

// SequenceFlow connects two elements.
type SequenceFlow struct {
	ID, Source, Target, Container, Name string
	Condition                           string
	Props                               map[string]string
}

// Kind is the component name of an element (GroovyScript, Enricher, ...)
// from cmdVariantUri, else its activityType, else the BPMN tag.
func (e *Element) Kind() string {
	if _, cname := Variant(e.Props["cmdVariantUri"]); cname != "" {
		return cname
	}
	if t := e.Props["activityType"]; t != "" {
		return t
	}
	return e.Tag
}

// Version is the component version of an element.
func (e *Element) Version() string { return e.Props["componentVersion"] }

// IsStep reports whether an element is a processing step (not an event,
// gateway or container).
func (e *Element) IsStep() bool {
	return e.Tag == "callActivity" || e.Tag == "serviceTask"
}

var containerTags = map[string]bool{"process": true, "subProcess": true}

// Parse reads an .iflw model.
func Parse(data []byte) (*Model, error) {
	m := &Model{Elements: map[string]*Element{}}
	dec := xml.NewDecoder(bytes.NewReader(data))
	type frame struct {
		tag     string
		el      *Element
		flow    *SequenceFlow
		key     string
		text    strings.Builder
		props   map[string]string
		capture bool
		// container: a process or subprocess, whose direct children are the
		// flow elements
		container bool
	}
	var stack []*frame
	var containers []string
	owner := func() map[string]string {
		// property -> extensionElements -> owner
		for i := len(stack) - 1; i >= 0; i-- {
			if stack[i].el != nil {
				return stack[i].el.Props
			}
			if stack[i].flow != nil {
				return stack[i].flow.Props
			}
		}
		return nil
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			f := &frame{tag: t.Name.Local}
			attr := func(name string) string {
				for _, a := range t.Attr {
					if a.Name.Local == name {
						return a.Value
					}
				}
				return ""
			}
			id := attr("id")
			for _, fr := range stack {
				if fr.tag == "extensionElements" {
					id = "" // content of a property value, not a model element
				}
			}
			container := ""
			if n := len(containers); n > 0 {
				container = containers[n-1]
			}
			switch {
			case f.tag == "sequenceFlow":
				f.flow = &SequenceFlow{ID: id, Source: attr("sourceRef"), Target: attr("targetRef"), Container: container, Name: attr("name"), Props: map[string]string{}}
				m.Flows = append(m.Flows, f.flow)
			case f.tag == "collaboration" && m.Collaboration == nil:
				f.el = &Element{ID: id, Tag: f.tag, Name: attr("name"), Props: map[string]string{}}
				m.Collaboration = f.el
			case f.tag == "participant" || f.tag == "messageFlow":
				f.el = &Element{ID: id, Tag: f.tag, Name: attr("name"), Props: map[string]string{}}
				if f.tag == "messageFlow" {
					f.el.Source, f.el.Target = attr("sourceRef"), attr("targetRef")
					m.Adapters = append(m.Adapters, f.el)
				} else {
					f.el.ProcessRef = attr("processRef")
					m.Participants = append(m.Participants, f.el)
				}
			// flow elements are the direct children of a process or subprocess;
			// nested parts with an id (conditionExpression, loop
			// characteristics, event definitions) are not steps
			case id != "" && (containerTags[f.tag] || (len(stack) > 0 && stack[len(stack)-1].container)) && f.tag != "incoming" && f.tag != "outgoing":
				f.el = &Element{ID: id, Tag: f.tag, Name: attr("name"), Container: container, Props: map[string]string{},
					TriggeredByEvent: attr("triggeredByEvent") == "true"}
				m.Elements[id] = f.el
				m.Order = append(m.Order, id)
			}
			if containerTags[f.tag] && id != "" {
				containers = append(containers, id)
				f.container = true
			}
			f.capture = f.tag == "key" || f.tag == "value" || f.tag == "incoming" || f.tag == "outgoing" || f.tag == "conditionExpression"
			stack = append(stack, f)
		case xml.CharData:
			if n := len(stack); n > 0 && stack[n-1].capture {
				stack[n-1].text.Write(t)
			}
		case xml.EndElement:
			n := len(stack)
			if n == 0 {
				continue
			}
			f := stack[n-1]
			stack = stack[:n-1]
			text := f.text.String()
			switch f.tag {
			case "key", "value":
				if n >= 2 {
					p := stack[n-2]
					if p.props == nil {
						p.props = map[string]string{}
					}
					p.props[f.tag] = text
				}
			case "property":
				if props := owner(); props != nil {
					props[f.props["key"]] = f.props["value"]
				}
			case "incoming", "outgoing":
				for i := len(stack) - 1; i >= 0; i-- {
					if el := stack[i].el; el != nil {
						if f.tag == "incoming" {
							el.Incoming = append(el.Incoming, strings.TrimSpace(text))
						} else {
							el.Outgoing = append(el.Outgoing, strings.TrimSpace(text))
						}
						break
					}
				}
			case "conditionExpression":
				for i := len(stack) - 1; i >= 0; i-- {
					if fl := stack[i].flow; fl != nil {
						fl.Condition = strings.TrimSpace(text)
						break
					}
				}
			}
			if containerTags[f.tag] && len(containers) > 0 && f.el != nil && containers[len(containers)-1] == f.el.ID {
				containers = containers[:len(containers)-1]
			}
		}
	}
	// incoming/outgoing from the sequence flows (some models omit them)
	for _, fl := range m.Flows {
		if el := m.Elements[fl.Source]; el != nil && !contains(el.Outgoing, fl.ID) {
			el.Outgoing = append(el.Outgoing, fl.ID)
			m.MissingRefs = append(m.MissingRefs, FlowRef{Element: el.ID, Flow: fl.ID})
		}
		if el := m.Elements[fl.Target]; el != nil && !contains(el.Incoming, fl.ID) {
			el.Incoming = append(el.Incoming, fl.ID)
			m.MissingRefs = append(m.MissingRefs, FlowRef{Element: el.ID, Flow: fl.ID, Incoming: true})
		}
	}
	return m, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Flow returns a sequence flow by ID.
func (m *Model) Flow(id string) *SequenceFlow {
	for _, f := range m.Flows {
		if f.ID == id {
			return f
		}
	}
	return nil
}

// Unreachable returns the elements of each process that cannot be reached
// from a start event of the same process (event subprocesses and their
// content count as reachable; the containers themselves are skipped).
func (m *Model) Unreachable() []*Element {
	byContainer := map[string][]*Element{}
	for _, id := range m.Order {
		el := m.Elements[id]
		byContainer[el.Container] = append(byContainer[el.Container], el)
	}
	var out []*Element
	for container, els := range byContainer {
		if container == "" {
			continue
		}
		seen := map[string]bool{}
		var queue []string
		for _, el := range els {
			if el.Tag == "startEvent" || el.Tag == "boundaryEvent" || (el.Tag == "subProcess" && el.TriggeredByEvent) {
				queue = append(queue, el.ID)
			}
		}
		if len(queue) == 0 {
			continue // no start: nothing to judge
		}
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			if seen[id] {
				continue
			}
			seen[id] = true
			el := m.Elements[id]
			if el == nil {
				continue
			}
			for _, out := range el.Outgoing {
				if f := m.Flow(out); f != nil {
					queue = append(queue, f.Target)
				}
			}
			// a boundary event belongs to the activity it is attached to
		}
		for _, el := range els {
			if !seen[el.ID] && !containerTags[el.Tag] {
				out = append(out, el)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Next returns the single element an element leads to (nil when it has no
// or several outgoing flows).
func (m *Model) Next(el *Element) *Element {
	if len(el.Outgoing) != 1 {
		return nil
	}
	f := m.Flow(el.Outgoing[0])
	if f == nil {
		return nil
	}
	return m.Elements[f.Target]
}

// Variant splits a cmdVariantUri into its component type (ctype::) and name
// (cname::).
func Variant(uri string) (ctype, cname string) {
	for part := range strings.SplitSeq(uri, "/") {
		if v, ok := strings.CutPrefix(part, "ctype::"); ok {
			ctype = v
		} else if v, ok := strings.CutPrefix(part, "cname::"); ok {
			cname = v
		}
	}
	return ctype, cname
}
