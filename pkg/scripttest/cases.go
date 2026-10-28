package scripttest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/cpars-innovation/cpicli/pkg/testrun"
)

// CaseOptions configure RunCases.
type CaseOptions struct {
	// Project holds build.gradle with the cpictlScriptCases task (written
	// by Init) and the stand-ins (default the current directory).
	Project string
	// Content is the content tree with the flows and script collections
	// (default <Project>/packages).
	Content string
	// Command runs the case runner in Project; cpictl appends
	// -PcpictlPlan=<file> -PcpictlResults=<file> (default ./gradlew or
	// gradle -q cpictlScriptCases).
	Command []string
	// Output receives the command's output (default discarded).
	Output io.Writer
}

// casesTask is the Gradle task of the template's build.gradle that runs the
// cases on the stand-ins.
const casesTask = "cpictlScriptCases"

// planCase is one case for the runner (CaseRunner.groovy).
type planCase struct {
	ID            string                       `json:"id"`
	Script        string                       `json:"script"`
	Version       int                          `json:"version"`
	Function      string                       `json:"function"`
	Args          []any                        `json:"args"`
	HasArgs       bool                         `json:"hasArgs"`
	Body          *string                      `json:"body"`
	Headers       map[string]string            `json:"headers"`
	Properties    map[string]string            `json:"properties"`
	PD            map[string]string            `json:"pd"`
	ValueMappings map[string]string            `json:"valueMappings"`
	Secure        map[string]map[string]string `json:"secure"`
	Keystore      map[string]string            `json:"keystore"`
}

// RunCases runs script cases on the stand-ins (locally, nothing is sent to
// a tenant) and checks their expectations. Cases with skip are skipped;
// cases whose script cannot be found, or that are JavaScript, are errors of
// that case.
func RunCases(ctx context.Context, cases []testrun.ScriptCase, o CaseOptions) (*testrun.Result, error) {
	if o.Project == "" {
		o.Project = "."
	}
	if o.Content == "" {
		o.Content = filepath.Join(o.Project, "packages")
	}
	if o.Output == nil {
		o.Output = io.Discard
	}
	res := &testrun.Result{Cases: []testrun.CaseResult{}, StartedAt: time.Now().UTC()}
	start := time.Now()
	defer func() { res.DurationMs = time.Since(start).Milliseconds() }()
	if len(cases) == 0 {
		return res, nil
	}
	gradle, err := os.ReadFile(filepath.Join(o.Project, "build.gradle"))
	if err != nil || !bytes.Contains(gradle, []byte(casesTask)) {
		return nil, output.Usagef("%s has no %s task: run cpictl test scripts init (--force replaces an older build.gradle and the stand-ins)", filepath.Join(o.Project, "build.gradle"), casesTask)
	}
	artifacts, err := localArtifacts(ctx, o.Content)
	if err != nil {
		return nil, err
	}

	results := make([]testrun.CaseResult, len(cases))
	var plan []planCase
	index := map[string]int{}
	for i, c := range cases {
		cr := testrun.CaseResult{Flow: c.Suite(), Case: c.Name, File: c.File, Description: c.Description}
		if c.Skip != "" {
			cr.Status, cr.SkipReason = testrun.StatusSkipped, c.Skip
			results[i] = cr
			continue
		}
		pc, err := planFor(c, artifacts)
		if err != nil {
			cr.Status, cr.Error, cr.ErrorCategory = testrun.StatusError, err.Error(), "usage"
			results[i] = cr
			continue
		}
		pc.ID = fmt.Sprintf("c%d", i)
		index[pc.ID] = i
		plan = append(plan, pc)
	}

	if len(plan) > 0 {
		outcomes, err := runPlan(ctx, plan, o)
		if err != nil {
			return nil, err
		}
		for id, i := range index {
			oc, ok := outcomes[id]
			if !ok {
				results[i] = testrun.CaseResult{Flow: cases[i].Suite(), Case: cases[i].Name, File: cases[i].File,
					Status: testrun.StatusError, Error: "the runner reported no result for this case", ErrorCategory: "error"}
				continue
			}
			results[i] = testrun.ScriptCaseResult(cases[i], oc)
		}
	}
	for _, cr := range results {
		switch cr.Status {
		case testrun.StatusPassed:
			res.Passed++
		case testrun.StatusFailed:
			res.Failed++
		case testrun.StatusSkipped:
			res.Skipped++
		default:
			res.Errors++
		}
		res.Cases = append(res.Cases, cr)
	}
	return res, nil
}

