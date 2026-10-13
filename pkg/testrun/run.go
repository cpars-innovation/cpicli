package testrun

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// Status is the outcome of a case.
type Status string

const (
	StatusPassed  Status = "passed"
	StatusFailed  Status = "failed"  // an expectation did not hold
	StatusError   Status = "error"   // the case could not be run (usage, auth, tenant, configuration)
	StatusSkipped Status = "skipped" // skip is set
)

// CaseResult is the outcome of one case.
type CaseResult struct {
	Flow        string `json:"flow"`
	Case        string `json:"case"`
	File        string `json:"file,omitempty"`
	Description string `json:"description,omitempty"`
	Status      Status `json:"status"`
	DurationMs  int64  `json:"durationMs"`
	MessageGuid string `json:"messageGuid,omitempty"`
	HTTPStatus  int    `json:"httpStatus,omitempty"`
	// MessageStatus is the final status of the message, when the case
	// waited for it.
	MessageStatus string `json:"messageStatus,omitempty"`
	// SkipReason is the case's skip.
	SkipReason string `json:"skipReason,omitempty"`
	// Error and ErrorCategory say why a case could not be run (status error).
	Error         string    `json:"error,omitempty"`
	ErrorCategory string    `json:"errorCategory,omitempty"`
	Failures      []Failure `json:"failures,omitempty"`
	// Diagnostics are attached to failed cases (and errors after a send).
	Diagnostics *Diagnostics `json:"diagnostics,omitempty"`
	code        int
}

// Diagnostics locate a failure: the message, its last error, the failing
// step and the message path across flows.
type Diagnostics struct {
	MessageGuid   string `json:"messageGuid,omitempty"`
	MessageStatus string `json:"messageStatus,omitempty"`
	// LastError is the message's error text (or the response of a failed call).
	LastError string `json:"lastError,omitempty"`
	// ErrorInformation names the model step (.iflw element ID) that failed
	// last; FailedStep is the first step that reported an error.
	ErrorInformation *cpi.ErrorInformation `json:"errorInformation,omitempty"`
	FailedStep       *cpi.MessageRunStep   `json:"failedStep,omitempty"`
	// TraceRoot is the root of the message's path across flows (same
	// correlation ID); FirstFailure its earliest failed message.
	TraceRoot    *ops.TraceNode    `json:"traceRoot,omitempty"`
	FirstFailure *ops.TraceFailure `json:"firstFailure,omitempty"`
	// Warnings list diagnostics that could not be read.
	Warnings []string `json:"warnings,omitempty"`
}

// ConfigChange is what --apply-config did for one flow: the keys set to
// the test values and restored afterwards (values are never reported).
type ConfigChange struct {
	Flow      string   `json:"flow"`
	Applied   []string `json:"applied"`
	Unchanged []string `json:"unchanged,omitempty"`
	Restored  []string `json:"restored"`
	// Deploys counts the redeployments (apply and restore).
	Deploys      int    `json:"deploys"`
	Error        string `json:"error,omitempty"`
	RestoreError string `json:"restoreError,omitempty"`
}

// Result is the outcome of a test run.
type Result struct {
	Passed  int          `json:"passed"`
	Failed  int          `json:"failed"`
	Errors  int          `json:"errors"`
	Skipped int          `json:"skipped"`
	Cases   []CaseResult `json:"cases"`
	// Config lists the configuration changes of --apply-config.
	Config []ConfigChange `json:"config,omitempty"`
	// Warnings are notes about the run, e.g. a config.yaml whose values
	// differ from the tenant when the run did not apply it.
	Warnings   []string  `json:"warnings,omitempty"`
	StartedAt  time.Time `json:"startedAt"`
	DurationMs int64     `json:"durationMs"`
}

