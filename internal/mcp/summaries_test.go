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
