package cmd

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mcpSession runs cpictl mcp with the JSON-RPC lines on stdin and returns
// the replies by id ("" for notifications, in order).
func mcpSession(t *testing.T, args []string, lines ...string) (map[string]map[string]any, []string) {
	t.Helper()
	return mcpSessionAs(t, "t", args, lines...)
}

// mcpSessionAs is mcpSession for a client of this name (clientInfo.name).
func mcpSessionAs(t *testing.T, client string, args []string, lines ...string) (map[string]map[string]any, []string) {
	t.Helper()
	in, err := os.CreateTemp(t.TempDir(), "stdin")
	require.NoError(t, err)
	defer in.Close() // Windows cannot remove an open file
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"` + client + `","version":"1"}}}`
	_, _ = in.WriteString(strings.Join(append([]string{init}, lines...), "\n") + "\n")
	_, _ = in.Seek(0, 0)
	old := os.Stdin
	os.Stdin = in
	defer func() { os.Stdin = old }()
	mock := cpitest.NewTenant(t, nil)
	r := runMain(t, append(append([]string{"mcp", "--root", t.TempDir()}, args...), basicAuth(mock)...)...)
	require.Equal(t, 0, r.code, r.stderr)
	byID := map[string]map[string]any{}
	var notes []string
	for line := range strings.SplitSeq(strings.TrimSpace(r.stdout), "\n") {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m), line)
		if id, ok := m["id"]; ok {
			byID[jsonString(id)] = m
		} else {
			notes = append(notes, m["method"].(string))
		}
	}
	return byID, notes
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func toolNames(t *testing.T, reply map[string]any) []string {
	t.Helper()
	var names []string
	for _, tl := range reply["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	return names
}

const listTools = `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`

// By default the core tools are listed, and enable_toolset brings the rest
// with their instructions; --all-tools, --toolset and --tools list exactly
// what they choose.
func TestMCPToolsOnDemandByDefault(t *testing.T) {
	byID, _ := mcpSession(t, nil, listTools)
	assert.ElementsMatch(t, mcp.CoreTools, toolNames(t, byID["2"]))
	instructions := byID["1"]["result"].(map[string]any)["instructions"].(string)
	assert.Contains(t, instructions, "Tools on demand")
	assert.NotContains(t, instructions, "Build loop", "the build part comes with enable_toolset")

	byID, notes := mcpSession(t, nil,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"enable_toolset","arguments":{"toolsets":["build"]}}}`)
	assert.Equal(t, []string{"notifications/tools/list_changed"}, notes)
	text := byID["3"]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	assert.Contains(t, text, `"name":"deploy"`)
	assert.Contains(t, text, "Build loop")

	byID, _ = mcpSession(t, []string{"--all-tools"}, listTools)
	all := toolNames(t, byID["2"])
	assert.Greater(t, len(all), 90)
	assert.NotContains(t, all, "enable_toolset")

	byID, _ = mcpSession(t, []string{"--toolset", "monitor"}, listTools)
	assert.Contains(t, toolNames(t, byID["2"]), "list_jms_queues")
	assert.NotContains(t, toolNames(t, byID["2"]), "enable_toolset")

	byID, _ = mcpSession(t, []string{"--tools", "list_packages"}, listTools)
	assert.Contains(t, toolNames(t, byID["2"]), "list_packages")
	assert.NotContains(t, toolNames(t, byID["2"]), "list_message_logs")

	byID, _ = mcpSession(t, []string{"--mode", "discover"}, listTools)
	assert.Contains(t, toolNames(t, byID["2"]), "message_summary")

	// Claude Code loads tool schemas on demand itself: it gets every tool
	byID, _ = mcpSessionAs(t, "claude-code", nil, listTools)
	assert.ElementsMatch(t, all, toolNames(t, byID["2"]))
	assert.Contains(t, byID["1"]["result"].(map[string]any)["instructions"], "Build loop")
	assert.NotContains(t, byID["1"]["result"].(map[string]any)["instructions"], "Tools on demand")

	r := runMain(t, "mcp", "--all-tools", "--dynamic-toolsets")
	assert.Equal(t, 2, r.code, r.stderr)
}
