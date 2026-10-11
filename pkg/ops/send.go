package ops

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// EndpointExecuterFunc returns an executer and request path for a runtime
// endpoint URL (see cpi.NewEndpointExecuter).
type EndpointExecuterFunc func(endpointURL string) (*httpclnt.HTTPExecuter, string, error)

// TestMessage is a message sent to a deployed integration flow.
type TestMessage struct {
	ArtifactID string
	// URL is one of the artifact's entry point URLs; empty: its only one.
	URL         string
	Method      string // default POST
	Body        []byte
	ContentType string
	Headers     map[string]string
	// MaxBytes limits the response body returned (see NewContent).
	MaxBytes int
	// Wait > 0 waits up to this long for the message processing log to reach
	// a final status.
	Wait         time.Duration
	PollInterval time.Duration
	// ProcessDirectAddress sends the message through the test harness flow
	// (Harness, default DefaultHarnessID) to this ProcessDirect address, for
	// flows without an HTTP sender. ArtifactID is the flow behind the address.
	ProcessDirectAddress string
	Harness              string
	// NoTrace sends no W3C traceparent header. By default one is generated
	// (unless Headers already has one) and its trace ID returned, so the
	// message can be followed with TraceTreeFor.
	NoTrace bool
}

// DefaultHarnessID is the test harness flow: an HTTPS sender that forwards
// the message to the ProcessDirect address in header HarnessAddressHeader.
const DefaultHarnessID = "CPICTL_Test_Harness"

// HarnessAddressHeader carries the ProcessDirect target address to the harness.
const HarnessAddressHeader = "CpictlTargetAddress"

// SentMessage is the outcome of SendTestMessage.
type SentMessage struct {
	ArtifactID string `json:"artifactId"`
	// Harness and HarnessMessageGuid are set when the message went through
	// the test harness; MessageGuid is then the message of ArtifactID.
	Harness              string `json:"harness,omitempty"`
	HarnessMessageGuid   string `json:"harnessMessageGuid,omitempty"`
	ProcessDirectAddress string `json:"processDirectAddress,omitempty"`
	// TraceID is the trace ID of the traceparent header sent (if any).
	TraceID             string    `json:"traceId,omitempty"`
	URL                 string    `json:"url"`
	SentAt              time.Time `json:"sentAt"`
	HTTPStatus          int       `json:"httpStatus"`
	MessageGuid         string    `json:"messageGuid,omitempty"`
	CorrelationID       string    `json:"correlationId,omitempty"`
	ResponseContentType string    `json:"responseContentType,omitempty"`
	Response            Content   `json:"response"`
	// Log is the processing log of the message (only with Wait).
	Log *MessageLogDetail `json:"log,omitempty"`
}

// headers that a test message may not set: authentication and session
// handling belong to the executer.
var reservedHeaders = []string{"authorization", "cookie", "x-csrf-token", "host", "content-length"}

// ResolveEndpoint returns the entry point URL to send to: url itself when it
// is one of the artifact's entry points, or the artifact's only entry point.
// Only URLs the tenant lists for the artifact are accepted.
func ResolveEndpoint(exe *httpclnt.HTTPExecuter, artifactID, url string) (string, error) {
	eps, err := cpi.NewContent(exe).ServiceEndpoints(artifactID)
	if err != nil {
		return "", err
	}
	var urls []string
	for _, ep := range eps {
		if ep.ArtifactID != artifactID {
			continue
		}
		for _, e := range ep.EntryPoints {
			urls = append(urls, e.URL)
		}
	}
	switch {
	case len(urls) == 0:
		return "", output.Usagef("integration flow %s has no endpoint: is it deployed and does it have an HTTP-based sender (HTTPS, SOAP, ...)?", artifactID)
	case url != "":
		if slices.Contains(urls, url) {
			return url, nil
		}
		return "", output.Usagef("%s is not an endpoint of %s (endpoints: %s)", url, artifactID, strings.Join(urls, ", "))
	case len(urls) > 1:
		return "", output.Usagef("%s has %d endpoints, choose one with url: %s", artifactID, len(urls), strings.Join(urls, ", "))
	}
	return urls[0], nil
}

