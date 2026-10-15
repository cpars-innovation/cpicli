package ops

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// FinalMessageStatuses are message statuses that no longer change.
var FinalMessageStatuses = []string{"COMPLETED", "FAILED", "ESCALATED", "CANCELLED", "DISCARDED", "ABANDONED"}

// errorMessageStatuses are statuses that carry error information.
var errorMessageStatuses = []string{"FAILED", "ESCALATED", "RETRY", "ABANDONED"}

// MaxMessageLogs caps one query so a result stays small enough for an agent.
const MaxMessageLogs = 200

// MessageLogQuery selects message processing logs.
type MessageLogQuery struct {
	ArtifactID           string
	Statuses             []string
	Since, Until         time.Time
	CorrelationID        string
	ApplicationMessageID string
	Top, Skip            int
	// IncludeErrors fetches the error text of failed messages (one extra call each).
	IncludeErrors bool
	// MaxErrorBytes truncates error texts (0: 4096).
	MaxErrorBytes int
}

// MessageLog is the agent-facing view of a message processing log.
type MessageLog struct {
	MessageGuid            string     `json:"messageGuid"`
	Status                 string     `json:"status"`
	CustomStatus           string     `json:"customStatus,omitempty"`
	ArtifactID             string     `json:"artifactId,omitempty"`
	ArtifactName           string     `json:"artifactName,omitempty"`
	PackageID              string     `json:"packageId,omitempty"`
	PackageName            string     `json:"packageName,omitempty"`
	CorrelationID          string     `json:"correlationId,omitempty"`
	ApplicationMessageID   string     `json:"applicationMessageId,omitempty"`
	ApplicationMessageType string     `json:"applicationMessageType,omitempty"`
	PredecessorMessageGuid string     `json:"predecessorMessageGuid,omitempty"`
	Sender                 string     `json:"sender,omitempty"`
	Receiver               string     `json:"receiver,omitempty"`
	LogStart               *time.Time `json:"logStart,omitempty"`
	LogEnd                 *time.Time `json:"logEnd,omitempty"`
	DurationMs             int64      `json:"durationMs,omitempty"`
	LogLevel               string     `json:"logLevel,omitempty"`
	WebLink                string     `json:"webLink,omitempty"`
	ErrorText              string     `json:"errorText,omitempty"`
	ErrorTruncated         bool       `json:"errorTruncated,omitempty"`
}

// MessageLogList is the result of QueryMessageLogs.
type MessageLogList struct {
	Total  int          `json:"total"`
	Logs   []MessageLog `json:"logs"`
	Filter string       `json:"filter,omitempty"`
	// Scanned and Truncated are set by client-side scans (custom header
	// filter): how many messages were read, and whether the cap was hit.
	Scanned   int  `json:"scanned,omitempty"`
	Truncated bool `json:"truncated,omitempty"`
}

func toMessageLog(l cpi.MessageLog) MessageLog {
	m := MessageLog{
		MessageGuid: l.MessageGuid, Status: l.Status, CustomStatus: l.CustomStatus,
		ArtifactID: l.ArtifactId, ArtifactName: l.ArtifactName, PackageID: l.PackageId,
		CorrelationID: l.CorrelationId, ApplicationMessageID: l.ApplicationMessageId,
		ApplicationMessageType: l.ApplicationMessageType, PredecessorMessageGuid: l.PredecessorMessageGuid, PackageName: l.PackageName,
		Sender: l.Sender, Receiver: l.Receiver, LogLevel: l.LogLevel, WebLink: l.AlternateWebLink,
	}
	if m.CustomStatus == m.Status {
		m.CustomStatus = ""
	}
	if !l.LogStart.IsZero() {
		start := l.LogStart
		m.LogStart = &start
	}
	if !l.LogEnd.IsZero() {
		end := l.LogEnd
		m.LogEnd = &end
		if m.LogStart != nil {
			m.DurationMs = end.Sub(*m.LogStart).Milliseconds()
		}
	}
	return m
}

