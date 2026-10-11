package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cpars-innovation/cpicli/pkg/catalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComponentsImportAndList(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "components.json")
	meta, err := filepath.Abs(filepath.Join("..", "..", "pkg", "catalog", "testdata", "editor-metadata.json"))
	require.NoError(t, err)
	r := runMainWith(t, func() {}, "components", "import", meta, "--file", file, "--tenant", "dev", "--output", "json")
	require.Equal(t, 0, r.code, r.stderr)
	c, err := catalog.Load(file)
	require.NoError(t, err)
	assert.Equal(t, "dev", c.Tenant)
	assert.NotEmpty(t, c.Adapters)

	r = runMainWith(t, func() {}, "components", "list", "--file", file, "--name", "groovy", "--output", "json")
	require.Equal(t, 0, r.code, r.stderr)
	var env struct {
		Result struct {
			Steps []catalog.Step `json:"steps"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &env), r.stdout)
	require.Len(t, env.Result.Steps, 1)
	assert.Equal(t, "2.0.0", env.Result.Steps[0].Version)

	require.NoError(t, os.Remove(file))
	r = runMainWith(t, func() {}, "components", "list", "--file", file)
	assert.Equal(t, 2, r.code)
	assert.Contains(t, r.stderr, "no component catalogue")
}
