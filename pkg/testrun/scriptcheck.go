package testrun

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// ScriptOutcome is what a script case runner observed for one case: the
// message after the call, the return value or the exception, and what the
// script wrote to the message log.
type ScriptOutcome struct {
	ID string `json:"id"`
	// Error: the case could not be run (e.g. the function does not exist).
	Error      string             `json:"error,omitempty"`
	Body       *string            `json:"body"`
	Headers    map[string]*string `json:"headers"`
	Properties map[string]*string `json:"properties"`
	Returns    any                `json:"returns"`
	HasReturns bool               `json:"hasReturns"`
	Throws     *struct {
		Class   string `json:"class"`
		Message string `json:"message"`
	} `json:"throws"`
	// Unsupported: the exception is the stand-ins' (a part only the tenant
	// has), not the script's.
	Unsupported bool `json:"unsupported"`
	Log         struct {
		CustomHeaders map[string][]string `json:"customHeaders"`
		Properties    map[string]any      `json:"properties"`
	} `json:"log"`
	DurationMs int64 `json:"durationMs"`
}

// ScriptCaseResult turns an outcome into the case's result.
func ScriptCaseResult(c ScriptCase, o ScriptOutcome) CaseResult {
	cr := CaseResult{Flow: c.Suite(), Case: c.Name, File: c.File, Description: c.Description, DurationMs: o.DurationMs}
	switch {
	case o.Error != "":
		cr.Status, cr.Error, cr.ErrorCategory = StatusError, o.Error, "usage"
		return cr
	case o.Unsupported && c.Expect.Throws == nil:
		cr.Status, cr.ErrorCategory = StatusError, "unsupported"
		cr.Error = "the script uses a part of SAP's API the stand-ins do not implement (" + o.Throws.Message + "): test it on the tenant"
		return cr
	}
	cr.Status = StatusPassed
	if failures := CheckScript(c, o); len(failures) > 0 {
		cr.Status, cr.Failures = StatusFailed, failures
		if o.Body != nil {
			e := ops.NewContent([]byte(*o.Body), ResponseExcerptBytes)
			cr.Response = &e
		}
		if o.Throws != nil {
			cr.Diagnostics = &Diagnostics{LastError: excerpt(o.Throws.Class + ": " + o.Throws.Message)}
		}
	}
	return cr
}

