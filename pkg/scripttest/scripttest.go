// Package scripttest runs the tests of script collection scripts (the
// project's Gradle build, see the build skill's script-tests.md) and reports
// them per test and per script function: its line coverage (JaCoCo), the
// test files that call it and the outcome of their tests. The paths are the
// lint configuration's (scriptTests in .cpi/lint.yaml), so lint's
// collection-script-tests and collection-script-coverage rules read the same
// files.
package scripttest

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/lint"
	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// Options configure Run.
type Options struct {
	// Project is the project root: the folder with .cpi and the Gradle build
	// (default the current directory).
	Project string
	// Content is the content tree with the script collections (default
	// <Project>/packages).
	Content string
	// Config is the lint configuration (default <Project>/.cpi/lint.yaml):
	// scriptTests.dir and scriptTests.coverageReport.
	Config *lint.Config
	// ResultsDir holds the JUnit XML reports (default
	// <Project>/build/test-results/test).
	ResultsDir string
	// Command runs the tests in Project (default ./gradlew test when the
	// project has a Gradle wrapper, else gradle test).
	Command []string
	// NoRun only reads the reports of an earlier run.
	NoRun bool
	// Output receives the command's output (default discarded).
	Output io.Writer
}

// Test statuses.
const (
	StatusPassed  = "passed"
	StatusFailed  = "failed"
	StatusError   = "error"
	StatusSkipped = "skipped"
	// StatusUntested: no test file calls the function.
	StatusUntested = "untested"
)

