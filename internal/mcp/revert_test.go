package mcp

import (
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogLevelRevert(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": {Type: "Integration", DesignVersion: "1"}, "B": {Type: "Integration", DesignVersion: "1"}})
	now := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	rev := NewLogLevelReverter(mock.Executer())
	rev.Now = func() time.Time { return now }
	srv := NewServer("cpicli", "test", Instructions, Tools(Config{Exe: mock.Executer(), Root: t.TempDir(), LogLevels: rev}))

	resp := serve(t, srv, call(1, "set_log_level", map[string]any{"artifact_id": "A", "level": "TRACE"}),
		call(2, "set_log_level", map[string]any{"artifact_id": "B", "level": "DEBUG", "revert_after_minutes": 0}))
	res := toolResult(t, resp["1"]).StructuredContent
	require.True(t, res.OK, res.Error)
	assert.Equal(t, "2026-10-05T10:10:00Z", res.Result.(map[string]any)["revertsAt"])
	assert.NotContains(t, toolResult(t, resp["2"]).StructuredContent.Result.(map[string]any), "revertsAt")
	assert.Equal(t, "TRACE", mock.LogLevels["A"]["mplLogLevel"])

	// before the deadline nothing happens; the first call after it reverts
	serve(t, srv, call(3, "list_packages", map[string]any{}))
	assert.Equal(t, "TRACE", mock.LogLevels["A"]["mplLogLevel"])
	now = now.Add(11 * time.Minute)
	serve(t, srv, call(4, "list_packages", map[string]any{}))
	assert.Equal(t, "INFO", mock.LogLevels["A"]["mplLogLevel"])
	assert.Equal(t, "DEBUG", mock.LogLevels["B"]["mplLogLevel"], "revert_after_minutes 0 never reverts")

	// shutdown reverts what is still pending
	serve(t, srv, call(5, "set_log_level", map[string]any{"artifact_id": "A", "level": "TRACE", "revert_after_minutes": 60}))
	rev.RevertAll()
	assert.Equal(t, "INFO", mock.LogLevels["A"]["mplLogLevel"])
}
