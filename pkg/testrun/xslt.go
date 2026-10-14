package testrun

import (
	"bytes"
	"cmp"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// XSLTDir is the folder below the tests folder with the XSLT cases of
// Partner Directory mappings: <dir>/pd/<PID>/<ID>/<case>.yaml tests the
// binary parameter <PID>/<ID>.
const XSLTDir = "pd"

// XSLTCase is a test of an XSLT mapping stored in the Partner Directory.
type XSLTCase struct {
	// PID and ID name the binary parameter (the folders); Name is the file
	// name without .yaml.
	PID  string `yaml:"-" json:"pid"`
	ID   string `yaml:"-" json:"id"`
	Name string `yaml:"-" json:"name"`
	File string `yaml:"-" json:"file"`

	Description string   `yaml:"description" json:"description,omitempty"`
	Tags        []string `yaml:"tags" json:"tags,omitempty"`
	Skip        string   `yaml:"skip" json:"skip,omitempty"`
	Order       int      `yaml:"order" json:"order,omitempty"`
	// XSLT is the stylesheet, relative to the case file; default the
	// Partner Directory file <pd dir>/<PID>/Binary/<ID>.xsl (or .xslt).
	XSLT string `yaml:"xslt" json:"xslt,omitempty"`
	// Input or InputFile (relative to the case file) is the message body.
	Input     string `yaml:"input" json:"input,omitempty"`
	InputFile string `yaml:"input_file" json:"inputFile,omitempty"`
	// Headers and Properties are passed as stylesheet parameters
	// (xsl:param), as Cloud Integration's XSLT mapping does.
	Headers    map[string]string `yaml:"headers" json:"headers,omitempty"`
	Properties map[string]string `yaml:"properties" json:"properties,omitempty"`
	Expect     XSLTExpect        `yaml:"expect" json:"expect"`
}

// XSLTExpect is the expected outcome of an XSLT case.
type XSLTExpect struct {
	// Output (or OutputFile, relative to the case file) is the expected
	// result: compared as XML without insignificant whitespace, comments
	// and the XML declaration; as trimmed text when it is not XML.
	Output            string            `yaml:"output" json:"output,omitempty"`
	OutputFile        string            `yaml:"output_file" json:"outputFile,omitempty"`
	OutputContains    StringList        `yaml:"output_contains" json:"outputContains,omitempty"`
	OutputNotContains StringList        `yaml:"output_not_contains" json:"outputNotContains,omitempty"`
	OutputXPath       map[string]string `yaml:"output_xpath" json:"outputXpath,omitempty"`
	// ErrorContains expects the transformation to fail with these texts.
	ErrorContains StringList `yaml:"error_contains" json:"errorContains,omitempty"`
}

// Key is <PID>/<ID>, the parameter under test.
func (c XSLTCase) Key() string { return c.PID + "/" + c.ID }

// suite is the Flow of the case's CaseResult: pd:<PID>/<ID>.
func (c XSLTCase) suite() string { return "pd:" + c.Key() }

// LoadXSLT reads the XSLT cases below <dir>/pd strictly (unknown keys and
// invalid YAML are usage errors with file:line). Filter.Flows matches
// <PID>/<ID>, Cases the case name, Tags the tags. No folder: no cases.
func LoadXSLT(dir string, filter Filter) ([]XSLTCase, error) {
	root := filepath.Join(dir, XSLTDir)
	files, _ := filepath.Glob(filepath.Join(root, "*", "*", "*.yaml"))
	more, _ := filepath.Glob(filepath.Join(root, "*", "*", "*.yml"))
	files = append(files, more...)
	sort.Strings(files)
	var cases []XSLTCase
	var problems []string
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var c XSLTCase
		if err := decodeStrict(data, &c); err != nil {
			problems = append(problems, fileError(f, err).Error())
			continue
		}
		c.File = f
		c.Name = strings.TrimSuffix(strings.TrimSuffix(filepath.Base(f), ".yaml"), ".yml")
		c.ID = filepath.Base(filepath.Dir(f))
		c.PID = filepath.Base(filepath.Dir(filepath.Dir(f)))
		if err := c.validate(); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", f, err))
			continue
		}
		if matchAny(filter.Flows, c.Key()) && matchAny(filter.Cases, c.Name) && (len(filter.Tags) == 0 || matchAny(filter.Tags, c.Tags...)) {
			cases = append(cases, c)
		}
	}
	if len(problems) > 0 {
		return nil, output.Usagef("invalid XSLT case file(s):\n%s", strings.Join(problems, "\n"))
	}
	sort.SliceStable(cases, func(i, j int) bool {
		a, b := cases[i], cases[j]
		if a.Key() != b.Key() {
			return a.Key() < b.Key()
		}
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		return a.Name < b.Name
	})
	return cases, nil
}

