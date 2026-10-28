package mcp

import (
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTraceTreeByMessage(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.FilterMessageLogs = true
	mock.NoCustomHeaderSearch = true // key headers: scan, which needs a scope
	t0 := time.Now().Add(-time.Minute)
	mock.MessageLogSteps = [][]cpitest.MessageLog{{
		{Guid: "M1", Artifact: "Orders_In", Status: "COMPLETED", CorrelationID: "C1", Start: t0, End: t0, Headers: map[string]string{"OrderNo": "4711"}},
		{Guid: "M2", Artifact: "Orders_Route", Status: "FAILED", ErrorText: "boom", CorrelationID: "C1", Predecessor: "M1", Start: t0.Add(time.Second), End: t0.Add(time.Second)},
	}}
	byID := serveCompact(t, NewServer("cpicli", "test", Instructions, Tools(Config{Exe: mock.Executer(), Root: t.TempDir()})),
		call(1, "get_trace_tree", map[string]any{"message_guid": "M2"}),
		call(2, "get_trace_tree", map[string]any{}),
		call(3, "get_trace_tree", map[string]any{"message_guid": "M2", "key_headers": []string{"OrderNo"}}),
		call(4, "get_trace_tree", map[string]any{"message_guid": "M2", "detail": "full"}))
	res := toolResult(t, byID["1"])
	require.False(t, res.IsError, res.Content[0].Text)
	// lean: the link kind on the node, the error text once
	assert.Contains(t, res.Content[0].Text, `"link":"predecessor"`)
	assert.Equal(t, 1, strings.Count(res.Content[0].Text, `"boom"`))
	assert.NotContains(t, res.Content[0].Text, `"hops"`)
	full := toolResult(t, byID["4"])
	assert.Contains(t, full.Content[0].Text, `"hops":[{"from":"Orders_In","to":"Orders_Route","fromMessage":"M1","toMessage":"M2","link":"predecessor"}]`)
	assert.Contains(t, res.Content[0].Text, `"pathKey":"`)
	assert.Equal(t, "usage", toolResult(t, byID["2"]).StructuredContent.ErrorCategory)
	assert.Equal(t, "usage", toolResult(t, byID["3"]).StructuredContent.ErrorCategory, "key headers need a scope (without the custom header search)")
}
