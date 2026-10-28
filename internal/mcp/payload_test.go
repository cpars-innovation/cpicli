package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// payloadServer serves a send_test_message and a get_message_content that
// return body (as the handlers do: cut at max_bytes), plus get_content.
func payloadServer(t *testing.T, root string, body []byte, sends *int) *Server {
	t.Helper()
	handler := func(withGuid bool) func(context.Context, json.RawMessage) (any, error) {
		return func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a struct {
				MaxBytes int `json:"max_bytes"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
			if withGuid {
				*sends++
				return map[string]any{"messageGuid": "G1", "httpStatus": 200, "response": ops.NewContent(body, a.MaxBytes)}, nil
			}
			return map[string]any{"id": "e1", "content": ops.NewContent(body, a.MaxBytes)}, nil
		}
	}
	tools := []Tool{
		{Name: "send_test_message", InputSchema: object(props{"max_bytes": maxBytesSchema()}), Handler: handler(true)},
		{Name: "get_message_content", InputSchema: object(props{"store_entry_id": str("ID"), "max_bytes": maxBytesSchema()}), Handler: handler(false)},
	}
	withSaveTo(tools)
	var srv *Server
	tools = append(tools, ContentTool(func() *ContentStore { return srv.Contents() }, func() string { return root }, false))
	srv = NewServer("cpicli", "test", "", tools)
	srv.Root = root
	return srv
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// A cut payload is stored whole and says first how to get the rest; nothing
// is lost above 1 MiB, and the store survives a restart of the server.
func TestPayloadStoredByReference(t *testing.T) {
	root := t.TempDir()
	for _, size := range []int{40_000, 2_400_000, 20_000_000} {
		body := []byte(strings.Repeat("<a>x</a>", size/8))
		sends := 0
		srv := payloadServer(t, root, body, &sends)
		resp := serveCompact(t, srv, call(1, "send_test_message", map[string]any{}))
		r := toolResult(t, resp["1"])
		require.False(t, r.IsError, r.Content[0].Text)
		c := r.StructuredContent.Result.(map[string]any)["response"].(map[string]any)
		ref := c["ref"].(string)
		assert.Len(t, c["text"], inlinePayloadBytes)
		assert.EqualValues(t, len(body), c["size"])
		assert.Contains(t, c["cut"], `get_content {"ref": "`+ref+`"}`)
		text := r.Content[0].Text
		assert.Less(t, strings.Index(text, `"cut"`), strings.Index(text, `"text"`), "the cut is said before the text")

		// a new server (restart) reads it from the store, by offset and whole
		srv = payloadServer(t, root, body, &sends)
		resp = serveCompact(t, srv,
			call(2, "get_content", map[string]any{"ref": ref, "offset": len(body) - 100}),
			call(3, "get_content", map[string]any{"ref": ref, "save_to": "out/body.xml"}),
			call(4, "get_message_content", map[string]any{"response_of": "G1", "save_to": "out/again.xml"}))
		require.False(t, toolResult(t, resp["2"]).IsError, toolResult(t, resp["2"]).Content[0].Text)
		part := toolResult(t, resp["2"]).StructuredContent.Result.(map[string]any)
		assert.Equal(t, string(body[len(body)-100:]), part["text"])
		assert.NotContains(t, part, "next", "the end reached")
		saved := toolResult(t, resp["3"]).StructuredContent
		require.True(t, saved.OK, saved.Error)
		assert.Equal(t, sha(body), saved.Result.(map[string]any)["sha256"])
		got, err := os.ReadFile(filepath.Join(root, "out", "body.xml"))
		require.NoError(t, err)
		assert.Equal(t, len(body), len(got))
		assert.Equal(t, sha(body), sha(got))
		again := toolResult(t, resp["4"]).StructuredContent
		require.True(t, again.OK, again.Error)
		assert.Equal(t, sha(body), again.Result.(map[string]any)["response"].(map[string]any)["sha256"])
		assert.Equal(t, 1, sends, "the message was sent once")
	}
	ignore, err := os.ReadFile(filepath.Join(root, ".cpi", "cache", ".gitignore"))
	require.NoError(t, err)
	assert.Contains(t, string(ignore), "*", "the cache stays out of Git")
}

// Small contents stay inline; max_bytes and save_to work on any payload
// tool; paths stay under the root; a redacting server stores nothing.
func TestPayloadInlineSaveAndRedaction(t *testing.T) {
	root := t.TempDir()
	body := []byte("<persisted/>")
	sends := 0
	srv := payloadServer(t, root, body, &sends)
	resp := serveCompact(t, srv,
		call(1, "get_message_content", map[string]any{"store_entry_id": "e1"}),
		call(2, "get_message_content", map[string]any{"store_entry_id": "e1", "max_bytes": 5}),
		call(3, "get_message_content", map[string]any{"store_entry_id": "e1", "save_to": "../outside.xml"}),
		call(4, "get_message_content", map[string]any{"store_entry_id": "e1", "max_bytes": 2 << 20}),
		call(5, "get_content", map[string]any{"ref": "c:nope"}))
	assert.JSONEq(t, `{"id":"e1","content":{"size":12,"text":"<persisted/>"}}`, toolResult(t, resp["1"]).Content[0].Text)
	cut := toolResult(t, resp["2"]).StructuredContent.Result.(map[string]any)["content"].(map[string]any)
	assert.Equal(t, "<pers", cut["text"])
	assert.Contains(t, cut["cut"], "5 of 12 bytes shown")
	for _, id := range []string{"3", "4", "5"} {
		assert.Equal(t, "usage", toolResult(t, resp[id]).StructuredContent.ErrorCategory, id)
	}

	srv = payloadServer(t, t.TempDir(), body, &sends)
	srv.Redactor.RedactPayloads = true
	resp = serveCompact(t, srv,
		call(6, "get_message_content", map[string]any{"store_entry_id": "e1", "save_to": "b.xml"}),
		call(7, "get_message_content", map[string]any{"store_entry_id": "e1", "max_bytes": 5}))
	assert.Contains(t, toolResult(t, resp["6"]).StructuredContent.Error, "redacts")
	assert.NotContains(t, toolResult(t, resp["7"]).Content[0].Text, `"ref"`, "nothing stored")
	_, err := os.Stat(filepath.Join(srv.Root, ".cpi", "cache"))
	assert.True(t, os.IsNotExist(err))
}
