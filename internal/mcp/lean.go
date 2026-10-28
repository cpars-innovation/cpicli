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
	CorrelationID        string     `json:"correlationId,omitempty"`
	ApplicationMessageID string     `json:"applicationMessageId,omitempty"`
	Sender               string     `json:"sender,omitempty"`
	Receiver             string     `json:"receiver,omitempty"`
	LogEnd               *time.Time `json:"logEnd,omitempty"`
	DurationMs           int64      `json:"durationMs,omitempty"`
	ErrorText            string     `json:"errorText,omitempty"`
	ErrorTruncated       bool       `json:"errorTruncated,omitempty"`
}

const leanLogsNote = "lean rows: packageId, packageName, predecessorMessageGuid, logStart, logLevel, webLink and an artifactName equal to artifactId are left out; detail=full for all fields"

func leanMessageLogs(v any) any {
	l, ok := v.(*ops.MessageLogList)
	if !ok || l == nil {
		return v
	}
	rows := make([]leanMessageLog, 0, len(l.Logs))
	for _, m := range l.Logs {
		r := leanMessageLog{MessageGuid: m.MessageGuid, Status: m.Status, CustomStatus: m.CustomStatus, ArtifactID: m.ArtifactID,
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
