package cpitest

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/iflow"
	"github.com/cpars-innovation/cpicli/pkg/testtools"
)

// The receiver stub (cpictl test tools install, testtools.StubID) runs a
// Groovy script the mock does not execute. The mock emulates it: a deployed
// flow whose script writes the attachment testtools.StubAttachment is a stub.
// A request to its endpoint (<mock>/http/cpictl/stub/<rule>/...), sent
// directly or by a receiver of another flow whose address is a URL of the
// mock itself, gets the answer configured for the rule in the Partner
// Directory (PID testtools.StubPID, parameter ID = rule; none: 200 {}) and
// produces a COMPLETED message log with the recorded request as attachment
// and the custom header properties CpictlStubRule and CpictlStubPath, as the
// script does on a tenant.

// stubRequest is a request the stub received.
type stubRequest struct {
	Method, Path, Query string
	Headers             map[string]string
	Body                string
}

// stubAnswer is what the stub answers.
type stubAnswer struct {
	Status      int
	ContentType string
	Body        string
	Headers     map[string]string
	// Delay is the configured delay; Matched the chosen answer (the "when"
	// entry's name or when[<i>], default, "" without configuration).
	Delay   time.Duration
	Matched string
}

// stubConfig is a rule's configuration (ops.StubResponse).
type stubConfig struct {
	Name         string            `json:"name"`
	PathContains string            `json:"pathContains"`
	Header       map[string]string `json:"header"`
	BodyContains string            `json:"bodyContains"`
	Status       int               `json:"status"`
	ContentType  string            `json:"contentType"`
	Body         *string           `json:"body"`
	Headers      map[string]any    `json:"headers"`
	DelayMs      *int              `json:"delayMs"`
	When         []stubConfig      `json:"when"`
}

// matches is the script's matches: every condition set holds, at least one.
func (w stubConfig) matches(req stubRequest, path string) bool {
	if w.PathContains == "" && len(w.Header) == 0 && w.BodyContains == "" {
		return false
	}
	if w.PathContains != "" && !strings.Contains(path, w.PathContains) {
		return false
	}
	for name, want := range w.Header {
		found := false
		for k, v := range req.Headers {
			if strings.EqualFold(k, name) {
				found = v == want
				break
			}
		}
		if !found {
			return false
		}
	}
	return w.BodyContains == "" || strings.Contains(req.Body, w.BodyContains)
}

// stubAnswerFor is the configured answer of a rule for a request (the
// script's lookup and choice of a "when" entry).
func (m *Tenant) stubAnswerFor(rule, path string, req stubRequest) stubAnswer {
	ans := stubAnswer{Status: http.StatusOK, ContentType: "application/json", Body: "{}"}
	value, ok := m.PDStrings[testtools.StubPID+"/"+rule]
	if rule == "" || !ok {
		return ans
	}
	var r stubConfig
	if err := json.Unmarshal([]byte(value), &r); err != nil {
		return stubAnswer{Status: http.StatusInternalServerError, ContentType: "text/plain",
			Body: "cpictl stub: the Partner Directory value of rule " + rule + " is not valid JSON: " + err.Error()}
	}
	chosen, matched := stubConfig{}, "default"
	for i, w := range r.When {
		if w.matches(req, path) {
			chosen, matched = w, cmp.Or(w.Name, fmt.Sprintf("when[%d]", i))
			break
		}
	}
	ans.Matched = matched
	ans.Status = cmp.Or(chosen.Status, r.Status, http.StatusOK)
	ans.ContentType = cmp.Or(chosen.ContentType, r.ContentType, ans.ContentType)
	body := cmp.Or(chosen.Body, r.Body)
	ans.Body = ""
	if body != nil {
		ans.Body = *body
	}
	headers := r.Headers
	if chosen.Headers != nil {
		headers = chosen.Headers
	}
	for k, v := range headers {
		if ans.Headers == nil {
			ans.Headers = map[string]string{}
		}
		ans.Headers[k] = fmt.Sprint(v)
	}
	delay := r.DelayMs
	if chosen.DelayMs != nil {
		delay = chosen.DelayMs
	}
	if delay != nil {
		ans.Delay = time.Duration(min(max(*delay, 0), 60000)) * time.Millisecond
	}
	return ans
}

