package ops

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// LogLevels are the message processing log levels of a deployed flow.
var LogLevels = []string{"NONE", "INFO", "DEBUG", "TRACE"}

// TraceDuration is how long the tenant keeps a flow on TRACE before it
// falls back to its previous level.
const TraceDuration = 10 * time.Minute

// setLogLevelPath is the Web UI operations command that sets the log level
// (there is no public OData API for it).
const setLogLevelPath = "/Operations/com.sap.it.op.tmn.commands.dashboard.webui.IntegrationComponentSetMplLogLevelCommand"

// LogLevelRequest sets the log level of a deployed integration flow.
type LogLevelRequest struct {
	ArtifactID string
	Level      string
	// NodeType (default IFLMAP) and RuntimeLocationID (default
	// cloudintegration) select the runtime; the defaults fit Cloud Foundry
	// tenants without edge integration cells.
	NodeType          string
	RuntimeLocationID string
}

// LogLevelResult is the outcome of SetLogLevel.
type LogLevelResult struct {
	ArtifactID string `json:"artifactId"`
	Level      string `json:"level"`
	// Until is when TRACE ends (approximate, set by the tenant).
	Until *time.Time `json:"until,omitempty"`
	// RevertsAt is when the MCP server sets the flow back to INFO.
	RevertsAt *time.Time `json:"revertsAt,omitempty"`
}

// SetLogLevel changes the message processing log level of a deployed flow.
// TRACE records the payload and headers at every step (see GetMessageTrace)
// for the next 10 minutes; use it on development tenants only, traces contain
// business data.
func SetLogLevel(exe *httpclnt.HTTPExecuter, req LogLevelRequest) (*LogLevelResult, error) {
	level := strings.ToUpper(req.Level)
	if req.ArtifactID == "" {
		return nil, output.Usagef("artifact ID is required")
	}
	if !slices.Contains(LogLevels, level) {
		return nil, output.Usagef("invalid log level %q (%s)", req.Level, strings.Join(LogLevels, ", "))
	}
	if req.NodeType == "" {
		req.NodeType = "IFLMAP"
	}
	if req.RuntimeLocationID == "" {
		req.RuntimeLocationID = "cloudintegration"
	}
	body, _ := json.Marshal(map[string]string{"artifactSymbolicName": req.ArtifactID, "mplLogLevel": level,
		"nodeType": req.NodeType, "runtimeLocationId": req.RuntimeLocationID})
	// The Web UI command does not answer with application/json: a JSON-only
	// Accept header gets 406 Not Acceptable, so accept any representation (the
	// body is not used).
	resp, err := exe.Exec(http.MethodPost, setLogLevelPath, bytes.NewReader(body), map[string]string{"Content-Type": "application/json", "Accept": "*/*"})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		_, err = exe.LogError(resp, "Set log level")
		return nil, err
	}
	_, _ = exe.ReadRespBody(resp)
	res := &LogLevelResult{ArtifactID: req.ArtifactID, Level: level}
	if level == "TRACE" {
		until := time.Now().UTC().Add(TraceDuration).Truncate(time.Second)
		res.Until = &until
	}
	return res, nil
}

// StepTrace is a processing step with the trace messages recorded for it.
type StepTrace struct {
	cpi.MessageRunStep
	Traces []cpi.TraceMessage `json:"traces"`
}

// MessageTrace lists what was traced for a message.
type MessageTrace struct {
	MessageGuid string `json:"messageGuid"`
	// Status is "missing_role" when the tenant refuses trace content (403);
	// empty otherwise.
	Status string      `json:"status,omitempty"`
	Steps  []StepTrace `json:"steps"`
	// Hint explains an empty result.
	Hint string `json:"hint,omitempty"`
}

