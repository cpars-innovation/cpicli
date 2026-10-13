package testrun

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

type junitSuites struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Name     string       `xml:"name,attr"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Errors   int          `xml:"errors,attr"`
	Skipped  int          `xml:"skipped,attr"`
	Time     string       `xml:"time,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name      string      `xml:"name,attr"`
	Tests     int         `xml:"tests,attr"`
	Failures  int         `xml:"failures,attr"`
	Errors    int         `xml:"errors,attr"`
	Skipped   int         `xml:"skipped,attr"`
	Time      string      `xml:"time,attr"`
	Timestamp string      `xml:"timestamp,attr,omitempty"`
	Cases     []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	File      string        `xml:"file,attr,omitempty"`
	Time      string        `xml:"time,attr"`
	Failure   *junitMessage `xml:"failure,omitempty"`
	Error     *junitMessage `xml:"error,omitempty"`
	Skipped   *junitMessage `xml:"skipped,omitempty"`
	SystemOut string        `xml:"system-out,omitempty"`
}

type junitMessage struct {
	Message string `xml:"message,attr,omitempty"`
	Type    string `xml:"type,attr,omitempty"`
	Text    string `xml:",cdata"`
}

func seconds(ms int64) string { return fmt.Sprintf("%.3f", float64(ms)/1000) }

// WriteJUnit writes r as JUnit XML: a testsuite per flow, a testcase per
// case (classname the flow), failure elements with the failed expectations,
// error elements for cases that could not be run and skipped elements.
func WriteJUnit(w io.Writer, r *Result) error {
	doc := junitSuites{Name: "cpictl test run", Tests: len(r.Cases), Failures: r.Failed, Errors: r.Errors, Skipped: r.Skipped,
		Time: seconds(r.DurationMs)}
	index := map[string]int{}
	for _, c := range r.Cases {
		i, ok := index[c.Flow]
		if !ok {
			i = len(doc.Suites)
			index[c.Flow] = i
			ts := ""
			if !r.StartedAt.IsZero() {
				ts = r.StartedAt.UTC().Format("2006-01-02T15:04:05")
			}
			doc.Suites = append(doc.Suites, junitSuite{Name: c.Flow, Timestamp: ts})
		}
		s := &doc.Suites[i]
		s.Tests++
		jc := junitCase{Name: c.Case, Classname: c.Flow, File: c.File, Time: seconds(c.DurationMs)}
		switch c.Status {
		case StatusFailed:
			s.Failures++
			msgs := make([]string, 0, len(c.Failures))
			var text strings.Builder
			for _, f := range c.Failures {
				msgs = append(msgs, f.Message)
				fmt.Fprintf(&text, "%s: %s\n", f.Expectation, f.Message)
				if f.Expected != "" || f.Actual != "" {
					fmt.Fprintf(&text, "  expected: %s\n  actual:   %s\n", f.Expected, f.Actual)
				}
			}
			text.WriteString(diagnosticsText(c))
			jc.Failure = &junitMessage{Message: strings.Join(msgs, "; "), Type: "expectation", Text: text.String()}
		case StatusError:
			s.Errors++
			jc.Error = &junitMessage{Message: c.Error, Type: c.ErrorCategory, Text: diagnosticsText(c)}
		case StatusSkipped:
			s.Skipped++
			jc.Skipped = &junitMessage{Message: c.SkipReason}
		}
		if c.MessageGuid != "" {
			jc.SystemOut = fmt.Sprintf("message %s, HTTP %d", c.MessageGuid, c.HTTPStatus)
			if c.MessageStatus != "" {
				jc.SystemOut += ", status " + c.MessageStatus
			}
		}
		s.Cases = append(s.Cases, jc)
	}
	for i := range doc.Suites {
		var ms int64
		for _, c := range r.Cases {
			if c.Flow == doc.Suites[i].Name {
				ms += c.DurationMs
			}
		}
		doc.Suites[i].Time = seconds(ms)
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// diagnosticsText is the diagnostics of a case for a report.
func diagnosticsText(c CaseResult) string {
	d := c.Diagnostics
	if d == nil {
		return ""
	}
	var b strings.Builder
	if d.MessageGuid != "" {
		fmt.Fprintf(&b, "message: %s", d.MessageGuid)
		if d.MessageStatus != "" {
			fmt.Fprintf(&b, " (%s)", d.MessageStatus)
		}
		b.WriteString("\n")
	}
	if d.ErrorInformation != nil && d.ErrorInformation.LastErrorModelStepID != "" {
		fmt.Fprintf(&b, "last error step: %s\n", d.ErrorInformation.LastErrorModelStepID)
	}
	if d.FailedStep != nil {
		fmt.Fprintf(&b, "failed step: %s %s\n", d.FailedStep.ModelStepID, d.FailedStep.Activity)
	}
	if d.FirstFailure != nil && d.FirstFailure.ArtifactID != "" && d.FirstFailure.ArtifactID != c.Flow {
		fmt.Fprintf(&b, "first failure: %s (%s)\n", d.FirstFailure.ArtifactID, d.FirstFailure.MessageGuid)
	}
	if d.LastError != "" {
		fmt.Fprintf(&b, "error: %s\n", excerpt(d.LastError))
	}
	return b.String()
}