func truncate(s string, max int) (string, bool) {
	if max <= 0 {
		max = 4096
	}
	if len(s) <= max {
		return s, false
	}
	return s[:max], true
}

// QueryMessageLogs returns message processing logs, newest first.
func QueryMessageLogs(exe *httpclnt.HTTPExecuter, q MessageLogQuery) (*MessageLogList, error) {
	if q.Top > MaxMessageLogs {
		return nil, output.Usagef("top must be at most %d", MaxMessageLogs)
	}
	cq := cpi.MessageLogQuery{
		ArtifactID: q.ArtifactID, Statuses: q.Statuses, Since: q.Since, Until: q.Until,
		CorrelationID: q.CorrelationID, ApplicationMessageID: q.ApplicationMessageID, Top: q.Top, Skip: q.Skip,
	}
	filter, err := cq.Filter()
	if err != nil {
		return nil, output.Usage(err)
	}
	mpl := cpi.NewMessageLogs(exe)
	logs, total, err := mpl.Query(cq)
	if err != nil {
		return nil, err
	}
	res := &MessageLogList{Total: total, Logs: make([]MessageLog, 0, len(logs)), Filter: filter}
	for _, l := range logs {
		m := toMessageLog(l)
		if q.IncludeErrors && slices.Contains(errorMessageStatuses, l.Status) {
			if text, err := mpl.ErrorText(l.MessageGuid); err == nil {
				m.ErrorText, m.ErrorTruncated = truncate(text, q.MaxErrorBytes)
			}
		}
		res.Logs = append(res.Logs, m)
	}
	return res, nil
}

// TimeoutError reports that waiting for messages did not finish in time.
type TimeoutError struct{ Msg string }

func (e *TimeoutError) Error() string { return e.Msg }
func (e *TimeoutError) ExitCode() int { return exitcode.Timeout }

// WaitForMessageLogs polls until at least one message matches q and all
// matching messages are in a final status (see FinalMessageStatuses), or
// until timeout. Use Since = the time a test message was sent. On timeout the
// last result is returned together with a *TimeoutError.
func WaitForMessageLogs(ctx context.Context, exe *httpclnt.HTTPExecuter, q MessageLogQuery, timeout, interval time.Duration) (*MessageLogList, error) {
	return waitForMessageLogs(ctx, exe, q, timeout, interval, nil)
}

func waitForMessageLogs(ctx context.Context, exe *httpclnt.HTTPExecuter, q MessageLogQuery, timeout, interval time.Duration, prog *progress) (*MessageLogList, error) {
	exe = exe.WithContext(ctx)
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		res, err := QueryMessageLogs(exe, q)
		if err != nil && !isTransient(err) {
			return res, err
		}
		if err == nil && len(res.Logs) > 0 && allFinal(res.Logs) {
			return res, nil
		}
		if res != nil && len(res.Logs) > 0 {
			prog.report(0, "%d message(s) found, waiting for a final status (poll %d)", len(res.Logs), prog.steps()+1)
		} else {
			prog.report(0, "waiting for the message processing log (poll %d)", prog.steps()+1)
		}
		if time.Now().Add(interval).After(deadline) {
			msg := fmt.Sprintf("no final message processing log within %s", timeout)
			if res != nil && len(res.Logs) > 0 {
				msg = fmt.Sprintf("%d message(s) found, but not all reached a final status within %s", len(res.Logs), timeout)
			}
			return res, &TimeoutError{Msg: msg}
		}
		if err := sleep(ctx, interval); err != nil {
			return res, err
		}
	}
}

func allFinal(logs []MessageLog) bool {
	for _, l := range logs {
		if !slices.Contains(FinalMessageStatuses, l.Status) {
			return false
		}
	}
	return true
}

func isTransient(err error) bool {
	code := httpclnt.StatusCode(err)
	return code >= 500 || code == 429
}

