package lint

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/cpars-innovation/cpicli/pkg/iflow"
	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// Rules on the structure of a flow: local processes, exception subprocesses
// and header names.

func isLocalProcess(el *iflow.Element) bool {
	return el.Tag == "process" && el.Kind() == "LocalIntegrationProcess"
}

func isProcessCall(el *iflow.Element) bool {
	return el.Props["activityType"] == "ProcessCallElement" || el.Kind() == "NonLoopingProcess" || el.Kind() == "LoopingProcess"
}

func isExceptionSubprocess(el *iflow.Element) bool {
	return el.Tag == "subProcess" && (el.TriggeredByEvent || strings.Contains(el.Kind(), "ErrorEventSubProcess"))
}

// within lists the elements a container holds, also those of nested
// subprocesses, in document order.
func within(m *iflow.Model, container string) []*iflow.Element {
	inside := map[string]bool{container: true}
	var out []*iflow.Element
	// document order puts a subprocess before its content
	for _, id := range m.Order {
		el := m.Elements[id]
		if inside[el.Container] {
			out = append(out, el)
			if el.Tag == "subProcess" {
				inside[el.ID] = true
			}
		}
	}
	return out
}

// raisesError reports whether a local process (or one it calls) ends in an
// Error or Escalation End event, so a caller's error stays an error.
func raisesError(m *iflow.Model, process string, seen map[string]bool) bool {
	if seen[process] {
		return false
	}
	seen[process] = true
	for _, el := range within(m, process) {
		if k := el.Kind(); k == "ErrorEndEvent" || k == "EscalationEndEvent" {
			return true
		}
		if isProcessCall(el) && el.Props["processId"] != "" && raisesError(m, el.Props["processId"], seen) {
			return true
		}
	}
	return false
}

// ruleExceptionSubprocessMessageEnd: an exception subprocess that ends in a
// Message End event marks the message as completed: the error is gone from
// monitoring and alerting. Fine when something in it raises the error again
// (a script, a called local process with an Error or Escalation End) or
// answers a synchronous sender with an error status (CamelHttpResponseCode).
func ruleExceptionSubprocessMessageEnd(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	eachElement(a, func(file string, m *iflow.Model, sub *iflow.Element) {
		if !isExceptionSubprocess(sub) {
			return
		}
		var messageEnd *iflow.Element
		steps, reraises := 0, false
		for _, el := range within(m, sub.ID) {
			switch {
			case el.Tag == "endEvent" && el.Kind() == "MessageEndEvent" && el.Container == sub.ID:
				messageEnd = el
			case el.IsStep():
				steps++
				if el.Props["activityType"] == "Script" || strings.HasSuffix(el.Kind(), "Script") ||
					isProcessCall(el) && raisesError(m, el.Props["processId"], map[string]bool{}) ||
					isContentModifier(el) && strings.Contains(el.Props["headerTable"], "CamelHttpResponseCode") {
					reraises = true
				}
			}
		}
		if messageEnd == nil || reraises {
			return
		}
		msg := fmt.Sprintf("the exception subprocess %q ends in the Message End %q: the message is marked completed and the error is lost", sub.Name, messageEnd.Name)
		if steps == 0 {
			msg = fmt.Sprintf("the exception subprocess %q does nothing and ends in a Message End: every error is swallowed", sub.Name)
		}
		out = append(out, a.finding("exception-subprocess-message-end", file, sub.ID, sub.Name, sub.ID, msg,
			"end it with an Error End (or Escalation End), or call a local process that does; for a synchronous sender set CamelHttpResponseCode"))
	})
	return out
}

// callsTo maps a local process ID to the process call steps that call it.
func callsTo(m *iflow.Model) map[string][]*iflow.Element {
	calls := map[string][]*iflow.Element{}
	for _, id := range m.Order {
		el := m.Elements[id]
		if isProcessCall(el) && el.Props["processId"] != "" {
			calls[el.Props["processId"]] = append(calls[el.Props["processId"]], el)
		}
	}
	return calls
}

// ruleDeadLocalProcess: a local process no process call references.
func ruleDeadLocalProcess(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	for _, file := range sortedModelFiles(a) {
		m := a.Models[file]
		calls := callsTo(m)
		for _, id := range m.Order {
			el := m.Elements[id]
			if !isLocalProcess(el) || len(calls[el.ID]) > 0 {
				continue
			}
			msg := fmt.Sprintf("the local process %q is not called by any process call", el.Name)
			if r := onlyReceiversOf(m, el.ID); len(r) > 0 {
				msg += "; only it uses the " + plural(len(r), "receiver ", "receivers ") + listIDs(r, 5)
			}
			out = append(out, a.finding("dead-local-process", file, el.ID, el.Name, el.ID, msg,
				"remove it (with its receivers), or call it where it was meant to run"))
		}
	}
	return out
}

