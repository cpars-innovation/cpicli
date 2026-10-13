package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/cpars-innovation/cpicli/pkg/testtools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func toolByName(t *testing.T, cfg Config, name string) Tool {
	t.Helper()
	for _, tl := range Tools(cfg) {
		if tl.Name == name {
			return tl
		}
	}
	t.Fatalf("no tool %s", name)
	return Tool{}
}

func callTool(t *testing.T, tl Tool, args map[string]any) (any, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	return tl.Handler(context.Background(), raw)
}

func TestTestToolingTools(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.Live, mock.FilterMessageLogs = true, true
	cfg := Config{Exe: mock.Executer(), PollInterval: time.Millisecond, MaxChecks: 5}

	res, err := callTool(t, toolByName(t, cfg, "install_test_tools"), map[string]any{"deploy": true, "role": "ACME.Test"})
	require.NoError(t, err)
	install := res.(*ops.TestToolsInstallResult)
	require.Len(t, install.Artifacts, 2)
	for _, a := range install.Artifacts {
		require.NotNil(t, a.Deploy, a.ID)
		assert.Equal(t, ops.StatusDeployed, a.Deploy.Status, a.ID)
		assert.Equal(t, "ACME.Test", a.Role, a.ID)
	}
	assert.Equal(t, "ACME.Test", mock.Artifacts[testtools.HarnessID].Parameters[testtools.HarnessRoleParameter])

	set := toolByName(t, cfg, "set_stub_response")
	_, err = callTool(t, set, map[string]any{"rule": "erp", "status": 503, "body": "busy", "content_type": "text/plain"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"status":503,"contentType":"text/plain","body":"busy","headers":{}}`, mock.PDStrings[testtools.StubPID+"/erp"])

	_, err = callTool(t, set, map[string]any{"rule": "erp", "clear": true, "status": 200})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err), "clear takes no answer")
	res, err = callTool(t, set, map[string]any{"rule": "erp", "clear": true})
	require.NoError(t, err)
	assert.Equal(t, "CLEARED", res.(map[string]any)["action"])
	assert.NotContains(t, mock.PDStrings, testtools.StubPID+"/erp")
}

// On a production tier both tools refuse before anything is sent; only the
// server's start option lifts it.
func TestTestToolingRefusedOnProduction(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	cfg := Config{Exe: mock.Executer(), Tier: "prod"}
	for name, args := range map[string]map[string]any{
		"install_test_tools": {"deploy": true},
		"set_stub_response":  {"rule": "erp", "status": 500},
	} {
		before := len(mock.Requests())
		_, err := callTool(t, toolByName(t, cfg, name), args)
		require.Error(t, err, name)
		assert.Equal(t, exitcode.Usage, output.ExitCode(err), name)
		assert.Contains(t, err.Error(), "production tenant", name)
		assert.Len(t, mock.Requests(), before, "nothing was sent: %s", name)
	}
	cfg.AllowProduction = true
	_, err := callTool(t, toolByName(t, cfg, "set_stub_response"), map[string]any{"rule": "erp", "status": 500})
	require.NoError(t, err)
}
