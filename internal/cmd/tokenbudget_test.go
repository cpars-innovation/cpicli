package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// budgetFile holds the token budget of each golden MCP call (characters /
// 3.6, what a model reads: the text content, the instructions, the tool
// list's names, descriptions and schemas). Update it with
// go test ./internal/cmd -run TestTokenBudgets -update after a deliberate
// change, and say in the release notes why it grew.
const budgetFile = "testdata/token-budgets.json"

// budgetSlack is how much a call may grow over its budget before the test
// fails (payload timestamps of the demo tenant vary a little).
const budgetSlack = 1.10

func tokens(s string) int { return (len(s)*10 + 35) / 36 }

// TestTokenBudgets runs golden calls of cpictl mcp against the demo tenant
// and fails when one grows more than 10% over its budget.
func TestTokenBudgets(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(mock, "dev", time.Now()))

	measured := map[string]int{}
	for _, client := range []string{"claude-code", "other"} {
		calls := []struct {
			name string
			args map[string]any
		}{
			{"help", map[string]any{}},
			{"message_summary", map[string]any{"since": "7d"}},
			{"list_message_logs", map[string]any{"since": "7d", "top": 50}},
			{"list_message_logs", map[string]any{"since": "7d", "statuses": []string{"FAILED"}}},
			{"get_trace_tree", map[string]any{"message_guid": "AGD0000000000000000000000092"}},
			{"get_message_log", map[string]any{"message_guid": "AGD0000000000000000000000093", "include_steps": true}},
			{"list_runtime_artifacts", map[string]any{}},
			{"list_artifacts", map[string]any{"package_id": "Orders"}},
			{"list_packages", map[string]any{}},
		}
		lines := []string{listTools}
		for i, c := range calls {
			args, _ := json.Marshal(c.args)
			lines = append(lines, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, 10+i, c.name, args))
		}
		byID, _ := mcpSessionWith(t, mock, client, []string{"--mode", "full"}, lines...)

		instructions := byID["1"]["result"].(map[string]any)["instructions"].(string)
		measured[client+" instructions"] = tokens(instructions)
		var listed []map[string]any
		for _, tl := range byID["2"]["result"].(map[string]any)["tools"].([]any) {
			m := tl.(map[string]any)
			listed = append(listed, map[string]any{"name": m["name"], "description": m["description"], "input_schema": m["inputSchema"]})
		}
		data, _ := json.Marshal(listed)
		measured[client+" tool list"] = tokens(string(data))
		if client != "claude-code" {
			continue // the results are the same for every client
		}
		for i, c := range calls {
			r := byID[fmt.Sprint(10+i)]["result"].(map[string]any)
			require.Equal(t, false, r["isError"], "%s: %v", c.name, r)
			text := r["content"].([]any)[0].(map[string]any)["text"].(string)
			args, _ := json.Marshal(c.args)
			measured[c.name+" "+string(args)] = tokens(text)
		}
	}

	if *updateGolden {
		data, err := json.MarshalIndent(measured, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(budgetFile, append(data, '\n'), 0o644))
		return
	}
	data, err := os.ReadFile(budgetFile)
	require.NoError(t, err, "run go test ./internal/cmd -run TestTokenBudgets -update")
	var budgets map[string]int
	require.NoError(t, json.Unmarshal(data, &budgets))
	var keys []string
	for k := range measured {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var report []string
	for _, k := range keys {
		b, ok := budgets[k]
		if !assert.True(t, ok, "%s has no budget: run with -update", k) {
			continue
		}
		report = append(report, fmt.Sprintf("%-80s %6d / %6d", k, measured[k], b))
		assert.LessOrEqual(t, float64(measured[k]), float64(b)*budgetSlack,
			"%s grew over its token budget (%d > %d +10%%): make it smaller, or update the budget with -update and say why in the release notes", k, measured[k], b)
	}
	t.Log("tokens (measured / budget):\n" + strings.Join(report, "\n"))
}

// mcpSessionWith runs cpictl mcp against mock as client.
func mcpSessionWith(t *testing.T, mock *cpitest.Tenant, client string, args []string, lines ...string) (map[string]map[string]any, []string) {
	t.Helper()
	in, err := os.CreateTemp(t.TempDir(), "stdin")
	require.NoError(t, err)
	defer in.Close() // Windows cannot remove an open file
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"` + client + `","version":"1"}}}`
	_, _ = in.WriteString(strings.Join(append([]string{init}, lines...), "\n") + "\n")
	_, _ = in.Seek(0, 0)
	old := os.Stdin
	os.Stdin = in
	defer func() { os.Stdin = old }()
	root := t.TempDir()
	r := runMain(t, append(append([]string{"mcp", "--root", root}, args...), basicAuth(mock)...)...)
	require.Equal(t, 0, r.code, r.stderr)
	byID := map[string]map[string]any{}
	var notes []string
	for line := range strings.SplitSeq(strings.TrimSpace(r.stdout), "\n") {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m), line)
		if id, ok := m["id"]; ok {
			byID[jsonString(id)] = m
		} else {
			notes = append(notes, m["method"].(string))
		}
	}
	return byID, notes
}
