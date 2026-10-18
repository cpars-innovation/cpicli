package schemagen

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cmd"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite schemas/")

func TestSchemasUpToDate(t *testing.T) {
	files, err := Generate(filepath.Join("..", ".."), cmd.NewCLI("dev"), output.SchemaVersion)
	require.NoError(t, err)
	dir := filepath.Join("..", "..", "schemas")
	if *update {
		old := Files{}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			require.NoError(t, err)
			old[e.Name()] = data
		}
		var oldVersion struct {
			SchemaVersion int `json:"schemaVersion"`
		}
		if data, ok := old["index.json"]; ok {
			require.NoError(t, json.Unmarshal(data, &oldVersion))
		}
		if b := Breaking(old, files); len(b) > 0 && oldVersion.SchemaVersion == output.SchemaVersion {
			t.Fatalf("breaking result changes need a new output.SchemaVersion (now %d) and a CHANGELOG entry:\n%s",
				output.SchemaVersion, strings.Join(b, "\n"))
		}
		require.NoError(t, os.RemoveAll(dir))
		require.NoError(t, os.MkdirAll(dir, 0o755))
		for name, data := range files {
			require.NoError(t, os.WriteFile(filepath.Join(dir, name), data, 0o644))
		}
		return
	}
	for name, data := range files {
		got, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err, "schemas/%s missing: go test ./internal/schemagen -update", name)
		require.Equal(t, string(data), string(got), "schemas/%s is stale: go test ./internal/schemagen -update", name)
	}
}
