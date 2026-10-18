package ops

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/idoc"
	"github.com/cpars-innovation/cpicli/pkg/testtools"
	"github.com/cpars-innovation/cpicli/pkg/versioning"
)

// EndpointExecuterFunc returns an executer and request path for a runtime
// endpoint URL (see cpi.NewEndpointExecuter).
type EndpointExecuterFunc func(endpointURL string) (*httpclnt.HTTPExecuter, string, error)

// TestMessage is a message sent to a deployed integration flow.
type TestMessage struct {
	ArtifactID string
	// URL is one of the artifact's entry point URLs, or a URL below one
	// (a sub-path or a query, e.g. .../cxf/out/test for a sender that routes
	// on the path); empty: its only one.
	URL string
	// Path is appended to the entry point (e.g. "test" or "/orders?x=1"):
	// the same as URL below the endpoint, without writing the host.
	Path        string
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
	// Progress, when set, receives every poll while waiting (Wait).
	Progress ProgressFunc
	// IDoc is IDoc XML for the flow's IDoc sender (instead of Body): it is
	// POSTed to the IDoc endpoint (/cxf/...) with Content-Type
	// application/x-sap.idoc (unless ContentType is set) and must hold an
	// EDI_DC40 control record.
	IDoc []byte
	// OData sends an OData V2 request to the flow's OData sender endpoint
	// (/gw/odata/...); Method is then OData.Method.
	OData *ODataRequest
}

// ODataRequest is a request to an entity set of an OData V2 service.
type ODataRequest struct {
	// Method is GET (default), POST, PUT, PATCH or DELETE.
	Method string `json:"method,omitempty"`
	// EntitySet is the entity set (or $metadata); empty: the service document.
	EntitySet string `json:"entitySet,omitempty"`
	// Key is the entity key as OData writes it in the parentheses, e.g.
	// '4711' (string) or 4711 (number) or OrderNo='4711',Item=10.
	Key string `json:"key,omitempty"`
	// Query is the query string, e.g. $filter=Status eq 'OPEN'&$top=10.
	Query string `json:"query,omitempty"`
}

// odataMethods are the methods of ODataRequest.
var odataMethods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// resourcePath is the part of the request URL after the service root.
func (o *ODataRequest) resourcePath() (string, error) {
	set := strings.Trim(o.EntitySet, "/")
	if strings.ContainsAny(set, "?#() ") {
		return "", output.Usagef("invalid entity set %q (put the key in key and the query in query)", o.EntitySet)
	}
	p := ""
	if set != "" {
		p = "/" + set
	}
	if o.Key != "" {
		if set == "" {
			return "", output.Usagef("a key needs an entity set")
		}
		p += "(" + strings.TrimSuffix(strings.TrimPrefix(o.Key, "("), ")") + ")"
	}
	if q := strings.TrimPrefix(o.Query, "?"); q != "" {
		values, err := neturl.ParseQuery(q)
		if err != nil {
			return "", output.Usagef("invalid OData query %q: %v", o.Query, err)
		}
		p += "?" + strings.NewReplacer("%24", "$", "+", "%20").Replace(values.Encode())
	}
	return p, nil
}

// DefaultHarnessID is the test harness flow: an HTTPS sender that forwards
// the message to the ProcessDirect address in header HarnessAddressHeader.
// (`cpictl test tools install` installs it.)
const DefaultHarnessID = testtools.HarnessID

// HarnessAddressHeader carries the ProcessDirect target address to the harness.
const HarnessAddressHeader = testtools.HarnessAddressHeader

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
	// ResponseHeaders are the headers of the answer (cookies left out),
	// several values joined with ", ".
	ResponseHeaders map[string]string `json:"responseHeaders,omitempty"`
	Response        Content           `json:"response"`
	// Log is the processing log of the message (only with Wait).
	Log *MessageLogDetail `json:"log,omitempty"`
	// IDocs are the control records of the IDocs sent (TestMessage.IDoc).
	IDocs []idoc.Control `json:"idocs,omitempty"`
}

