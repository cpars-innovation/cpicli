package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A summary belongs to a tool, is shorter than its full description, and
// tools/list sends it while help returns the full description.
func TestToolSummaries(t *testing.T) {
	full := map[string]string{}
	for _, tl := range allToolsForTest() {
		full[tl.Name] = tl.Description
	}
	full["help"] = HelpTool(HelpInfo{}).Description
	for name, s := range toolSummaries {
		if assert.Contains(t, full, name, "summary of an unknown tool") {
			assert.Less(t, len(s), len(full[name])*3/4, "%s: a summary saves at least a quarter", name)
		}
	}

	srv := NewServer("cpicli", "test", Instructions, allToolsForTest())
	for _, tl := range srv.listedTools() {
		if s, ok := toolSummaries[tl.Name]; ok {
			assert.Equal(t, s, tl.Description)
		}
	}
	got, err := callHelp(t, HelpTool(HelpInfo{Tools: allToolsForTest()}), "copy_iflow")
	assert.NoError(t, err)
	assert.Equal(t, full["copy_iflow"], got["description"])
}

// A parameter summary belongs to a parameter of the tool and is shorter;
// tools/list sends it, the tool keeps its full schema for help.
func TestPropSummaries(t *testing.T) {
	byName := map[string]Tool{}
	for _, tl := range allToolsForTest() {
		byName[tl.Name] = tl
	}
	for tool, ps := range propSummaries {
		tl, ok := byName[tool]
		if !assert.True(t, ok, tool) {
			continue
		}
		p := tl.InputSchema["properties"].(props)
		for name, short := range ps {
			if assert.Contains(t, p, name, "%s.%s", tool, name) {
				assert.Less(t, len(short), len(p[name].(map[string]any)["description"].(string)), "%s.%s", tool, name)
			}
		}
	}
	listed := summarized([]Tool{byName["list_message_logs"]})[0]
	lp := listed.InputSchema["properties"].(props)
	assert.NotContains(t, lp["correlation_id"], "description", "a description that repeats the name is left out")
	assert.Equal(t, propSummaries["list_message_logs"]["since"], lp["since"].(map[string]any)["description"])
	assert.Contains(t, byName["list_message_logs"].InputSchema["properties"].(props)["correlation_id"], "description", "the tool keeps its schema")
	assert.True(t, restatesName("application_message_id", "Application message ID"))
	assert.False(t, restatesName("artifact_id", "Integration flow ID"))
}
