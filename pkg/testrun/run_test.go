package testrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixtures = "testdata/tests"

// demo is the mock tenant's demo landscape (dev tier, live) with receivers
// that never fail, the test harness deployed, and an endpoint executer.
func demo(t *testing.T) (*cpitest.Tenant, ops.EndpointExecuterFunc) {
	t.Helper()
	m := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(m, "dev", time.Now()))
	// receivers that never fail (admin API)
	resp, err := http.Get(m.URL() + "/_mock/systems")
	require.NoError(t, err)
	var systems map[string]map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&systems))
	resp.Body.Close()
	for name, s := range systems {
		s["failRate"] = 0
		b, _ := json.Marshal(s)
		req, _ := http.NewRequest(http.MethodPut, m.URL()+"/_mock/systems/"+name, bytes.NewReader(b))
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}
	// the test harness flow
	m.Artifacts[ops.DefaultHarnessID] = &cpitest.Artifact{Type: "Integration", DesignVersion: "1.0.0", Package: "Test",
		Runtime: &cpitest.Runtime{Version: "1.0.0", Status: "STARTED"}, EndpointURL: m.URL() + "/http/cpictl/test"}
	m.Inbound["/http/cpictl/test"] = &cpitest.Inbound{Artifact: ops.DefaultHarnessID, Harness: true}
	return m, func(endpoint string) (*httpclnt.HTTPExecuter, string, error) {
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, "", err
		}
		p := u.EscapedPath()
		if u.RawQuery != "" {
			p += "?" + u.RawQuery
		}
		return m.Executer().ForEndpoint(u.EscapedPath()), p, nil
	}
}

func fast(o Options) Options {
	o.PollInterval, o.DeployInterval, o.DeployMaxChecks = time.Millisecond, time.Millisecond, 50
	o.StubWait = 20 * time.Millisecond
	return o
}

func byName(t *testing.T, r *Result, flow, name string) CaseResult {
	t.Helper()
	for _, c := range r.Cases {
		if c.Flow == flow && c.Case == name {
			return c
		}
	}
	t.Fatalf("no result for %s/%s", flow, name)
	return CaseResult{}
}

// The fixtures run end to end on the mock tenant: an HTTPS flow, an IDoc
// flow and a ProcessDirect flow through the harness.
func TestRunFixturesOnMockTenant(t *testing.T) {
	m, newExe := demo(t)
	cases, err := Load(fixtures, Filter{})
	require.NoError(t, err)
	var progress []string
	var mu sync.Mutex
	res, err := Run(context.Background(), m.Executer(), newExe, cases, fast(Options{Parallel: 3, Progress: func(c CaseResult) {
		mu.Lock()
		defer mu.Unlock()
		progress = append(progress, c.Flow+"/"+c.Case+":"+string(c.Status))
	}}))
	require.NoError(t, err)
	for _, c := range res.Cases {
		assert.Contains(t, []Status{StatusPassed, StatusSkipped}, c.Status, "%s/%s: %+v %s", c.Flow, c.Case, c.Failures, c.Error)
	}
	assert.Equal(t, 4, res.Passed)
	assert.Equal(t, 1, res.Skipped)
	assert.NoError(t, res.Err())
	assert.Len(t, progress, 5)
	assert.Contains(t, progress, "Orders_In/later:skipped")

	idocCase := byName(t, res, "Orders_IDoc_In", "orders05")
	assert.Equal(t, 200, idocCase.HTTPStatus)
	assert.Equal(t, "COMPLETED", idocCase.MessageStatus)
	route := byName(t, res, "Orders_Route", "via-harness")
	assert.NotEmpty(t, route.MessageGuid)
	assert.Equal(t, "COMPLETED", route.MessageStatus)
	assert.Contains(t, res.Warnings[0], "Orders_IDoc_In: the tenant's values of ORDER_API_URL differ", "config.yaml without --apply-config")
	assert.NotContains(t, res.Warnings[0], "orders-api-test", "values are never reported")
}

