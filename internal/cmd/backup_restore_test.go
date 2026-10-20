package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// backup create, then restore: --confirm is required to write, the dry run
// writes nothing and saves the plan.
func TestBackupRestoreCommand(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", Package: "P", DesignVersion: "1.0.0", Name: "A", Zip: flowZip(t, "A", "v1")},
	})
	mock.Packages = []cpitest.Package{{ID: "P", Name: "P", Version: "1.0.0", ShortText: "p"}}
	dir := t.TempDir()
	archive := filepath.Join(dir, "b.tar.gz")
	r := runMain(t, append([]string{"backup", "create", "--output-file", archive}, basicAuth(mock)...)...)
	require.Equal(t, 0, r.code, r.stderr)

	r = runMain(t, append([]string{"backup", "restore", archive}, basicAuth(mock)...)...)
	assert.Equal(t, 2, r.code, "writing needs --confirm")
	assert.Contains(t, r.stderr, "--dry-run first, then with --confirm")

	delete(mock.Artifacts, "A") // lost since the backup
	plan := filepath.Join(dir, "plans", "restore-plan.md")
	r = runMain(t, append([]string{"backup", "restore", archive, "--dry-run", "--plan-file", plan, "--output", "json"}, basicAuth(mock)...)...)
	require.Equal(t, 0, r.code, r.stderr)
	assert.NotContains(t, mock.Artifacts, "A", "dry run writes nothing")
	var env struct {
		Result struct {
			DryRun bool `json:"dryRun"`
			Steps  []struct{ Area, Object, Action string }
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &env))
	assert.True(t, env.Result.DryRun)
	assert.Contains(t, env.Result.Steps, struct{ Area, Object, Action string }{"artifacts", "A", "create"})
	md, err := os.ReadFile(plan)
	require.NoError(t, err)
	assert.Contains(t, string(md), "# Restore plan (dry run: nothing was changed)")
	assert.Contains(t, string(md), "| `A` | create |")

	report := filepath.Join(dir, "report.json")
	r = runMain(t, append([]string{"backup", "restore", archive, "--confirm", "--only", "packages,artifacts", "--plan-file", report}, basicAuth(mock)...)...)
	require.Equal(t, 0, r.code, r.stderr)
	assert.Contains(t, mock.Artifacts, "A", "restored")
	data, err := os.ReadFile(report)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"dryRun": false`)

	r = runMain(t, append([]string{"backup", "restore", archive, "--dry-run", "--only", "everything"}, basicAuth(mock)...)...)
	assert.Equal(t, 2, r.code)
}
