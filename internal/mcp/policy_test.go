package mcp

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// policyEnv is a loop environment whose ledger has a policy.
func policyEnv(t *testing.T, policy LoopPolicy) *loopEnv {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"Orders": {Type: "Integration", DesignVersion: "1"}})
	root := t.TempDir()
	ledger := NewLedger(root)
	ledger.Policy = policy
	tools := ledger.Wrap(Tools(Config{Exe: mock.Executer(), Root: root, PollInterval: time.Millisecond, MaxChecks: 1}))
	return &loopEnv{t: t, mock: mock, root: root, srv: NewServer("cpicli", "test", Instructions, tools)}
}

// The sequence of docs/mcp.md: a dry run does not use up max_deploys.
func TestLoopDeployDryRunDoesNotCount(t *testing.T) {
	e := newLoopEnv(t)
	start := e.call("loop_start", map[string]any{"goal": "probe", "max_deploys": 1})
	require.True(t, start.OK, start.Error)
	id := start.Result.(map[string]any)["loopId"].(string)

	dry := e.call("deploy", map[string]any{"artifact_ids": []string{"Orders"}, "dry_run": true})
	require.True(t, dry.OK, dry.Error)
	real := e.call("deploy", map[string]any{"artifact_ids": []string{"Orders"}})
	assert.NotEqual(t, "stopped", real.ErrorCategory, real.Error)
	assert.Equal(t, 1, e.mock.Count("POST /api/v1/DeployIntegrationDesigntimeArtifact"))

	st := e.call("loop_status", map[string]any{"loop_id": id}).Result.(map[string]any)
	assert.EqualValues(t, 1, st["deploys"])
	assert.Equal(t, "stopped", e.call("deploy", map[string]any{"artifact_ids": []string{"Orders"}}).ErrorCategory, "the second real deploy is over the limit")
}

func TestLoopRequired(t *testing.T) {
	e := policyEnv(t, LoopPolicy{Required: true})
	refused := e.call("deploy", map[string]any{"artifact_ids": []string{"Orders"}})
	assert.False(t, refused.OK)
	assert.Equal(t, "loop_required", refused.ErrorCategory)
	assert.Equal(t, exitcode.Usage, refused.ExitCode)
	assert.Contains(t, refused.Error, "loop_start")
	assert.Equal(t, 0, e.mock.Count("POST "), "nothing reached the tenant")

	assert.True(t, e.call("list_artifacts", map[string]any{"package_id": "P"}).ErrorCategory != "loop_required", "read tools work without a loop")
	assert.True(t, e.call("bump_versions", map[string]any{"dir": "x"}).ErrorCategory != "loop_required", "local tools work without a loop")

	start := e.call("loop_start", map[string]any{"goal": "g"})
	require.True(t, start.OK, start.Error)
	assert.NotEqual(t, "loop_required", e.call("deploy", map[string]any{"artifact_ids": []string{"Orders"}}).ErrorCategory)
	assert.Equal(t, 1, e.mock.Count("POST /api/v1/DeployIntegrationDesigntimeArtifact"))

	id := start.Result.(map[string]any)["loopId"].(string)
	require.True(t, e.call("loop_end", map[string]any{"loop_id": id, "outcome": "done"}).OK)
	assert.Equal(t, "loop_required", e.call("deploy", map[string]any{"artifact_ids": []string{"Orders"}}).ErrorCategory, "a closed loop does not count")
}