// A failing receiver: the expectation fails and the diagnostics name the
// failing step, the error and the message path.
func TestRunFailureDiagnostics(t *testing.T) {
	m, newExe := demo(t)
	b := []byte(`{"match":"erp-*.example.com","failRate":1,"status":504,"error":"java.net.SocketTimeoutException: Read timed out calling {host}"}`)
	req, _ := http.NewRequest(http.MethodPut, m.URL()+"/_mock/systems/erp", bytes.NewReader(b))
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	cases, err := Load(fixtures, Filter{Flows: []string{"Orders_Route"}})
	require.NoError(t, err)
	res, err := Run(context.Background(), m.Executer(), newExe, cases, fast(Options{}))
	require.NoError(t, err)
	c := byName(t, res, "Orders_Route", "via-harness")
	require.Equal(t, StatusFailed, c.Status, c.Error)
	require.Len(t, c.Failures, 2)
	assert.Equal(t, Failure{Expectation: "http_status", Expected: "2xx", Actual: "500",
		Message: "HTTP status 500, expected 2xx: java.net.SocketTimeoutException: Read timed out calling erp-dev.example.com"}, c.Failures[0])
	assert.Equal(t, "message_status", c.Failures[1].Expectation)
	assert.Equal(t, "FAILED", c.Failures[1].Actual)
	require.NotNil(t, c.Diagnostics)
	assert.Contains(t, c.Diagnostics.LastError, "Read timed out")
	require.NotNil(t, c.Diagnostics.FailedStep)
	assert.Equal(t, "ServiceTask_0", c.Diagnostics.FailedStep.ModelStepID)
	require.NotNil(t, c.Diagnostics.TraceRoot)
	assert.Equal(t, ops.DefaultHarnessID, c.Diagnostics.TraceRoot.ArtifactID, "the path starts at the harness")
	require.NotNil(t, c.Diagnostics.FirstFailure)
	require.Len(t, c.Diagnostics.TraceRoot.Children, 1)
	assert.Equal(t, "Orders_Route", c.Diagnostics.TraceRoot.Children[0].ArtifactID)

	err = res.Err()
	assert.Equal(t, exitcode.DeployFailed, output.ExitCode(err))
	assert.Contains(t, err.Error(), "1 of 1 test case(s) failed")
}

// A negative case expects the failure: HTTP 500 with the receiver's error.
func TestRunNegativeCase(t *testing.T) {
	m, newExe := demo(t)
	b := []byte(`{"match":"erp-*.example.com","failRate":1,"status":504,"error":"HTTP 504 from {host}"}`)
	req, _ := http.NewRequest(http.MethodPut, m.URL()+"/_mock/systems/erp", bytes.NewReader(b))
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	c := Case{Flow: "Orders_Route", Name: "erp-down", Request: Request{ProcessDirectAddress: "/orders/route", Body: "<Order/>"},
		Expect: Expect{HTTPStatus: 500, MessageStatus: "FAILED", ErrorContains: StringList{"HTTP 504 from erp-dev.example.com"}}}
	res, err := Run(context.Background(), m.Executer(), newExe, []Case{c}, fast(Options{}))
	require.NoError(t, err)
	assert.Equal(t, StatusPassed, res.Cases[0].Status, "%+v %s", res.Cases[0].Failures, res.Cases[0].Error)
	assert.Nil(t, res.Cases[0].Diagnostics)
}

// Cases that cannot run are errors, with the most specific exit code.
func TestRunErrors(t *testing.T) {
	m, newExe := demo(t)
	cases := []Case{
		{Flow: "NoSuchFlow", Name: "a", Request: Request{Body: "x"}},
		{Flow: "Orders_In", Name: "b", Request: Request{Headers: map[string]string{"Authorization": "x"}}},
	}
	res, err := Run(context.Background(), m.Executer(), newExe, cases, fast(Options{}))
	require.NoError(t, err)
	assert.Equal(t, 2, res.Errors)
	assert.Equal(t, "usage", res.Cases[0].ErrorCategory)
	assert.Equal(t, exitcode.Usage, output.ExitCode(res.Err()))

	// next to a passed case: partial
	cases = append(cases, Case{Flow: "Orders_In", Name: "ok", Request: Request{Body: "<Order/>"}})
	res, err = Run(context.Background(), m.Executer(), newExe, cases, fast(Options{}))
	require.NoError(t, err)
	assert.Equal(t, 1, res.Passed)
	assert.Equal(t, exitcode.Partial, output.ExitCode(res.Err()))

	// auth wins over usage
	r := &Result{Errors: 2, Cases: []CaseResult{{Status: StatusError, ErrorCategory: "usage"}, {Status: StatusError, ErrorCategory: "auth"}}}
	assert.Equal(t, exitcode.Auth, output.ExitCode(r.Err()))
	assert.NoError(t, (&Result{Skipped: 1}).Err())
}