// localArtifacts maps artifact IDs to their folders.
func localArtifacts(ctx context.Context, content string) (map[string]ops.LocalArtifact, error) {
	if info, err := os.Stat(content); err != nil || !info.IsDir() {
		return nil, output.Usagef("no content tree at %s (the folder with the packages; --content)", content)
	}
	out := map[string]ops.LocalArtifact{}
	err := ops.WalkLocalArtifacts(ctx, content, func(la ops.LocalArtifact) { out[la.ID] = la }, func(error) {})
	return out, err
}

var rePackageV2 = regexp.MustCompile(`(?m)^\s*package\s+script\.v2\b`)

// planFor resolves a case's script and service answers.
func planFor(c testrun.ScriptCase, artifacts map[string]ops.LocalArtifact) (planCase, error) {
	la, ok := artifacts[c.Owner]
	want := "Integration"
	if c.Kind == testrun.ScriptKindCollection {
		want = "ScriptCollection"
	}
	if !ok || la.Type != want {
		return planCase{}, fmt.Errorf("%s %s is not in the content tree", strings.ToLower(want), c.Owner)
	}
	scriptDir := filepath.Join(la.Dir, filepath.FromSlash(ops.ResourcesDir), "script")
	var script string
	if c.Kind == testrun.ScriptKindFlow {
		for _, p := range []string{filepath.Join(scriptDir, c.Script), filepath.Join(scriptDir, "v2", c.Script)} {
			if _, err := os.Stat(p); err == nil {
				script = p
				break
			}
		}
		if script == "" {
			return planCase{}, fmt.Errorf("%s has no script %s (src/main/resources/script)", c.Owner, c.Script)
		}
	} else {
		var err error
		if script, err = scriptDefining(scriptDir, c.Function()); err != nil {
			return planCase{}, fmt.Errorf("script collection %s: %w", c.Owner, err)
		}
	}
	if !strings.HasSuffix(script, ".groovy") && !strings.HasSuffix(script, ".gsh") {
		return planCase{}, fmt.Errorf("%s: only Groovy scripts run locally; JavaScript cases run on the tenant", filepath.Base(script))
	}
	src, err := os.ReadFile(script)
	if err != nil {
		return planCase{}, err
	}
	pc := planCase{Script: script, Version: 1, Function: c.Function(), Args: c.Call.Args, HasArgs: c.Call.Args != nil,
		Headers: c.Input.Headers, Properties: c.Input.Properties, ValueMappings: c.ValueMappings, Keystore: map[string]string{}}
	if rePackageV2.Match(src) || filepath.Base(filepath.Dir(script)) == "v2" {
		pc.Version = 2
	}
	switch {
	case c.Input.BodyFile != "":
		data, err := c.InputBody()
		if err != nil {
			return planCase{}, err
		}
		s := string(data)
		pc.Body = &s
	case c.Input.Body != "":
		s := c.Input.Body
		pc.Body = &s
	}
	if len(c.PD) > 0 {
		pc.PD = map[string]string{}
		for k, v := range c.PD {
			if f, ok := strings.CutPrefix(v, "file:"); ok {
				data, err := os.ReadFile(filepath.Join(filepath.Dir(c.File), f))
				if err != nil {
					return planCase{}, fmt.Errorf("pd.%s: %w", k, err)
				}
				v = base64.StdEncoding.EncodeToString(data) // binary parameters are base64 for the stand-ins
			}
			pc.PD[k] = v
		}
	}
	if len(c.Secure) > 0 {
		pc.Secure = map[string]map[string]string{}
		for alias, s := range c.Secure {
			e := map[string]string{}
			if s.Value != "" {
				e["value"] = s.Value
			} else {
				e["user"], e["password"] = s.User, s.Password
			}
			pc.Secure[alias] = e
		}
	}
	for alias, v := range c.Keystore {
		if strings.HasPrefix(v, "generate:") {
			v = "generate" // a fresh RSA key with a self-signed certificate
		}
		pc.Keystore[alias] = v
	}
	return pc, nil
}

