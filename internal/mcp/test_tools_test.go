package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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

// testsRoot is a server root with the testrun fixtures in .cpi/tests.
func testsRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join("..", "..", "pkg", "testrun", "testdata", "tests")
	require.NoError(t, filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		target := filepath.Join(root, ".cpi", "tests", rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	}))
	return root
}

func setMockSystem(t *testing.T, m *cpitest.Tenant, name, spec string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, m.URL()+"/_mock/systems/"+name, bytes.NewReader([]byte(spec)))
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// testsConfig is the demo landscape (receivers that never fail, harness
// deployed) and a server config for it.
func testsConfig(t *testing.T, root string) (*cpitest.Tenant, Config) {
	t.Helper()
	m := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(m, "dev", time.Now()))
	for name, match := range map[string]string{"erp": "erp-*.example.com", "finance": "finance-*.example.com", "order-api": "orders-api-*.example.com",
		"partner-sftp": "sftp.partner.example.com", "returns": "returns-*.example.com"} {
		setMockSystem(t, m, name, `{"match":"`+match+`","failRate":0}`)
	}
	m.Artifacts[ops.DefaultHarnessID] = &cpitest.Artifact{Type: "Integration", DesignVersion: "1.0.0",
		Runtime: &cpitest.Runtime{Version: "1.0.0", Status: "STARTED"}, EndpointURL: m.URL() + "/http/cpictl/test"}
	m.Inbound["/http/cpictl/test"] = &cpitest.Inbound{Artifact: ops.DefaultHarnessID, Harness: true}
	newExe := func(endpoint string) (*httpclnt.HTTPExecuter, string, error) {
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, "", err
		}
		return m.Executer().ForEndpoint(u.EscapedPath()), u.EscapedPath(), nil
	}
	return m, Config{Exe: m.Executer(), Root: root, PollInterval: time.Millisecond, MaxChecks: 50, LogPollInterval: time.Millisecond, NewEndpointExecuter: newExe}
}

func TestListTestsTool(t *testing.T) {
	root := testsRoot(t)
	m, cfg := testsConfig(t, root)
	before := len(m.Requests())
	resp := sessionWith(t, cfg,
		call(1, "list_tests", map[string]any{}),
		call(2, "list_tests", map[string]any{"flow": "Orders_In", "tags": []string{"smoke"}}),
		call(3, "list_tests", map[string]any{"dir": "../outside"}),
	)
	res := toolResult(t, resp["1"])
	require.False(t, res.IsError, res.Content[0].Text)
	l := res.StructuredContent.Result.(map[string]any)
	assert.EqualValues(t, 5, l["cases"])
	flows := l["flows"].([]any)
	first := flows[0].(map[string]any)
	assert.Equal(t, "Orders_IDoc_In", first["flow"])
	assert.Equal(t, []any{"ORDER_API_URL"}, first["configKeys"])
	assert.Equal(t, ".cpi/tests/Orders_IDoc_In/orders05.yaml", first["cases"].([]any)[0].(map[string]any)["file"], "root-relative")

	res = toolResult(t, resp["2"])
	require.False(t, res.IsError, res.Content[0].Text)
	assert.EqualValues(t, 2, res.StructuredContent.Result.(map[string]any)["cases"])

	res = toolResult(t, resp["3"])
	assert.True(t, res.IsError)
	assert.Equal(t, "usage", res.StructuredContent.ErrorCategory)
	assert.Contains(t, res.StructuredContent.Error, "outside the server root")
	assert.Len(t, m.Requests(), before, "local only")
}

func TestRunTestsTool(t *testing.T) {
	root := testsRoot(t)
	m, cfg := testsConfig(t, root)
	srv := NewServer("cpicli", "test", Instructions, Tools(cfg))
	msgs := serveLines(t, srv, callWithToken(1, "run_tests", map[string]any{"parallel": 2}, "tok"))
	progress := progressBefore(t, msgs, "1", `"tok"`)
	require.Len(t, progress, 5, "one notification per case")
	assert.EqualValues(t, 5, progress[4].Total)
	var r toolCallResult
	for _, msg := range msgs {
		if string(msg.ID) == "1" {
			require.NoError(t, json.Unmarshal(msg.Result, &r))
		}
	}
	require.False(t, r.IsError, r.Content[0].Text)
	res := r.StructuredContent.Result.(map[string]any)
	assert.EqualValues(t, 4, res["passed"])
	assert.EqualValues(t, 1, res["skipped"])
	c := res["cases"].([]any)[0].(map[string]any)
	assert.Equal(t, ".cpi/tests/Orders_IDoc_In/orders05.yaml", c["file"], "root-relative")

	// a failing receiver: errorCategory failed, with failures and diagnostics
	setMockSystem(t, m, "erp", `{"match":"erp-*.example.com","failRate":1,"status":504,"error":"Read timed out calling {host}"}`)
	resp := sessionWith(t, cfg,
		call(1, "run_tests", map[string]any{"flow": "Orders_Route", "case": "via-harness"}),
		call(2, "run_tests", map[string]any{"flow": "Nope"}),
		call(3, "run_tests", map[string]any{"dir": "/etc"}),
	)
	r = toolResult(t, resp["1"])
	assert.True(t, r.IsError)
	assert.Equal(t, "failed", r.StructuredContent.ErrorCategory)
	res = r.StructuredContent.Result.(map[string]any)
	assert.EqualValues(t, 1, res["failed"])
	c = res["cases"].([]any)[0].(map[string]any)
	assert.NotEmpty(t, c["failures"])
	assert.Equal(t, "ServiceTask_0", c["diagnostics"].(map[string]any)["failedStep"].(map[string]any)["modelStepId"])

	assert.Equal(t, "usage", toolResult(t, resp["2"]).StructuredContent.ErrorCategory, "no case matches")
	assert.Equal(t, "usage", toolResult(t, resp["3"]).StructuredContent.ErrorCategory, "outside the root")
}

