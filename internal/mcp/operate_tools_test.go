package mcp

import (
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJMSTools(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.Queues = map[string]*cpitest.JMSQueue{
		"Q":     {Name: "Q", Messages: []cpitest.JMSMessage{{ID: "ID:1", Failed: true, MplID: "M1"}, {ID: "ID:2"}}},
		"Q_ERR": {Name: "Q_ERR", Messages: []cpitest.JMSMessage{}},
	}
	// one call per session: the server runs calls concurrently
	run := func(name string, args map[string]any) ToolResult {
		return toolResult(t, session(t, mock, t.TempDir(), call(1, name, args))["1"]).StructuredContent
	}
	list := run("list_jms_messages", map[string]any{"queue": "Q", "failed_only": true})
	require.True(t, list.OK, list.Error)
	assert.EqualValues(t, 1, list.Result.(map[string]any)["total"])
	assert.Equal(t, "usage", run("retry_jms_messages", map[string]any{"queue": "Q", "all": true}).ErrorCategory, "confirm required")
	r := run("retry_jms_messages", map[string]any{"queue": "Q", "ids": []string{"ID:1"}, "confirm": true})
	assert.True(t, r.OK, r.Error)
	r = run("move_jms_messages", map[string]any{"queue": "Q", "ids": []string{"ID:2"}, "target_queue": "Q_ERR", "confirm": true})
	assert.True(t, r.OK, r.Error)
	assert.Equal(t, "usage", run("delete_jms_messages", map[string]any{"queue": "Q", "ids": []string{"ID:1"}}).ErrorCategory, "confirm required")
	r = run("delete_jms_messages", map[string]any{"queue": "Q", "ids": []string{"ID:1"}, "confirm": true})
	assert.True(t, r.OK, r.Error)
	r = run("set_jms_queue_state", map[string]any{"queue": "Q", "active": false})
	assert.True(t, r.OK, r.Error)
	assert.Equal(t, "usage", run("list_jms_messages", map[string]any{"queue": "nope"}).ErrorCategory)
	assert.Empty(t, mock.Queues["Q"].Messages)
	assert.Equal(t, "ID:2", mock.Queues["Q_ERR"].Messages[0].ID)
	assert.True(t, mock.Queues["Q"].Inactive)
	assert.Equal(t, 1, mock.Count("DELETE /api/v1/MessagingMessages"), "deleted once, only with confirm")
}

func TestFindMessagesByHeader(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	t0 := time.Now().Add(-time.Minute)
	mock.MessageLogSteps = [][]cpitest.MessageLog{{
		{Guid: "M1", Artifact: "Orders_In", Status: "COMPLETED", Start: t0, End: t0, Headers: map[string]string{"OrderNo": "4711"}},
		{Guid: "M2", Artifact: "Orders_In", Status: "FAILED", ErrorText: "boom", Start: t0, End: t0.Add(time.Second), Headers: map[string]string{"OrderNo": "4711"}},
		{Guid: "M3", Artifact: "Orders_In", Status: "COMPLETED", Start: t0, End: t0, Headers: map[string]string{"OrderNo": "4712"}},
	}}
	resp := session(t, mock, t.TempDir(),
		call(1, "find_messages_by_header", map[string]any{"name": "OrderNo", "value": "4711"}),
		call(2, "find_messages_by_header", map[string]any{"name": "OrderNo", "value": "4711", "statuses": []string{"FAILED"}}),
		call(3, "find_messages_by_header", map[string]any{"value": "4711"}),
	)
	res := toolResult(t, resp["1"]).StructuredContent
	require.True(t, res.OK, res.Error)
	logs := res.Result.(map[string]any)["logs"].([]any)
	require.Len(t, logs, 2)
	assert.Equal(t, "M2", logs[0].(map[string]any)["messageGuid"], "newest first")
	assert.Equal(t, "boom", logs[0].(map[string]any)["errorText"])
	assert.EqualValues(t, 1, toolResult(t, resp["2"]).StructuredContent.Result.(map[string]any)["total"])
	assert.Equal(t, "usage", toolResult(t, resp["3"]).StructuredContent.ErrorCategory, "name is required")
}
