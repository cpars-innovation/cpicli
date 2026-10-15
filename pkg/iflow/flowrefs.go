package iflow

import (
	"strings"

	"github.com/beevik/etree"
)

// AddFlowReferences adds the incoming and outgoing references each element
// lacks for the sequence flows that start or end at it, as SAP's editor
// writes them (after extensionElements, incoming before outgoing). SAP's
// runtime builds the route from these references: a model without them
// deploys, but its process does not run. It reports whether it changed the
// document.
func AddFlowReferences(doc *etree.Document) bool {
	byID := map[string]*etree.Element{}
	var flows []*etree.Element
	var walk func(el *etree.Element)
	walk = func(el *etree.Element) {
		if el.Tag == "extensionElements" {
			return
		}
		if el.Tag == "sequenceFlow" {
			flows = append(flows, el)
		} else if id := el.SelectAttrValue("id", ""); id != "" {
			byID[id] = el
		}
		for _, c := range el.ChildElements() {
			walk(c)
		}
	}
	walk(doc.Root())
	changed := false
	for _, fl := range flows {
		id := fl.SelectAttrValue("id", "")
		if id == "" {
			continue
		}
		if el := byID[fl.SelectAttrValue("sourceRef", "")]; el != nil && addFlowRef(el, "outgoing", id) {
			changed = true
		}
		if el := byID[fl.SelectAttrValue("targetRef", "")]; el != nil && addFlowRef(el, "incoming", id) {
			changed = true
		}
	}
	return changed
}

// addFlowRef adds <incoming>/<outgoing> flow to el unless it is there.
func addFlowRef(el *etree.Element, tag, flow string) bool {
	pos := 0 // after extensionElements and the existing references
	for i, c := range el.Child {
		ce, ok := c.(*etree.Element)
		if !ok {
			continue
		}
		switch {
		case (ce.Tag == "incoming" || ce.Tag == "outgoing") && strings.TrimSpace(ce.Text()) == flow && ce.Tag == tag:
			return false
		case ce.Tag == "extensionElements" || ce.Tag == "incoming" || (tag == "outgoing" && ce.Tag == "outgoing"):
			pos = i + 1
		}
	}
	ref := etree.NewElement(tag)
	ref.Space = el.Space
	ref.SetText(flow)
	el.InsertChildAt(pos, ref)
	return true
}
