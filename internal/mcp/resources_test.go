package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readReq(id int, uri string) string {
	u, _ := json.Marshal(uri)
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"resources/read","params":{"uri":%s}}`, id, u)
}

func TestResources(t *testing.T) {
	e := newLoopEnv(t)
	start := e.call("loop_start", map[string]any{"goal": "fix the mapping"})
	require.True(t, start.OK, start.Error)
	id := start.Result.(map[string]any)["loopId"].(string)
	require.True(t, e.call("loop_end", map[string]any{"loop_id": id, "outcome": "done"}).OK)
	// a file outside the root that traversal would reach
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(e.root), "secret.md"), []byte("secret"), 0o644))
	e.srv.Root = e.root

	resp := serve(t, e.srv,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"resources/list"}`,
		readReq(3, "cpictl://loops/"+id),
		readReq(4, "cpictl://skills/cpi-build"),
		readReq(5, "cpictl://workflows"),
		readReq(6, "cpictl://loops/../../secret"),
		readReq(7, "cpictl://loops/..%2F..%2Fsecret"),
		readReq(8, "cpictl://nothing/here"),
		readReq(9, "cpictl://skills/no-such-skill"),
		readReq(10, "cpictl://loops/20990101-000000-00000000"),
	)

	var init struct {
		Capabilities map[string]any `json:"capabilities"`
	}
	require.NoError(t, json.Unmarshal(resp["1"].Result, &init))
	assert.Contains(t, init.Capabilities, "resources")

	var list struct {
		Resources []Resource `json:"resources"`
	}
	require.Nil(t, resp["2"].Error)
	require.NoError(t, json.Unmarshal(resp["2"].Result, &list))
	uris := map[string]Resource{}
	for _, r := range list.Resources {
		uris[r.URI] = r
	}
	require.Contains(t, uris, "cpictl://loops/"+id)
	assert.Equal(t, "text/markdown", uris["cpictl://loops/"+id].MIMEType)
	assert.Contains(t, uris, "cpictl://skills/cpi-build")
	assert.Contains(t, uris, "cpictl://workflows")

	type contents struct {
		Contents []struct {
			URI      string `json:"uri"`
			MIMEType string `json:"mimeType"`
			Text     string `json:"text"`
		} `json:"contents"`
	}
	read := func(id string) contents {
		t.Helper()
		require.Nil(t, resp[id].Error, id)
		var c contents
		require.NoError(t, json.Unmarshal(resp[id].Result, &c))
		require.Len(t, c.Contents, 1)
		return c
	}
	journal := read("3").Contents[0]
	assert.Equal(t, "text/markdown", journal.MIMEType)
	assert.Contains(t, journal.Text, "fix the mapping")
	assert.Contains(t, read("4").Contents[0].Text, "name: cpi-build")
	assert.Contains(t, read("5").Contents[0].Text, "cpi-build")

	for _, id := range []string{"6", "7"} {
		require.NotNil(t, resp[id].Error, "path traversal must be refused (%s)", id)
		assert.Equal(t, codeInvalidParams, resp[id].Error.Code)
	}
	for _, id := range []string{"8", "9", "10"} {
		require.NotNil(t, resp[id].Error, id)
		assert.Equal(t, codeResourceNotFound, resp[id].Error.Code)
	}
}