// MessageLogDetail is one message with error text, headers, attachments and
// persisted messages.
type MessageLogDetail struct {
	MessageLog
	CustomHeaderProperties []cpi.NameValue         `json:"customHeaderProperties"`
	AdapterAttributes      []cpi.NameValue         `json:"adapterAttributes"`
	Attachments            []cpi.MessageAttachment `json:"attachments"`
	MessageStoreEntries    []cpi.MessageStoreEntry `json:"messageStoreEntries"`
	// ErrorInformation is the error type and the model step (id of the .iflw
	// element) where the message failed last, for failed messages.
	ErrorInformation *cpi.ErrorInformation `json:"errorInformation,omitempty"`
	// Warnings lists optional details that could not be read.
	Warnings []string `json:"warnings,omitempty"`
}

// GetMessageLog returns the details of one message. maxErrorBytes truncates
// the error text (0: 16384). Optional details that fail to load are reported
// in Warnings; authentication errors still fail the call.
func GetMessageLog(exe *httpclnt.HTTPExecuter, guid string, maxErrorBytes int) (*MessageLogDetail, error) {
	if guid == "" {
		return nil, output.Usagef("message GUID is required")
	}
	if maxErrorBytes <= 0 {
		maxErrorBytes = 16384
	}
	mpl := cpi.NewMessageLogs(exe)
	l, err := mpl.Get(guid)
	if err != nil {
		return nil, err
	}
	if l == nil {
		return nil, output.Usagef("message %s not found", guid)
	}
	d := &MessageLogDetail{MessageLog: toMessageLog(*l),
		CustomHeaderProperties: []cpi.NameValue{}, AdapterAttributes: []cpi.NameValue{},
		Attachments: []cpi.MessageAttachment{}, MessageStoreEntries: []cpi.MessageStoreEntry{}}

	// a detail the key may not read (403, 401) is a warning: the log itself
	// was readable (#19)
	optional := func(what string, err error) error {
		switch {
		case err == nil:
		case httpclnt.IsAuthError(err):
			hint := "the service key lacks the role to read it"
			if what == "message store entries" {
				hint = "the service key lacks esbmessagestorage.read"
			}
			d.Warnings = append(d.Warnings, fmt.Sprintf("%s: forbidden (HTTP %d), %s", what, httpclnt.StatusCode(err), hint))
		default:
			d.Warnings = append(d.Warnings, fmt.Sprintf("%s: %v", what, err))
		}
		return nil
	}
	if slices.Contains(errorMessageStatuses, l.Status) {
		text, err := mpl.ErrorText(guid)
		if err := optional("error text", err); err != nil {
			return nil, err
		}
		d.ErrorText, d.ErrorTruncated = truncate(text, maxErrorBytes)
		info, err := mpl.ErrorInformation(guid)
		if err := optional("error information", err); err != nil {
			return nil, err
		}
		d.ErrorInformation = info
	}
	if headers, err := mpl.CustomHeaderProperties(guid); optional("custom header properties", err) != nil {
		return nil, err
	} else if err == nil {
		d.CustomHeaderProperties = headers
	}
	if attrs, err := mpl.AdapterAttributes(guid); optional("adapter attributes", err) != nil {
		return nil, err
	} else if err == nil {
		d.AdapterAttributes = attrs
	}
	if atts, err := mpl.Attachments(guid); optional("attachments", err) != nil {
		return nil, err
	} else if err == nil {
		d.Attachments = atts
	}
	if entries, err := mpl.MessageStoreEntries(guid); optional("message store entries", err) != nil {
		return nil, err
	} else if err == nil {
		d.MessageStoreEntries = entries
	}
	return d, nil
}

// DownloadedContent is a downloaded attachment or persisted message.
type DownloadedContent struct {
	ID string `json:"id"`
	Content
}

