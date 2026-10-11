package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/deploy"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/models"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/cpars-innovation/cpicli/pkg/pdrepo"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const examplesDir = "../../docs/examples"

// mcpServerExample is an MCP server entry of an agent configuration example.
type mcpServerExample struct {
	Command string            `json:"command" mapstructure:"command"`
	Args    []string          `json:"args" mapstructure:"args"`
	Env     map[string]string `json:"env" mapstructure:"env"`
}

// TestDocsExamples loads every file in docs/examples with the same code the
// commands use, so the examples cannot silently go stale.
func TestDocsExamples(t *testing.T) {
	t.Run("cpictl.yaml", func(t *testing.T) {
		v := viper.New()
		v.SetConfigFile(filepath.Join(examplesDir, "cpictl.yaml"))
		require.NoError(t, v.ReadInConfig())
		assert.NotEmpty(t, v.GetString("tmn-host"))
		assert.NotContains(t, v.AllSettings(), "oauth-clientsecret", "secrets belong in the environment")
		for _, key := range []string{"deploy.delayLength", "undeploy.maxCheckLimit", "configure.configPath",
			"configure.pull.outputDir", "orchestrator.deployConfig", "pd-deploy.resources-path", "pd-snapshot.replace"} {
			assert.True(t, v.IsSet(key), key)
		}
		assert.True(t, cpi.IsValidArtifactType(v.GetString("deploy.artifactType")))
	})

	t.Run("deploy-config.yml", func(t *testing.T) {
		loader := deploy.NewConfigLoader()
		require.NoError(t, loader.DetectSource(filepath.Join(examplesDir, "deploy-config.yml")))
		files, err := loader.LoadConfigs()
		require.NoError(t, err)
		cfg := files[0].Config
		require.Len(t, cfg.Packages, 2)
		assert.True(t, cfg.Packages[1].Sync, "omitted sync defaults to true")
		assert.False(t, cfg.Packages[1].Deploy)
		for _, p := range cfg.Packages {
			for _, a := range p.Artifacts {
				assert.True(t, cpi.IsValidArtifactType(ops.DesigntimeType(a.Type)), a.Type)
			}
		}
	})

	t.Run("configure.yml", func(t *testing.T) {
		files, err := ops.LoadConfigureFiles(filepath.Join(examplesDir, "configure.yml"))
		require.NoError(t, err)
		pkg := files[0].Config.Packages[0]
		require.Len(t, pkg.Artifacts, 2)
		for _, a := range pkg.Artifacts {
			assert.True(t, cpi.IsValidArtifactType(a.Type), a.Type)
			assert.Equal(t, "active", a.Version)
			assert.NotEmpty(t, a.Parameters)
		}
		assert.False(t, pkg.Artifacts[1].Batch.Enabled)
		assert.True(t, models.EffectiveAllowDowngrade(pkg, pkg.Artifacts[0], false), "allowDowngrade on the artifact")
		assert.False(t, models.EffectiveAllowDowngrade(pkg, pkg.Artifacts[1], false))
		assert.True(t, models.EffectiveAllowDowngrade(pkg, pkg.Artifacts[1], true), "the flag applies without a setting")
	})

	for _, name := range []string{"mcp.json", "claude-code.mcp.json", "profiles.mcp.json",
		"agents/cursor-mcp.json", "agents/cursor-mcp-env.json", "agents/gemini-settings.json", "agents/codex-config.toml"} {
		t.Run(name, func(t *testing.T) {
			var cfg struct {
				MCPServers map[string]mcpServerExample `json:"mcpServers"`
			}
			if strings.HasSuffix(name, ".toml") {
				v := viper.New()
				v.SetConfigFile(filepath.Join(examplesDir, name))
				require.NoError(t, v.ReadInConfig())
				require.NoError(t, v.UnmarshalKey("mcp_servers", &cfg.MCPServers))
			} else {
				data, err := os.ReadFile(filepath.Join(examplesDir, name))
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(data, &cfg))
			}
			require.Contains(t, cfg.MCPServers, "cpi-dev")
			root := NewCLI("test")
			mcpCmd, _, err := root.Find([]string{"mcp"})
			require.NoError(t, err)
			lookup := func(name string) bool {
				return mcpCmd.Flags().Lookup(name) != nil || root.PersistentFlags().Lookup(name) != nil
			}
			for id, server := range cfg.MCPServers {
				require.NotEmpty(t, server.Args, id)
				assert.Equal(t, "mcp", server.Args[0], id)
				for _, arg := range server.Args[1:] {
					if flag, ok := strings.CutPrefix(arg, "--"); ok {
						assert.True(t, lookup(flag), "%s: unknown flag %s", id, arg)
					}
				}
				if slices.Contains(server.Args, "--profile") {
					assert.Empty(t, server.Env, "%s: a profile holds the connection", id)
				} else {
					assert.Contains(t, server.Env, "CPICTL_TMN_HOST", id)
				}
				for k, v := range server.Env {
					assert.True(t, strings.HasPrefix(k, "CPICTL_"), "%s: %s", id, k)
					switch name {
					case "claude-code.mcp.json":
						// a committed project file must not contain values, only references
						assert.Regexp(t, `^\$\{[A-Z0-9_]+\}$`, v, "%s: %s", id, k)
					case "agents/cursor-mcp-env.json":
						assert.Regexp(t, `^\$\{env:[A-Z0-9_]+\}$`, v, "%s: %s", id, k)
					}
				}
			}
		})
	}

	// OpenCode: "mcp" with type local and the command as one array
	for _, name := range []string{"agents/opencode.json", "agents/opencode-env.json"} {
		t.Run(name, func(t *testing.T) {
			var cfg struct {
				Schema string `json:"$schema"`
				MCP    map[string]struct {
					Type        string            `json:"type"`
					Command     []string          `json:"command"`
					Enabled     bool              `json:"enabled"`
					Timeout     int               `json:"timeout"`
					Environment map[string]string `json:"environment"`
				} `json:"mcp"`
			}
			data, err := os.ReadFile(filepath.Join(examplesDir, name))
			require.NoError(t, err)
			dec := json.NewDecoder(bytes.NewReader(data))
			dec.DisallowUnknownFields()
			require.NoError(t, dec.Decode(&cfg))
			assert.Equal(t, "https://opencode.ai/config.json", cfg.Schema)
			require.Contains(t, cfg.MCP, "cpi-dev")
			root := NewCLI("test")
			mcpCmd, _, err := root.Find([]string{"mcp"})
			require.NoError(t, err)
			for id, server := range cfg.MCP {
				assert.Equal(t, "local", server.Type, id)
				assert.True(t, server.Enabled, id)
				require.GreaterOrEqual(t, len(server.Command), 2, id)
				assert.Equal(t, []string{"cpictl", "mcp"}, server.Command[:2], id)
				for _, arg := range server.Command[2:] {
					if flag, ok := strings.CutPrefix(arg, "--"); ok {
						assert.True(t, mcpCmd.Flags().Lookup(flag) != nil || root.PersistentFlags().Lookup(flag) != nil, "%s: unknown flag %s", id, arg)
					}
				}
				if slices.Contains(server.Command, "--profile") {
					assert.Empty(t, server.Environment, id)
				} else {
					assert.Contains(t, server.Environment, "CPICTL_TMN_HOST", id)
				}
				for k, v := range server.Environment {
					assert.True(t, strings.HasPrefix(k, "CPICTL_"), "%s: %s", id, k)
					assert.Regexp(t, `^\{env:[A-Z0-9_]+\}$`, v, "%s: %s", id, k)
				}
			}
		})
	}

	t.Run("partner-directory", func(t *testing.T) {
		pd := pdrepo.New(filepath.Join(examplesDir, "partner-directory"))
		pids, err := pd.GetLocalPIDs()
		require.NoError(t, err)
		assert.Equal(t, []string{"SAP_SYSTEM_001"}, pids)
		strs, err := pd.ReadStringParameters("SAP_SYSTEM_001")
		require.NoError(t, err)
		assert.Len(t, strs, 2)
		bins, err := pd.ReadBinaryParameters("SAP_SYSTEM_001")
		require.NoError(t, err)
		require.Len(t, bins, 1)
		assert.Equal(t, "Identity", bins[0].ID)
		assert.Equal(t, "xsl", bins[0].ContentType)
	})
}