// Options configure Run.
type Options struct {
	// Parallel is the number of cases run at the same time (default 1).
	// Cases with stub_calls always run alone.
	Parallel int
	// Progress receives each case result when it is final (one call at a
	// time).
	Progress func(CaseResult)
	// Harness is the test harness flow for process_direct_address (default
	// ops.DefaultHarnessID).
	Harness string
	// StubCalls reads the receiver stub's calls (default ops.StubCalls).
	StubCalls func(ctx context.Context, exe *httpclnt.HTTPExecuter, since time.Time) ([]ops.StubCall, error)
	// Now is the clock (default time.Now).
	Now func() time.Time
	// ApplyConfig sets each flow's config.yaml values before its cases,
	// redeploys it and restores the previous values afterwards (also on
	// failure or cancellation).
	ApplyConfig bool
	// Tier and AllowProduction feed the production guard (ops.GuardProduction).
	Tier            string
	AllowProduction bool
	// PollInterval is the interval of message log and stub polls (default 5s).
	PollInterval time.Duration
	// DeployInterval and DeployMaxChecks poll the redeployments of
	// ApplyConfig (default 10s, 30).
	DeployInterval  time.Duration
	DeployMaxChecks int
	// StubWait is how long a case waits for expected stub calls after its
	// message is final (default 10s); StubClockSkew the tolerance between
	// this machine's and the tenant's clock (default 5s).
	StubWait      time.Duration
	StubClockSkew time.Duration
}