// headers that a test message may not set: authentication and session
// handling belong to the executer.
var reservedHeaders = []string{"authorization", "cookie", "x-csrf-token", "host", "content-length"}

// ResolveEndpoint returns the entry point URL to send to: url itself when it
// is one of the artifact's entry points, or the artifact's only entry point.
// Only URLs the tenant lists for the artifact are accepted.
func ResolveEndpoint(exe *httpclnt.HTTPExecuter, artifactID, url string) (string, error) {
	return resolveEndpoint(exe, artifactID, url, "")
}

// Endpoint kinds for resolveEndpoint.
const (
	endpointIDoc  = "IDoc"  // /cxf/ (IDoc and SOAP senders)
	endpointOData = "OData" // OData services (/gw/odata/ or Protocol ODATA)
)

// endpointIs reports whether an entry point is of kind ("": any). IDoc
// senders are reached under /cxf/ like SOAP senders; OData services under
// /gw/odata/ or with Protocol ODATA.
func endpointIs(kind, protocol, url string) bool {
	switch kind {
	case endpointIDoc:
		return strings.Contains(url, "/cxf/")
	case endpointOData:
		return strings.EqualFold(protocol, "ODATA") || strings.Contains(url, "/gw/odata/")
	}
	return true
}

// belowEndpoint reports whether url is ep with a sub-path and/or a query:
// same scheme and host, path ep's path plus "/..." (whole segments only, no
// "..") — the same sender, so the same flow.
func belowEndpoint(url, ep string) bool {
	u, err1 := neturl.Parse(url)
	e, err2 := neturl.Parse(ep)
	if err1 != nil || err2 != nil || u.Scheme != e.Scheme || u.Host != e.Host || u.User != nil || u.Fragment != "" {
		return false
	}
	if strings.Contains(u.Path, "..") || strings.Contains(u.Path, "//") {
		return false
	}
	base := strings.TrimSuffix(e.Path, "/")
	return u.Path == e.Path || u.Path == base || strings.HasPrefix(u.Path, base+"/")
}

// withPath appends a sub-path (and query) to an entry point URL.
func withPath(ep, sub string) (string, error) {
	if sub == "" {
		return ep, nil
	}
	if strings.Contains(sub, "://") || strings.Contains(sub, "..") || strings.HasPrefix(sub, "//") {
		return "", output.Usagef("path %q: a path below the endpoint (e.g. test or /orders?x=1)", sub)
	}
	path, query, _ := strings.Cut(sub, "?")
	u := strings.TrimSuffix(ep, "/")
	if path = strings.Trim(path, "/"); path != "" {
		u += "/" + path
	}
	if query != "" {
		u += "?" + query
	}
	return u, nil
}

func resolveEndpoint(exe *httpclnt.HTTPExecuter, artifactID, url, kind string) (string, error) {
	eps, err := cpi.NewContent(exe).ServiceEndpoints(artifactID)
	if err != nil {
		return "", err
	}
	var urls, others []string
	for _, ep := range eps {
		if ep.ArtifactID != artifactID {
			continue
		}
		for _, e := range ep.EntryPoints {
			if endpointIs(kind, ep.Protocol, e.URL) {
				urls = append(urls, e.URL)
			} else {
				others = append(others, e.URL)
			}
		}
	}
	switch {
	case len(urls) == 0 && len(others) > 0:
		return "", output.Usagef("integration flow %s has no %s endpoint (endpoints: %s)", artifactID, kind, strings.Join(others, ", "))
	case len(urls) == 0:
		return "", &noEndpointError{output.Usagef("integration flow %s has no endpoint: is it deployed and does it have an HTTP-based sender (HTTPS, SOAP, IDoc, OData, ...)?", artifactID)}
	case url != "":
		if slices.Contains(urls, url) || slices.ContainsFunc(urls, func(ep string) bool { return belowEndpoint(url, ep) }) {
			return url, nil
		}
		return "", output.Usagef("%s is not an endpoint of %s and not below one (endpoints: %s)", url, artifactID, strings.Join(urls, ", "))
	case len(urls) > 1:
		return "", output.Usagef("%s has %d endpoints, choose one with url: %s", artifactID, len(urls), strings.Join(urls, ", "))
	}
	return urls[0], nil
}