func TestLoopCaps(t *testing.T) {
	e := policyEnv(t, LoopPolicy{Caps: LoopLimits{MaxDeploys: 3, WallClockMinutes: 30}})
	over := e.call("loop_start", map[string]any{"goal": "g", "max_deploys": 5})
	assert.Equal(t, "usage", over.ErrorCategory)
	assert.Contains(t, over.Error, "max_deploys 5 exceeds this server's cap of 3 (--loop-max-deploys)")

	start := e.call("loop_start", map[string]any{"goal": "g", "max_deploys": 2})
	require.True(t, start.OK, start.Error)
	limits := start.Result.(map[string]any)["limits"].(map[string]any)
	assert.EqualValues(t, 2, limits["maxDeploys"], "below the cap is fine")
	assert.EqualValues(t, 30, limits["wallClockMinutes"], "the default of 60 is lowered to the cap")
	assert.EqualValues(t, DefaultLoopLimits.MaxIterations, limits["maxIterations"], "no cap")

	idx := slices.IndexFunc(e.srv.tools, func(t Tool) bool { return t.Name == "loop_start" })
	schema := e.srv.tools[idx].InputSchema["properties"].(map[string]any)
	assert.Equal(t, 3, schema["max_deploys"].(map[string]any)["maximum"])
	assert.Equal(t, 50, schema["max_iterations"].(map[string]any)["maximum"])

	assert.Contains(t, LoopPolicyInstructions(LoopPolicy{Required: true, Caps: LoopLimits{MaxDeploys: 3}}), "max_deploys 3")
	assert.Empty(t, LoopPolicyInstructions(LoopPolicy{}))
}

// ledgerAt returns a ledger with an open loop started at start.
func ledgerAt(t *testing.T, start time.Time) *Ledger {
	l := NewLedger(t.TempDir())
	l.now = func() time.Time { return start }
	_, err := l.start("diagnose", LoopLimits{})
	require.NoError(t, err)
	l.now = func() time.Time { return start.Add(time.Minute) }
	return l
}

func failedLogs(artifact string, end time.Time) *ops.MessageLogList {
	return &ops.MessageLogList{Total: 1, Logs: []ops.MessageLog{{MessageGuid: "M1", Status: "FAILED", ArtifactID: artifact,
		LogStart: &end, LogEnd: &end, ErrorText: "mapping failed"}}}
}

// Historic failures read while diagnosing do not stop the loop; failures
// after loop_start, or of artifacts the loop touched, do.
func TestLoopCountsOnlyItsFailures(t *testing.T) {
	start := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	args := json.RawMessage(`{"statuses":["FAILED"],"since":"1d"}`)

	l := ledgerAt(t, start)
	for range 3 {
		require.NoError(t, l.before("list_message_logs", args))
		l.after("list_message_logs", args, failedLogs("Billing_Post", start.Add(-5*time.Hour)), nil)
	}
	assert.Empty(t, l.open.Errors, "historic failures of other flows")
	assert.Empty(t, l.open.Stopped)
	assert.NoError(t, l.before("create_package", json.RawMessage(`{}`)))

	// a failure after loop_start belongs to the loop
	for range 2 {
		l.after("list_message_logs", args, failedLogs("Billing_Post", start.Add(30*time.Second)), nil)
	}
	assert.Contains(t, l.open.Stopped, "same_error_limit")

	// a historic failure of an artifact the loop uploaded counts too
	l = ledgerAt(t, start)
	l.after("upload_artifact", json.RawMessage(`{"artifact_id":"Billing_Post","package_id":"P","dir":"x"}`), nil, nil)
	l.after("upload_artifact", json.RawMessage(`{"artifact_id":"Other","package_id":"P","dir":"x","dry_run":true}`), nil, nil)
	l.after("list_message_logs", args, failedLogs("Other", start.Add(-time.Hour)), nil)
	assert.Empty(t, l.open.Errors, "a dry run touches nothing")
	for range 2 {
		l.after("list_message_logs", args, failedLogs("Billing_Post", start.Add(-time.Hour)), nil)
	}
	assert.Contains(t, l.open.Stopped, "same_error_limit")

	// trace trees: the time of the failed node decides
	l = ledgerAt(t, start)
	old := start.Add(-time.Hour)
	tree := &ops.TraceTree{Roots: []*ops.TraceNode{{MessageGuid: "A", Children: []*ops.TraceNode{{MessageGuid: "B", ArtifactID: "Sub", LogEnd: &old}}}},
		FirstFailure: &ops.TraceFailure{MessageGuid: "B", ArtifactID: "Sub", ErrorText: "boom"}}
	l.after("get_trace_tree", nil, tree, nil)
	l.after("get_trace_tree", nil, tree, nil)
	assert.Empty(t, l.open.Errors)
}

