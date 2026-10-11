package mcp

import (
	"os"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func toolNames(tools []Tool) []string {
	var names []string
	for _, t := range tools {
		names = append(names, t.Name)
	}
	return names
}

// A new tool must be classified, otherwise read-only servers could offer it.
func TestEveryToolHasAnEffect(t *testing.T) {
	// help is added by the server command after filtering
	all := append(NewLedger(t.TempDir()).Wrap(Tools(Config{})), HelpTool(HelpInfo{}))
	dyn := NewServer("x", "t", "", nil)
	require.NoError(t, dyn.UseDynamicToolsets(nil))
	all = append(all, dyn.tools...) // list_toolsets, enable_toolset
	for _, tool := range all {
		effect, ok := toolEffects[tool.Name]
		require.True(t, ok, "add %s to toolEffects", tool.Name)
		ro, _ := tool.Annotations["readOnlyHint"].(bool)
		if ro {
			assert.Equal(t, EffectRead, effect, "%s is annotated read-only", tool.Name)
		}
		if effect == EffectTenant {
			assert.False(t, ro, tool.Name)
		}
	}
	assert.Len(t, toolEffects, len(all), "toolEffects lists a tool that does not exist")
}

func TestFilter(t *testing.T) {
	all := Tools(Config{})

	kept, removed, err := Filter(all, ToolFilter{ReadOnly: true})
	require.NoError(t, err)
	for _, name := range toolNames(kept) {
		assert.NotEqual(t, EffectTenant, EffectOf(name), name)
	}
	assert.Contains(t, removed, "deploy")
	assert.Contains(t, removed, "send_test_message")
	assert.Contains(t, toolNames(kept), "download_artifact", "local writes stay available")

	kept, _, err = Filter(all, ToolFilter{Allow: []string{"list_*", "deploy"}, Deny: []string{"list_keystore"}})
	require.NoError(t, err)
	assert.Contains(t, toolNames(kept), "list_packages")
	assert.Contains(t, toolNames(kept), "deploy")
	assert.NotContains(t, toolNames(kept), "list_keystore")
	assert.NotContains(t, toolNames(kept), "undeploy")

	kept, _, err = Filter(all, ToolFilter{ReadOnly: true, Allow: []string{"deploy", "list_packages"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"list_packages"}, toolNames(kept), "read-only wins over allow")

	for _, f := range []ToolFilter{{Allow: []string{"deplyo"}}, {Deny: []string{"["}}, {Allow: []string{"deploy"}, Deny: []string{"deploy"}}} {
		_, _, err = Filter(all, f)
		assert.Equal(t, exitcode.Usage, output.ExitCode(err), "%+v", f)
	}
}

func TestFilteredServerHidesAndRejectsTools(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	filter := ToolFilter{ReadOnly: true}
	tools, removed, err := Filter(Tools(Config{Exe: mock.Executer(), Root: t.TempDir()}), filter)
	require.NoError(t, err)
	instructions := FilteredInstructions(Instructions, filter, removed)
	assert.Contains(t, instructions, "read-only")
	assert.Contains(t, instructions, "undeploy")

	srv := NewServer("cpicli", "test", instructions, tools)
	resp := serve(t, srv,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		call(2, "undeploy", map[string]any{"artifact_ids": []string{"A"}, "confirm": true}),
	)
	assert.NotContains(t, string(resp["1"].Result), `"undeploy"`)
	require.NotNil(t, resp["2"].Error)
	assert.Equal(t, codeInvalidParams, resp["2"].Error.Code)
	assert.Empty(t, mock.Requests())
}

func TestToolsets(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range allToolsForTest() {
		names[tool.Name] = true
	}
	inSome := map[string]bool{}
	for set, tools := range Toolsets {
		for _, tool := range tools {
			assert.True(t, names[tool], "toolset %s names unknown tool %s", set, tool)
			inSome[tool] = true
		}
	}
	// destructive tools are offered only on request (--tools)
	for name := range names {
		if name == "undeploy" || name == "delete_data_store_entry" || name == "unlock_artifact" || name == "doctor" {
			continue
		}
		assert.True(t, inSome[name], "tool %s is in no toolset", name)
	}
	got, err := ToolsetTools([]string{"promote", "security"})
	require.NoError(t, err)
	assert.Contains(t, got, "transport_check")
	assert.Contains(t, got, "doctor")
	_, err = ToolsetTools([]string{"everything"})
	assert.Error(t, err)

	kept, _, err := ApplyFilters(allToolsForTest(), "discover", ToolFilter{Allow: got})
	require.NoError(t, err)
	for _, tool := range kept {
		assert.Contains(t, got, tool.Name)
	}
}

func allToolsForTest() []Tool {
	return NewLedger(os.TempDir()).Wrap(Tools(Config{}))
}
