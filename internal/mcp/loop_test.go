package mcp

import (
	"fmt"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The exit codes are a contract: they are never renumbered.
func TestExitCodeValues(t *testing.T) {
	assert.Equal(t, []int{0, 1, 2, 3, 4, 5, 6, 7, 8}, []int{exitcode.OK, exitcode.Error, exitcode.Usage, exitcode.Auth,
		exitcode.TenantHTTP, exitcode.DeployFailed, exitcode.Timeout, exitcode.Partial, exitcode.Stopped})
	assert.Equal(t, "stopped", Category(exitcode.Stopped))
}

func TestNormalizeError(t *testing.T) {
	a := ops.NormalizeError("Message AFq478Bblxi4wCjBcDb_G0vAGGZG failed at 2026-10-05T08:00:01.123Z, order 1234567")
	b := ops.NormalizeError("Message AGr123Bblxi4wCjBcDb_G0vAZZZZ failed at 2026-10-05T09:12:44Z, order 7654321")
	assert.Equal(t, a, b)
	assert.NotEqual(t, ops.NormalizeError("mapping failed"), ops.NormalizeError("timeout"))
}

type loopEnv struct {
	t    *testing.T
	mock *cpitest.Tenant
	root string
	srv  *Server
	n    int
}

func newLoopEnv(t *testing.T) *loopEnv {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"Orders": {Type: "Integration", DesignVersion: "1"}})
	host, port := mock.HostPort()
	mock.Artifacts["Orders"].EndpointURL = fmt.Sprintf("http://%s:%d/http/orders", host, port)
	mock.Inbound = map[string]*cpitest.Inbound{"/http/orders": {Status: 500, Response: "mapping failed at step 4"}}
	root := t.TempDir()
	newExe := func(url string) (*httpclnt.HTTPExecuter, string, error) {
		return cpi.NewEndpointExecuter(&cpi.ServiceDetails{Userid: "u", Password: "p"}, url)
	}
	tools := NewLedger(root).Wrap(Tools(Config{Exe: mock.Executer(), Root: root, PollInterval: time.Millisecond, MaxChecks: 1, NewEndpointExecuter: newExe}))
	return &loopEnv{t: t, mock: mock, root: root, srv: NewServer("cpicli", "test", Instructions, tools)}
}

func (e *loopEnv) call(tool string, args any) ToolResult {
	e.t.Helper()
	e.n++
	resp := serve(e.t, e.srv, call(e.n, tool, args))
	return toolResult(e.t, resp[fmt.Sprint(e.n)]).StructuredContent
}

func TestLoopStopsOnRepeatedError(t *testing.T) {
	e := newLoopEnv(t)
	start := e.call("loop_start", map[string]any{"goal": "fix the mapping"})
	require.True(t, start.OK, start.Error)
	id := start.Result.(map[string]any)["loopId"].(string)

	deploy := map[string]any{"artifact_ids": []string{"Orders"}}
	send := map[string]any{"artifact_id": "Orders", "trace": false}
	assert.NotEqual(t, "stopped", e.call("deploy", deploy).ErrorCategory)
	assert.Equal(t, "failed", e.call("send_test_message", send).ErrorCategory)
	assert.NotEqual(t, "stopped", e.call("deploy", deploy).ErrorCategory, "second deploy: iteration 1")
	assert.Equal(t, "failed", e.call("send_test_message", send).ErrorCategory, "same error again")

	third := e.call("deploy", deploy)
	assert.Equal(t, "stopped", third.ErrorCategory)
	assert.Equal(t, exitcode.Stopped, third.ExitCode)
	assert.Contains(t, third.Error, "same error")
	assert.Equal(t, 2, e.mock.Count("POST /api/v1/DeployIntegrationDesigntimeArtifact"), "the third deploy never reached the tenant")

	// read tools still work, and loop_end always works
	assert.NotEqual(t, "stopped", e.call("list_runtime_artifacts", map[string]any{"artifact_ids": []string{"Orders"}}).ErrorCategory)
	status := e.call("loop_status", map[string]any{"loop_id": id})
	require.True(t, status.OK)
	st := status.Result.(map[string]any)
	assert.EqualValues(t, 2, st["deploys"])
	assert.EqualValues(t, 1, st["iterations"])

	end := e.call("loop_end", map[string]any{"loop_id": id, "outcome": "stopped after repeated mapping error"})
	require.True(t, end.OK, end.Error)
	summary, err := os.ReadFile(filepath.Join(e.root, end.Result.(map[string]any)["summaryFile"].(string)))
	require.NoError(t, err)
	assert.Contains(t, string(summary), "fix the mapping")
	assert.Contains(t, string(summary), "| deploy | stopped |")
	lines, err := os.ReadFile(filepath.Join(e.root, ".cpi", "loops", id+".jsonl"))
	require.NoError(t, err)
	assert.Equal(t, 6, strings.Count(string(lines), "\n"), "every tool call of the loop is recorded")

	// after loop_end, tenant calls are no longer limited
	assert.NotEqual(t, "stopped", e.call("deploy", deploy).ErrorCategory)
}

