package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readEvents(t *testing.T, data string) []map[string]any {
	t.Helper()
	var events []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		var ev map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &ev), line)
		events = append(events, ev)
	}
	return events
}

// --events writes start, one item per artifact and end as JSON lines (Studio C7).
func TestEventsDeploy(t *testing.T) {
	m := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", DesignVersion: "1.0.2", TaskStatuses: []string{"SUCCESS"},
			AfterDeploy: []*cpitest.Runtime{{Version: "1.0.2", Status: "STARTED", DeployedOn: time.Now()}}},
	})
	file := filepath.Join(t.TempDir(), "events.jsonl")
	res := runMain(t, append([]string{"deploy", "--artifact-ids", "A,B", "--delay-length", "0", "--max-check-limit", "2",
		"--events", file}, basicAuth(m)...)...)
	assert.Equal(t, 7, res.code)
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	events := readEvents(t, string(data))
	require.Len(t, events, 4)
	for i, ev := range events {
		assert.EqualValues(t, i+1, ev["seq"])
		assert.Equal(t, "deploy", ev["command"])
		assert.EqualValues(t, output.SchemaVersion, ev["schemaVersion"])
	}
	assert.Equal(t, "start", events[0]["event"])
	assert.Equal(t, "item", events[1]["event"])
	assert.Equal(t, "A", events[1]["data"].(map[string]any)["id"])
	assert.Equal(t, "B", events[2]["data"].(map[string]any)["id"])
	assert.Equal(t, "end", events[3]["event"])
	assert.Equal(t, map[string]any{"exitCode": 7.0, "errorCategory": "partial", "durationMs": events[3]["data"].(map[string]any)["durationMs"]}, events[3]["data"])
}

func TestEventsSnapshotToStderr(t *testing.T) {
	m := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(m, "dev", time.Now()))
	dir := t.TempDir()
	res := runMain(t, append([]string{"snapshot", "--dir-git-repo", dir, "--git-skip-commit", "--ids-include", "Billing",
		"--events", "-", "--output", "json"}, basicAuth(m)...)...)
	require.Equal(t, 0, res.code, res.stderr)
	var items []map[string]any
	var last map[string]any
	for _, line := range strings.Split(strings.TrimSpace(res.stderr), "\n") {
		var ev map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &ev), line)
		if ev["event"] == nil {
			continue // a log line
		}
		last = ev
		if ev["event"] == "item" {
			items = append(items, ev["data"].(map[string]any))
		}
	}
	require.NotEmpty(t, items)
	for _, it := range items {
		assert.Equal(t, "Billing", it["package"])
		assert.NotEmpty(t, it["status"])
	}
	assert.Equal(t, "end", last["event"])
}

func TestEventsBadPath(t *testing.T) {
	res := runMain(t, "skills", "list", "--events", filepath.Join(t.TempDir(), "missing", "dir", "e.jsonl"))
	assert.Equal(t, 2, res.code)
}