func (o *Options) defaults() {
	o.Parallel = max(o.Parallel, 1)
	o.Harness = cmp.Or(o.Harness, ops.DefaultHarnessID)
	if o.StubCalls == nil {
		o.StubCalls = ops.StubCalls
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	o.PollInterval = cmp.Or(o.PollInterval, 5*time.Second)
	o.DeployInterval = cmp.Or(o.DeployInterval, 10*time.Second)
	o.DeployMaxChecks = cmp.Or(o.DeployMaxChecks, 30)
	o.StubWait = cmp.Or(o.StubWait, 10*time.Second)
	o.StubClockSkew = cmp.Or(o.StubClockSkew, 5*time.Second)
}

// runner is one Run.
type runner struct {
	exe    *httpclnt.HTTPExecuter
	newExe ops.EndpointExecuterFunc
	o      Options

	mu       sync.Mutex // Progress, lastStub
	lastStub time.Time  // end of the last stub case: later cases ignore earlier calls
}

// Run runs the cases: it sends each case's message with
// ops.SendTestMessage, waits for the processing log and checks the
// expectations. A case that fails gets diagnostics. The error is the
// production guard's or a usage error before anything ran; the outcome of the
// cases is in the result (see Result.Err).
func Run(ctx context.Context, exe *httpclnt.HTTPExecuter, newExe ops.EndpointExecuterFunc, cases []Case, o Options) (*Result, error) {
	o.defaults()
	if err := ops.GuardProduction(o.Tier, o.AllowProduction, "a test run"); err != nil {
		return nil, err
	}
	if exe == nil || newExe == nil {
		return nil, output.Usagef("a test run needs the tenant and runtime endpoint credentials")
	}
	exe = exe.WithContext(ctx)
	r := &runner{exe: exe, newExe: newExe, o: o}
	res := &Result{StartedAt: o.Now().UTC(), Cases: make([]CaseResult, len(cases))}

	var groups [][]int // indexes into cases
	if o.ApplyConfig {
		byFlow := map[string]int{}
		for i, c := range cases {
			g, ok := byFlow[c.Flow]
			if !ok {
				g = len(groups)
				byFlow[c.Flow] = g
				groups = append(groups, nil)
			}
			groups[g] = append(groups[g], i)
		}
	} else {
		all := make([]int, len(cases))
		for i := range cases {
			all[i] = i
		}
		groups = [][]int{all}
		res.Warnings = r.configWarnings(cases)
	}

	for _, g := range groups {
		if ctx.Err() != nil {
			break
		}
		if c := cases[g[0]]; o.ApplyConfig && (c.Config != nil || len(c.FlowConfig) > 0) && hasRunnable(cases, g) {
			// the flow's own values first, then the other flows (flows:);
			// restored in reverse order, also after a failure
			type target struct {
				flow   string
				values map[string]string
			}
			var targets []target
			if c.Config != nil {
				targets = append(targets, target{c.Flow, c.Config})
			}
			for _, f := range sortedKeys(c.FlowConfig) {
				targets = append(targets, target{f, c.FlowConfig[f]})
			}
			var restores []func()
			var changes []*ConfigChange
			var applyErr error
			for _, t := range targets {
				change, restore, err := r.applyConfig(ctx, t.flow, t.values)
				changes = append(changes, change)
				if restore != nil {
					restores = append(restores, restore)
				}
				if err != nil {
					applyErr = fmt.Errorf("applying %s (%s): %w", ConfigFile, t.flow, err)
					break
				}
			}
			if applyErr != nil {
				for _, i := range g {
					res.Cases[i] = r.errorResult(cases[i], applyErr)
					r.progress(res.Cases[i])
				}
			} else {
				r.runGroup(ctx, cases, g, res.Cases)
			}
			for i := len(restores) - 1; i >= 0; i-- {
				restores[i]()
			}
			for _, ch := range changes { // after the restores, which fill them in
				res.Config = append(res.Config, *ch)
			}
			continue
		}
		r.runGroup(ctx, cases, g, res.Cases)
	}
	for i := range res.Cases {
		if res.Cases[i].Status == "" { // cancelled before it ran
			res.Cases[i] = r.errorResult(cases[i], cmp.Or(ctx.Err(), context.Canceled))
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

func hasRunnable(cases []Case, g []int) bool {
	return slices.ContainsFunc(g, func(i int) bool { return cases[i].Skip == "" })
}

// runGroup runs the cases g: skipped ones at once, the others Parallel at a
// time, then the cases with stub_calls one by one.
func (r *runner) runGroup(ctx context.Context, cases []Case, g []int, out []CaseResult) {
	var normal, alone []int
	for _, i := range g {
		switch {
		case cases[i].Skip != "":
			out[i] = CaseResult{Flow: cases[i].Flow, Case: cases[i].Name, File: cases[i].File, Description: cases[i].Description,
				Status: StatusSkipped, SkipReason: cases[i].Skip}
			r.progress(out[i])
		case len(cases[i].Expect.StubCalls) > 0:
			alone = append(alone, i)
		default:
			normal = append(normal, i)
		}
	}
	slots := make(chan struct{}, r.o.Parallel)
	var wg sync.WaitGroup
	for _, i := range normal {
		if ctx.Err() != nil {
			break
		}
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			out[i] = r.runCase(ctx, cases[i])
			r.progress(out[i])
		})
	}
	wg.Wait()
	for _, i := range alone {
		if ctx.Err() != nil {
			break
		}
		out[i] = r.runCase(ctx, cases[i])
		r.progress(out[i])
	}
}

func (r *runner) progress(c CaseResult) {
	if r.o.Progress == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.o.Progress(c)
}

func (r *runner) errorResult(c Case, err error) CaseResult {
	code := output.ExitCode(err)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		code = exitcode.Error
	}
	return CaseResult{Flow: c.Flow, Case: c.Name, File: c.File, Description: c.Description, Status: StatusError,
		Error: err.Error(), ErrorCategory: categoryOf(code), code: code}
}

func categoryOf(code int) string {
	return cmp.Or(output.Category(code), "error")
}

// message builds the test message of a case.
func (r *runner) message(c Case) (ops.TestMessage, error) {
	q := c.Request
	m := ops.TestMessage{ArtifactID: c.Flow, URL: q.url(), Path: q.Path, Method: q.Method, ContentType: q.ContentType,
		Headers: q.Headers, ProcessDirectAddress: q.ProcessDirectAddress, Harness: r.o.Harness,
		PollInterval: r.o.PollInterval, Body: []byte(q.Body)}
	if q.BodyFile != "" {
		b, err := c.readFile(q.BodyFile)
		if err != nil {
			return m, err
		}
		m.Body = b
	}
	switch {
	case q.IDocFile != "":
		b, err := c.readFile(q.IDocFile)
		if err != nil {
			return m, err
		}
		m.IDoc = b
	case q.IDoc != "":
		m.IDoc = []byte(q.IDoc)
	}
	if q.OData != nil {
		m.OData = &ops.ODataRequest{Method: q.OData.Method, EntitySet: q.OData.EntitySet, Key: q.OData.Key, Query: q.OData.Query}
	}
	if c.Expect.needsLog() {
		m.Wait = cmp.Or(time.Duration(q.Wait), DefaultWait)
	}
	return m, nil
}

// observed reports whether a send error is an outcome to check rather than
// a reason the case could not run: the message failed or did not finish,
// or the endpoint answered with the status the case expects.
func observed(sent *ops.SentMessage, err error, e Expect) bool {
	if sent == nil {
		return false
	}
	switch output.ExitCode(err) {
	case exitcode.DeployFailed, exitcode.Timeout:
		return true
	}
	return e.HTTPStatus != 0 && sent.HTTPStatus == e.HTTPStatus
}

func (r *runner) runCase(ctx context.Context, c Case) CaseResult {
	start := r.o.Now()
	res := CaseResult{Flow: c.Flow, Case: c.Name, File: c.File, Description: c.Description}
	finish := func(cr CaseResult) CaseResult {
		cr.DurationMs = r.o.Now().Sub(start).Milliseconds()
		return cr
	}
	msg, err := r.message(c)
	if err != nil {
		return finish(r.errorResult(c, err))
	}
	sent, err := ops.SendTestMessage(ctx, r.exe, r.newExe, msg)
	elapsed := r.o.Now().Sub(start)
	if sent != nil {
		res.HTTPStatus, res.MessageGuid = sent.HTTPStatus, cmp.Or(sent.MessageGuid, sent.HarnessMessageGuid)
		if sent.Log != nil {
			res.MessageStatus = sent.Log.Status
		}
	}
	if err != nil && !observed(sent, err, c.Expect) {
		er := r.errorResult(c, err)
		er.HTTPStatus, er.MessageGuid, er.MessageStatus = res.HTTPStatus, res.MessageGuid, res.MessageStatus
		if sent != nil && res.MessageGuid != "" {
			er.Diagnostics = r.diagnose(ctx, sent)
		}
		return finish(er)
	}
	obs := &observation{sent: sent, sendErr: err, elapsed: elapsed}
	if len(c.Expect.StepsInclude) > 0 {
		if sent.MessageGuid == "" {
			obs.stepsErr = fmt.Errorf("no message GUID")
		} else {
			obs.steps, obs.stepsErr = ops.GetMessageSteps(r.exe, sent.MessageGuid)
		}
	}
	if len(c.Expect.StubCalls) > 0 {
		obs.stubs, obs.stubsErr = r.stubCalls(ctx, c, start)
	}
	res.Failures = check(c.Expect, obs)
	res.Status = StatusPassed
	if len(res.Failures) > 0 {
		res.Status = StatusFailed
		res.Diagnostics = r.diagnose(ctx, sent)
	}
	return finish(res)
}

// stubCalls reads the stub's calls since the case started (less the clock
// skew, but not before the previous stub case ended), polling until every
// matcher has its count or StubWait passed.
func (r *runner) stubCalls(ctx context.Context, c Case, start time.Time) ([]ops.StubCall, error) {
	r.mu.Lock()
	since := start.Add(-r.o.StubClockSkew)
	if r.lastStub.After(since) {
		since = r.lastStub
	}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.lastStub = r.o.Now()
		r.mu.Unlock()
	}()
	deadline := time.Now().Add(r.o.StubWait)
	for {
		calls, err := r.o.StubCalls(ctx, r.exe, since)
		if err != nil {
			return nil, err
		}
		calls = slices.DeleteFunc(calls, func(sc ops.StubCall) bool { return !sc.Time.IsZero() && sc.Time.Before(since) })
		if len(checkStubs(c.Expect.StubCalls, calls, nil)) == 0 || time.Now().Add(r.o.PollInterval).After(deadline) {
			return calls, nil
		}
		select {
		case <-ctx.Done():
			return calls, nil
		case <-time.After(r.o.PollInterval):
		}
	}
}