// onlyReceiversOf names the receiver systems whose channels all start at
// steps of the process.
func onlyReceiversOf(m *iflow.Model, process string) []string {
	inside := map[string]bool{}
	for _, el := range within(m, process) {
		inside[el.ID] = true
	}
	names := map[string]string{}
	for _, p := range m.Participants {
		names[p.ID] = p.Name
	}
	used, other := map[string]bool{}, map[string]bool{}
	for _, ad := range m.Adapters {
		if _, ok := names[ad.Target]; !ok {
			continue
		}
		if inside[ad.Source] {
			used[ad.Target] = true
		} else {
			other[ad.Target] = true
		}
	}
	var out []string
	for id := range used {
		if !other[id] {
			n := names[id]
			if n == "" {
				n = id
			}
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// ruleInlineLocalProcess: a local process called once, without a loop, that
// is a straight line of steps adds a hop and a diagram to read for nothing.
func ruleInlineLocalProcess(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	for _, file := range sortedModelFiles(a) {
		m := a.Models[file]
		calls := callsTo(m)
		for _, id := range m.Order {
			el := m.Elements[id]
			c := calls[el.ID]
			if !isLocalProcess(el) || len(c) != 1 || c[0].Kind() != "NonLoopingProcess" || !linearProcess(m, el.ID) {
				continue
			}
			out = append(out, a.finding("inline-local-process", file, el.ID, el.Name, el.ID,
				fmt.Sprintf("the local process %q is called once (by %q) and is a straight line of steps", el.Name, c[0].Name),
				"move its steps into the calling process in place of the process call"))
		}
	}
	return out
}

// linearProcess: one start, one plain end, no gateways or subprocesses, and
// every element has at most one way in and one way out.
func linearProcess(m *iflow.Model, process string) bool {
	starts, ends := 0, 0
	for _, el := range within(m, process) {
		switch {
		case el.Tag == "subProcess", strings.Contains(el.Tag, "Gateway"), len(el.Incoming) > 1, len(el.Outgoing) > 1:
			return false
		case el.Tag == "startEvent":
			starts++
		case el.Tag == "endEvent":
			if el.Kind() != "EndEvent" && el.Kind() != "endEvent" {
				return false
			}
			ends++
		}
	}
	return starts == 1 && ends == 1
}

var reSetHeader = regexp.MustCompile(`setHeader\(\s*["']([^"'$]+)["']`)

// ruleDuplicateHeaderCase: Camel header names are case-insensitive, so
// OrderNo and orderNo are one header; which spelling a receiver sees depends
// on which step wrote first.
func ruleDuplicateHeaderCase(lc *lintContext, a *lintArtifact) []Finding {
	type use struct{ file, element, name string }
	spellings := map[string]map[string]use{} // lower-case -> spelling -> first use
	add := func(name string, u use) {
		low := strings.ToLower(name)
		if spellings[low] == nil {
			spellings[low] = map[string]use{}
		}
		if _, ok := spellings[low][name]; !ok {
			spellings[low][name] = u
		}
	}
	eachElement(a, func(file string, m *iflow.Model, el *iflow.Element) {
		if !isContentModifier(el) {
			return
		}
		for _, n := range ops.TableNames(el.Props["headerTable"]) {
			add(n, use{file, el.ID, el.Name})
		}
	})
	for _, file := range sortedKeys(a.Scripts) {
		for _, mm := range reSetHeader.FindAllStringSubmatch(string(a.Scripts[file]), -1) {
			add(mm[1], use{file: file})
		}
	}
	var out []Finding
	for _, low := range sortedKeys(spellings) {
		s := spellings[low]
		if len(s) < 2 {
			continue
		}
		names := sortedKeys(s)
		first := s[names[0]]
		var where []string
		for _, n := range names {
			w := s[n].file
			if s[n].name != "" {
				w = s[n].name
			}
			where = append(where, fmt.Sprintf("%s (%s)", n, w))
		}
		out = append(out, a.finding("duplicate-header-case", first.file, first.element, first.name, low,
			"the header is written as "+strings.Join(where, ", ")+": header names are case-insensitive, so these are one header",
			"use one spelling everywhere"))
	}
	return out
}
