package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const pluginDir = "../../plugin"

// The Claude Code plugin refers to MCP tools by name: every reference must
// name an existing tool, and the manifests must stay valid.
func TestPluginReferencesExistingTools(t *testing.T) {
	tools := map[string]bool{}
	for _, tool := range Tools(Config{}) {
		tools[tool.Name] = true
	}
	ref := regexp.MustCompile(`mcp__plugin_cpi_cpi__([a-z_*]+)`)
	found := 0
	err := filepath.WalkDir(pluginDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range ref.FindAllStringSubmatch(string(data), -1) {
			found++
			if !strings.Contains(m[1], "*") {
				assert.True(t, tools[m[1]], "%s refers to unknown tool %s", p, m[1])
			}
		}
		return nil
	})
	require.NoError(t, err)
	assert.Positive(t, found)
}

func TestPluginManifests(t *testing.T) {
	var plugin struct{ Name, Version, Description string }
	readJSON(t, filepath.Join(pluginDir, ".claude-plugin", "plugin.json"), &plugin)
	assert.Equal(t, "cpi", plugin.Name, "tool references use the prefix mcp__plugin_cpi_cpi__")

	var mcp struct {
		McpServers map[string]struct {
			Command string
			Args    []string
		} `json:"mcpServers"`
	}
	readJSON(t, filepath.Join(pluginDir, ".mcp.json"), &mcp)
	require.Contains(t, mcp.McpServers, "cpi")
	assert.Equal(t, "mcp", mcp.McpServers["cpi"].Args[0])

	var market struct {
		Name    string
		Plugins []struct{ Name, Source string }
	}
	readJSON(t, filepath.Join(pluginDir, "..", ".claude-plugin", "marketplace.json"), &market)
	require.Len(t, market.Plugins, 1)
	assert.Equal(t, "cpi", market.Plugins[0].Name)
	assert.DirExists(t, filepath.Join(pluginDir, "..", market.Plugins[0].Source))

	frontmatter := regexp.MustCompile(`(?s)^---\n(.*?)\n---\n`)
	skills, err := filepath.Glob(filepath.Join(pluginDir, "skills", "*", "SKILL.md"))
	require.NoError(t, err)
	assert.Len(t, skills, 7)
	agents, err := filepath.Glob(filepath.Join(pluginDir, "agents", "*.md"))
	require.NoError(t, err)
	for _, f := range append(skills, agents...) {
		data, err := os.ReadFile(f)
		require.NoError(t, err)
		m := frontmatter.FindSubmatch(data)
		require.NotNil(t, m, "%s has no frontmatter", f)
		fm := string(m[1])
		name := filepath.Base(filepath.Dir(f))
		if strings.Contains(f, "agents") {
			name = strings.TrimSuffix(filepath.Base(f), ".md")
		}
		assert.Contains(t, fm, "name: "+name+"\n", f)
		assert.Regexp(t, `(?m)^description: .{80,}$`, fm, "%s needs a description that says when to use it", f)
		// relative links to supporting files must exist
		for _, l := range regexp.MustCompile(`\]\(([a-z-]+\.md)\)`).FindAllStringSubmatch(string(data), -1) {
			assert.FileExists(t, filepath.Join(filepath.Dir(f), l[1]), f)
		}
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, v), path)
}

// skillInput is one entry of a skill's inputs front matter (the format CPI
// Studio reads to ask for the inputs in one form).
type skillInput struct {
	ID       string   `yaml:"id"`
	Label    string   `yaml:"label"`
	Kind     string   `yaml:"kind"`
	Required bool     `yaml:"required"`
	Default  any      `yaml:"default"`
	Help     string   `yaml:"help"`
	Pattern  string   `yaml:"pattern"`
	Options  []string `yaml:"options"`
	Source   string   `yaml:"source"`
	Checks   []string `yaml:"checks"`
}

func TestSkillInputs(t *testing.T) {
	kinds := []string{"text", "select", "multiselect", "boolean"}
	sources := []string{"packages", "artifactTypes", "templates", "tiers"}
	checks := []string{"artifact-id-free", "package-id-free", "package-exists", "naming"}
	frontmatter := regexp.MustCompile(`(?s)^---\n(.*?)\n---\n`)
	skills, err := filepath.Glob(filepath.Join(pluginDir, "skills", "*", "SKILL.md"))
	require.NoError(t, err)
	for _, f := range skills {
		data, err := os.ReadFile(f)
		require.NoError(t, err)
		var fm struct {
			Inputs []skillInput `yaml:"inputs"`
		}
		dec := yaml.NewDecoder(strings.NewReader(string(frontmatter.FindSubmatch(data)[1])))
		require.NoError(t, dec.Decode(&fm), f)
		require.NotEmpty(t, fm.Inputs, "%s lists its inputs", f)
		seen := map[string]bool{}
		for _, in := range fm.Inputs {
			where := f + " " + in.ID
			assert.Regexp(t, `^[a-z][A-Za-z0-9]*$`, in.ID, where)
			assert.False(t, seen[in.ID], "%s: duplicate", where)
			seen[in.ID] = true
			assert.NotEmpty(t, in.Label, where)
			assert.Contains(t, kinds, in.Kind, "%s: no secret or other kinds", where)
			if in.Kind == "select" || in.Kind == "multiselect" {
				assert.True(t, len(in.Options) > 0 || in.Source != "", "%s: options or source", where)
			}
			if in.Source != "" {
				assert.Contains(t, sources, in.Source, where)
			}
			for _, c := range in.Checks {
				assert.Contains(t, checks, c, where)
			}
			if in.Pattern != "" {
				_, err := regexp.Compile(in.Pattern)
				assert.NoError(t, err, where)
			}
			if in.Default != nil && len(in.Options) > 0 {
				assert.Contains(t, in.Options, in.Default, "%s: default is an option", where)
			}
			if in.Kind == "boolean" && in.Default != nil {
				assert.IsType(t, true, in.Default, where)
			}
			// the body names the input too (the model may not see the front matter)
			assert.Contains(t, string(data), "(`"+in.ID+"`", "%s: listed under **Inputs:**", where)
		}
	}
}