// TestCase is one test of the JUnit reports.
type TestCase struct {
	Class      string `json:"class"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMs int64  `json:"durationMs"`
	// Message is the failure or error message (first line).
	Message string `json:"message,omitempty"`
}

// Function is a public function of a collection script.
type Function struct {
	Name    string `json:"name"`
	Line    int    `json:"line"`
	Summary string `json:"summary,omitempty"`
	// LineCoverage is the percentage of the function's lines the tests ran
	// (nil: not in the coverage report).
	LineCoverage *float64 `json:"lineCoverage,omitempty"`
	// TestFiles are the test files (below the tests folder of the
	// collection) that call the function; Tests the test methods that call
	// it ("Class.method", from the sources).
	TestFiles []string `json:"testFiles,omitempty"`
	Tests     []string `json:"tests,omitempty"`
	// Status sums up those tests: failed when one failed or had an error,
	// passed when they ran and passed, skipped when none of them ran,
	// untested without test files.
	Status string `json:"status"`
}

// Script is a script of a script collection.
type Script struct {
	Package    string `json:"package"`
	Collection string `json:"collection"`
	// File is relative to the content tree.
	File string `json:"file"`
	// LineCoverage is the script's line coverage (nil: not in the report).
	LineCoverage *float64   `json:"lineCoverage,omitempty"`
	Functions    []Function `json:"functions"`
}

// Result is the outcome of Run.
type Result struct {
	// Command is the command that ran ("" with NoRun), ExitCode its exit
	// code.
	Command    string     `json:"command,omitempty"`
	ExitCode   int        `json:"exitCode"`
	DurationMs int64      `json:"durationMs"`
	Passed     int        `json:"passed"`
	Failed     int        `json:"failed"`
	Errors     int        `json:"errors"`
	Skipped    int        `json:"skipped"`
	Tests      []TestCase `json:"tests"`
	Scripts    []Script   `json:"scripts"`
	Warnings   []string   `json:"warnings,omitempty"`
}

// Err is the outcome as an error with an exit code: failed (5) when a test
// failed or the command failed without a failed test, nil otherwise.
func (r *Result) Err() error {
	switch {
	case r.Failed+r.Errors > 0:
		return output.Failed(fmt.Errorf("%d of %d script test(s) failed", r.Failed+r.Errors, len(r.Tests)))
	case r.ExitCode != 0:
		return output.Failed(fmt.Errorf("the test command failed with exit code %d (see its output)", r.ExitCode))
	}
	return nil
}

// Run runs the tests (unless NoRun) and reads the reports.
func Run(ctx context.Context, o Options) (*Result, error) {
	if o.Project == "" {
		o.Project = "."
	}
	if o.Content == "" {
		o.Content = filepath.Join(o.Project, "packages")
	}
	if info, err := os.Stat(o.Content); err != nil || !info.IsDir() {
		return nil, output.Usagef("no content tree at %s (the folder with the packages; --content)", o.Content)
	}
	if o.Config == nil {
		cfg, err := lint.LoadConfig(filepath.Join(o.Project, ".cpi", "lint.yaml"))
		if err != nil {
			return nil, err
		}
		o.Config = cfg
	}
	if o.ResultsDir == "" {
		o.ResultsDir = filepath.Join(o.Project, "build", "test-results", "test")
	}
	if o.Output == nil {
		o.Output = io.Discard
	}
	res := &Result{Tests: []TestCase{}, Scripts: []Script{}}
	start := time.Now()
	if !o.NoRun {
		cmd := o.Command
		if len(cmd) == 0 {
			cmd = []string{"gradle", "test"}
			if _, err := os.Stat(filepath.Join(o.Project, "gradlew")); err == nil {
				cmd = []string{"./gradlew", "test"}
			}
		}
		res.Command = strings.Join(cmd, " ")
		c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
		c.Dir, c.Stdout, c.Stderr = o.Project, o.Output, o.Output
		if err := c.Run(); err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				return nil, output.Usagef("running %s: %v (install Gradle, or pass the command)", res.Command, err)
			}
			res.ExitCode = exit.ExitCode() // failed tests fail the build: read the reports
		}
	}
	res.DurationMs = time.Since(start).Milliseconds()

	if err := readJUnit(o.ResultsDir, res); err != nil {
		return nil, err
	}
	cov, err := readJaCoCo(o.Config.Path(o.Config.ScriptTests.CoverageReport))
	if err != nil {
		res.Warnings = append(res.Warnings, "coverage: "+err.Error())
	}
	if err := readScripts(ctx, o, cov, res); err != nil {
		return nil, err
	}
	return res, nil
}

// junitSuite is the part of a JUnit XML report Run reads.
type junitSuite struct {
	Cases []struct {
		Class   string  `xml:"classname,attr"`
		Name    string  `xml:"name,attr"`
		Time    float64 `xml:"time,attr"`
		Failure *struct {
			Message string `xml:"message,attr"`
			Text    string `xml:",chardata"`
		} `xml:"failure"`
		Error *struct {
			Message string `xml:"message,attr"`
			Text    string `xml:",chardata"`
		} `xml:"error"`
		Skipped *struct{} `xml:"skipped"`
	} `xml:"testcase"`
}

func readJUnit(dir string, res *Result) error {
	files, _ := filepath.Glob(filepath.Join(dir, "*.xml"))
	if len(files) == 0 {
		res.Warnings = append(res.Warnings, "no JUnit reports in "+filepath.ToSlash(dir))
		return nil
	}
	sort.Strings(files)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		var s junitSuite
		if err := xml.Unmarshal(data, &s); err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %v", filepath.Base(f), err))
			continue
		}
		for _, c := range s.Cases {
			tc := TestCase{Class: c.Class, Name: c.Name, Status: StatusPassed, DurationMs: int64(c.Time * 1000)}
			switch {
			case c.Failure != nil:
				tc.Status, tc.Message = StatusFailed, firstLine(c.Failure.Message, c.Failure.Text)
				res.Failed++
			case c.Error != nil:
				tc.Status, tc.Message = StatusError, firstLine(c.Error.Message, c.Error.Text)
				res.Errors++
			case c.Skipped != nil:
				tc.Status = StatusSkipped
				res.Skipped++
			default:
				res.Passed++
			}
			res.Tests = append(res.Tests, tc)
		}
	}
	return nil
}

func firstLine(texts ...string) string {
	for _, t := range texts {
		if t = strings.TrimSpace(t); t != "" {
			line, _, _ := strings.Cut(t, "\n")
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// coverage is the line coverage of a JaCoCo report: per source file, and
// per source file and method name.
type coverage struct {
	files   map[string]counter
	methods map[string]counter // "<file>#<method>"
}

type counter struct{ missed, covered int }

func (c counter) percent() *float64 {
	if c.missed+c.covered == 0 {
		return nil
	}
	p := 100 * float64(c.covered) / float64(c.missed+c.covered)
	return &p
}

type jacocoCounter struct {
	Type    string `xml:"type,attr"`
	Missed  int    `xml:"missed,attr"`
	Covered int    `xml:"covered,attr"`
}

func lineCounter(cs []jacocoCounter) counter {
	for _, c := range cs {
		if c.Type == "LINE" {
			return counter{c.Missed, c.Covered}
		}
	}
	return counter{}
}

func readJaCoCo(file string) (*coverage, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var r struct {
		Packages []struct {
			Classes []struct {
				Source  string `xml:"sourcefilename,attr"`
				Methods []struct {
					Name     string          `xml:"name,attr"`
					Counters []jacocoCounter `xml:"counter"`
				} `xml:"method"`
			} `xml:"class"`
			Sources []struct {
				Name     string          `xml:"name,attr"`
				Counters []jacocoCounter `xml:"counter"`
			} `xml:"sourcefile"`
		} `xml:"package"`
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false // JaCoCo reports reference a DTD
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	cov := &coverage{files: map[string]counter{}, methods: map[string]counter{}}
	for _, p := range r.Packages {
		for _, s := range p.Sources {
			c := lineCounter(s.Counters)
			old := cov.files[s.Name]
			cov.files[s.Name] = counter{old.missed + c.missed, old.covered + c.covered}
		}
		for _, cl := range p.Classes {
			for _, m := range cl.Methods {
				// overloads and generated variants (default arguments) add up
				key := cl.Source + "#" + m.Name
				c, old := lineCounter(m.Counters), cov.methods[key]
				cov.methods[key] = counter{old.missed + c.missed, old.covered + c.covered}
			}
		}
	}
	return cov, nil
}

// readScripts lists the public functions of the collection scripts with
// coverage, calling test files and their outcome.
func readScripts(ctx context.Context, o Options, cov *coverage, res *Result) error {
	byClass := map[string][]TestCase{}
	for _, t := range res.Tests {
		simple := t.Class[strings.LastIndex(t.Class, ".")+1:]
		byClass[simple] = append(byClass[simple], t)
	}
	var walkErr error
	err := ops.WalkLocalArtifacts(ctx, o.Content, func(la ops.LocalArtifact) {
		if la.Type != "ScriptCollection" {
			return
		}
		scriptDir := filepath.Join(la.Dir, "src", "main", "resources", "script")
		files, _ := filepath.Glob(filepath.Join(scriptDir, "*.groovy"))
		sort.Strings(files)
		tests := testSources(filepath.Join(o.Config.Path(o.Config.ScriptTests.Dir), filepath.FromSlash(la.Rel)))
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				walkErr = errors.Join(walkErr, err)
				continue
			}
			name := filepath.Base(f)
			s := Script{Package: la.PackageID, Collection: la.ID, File: path.Join(la.Rel, "src/main/resources/script", name), Functions: []Function{}}
			if cov != nil {
				s.LineCoverage = cov.files[name].percent()
			}
			for _, fn := range ops.ParseScriptDoc(data).Functions {
				if fn.Private {
					continue
				}
				out := Function{Name: fn.Name, Line: fn.Line, Summary: fn.Summary, Status: StatusUntested}
				if cov != nil {
					out.LineCoverage = cov.methods[name+"#"+fn.Name].percent()
				}
				calls := regexp.MustCompile(`\b` + regexp.QuoteMeta(fn.Name) + `\s*\(`)
				var cases []TestCase
				for _, tf := range sortedKeys(tests) {
					src := tests[tf]
					if !calls.MatchString(src) {
						continue
					}
					out.TestFiles = append(out.TestFiles, tf)
					class := strings.TrimSuffix(path.Base(tf), path.Ext(tf))
					methods := testMethods(src)
					var callers []string
					for _, m := range methods {
						if calls.MatchString(src[m.start:m.end]) {
							callers = append(callers, m.name)
						}
					}
					if len(callers) == 0 { // called from a helper or setup: every test of the class
						cases = append(cases, byClass[class]...)
						continue
					}
					for _, name := range callers {
						out.Tests = append(out.Tests, class+"."+name)
						for _, t := range byClass[class] {
							if strings.TrimSuffix(t.Name, "()") == name {
								cases = append(cases, t)
							}
						}
					}
				}
				if len(out.TestFiles) > 0 {
					out.Status = statusOf(cases)
				}
				s.Functions = append(s.Functions, out)
			}
			res.Scripts = append(res.Scripts, s)
		}
	}, func(err error) { walkErr = errors.Join(walkErr, err) })
	if err != nil {
		return err
	}
	return walkErr
}

// statusOf sums up the tests that call a function.
func statusOf(cases []TestCase) string {
	status := StatusSkipped // test files call it, but none of their tests ran
	for _, t := range cases {
		switch t.Status {
		case StatusFailed, StatusError:
			return StatusFailed
		case StatusPassed:
			status = StatusPassed
		}
	}
	return status
}

// testMethod is a method of a test source and its text range.
type testMethod struct {
	name       string
	start, end int
}

// reTestMethod finds method declarations: JUnit (void name(), def name())
// and Spock feature methods (def "name"()).
var reTestMethod = regexp.MustCompile(`(?m)^[ \t]*(?:@\w+(?:\([^)]*\))?\s+)*(?:(?:public|protected|private|static|final)\s+)*(?:void|def|[\w.<>\[\]]+)\s+(?:(\w+)|"([^"]+)"|'([^']+)')\s*\([^)]*\)\s*(?:throws\s+[\w.,\s]+)?\{`)

// testMethods splits a test source into its methods: each runs from its
// declaration to the next one.
func testMethods(src string) []testMethod {
	idx := reTestMethod.FindAllStringSubmatchIndex(src, -1)
	out := make([]testMethod, 0, len(idx))
	for i, m := range idx {
		name := ""
		for g := 2; g+1 < len(m); g += 2 {
			if m[g] >= 0 {
				name = src[m[g]:m[g+1]]
				break
			}
		}
		end := len(src)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		out = append(out, testMethod{name: name, start: m[1], end: end})
	}
	return out
}

// testSources are the Groovy and Java files below dir by relative path.
func testSources(dir string) map[string]string {
	out := map[string]string{}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".groovy", ".java":
			if data, err := os.ReadFile(p); err == nil {
				rel, _ := filepath.Rel(dir, p)
				out[filepath.ToSlash(rel)] = string(data)
			}
		}
		return nil
	})
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