var reStubSkipHeader = regexp.MustCompile(`^(Camel|SAP_)|^(?i:sapauthenticatedusername)$`)

// recordedHeaders are the headers the stub script records: no Camel or SAP
// internal headers and no sensitive names.
func recordedHeaders(h map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		if reStubSkipHeader.MatchString(k) || iflow.SensitiveName(k) {
			continue
		}
		if len(v) > 1024 {
			v = v[:1024]
		}
		out[k] = v
	}
	return out
}

// runStub runs the stub flow id for one request: it logs the run (with the
// recorded request) and returns the answer.
func (x *executor) runStub(id string, req stubRequest, correlation, predecessor string, start time.Time) (runResult, stubAnswer) {
	a := x.m.Artifacts[id]
	fi := a.info()
	path := strings.TrimLeft(req.Path, "/")
	rule, _, _ := strings.Cut(path, "/")
	body, truncated := req.Body, false
	if len(body) > testtools.StubMaxBody {
		body, truncated = body[:testtools.StubMaxBody], true
	}
	ans := x.m.stubAnswerFor(rule, path, req)
	record, _ := json.Marshal(map[string]any{"time": start.UTC().Format(time.RFC3339Nano), "rule": rule, "method": req.Method,
		"path": path, "query": req.Query, "headers": recordedHeaders(req.Headers), "body": body, "bodyTruncated": truncated,
		"matched": ans.Matched})
	l := MessageLog{Guid: x.guid(), Artifact: id, Package: a.Package, Status: "COMPLETED", CorrelationID: correlation,
		Predecessor: predecessor, Start: start, Attachments: map[string]string{testtools.StubAttachment: string(record)}}
	for k, v := range map[string]string{testtools.StubRuleHeader: rule, testtools.StubPathHeader: path} {
		if v != "" {
			if l.Headers == nil {
				l.Headers = map[string]string{}
			}
			l.Headers[k] = v
		}
	}
	// the delay is processing time of the message (the mock does not sleep)
	t := start.Add(time.Duration(5+x.rng.IntN(10))*time.Millisecond + ans.Delay)
	for i, el := range fi.steps {
		t = t.Add(time.Duration(5+x.rng.IntN(20)) * time.Millisecond)
		l.Steps = append(l.Steps, Step{StepID: fmt.Sprintf("%s-%d", l.Guid, i+1), ModelStepID: el.ID, Activity: stepName(el), Status: "COMPLETED"})
	}
	l.End = t
	x.logs = append(x.logs, l)
	return runResult{guid: l.Guid, status: l.Status, end: t}, ans
}

// handleStub answers a request to a deployed stub's endpoint (Live mode).
func (m *Tenant) handleStub(w http.ResponseWriter, r *http.Request, id, rest string, body []byte) {
	m.Received = append(m.Received, ReceivedMessage{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: string(body)})
	m.liveSerial++
	correlation := cmp.Or(r.Header.Get("SAP_MplCorrelationId"), fmt.Sprintf("C-live-%d", m.liveSerial))
	x := m.newExecutor("LIVE")
	res, ans := x.runStub(id, stubRequest{Method: r.Method, Path: rest, Query: r.URL.RawQuery, Headers: firstValues(r.Header), Body: string(body)},
		correlation, "", time.Now())
	m.addLogs(x.logs)
	w.Header().Set("SAP_MessageProcessingLogID", res.guid)
	w.Header().Set("SAP_MplCorrelationId", correlation)
	writeStubAnswer(w, ans)
}

func writeStubAnswer(w http.ResponseWriter, ans stubAnswer) {
	for k, v := range ans.Headers {
		w.Header().Set(k, v)
	}
	w.Header().Set("Content-Type", ans.ContentType)
	w.WriteHeader(ans.Status)
	_, _ = w.Write([]byte(ans.Body))
}