// CheckScript compares an outcome with the case's expectations.
func CheckScript(c ScriptCase, o ScriptOutcome) []Failure {
	e := c.Expect
	var out []Failure
	fail := func(exp, want, got, msg string) {
		out = append(out, Failure{Expectation: exp, Expected: want, Actual: excerpt(got), Message: msg})
	}
	if e.MaxDuration != "" {
		if d, err := time.ParseDuration(e.MaxDuration); err == nil && time.Duration(o.DurationMs)*time.Millisecond > d {
			fail("max_duration", e.MaxDuration, fmt.Sprintf("%dms", o.DurationMs), "the call took longer")
		}
	}
	for name, want := range e.Log {
		values := o.Log.CustomHeaders[name]
		if slices.Contains(values, want) {
			continue
		}
		if v, ok := o.Log.Properties[name]; ok && fmt.Sprint(v) == want {
			continue
		}
		fail("log["+name+"]", want, strings.Join(values, ", "), "the message log has no such entry")
	}
	thrown := o.Throws != nil
	switch {
	case e.Throws != nil && !thrown:
		fail("throws", throwsText(e.Throws), "", "the call did not fail")
		return out
	case e.Throws != nil:
		if e.Throws.Class != "" && o.Throws.Class != e.Throws.Class && !strings.HasSuffix(o.Throws.Class, "."+e.Throws.Class) {
			fail("throws.class", e.Throws.Class, o.Throws.Class, "another exception")
		}
		if e.Throws.MessageContains != "" && !strings.Contains(o.Throws.Message, e.Throws.MessageContains) {
			fail("throws.message_contains", e.Throws.MessageContains, o.Throws.Message, "the exception message differs")
		}
		return out
	case thrown:
		fail("throws", "no exception", o.Throws.Class+": "+o.Throws.Message, "the call failed")
		return out
	}

	body := ""
	if o.Body != nil {
		body = *o.Body
	}
	if e.BodyEqualsFile != "" {
		want, err := readRef(c.File, e.BodyEqualsFile, c.Root)
		switch {
		case err != nil:
			fail("body_equals_file", e.BodyEqualsFile, "", err.Error())
		case json.Valid(want) && json.Valid([]byte(body)):
			if !jsonEqual(want, []byte(body)) {
				fail("body_equals_file", e.BodyEqualsFile, body, "the body differs (compared as JSON)")
			}
		case !sameOutput(want, []byte(body)):
			fail("body_equals_file", e.BodyEqualsFile, body, "the body differs")
		}
	}
	for _, s := range e.BodyContains {
		if !strings.Contains(body, s) {
			fail("body_contains", s, body, "the body does not contain it")
		}
	}
	for _, s := range e.BodyNotContains {
		if strings.Contains(body, s) {
			fail("body_not_contains", s, body, "the body contains it")
		}
	}
	for _, p := range sortedKeys(e.BodyXPath) {
		got, found, err := XPath([]byte(body), p)
		switch {
		case err != nil:
			fail("body_xpath["+p+"]", e.BodyXPath[p], body, err.Error())
		case !found:
			fail("body_xpath["+p+"]", e.BodyXPath[p], "", "no match")
		case got != e.BodyXPath[p]:
			fail("body_xpath["+p+"]", e.BodyXPath[p], got, "another value")
		}
	}
	for _, p := range sortedKeys(e.BodyJSON) {
		want := e.BodyJSON[p]
		got, found, err := JSONPath([]byte(body), p)
		switch {
		case err != nil:
			fail("body_json["+p+"]", scalar(want), body, err.Error())
		case !found:
			fail("body_json["+p+"]", scalar(want), "", "no match")
		case !sameValue(got, want):
			fail("body_json["+p+"]", scalar(want), scalar(got), "another value")
		}
	}
	// headers are case-insensitive, properties are not (as on the runtime)
	checkValues(fail, "headers", e.Headers, o.Headers, true)
	checkValues(fail, "properties", e.Properties, o.Properties, false)
	if e.HasReturns {
		switch {
		case !o.HasReturns:
			fail("returns", scalar(e.Returns), "", "the function returned the message, not a value")
		case e.Returns == nil && o.Returns != nil:
			fail("returns", "null", scalar(o.Returns), "another value")
		case e.Returns != nil && !sameReturn(o.Returns, e.Returns):
			fail("returns", scalar(e.Returns), scalar(o.Returns), "another value")
		}
	}
	return out
}

// sameReturn compares a return value: two strings exactly ("00004711" is not
// 4711), anything else as sameValue does.
func sameReturn(got, want any) bool {
	g, gs := got.(string)
	w, ws := want.(string)
	if gs && ws {
		return g == w
	}
	return sameValue(got, want)
}

func throwsText(t *ScriptThrows) string {
	return strings.TrimSpace(t.Class + " " + t.MessageContains)
}

func checkValues(fail func(exp, want, got, msg string), kind string, want, got map[string]*string, foldCase bool) {
	// a value of null counts as absent
	lookup := func(name string) (*string, bool) {
		if v, ok := got[name]; ok {
			return v, v != nil
		}
		if foldCase {
			for k, v := range got {
				if strings.EqualFold(k, name) {
					return v, v != nil
				}
			}
		}
		return nil, false
	}
	for _, name := range sortedKeys(want) {
		w := want[name]
		g, present := lookup(name)
		exp := kind + "[" + name + "]"
		switch {
		case w == nil && present:
			fail(exp, "absent", deref(g), "it is set")
		case w == nil:
		case !present:
			fail(exp, *w, "absent", "it is not set")
		case deref(g) != *w:
			fail(exp, *w, deref(g), "another value")
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