// Every tool has a class; tenant tools are classified explicitly.
func TestEveryToolHasAClass(t *testing.T) {
	all := append(allToolsForTest(), HelpTool(HelpInfo{}))
	for _, tool := range all {
		c := ClassOf(tool.Name)
		require.NotEmpty(t, c, tool.Name)
		switch EffectOf(tool.Name) {
		case EffectTenant:
			_, ok := tenantClasses[tool.Name]
			assert.True(t, ok, "add %s to tenantClasses", tool.Name)
		case EffectRead:
			assert.Equal(t, ClassRead, c)
		case EffectLocal:
			assert.Equal(t, ClassLocal, c)
		}
	}
	for name := range tenantClasses {
		assert.Equal(t, EffectTenant, EffectOf(name), "%s in tenantClasses is no tenant tool", name)
	}
	assert.Equal(t, ClassDeploy, ClassOf("undeploy"))
	assert.Equal(t, ClassProcess, ClassOf("send_test_message"))
	assert.Equal(t, ClassLog, ClassOf("set_log_level"))
	assert.Equal(t, ClassDestructive, ClassOf("unlock_artifact"))
	assert.Equal(t, ClassWrite, ClassOf("create_package"))
	for _, n := range optInTools {
		assert.Equal(t, ClassDestructive, ClassOf(n), n)
	}
}

func TestHelpServerTopic(t *testing.T) {
	kept, removed, err := ApplyFiltersWithReasons(allToolsForTest(), "discover", ToolFilter{})
	require.NoError(t, err)
	var names []string
	reasons := map[string]string{}
	for _, r := range removed {
		names, reasons[r.Name] = append(names, r.Name), r.Reason
	}
	help := HelpTool(HelpInfo{Tools: kept, Removed: names, Reasons: reasons, Mode: "discover", Loop: LoopPolicy{Required: true}})
	got, err := callHelp(t, help, "server")
	require.NoError(t, err)
	server := got["server"].(map[string]any)
	assert.Equal(t, true, server["readOnly"], "discover mode is read-only without --read-only")
	assert.Equal(t, true, server["loop"].(map[string]any)["required"])
	sets := map[string]map[string]any{}
	for _, s := range server["toolsets"].([]any) {
		sets[s.(map[string]any)["name"].(string)] = s.(map[string]any)
	}
	require.Contains(t, sets, "inspect")
	assert.Equal(t, map[string]any{"effect": "local", "class": "local"}, sets["inspect"]["toolEffects"].(map[string]any)["discover_tenant"])
	require.Contains(t, sets, OtherToolset)
	assert.Contains(t, sets[OtherToolset]["tools"], "doctor")
	assert.NotContains(t, sets[OtherToolset]["tools"], "help")

	tool, err := callHelp(t, help, "deploy")
	require.NoError(t, err)
	assert.Equal(t, "deploy", tool["class"])
	assert.Contains(t, tool["reason"], "mode discover")

	full, _, err := ApplyFilters(allToolsForTest(), "full", ToolFilter{})
	require.NoError(t, err)
	got, err = callHelp(t, HelpTool(HelpInfo{Tools: full, Mode: "full"}), "")
	require.NoError(t, err)
	assert.Equal(t, false, got["server"].(map[string]any)["readOnly"])
	for _, s := range got["toolsets"].([]any) {
		if s.(map[string]any)["name"] == OtherToolset {
			assert.Contains(t, s.(map[string]any)["tools"], "undeploy")
		}
	}
	for _, tl := range got["tools"].([]any) {
		assert.NotEmpty(t, tl.(map[string]any)["class"])
	}
}