// The production guard refuses before anything is read or sent.
func TestRunProductionGuard(t *testing.T) {
	m, newExe := demo(t)
	before := len(m.Requests())
	_, err := Run(context.Background(), m.Executer(), newExe, []Case{{Flow: "Orders_In", Name: "a"}}, Options{Tier: "prod"})
	require.Error(t, err)
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
	assert.Contains(t, err.Error(), "production tenant")
	assert.Len(t, m.Requests(), before)

	res, err := Run(context.Background(), m.Executer(), newExe, []Case{{Flow: "Orders_In", Name: "a", Request: Request{Body: "<Order/>"}}},
		fast(Options{Tier: "prod", AllowProduction: true}))
	require.NoError(t, err)
	assert.Equal(t, 1, res.Passed)
}

// --apply-config sets the test values, redeploys, runs the cases and
// restores the previous values with a second redeployment.
func TestRunApplyConfig(t *testing.T) {
	m, newExe := demo(t)
	before := m.Artifacts["Orders_IDoc_In"].Parameters["ORDER_API_URL"]
	require.NotEqual(t, "https://orders-api-test.example.com/v1/orders", before)
	deploys := m.Artifacts["Orders_IDoc_In"].Deploys
	var during string
	cases, err := Load(fixtures, Filter{Flows: []string{"Orders_IDoc_In"}})
	require.NoError(t, err)
	res, err := Run(context.Background(), m.Executer(), newExe, cases, fast(Options{ApplyConfig: true, Progress: func(CaseResult) {
		during = m.Artifacts["Orders_IDoc_In"].Parameters["ORDER_API_URL"]
	}}))
	require.NoError(t, err)
	assert.Equal(t, 1, res.Passed, "%+v", res.Cases)
	assert.Equal(t, "https://orders-api-test.example.com/v1/orders", during)
	assert.Equal(t, before, m.Artifacts["Orders_IDoc_In"].Parameters["ORDER_API_URL"], "restored")
	assert.Equal(t, deploys+2, m.Artifacts["Orders_IDoc_In"].Deploys)
	require.Len(t, res.Config, 1)
	assert.Equal(t, ConfigChange{Flow: "Orders_IDoc_In", Applied: []string{"ORDER_API_URL"}, Restored: []string{"ORDER_API_URL"}, Deploys: 2}, res.Config[0])
	assert.Empty(t, res.Warnings)
	data, _ := json.Marshal(res)
	assert.NotContains(t, string(data), "orders-api-", "no parameter values in the result")
}

// The configuration is restored when the cases fail and when the run is
// cancelled.
func TestRunApplyConfigRestoresOnFailureAndCancel(t *testing.T) {
	m, newExe := demo(t)
	before := m.Artifacts["Orders_IDoc_In"].Parameters["ORDER_API_URL"]
	failing := Case{Flow: "Orders_IDoc_In", Name: "f", Config: map[string]string{"ORDER_API_URL": "https://orders-api-x.example.com"},
		Request: Request{IDoc: string(mustRead(t, fixtures+"/Orders_IDoc_In/orders05.xml"))}, Expect: Expect{HTTPStatus: 418}}
	res, err := Run(context.Background(), m.Executer(), newExe, []Case{failing}, fast(Options{ApplyConfig: true}))
	require.NoError(t, err)
	assert.Equal(t, 1, res.Failed)
	assert.Equal(t, before, m.Artifacts["Orders_IDoc_In"].Parameters["ORDER_API_URL"])
	assert.Equal(t, []string{"ORDER_API_URL"}, res.Config[0].Restored)

	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	res, err = Run(ctx, m.Executer(), newExe, []Case{failing}, fast(Options{ApplyConfig: true, Now: func() time.Time {
		if calls++; calls == 2 {
			cancel() // cancelled when the case starts, after the configuration was applied
		}
		return time.Now()
	}}))
	require.NoError(t, err)
	assert.Equal(t, 1, res.Errors)
	assert.Equal(t, before, m.Artifacts["Orders_IDoc_In"].Parameters["ORDER_API_URL"], "restored with a fresh context")
	assert.Equal(t, []string{"ORDER_API_URL"}, res.Config[0].Restored)

	// an unknown parameter: nothing is changed, the cases are errors
	unknown := failing
	unknown.Config = map[string]string{"NOPE": "x"}
	deploys := m.Artifacts["Orders_IDoc_In"].Deploys
	res, err = Run(context.Background(), m.Executer(), newExe, []Case{unknown}, fast(Options{ApplyConfig: true}))
	require.NoError(t, err)
	assert.Equal(t, 1, res.Errors)
	assert.Contains(t, res.Cases[0].Error, "NOPE")
	assert.Equal(t, deploys, m.Artifacts["Orders_IDoc_In"].Deploys)
	assert.Equal(t, exitcode.Usage, output.ExitCode(res.Err()))
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}

