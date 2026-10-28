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
	assert.NotZero(t, server["disabledTools"])
	assert.Len(t, ov["skills"], 7)
	assert.Contains(t, ov["toolsets"].(map[string]any)["inspect"], "graph_neighbors")
	var newFlow string
	for _, w := range ov["workflows"].([]any) {
		if strings.HasPrefix(w.(string), "Create a new flow") {
			newFlow = w.(string)
		}
	}
	assert.Contains(t, newFlow, "copy_iflow", "local tools stay in discover mode")
	assert.Regexp(t, `\(not offered: [^)]*deploy\)`, newFlow)
	assert.NotContains(t, ov, "cli", "the index has no command list")
	data, _ := json.Marshal(ov)
	assert.Less(t, len(data), 8000, "the index stays small")

	srv, err := callHelp(t, help, "server")
	require.NoError(t, err)
	assert.Contains(t, srv["server"].(map[string]any)["disabledTools"], "deploy")
	all, err := callHelp(t, help, "tools")
	require.NoError(t, err)
	assert.Len(t, all["tools"], len(kept))
	wf, err := callHelp(t, help, "workflows")
	require.NoError(t, err)
	assert.Contains(t, wf["workflows"].([]any)[2].(map[string]any)["unavailable"], "deploy")
	cli, err := callHelp(t, help, "cli")
	require.NoError(t, err)
	assert.Equal(t, []any{map[string]any{"command": "iflow copy", "short": "Copy"}}, cli["cli"], "the command list has no long texts")

	sets, err := callHelp(t, help, "toolsets")
	require.NoError(t, err)
	for _, set := range sets["toolsets"].([]any) {
		s := set.(map[string]any)
		assert.NotEmpty(t, s["description"])
		if s["name"] == "build" {
			assert.Contains(t, s["tools"], "lint")
			assert.NotContains(t, s["tools"], "deploy", "only tools the server offers")
			assert.Contains(t, s["notOffered"], "deploy", "members the server does not offer are named")
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

func TestHelpInstructionsPerToolset(t *testing.T) {
	for _, f := range instructionFragments {
		for _, set := range f.toolsets {
			_, ok := Toolsets[set]
			assert.True(t, ok || set == OtherToolset, "fragment keyed by unknown toolset %q", set)
		}
	}
	all := Tools(Config{})
	kept, _, err := ApplyFilters(all, "", ToolFilter{Allow: []string{"doctor", "list_jms_messages", "message_summary"}})
	require.NoError(t, err)
	help := HelpTool(HelpInfo{Tools: kept, ServerInstructions: ModeInstructions("operate")})
	call := func(args map[string]any) (map[string]any, error) {
		raw, _ := json.Marshal(args)
		res, err := help.Handler(context.Background(), raw)
		if err != nil {
			return nil, err
		}
		return res.(map[string]any), nil
	}

	// explicit toolsets
	res, err := call(map[string]any{"topic": "instructions", "toolsets": []string{"build"}})
	require.NoError(t, err)
	text := res["instructions"].(string)
	assert.Contains(t, text, "Call help", "general part")
	assert.Contains(t, text, "errorCategory", "general part")
	assert.Contains(t, text, "Build loop")
	assert.Contains(t, text, "check_guidelines")
	assert.NotContains(t, text, "JMS:")
	assert.NotContains(t, text, "Security material")
	assert.Contains(t, text, "Mode: operate", "server-specific part")
	assert.Less(t, len(text), len(Instructions))

	// default: the toolsets of the available tools
	res, err = call(map[string]any{"topic": "instructions"})
	require.NoError(t, err)
	assert.Equal(t, []string{"monitor"}, res["toolsets"])
	assert.Equal(t, "active", res["toolsetsSource"])
	text = res["instructions"].(string)
	assert.Contains(t, text, "JMS:")
	assert.NotContains(t, text, "Build loop")

	// dynamic: the enabled toolsets
	help = HelpTool(HelpInfo{Tools: all, ActiveToolsets: func() []string { return []string{"security"} }})
	res, err = call(map[string]any{"topic": "instructions"})
	require.NoError(t, err)
	assert.Contains(t, res["instructions"], "Security material")
	assert.NotContains(t, res["instructions"], "JMS:")

	_, err = call(map[string]any{"topic": "instructions", "toolsets": []string{"bogus"}})
	assert.ErrorContains(t, err, "invalid toolset")
	assert.Equal(t, Instructions, ToolsetInstructions(append(ToolsetNames(), OtherToolset)), "all toolsets give the full instructions")
}