// A call to a tool the filters removed is an error result that says why;
// a name that was never a tool stays a JSON-RPC error.
func TestRemovedToolCallIsErrorResult(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	tools, removed, err := ApplyFiltersWithReasons(Tools(Config{Exe: mock.Executer(), Root: t.TempDir()}), "", ToolFilter{Deny: []string{"undeploy"}})
	require.NoError(t, err)
	srv := NewServer("cpicli", "test", "", tools)
	srv.Removed = map[string]string{}
	for _, r := range removed {
		srv.Removed[r.Name] = r.Reason
	}
	resp := serve(t, srv,
		call(1, "undeploy", map[string]any{"artifact_ids": []string{"A"}, "confirm": true}),
		call(2, "delete_package", map[string]any{"package_id": "A"}),
		call(3, "no_such_tool", nil))
	res := toolResult(t, resp["1"])
	assert.True(t, res.IsError)
	assert.Equal(t, "usage", res.StructuredContent.ErrorCategory)
	assert.Equal(t, exitcode.Usage, res.StructuredContent.ExitCode)
	assert.Contains(t, res.StructuredContent.Error, "undeploy is not available on this server: disabled by --disable-tools")
	assert.Contains(t, toolResult(t, resp["2"]).StructuredContent.Error, "opt-in")
	require.NotNil(t, resp["3"].Error)
	assert.Equal(t, codeInvalidParams, resp["3"].Error.Code)
	assert.Empty(t, mock.Requests())
}