// Stub assertions: the calls since the case started are matched; cases with
// stub_calls run alone.
func TestRunStubCalls(t *testing.T) {
	m, newExe := demo(t)
	var mu sync.Mutex
	running, maxRunning := 0, 0
	var sinces []time.Time
	stub := func(ctx context.Context, exe *httpclnt.HTTPExecuter, since time.Time) ([]ops.StubCall, error) {
		sinces = append(sinces, since)
		return []ops.StubCall{
			{Time: time.Now(), Rule: "erp", Method: "POST", Path: "erp/orders", Body: "<Order><OrderNo>1</OrderNo></Order>", Headers: map[string]string{"Content-Type": "application/xml"}},
			{Time: time.Now(), Rule: "finance", Method: "PUT", Path: "finance/invoices/1"},
			{Time: time.Now().Add(-time.Hour), Rule: "erp", Method: "POST", Path: "erp/old"}, // before the case
		}, nil
	}
	track := func(c CaseResult) {}
	cases := []Case{
		{Flow: "Orders_In", Name: "stub-ok", Request: Request{Body: "<Order/>"}, Expect: Expect{StubCalls: []StubMatcher{
			{Rule: "erp", Method: "post", PathContains: "orders", BodyContains: StringList{"<OrderNo>1</OrderNo>"}, Header: map[string]string{"content-type": "application/xml"}},
			{PathContains: "finance", Count: intp(1)},
			{Rule: "erp", PathContains: "old", Count: intp(0)},
		}}},
		{Flow: "Orders_In", Name: "stub-missing", Request: Request{Body: "<Order/>"}, Expect: Expect{StubCalls: []StubMatcher{{Rule: "crm"}}}},
		{Flow: "Orders_In", Name: "stub-twice", Request: Request{Body: "<Order/>"}, Expect: Expect{StubCalls: []StubMatcher{{Rule: "erp", Count: intp(2)}}}},
		{Flow: "Orders_In", Name: "plain-1", Request: Request{Body: "<Order/>"}},
		{Flow: "Orders_In", Name: "plain-2", Request: Request{Body: "<Order/>"}},
	}
	o := fast(Options{Parallel: 4, StubCalls: stub, Progress: track})
	inner := o.StubCalls
	o.StubCalls = func(ctx context.Context, exe *httpclnt.HTTPExecuter, since time.Time) ([]ops.StubCall, error) {
		mu.Lock()
		running++
		maxRunning = max(maxRunning, running)
		mu.Unlock()
		defer func() { mu.Lock(); running--; mu.Unlock() }()
		return inner(ctx, exe, since)
	}
	res, err := Run(context.Background(), m.Executer(), newExe, cases, o)
	require.NoError(t, err)
	assert.Equal(t, StatusPassed, byName(t, res, "Orders_In", "stub-ok").Status, "%+v", byName(t, res, "Orders_In", "stub-ok").Failures)
	missing := byName(t, res, "Orders_In", "stub-missing")
	require.Equal(t, StatusFailed, missing.Status)
	assert.Equal(t, "stub_calls[0]", missing.Failures[0].Expectation)
	assert.Contains(t, missing.Failures[0].Message, "0 stub call(s) with rule crm, expected 1")
	assert.Equal(t, StatusFailed, byName(t, res, "Orders_In", "stub-twice").Status)
	assert.Equal(t, 1, maxRunning)
	for i := 1; i < len(sinces); i++ {
		assert.False(t, sinces[i].Before(sinces[i-1]), "a later stub case does not look before the previous one")
	}

	// the stub is not available: the expectation fails with the reason
	res, err = Run(context.Background(), m.Executer(), newExe, cases[:1], fast(Options{StubCalls: func(context.Context, *httpclnt.HTTPExecuter, time.Time) ([]ops.StubCall, error) {
		return nil, errors.New("stub not deployed")
	}}))
	require.NoError(t, err)
	assert.Contains(t, res.Cases[0].Failures[0].Message, "stub not deployed")
}

