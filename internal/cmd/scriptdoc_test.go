package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScriptDocHook(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "poor.groovy"), []byte("def Message processData(Message m) { m }\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "good.groovy"),
		[]byte("// Writes the order number to the message log.\ndef Message processData(Message m) { m }\n"), 0o644))
	event := func(file string) *strings.Reader {
		b, _ := json.Marshal(map[string]any{"cwd": dir, "tool_input": map[string]string{"file_path": file}})
		return strings.NewReader(string(b))
	}

	msg := scriptDocHook(event("poor.groovy"))
	assert.Contains(t, msg, "the comments of poor.groovy are incomplete")
	assert.Contains(t, msg, "- no comment says what the script does")
	assert.Empty(t, scriptDocHook(event("good.groovy")))
	assert.Empty(t, scriptDocHook(event("README.md")), "not a script")
	assert.Empty(t, scriptDocHook(event("missing.groovy")), "unreadable files are not reported")
	assert.Empty(t, scriptDocHook(strings.NewReader("not json")))
}

// The plugin's hook calls script-doc --hook after file writes.
func TestPluginHook(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "plugin", "hooks", "hooks.json"))
	require.NoError(t, err)
	var cfg struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	require.NoError(t, json.Unmarshal(data, &cfg))
	post := cfg.Hooks["PostToolUse"]
	require.Len(t, post, 1)
	assert.Equal(t, "Write|Edit|MultiEdit", post[0].Matcher)
	require.Len(t, post[0].Hooks, 1)
	assert.Contains(t, post[0].Hooks[0].Command, "script-doc --hook")
}

func TestScriptDocCommand(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "x.groovy")
	require.NoError(t, os.WriteFile(file, []byte("def Message processData(Message m) { m }\n"), 0o644))
	r := runMain(t, "script-doc", "--output", "json", file)
	assert.Equal(t, 0, r.code, r.stderr)
	assert.Contains(t, r.stdout, `"docStatus": "missing"`)
	r = runMain(t, "script-doc", "--fail", file)
	assert.Equal(t, 5, r.code)
}