func applyJSON(t *testing.T, r *Redactor, tool string, v any) map[string]any {
	t.Helper()
	data, err := json.Marshal(r.Apply(tool, v))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

func TestRedactorMasksNameValueLists(t *testing.T) {
	r, err := NewRedactor("", []string{"^order"}, false)
	require.NoError(t, err)
	detail := &ops.MessageLogDetail{
		CustomHeaderProperties: []cpi.NameValue{{Name: "OrderNo", Value: "4711"}, {Name: "Customer", Value: "ACME"}},
		AdapterAttributes:      []cpi.NameValue{{Name: "Authorization", Value: "Basic abc"}},
	}
	m := applyJSON(t, r, "get_message_log", detail)
	assert.Equal(t, []any{map[string]any{"name": "OrderNo", "value": Masked}, map[string]any{"name": "Customer", "value": "ACME"}}, m["customHeaderProperties"])
	assert.Equal(t, Masked, m["adapterAttributes"].([]any)[0].(map[string]any)["value"])
	assert.Equal(t, "4711", detail.CustomHeaderProperties[0].Value, "the original is unchanged")

	steps := &ops.MessageSteps{Runs: []ops.MessageRunWithSteps{{Steps: []cpi.MessageRunStep{{StepID: "s", Properties: []cpi.NameValue{{Name: "api-key", Value: "k"}, {Name: "URL", Value: "u"}}}}}}}
	m = applyJSON(t, r, "get_message_steps", steps)
	props := m["runs"].([]any)[0].(map[string]any)["steps"].([]any)[0].(map[string]any)["properties"].([]any)
	assert.Equal(t, Masked, props[0].(map[string]any)["value"])
	assert.Equal(t, "u", props[1].(map[string]any)["value"])

	tree := &ops.TraceTree{Keys: map[string]string{"OrderNo": "4711", "Region": "EU"}}
	m = applyJSON(t, r, "get_trace_tree", tree)
	assert.Equal(t, map[string]any{"OrderNo": Masked, "Region": "EU"}, m["keys"])

	list := &ops.MessageLogList{Logs: []ops.MessageLog{}, Filter: `custom header OrderNo = "4711"`}
	m = applyJSON(t, r, "find_messages_by_header", list)
	assert.Equal(t, `custom header OrderNo = "***"`, m["filter"])

	plain := &ops.MessageLogList{Logs: []ops.MessageLog{}, Filter: `custom header Region = "EU"`}
	assert.Same(t, plain, r.Apply("find_messages_by_header", plain), "nothing to change: the value is returned as is")

	_, err = NewRedactor("", []string{"("}, false)
	assert.Error(t, err)
}

// run_tests failures quote the response and stub bodies: masked with
// --redact-payloads, the expectation and the expected value stay.
func TestRedactorRedactsTestRunFailures(t *testing.T) {
	r, err := NewRedactor("", nil, true)
	require.NoError(t, err)
	res := map[string]any{"cases": []any{map[string]any{"case": "c", "failures": []any{
		map[string]any{"expectation": "response_contains", "expected": "<ok/>", "actual": "<customer>ACME</customer>", "message": "response lacks <ok/>: <customer>ACME</customer>"},
		map[string]any{"expectation": "message_status", "expected": "COMPLETED", "actual": "FAILED", "message": "status FAILED"},
	}}}}
	m := applyJSON(t, r, "run_tests", res)
	fs := m["cases"].([]any)[0].(map[string]any)["failures"].([]any)
	resp := fs[0].(map[string]any)
	assert.Equal(t, RedactedPayload, resp["actual"])
	assert.Equal(t, RedactedPayload, resp["message"])
	assert.Equal(t, "<ok/>", resp["expected"])
	assert.Equal(t, "FAILED", fs[1].(map[string]any)["actual"], "statuses are no payload")
}

func TestRedactorRedactsPayloads(t *testing.T) {
	r, err := NewRedactor("", nil, true)
	require.NoError(t, err)
	att := &ops.DownloadedContent{ID: "A1", Content: ops.NewContent([]byte("<secret/>"), 0)}
	m := applyJSON(t, r, "get_message_attachment", att)
	assert.Equal(t, RedactedPayload, m["text"])
	assert.EqualValues(t, 9, m["size"])
	sum := sha256.Sum256([]byte("<secret/>"))
	assert.Equal(t, hex.EncodeToString(sum[:]), m["sha256"])
	assert.Equal(t, true, m["redacted"])

	bin := &ops.DownloadedContent{ID: "A2", Content: ops.NewContent([]byte{0xff, 0x00, 0x01}, 0)}
	m = applyJSON(t, r, "get_message_store_entry", bin)
	assert.Nil(t, m["base64"])
	assert.Equal(t, RedactedPayload, m["text"])

	res := &ops.ResourceContent{ArtifactID: "X", Name: "script.groovy", Content: ops.NewContent([]byte("println 1"), 0)}
	m = applyJSON(t, r, "get_resource", res)
	assert.Equal(t, "println 1", m["text"], "resources are content, not payloads")

	off, _ := NewRedactor("", nil, false)
	assert.Same(t, att, off.Apply("get_message_attachment", att))
}

func TestRedactorMakesPathsRootRelative(t *testing.T) {
	root := t.TempDir()
	r, err := NewRedactor(root, nil, false)
	require.NoError(t, err)
	v := map[string]any{"dir": filepath.Join(root, "packages", "P", "Orders"), "root": root, "other": "/elsewhere/x", "n": 3}
	m := applyJSON(t, r, "download_artifact", v)
	assert.Equal(t, "packages/P/Orders", m["dir"])
	assert.Equal(t, ".", m["root"])
	assert.Equal(t, "/elsewhere/x", m["other"])
	assert.EqualValues(t, 3, m["n"])
	assert.Equal(t, `path "x" is outside the server root "."`, r.relativeText(fmt.Sprintf(`path "x" is outside the server root %q`, root)))

	// through the server: download_artifact reports its directory relative to the root
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	w, _ := zw.Create("META-INF/MANIFEST.MF")
	_, _ = w.Write([]byte("Manifest-Version: 1.0\n"))
	require.NoError(t, zw.Close())
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"Orders": {Type: "Integration", DesignVersion: "1", Zip: zipBuf.Bytes()}})
	srv := NewServer("cpicli", "test", "", Tools(Config{Exe: mock.Executer(), Root: root}))
	srv.Redactor = r
	resp := serve(t, srv, call(1, "download_artifact", map[string]any{"artifact_id": "Orders", "dir": "src/Orders"}),
		call(2, "download_artifact", map[string]any{"artifact_id": "Orders", "dir": "/elsewhere"}))
	res := toolResult(t, resp["1"])
	require.False(t, res.IsError, res.Content[0].Text)
	assert.NotContains(t, res.Content[0].Text, root)
	assert.Contains(t, res.Content[0].Text, `"src/Orders`)
	res = toolResult(t, resp["2"])
	assert.NotContains(t, res.Content[0].Text, root)
	_, err = os.Stat(filepath.Join(root, "src", "Orders"))
	assert.NoError(t, err)
}