// GetMessageAttachment downloads an MPL attachment (see NewContent for max).
func GetMessageAttachment(exe *httpclnt.HTTPExecuter, attachmentID string, max int) (*DownloadedContent, error) {
	if attachmentID == "" {
		return nil, output.Usagef("attachment ID is required (from the attachments of a message log)")
	}
	b, err := cpi.NewMessageLogs(exe).AttachmentContent(attachmentID)
	if err != nil {
		if httpclnt.StatusCode(err) == 404 {
			return nil, output.Usagef("attachment %s not found", attachmentID)
		}
		return nil, err
	}
	return &DownloadedContent{ID: attachmentID, Content: NewContent(b, max)}, nil
}

// GetMessageStoreEntry downloads a persisted message payload.
func GetMessageStoreEntry(exe *httpclnt.HTTPExecuter, entryID string, max int) (*DownloadedContent, error) {
	if entryID == "" {
		return nil, output.Usagef("message store entry ID is required (from messageStoreEntries of a message log)")
	}
	b, err := cpi.NewMessageLogs(exe).MessageStoreEntryContent(entryID)
	if err != nil {
		if httpclnt.StatusCode(err) == 404 {
			return nil, output.Usagef("message store entry %s not found", entryID)
		}
		return nil, err
	}
	return &DownloadedContent{ID: entryID, Content: NewContent(b, max)}, nil
}

// MessageRunWithSteps is a processing run and its steps.
type MessageRunWithSteps struct {
	cpi.MessageRun
	Steps []cpi.MessageRunStep `json:"steps"`
}

// MessageSteps is the step-level processing trace of a message.
type MessageSteps struct {
	MessageGuid string                `json:"messageGuid"`
	Runs        []MessageRunWithSteps `json:"runs"`
	// FailedStep is the first step that reported an error, if any.
	FailedStep *cpi.MessageRunStep `json:"failedStep,omitempty"`
}

// GetMessageSteps returns the processing runs and steps of a message, and the
// first failing step (to locate the error in the iFlow model: ModelStepID).
func GetMessageSteps(exe *httpclnt.HTTPExecuter, guid string) (*MessageSteps, error) {
	return GetMessageStepsWith(exe, guid, false)
}

// GetMessageStepsWith is GetMessageSteps; properties adds each step's
// properties (MessageProcessingLogRunStepProperties, read with $expand).
func GetMessageStepsWith(exe *httpclnt.HTTPExecuter, guid string, properties bool) (*MessageSteps, error) {
	if guid == "" {
		return nil, output.Usagef("message GUID is required")
	}
	mpl := cpi.NewMessageLogs(exe)
	runs, err := mpl.Runs(guid)
	if err != nil {
		if httpclnt.StatusCode(err) == 404 {
			return nil, output.Usagef("message %s not found", guid)
		}
		return nil, err
	}
	res := &MessageSteps{MessageGuid: guid, Runs: make([]MessageRunWithSteps, 0, len(runs))}
	for _, r := range runs {
		read := mpl.RunSteps
		if properties {
			read = mpl.RunStepsWithProperties
		}
		steps, err := read(r.ID)
		if err != nil {
			return nil, err
		}
		res.Runs = append(res.Runs, MessageRunWithSteps{MessageRun: r, Steps: steps})
		for i := range steps {
			st := strings.ToUpper(steps[i].Status)
			if res.FailedStep == nil && (steps[i].Error != "" || strings.Contains(st, "FAIL") || strings.Contains(st, "ERROR")) {
				step := steps[i]
				res.FailedStep = &step
			}
		}
	}
	return res, nil
}

// ParseTimeArg accepts "", a duration back from now (30m, 2h, 1d) or an
// RFC 3339 timestamp.
func ParseTimeArg(v string, now time.Time) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	if days, ok := strings.CutSuffix(v, "d"); ok {
		if d, err := time.ParseDuration(days + "h"); err == nil {
			return now.Add(-24 * d), nil
		}
	}
	if d, err := time.ParseDuration(v); err == nil {
		return now.Add(-d), nil
	}
	return time.Parse(time.RFC3339, v)
}