func TestLoopMaxDeploys(t *testing.T) {
	e := newLoopEnv(t)
	start := e.call("loop_start", map[string]any{"goal": "g", "max_deploys": 1})
	require.True(t, start.OK)
	deploy := map[string]any{"artifact_ids": []string{"Orders"}}
	assert.NotEqual(t, "stopped", e.call("deploy", deploy).ErrorCategory)
	assert.Equal(t, "stopped", e.call("deploy", deploy).ErrorCategory)
	// a pd_deploy dry run is not a deployment, but a stopped loop refuses all tenant tools
	assert.Equal(t, "stopped", e.call("set_parameters", map[string]any{"artifact_id": "Orders", "parameters": map[string]string{"a": "b"}}).ErrorCategory)

	second := e.call("loop_start", map[string]any{"goal": "another"})
	assert.Equal(t, "usage", second.ErrorCategory, "only one open loop")
}

func TestModes(t *testing.T) {
	all := NewLedger(t.TempDir()).Wrap(Tools(Config{}))
	kept, _, err := ApplyFilters(all, "discover", ToolFilter{})
	require.NoError(t, err)
	for _, n := range toolNames(kept) {
		assert.NotEqual(t, EffectTenant, EffectOf(n), n)
	}

	kept, _, err = ApplyFilters(all, "operate", ToolFilter{})
	require.NoError(t, err)
	names := toolNames(kept)
	assert.Contains(t, names, "set_log_level")
	assert.Contains(t, names, "list_message_logs")
	assert.NotContains(t, names, "deploy")
	assert.NotContains(t, names, "send_test_message")
	assert.Contains(t, names, "retry_jms_messages", "operations as in the Web UI's Monitor")
	assert.NotContains(t, names, "delete_jms_messages", "opt-in")

	// the most restrictive wins, and naming a tool the mode removes is fine
	kept, _, err = ApplyFilters(all, "operate", ToolFilter{Deny: []string{"set_log_level", "deploy"}})
	require.NoError(t, err)
	assert.NotContains(t, toolNames(kept), "set_log_level")

	kept, _, err = ApplyFilters(all, "develop", ToolFilter{})
	require.NoError(t, err)
	assert.Len(t, kept, len(all)-len(optInTools))

	_, _, err = ApplyFilters(all, "admin", ToolFilter{})
	assert.Error(t, err)
}

func TestDevelopModeRefusesFullSync(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pd", "PID1"), 0o755))
	srv := NewServer("cpicli", "test", Instructions, Tools(Config{Exe: mock.Executer(), Root: root, DenyFullSync: true}))
	resp := serve(t, srv, call(1, "pd_deploy", map[string]any{"resources_path": "pd", "full_sync": true}))
	res := toolResult(t, resp["1"]).StructuredContent
	assert.Equal(t, "usage", res.ErrorCategory)
	assert.Contains(t, res.Error, "full_sync is not allowed")
}

func TestFullModeAllowsEverything(t *testing.T) {
	all := NewLedger(t.TempDir()).Wrap(Tools(Config{}))
	kept, removed, err := ApplyFilters(all, "full", ToolFilter{})
	require.NoError(t, err)
	assert.Len(t, kept, len(all)-len(optInTools))
	assert.Equal(t, optInTools, removed, "opt-in tools need --tools even in full mode")
	assert.Contains(t, ModeInstructions("full"), "full_sync")
}

// unlock_artifact is offered only when --tools names it exactly.
func TestOptInToolNeedsExplicitName(t *testing.T) {
	all := Tools(Config{})
	for _, f := range []ToolFilter{{}, {Allow: []string{"*"}}, {Allow: []string{"unlock_*", "list_locks"}}} {
		kept, removed, err := ApplyFilters(all, "full", f)
		require.NoError(t, err)
		assert.NotContains(t, toolNames(kept), "unlock_artifact", "%v", f.Allow)
		assert.Contains(t, removed, "unlock_artifact")
	}
	kept, _, err := ApplyFilters(all, "develop", ToolFilter{Allow: []string{"list_locks", "unlock_artifact"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"list_locks", "unlock_artifact"}, toolNames(kept))

	kept, _, err = ApplyFilters(all, "discover", ToolFilter{Allow: []string{"list_locks", "unlock_artifact"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"list_locks"}, toolNames(kept), "read-only modes still drop it")
}

// delete_artifact and delete_package are opt-in like unlock_artifact.
func TestDeleteToolsAreOptIn(t *testing.T) {
	all := Tools(Config{})
	for _, f := range []ToolFilter{{}, {Allow: []string{"*"}}, {Allow: []string{"delete_*"}}} {
		kept, _, err := ApplyFilters(all, "full", f)
		require.NoError(t, err)
		assert.NotContains(t, toolNames(kept), "delete_artifact", "%v", f.Allow)
		assert.NotContains(t, toolNames(kept), "delete_package", "%v", f.Allow)
	}
	kept, _, err := ApplyFilters(all, "develop", ToolFilter{Allow: []string{"delete_artifact", "delete_package"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"delete_artifact", "delete_package"}, toolNames(kept))
}