func (c *XSLTCase) validate() error {
	switch {
	case c.Input != "" && c.InputFile != "":
		return fmt.Errorf("use either input or input_file")
	case c.Expect.Output != "" && c.Expect.OutputFile != "":
		return fmt.Errorf("expect: use either output or output_file")
	}
	for name := range c.Headers {
		if _, dup := c.Properties[name]; dup {
			return fmt.Errorf("%s is both a header and a property: the stylesheet parameter would be ambiguous", name)
		}
	}
	for _, f := range []string{c.XSLT, c.InputFile, c.Expect.OutputFile} {
		if f == "" {
			continue
		}
		if filepath.IsAbs(f) {
			return fmt.Errorf("%s must be relative to the case file", f)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(c.File), f)); err != nil {
			return fmt.Errorf("%s not found next to the case file", f)
		}
	}
	return nil
}

// XSLTOptions configure RunXSLT.
type XSLTOptions struct {
	// PDDir is the Partner Directory folder of pd-snapshot (default
	// partner-directory): the stylesheet of a case without xslt.
	PDDir string
	// Command runs Saxon: the arguments -s:<input> -xsl:<stylesheet>
	// -o:<output> and name=value per parameter are appended (e.g. java -jar
	// saxon-he.jar).
	Command []string
	// Parallel is the number of cases run at the same time (default 1).
	Parallel int
	// Progress receives each case result when it is final.
	Progress func(CaseResult)
	// Now is the clock (default time.Now).
	Now func() time.Time
}

// RunXSLT runs the XSLT cases with Saxon and checks their expectations.
// The result has one CaseResult per case (Flow pd:<PID>/<ID>, Response the
// output); Result.Err gives the exit code. Nothing is sent to a tenant.
func RunXSLT(ctx context.Context, cases []XSLTCase, o XSLTOptions) (*Result, error) {
	if len(o.Command) == 0 {
		return nil, output.Usagef("no XSLT processor: give the Saxon command (e.g. --saxon-jar saxon-he.jar)")
	}
	o.PDDir = cmp.Or(o.PDDir, "partner-directory")
	o.Parallel = max(o.Parallel, 1)
	if o.Now == nil {
		o.Now = time.Now
	}
	res := &Result{StartedAt: o.Now().UTC(), Cases: make([]CaseResult, len(cases))}
	var mu sync.Mutex
	slots := make(chan struct{}, o.Parallel)
	var wg sync.WaitGroup
	for i, c := range cases {
		if ctx.Err() != nil {
			break
		}
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			cr := runXSLTCase(ctx, c, o)
			mu.Lock()
			defer mu.Unlock()
			res.Cases[i] = cr
			if o.Progress != nil {
				o.Progress(cr)
			}
		})
	}
	wg.Wait()
	for i := range res.Cases {
		if res.Cases[i].Status == "" {
			res.Cases[i] = CaseResult{Flow: cases[i].suite(), Case: cases[i].Name, File: cases[i].File, Status: StatusError,
				Error: cmp.Or(ctx.Err(), context.Canceled).Error(), ErrorCategory: categoryOf(output.ExitCode(context.Canceled))}
		}
		switch res.Cases[i].Status {
		case StatusPassed:
			res.Passed++
		case StatusFailed:
			res.Failed++
		case StatusSkipped:
			res.Skipped++
		default:
			res.Errors++
		}
	}
	res.DurationMs = o.Now().Sub(res.StartedAt).Milliseconds()
	return res, nil
}

// stylesheet is the case's XSLT file.
func (c XSLTCase) stylesheet(pdDir string) (string, error) {
	if c.XSLT != "" {
		return filepath.Join(filepath.Dir(c.File), c.XSLT), nil
	}
	for _, ext := range []string{".xsl", ".xslt"} {
		p := filepath.Join(pdDir, c.PID, "Binary", c.ID+ext)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", output.Usagef("no stylesheet for %s: %s/%s/Binary/%s.xsl does not exist (run pd-snapshot, or set xslt in the case)",
		c.Key(), filepath.ToSlash(pdDir), c.PID, c.ID)
}