// SendTestMessage sends m to an endpoint of a deployed integration flow and
// returns the HTTP outcome and the message GUID the runtime reports. A non-2xx
// answer is returned together with an error: 401/403 as an authentication
// error, anything else as failed (exit code 5).
func SendTestMessage(ctx context.Context, exe *httpclnt.HTTPExecuter, newExe EndpointExecuterFunc, m TestMessage) (*SentMessage, error) {
	exe = exe.WithContext(ctx)
	if m.ArtifactID == "" {
		return nil, output.Usagef("artifact ID is required")
	}
	method := strings.ToUpper(m.Method)
	if method == "" {
		method = http.MethodPost
	}
	if !slices.Contains([]string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodGet, http.MethodDelete}, method) {
		return nil, output.Usagef("invalid method %q (POST, PUT, PATCH, GET, DELETE)", m.Method)
	}
	headers := map[string]string{}
	for k, v := range m.Headers {
		if slices.Contains(reservedHeaders, strings.ToLower(k)) {
			return nil, output.Usagef("header %s cannot be set", k)
		}
		headers[k] = v
	}
	if m.ContentType != "" {
		headers["Content-Type"] = m.ContentType
	}
	traceID := ""
	for k, v := range headers {
		if strings.EqualFold(k, "traceparent") {
			traceID = TraceIDOf(v)
			if traceID == "" {
				return nil, output.Usagef("invalid traceparent header %q (expected 00-<32 hex>-<16 hex>-<2 hex>)", v)
			}
		}
	}
	if traceID == "" && !m.NoTrace {
		var tp string
		tp, traceID = NewTraceparent()
		headers["traceparent"] = tp
	}
	endpointArtifact := m.ArtifactID
	if m.ProcessDirectAddress != "" {
		if !strings.HasPrefix(m.ProcessDirectAddress, "/") {
			return nil, output.Usagef("ProcessDirect address must start with /")
		}
		if m.Harness == "" {
			m.Harness = DefaultHarnessID
		}
		endpointArtifact = m.Harness
		headers[HarnessAddressHeader] = m.ProcessDirectAddress
	}
	url, err := ResolveEndpoint(exe, endpointArtifact, m.URL)
	if err != nil && m.ProcessDirectAddress != "" && output.ExitCode(err) == exitcode.Usage {
		return nil, output.Usagef("test harness %s is not deployed or has no endpoint (see the cpi-test skill to set it up): %v", m.Harness, err)
	}
	if err != nil {
		return nil, err
	}
	rtExe, path, err := newExe(url)
	if err != nil {
		return nil, output.Usage(err)
	}

	sent := &SentMessage{ArtifactID: m.ArtifactID, URL: url, SentAt: time.Now().UTC().Truncate(time.Millisecond), TraceID: traceID}
	if m.ProcessDirectAddress != "" {
		sent.Harness, sent.ProcessDirectAddress = m.Harness, m.ProcessDirectAddress
	}
	resp, err := rtExe.Exec(method, path, bytes.NewReader(m.Body), headers)
	if err != nil {
		return nil, err
	}
	body, err := rtExe.ReadRespBody(resp)
	if err != nil {
		return nil, err
	}
	sent.HTTPStatus = resp.StatusCode
	sent.MessageGuid = resp.Header.Get("SAP_MessageProcessingLogID")
	sent.CorrelationID = resp.Header.Get("SAP_MplCorrelationId")
	sent.ResponseContentType = resp.Header.Get("Content-Type")
	sent.Response = NewContent(body, m.MaxBytes)

	authFailed := resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden
	if sent.Harness != "" {
		sent.HarnessMessageGuid, sent.MessageGuid = sent.MessageGuid, ""
	}
	if m.Wait > 0 && !authFailed {
		if sent.Harness != "" {
			sent.Log, err = waitForHarnessTarget(ctx, exe, sent, m.Wait, m.PollInterval)
		} else {
			sent.Log, err = waitForMessage(ctx, exe, sent, m.Wait, m.PollInterval)
		}
		if err != nil {
			return sent, err
		}
	}
	switch {
	case authFailed:
		return sent, &httpclnt.HTTPError{CallType: "Send test message (check the runtime credentials and the role ESBMessaging.send)", StatusCode: resp.StatusCode}
	case resp.StatusCode >= 300:
		return sent, output.Failed(fmt.Errorf("endpoint answered HTTP %d%s", resp.StatusCode, messageHint(sent)))
	case sent.Log != nil && sent.Log.Status != "COMPLETED":
		return sent, output.Failed(fmt.Errorf("message %s ended with status %s", sent.Log.MessageGuid, sent.Log.Status))
	}
	return sent, nil
}

