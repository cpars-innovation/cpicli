package ops

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/testtools"
)

// The receiver stub (testtools.StubID) answers requests to
// <runtime>/http/cpictl/stub/<rule>/... with the response configured for the
// rule in the Partner Directory and records every request as the message
// processing log attachment testtools.StubAttachment. Point a receiver of the
// flow under test at it, run the test, then read what the flow sent with
// StubCalls.

// StubCall is one request the receiver stub received: the recorded
// attachment plus the stub message's GUID.
type StubCall struct {
	Time        time.Time         `json:"time"`
	Rule        string            `json:"rule"`
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Query       string            `json:"query,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        string            `json:"body,omitempty"`
	MessageGuid string            `json:"messageGuid"`
	// Matched is the answer the stub chose: the name of the StubWhen entry
	// (when[<i>] without a name), "default" (the rule's own answer) or ""
	// (no answer configured: 200 {}).
	Matched string `json:"matched,omitempty"`
	// DurationMs is the processing time of the stub's message (delay
	// included).
	DurationMs int64 `json:"durationMs,omitempty"`
}

// maxStubLogs caps the stub messages StubCalls reads.
const maxStubLogs = 1000

// StubCalls returns the requests the receiver stub received since a time,
// oldest first: the stub's message processing logs since then (any status),
// each with its recorded request. Messages without the attachment (still
// processing, or failed before the script ran) are left out.
func StubCalls(ctx context.Context, exe *httpclnt.HTTPExecuter, since time.Time) ([]StubCall, error) {
	exe = exe.WithContext(ctx)
	mpl := cpi.NewMessageLogs(exe)
	var logs []cpi.MessageLog
	for skip := 0; skip < maxStubLogs; {
		page, total, err := mpl.Query(cpi.MessageLogQuery{ArtifactID: testtools.StubID, Since: since, Top: MaxMessageLogs, Skip: skip})
		if err != nil {
			return nil, err
		}
		logs = append(logs, page...)
		skip += len(page)
		if len(page) == 0 || skip >= total {
			break
		}
	}
	calls := []StubCall{}
	for _, l := range logs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		atts, err := mpl.Attachments(l.MessageGuid)
		if err != nil {
			return nil, err
		}
		for _, a := range atts {
			if a.Name != testtools.StubAttachment {
				continue
			}
			data, err := mpl.AttachmentContent(a.ID)
			if err != nil {
				return nil, err
			}
			var c StubCall
			if err := json.Unmarshal(data, &c); err != nil {
				return nil, output.Failed(fmt.Errorf("stub message %s: attachment %s is not a recorded request: %v", l.MessageGuid, a.Name, err))
			}
			c.MessageGuid = l.MessageGuid
			if c.Time.IsZero() {
				c.Time = l.LogStart
			}
			if !l.LogStart.IsZero() && l.LogEnd.After(l.LogStart) {
				c.DurationMs = l.LogEnd.Sub(l.LogStart).Milliseconds()
			}
			calls = append(calls, c)
			break
		}
	}
	sort.SliceStable(calls, func(i, j int) bool { return calls[i].Time.Before(calls[j].Time) })
	return calls, nil
}

// StubResponse is the answer of the receiver stub for one rule, stored as
// JSON in the Partner Directory (PID testtools.StubPID, parameter ID = rule).
type StubResponse struct {
	// Status is the HTTP status (100-599; 0: 200).
	Status int `json:"status"`
	// ContentType of the body (default application/json).
	ContentType string `json:"contentType"`
	Body        string `json:"body"`
	// Headers are additional response headers.
	Headers map[string]string `json:"headers"`
	// DelayMs delays the answer (0-60000), e.g. to test receiver timeouts.
	DelayMs int `json:"delayMs,omitempty"`
	// When lists conditional answers: the first entry whose conditions all
	// hold answers; without a match the rule's own answer does.
	When []StubWhen `json:"when,omitempty"`
}

// StubWhen is a conditional answer of a stub rule. Conditions (at least
// one): PathContains (the path after /cpictl/stub/), Header (name ->
// value, the name case-insensitive, the value equal), BodyContains. The
// answer fields it sets override the rule's answer; the others are the
// rule's (Body nil: the rule's body).
type StubWhen struct {
	Name         string            `json:"name,omitempty"`
	PathContains string            `json:"pathContains,omitempty"`
	Header       map[string]string `json:"header,omitempty"`
	BodyContains string            `json:"bodyContains,omitempty"`

	Status      int               `json:"status,omitempty"`
	ContentType string            `json:"contentType,omitempty"`
	Body        *string           `json:"body,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	DelayMs     int               `json:"delayMs,omitempty"`
}

