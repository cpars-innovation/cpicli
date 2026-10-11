package mcp

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// progressKey carries the progress callback of a tool call in its context.
type progressKey struct{}

// progressFrom returns the progress callback of the tool call ctx belongs
// to; nil when the client asked for no progress (no _meta.progressToken).
// Tools pass it to the ops options of long operations.
func progressFrom(ctx context.Context) ops.ProgressFunc {
	fn, _ := ctx.Value(progressKey{}).(ops.ProgressFunc)
	return fn
}

// progressToken returns _meta.progressToken of tools/call params (a string
// or a number; nil when absent).
func progressToken(params json.RawMessage) json.RawMessage {
	var p struct {
		Meta struct {
			ProgressToken json.RawMessage `json:"progressToken"`
		} `json:"_meta"`
	}
	if json.Unmarshal(params, &p) != nil || len(p.Meta.ProgressToken) == 0 || string(p.Meta.ProgressToken) == "null" {
		return nil
	}
	return p.Meta.ProgressToken
}

// withProgress returns ctx with a callback that sends notifications/progress
// for token, and a stop function to call before the result is sent: later
// reports are dropped, so no notification follows the response. Progress
// values only grow, as MCP requires.
func (s *Server) withProgress(ctx context.Context, token json.RawMessage) (context.Context, func()) {
	var mu sync.Mutex
	last, stopped := 0, false
	fn := ops.ProgressFunc(func(p ops.Progress) {
		mu.Lock()
		defer mu.Unlock()
		if stopped {
			return
		}
		last = max(p.Step, last+1)
		params := map[string]any{"progressToken": token, "progress": last, "message": p.Message}
		if p.Total >= last {
			params["total"] = p.Total
		}
		s.writeMessage(map[string]any{"jsonrpc": "2.0", "method": "notifications/progress", "params": params})
	})
	return context.WithValue(ctx, progressKey{}, fn), func() {
		mu.Lock()
		stopped = true
		mu.Unlock()
	}
}
