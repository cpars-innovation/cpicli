package testrun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/beevik/etree"
	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// Failure is an expectation that did not hold.
type Failure struct {
	// Expectation is the key of expect, e.g. http_status or
	// response_xpath[/Order/Status].
	Expectation string `json:"expectation"`
	Expected    string `json:"expected"`
	Actual      string `json:"actual"`
	Message     string `json:"message"`
}

// observation is what a case observed: the send result and what was read
// after it.
type observation struct {
	sent     *ops.SentMessage
	sendErr  error         // a failed or timed out message (not an error of the case)
	elapsed  time.Duration // the HTTP call
	steps    *ops.MessageSteps
	stepsErr error
	stubs    []ops.StubCall
	stubsErr error
}

// maxActual caps texts quoted in failures.
const maxActual = 300

func excerpt(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxActual {
		return s[:maxActual] + "…"
	}
	return s
}

func (o *observation) response() string {
	if o.sent == nil {
		return ""
	}
	return o.sent.Response.Text
}

// errorText is the text error_contains looks in: the log's error text, the
// response of a failed call and the send error.
func (o *observation) errorText() string {
	var parts []string
	if o.sent != nil && o.sent.Log != nil && o.sent.Log.ErrorText != "" {
		parts = append(parts, o.sent.Log.ErrorText)
	}
	if o.sent != nil && o.sent.HTTPStatus >= 300 {
		parts = append(parts, o.sent.Response.Text)
	}
	if o.sendErr != nil {
		parts = append(parts, o.sendErr.Error())
	}
	return strings.Join(parts, "\n")
}

// check evaluates every expectation of e against o.
func check(e Expect, o *observation) []Failure {
	var fs []Failure
	add := func(exp, expected, actual, format string, args ...any) {
		fs = append(fs, Failure{Expectation: exp, Expected: expected, Actual: actual, Message: fmt.Sprintf(format, args...)})
	}
	status := o.sent.HTTPStatus
	switch {
	case e.HTTPStatus != 0 && status != e.HTTPStatus:
		add("http_status", strconv.Itoa(e.HTTPStatus), strconv.Itoa(status), "HTTP status %d, expected %d", status, e.HTTPStatus)
	// a case that expects the message to fail does not expect a 2xx answer
	case e.HTTPStatus == 0 && len(e.ErrorContains) == 0 && !expectsFailure(e.MessageStatus) && (status < 200 || status > 299):
		add("http_status", "2xx", strconv.Itoa(status), "HTTP status %d, expected 2xx: %s", status, excerpt(o.response()))
	}
	if e.MessageStatus != "" {
		actual := "no processing log"
		if o.sent.Log != nil {
			actual = o.sent.Log.Status
		} else if o.sendErr != nil {
			actual = o.sendErr.Error()
		}
		if !strings.EqualFold(actual, e.MessageStatus) {
			msg := fmt.Sprintf("message status %s, expected %s", actual, e.MessageStatus)
			if o.sent.Log != nil && o.sent.Log.ErrorText != "" {
				msg += ": " + excerpt(o.sent.Log.ErrorText)
			}
			add("message_status", e.MessageStatus, actual, "%s", msg)
		}
	}
	resp := o.response()
	for _, s := range e.ResponseContains {
		if !strings.Contains(resp, s) {
			add("response_contains", s, excerpt(resp), "the response does not contain %q", s)
		}
	}
	for _, s := range e.ResponseNotContains {
		if strings.Contains(resp, s) {
			add("response_not_contains", s, excerpt(resp), "the response contains %q", s)
		}
	}
	for _, p := range sortedKeys(e.ResponseXPath) {
		want := e.ResponseXPath[p]
		got, found, err := XPath([]byte(resp), p)
		exp := "response_xpath[" + p + "]"
		switch {
		case err != nil:
			add(exp, want, "", "%s: %v", p, err)
		case !found:
			add(exp, want, "", "%s: not found in the response", p)
		case got != want:
			add(exp, want, got, "%s is %q, expected %q", p, got, want)
		}
	}
	for _, p := range sortedKeys(e.ResponseJSON) {
		want := e.ResponseJSON[p]
		got, found, err := JSONPath([]byte(resp), p)
		exp := "response_json[" + p + "]"
		switch {
		case err != nil:
			add(exp, scalar(want), "", "%s: %v", p, err)
		case !found:
			add(exp, scalar(want), "", "%s: not found in the response", p)
		case !sameValue(got, want):
			add(exp, scalar(want), scalar(got), "%s is %s, expected %s", p, scalar(got), scalar(want))
		}
	}
	if len(e.ErrorContains) > 0 {
		text := o.errorText()
		for _, s := range e.ErrorContains {
			if !strings.Contains(text, s) {
				actual := excerpt(text)
				if actual == "" {
					actual = "no error"
				}
				add("error_contains", s, actual, "the error does not contain %q", s)
			}
		}
	}
	if len(e.CustomHeaders) > 0 {
		got := map[string]string{}
		if o.sent.Log != nil {
			for _, h := range o.sent.Log.CustomHeaderProperties {
				got[h.Name] = h.Value
			}
		}
		for _, k := range sortedKeys(e.CustomHeaders) {
			want := e.CustomHeaders[k]
			v, ok := got[k]
			switch {
			case !ok:
				add("custom_headers["+k+"]", want, "", "custom header %s is not in the message log", k)
			case v != want:
				add("custom_headers["+k+"]", want, v, "custom header %s is %q, expected %q", k, v, want)
			}
		}
	}
	if len(e.StepsInclude) > 0 {
		switch {
		case o.stepsErr != nil:
			add("steps_include", strings.Join(e.StepsInclude, ", "), "", "the steps could not be read: %v", o.stepsErr)
		default:
			var seen []string
			if o.steps != nil {
				for _, r := range o.steps.Runs {
					for _, s := range r.Steps {
						seen = append(seen, s.ModelStepID, s.Activity)
					}
				}
			}
			for _, want := range e.StepsInclude {
				if !slices.Contains(seen, want) {
					add("steps_include", want, "", "step %s is not in the message's steps", want)
				}
			}
		}
	}
	if e.MaxDuration > 0 {
		took, what := o.elapsed, "the HTTP call"
		if l := o.sent.Log; l != nil && l.LogStart != nil && l.LogEnd != nil {
			took, what = l.LogEnd.Sub(*l.LogStart), "the message"
		}
		if took > time.Duration(e.MaxDuration) {
			add("max_duration", time.Duration(e.MaxDuration).String(), took.Round(time.Millisecond).String(),
				"%s took %s, more than %s", what, took.Round(time.Millisecond), time.Duration(e.MaxDuration))
		}
	}
	if len(e.StubCalls) > 0 {
		fs = append(fs, checkStubs(e.StubCalls, o.stubs, o.stubsErr)...)
	}
	return fs
}