// firstValues flattens request headers (first value of each).
func firstValues(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

// isStub reports whether a deployed artifact is a receiver stub the mock
// emulates.
func (a *Artifact) isStub() bool {
	fi := a.info()
	return a.running() && fi != nil && fi.stub
}

// selfURL returns the parsed address when it is a runtime URL (/http/...)
// of the mock itself: its URL, EndpointBase, or a loopback name with its port.
func (m *Tenant) selfURL(address string) (*url.URL, bool) {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || !strings.HasPrefix(u.Path, "/http/") || m.server == nil {
		return nil, false
	}
	for _, base := range []string{m.URL(), m.EndpointBase} {
		b, err := url.Parse(base)
		if err != nil || b.Host == "" {
			continue
		}
		if strings.EqualFold(b.Host, u.Host) {
			return u, true
		}
		_, port, _ := net.SplitHostPort(b.Host)
		if port != "" && u.Port() == port && isLoopback(u.Hostname()) && isLoopbackOrAny(b.Hostname()) {
			return u, true
		}
	}
	return nil, false
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isLoopbackOrAny(host string) bool {
	ip := net.ParseIP(host)
	return isLoopback(host) || (ip != nil && ip.IsUnspecified())
}

// callSelf is a receiver call to a runtime endpoint of the mock itself: the
// stub answers as configured (a status >= 300 fails the call like an HTTP
// receiver does), another deployed flow runs with the same correlation ID.
func (x *executor) callSelf(ch channel, msg message, u *url.URL, t time.Time, depth int) (time.Time, string) {
	in, rest := x.m.inboundFor(u.Path)
	var a *Artifact
	if in != nil {
		a = x.m.Artifacts[in.Artifact]
	}
	if a == nil || !a.running() || a.info() == nil || depth > 20 {
		return t.Add(20 * time.Millisecond), fmt.Sprintf("com.sap.it.rt.adapter.http.api.exception.HttpResponseException: An internal server error occured: HTTP 404 from %s", u.Host)
	}
	if !a.isStub() {
		r := x.run(in.Artifact, msg, t.Add(3*time.Millisecond), "", depth+1)
		if r.status != "COMPLETED" {
			return r.end, fmt.Sprintf("com.sap.it.rt.adapter.http.api.exception.HttpResponseException: An internal server error occured: HTTP 500 from %s: %s", u.Host, r.errorText)
		}
		return r.end.Add(2 * time.Millisecond), ""
	}
	method := strings.ToUpper(ch.Props["httpMethod"])
	if method == "" || strings.Contains(method, "$") {
		method = http.MethodPost
	}
	r, ans := x.runStub(in.Artifact, stubRequest{Method: method, Path: rest, Query: u.RawQuery, Body: msg.payload}, msg.correlation, "", t.Add(3*time.Millisecond))
	t = r.end.Add(2 * time.Millisecond)
	if ans.Status >= 300 {
		body := ans.Body
		if len(body) > 200 {
			body = body[:200]
		}
		return t, fmt.Sprintf("com.sap.it.rt.adapter.http.api.exception.HttpResponseException: An internal server error occured: HTTP %d from %s: %s", ans.Status, u.Host, body)
	}
	return t, ""
}

var reHeaderRef = regexp.MustCompile(`\$\{header\.([^}]+)\}`)

// resolveHeaders replaces ${header.<name>} with the message's header (as
// the runtime resolves dynamic addresses, e.g. the test harness's
// ProcessDirect address ${header.CpictlTargetAddress}); names match without
// regard to case, like HTTP header names.
func resolveHeaders(s string, headers map[string]string) string {
	if !strings.Contains(s, "${header.") {
		return s
	}
	return reHeaderRef.ReplaceAllStringFunc(s, func(ref string) string {
		name := reHeaderRef.FindStringSubmatch(ref)[1]
		for k, v := range headers {
			if strings.EqualFold(k, name) {
				return v
			}
		}
		return ref
	})
}