// apply_config sets the flow's config.yaml for the run and restores it.
func TestRunTestsToolApplyConfig(t *testing.T) {
	root := testsRoot(t)
	m, cfg := testsConfig(t, root)
	before := m.Artifacts["Orders_IDoc_In"].Parameters["ORDER_API_URL"]
	resp := sessionWith(t, cfg, call(1, "run_tests", map[string]any{"flow": "Orders_IDoc_In", "apply_config": true}))
	r := toolResult(t, resp["1"])
	require.False(t, r.IsError, r.Content[0].Text)
	res := r.StructuredContent.Result.(map[string]any)
	change := res["config"].([]any)[0].(map[string]any)
	assert.Equal(t, []any{"ORDER_API_URL"}, change["applied"])
	assert.Equal(t, []any{"ORDER_API_URL"}, change["restored"])
	assert.Equal(t, before, m.Artifacts["Orders_IDoc_In"].Parameters["ORDER_API_URL"])
}

// On a production tier run_tests is refused; only the server's start
// option lifts it.
func TestRunTestsRefusedOnProduction(t *testing.T) {
	root := testsRoot(t)
	m, cfg := testsConfig(t, root)
	cfg.Tier = "prod"
	var run Tool
	for _, tl := range Tools(cfg) {
		if tl.Name == "run_tests" {
			run = tl
		}
	}
	before := len(m.Requests())
	_, err := run.Handler(context.Background(), json.RawMessage(`{}`))
	require.Error(t, err)
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
	assert.Contains(t, err.Error(), "production tenant")
	assert.Len(t, m.Requests(), before)

	cfg.AllowProduction = true
	for _, tl := range Tools(cfg) {
		if tl.Name == "run_tests" {
			run = tl
		}
	}
	_, err = run.Handler(context.Background(), json.RawMessage(`{"flow":"Orders_In","tags":["smoke"]}`))
	assert.NoError(t, err)
}

// run_tests counts in the loop like send_test_message: the same failure
// twice reaches same_error_limit.
func TestRunTestsCountsInLoop(t *testing.T) {
	root := testsRoot(t)
	m, cfg := testsConfig(t, root)
	setMockSystem(t, m, "erp", `{"match":"erp-*.example.com","failRate":1,"status":504,"error":"Read timed out calling {host}"}`)
	srv := NewServer("cpicli", "test", Instructions, NewLedger(root).Wrap(Tools(cfg)))
	n := 0
	callTool := func(tool string, args any) ToolResult {
		n++
		resp := serve(t, srv, call(n, tool, args))
		return toolResult(t, resp[jsonID(n)]).StructuredContent
	}
	start := callTool("loop_start", map[string]any{"goal": "fix the route"})
	require.True(t, start.OK, start.Error)
	id := start.Result.(map[string]any)["loopId"].(string)
	args := map[string]any{"flow": "Orders_Route"}
	assert.Equal(t, "failed", callTool("run_tests", args).ErrorCategory)
	st := callTool("loop_status", map[string]any{"loop_id": id}).Result.(map[string]any)
	assert.Len(t, st["errors"], 1)
	assert.Equal(t, "failed", callTool("run_tests", args).ErrorCategory, "same failure again")
	third := callTool("run_tests", args)
	assert.Equal(t, "stopped", third.ErrorCategory)
	assert.Contains(t, third.Error, "same error")

	// a passing run in another loop counts no failure
	callTool("loop_end", map[string]any{"loop_id": id, "outcome": "stopped"})
	setMockSystem(t, m, "erp", `{"match":"erp-*.example.com","failRate":0}`)
	start = callTool("loop_start", map[string]any{"goal": "again"})
	id = start.Result.(map[string]any)["loopId"].(string)
	assert.True(t, callTool("run_tests", args).OK)
	st = callTool("loop_status", map[string]any{"loop_id": id}).Result.(map[string]any)
	assert.Empty(t, st["errors"])
	assert.EqualValues(t, 1, st["steps"])
}

func jsonID(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
