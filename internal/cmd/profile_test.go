package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProfiles(t *testing.T) {
	dev := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": {}})
	qa := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": {}})
	home := t.TempDir()
	for name, m := range map[string]*cpitest.Tenant{"dev": dev, "qa": qa} {
		host, port := m.HostPort()
		writeModeFile(t, filepath.Join(home, ".cpictl", name+".yaml"),
			fmt.Sprintf("tmn-host: http://%s:%d\ntmn-userid: user\ntmn-password: secret\n", host, port), 0o600)
	}
	for _, k := range []string{"CPICTL_PROFILE", "CPICTL_CONFIG", "CPICTL_TMN_HOST", "CPICTL_TMN_USERID", "CPICTL_TMN_PASSWORD"} {
		t.Setenv(k, "")
	}
	t.Chdir(t.TempDir())
	run := func(args ...string) (int, string, string) {
		cpitest.SetHome(t, home)
		viper.Reset()
		t.Cleanup(viper.Reset)
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), args, &stdout, &stderr, "test", "test")
		return code, stdout.String(), stderr.String()
	}
	undeploys := func(m *cpitest.Tenant) int { return m.Count("GET /api/v1/IntegrationRuntimeArtifacts") }

	// no profile chosen, no home file: no tenant
	code, _, _ := run("undeploy", "--artifact-ids", "A")
	assert.Equal(t, 2, code)

	code, _, stderr := run("profile", "use", "qa")
	require.Equal(t, 0, code, stderr)
	code, _, stderr = run("undeploy", "--artifact-ids", "A")
	require.Equal(t, 0, code, stderr)
	assert.Contains(t, stderr, "Profile qa", "the tenant in use is always shown")
	assert.Equal(t, 1, undeploys(qa))

	// one-off and per shell
	code, _, _ = run("--profile", "dev", "undeploy", "--artifact-ids", "A")
	require.Equal(t, 0, code)
	assert.Equal(t, 1, undeploys(dev))
	t.Setenv("CPICTL_PROFILE", "dev")
	code, _, _ = run("undeploy", "--artifact-ids", "A")
	require.Equal(t, 0, code)
	assert.Equal(t, 2, undeploys(dev))
	t.Setenv("CPICTL_PROFILE", "")

	code, stdout, _ := run("profile", "list", "--output", "json")
	require.Equal(t, 0, code)
	var env struct {
		Result struct {
			Active   string
			Profiles []ProfileInfo
		}
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &env))
	assert.Equal(t, "qa", env.Result.Active)
	require.Len(t, env.Result.Profiles, 2)
	assert.True(t, env.Result.Profiles[1].Active)
	assert.NotContains(t, stdout, "secret", "credentials are never listed")

	code, _, stderr = run("--profile", "prod", "undeploy", "--artifact-ids", "A")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "available: dev, qa")
	code, _, _ = run("--profile", "../x", "packages")
	assert.Equal(t, 2, code)
	code, _, _ = run("--profile", "dev", "--config", "x.yaml", "packages")
	assert.Equal(t, 2, code)

	code, _, _ = run("profile", "use", "-")
	require.Equal(t, 0, code)
	_, err := os.Stat(filepath.Join(home, ".cpictl", "current"))
	assert.True(t, os.IsNotExist(err))
}
