package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type rpcMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// serveLines runs a session and returns every message the server wrote, in
// order (responses and notifications).
func serveLines(t *testing.T, srv *Server, msgs ...string) []rpcMessage {
	t.Helper()
	var out bytes.Buffer
	require.NoError(t, srv.Serve(context.Background(), strings.NewReader(strings.Join(msgs, "\n")+"\n"), &out))
	var res []rpcMessage
	for line := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
		var m rpcMessage
		require.NoError(t, json.Unmarshal([]byte(line), &m), line)
		res = append(res, m)
	}
	return res
}

func callWithToken(id int, tool string, args any, token any) string {
	a, _ := json.Marshal(args)
	tok, _ := json.Marshal(token)
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s,"_meta":{"progressToken":%s}}}`, id, tool, a, tok)
}

type progressParams struct {
	ProgressToken json.RawMessage `json:"progressToken"`
	Progress      float64         `json:"progress"`
	Total         float64         `json:"total"`
	Message       string          `json:"message"`
}

// progressBefore returns the progress notifications for token written before
// the response with id, and fails if none or if any follows the response.
func progressBefore(t *testing.T, msgs []rpcMessage, id, token string) []progressParams {
	t.Helper()
	var got []progressParams
	answered := false
	for _, m := range msgs {
		if string(m.ID) == id {
			answered = true
			continue
		}
		if m.Method != "notifications/progress" {
			continue
		}
		var p progressParams
		require.NoError(t, json.Unmarshal(m.Params, &p))
		if string(p.ProgressToken) != token {
			continue
		}
		assert.False(t, answered, "progress after the result: %s", p.Message)
		got = append(got, p)
	}
	require.True(t, answered, "no response to %s", id)
	require.NotEmpty(t, got, "no progress notification before the result")
	for i := 1; i < len(got); i++ {
		assert.Greater(t, got[i].Progress, got[i-1].Progress, "progress must increase")
	}
	return got
}

func TestProgressNotifications(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"Orders": {Type: "Integration", DesignVersion: "1.0.1", Package: "Sales", Name: "Orders",
			TaskStatuses:      []string{"DEPLOYING", "SUCCESS"},
			AfterDeploy:       []*cpitest.Runtime{{Version: "1.0.1", Status: "STARTING"}, {Version: "1.0.1", Status: "STARTED", DeployedOn: time.Now().Add(time.Hour)}},
			GuidelineStatuses: []string{"IN_PROGRESS", "PASS"}},
	})
	mock.Packages = []cpitest.Package{{ID: "Sales", Name: "Sales"}}
	srv := NewServer("cpicli", "test", Instructions, Tools(Config{Exe: mock.Executer(), Root: t.TempDir(),
		PollInterval: time.Millisecond, MaxChecks: 10, LogPollInterval: time.Millisecond}))
	msgs := serveLines(t, srv,
		callWithToken(1, "deploy", map[string]any{"artifact_ids": []string{"Orders"}}, "dep"),
		callWithToken(2, "check_guidelines", map[string]any{"artifact_id": "Orders"}, 7),
		callWithToken(3, "discover_tenant", map[string]any{}, "disc"),
		call(4, "list_packages", map[string]any{}),
	)

	deploy := progressBefore(t, msgs, "1", `"dep"`)
	assert.Equal(t, "artifact Orders: build and deploy task DEPLOYING, check 1/10", deploy[0].Message)
	assert.Equal(t, float64(10), deploy[0].Total)
	assert.Contains(t, deploy[len(deploy)-1].Message, "STARTED")

	guidelines := progressBefore(t, msgs, "2", `7`)
	assert.Contains(t, guidelines[0].Message, "guideline check of Orders")

	discover := progressBefore(t, msgs, "3", `"disc"`)
	assert.Equal(t, "listing packages", discover[0].Message)
	assert.Contains(t, discover[len(discover)-1].Message, "analysed integration flow Orders (1/1)")

}

func TestNoProgressWithoutToken(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"Orders": {Type: "Integration", DesignVersion: "1"}})
	msgs := serveLines(t, NewServer("cpicli", "test", Instructions, Tools(Config{Exe: mock.Executer(), Root: t.TempDir(),
		PollInterval: time.Millisecond, MaxChecks: 3})),
		call(1, "deploy", map[string]any{"artifact_ids": []string{"Orders"}}))
	require.Len(t, msgs, 1)
	assert.Equal(t, "1", string(msgs[0].ID))
}

// A tools/call followed at once by notifications/cancelled stops the tool.
func TestCancelledStopsTheTool(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	srv := NewServer("cpicli", "test", Instructions, Tools(Config{Exe: mock.Executer(), Root: t.TempDir(), LogPollInterval: 5 * time.Millisecond}))
	r, w := io.Pipe()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- srv.Serve(context.Background(), r, &out) }()
	_, _ = io.WriteString(w, call(1, "list_message_logs", map[string]any{"artifact_id": "Nothing", "since": "2026-01-01T00:00:00Z", "wait_seconds": 600})+"\n")
	_, _ = io.WriteString(w, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1,"reason":"user"}}`+"\n")
	_ = w.Close()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled tool call did not stop")
	}
	assert.Contains(t, out.String(), `"isError":true`)
}