// MaxStubDelayMs is the longest delay of a stub answer.
const MaxStubDelayMs = 60000

var (
	reStubRule   = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	reHeaderName = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]+$")
)

func checkStubRule(rule string) error {
	if !reStubRule.MatchString(rule) {
		return output.Usagef("invalid stub rule %q: letters, digits, '_', '.' and '-' (the first path segment after %s/)", rule, testtools.StubPath)
	}
	return nil
}

// normalize checks r and fills the defaults.
func (r StubResponse) normalize() (StubResponse, error) {
	if r.Status == 0 {
		r.Status = 200
	}
	if r.Status < 100 || r.Status > 599 {
		return r, output.Usagef("invalid status %d (100-599)", r.Status)
	}
	r.ContentType = cmp.Or(r.ContentType, "application/json")
	if r.Headers == nil {
		r.Headers = map[string]string{}
	}
	for k := range r.Headers {
		if !reHeaderName.MatchString(k) {
			return r, output.Usagef("invalid header name %q", k)
		}
	}
	if r.DelayMs < 0 || r.DelayMs > MaxStubDelayMs {
		return r, output.Usagef("invalid delay %d ms (0-%d)", r.DelayMs, MaxStubDelayMs)
	}
	names := map[string]bool{}
	for i, w := range r.When {
		label := cmp.Or(w.Name, fmt.Sprintf("when[%d]", i))
		switch {
		case w.PathContains == "" && len(w.Header) == 0 && w.BodyContains == "":
			return r, output.Usagef("%s has no condition (pathContains, header or bodyContains)", label)
		case w.Status != 0 && (w.Status < 100 || w.Status > 599):
			return r, output.Usagef("%s: invalid status %d (100-599)", label, w.Status)
		case w.DelayMs < 0 || w.DelayMs > MaxStubDelayMs:
			return r, output.Usagef("%s: invalid delay %d ms (0-%d)", label, w.DelayMs, MaxStubDelayMs)
		case names[label]:
			return r, output.Usagef("duplicate name %s in when", label)
		}
		names[label] = true
		for _, h := range []map[string]string{w.Header, w.Headers} {
			for k := range h {
				if !reHeaderName.MatchString(k) {
					return r, output.Usagef("%s: invalid header name %q", label, k)
				}
			}
		}
	}
	return r, nil
}

// SetStubResponse configures the receiver stub's answer for a rule (creates
// or updates the Partner Directory string parameter).
func SetStubResponse(exe *httpclnt.HTTPExecuter, rule string, r StubResponse) error {
	if err := checkStubRule(rule); err != nil {
		return err
	}
	r, err := r.normalize()
	if err != nil {
		return err
	}
	value, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = upsertString(cpi.NewPartnerDirectory(exe), cpi.StringParameter{Pid: testtools.StubPID, ID: rule, Value: string(value)}, false)
	return err
}

// GetStubResponse returns the configured answer of a rule (nil: none, the
// stub answers 200 with {}).
func GetStubResponse(exe *httpclnt.HTTPExecuter, rule string) (*StubResponse, error) {
	if err := checkStubRule(rule); err != nil {
		return nil, err
	}
	p, err := cpi.NewPartnerDirectory(exe).GetStringParameter(testtools.StubPID, rule)
	if err != nil || p == nil {
		return nil, err
	}
	var r StubResponse
	if err := json.Unmarshal([]byte(p.Value), &r); err != nil {
		return nil, output.Failed(fmt.Errorf("Partner Directory parameter %s/%s is not a stub response: %v", testtools.StubPID, rule, err))
	}
	return &r, nil
}

// ClearStubResponse removes the answer of a rule (the stub then answers 200
// with {}); a rule without one is not an error.
func ClearStubResponse(exe *httpclnt.HTTPExecuter, rule string) error {
	if err := checkStubRule(rule); err != nil {
		return err
	}
	pd := cpi.NewPartnerDirectory(exe)
	p, err := pd.GetStringParameter(testtools.StubPID, rule)
	if err != nil || p == nil {
		return err
	}
	return pd.DeleteStringParameter(testtools.StubPID, rule)
}
