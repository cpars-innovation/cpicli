package scripttest

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/cpars-innovation/cpicli/pkg/testrun"
	"github.com/cpars-innovation/cpicli/pkg/testtools"
	"github.com/cpars-innovation/cpicli/pkg/versioning"
)

// OnlineOptions configure RunCasesOnline.
type OnlineOptions struct {
	// Content is the content tree with the flows and script collections.
	Content string
	// Exe and NewExe are the tenant and runtime endpoint credentials; Tier
	// and AllowProduction feed the production guard.
	Exe             *httpclnt.HTTPExecuter
	NewExe          ops.EndpointExecuterFunc
	Tier            string
	AllowProduction bool
	// Keep leaves the generated test flows deployed (a later run with the
	// same script and function reuses their IDs).
	Keep bool
	// Role is the user role a runtime client needs to call the test flows
	// (default testtools.DefaultRole).
	Role string
	// Deploy polls the deployment of the test flows (Timeout, Interval;
	// default timeout 10 minutes).
	Deploy ops.Options
	// Progress receives each case result when it is final.
	Progress func(testrun.CaseResult)
}

// onlineFlow is one generated test flow and its cases (indexes).
type onlineFlow struct {
	flow  testtools.ScriptFlow
	cases []int
}

// RunCasesOnline runs script cases on the tenant's runtime: per script and
// function it generates a test flow (HTTPS -> the case's headers and
// properties -> the script under test -> the result as JSON) in the test
// tooling package, deploys the flows, sends the cases and deletes the flows
// afterwards (unless Keep). The expectations are checked as in RunCases.
//
// Cases that need what only the local run offers are skipped with the
// reason: call.args, pd, value_mappings, secure, keystore, log and returns
// expectations. max_duration is not checked online (the time includes the
// HTTP round trip).
func RunCasesOnline(ctx context.Context, cases []testrun.ScriptCase, o OnlineOptions) (*testrun.Result, error) {
	if err := ops.GuardProduction(o.Tier, o.AllowProduction, "online script tests"); err != nil {
		return nil, err
	}
	if o.Exe == nil || o.NewExe == nil {
		return nil, output.Usagef("online script tests need the tenant and runtime endpoint credentials")
	}
	exe := o.Exe.WithContext(ctx)
	res := &testrun.Result{Cases: []testrun.CaseResult{}, StartedAt: time.Now().UTC()}
	start := time.Now()
	defer func() { res.DurationMs = time.Since(start).Milliseconds() }()
	if len(cases) == 0 {
		return res, nil
	}
	artifacts, err := localArtifacts(ctx, cmp.Or(o.Content, "packages"))
	if err != nil {
		return nil, err
	}

	results := make([]testrun.CaseResult, len(cases))
	done := make([]bool, len(cases))
	finish := func(i int, cr testrun.CaseResult) {
		results[i], done[i] = cr, true
		if o.Progress != nil {
			o.Progress(cr)
		}
	}
	base := func(c testrun.ScriptCase) testrun.CaseResult {
		return testrun.CaseResult{Flow: c.Suite(), Case: c.Name, File: c.File, Description: c.Description}
	}
	flows := map[string]*onlineFlow{}
	for i, c := range cases {
		cr := base(c)
		if reason := cmp.Or(c.Skip, onlineOnlyLocal(c)); reason != "" {
			cr.Status, cr.SkipReason = testrun.StatusSkipped, reason
			finish(i, cr)
			continue
		}
		f, err := onlineFlowFor(c, artifacts, o.Role)
		if err != nil {
			cr.Status, cr.Error, cr.ErrorCategory = testrun.StatusError, err.Error(), "usage"
			finish(i, cr)
			continue
		}
		id := f.ID()
		if flows[id] == nil {
			flows[id] = &onlineFlow{flow: f}
		}
		flows[id].cases = append(flows[id].cases, i)
	}

	if len(flows) > 0 {
		ids := make([]string, 0, len(flows))
		for id := range flows {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		failAll := func(id string, err error, category string) {
			for _, i := range flows[id].cases {
				cr := base(cases[i])
				cr.Status, cr.Error, cr.ErrorCategory = testrun.StatusError, err.Error(), category
				finish(i, cr)
			}
		}
		pkgID, err := ops.TestToolsPackage(exe)
		if err != nil {
			return nil, err
		}
		if !o.Keep {
			// all of them: a flow whose upload failed may be one kept earlier
			var generated []ops.Artifact
			for _, id := range ids {
				generated = append(generated, ops.Artifact{ID: id, Type: "Integration", PackageID: pkgID})
			}
			defer func() { cleanupOnline(context.WithoutCancel(ctx), exe, generated, o.Deploy) }()
		}
		// a new version per run: changed content replaces a flow kept earlier
		version := fmt.Sprintf("1.0.%d", time.Now().Unix())
		var toDeploy []ops.Artifact
		for _, id := range ids {
			flows[id].flow.Version = version
			if err := uploadOnlineFlow(exe, flows[id].flow, pkgID); err != nil {
				failAll(id, fmt.Errorf("uploading the test flow %s: %w", id, err), "error")
				continue
			}
			toDeploy = append(toDeploy, ops.Artifact{ID: id, Type: "Integration", PackageID: pkgID, Force: true})
		}
		dopts := o.Deploy
		if dopts.Timeout == 0 && dopts.Deadline.IsZero() {
			dopts.Timeout = 10 * time.Minute
		}
		dopts.Versioning, dopts.AllowDowngrade = versioning.Manifest, true
		for _, d := range ops.Deploy(ctx, ops.NewTenant(exe), toDeploy, dopts) {
			if !d.Status.Succeeded() {
				failAll(d.ID, fmt.Errorf("deploying the test flow %s: %s", d.ID, cmp.Or(d.Error, string(d.Status))), "error")
				continue
			}
			for _, i := range flows[d.ID].cases {
				if ctx.Err() != nil {
					break
				}
				finish(i, runOnlineCase(ctx, exe, o.NewExe, d.ID, cases[i]))
			}
		}
	}
	for i := range cases {
		if !done[i] {
			cr := base(cases[i])
			cr.Status, cr.Error, cr.ErrorCategory = testrun.StatusError, cmp.Or(ctx.Err(), context.Canceled).Error(), "stopped"
			results[i] = cr
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

// onlineOnlyLocal is why a case cannot run on the tenant ("" if it can).
func onlineOnlyLocal(c testrun.ScriptCase) string {
	switch {
	case c.Call.Args != nil:
		return "call.args: runs locally only (a script step passes the message)"
	case len(c.PD) > 0:
		return "uses pd values: local only"
	case len(c.ValueMappings) > 0:
		return "uses value_mappings: local only"
	case len(c.Secure) > 0:
		return "uses secure values: local only"
	case len(c.Keystore) > 0:
		return "uses keystore entries: local only"
	case len(c.Expect.Log) > 0:
		return "log expectations: local only"
	case c.Expect.HasReturns:
		return "returns: local only (a script step returns the message)"
	}
	return ""
}

var reJSFunction = func(fn string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^[ \t]*function[ \t]+` + regexp.QuoteMeta(fn) + `[ \t]*\(`)
}

// onlineFlowFor resolves a case's script and the test flow that runs it.
func onlineFlowFor(c testrun.ScriptCase, artifacts map[string]ops.LocalArtifact, role string) (testtools.ScriptFlow, error) {
	la, ok := artifacts[c.Owner]
	want := "Integration"
	if c.Kind == testrun.ScriptKindCollection {
		want = "ScriptCollection"
	}
	if !ok || la.Type != want {
		return testtools.ScriptFlow{}, fmt.Errorf("%s %s is not in the content tree", strings.ToLower(want), c.Owner)
	}
	resDir := filepath.Join(la.Dir, filepath.FromSlash(ops.ResourcesDir))
	scriptDir := filepath.Join(resDir, "script")
	var script string
	if c.Kind == testrun.ScriptKindFlow {
		for _, p := range []string{c.Script, path.Join("v2", c.Script)} {
			if _, err := os.Stat(filepath.Join(scriptDir, filepath.FromSlash(p))); err == nil {
				script = p
				break
			}
		}
		if script == "" {
			return testtools.ScriptFlow{}, fmt.Errorf("%s has no script %s (src/main/resources/script)", c.Owner, c.Script)
		}
	} else {
		found, err := scriptDefining(scriptDir, c.Function())
		if err != nil {
			// JavaScript functions of a collection
			found, err = jsDefining(scriptDir, c.Function(), err)
			if err != nil {
				return testtools.ScriptFlow{}, fmt.Errorf("script collection %s: %w", c.Owner, err)
			}
		}
		rel, _ := filepath.Rel(scriptDir, found)
		script = filepath.ToSlash(rel)
	}
	lang := testtools.GroovyV1
	full := filepath.Join(scriptDir, filepath.FromSlash(script))
	src, err := os.ReadFile(full)
	if err != nil {
		return testtools.ScriptFlow{}, err
	}
	switch {
	case strings.HasSuffix(script, ".js"):
		lang = testtools.JavaScript
	case rePackageV2.Match(src) || strings.HasPrefix(script, "v2/"):
		lang = testtools.GroovyV2
		if !strings.HasPrefix(script, "v2/") {
			return testtools.ScriptFlow{}, fmt.Errorf("%s: a version 2 script (package script.v2) must be in script/v2/ to run on the tenant", script)
		}
	}
	files := map[string][]byte{}
	for _, dir := range []struct{ fs, prefix string }{{scriptDir, ""}, {filepath.Join(resDir, "lib"), "../lib/"}} {
		err := filepath.WalkDir(dir.fs, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(dir.fs, p)
			files[dir.prefix+filepath.ToSlash(rel)] = data
			return nil
		})
		if err != nil {
			return testtools.ScriptFlow{}, err
		}
	}
	return testtools.ScriptFlow{Owner: c.Owner, Script: script, Function: c.Function(), Language: lang, Files: files, Role: role}, nil
}

// jsDefining finds the one JavaScript file of a collection that defines the
// function; groovyErr is returned when there is none.
func jsDefining(dir, function string, groovyErr error) (string, error) {
	re := reJSFunction(function)
	var found []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".js") {
			if data, err := os.ReadFile(p); err == nil && re.Match(data) {
				found = append(found, p)
			}
		}
		return nil
	})
	switch len(found) {
	case 0:
		return "", groovyErr
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("%s is defined in several scripts", function)
}

func uploadOnlineFlow(exe *httpclnt.HTTPExecuter, f testtools.ScriptFlow, pkgID string) error {
	dir, err := os.MkdirTemp("", "cpictl-scriptflow-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := f.WriteDir(dir); err != nil {
		return err
	}
	_, err = ops.UploadArtifact(exe, ops.UploadRequest{ID: f.ID(), Type: "Integration", PackageID: pkgID, Dir: dir, Versioning: versioning.Manifest})
	return err
}

// cleanupOnline undeploys and deletes the generated test flows.
func cleanupOnline(ctx context.Context, exe *httpclnt.HTTPExecuter, flows []ops.Artifact, opts ops.Options) {
	if len(flows) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	exe = exe.WithContext(ctx)
	opts.Timeout, opts.Deadline = 3*time.Minute, time.Time{}
	ops.Undeploy(ctx, ops.NewTenant(exe), flows, opts)
	for _, f := range flows {
		_, _ = ops.DeleteArtifact(ctx, exe, ops.DeleteRequest{ArtifactID: f.ID, Type: "Integration"})
	}
}

// onlineEnvelope is what the test flow answers.
type onlineEnvelope struct {
	Body       *string            `json:"body"`
	Headers    map[string]*string `json:"headers"`
	Properties map[string]*string `json:"properties"`
}

// runOnlineCase sends one case to its test flow and checks the result.
func runOnlineCase(ctx context.Context, exe *httpclnt.HTTPExecuter, newExe ops.EndpointExecuterFunc, flowID string, c testrun.ScriptCase) testrun.CaseResult {
	cr := testrun.CaseResult{Flow: c.Suite(), Case: c.Name, File: c.File, Description: c.Description}
	body := []byte(c.Input.Body)
	if c.Input.BodyFile != "" {
		data, err := os.ReadFile(filepath.Join(filepath.Dir(c.File), c.Input.BodyFile))
		if err != nil {
			cr.Status, cr.Error, cr.ErrorCategory = testrun.StatusError, err.Error(), "usage"
			return cr
		}
		body = data
	}
	spec, err := json.Marshal(map[string]map[string]string{"headers": c.Input.Headers, "properties": c.Input.Properties})
	if err != nil {
		cr.Status, cr.Error, cr.ErrorCategory = testrun.StatusError, err.Error(), "error"
		return cr
	}
	start := time.Now()
	sent, err := ops.SendTestMessage(ctx, exe, newExe, ops.TestMessage{ArtifactID: flowID, Body: body, ContentType: "text/plain; charset=utf-8",
		Headers: map[string]string{testtools.ScriptCaseHeader: string(spec)}, MaxBytes: -1})
	if sent == nil || sent.HTTPStatus == 0 || sent.HTTPStatus == http.StatusUnauthorized || sent.HTTPStatus == http.StatusForbidden {
		msg := "sending the case failed"
		if err != nil {
			msg = err.Error()
		}
		if sent != nil && (sent.HTTPStatus == http.StatusUnauthorized || sent.HTTPStatus == http.StatusForbidden) {
			msg = fmt.Sprintf("HTTP %d from the test flow: the runtime client needs the role %s (--role)", sent.HTTPStatus, testtools.ScriptRoleParameter)
		}
		cr.Status, cr.Error, cr.ErrorCategory = testrun.StatusError, msg, "error"
		return cr
	}
	answer := []byte(sent.Response.Text)
	if sent.Response.Base64 != "" {
		answer, _ = base64.StdEncoding.DecodeString(sent.Response.Base64)
	}
	oc := testrun.ScriptOutcome{DurationMs: time.Since(start).Milliseconds()}
	if sent.HTTPStatus >= 300 {
		text := testrun.FailedMessageError(ctx, exe, sent.MessageGuid)
		if text == "" {
			text = strings.TrimSpace(string(answer))
		}
		class, message := thrownBy(text)
		oc.Throws = &struct {
			Class   string `json:"class"`
			Message string `json:"message"`
		}{class, message}
	} else {
		var env onlineEnvelope
		if err := json.Unmarshal(answer, &env); err != nil {
			cr.Status, cr.Error, cr.ErrorCategory = testrun.StatusError, "the test flow's answer is not its JSON result: "+firstLine(string(answer)), "error"
			return cr
		}
		oc.Body, oc.Headers, oc.Properties = env.Body, env.Headers, env.Properties
	}
	c.Expect.MaxDuration = "" // the round trip, not the script
	cr = testrun.ScriptCaseResult(c, oc)
	cr.MessageGuid = sent.MessageGuid
	return cr
}

var reThrown = regexp.MustCompile(`((?:[A-Za-z_$][\w$]*\.)+[A-Z][\w$]*(?:Exception|Error|Throwable)): ?`)

// wrappers are exceptions of the runtime around the script's own.
var wrappers = map[string]bool{
	"com.sap.it.rt.adapter.http.api.exception.HttpResponseException": true,
	"javax.script.ScriptException":                                   true,
	"org.apache.camel.CamelExchangeException":                        true,
	"org.apache.camel.RuntimeCamelException":                         true,
}

// thrownBy finds the script's exception in a message log's error text:
// "...HttpResponseException: An internal server error occured:
// java.lang.IllegalArgumentException: OrderNo missing@ line 4 in
// Keys.groovy." gives java.lang.IllegalArgumentException and "OrderNo
// missing".
func thrownBy(text string) (class, message string) {
	ms := reThrown.FindAllStringSubmatchIndex(text, -1)
	pick := -1
	for i, m := range ms {
		if !wrappers[text[m[2]:m[3]]] {
			pick = i
		}
	}
	if pick < 0 {
		if len(ms) == 0 {
			return "", strings.TrimSpace(text)
		}
		pick = len(ms) - 1
	}
	m := ms[pick]
	class = text[m[2]:m[3]]
	rest := text[m[1]:]
	if i := strings.Index(rest, "\nThe MPL ID"); i >= 0 {
		rest = rest[:i]
	}
	if i := regexp.MustCompile(`@ line \d+ in [^\s]+\.?\s*$`).FindStringIndex(rest); i != nil {
		rest = rest[:i[0]]
	}
	return class, strings.TrimSpace(rest)
}
