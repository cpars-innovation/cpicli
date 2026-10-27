package cmd

import (
	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// allFlagNames collects every flag of the command tree (top-level config keys
// are flag names).
func allFlagNames(c *cobra.Command, names map[string]bool) {
	visit := func(f *pflag.Flag) { names[f.Name] = true }
	c.PersistentFlags().VisitAll(visit)
	c.Flags().VisitAll(visit)
	for _, sub := range c.Commands() {
		allFlagNames(sub, names)
	}
}

func readExample(t *testing.T, name string) *viper.Viper {
	t.Helper()
	v := viper.New()
	v.SetConfigFile(filepath.Join(examplesDir, name))
	require.NoError(t, v.ReadInConfig(), name)
	return v
}

// The profile and project file examples only use real settings, and the
// project file obeys the project rules.
func TestProfileAndProjectExamples(t *testing.T) {
	flags := map[string]bool{}
	allFlagNames(NewCLI("test"), flags)
	sections := map[string]bool{"deploy": true, "undeploy": true, "configure": true, "orchestrator": true, "pd-deploy": true, "pd-snapshot": true, "sync": true, "snapshot": true, "update": true}

	for _, name := range []string{"profiles/dev.yaml", "profiles/qa.yaml", "project-cpictl.yaml"} {
		v := readExample(t, name)
		for key, val := range v.AllSettings() {
			if _, nested := val.(map[string]any); nested {
				assert.True(t, sections[key], "%s: unknown command section %q", name, key)
				continue
			}
			assert.True(t, flags[key], "%s: %q is not a setting", name, key)
		}
	}
	for _, name := range []string{"profiles/dev.yaml", "profiles/qa.yaml"} {
		v := readExample(t, name)
		assert.NotEmpty(t, v.GetString("tmn-host"), name)
		assert.NotEmpty(t, v.GetString("oauth-host"), name)
	}

	project := readExample(t, "project-cpictl.yaml")
	for _, k := range secretConfigKeys {
		assert.False(t, project.InConfig(k), "project file must not contain %s", k)
	}

	// the example project file is accepted next to a profile
	home, repo := t.TempDir(), t.TempDir()
	data, err := os.ReadFile(filepath.Join(examplesDir, "project-cpictl.yaml"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "cpictl.yaml"), data, 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".cpictl"), 0o700))
	dev, err := os.ReadFile(filepath.Join(examplesDir, "profiles", "dev.yaml"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, ".cpictl", "dev.yaml"), dev, 0o600))
	cpitest.SetHome(t, home)
	t.Chdir(repo)
	viper.Reset()
	t.Cleanup(viper.Reset)
	files, err := loadConfigFiles("", "dev", false)
	require.NoError(t, err)
	assert.Len(t, files, 2)
	assert.Equal(t, "./packages", viper.GetString("orchestrator.packagesDir"))
	assert.NotEmpty(t, viper.GetString("oauth-clientsecret"))
}