// reFailedMPLID finds the message ID in the answer of a failed message
// ("An internal server error occured: The MPL ID for the failed message is : AGr...").
var reFailedMPLID = regexp.MustCompile(`The MPL ID for the failed message is : ([A-Za-z0-9_-]+)`)

// harnessRunsCall is the first harness version whose process runs on a
// tenant (older models lacked the sequence flow references SAP's runtime
// needs, so the harness answered without calling the address).
const harnessRunsCall = "1.2.0"

// noEndpointError: the tenant lists no endpoint at all for the flow.
type noEndpointError struct{ error }

func (e *noEndpointError) Unwrap() error { return e.error }

// How long a send waits for the endpoint of a started flow: the tenant
// lists it about a minute after the deployment reports STARTED.
var (
	endpointWait = 2 * time.Minute
	endpointPoll = 10 * time.Second
)

// awaitEndpoint is resolveEndpoint that, when a STARTED flow has no endpoint
// yet (just deployed), asks again until endpointWait has passed.
func awaitEndpoint(ctx context.Context, exe *httpclnt.HTTPExecuter, artifactID, url, kind string, prog *progress) (string, error) {
	deadline := time.Now().Add(endpointWait)
	for {
		ep, err := resolveEndpoint(exe, artifactID, url, kind)
		var none *noEndpointError
		if !errors.As(err, &none) || time.Now().After(deadline) {
			return ep, err
		}
		if _, status, serr := cpi.NewRuntime(exe).Get(artifactID); serr != nil || status != "STARTED" {
			return ep, err
		}
		prog.report(0, "%s is started, waiting for the tenant to list its endpoint", artifactID)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(endpointPoll):
		}
	}
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
	kind, resource, idocs, err := prepareSAPMessage(&m)
	if err != nil {
		return nil, err
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
	if m.OData != nil && !hasHeader(headers, "Accept") {
		headers["Accept"] = "application/json"
		if strings.Trim(m.OData.EntitySet, "/") == "$metadata" {
			headers["Accept"] = "application/xml"
		}
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
	prog := newProgress(m.Progress)
	url, err := awaitEndpoint(ctx, exe, endpointArtifact, m.URL, kind, prog)
	if err != nil && m.ProcessDirectAddress != "" && output.ExitCode(err) == exitcode.Usage {
		return nil, output.Usagef("test harness %s is not deployed or has no endpoint (install it with 'cpictl test tools install --deploy', MCP install_test_tools): %v", m.Harness, err)
	}
	if err != nil {
		return nil, err
	}
	if kind == endpointOData { // the entry point is the service root
		url = strings.TrimSuffix(strings.TrimSuffix(url, "/$metadata"), "/") + resource
	}
	if m.Path != "" {
		if kind != "" || m.ProcessDirectAddress != "" {
			return nil, output.Usagef("path is for HTTP-like senders (not with idoc, OData or process_direct_address)")
		}
		if url, err = withPath(url, m.Path); err != nil {
			return nil, err
		}
	}
	rtExe, path, err := newExe(url)
	if err != nil {
		return nil, output.Usage(err)
	}

	sent := &SentMessage{ArtifactID: m.ArtifactID, URL: url, SentAt: time.Now().UTC().Truncate(time.Millisecond), TraceID: traceID, IDocs: idocs}
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
	if sent.MessageGuid == "" && resp.StatusCode >= 300 {
		// a failed message's answer has no ID headers, only this text
		if mm := reFailedMPLID.FindSubmatch(body); mm != nil {
			sent.MessageGuid = string(mm[1])
		}
	}
	sent.CorrelationID = resp.Header.Get("SAP_MplCorrelationId")
	sent.ResponseContentType = resp.Header.Get("Content-Type")
	sent.ResponseHeaders = map[string]string{}
	for k, v := range resp.Header {
		if !strings.EqualFold(k, "Set-Cookie") {
			sent.ResponseHeaders[k] = strings.Join(v, ", ")
		}
	}
	sent.Response = NewContent(body, m.MaxBytes)

	authFailed := resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden
	if sent.Harness != "" {
		sent.HarnessMessageGuid, sent.MessageGuid = sent.MessageGuid, ""
	}
	if m.Wait > 0 && !authFailed {
		prog.report(0, "endpoint answered HTTP %d, waiting for the message processing log", resp.StatusCode)
		if sent.Harness != "" {
			sent.Log, err = waitForHarnessTarget(ctx, exe, sent, m.Wait, m.PollInterval, prog)
		} else {
			sent.Log, err = waitForMessage(ctx, exe, sent, m.Wait, m.PollInterval, prog)
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

// prepareSAPMessage checks and completes an IDoc or OData test message:
// body, method and content type, the endpoint kind to resolve, the OData
// resource path and the IDocs' control records.
func prepareSAPMessage(m *TestMessage) (kind, resource string, idocs []idoc.Control, err error) {
	switch {
	case m.IDoc != nil && m.OData != nil:
		return "", "", nil, output.Usagef("send either an IDoc or an OData request")
	case (m.IDoc != nil || m.OData != nil) && m.ProcessDirectAddress != "":
		return "", "", nil, output.Usagef("IDoc and OData requests go to the flow's own endpoint, not through the test harness")
	case m.IDoc != nil:
		if len(m.Body) > 0 {
			return "", "", nil, output.Usagef("send either a body or an IDoc")
		}
		if m.Method != "" && !strings.EqualFold(m.Method, http.MethodPost) {
			return "", "", nil, output.Usagef("an IDoc is sent with POST, not %s", m.Method)
		}
		doc, err := idoc.Parse(m.IDoc)
		if err != nil {
			return "", "", nil, output.Usagef("invalid IDoc: %v", err)
		}
		m.Body, m.Method = m.IDoc, http.MethodPost
		m.ContentType = cmp.Or(m.ContentType, idoc.ContentType)
		return endpointIDoc, "", doc.IDocs, nil
	case m.OData != nil:
		method := strings.ToUpper(cmp.Or(m.OData.Method, http.MethodGet))
		if !slices.Contains(odataMethods, method) {
			return "", "", nil, output.Usagef("invalid OData method %q (%s)", m.OData.Method, strings.Join(odataMethods, ", "))
		}
		if len(m.Body) > 0 && (method == http.MethodGet || method == http.MethodDelete) {
			return "", "", nil, output.Usagef("an OData %s has no body", method)
		}
		resource, err := m.OData.resourcePath()
		if err != nil {
			return "", "", nil, err
		}
		m.Method = method
		if len(m.Body) > 0 {
			m.ContentType = cmp.Or(m.ContentType, "application/json")
		}
		return endpointOData, resource, nil, nil
	}
	return "", "", nil, nil
}

func hasHeader(headers map[string]string, name string) bool {
	for k := range headers {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

func messageHint(s *SentMessage) string {
	guid := s.MessageGuid
	if guid == "" {
		guid = s.HarnessMessageGuid
	}
	if guid == "" {
		return ""
	}
	return fmt.Sprintf(" (message %s, see get_message_log with include_steps)", guid)
}

// waitForMessage waits until the message log of s is final. Without a message
// GUID (e.g. no processing log created synchronously) the newest log of the
// artifact since the send time is used.
func waitForMessage(ctx context.Context, exe *httpclnt.HTTPExecuter, s *SentMessage, timeout, interval time.Duration, prog *progress) (*MessageLogDetail, error) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	guid := s.MessageGuid
	if guid == "" {
		// Tolerate clock skew between this machine and the tenant
		q := MessageLogQuery{ArtifactID: s.ArtifactID, Since: s.SentAt.Add(-30 * time.Second), Top: 1}
		list, err := waitForMessageLogs(ctx, exe, q, timeout, interval, prog)
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
		state := "no processing log yet"
		if l != nil {
			state = l.Status
		}
		prog.report(0, "message %s: %s (poll %d)", guid, state, prog.steps()+1)
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
func waitForHarnessTarget(ctx context.Context, exe *httpclnt.HTTPExecuter, s *SentMessage, timeout, interval time.Duration, prog *progress) (*MessageLogDetail, error) {
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
		prog.report(0, "harness message %s: waiting for its correlation ID (poll %d)", s.HarnessMessageGuid, prog.steps()+1)
		if err := sleep(ctx, interval); err != nil {
			return nil, err
		}
	}
	// no IDs in the harness's answer (#20): find the harness's own log of
	// this send by time
	for correlation == "" && s.Harness != "" {
		list, err := QueryMessageLogs(exe, MessageLogQuery{ArtifactID: s.Harness, Since: s.SentAt.Add(-2 * time.Second), Top: 1})
		if err != nil && !isTransient(err) {
			return nil, err
		}
		if err == nil && len(list.Logs) > 0 && list.Logs[0].CorrelationID != "" {
			s.HarnessMessageGuid, correlation = list.Logs[0].MessageGuid, list.Logs[0].CorrelationID
			break
		}
		if time.Now().Add(interval).After(deadline) {
			break
		}
		prog.report(0, "the harness answered without message IDs: looking for its log (poll %d)", prog.steps()+1)
		if err := sleep(ctx, interval); err != nil {
			return nil, err
		}
	}
	if correlation == "" {
		return nil, output.Failed(fmt.Errorf("the harness returned no message GUID or correlation ID and has no log since %s, cannot find the message of %s",
			s.SentAt.Format(time.RFC3339), s.ArtifactID))
	}
	s.CorrelationID = correlation
	// the harness failed because no flow consumes the address: the flow
	// never ran, there is no log of it to wait for
	if s.HTTPStatus >= 300 && s.HarnessMessageGuid != "" {
		if h, err := GetMessageLog(exe, s.HarnessMessageGuid, 0); err == nil && h != nil && h.Status == "FAILED" &&
			strings.Contains(strings.ToLower(h.ErrorText+" "+s.Response.Text), "no consumer") {
			return h, output.Failed(fmt.Errorf("the test harness could not reach %s: no deployed flow consumes ProcessDirect address %s (harness message %s: %s)",
				s.ArtifactID, s.ProcessDirectAddress, s.HarnessMessageGuid, firstLine(h.ErrorText)))
		}
	}
	q := MessageLogQuery{ArtifactID: s.ArtifactID, CorrelationID: correlation, Top: 1}
	list, err := waitForMessageLogs(ctx, exe, q, time.Until(deadline), interval, prog)
	var timedOut *TimeoutError
	if errors.As(err, &timedOut) && s.HarnessMessageGuid != "" {
		// the harness ran but the flow under test never did: say where it stopped
		if h, herr := GetMessageLog(exe, s.HarnessMessageGuid, 0); herr == nil && h != nil && h.Status == "COMPLETED" {
			msg := fmt.Sprintf("the test harness completed (message %s) but %s has no message with its correlation ID %s: the harness did not reach ProcessDirect address %s. ",
				s.HarnessMessageGuid, s.ArtifactID, correlation, s.ProcessDirectAddress)
			if v, _, verr := cpi.NewRuntime(exe).Get(s.Harness); verr == nil && v != "" && v != "NOT_DEPLOYED" && versioning.Compare(v, harnessRunsCall) < 0 {
				msg += fmt.Sprintf("The deployed harness %s is version %s: harness versions before %s skip their process and return the body unchanged. "+
					"Run cpictl test tools install --deploy", s.Harness, v, harnessRunsCall)
			} else {
				msg += fmt.Sprintf("Check that %s is deployed and STARTED on the same runtime as the harness", s.ArtifactID)
			}
			return h, output.Failed(errors.New(msg))
		}
	}
	if err != nil {
		return nil, err
	}
	s.MessageGuid = list.Logs[0].MessageGuid
	return GetMessageLog(exe, s.MessageGuid, 0)
}

// firstLine is the first line of a text.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}