func messageHint(s *SentMessage) string {
	guid := s.MessageGuid
	if guid == "" {
		guid = s.HarnessMessageGuid
	}
	if guid == "" {
		return ""
	}
	return fmt.Sprintf(" (message %s, see get_message_log / get_message_steps)", guid)
}

// waitForMessage waits until the message log of s is final. Without a message
// GUID (e.g. no processing log created synchronously) the newest log of the
// artifact since the send time is used.
func waitForMessage(ctx context.Context, exe *httpclnt.HTTPExecuter, s *SentMessage, timeout, interval time.Duration) (*MessageLogDetail, error) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	guid := s.MessageGuid
	if guid == "" {
		// Tolerate clock skew between this machine and the tenant
		q := MessageLogQuery{ArtifactID: s.ArtifactID, Since: s.SentAt.Add(-30 * time.Second), Top: 1}
		list, err := WaitForMessageLogs(ctx, exe, q, timeout, interval)
		if err != nil {
			return nil, err
		}
		guid = list.Logs[0].MessageGuid
		s.MessageGuid = guid
		return GetMessageLog(exe, guid, 0)
	}
	mpl := cpi.NewMessageLogs(exe)
	deadline := time.Now().Add(timeout)
	for {
		l, err := mpl.Get(guid)
		if err != nil && !isTransient(err) {
			return nil, err
		}
		if l != nil && slices.Contains(FinalMessageStatuses, l.Status) {
			return GetMessageLog(exe, guid, 0)
		}
		if time.Now().Add(interval).After(deadline) {
			return nil, &TimeoutError{Msg: fmt.Sprintf("message %s did not reach a final status within %s", guid, timeout)}
		}
		if err := sleep(ctx, interval); err != nil {
			return nil, err
		}
	}
}

// waitForHarnessTarget waits for the message that the harness passed on: the
// flow behind the ProcessDirect address shares the harness message's
// correlation ID.
func waitForHarnessTarget(ctx context.Context, exe *httpclnt.HTTPExecuter, s *SentMessage, timeout, interval time.Duration) (*MessageLogDetail, error) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	correlation := s.CorrelationID
	mpl := cpi.NewMessageLogs(exe)
	for correlation == "" && s.HarnessMessageGuid != "" {
		l, err := mpl.Get(s.HarnessMessageGuid)
		if err != nil && !isTransient(err) {
			return nil, err
		}
		if l != nil && l.CorrelationId != "" {
			correlation = l.CorrelationId
			break
		}
		if time.Now().Add(interval).After(deadline) {
			return nil, &TimeoutError{Msg: fmt.Sprintf("harness message %s has no processing log within %s", s.HarnessMessageGuid, timeout)}
		}
		if err := sleep(ctx, interval); err != nil {
			return nil, err
		}
	}
	if correlation == "" {
		return nil, output.Failed(fmt.Errorf("the harness returned no message GUID or correlation ID, cannot find the message of %s", s.ArtifactID))
	}
	s.CorrelationID = correlation
	q := MessageLogQuery{ArtifactID: s.ArtifactID, CorrelationID: correlation, Top: 1}
	list, err := WaitForMessageLogs(ctx, exe, q, time.Until(deadline), interval)
	if err != nil {
		return nil, err
	}
	s.MessageGuid = list.Logs[0].MessageGuid
	return GetMessageLog(exe, s.MessageGuid, 0)
}
