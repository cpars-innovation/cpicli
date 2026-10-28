package mcp

import (
	"os"
	"slices"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/scope"
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
	all := append(NewLedger(t.TempDir()).Wrap(Tools(Config{})), HelpTool(HelpInfo{}), testContentTool())
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
		if name == "undeploy" || name == "delete_data_store_entry" || slices.Contains(optInTools, name) || name == "doctor" {
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

// allToolsForTest are the tools of a server: those of Tools and get_content
// (the server command adds it, with help, after filtering).
func allToolsForTest() []Tool {
	return append(NewLedger(os.TempDir()).Wrap(Tools(Config{})), testContentTool())
}

func testContentTool() Tool {
	store := NewContentStore(os.TempDir())
	return ContentTool(func() *ContentStore { return store }, func() string { return "" }, false)
}

// --opt-in adds an opt-in tool without limiting the others; deny and the
// mode still apply.
func TestOptIn(t *testing.T) {
	all := allToolsForTest()
	kept, _, err := ApplyFiltersWithReasons(all, "", ToolFilter{OptIn: []string{"delete_artifact"}})
	require.NoError(t, err)
	names := toolNames(kept)
	assert.Contains(t, names, "delete_artifact")
	assert.Contains(t, names, "deploy", "the other tools stay")
	assert.NotContains(t, names, "delete_package")

	kept, _, err = ApplyFiltersWithReasons(all, "", ToolFilter{Allow: []string{"list_*"}, OptIn: []string{"unlock_artifact"}})
	require.NoError(t, err)
	names = toolNames(kept)
	assert.Contains(t, names, "unlock_artifact", "offered next to the --tools selection")
	assert.NotContains(t, names, "deploy")

	kept, removed, err := ApplyFiltersWithReasons(all, "discover", ToolFilter{OptIn: []string{"delete_artifact"}})
	require.NoError(t, err)
	assert.NotContains(t, toolNames(kept), "delete_artifact", "the mode still applies")
	assert.Contains(t, removed, RemovedTool{Name: "delete_artifact", Reason: "not offered in mode discover (read-only)"})

	kept, _, err = ApplyFiltersWithReasons(all, "", ToolFilter{OptIn: []string{"delete_artifact"}, Deny: []string{"delete_*"}})
	require.NoError(t, err)
	assert.NotContains(t, toolNames(kept), "delete_artifact", "--disable-tools wins")

	_, _, err = ApplyFiltersWithReasons(all, "", ToolFilter{OptIn: []string{"deploy"}})
	assert.ErrorContains(t, err, `"deploy" is not an opt-in tool`)
}

// Every tool the scope filter knows exists, and a scope without an action
// removes exactly the tools that need it (#40).
func TestScopeFilter(t *testing.T) {
	all := Tools(Config{})
	names := map[string]bool{}
	for _, tl := range all {
		names[tl.Name] = true
	}
	for n := range scopeNeeds {
		assert.True(t, names[n], "scopeNeeds names an unknown tool %s", n)
	}
	sc, err := scope.Parse([]byte(`{"version":1,"packages":["SD_*"],"tenant":["jms:write"],"actions":["read","test"]}`))
	require.NoError(t, err)
	kept, removed := ScopeFilter(all, sc, nil)
	gone := map[string]string{}
	for _, r := range removed {
		gone[r.Name] = r.Reason
	}
	assert.Contains(t, gone["deploy"], "deploy is granted on no package")
	assert.Contains(t, gone["unlock_artifact"], "tenant-wide locks")
	for _, n := range []string{"send_test_message", "run_tests", "retry_jms_messages", "list_packages", "get_parameters"} {
		assert.NotContains(t, gone, n)
	}
	assert.Len(t, kept, len(all)-len(removed))
	kept, removed = ScopeFilter(all, nil)
	assert.Empty(t, removed, "no scope: nothing removed")
	assert.Len(t, kept, len(all))
}