func runXSLTCase(ctx context.Context, c XSLTCase, o XSLTOptions) CaseResult {
	start := o.Now()
	cr := CaseResult{Flow: c.suite(), Case: c.Name, File: c.File, Description: c.Description}
	finish := func(r CaseResult) CaseResult {
		r.DurationMs = o.Now().Sub(start).Milliseconds()
		return r
	}
	errResult := func(err error) CaseResult {
		r := cr
		r.Status, r.Error, r.code = StatusError, err.Error(), output.ExitCode(err)
		r.ErrorCategory = categoryOf(r.code)
		return finish(r)
	}
	if c.Skip != "" {
		cr.Status, cr.SkipReason = StatusSkipped, c.Skip
		return cr
	}
	xsl, err := c.stylesheet(o.PDDir)
	if err != nil {
		return errResult(err)
	}
	input := []byte(c.Input)
	if c.InputFile != "" {
		if input, err = os.ReadFile(filepath.Join(filepath.Dir(c.File), c.InputFile)); err != nil {
			return errResult(output.Usage(err))
		}
	}
	tmp, err := os.MkdirTemp("", "cpictl-xslt-*")
	if err != nil {
		return errResult(err)
	}
	defer os.RemoveAll(tmp)
	in, out := filepath.Join(tmp, "input.xml"), filepath.Join(tmp, "output")
	if err := os.WriteFile(in, input, 0o600); err != nil {
		return errResult(err)
	}
	args := append(append([]string{}, o.Command[1:]...), "-s:"+in, "-xsl:"+xsl, "-o:"+out)
	for _, params := range []map[string]string{c.Properties, c.Headers} {
		for _, k := range sortedKeys(params) {
			args = append(args, k+"="+params[k])
		}
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, o.Command[0], args...)
	cmd.Stdout, cmd.Stderr = io.Discard, &stderr
	runErr := cmd.Run()
	var exit *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exit) {
		return errResult(output.Usagef("running %s: %v", o.Command[0], runErr))
	}
	result, _ := os.ReadFile(out)
	errText := strings.TrimSpace(stderr.String())
	if runErr == nil {
		errText = ""
	}
	failures, err := checkXSLT(c, result, errText, runErr != nil)
	if err != nil {
		return errResult(err)
	}
	cr.Status = StatusPassed
	if len(failures) > 0 {
		cr.Status, cr.Failures = StatusFailed, failures
		e := ops.NewContent(result, ResponseExcerptBytes)
		cr.Response = &e
		if runErr != nil {
			cr.Diagnostics = &Diagnostics{LastError: excerpt(errText)}
		}
	}
	return finish(cr)
}

// checkXSLT compares the transformation's outcome with the expectations.
func checkXSLT(c XSLTCase, result []byte, errText string, failed bool) ([]Failure, error) {
	e := c.Expect
	var out []Failure
	add := func(exp, want, got, msg string) {
		out = append(out, Failure{Expectation: exp, Expected: want, Actual: got, Message: msg})
	}
	if len(e.ErrorContains) > 0 {
		if !failed {
			add("error_contains", strings.Join(e.ErrorContains, ", "), "no error", "the transformation succeeded, expected it to fail")
		}
		for _, want := range e.ErrorContains {
			if failed && !strings.Contains(errText, want) {
				add("error_contains", want, excerpt(errText), "the error does not contain "+want)
			}
		}
		return out, nil
	}
	if failed {
		add("transformation", "success", excerpt(errText), "the transformation failed: "+excerpt(errText))
		return out, nil
	}
	text := string(result)
	want := e.Output
	if e.OutputFile != "" {
		b, err := os.ReadFile(filepath.Join(filepath.Dir(c.File), e.OutputFile))
		if err != nil {
			return nil, output.Usage(err)
		}
		want = string(b)
	}
	if want != "" && !sameOutput([]byte(want), result) {
		add("output", excerpt(want), excerpt(text), "the output differs from the expected one")
	}
	for _, s := range e.OutputContains {
		if !strings.Contains(text, s) {
			add("output_contains", s, excerpt(text), "the output does not contain "+s)
		}
	}
	for _, s := range e.OutputNotContains {
		if strings.Contains(text, s) {
			add("output_not_contains", s, excerpt(text), "the output contains "+s)
		}
	}
	for _, p := range sortedKeys(e.OutputXPath) {
		got, found, err := XPath(result, p)
		switch {
		case err != nil:
			add("output_xpath["+p+"]", e.OutputXPath[p], "", err.Error())
		case !found:
			add("output_xpath["+p+"]", e.OutputXPath[p], "(no match)", p+" matches nothing in the output")
		case got != e.OutputXPath[p]:
			add("output_xpath["+p+"]", e.OutputXPath[p], got, fmt.Sprintf("%s is %q, expected %q", p, got, e.OutputXPath[p]))
		}
	}
	return out, nil
}

// sameOutput compares two outputs as XML (elements, attributes in any
// order, trimmed text; comments, processing instructions and whitespace
// between elements do not count), or as trimmed text when either is not
// XML.
func sameOutput(a, b []byte) bool {
	ta, errA := xmlTokens(a)
	tb, errB := xmlTokens(b)
	if errA != nil || errB != nil {
		return strings.TrimSpace(string(a)) == strings.TrimSpace(string(b))
	}
	if len(ta) != len(tb) {
		return false
	}
	for i := range ta {
		if ta[i] != tb[i] {
			return false
		}
	}
	return true
}

func xmlTokens(data []byte) ([]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	var out []string
	elements := 0
	for {
		t, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := t.(type) {
		case xml.StartElement:
			elements++
			attrs := make([]string, 0, len(t.Attr))
			for _, a := range t.Attr {
				if a.Name.Space == "xmlns" || a.Name.Local == "xmlns" {
					continue // declarations: the resolved names count
				}
				attrs = append(attrs, a.Name.Space+"|"+a.Name.Local+"="+a.Value)
			}
			sort.Strings(attrs)
			out = append(out, "<"+t.Name.Space+"|"+t.Name.Local+" "+strings.Join(attrs, " "))
		case xml.EndElement:
			out = append(out, ">")
		case xml.CharData:
			if s := strings.TrimSpace(string(t)); s != "" {
				out = append(out, "#"+s)
			}
		}
	}
	if elements == 0 {
		return nil, errors.New("no XML element")
	}
	return out, nil
}