// scriptDefining finds the one script of a collection that defines the
// function.
func scriptDefining(dir, function string) (string, error) {
	re := regexp.MustCompile(`(?m)^[ \t]*(?:[\w.<>\[\], ]+[ \t]+)?` + regexp.QuoteMeta(function) + `[ \t]*\([^)]*\)[ \t]*(?:throws[^{]*)?\{`)
	var found []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".groovy") {
			return err
		}
		data, err := os.ReadFile(p)
		if err == nil && re.Match(data) {
			found = append(found, p)
		}
		return err
	})
	switch {
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return "", err
	case len(found) == 0:
		return "", fmt.Errorf("no script defines %s", function)
	case len(found) > 1:
		var names []string
		for _, f := range found {
			rel, _ := filepath.Rel(dir, f)
			names = append(names, filepath.ToSlash(rel))
		}
		return "", fmt.Errorf("%s is defined in several scripts (%s)", function, strings.Join(names, ", "))
	}
	return found[0], nil
}

// runPlan writes the plan, runs the Gradle task and reads the outcomes.
func runPlan(ctx context.Context, plan []planCase, o CaseOptions) (map[string]testrun.ScriptOutcome, error) {
	dir := filepath.Join(o.Project, "build", "cpictl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	planFile, resultsFile := filepath.Join(dir, "script-cases.json"), filepath.Join(dir, "script-results.json")
	data, err := json.Marshal(map[string]any{"cases": plan})
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(planFile, data, 0o600); err != nil {
		return nil, err
	}
	_ = os.Remove(resultsFile)
	absPlan, _ := filepath.Abs(planFile)
	absResults, _ := filepath.Abs(resultsFile)
	cmd := o.Command
	if len(cmd) == 0 {
		cmd = []string{"gradle", "-q", casesTask}
		if _, err := os.Stat(filepath.Join(o.Project, "gradlew")); err == nil {
			cmd = []string{"./gradlew", "-q", casesTask}
		}
	}
	cmd = append(append([]string{}, cmd...), "-PcpictlPlan="+absPlan, "-PcpictlResults="+absResults)
	var stderr bytes.Buffer
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	c.Dir, c.Stdout, c.Stderr = o.Project, o.Output, io.MultiWriter(o.Output, &stderr)
	if err := c.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return nil, output.Usagef("running %s: %v (install Gradle, or pass the command)", strings.Join(cmd, " "), err)
		}
		return nil, fmt.Errorf("%s failed (exit code %d): %s", strings.Join(cmd, " "), exit.ExitCode(), lastLines(stderr.String(), 15))
	}
	out, err := os.ReadFile(resultsFile)
	if err != nil {
		return nil, fmt.Errorf("the case runner wrote no results (%s): %w", resultsFile, err)
	}
	var parsed struct {
		Cases []testrun.ScriptOutcome `json:"cases"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("reading %s: %w", resultsFile, err)
	}
	outcomes := map[string]testrun.ScriptOutcome{}
	for _, oc := range parsed.Cases {
		outcomes[oc.ID] = oc
	}
	return outcomes, nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
