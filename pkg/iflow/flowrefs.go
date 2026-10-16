package iflow

import (
	"regexp"
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

// ScriptStepGaps lists the properties a script step (a call activity with
// a script) lacks that SAP's editor writes: activityType Script,
// subActivityType (GroovyScript, JavaScript) and componentVersion. Check
// passes without them, but the runtime fails the step ("mappingClass" is
// null). Keys are element IDs, values the missing property names.
func ScriptStepGaps(m *Model) map[string][]string {
	out := map[string][]string{}
	for _, id := range m.Order {
		el := m.Elements[id]
		if el.Tag != "callActivity" || el.Props["script"] == "" {
			continue
		}
		var missing []string
		for _, k := range []string{"activityType", "subActivityType", "componentVersion"} {
			if el.Props[k] == "" {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			out[id] = missing
		}
	}
	return out
}

// CompleteScriptSteps adds the missing activityType, subActivityType and
// componentVersion to the script steps of doc (see ScriptStepGaps), taken
// from the step's cmdVariantUri (GroovyScript or JavaScript, version major.minor)
// or the script's file extension. It reports whether it changed doc.
func CompleteScriptSteps(doc *etree.Document) bool {
	changed := false
	for _, el := range doc.FindElements("//callActivity") {
		props := map[string]*etree.Element{}
		var ext *etree.Element
		for _, c := range el.ChildElements() {
			if c.Tag != "extensionElements" {
				continue
			}
			ext = c
			for _, p := range c.ChildElements() {
				if k := p.SelectElement("key"); p.Tag == "property" && k != nil {
					props[strings.TrimSpace(k.Text())] = p
				}
			}
		}
		value := func(k string) string {
			if p := props[k]; p != nil {
				if v := p.SelectElement("value"); v != nil {
					return strings.TrimSpace(v.Text())
				}
			}
			return ""
		}
		script := value("script")
		if ext == nil || script == "" {
			continue
		}
		kind, version := "GroovyScript", ""
		if m := reVariant.FindStringSubmatch(value("cmdVariantUri")); m != nil {
			kind, version = m[1], m[2]
		} else if strings.HasSuffix(strings.ToLower(script), ".js") {
			kind = "JavaScript"
		}
		want := map[string]string{"activityType": "Script", "subActivityType": kind}
		if version != "" {
			want["componentVersion"] = version
		}
		for _, k := range []string{"componentVersion", "activityType", "subActivityType"} {
			if want[k] == "" || value(k) != "" {
				continue
			}
			p := ext.CreateElement("property")
			p.Space = "ifl"
			p.CreateElement("key").SetText(k)
			p.CreateElement("value").SetText(want[k])
			changed = true
		}
	}
	return changed
}

// reVariant reads the component and its major.minor version from a script
// step's cmdVariantUri (ctype::FlowstepVariant/cname::GroovyScript/version::1.1.2).
var reVariant = regexp.MustCompile(`cname::(GroovyScript|JavaScript)/version::(\d+\.\d+)`)
