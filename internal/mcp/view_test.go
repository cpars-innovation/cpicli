package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func viewOf(t *testing.T, tool string, full bool, value string) string {
	t.Helper()
	srv := NewServer("x", "t", "", nil)
	srv.Root = t.TempDir()
	v, err := srv.view(tool, full, payloadCall{inline: inlinePayloadBytes}, json.RawMessage(value))
	require.NoError(t, err)
	return string(encodeJV(v))
}

// The view keeps the order of keys, leaves out empty values and writes long
// lists of objects as columns; detail "full" keeps the shape.
func TestViewCompact(t *testing.T) {
	in := `{"z":1,"a":"","b":[],"c":{},"d":null,"e":false,"f":0,"g":{"h":"","i":"x"},` +
		`"rows":[{"id":"1","s":"OK"},{"id":"2","s":"OK","x":"y"},{"id":"3"},{"id":"4","s":""},{"id":"5","s":"FAILED"}],` +
		`"short":[{"id":"1"},{"id":"2"}],` +
		`"tree":[{"n":1,"children":[{"n":2}]},{"n":3},{"n":4},{"n":5},{"n":6}]}`
	assert.Equal(t, `{"z":1,"e":false,"f":0,"g":{"i":"x"},`+
		`"rows":{"columns":["id","s","x"],"rows":[["1","OK",null],["2","OK","y"],["3",null,null],["4",null,null],["5","FAILED",null]]},`+
		`"short":[{"id":"1"},{"id":"2"}],`+
		`"tree":[{"n":1,"children":[{"n":2}]},{"n":3},{"n":4},{"n":5},{"n":6}]}`, viewOf(t, "x", false, in))
	assert.JSONEq(t, in, viewOf(t, "x", true, in), "full: the result as the tool returned it")
	assert.Equal(t, `[]`, viewOf(t, "x", false, `[]`), "the top-level value stays")
}

// message_summary: one group for an error a caller passes on, reported where
// it failed, without occurrences and buckets.
func TestViewMessageSummaryMergesPassedOnErrors(t *testing.T) {
	in := `{"flows":[],"edges":[{"from":"Orders_Route","to":"Billing_In"},{"from":"Billing_In","to":"Billing_Post"}],` +
		`"errors":[` +
		`{"fingerprint":"a","artifact":"Billing_In","step":"EndEvent_1","sample":"HTTP 500: period closed","count":2,"first":"2026-10-27T14:23:41Z","last":"2026-10-27T15:58:40Z","messageGuid":"G92","occurrences":[{"time":"x"}],"buckets":[0,1]},` +
		`{"fingerprint":"b","artifact":"Billing_Post","step":"ServiceTask_0","sample":"HTTP 500: period closed","count":2,"first":"2026-10-27T14:23:42Z","last":"2026-10-27T15:58:41Z","messageGuid":"G93","occurrences":[],"buckets":[0,1]},` +
		`{"fingerprint":"c","artifact":"Orders_IDoc_In","step":"EndEvent_1","sample":"HTTP 503","count":2,"first":"2026-10-27T23:21:43Z","last":"2026-10-28T09:16:37Z","messageGuid":"G578"}` +
		`],"bucketSeconds":3600}`
	var got struct {
		Errors []map[string]any `json:"errors"`
		Detail string           `json:"detail"`
	}
	require.NoError(t, json.Unmarshal([]byte(viewOf(t, "message_summary", false, in)), &got))
	require.Len(t, got.Errors, 2)
	g := got.Errors[0]
	assert.Equal(t, "Billing_Post", g["artifact"])
	assert.Equal(t, "ServiceTask_0", g["step"], "where it really failed")
	assert.Equal(t, []any{"Billing_In", "Billing_Post"}, g["flows"])
	assert.Equal(t, "2026-10-27T14:23:41Z", g["first"])
	assert.Equal(t, "2026-10-27T15:58:41Z", g["last"])
	assert.NotContains(t, g, "occurrences")
	assert.NotContains(t, g, "buckets")
	assert.Equal(t, "Orders_IDoc_In", got.Errors[1]["artifact"])
	assert.Contains(t, got.Detail, "detail=full")

	full := viewOf(t, "message_summary", true, in)
	assert.Contains(t, full, `"occurrences"`)
	assert.Contains(t, full, `"fingerprint":"a"`)
}

// The default reply is the result alone; errors carry category, text and
// exit code; --verbose-results and --structured-content restore the
// envelope.
func TestResultEnvelope(t *testing.T) {
	tools := []Tool{
		{Name: "ok", InputSchema: object(nil), Handler: func(context.Context, json.RawMessage) (any, error) { return map[string]any{"a": 1}, nil }},
		{Name: "bad", InputSchema: object(nil), Handler: func(context.Context, json.RawMessage) (any, error) {
			return nil, errUsageForTest
		}},
	}
	srv := NewServer("x", "t", "", tools)
	resp := serveCompact(t, srv, call(1, "ok", map[string]any{}), call(2, "bad", map[string]any{}))
	assert.Equal(t, `{"a":1}`, toolResult(t, resp["1"]).Content[0].Text)
	assert.NotContains(t, string(resp["1"].Result), "structuredContent")
	bad := toolResult(t, resp["2"])
	assert.True(t, bad.IsError)
	assert.Equal(t, `{"errorCategory":"usage","error":"wrong","exitCode":2}`, bad.Content[0].Text)

	srv.VerboseResults, srv.StructuredContent = true, true
	resp = serveCompact(t, srv, call(3, "ok", map[string]any{}))
	assert.Contains(t, toolResult(t, resp["3"]).Content[0].Text, `"ok":true,"exitCode":0,"durationMs":`)
	assert.Contains(t, string(resp["3"].Result), `"structuredContent"`)
}

var errUsageForTest = usageErr("wrong")

func usageErr(msg string) error { return output.Usagef("%s", msg) }
