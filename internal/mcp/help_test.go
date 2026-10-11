package mcp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func callHelp(t *testing.T, tool Tool, topic string) (map[string]any, error) {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"topic": topic})
	res, err := tool.Handler(context.Background(), raw)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(res)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	return m, nil
}

func TestHelpTool(t *testing.T) {
	kept, removed, err := ApplyFilters(Tools(Config{}), "discover", ToolFilter{})
	require.NoError(t, err)
	help := HelpTool(HelpInfo{Tools: kept, Removed: removed, Mode: "discover",
		Commands: []CommandInfo{{Command: "iflow copy", Short: "Copy", Long: "long text", Usage: "cpictl iflow copy [flags]"}}})
	assert.Equal(t, EffectRead, EffectOf("help"))

	ov, err := callHelp(t, help, "")
	require.NoError(t, err)
	server := ov["server"].(map[string]any)
	assert.Equal(t, "discover", server["mode"])
	assert.Contains(t, server["disabledTools"], "deploy")
	assert.Len(t, ov["skills"], 7)
	assert.Len(t, ov["tools"], len(kept))
	var newFlow map[string]any
	for _, w := range ov["workflows"].([]any) {
		if strings.HasPrefix(w.(map[string]any)["task"].(string), "Create a new flow") {
			newFlow = w.(map[string]any)
		}
	}
	require.NotNil(t, newFlow)
	assert.Contains(t, newFlow["tools"], "copy_iflow", "local tools stay in discover mode")
	assert.Contains(t, newFlow["unavailable"], "deploy")
	assert.Equal(t, []any{map[string]any{"command": "iflow copy", "short": "Copy"}}, ov["cli"], "the overview has no long texts")

	sets, err := callHelp(t, help, "toolsets")
	require.NoError(t, err)
	for _, set := range sets["toolsets"].([]any) {
		s := set.(map[string]any)
		assert.NotEmpty(t, s["description"])
		if s["name"] == "build" {
			assert.Contains(t, s["tools"], "lint")
			assert.NotContains(t, s["tools"], "deploy", "only tools the server offers")
		}
	}

	tool, err := callHelp(t, help, "graph_neighbors")
	require.NoError(t, err)
	assert.Equal(t, true, tool["available"])
	assert.NotEmpty(t, tool["inputSchema"])
	tool, err = callHelp(t, help, "deploy")
	require.NoError(t, err)
	assert.Equal(t, false, tool["available"])

	skill, err := callHelp(t, help, "cpi-build")
	require.NoError(t, err)
	assert.Contains(t, skill["content"], "# Build an integration flow")
	assert.Contains(t, skill["files"], "iflow-structure.md")
	file, err := callHelp(t, help, "cpi-plan/brief-template.md")
	require.NoError(t, err)
	assert.Contains(t, file["content"], "# Brief")
	_, err = callHelp(t, help, "cpi-plan/../../go.mod")
	assert.Error(t, err)

	cmd, err := callHelp(t, help, "cpictl iflow copy")
	require.NoError(t, err)
	assert.Equal(t, "long text", cmd["long"])

	_, err = callHelp(t, help, "graph")
	assert.ErrorContains(t, err, "graph_neighbors", "suggestions")
}

// TestWorkflowTools catches typos in the help workflows.
func TestWorkflowTools(t *testing.T) {
	for _, w := range workflows {
		for _, name := range w.Tools {
			_, ok := toolEffects[name]
			assert.True(t, ok, "%s: unknown tool %s", w.Task, name)
		}
		_, err := os.Stat("../../docs/" + w.Docs)
		assert.NoError(t, err, w.Docs)
	}
}

// TestToolsDocumented keeps docs/mcp.md complete.
func TestToolsDocumented(t *testing.T) {
	doc, err := os.ReadFile("../../docs/mcp.md")
	require.NoError(t, err)
	for name := range toolEffects {
		assert.Contains(t, string(doc), "`"+name+"`", "docs/mcp.md does not mention %s", name)
	}
}
