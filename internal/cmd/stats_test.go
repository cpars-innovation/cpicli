package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runWithStats(t *testing.T, file string, args ...string) (int, string) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Setenv("CPICTL_STATS_FILE", file)
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), args, &stdout, &stderr, "test", "test")
	return code, stdout.String()
}

func TestStatsRecordsCommands(t *testing.T) {
	cpitest.SetHome(t, t.TempDir())
	file := filepath.Join(t.TempDir(), "stats.jsonl")
	runWithStats(t, file, "skills", "list")
	runWithStats(t, file, "skills", "list")
	code, _ := runWithStats(t, file, "skills", "show", "no-such-skill")
	assert.NotEqual(t, 0, code)
	runWithStats(t, file, "--help") // not a run
	runWithStats(t, file, "stats")  // not recorded itself

	code, out := runWithStats(t, file, "stats", "--output", "json")
	require.Equal(t, 0, code)
	var env struct {
		Result statsResult `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &env))
	assert.Equal(t, 3, env.Result.Entries)
	require.Len(t, env.Result.Commands, 2)
	assert.Equal(t, "skills list", env.Result.Commands[0].Command)
	assert.Equal(t, 2, env.Result.Commands[0].Runs)
	assert.Equal(t, "skills show", env.Result.Commands[1].Command)
	assert.Equal(t, 1, env.Result.Commands[1].Failed)

	code, _ = runWithStats(t, file, "stats", "--since", "nope")
	assert.Equal(t, 2, code)

	code, out = runWithStats(t, file, "stats", "--reset", "--output", "json")
	require.Equal(t, 0, code)
	code, out = runWithStats(t, file, "stats", "--output", "json")
	require.Equal(t, 0, code)
	require.NoError(t, json.Unmarshal([]byte(out), &env))
	assert.Equal(t, 0, env.Result.Entries)
}

func TestParseSince(t *testing.T) {
	d, err := parseSince("7d")
	require.NoError(t, err)
	assert.Equal(t, "168h0m0s", d.String())
	_, err = parseSince("-1h")
	assert.Error(t, err)
}
