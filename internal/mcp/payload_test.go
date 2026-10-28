package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func payloadServer(t *testing.T, body []byte) (*Server, string) {
	t.Helper()
	tools := []Tool{{Name: "get_message_content", InputSchema: object(props{"id": str("ID"), "max_bytes": maxBytesSchema()}),
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a struct {
				ID       string `json:"id"`
				MaxBytes int    `json:"max_bytes"`
			}
			if err := decode(raw, &a); err != nil {
				return nil, err
			}
			return map[string]any{"id": a.ID, "content": ops.NewContent(body, a.MaxBytes)}, nil
		}}}
	withSaveTo(tools)
	srv := NewServer("cpicli", "test", "", tools)
	srv.Root = t.TempDir()
	return srv, srv.Root
}

func TestPayloadInlineDefaultAndSaveTo(t *testing.T) {
	body := []byte(strings.Repeat("<a>x</a>", 5000)) // 40000 bytes
	srv, root := payloadServer(t, body)
	resp := serve(t, srv,
		call(1, "get_message_content", map[string]any{"id": "1"}),
		call(2, "get_message_content", map[string]any{"id": "1", "max_bytes": 50000}),
		call(3, "get_message_content", map[string]any{"id": "1", "save_to": "out/body.xml"}),
		call(4, "get_message_content", map[string]any{"id": "1", "save_to": "../outside.xml"}),
	)
	inline := toolResult(t, resp["1"]).StructuredContent.Result.(map[string]any)["content"].(map[string]any)
	assert.Equal(t, true, inline["truncated"], "cut to the inline default, visibly")
	assert.Len(t, inline["text"], inlinePayloadBytes)
	assert.EqualValues(t, len(body), inline["size"])
	whole := toolResult(t, resp["2"]).StructuredContent.Result.(map[string]any)["content"].(map[string]any)
	assert.Len(t, whole["text"], len(body), "max_bytes still raises the limit")

	saved := toolResult(t, resp["3"]).StructuredContent
	require.True(t, saved.OK, saved.Error)
	c := saved.Result.(map[string]any)["content"].(map[string]any)
	assert.NotContains(t, c, "text")
	assert.Equal(t, "out/body.xml", c["savedTo"], "root-relative path")
	assert.Len(t, c["sha256"], 64)
	got, err := os.ReadFile(filepath.Join(root, "out", "body.xml"))
	require.NoError(t, err)
	assert.Equal(t, body, got)
	assert.Equal(t, "usage", toolResult(t, resp["4"]).StructuredContent.ErrorCategory, "only under the root")

	srv.Redactor.RedactPayloads = true
	resp = serve(t, srv, call(5, "get_message_content", map[string]any{"id": "1", "save_to": "b.xml"}))
	assert.Contains(t, toolResult(t, resp["5"]).StructuredContent.Error, "redacts")
}

// A cut send_test_message response says how to get the rest, and
// get_message_content response_of returns or saves it without sending the
// message again.
func TestSendResponseKeptForResponseOf(t *testing.T) {
	body := []byte(strings.Repeat("<a>x</a>", 5000)) // 40000 bytes
	sends := 0
	tools := []Tool{
		{Name: "send_test_message", InputSchema: object(props{"max_bytes": maxBytesSchema()}),
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					MaxBytes int `json:"max_bytes"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				sends++
				return map[string]any{"messageGuid": "G1", "httpStatus": 200, "response": ops.NewContent(body, a.MaxBytes)}, nil
			}},
		{Name: "get_message_content", InputSchema: object(props{"attachment_id": str("ID"), "max_bytes": maxBytesSchema()}),
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) { return nil, nil }},
	}
	withSaveTo(tools)
	srv := NewServer("cpicli", "test", "", tools)
	srv.Root = t.TempDir()

	resp := serve(t, srv, call(1, "send_test_message", map[string]any{}))
	r := toolResult(t, resp["1"])
	c := r.StructuredContent.Result.(map[string]any)["response"].(map[string]any)
	assert.Len(t, c["text"], inlinePayloadBytes)
	assert.EqualValues(t, len(body), c["size"])
	assert.Contains(t, c["cut"], `"response_of": "G1"`)
	assert.Less(t, strings.Index(r.Content[0].Text, `"cut"`), strings.Index(r.Content[0].Text, `"text"`), "the cut is said before the text")

	resp = serve(t, srv,
		call(2, "get_message_content", map[string]any{"response_of": "G1", "save_to": "resp.xml"}),
		call(3, "get_message_content", map[string]any{"response_of": "G1", "max_bytes": 50000}),
		call(4, "get_message_content", map[string]any{"response_of": "nope"}))
	saved := toolResult(t, resp["2"]).StructuredContent
	require.True(t, saved.OK, saved.Error)
	got, err := os.ReadFile(filepath.Join(srv.Root, "resp.xml"))
	require.NoError(t, err)
	assert.Equal(t, body, got)
	whole := toolResult(t, resp["3"]).StructuredContent.Result.(map[string]any)["response"].(map[string]any)
	assert.Len(t, whole["text"], len(body))
	assert.Equal(t, "usage", toolResult(t, resp["4"]).StructuredContent.ErrorCategory)
	assert.Equal(t, 1, sends, "the message was sent once")
}
