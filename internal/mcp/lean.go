package mcp

import (
	"encoding/json"
	"slices"
	"time"
)

// leanSpecs make the result of a tool smaller for an agent: the fields it
// needs to decide the next step, each text once. With argument detail
// "full" (or the server's --full-results) a tool returns every field. They
// run in the view step (view.go), after the tool: the loop ledger, stats
// and Go callers see the full result.
var leanSpecs = map[string]func(v *jv){
	"list_message_logs":       leanMessageLogs,
	"find_messages_by_header": leanMessageLogs,
	"get_trace_tree":          leanTraceTree,
	"get_message_log":         leanMessageLogDetail,
	"message_summary":         leanMessageSummary,
}

const detailProp = "detail"

// withDetail adds the detail argument to the tools with a lean shape.
func withDetail(tools []Tool) {
	for _, t := range tools {
		if _, ok := leanSpecs[t.Name]; !ok {
			continue
		}
		schema, ok := t.InputSchema["properties"].(props)
		if !ok {
			continue
		}
		schema[detailProp] = map[string]any{"type": "string", "enum": []string{"lean", "full"},
			"description": "lean (default) or full (every field)"}
	}
}

// splitDetail removes the detail argument of a tool with a lean shape, so
// that the tool's handler does not see it, and reports whether the full
// result was asked for.
func splitDetail(tool string, args json.RawMessage) (json.RawMessage, bool) {
	if _, ok := leanSpecs[tool]; !ok {
		return args, false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(args, &m) != nil {
		return args, false
	}
	raw, ok := m[detailProp]
	if !ok {
		return args, false
	}
	var d string
	_ = json.Unmarshal(raw, &d)
	delete(m, detailProp)
	out, err := json.Marshal(m)
	if err != nil {
		return args, false
	}
	return out, d == "full"
}

// dropSameAs removes key when it equals other (e.g. a name equal to the ID).
func dropSameAs(v *jv, key, other string) {
	if k := v.get(key); k != nil && k.equal(v.get(other)) {
		v.del(key)
	}
}

// leanMessageLogs keeps the triage fields of each message log row.
func leanMessageLogs(v *jv) {
	logs := v.get("logs")
	if !logs.isArray() {
		return
	}
	for _, row := range logs.vals {
		dropSameAs(row, "artifactName", "artifactId")
		row.keep("messageGuid", "status", "customStatus", "artifactId", "artifactName", "packageId", "correlationId",
			"applicationMessageId", "sender", "receiver", "logEnd", "durationMs", "errorText", "errorTruncated")
	}
	v.set(detailProp, jvString("lean; detail=full adds packageName, predecessor, logStart, logLevel, webLink"))
}

// sameError marks an error text that the result already has elsewhere.
const sameError = "same as firstFailure.errorText"

// leanTraceTree writes the error text once (firstFailure) and puts each
// hop's link kind on the node it leads to instead of a separate hop list.
func leanTraceTree(v *jv) {
	ffText := v.get("firstFailure").get("errorText").text()
	links := map[string]*jv{}
	hops := v.get("hops")
	if hops.isArray() {
		for _, h := range hops.vals {
			if to := h.get("toMessage").text(); to != "" {
				links[to] = h.get("link")
			}
		}
	}
	var walk func(nodes *jv)
	walk = func(nodes *jv) {
		if !nodes.isArray() {
			return
		}
		for _, node := range nodes.vals {
			if l := links[node.get("messageGuid").text()]; l != nil {
				node.set("link", l)
			}
			if ffText != "" && node.get("errorText").text() == ffText {
				node.set("errorText", jvString(sameError))
			}
			walk(node.get("children"))
		}
	}
	walk(v.get("roots"))
	if hops != nil {
		v.del("hops")
	}
	v.set(detailProp, jvString("lean; detail=full adds hops and repeated error texts"))
}

// leanMessageLogDetail writes the message's error text once and refers to
// the failed step instead of repeating it.
func leanMessageLogDetail(v *jv) {
	text := v.get("errorText").text()
	dropSameAs(v, "artifactName", "artifactId")
	steps := v.get("steps")
	if !steps.isObject() {
		return
	}
	if runs := steps.get("runs"); runs.isArray() {
		for _, run := range runs.vals {
			if list := run.get("steps"); list.isArray() {
				for _, step := range list.vals {
					if step.get("runId").equal(run.get("id")) {
						step.del("runId")
					}
					if text != "" && step.get("error").text() == text {
						step.set("error", jvString("same as errorText"))
					}
				}
			}
		}
	}
	if f := steps.get("failedStep"); f.isObject() {
		if f.get("error").text() == text {
			f.del("error")
		}
		f.keep("stepId", "modelStepId", "activity", "error")
	}
	if steps.get("messageGuid").equal(v.get("messageGuid")) {
		steps.del("messageGuid")
	}
	v.set(detailProp, jvString("lean; detail=full repeats the failed step and error texts"))
}

// leanMessageSummary keeps per error group what triage needs (no sampled
// occurrences, no time buckets) and merges the groups of flows that pass a
// failure on: the same error text in a caller and the flow it calls (an
// edge of the summary) is one group, reported where it failed (the callee)
// with the flows it went through.
func leanMessageSummary(v *jv) {
	v.del("bucketSeconds")
	errors := v.get("errors")
	if !errors.isArray() {
		return
	}
	for _, g := range errors.vals {
		g.keep("fingerprint", "artifact", "step", "sample", "count", "first", "last", "messageGuid")
	}
	if edges := v.get("edges"); edges.isArray() {
		for mergeCallerGroup(errors, edges) {
		}
	}
	v.set(detailProp, jvString("lean; detail=full adds occurrences, time buckets and one group per flow"))
}

// mergeCallerGroup merges one error group of a caller into the group of a
// flow it calls with the same error text; it reports whether it did.
func mergeCallerGroup(errors, edges *jv) bool {
	for _, e := range edges.vals {
		from, to := e.get("from").text(), e.get("to").text()
		for ci, caller := range errors.vals {
			if !groupHas(caller, from) || groupHas(caller, to) {
				continue
			}
			callee := slices.IndexFunc(errors.vals, func(g *jv) bool {
				return g != caller && groupHas(g, to) && g.get("sample").equal(caller.get("sample"))
			})
			if callee < 0 {
				continue
			}
			mergeGroup(errors.vals[callee], caller)
			errors.vals = slices.Delete(errors.vals, ci, ci+1)
			return true
		}
	}
	return false
}

// groupHas reports whether an error group is of a flow (its artifact, or
// one of the flows merged into it).
func groupHas(g *jv, flow string) bool {
	if g.get("artifact").text() == flow {
		return true
	}
	if fl := g.get("flows"); fl.isArray() {
		return slices.ContainsFunc(fl.vals, func(f *jv) bool { return f.text() == flow })
	}
	return false
}

// mergeGroup merges a caller's group into the callee's: flows in call
// order, the larger count, the earliest first and latest last.
func mergeGroup(callee, caller *jv) {
	flows := jvArray()
	for _, g := range []*jv{caller, callee} {
		if fl := g.get("flows"); fl.isArray() {
			for _, f := range fl.vals {
				if !slices.ContainsFunc(flows.vals, func(x *jv) bool { return x.equal(f) }) {
					flows.vals = append(flows.vals, f)
				}
			}
		} else if a := g.get("artifact"); a != nil && !slices.ContainsFunc(flows.vals, func(x *jv) bool { return x.equal(a) }) {
			flows.vals = append(flows.vals, a)
		}
	}
	callee.set("flows", flows)
	if c1, ok := caller.get("count").number(); ok {
		if c2, _ := callee.get("count").number(); c1 > c2 {
			callee.set("count", caller.get("count"))
		}
	}
	if earlier(caller.get("first"), callee.get("first")) {
		callee.set("first", caller.get("first"))
	}
	if earlier(callee.get("last"), caller.get("last")) {
		callee.set("last", caller.get("last"))
	}
}

func earlier(a, b *jv) bool {
	ta, err1 := time.Parse(time.RFC3339Nano, a.text())
	tb, err2 := time.Parse(time.RFC3339Nano, b.text())
	return err1 == nil && err2 == nil && ta.Before(tb)
}