// checkStubs compares the calls the stub recorded with the matchers.
func checkStubs(matchers []StubMatcher, calls []ops.StubCall, err error) []Failure {
	if err != nil {
		return []Failure{{Expectation: "stub_calls", Message: fmt.Sprintf("the stub calls could not be read: %v", err)}}
	}
	var fs []Failure
	for i, m := range matchers {
		n := 0
		for _, c := range calls {
			if m.matches(c) {
				n++
			}
		}
		if n != m.count() {
			fs = append(fs, Failure{Expectation: fmt.Sprintf("stub_calls[%d]", i), Expected: fmt.Sprintf("%d call(s) %s", m.count(), m),
				Actual:  fmt.Sprintf("%d of %d recorded call(s)", n, len(calls)),
				Message: fmt.Sprintf("%d stub call(s) %s, expected %d", n, m, m.count())})
		}
	}
	return fs
}

// matches reports whether a recorded call matches every field that is set.
func (m StubMatcher) matches(c ops.StubCall) bool {
	if m.Rule != "" && m.Rule != c.Rule {
		return false
	}
	if m.Method != "" && !strings.EqualFold(m.Method, c.Method) {
		return false
	}
	if m.PathContains != "" && !strings.Contains(c.Path, m.PathContains) {
		return false
	}
	for _, s := range m.BodyContains {
		if !strings.Contains(c.Body, s) {
			return false
		}
	}
	for k, v := range m.Header {
		found := false
		for hk, hv := range c.Headers {
			if strings.EqualFold(hk, k) && hv == v {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// String describes the matcher (rule, method, path, body, header).
func (m StubMatcher) String() string {
	var parts []string
	if m.Rule != "" {
		parts = append(parts, "rule "+m.Rule)
	}
	if m.Method != "" {
		parts = append(parts, "method "+m.Method)
	}
	if m.PathContains != "" {
		parts = append(parts, fmt.Sprintf("path containing %q", m.PathContains))
	}
	for _, s := range m.BodyContains {
		parts = append(parts, fmt.Sprintf("body containing %q", s))
	}
	for _, k := range sortedKeys(m.Header) {
		parts = append(parts, fmt.Sprintf("header %s=%q", k, m.Header[k]))
	}
	if len(parts) == 0 {
		return "(any)"
	}
	return "with " + strings.Join(parts, ", ")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// XPath evaluates a path on an XML document and returns the text of the
// first match (trimmed). Supported is the subset of github.com/beevik/etree:
// absolute (/a/b/c) and relative paths, descendants (//name), * , . and ..,
// positions ([1], [last()]) and filters ([@attr], [@attr='v'], [child],
// [child='v']); a final /@attr returns an attribute and /text() the text.
// Names match any namespace unless written with their prefix (ns:name).
func XPath(doc []byte, p string) (value string, found bool, err error) {
	d := etree.NewDocument()
	if err := d.ReadFromBytes(doc); err != nil {
		return "", false, fmt.Errorf("the response is not XML: %v", err)
	}
	attr := ""
	elemPath := strings.TrimSuffix(p, "/text()")
	if i := strings.LastIndex(elemPath, "/@"); i >= 0 && !strings.ContainsAny(elemPath[i+2:], "[]/='") {
		elemPath, attr = elemPath[:i], elemPath[i+2:]
	}
	if elemPath == "" {
		elemPath = "."
	}
	path, err := etree.CompilePath(elemPath)
	if err != nil {
		return "", false, fmt.Errorf("invalid path %q: %v", p, err)
	}
	e := d.FindElementPath(path)
	if e == nil {
		return "", false, nil
	}
	if attr != "" {
		a := e.SelectAttr(attr)
		if a == nil {
			return "", false, nil
		}
		return a.Value, true, nil
	}
	return strings.TrimSpace(textOf(e)), true, nil
}

// textOf is the text content of an element and its descendants.
func textOf(e *etree.Element) string {
	var b strings.Builder
	for _, t := range e.Child {
		switch t := t.(type) {
		case *etree.CharData:
			b.WriteString(t.Data)
		case *etree.Element:
			b.WriteString(textOf(t))
		}
	}
	return b.String()
}

// JSONPath returns the value at a dotted path (a.b.0.c: object keys and
// array indexes) of a JSON document; "" or "." is the document.
func JSONPath(doc []byte, p string) (value any, found bool, err error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false, fmt.Errorf("the response is not JSON: %v", err)
	}
	if p == "" || p == "." {
		return v, true, nil
	}
	for _, seg := range strings.Split(p, ".") {
		switch n := v.(type) {
		case map[string]any:
			if v, found = n[seg]; !found {
				return nil, false, nil
			}
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(n) {
				return nil, false, nil
			}
			v = n[i]
		default:
			return nil, false, nil
		}
	}
	return v, true, nil
}

// scalar writes a JSON or YAML value for comparison and reports: strings as
// they are, numbers in their shortest form, objects and arrays as JSON.
func scalar(v any) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64)
	case int, int64, uint64, bool:
		return fmt.Sprint(v)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// sameValue compares a JSON value with an expected YAML value: numbers by
// value, objects and arrays as JSON, anything else by its text.
func sameValue(got, want any) bool {
	g, w := scalar(got), scalar(want)
	if g == w {
		return true
	}
	gf, err1 := strconv.ParseFloat(g, 64)
	wf, err2 := strconv.ParseFloat(w, 64)
	if err1 == nil && err2 == nil && !math.IsNaN(gf) {
		return gf == wf
	}
	if gm, ok := got.(map[string]any); ok {
		wm, err := json.Marshal(normalizeYAML(want))
		if err != nil {
			return false
		}
		gb, _ := json.Marshal(gm)
		return jsonEqual(gb, wm)
	}
	if _, ok := got.([]any); ok {
		gb, _ := json.Marshal(got)
		wm, err := json.Marshal(normalizeYAML(want))
		return err == nil && jsonEqual(gb, wm)
	}
	return false
}

// normalizeYAML turns YAML maps with non-string keys into JSON-encodable ones.
func normalizeYAML(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, x := range v {
			out[k] = normalizeYAML(x)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(v))
		for k, x := range v {
			out[fmt.Sprint(k)] = normalizeYAML(x)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = normalizeYAML(x)
		}
		return out
	}
	return v
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return bytes.Equal(xb, yb)
}

// expectsFailure tells whether a message status means the message did not
// complete (FAILED, ESCALATED, RETRY, ABANDONED, ...).
func expectsFailure(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "", "COMPLETED", "PROCESSING", "DISCARDED":
		return false
	}
	return true
}
