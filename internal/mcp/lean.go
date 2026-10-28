package mcp

import (
	"encoding/json"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// leanShapers make the result of a tool smaller for an agent: the fields it
// needs to decide the next step. With argument detail "full" a tool returns
// every field. The server shapes after the tool ran, so the loop ledger and
// Go callers see the full result.
var leanShapers = map[string]func(any) any{
	"list_message_logs":       leanMessageLogs,
	"find_messages_by_header": leanMessageLogs,
	"get_trace_tree":          leanTraceTree,
	"get_message_log":         leanMessageLogDetail,
}

// asMap returns a result as a JSON object (nil if it is none).
func asMap(v any) map[string]any {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	return m
}

// sameError marks an error text that the result already has elsewhere.
const sameError = "same as firstFailure.errorText"

// leanTraceTree writes the error text once (firstFailure) and puts each
// hop's link kind on the node it leads to instead of a separate hop list.
func leanTraceTree(v any) any {
	m := asMap(v)
	if m == nil {
		return v
	}
	ff, _ := m["firstFailure"].(map[string]any)
	ffText, _ := ff["errorText"].(string)
	links := map[string]any{}
	hops, _ := m["hops"].([]any)
	for _, h := range hops {
		if h, ok := h.(map[string]any); ok {
			if to, ok := h["toMessage"].(string); ok {
				links[to] = h["link"]
			}
		}
	}
	var walk func(nodes []any)
	walk = func(nodes []any) {
		for _, n := range nodes {
			node, ok := n.(map[string]any)
			if !ok {
				continue
			}
			guid, _ := node["messageGuid"].(string)
			if l, ok := links[guid]; ok {
				node["link"] = l
			}
			if ffText != "" && node["errorText"] == ffText {
				node["errorText"] = sameError
			}
			children, _ := node["children"].([]any)
			if len(children) == 0 {
				delete(node, "children")
			}
			walk(children)
		}
	}
	roots, _ := m["roots"].([]any)
	walk(roots)
	if len(hops) > 0 {
		delete(m, "hops")
	}
	m["detail"] = "lean; detail=full adds hops and repeated error texts"
	return m
}

// leanMessageLogDetail writes the message's error text once and refers to
// the failed step instead of repeating it.
func leanMessageLogDetail(v any) any {
	m := asMap(v)
	if m == nil {
		return v
	}
	text, _ := m["errorText"].(string)
	if m["artifactName"] == m["artifactId"] {
		delete(m, "artifactName")
	}
	steps, _ := m["steps"].(map[string]any)
	if steps == nil {
		return m
	}
	runs, _ := steps["runs"].([]any)
	for _, r := range runs {
		run, ok := r.(map[string]any)
		if !ok {
			continue
		}
		list, _ := run["steps"].([]any)
		for _, st := range list {
			step, ok := st.(map[string]any)
			if !ok {
				continue
			}
			if step["runId"] == run["id"] {
				delete(step, "runId")
			}
			if text != "" && step["error"] == text {
				step["error"] = "same as errorText"
			}
		}
	}
	if f, ok := steps["failedStep"].(map[string]any); ok {
		ref := map[string]any{"stepId": f["stepId"], "modelStepId": f["modelStepId"], "activity": f["activity"]}
		if e, ok := f["error"].(string); ok && e != text {
			ref["error"] = e
		}
		steps["failedStep"] = ref
	}
	if steps["messageGuid"] == m["messageGuid"] {
		delete(steps, "messageGuid")
	}
	m["detail"] = "lean; detail=full repeats the failed step and error texts"
	return m
}

const detailProp = "detail"

// withDetail adds the detail argument to the tools with a lean shape.
func withDetail(tools []Tool) {
	for _, t := range tools {
		if _, ok := leanShapers[t.Name]; !ok {
			continue
		}
		schema, ok := t.InputSchema["properties"].(props)
		if !ok {
			continue
		}
		schema[detailProp] = map[string]any{"type": "string", "enum": []string{"lean", "full"},
			"description": "lean (default): the fields for triage; full: every field"}
	}
}

// splitDetail removes the detail argument of a tool with a lean shape, so
// that the tool's handler does not see it, and reports whether the full
// result was asked for.
func splitDetail(tool string, args json.RawMessage) (json.RawMessage, bool) {
	if _, ok := leanShapers[tool]; !ok {
		return args, true
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

// leanMessageLog is a message log row for triage.
type leanMessageLog struct {
	MessageGuid          string     `json:"messageGuid"`
	Status               string     `json:"status"`
	CustomStatus         string     `json:"customStatus,omitempty"`
	ArtifactID           string     `json:"artifactId,omitempty"`
	ArtifactName         string     `json:"artifactName,omitempty"`
	PackageID            string     `json:"packageId,omitempty"`
	CorrelationID        string     `json:"correlationId,omitempty"`
	ApplicationMessageID string     `json:"applicationMessageId,omitempty"`
	Sender               string     `json:"sender,omitempty"`
	Receiver             string     `json:"receiver,omitempty"`
	LogEnd               *time.Time `json:"logEnd,omitempty"`
	DurationMs           int64      `json:"durationMs,omitempty"`
	ErrorText            string     `json:"errorText,omitempty"`
	ErrorTruncated       bool       `json:"errorTruncated,omitempty"`
}

const leanLogsNote = "lean; detail=full adds packageName, predecessor, logStart, logLevel, webLink"

func leanMessageLogs(v any) any {
	l, ok := v.(*ops.MessageLogList)
	if !ok || l == nil {
		return v
	}
	rows := make([]leanMessageLog, 0, len(l.Logs))
	for _, m := range l.Logs {
		r := leanMessageLog{MessageGuid: m.MessageGuid, Status: m.Status, CustomStatus: m.CustomStatus, ArtifactID: m.ArtifactID, PackageID: m.PackageID,
			CorrelationID: m.CorrelationID, ApplicationMessageID: m.ApplicationMessageID, Sender: m.Sender, Receiver: m.Receiver,
			LogEnd: m.LogEnd, DurationMs: m.DurationMs, ErrorText: m.ErrorText, ErrorTruncated: m.ErrorTruncated}
		if m.ArtifactName != m.ArtifactID {
			r.ArtifactName = m.ArtifactName
		}
		rows = append(rows, r)
	}
	return struct {
		Total     int              `json:"total"`
		Logs      []leanMessageLog `json:"logs"`
		Filter    string           `json:"filter,omitempty"`
		Scanned   int              `json:"scanned,omitempty"`
		Truncated bool             `json:"truncated,omitempty"`
		Detail    string           `json:"detail"`
	}{l.Total, rows, l.Filter, l.Scanned, l.Truncated, leanLogsNote}
}
