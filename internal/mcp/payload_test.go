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
