package mcp

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A tool that covers a deprecated payload tool redacts payloads too.
func TestConsolidatedToolsKeepRedaction(t *testing.T) {
	for old, by := range deprecatedTools {
		if !slices.Contains(payloadTools, old) {
			continue
		}
		replacement, _, _ := strings.Cut(by, " ")
		assert.Contains(t, payloadTools, replacement, "%s returns payloads like %s", replacement, old)
	}
}

// Deprecated tools are not listed but still callable; the tools covering
// them are listed.
func TestDeprecatedToolsHidden(t *testing.T) {
	tools := Tools(Config{})
	srv := NewServer("cpicli", "test", Instructions, tools)
	listed := map[string]bool{}
	for _, tl := range srv.listedTools() {
		listed[tl.Name] = true
	}
	for old, by := range deprecatedTools {
		assert.False(t, listed[old], "%s listed", old)
		replacement, _, _ := strings.Cut(by, " ")
		assert.True(t, listed[replacement], "%s (replaces %s) not listed", replacement, old)
		assert.True(t, slices.ContainsFunc(tools, func(tl Tool) bool { return tl.Name == old }), "%s still callable", old)
	}
}

// The merged tools do what the deprecated ones did.
func TestConsolidatedTools(t *testing.T) {
	now := time.Now()
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", DesignVersion: "1", Package: "P", Runtime: &cpitest.Runtime{Version: "1", Status: "ERROR"}, ErrorInfo: "boom"},
	})
	mock.MessageLogSteps = [][]cpitest.MessageLog{{{Guid: "g1", Artifact: "A", Status: "FAILED", Start: now, End: now,
		Attachments: map[string]string{"in.xml": "<in/>"}, StoreEntries: map[string]string{"e1": "<persisted/>"},
		Steps: []cpitest.Step{{StepID: "s1", ModelStepID: "Mapping_1", Status: "FAILED", Error: "bad"}}}}}
	cfg := Config{Exe: mock.Executer(), PollInterval: time.Millisecond, MaxChecks: 3}
	asMap := func(v any) map[string]any {
		data, err := json.Marshal(v)
		require.NoError(t, err)
		m := map[string]any{}
		require.NoError(t, json.Unmarshal(data, &m))
		return m
	}

	res, err := callTool(t, toolByName(t, cfg, "get_message_log"), map[string]any{"message_guid": "g1", "include_steps": true})
	require.NoError(t, err)
	m := asMap(res)
	assert.Contains(t, m, "steps")
	assert.Contains(t, mustJSON(t, m["steps"]), "Mapping_1")
	res, err = callTool(t, toolByName(t, cfg, "get_message_log"), map[string]any{"message_guid": "g1"})
	require.NoError(t, err)
	assert.NotContains(t, asMap(res), "steps")

	res, err = callTool(t, toolByName(t, cfg, "get_message_content"), map[string]any{"attachment_id": "att-g1-in.xml"})
	require.NoError(t, err)
	assert.Contains(t, mustJSON(t, res), `\u003cin/\u003e`)
	res, err = callTool(t, toolByName(t, cfg, "get_message_content"), map[string]any{"store_entry_id": "e1", "max_bytes": 5})
	require.NoError(t, err)
	assert.Contains(t, mustJSON(t, res), `"truncated":true`)
	_, err = callTool(t, toolByName(t, cfg, "get_message_content"), map[string]any{})
	assert.Error(t, err)

	res, err = callTool(t, toolByName(t, cfg, "list_runtime_artifacts"), map[string]any{"artifact_ids": []string{"A"}})
	require.NoError(t, err)
	assert.Contains(t, mustJSON(t, res), "boom", "per-artifact status with the tenant's error")

	_, err = callTool(t, toolByName(t, cfg, "get_message_trace"), map[string]any{})
	assert.ErrorContains(t, err, "message_guid")
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return string(data)
}
