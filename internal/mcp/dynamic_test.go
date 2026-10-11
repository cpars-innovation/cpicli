package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func listedNames(t *testing.T, r rpcResp) []string {
	t.Helper()
	require.Nil(t, r.Error)
	var res struct {
		Tools []Tool `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(r.Result, &res))
	names := []string{}
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func dynamicServer(t *testing.T, filter ToolFilter, initial ...string) *Server {
	t.Helper()
	mock := cpitest.NewTenant(t, nil)
	tools, removed, err := ApplyFilters(Tools(Config{Exe: mock.Executer(), Root: t.TempDir(), PollInterval: time.Millisecond, MaxChecks: 3}), "", filter)
	require.NoError(t, err)
	tools = append(tools, HelpTool(HelpInfo{Tools: tools, Removed: removed}))
	srv := NewServer("cpicli", "test", Instructions+DynamicInstructions(), tools)
	require.NoError(t, srv.UseDynamicToolsets(initial))
	return srv
}

func TestDynamicToolsets(t *testing.T) {
	srv := dynamicServer(t, ToolFilter{})
	var out bytes.Buffer
	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		call(3, "list_toolsets", nil),
		call(4, "enable_toolset", map[string]any{"toolsets": []string{"security"}}),
	}
	require.NoError(t, srv.Serve(context.Background(), strings.NewReader(strings.Join(msgs, "\n")+"\n"), &out))
	byLine := map[string]rpcResp{}
	notified, enableReply := -1, -1
	for i, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var r rpcResp
		require.NoError(t, json.Unmarshal([]byte(line), &r))
		switch {
		case len(r.ID) == 0:
			assert.JSONEq(t, `{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`, line)
			notified = i
		case string(r.ID) == "4":
			enableReply = i
		}
		byLine[string(r.ID)] = r
	}
	// the client is told before the result of enable_toolset arrives
	require.GreaterOrEqual(t, notified, 0, out.String())
	assert.Less(t, notified, enableReply)

	assert.Contains(t, string(byLine["1"].Result), `"listChanged":true`)
	assert.ElementsMatch(t, []string{"doctor", "help", "list_toolsets", "enable_toolset"}, listedNames(t, byLine["2"]))

	res := toolResult(t, byLine["3"])
	assert.Contains(t, res.Content[0].Text, `"name":"promote"`)
	assert.Contains(t, res.Content[0].Text, `"name":"other"`) // undeploy, delete_data_store_entry

	res = toolResult(t, byLine["4"])
	assert.False(t, res.IsError)
	assert.Contains(t, res.Content[0].Text, "list_keystore")

	// a later tools/list has the toolset's tools
	byID := serve(t, srv, `{"jsonrpc":"2.0","id":5,"method":"tools/list"}`,
		call(6, "enable_toolset", map[string]any{"toolsets": []string{"security"}}),
		call(7, "enable_toolset", map[string]any{"toolsets": []string{"nope"}}))
	assert.ElementsMatch(t, append([]string{"doctor", "help", "list_toolsets", "enable_toolset"}, Toolsets["security"]...), listedNames(t, byID["5"]))
	again := toolResult(t, byID["6"])
	assert.Contains(t, again.Content[0].Text, `"addedTools":[]`) // nothing new, no notification
	assert.Equal(t, "usage", toolResult(t, byID["7"]).StructuredContent.ErrorCategory)
}

func TestDynamicToolsetsRespectFilters(t *testing.T) {
	srv := dynamicServer(t, ToolFilter{ReadOnly: true}, "build")
	byID := serve(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	names := listedNames(t, byID["1"])
	assert.Contains(t, names, "lint")
	assert.Contains(t, names, "list_toolsets")
	assert.NotContains(t, names, "deploy", "a read-only server cannot enable deploy through a toolset")
	assert.NotContains(t, names, "upload_artifact")

	assert.Error(t, NewServer("x", "t", "", nil).UseDynamicToolsets([]string{"everything"}))
}

func TestStaticServerDoesNotAnnounceListChanged(t *testing.T) {
	byID := serve(t, NewServer("cpicli", "test", "", nil), `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	assert.Contains(t, string(byID["1"].Result), `"listChanged":false`)
}