func intp(i int) *int { return &i }

// Cases run in parallel up to Parallel.
func TestRunParallel(t *testing.T) {
	m, newExe := demo(t)
	var cases []Case
	for i := range 6 {
		cases = append(cases, Case{Flow: "Orders_In", Name: fmt.Sprint(i), Request: Request{Body: "<Order/>"}})
	}
	res, err := Run(context.Background(), m.Executer(), newExe, cases, fast(Options{Parallel: 3}))
	require.NoError(t, err)
	assert.Equal(t, 6, res.Passed)
	for i, c := range res.Cases {
		assert.Equal(t, fmt.Sprint(i), c.Case, "results keep the order of the cases")
	}
}

func TestRunNeedsExecuters(t *testing.T) {
	_, err := Run(context.Background(), nil, nil, nil, Options{})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
}

func TestBodyFileRelativeToCase(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "F"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "F", "b.json"), []byte(`{"a":1}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "F", "c.yaml"), []byte("request: {body_file: b.json}\n"), 0o644))
	cases, err := Load(dir, Filter{})
	require.NoError(t, err)
	r := &runner{o: Options{Harness: "H"}}
	msg, err := r.message(cases[0])
	require.NoError(t, err)
	assert.Equal(t, `{"a":1}`, string(msg.Body))
	assert.Equal(t, "F", msg.ArtifactID)
	assert.Zero(t, msg.Wait, "no expectation needs the log: no wait")

	cases[0].Expect.MessageStatus = "COMPLETED"
	msg, _ = r.message(cases[0])
	assert.Equal(t, DefaultWait, msg.Wait)
	cases[0].Request.Wait = Duration(5 * time.Second)
	msg, _ = r.message(cases[0])
	assert.Equal(t, 5*time.Second, msg.Wait)
}

// Phases are reported in order; a restore that failed can be retried in
// this process (Result.RetryRestore).
func TestRunPhasesAndRetryRestore(t *testing.T) {
	m, newExe := demo(t)
	flow := m.Artifacts["Orders_IDoc_In"]
	before := flow.Parameters["ORDER_API_URL"]
	cases, err := Load(fixtures, Filter{Flows: []string{"Orders_IDoc_In"}})
	require.NoError(t, err)
	var events []string
	res, err := Run(context.Background(), m.Executer(), newExe, cases, fast(Options{ApplyConfig: true,
		Phase: func(e PhaseEvent) {
			name := e.Phase + ":" + e.State
			if e.Case != "" {
				name = e.Case + "/" + name
			}
			events = append(events, name)
			assert.False(t, e.At.IsZero())
		},
		// the restore fails: the tenant refuses the update
		Progress: func(CaseResult) { flow.ConfigUpdateStatus = http.StatusInternalServerError },
	}))
	require.NoError(t, err)
	assert.Equal(t, 1, res.Passed, "%+v", res.Cases)
	c := cases[0].Name
	assert.Equal(t, []string{
		"apply_config:started", "deploy:started", "deploy:done", "apply_config:done",
		c + "/send:started", c + "/send:done", c + "/wait_log:started", c + "/wait_log:done",
		"restore_config:started", "restore_config:failed",
	}, events)
	require.Len(t, res.Config, 1)
	assert.NotEmpty(t, res.Config[0].RestoreError)
	assert.Equal(t, exitcode.DeployFailed, output.ExitCode(res.Err()))
	assert.NotEqual(t, before, flow.Parameters["ORDER_API_URL"], "not restored yet")

	// the tenant accepts updates again: retry
	flow.ConfigUpdateStatus = 0
	events = nil
	require.NoError(t, res.RetryRestore(context.Background()))
	assert.Equal(t, before, flow.Parameters["ORDER_API_URL"], "restored")
	assert.Empty(t, res.Config[0].RestoreError)
	assert.Equal(t, []string{"ORDER_API_URL"}, res.Config[0].Restored)
	assert.Equal(t, []string{"restore_config:started", "deploy:started", "deploy:done", "restore_config:done"}, events)
	assert.NoError(t, res.RetryRestore(context.Background()), "nothing left to retry")
}