// diagnose reads what locates a failure; what cannot be read is a warning.
func (r *runner) diagnose(ctx context.Context, sent *ops.SentMessage) *Diagnostics {
	d := &Diagnostics{MessageGuid: sent.MessageGuid}
	if sent.HTTPStatus >= 300 {
		d.LastError = excerpt(sent.Response.Text)
	}
	guid := sent.MessageGuid
	if guid == "" {
		guid = sent.HarnessMessageGuid
		d.MessageGuid = guid
	}
	if guid == "" {
		d.Warnings = append(d.Warnings, "the endpoint returned no message GUID")
		return d
	}
	log := sent.Log
	if log == nil {
		l, err := ops.GetMessageLog(r.exe, guid, 4096)
		if err != nil {
			d.Warnings = append(d.Warnings, "message log: "+err.Error())
		}
		log = l
	}
	if log != nil {
		d.MessageStatus = log.Status
		d.ErrorInformation = log.ErrorInformation
		if log.ErrorText != "" {
			d.LastError = log.ErrorText
		}
	}
	if steps, err := ops.GetMessageSteps(r.exe, guid); err != nil {
		d.Warnings = append(d.Warnings, "steps: "+err.Error())
	} else {
		d.FailedStep = steps.FailedStep
	}
	tree, err := ops.MessagePathFor(ctx, r.exe, ops.MessagePathQuery{MessageGuid: guid, MaxMessages: 50})
	if err != nil {
		d.Warnings = append(d.Warnings, "trace tree: "+err.Error())
	} else {
		if len(tree.Roots) > 0 {
			d.TraceRoot = tree.Roots[0]
		}
		d.FirstFailure = tree.FirstFailure
	}
	return d
}