// GetMessageTrace lists the traced steps of a message (optionally only the
// steps of one model element) with their trace IDs; GetTraceMessage reads one.
func GetMessageTrace(exe *httpclnt.HTTPExecuter, guid, modelStepID string) (*MessageTrace, error) {
	steps, err := GetMessageSteps(exe, guid)
	if err != nil {
		return nil, err
	}
	mpl := cpi.NewMessageLogs(exe)
	res := &MessageTrace{MessageGuid: guid, Steps: []StepTrace{}}
	for _, run := range steps.Runs {
		for _, st := range run.Steps {
			if modelStepID != "" && st.ModelStepID != modelStepID {
				continue
			}
			traces, err := mpl.TraceMessages(st.RunID, st.ChildCount)
			if httpclnt.StatusCode(err) == http.StatusForbidden {
				res.Status, res.Hint, res.Steps = TraceMissingRole, missingRoleHint, []StepTrace{}
				return res, nil
			}
			if err != nil {
				if httpclnt.StatusCode(err) == http.StatusNotFound {
					continue
				}
				return nil, err
			}
			if len(traces) > 0 {
				res.Steps = append(res.Steps, StepTrace{MessageRunStep: st, Traces: traces})
			}
		}
	}
	if len(res.Steps) == 0 {
		res.Hint = "no trace data: set the flow's log level to TRACE (set_log_level), send the message again within 10 minutes; traces are kept for about an hour"
	}
	return res, nil
}

// TraceMessageDetail is the payload and headers of a message at one step.
type TraceMessageDetail struct {
	TraceID string `json:"traceId"`
	// Status and Hint are set when the tenant refuses trace content (403).
	Status             string          `json:"status,omitempty"`
	Hint               string          `json:"hint,omitempty"`
	Payload            Content         `json:"payload"`
	Headers            []cpi.NameValue `json:"headers"`
	ExchangeProperties []cpi.NameValue `json:"exchangeProperties"`
}

// TraceMissingRole is the status of trace results the tenant refuses (403).
const TraceMissingRole = "missing_role"

const missingRoleHint = "the tenant refused trace content (HTTP 403): the service key needs a role that allows reading message content (trace and payloads); continue without it and tell the user"

var traceIDPattern = regexp.MustCompile(`^[0-9]+$`)

var sensitiveName = regexp.MustCompile(`(?i)authorization|cookie|password|passwd|secret|token|credential|apikey|api-key`)

// SensitiveName reports whether the value of a header or property with this
// name is masked in results: authorization, cookies, tokens, passwords,
// secrets, credentials and API keys.
func SensitiveName(name string) bool { return sensitiveName.MatchString(name) }

// GetTraceMessage returns the payload (truncated to max bytes), headers and
// exchange properties of a trace message. Values of headers and properties
// whose names look sensitive (authorization, cookies, tokens, passwords) are
// masked.
func GetTraceMessage(exe *httpclnt.HTTPExecuter, traceID string, max int) (*TraceMessageDetail, error) {
	if !traceIDPattern.MatchString(traceID) {
		return nil, output.Usagef("invalid trace ID %q (a number from get_message_trace)", traceID)
	}
	mpl := cpi.NewMessageLogs(exe)
	payload, err := mpl.TracePayload(traceID)
	if httpclnt.StatusCode(err) == http.StatusForbidden {
		return &TraceMessageDetail{TraceID: traceID, Status: TraceMissingRole, Hint: missingRoleHint,
			Headers: []cpi.NameValue{}, ExchangeProperties: []cpi.NameValue{}}, nil
	}
	if err != nil {
		return nil, err
	}
	d := &TraceMessageDetail{TraceID: traceID, Payload: NewContent(payload, max)}
	if d.Headers, err = mpl.TraceHeaders(traceID, false); err != nil {
		return nil, err
	}
	if d.ExchangeProperties, err = mpl.TraceHeaders(traceID, true); err != nil {
		return nil, err
	}
	for _, list := range [][]cpi.NameValue{d.Headers, d.ExchangeProperties} {
		for i := range list {
			if SensitiveName(list[i].Name) {
				list[i].Value = "***"
			}
		}
	}
	return d, nil
}

// String implements fmt.Stringer for log output.
func (r LogLevelResult) String() string {
	if r.Until != nil {
		return fmt.Sprintf("%s: log level %s until about %s", r.ArtifactID, r.Level, r.Until.Format(time.RFC3339))
	}
	return fmt.Sprintf("%s: log level %s", r.ArtifactID, r.Level)
}