// Err is the outcome of the run as an error with an exit code (nil when
// every case that ran passed):
//
//   - a configuration that could not be restored: failed (5);
//   - any failed case: failed (5);
//   - cases that could not be run (status error) next to passed ones:
//     partial (7);
//   - only cases that could not be run: the most specific cause, in the
//     order auth (3), tenant HTTP (4), usage (2), failed (5, e.g. the
//     redeployment of --apply-config), timeout (6), error (1).
//
// Skipped cases count as neither.
func (r *Result) Err() error {
	var notRestored []string
	for _, c := range r.Config {
		if c.RestoreError != "" {
			notRestored = append(notRestored, fmt.Sprintf("%s: %s", c.Flow, c.RestoreError))
		}
	}
	if len(notRestored) > 0 {
		return output.Failed(fmt.Errorf("the test configuration could not be restored (%s); check the parameters on the tenant", strings.Join(notRestored, "; ")))
	}
	run := r.Passed + r.Failed + r.Errors
	switch {
	case r.Failed > 0:
		msg := fmt.Sprintf("%d of %d test case(s) failed", r.Failed, run)
		if r.Errors > 0 {
			msg += fmt.Sprintf(", %d could not be run", r.Errors)
		}
		return output.Failed(errors.New(msg))
	case r.Errors > 0 && r.Passed > 0:
		return output.Partial(fmt.Errorf("%d of %d test case(s) could not be run", r.Errors, run))
	case r.Errors > 0:
		order := []int{exitcode.Auth, exitcode.TenantHTTP, exitcode.Usage, exitcode.DeployFailed, exitcode.Timeout, exitcode.Error}
		best, first := len(order)-1, ""
		for _, c := range r.Cases {
			if c.Status != StatusError {
				continue
			}
			if first == "" {
				first = c.Error
			}
			if i := slices.Index(order, cmp.Or(c.code, codeOf(c.ErrorCategory))); i >= 0 && i < best {
				best = i
			}
		}
		code := order[best]
		return output.WithExitCode(fmt.Errorf("%d test case(s) could not be run: %s", r.Errors, first), code)
	}
	return nil
}

// codeOf maps an error category back to its exit code.
func codeOf(category string) int {
	for _, c := range []int{exitcode.Usage, exitcode.Auth, exitcode.TenantHTTP, exitcode.DeployFailed, exitcode.Timeout, exitcode.Partial, exitcode.Stopped} {
		if output.Category(c) == category {
			return c
		}
	}
	return exitcode.Error
}
