package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// projectRun runs the CLI in dir (a git repository) with an isolated HOME.
func projectRun(t *testing.T, home, dir string, args ...string) (int, string) {
	t.Helper()
	cpitest.SetHome(t, home)
	t.Chdir(dir)
	viper.Reset()
	t.Cleanup(viper.Reset)
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), args, &stdout, &stderr, "test", "test")
	return code, stderr.String()
}

func writeModeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), mode))
}

func TestProjectConfig(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": {}})
	host, port := mock.HostPort()
	tenant := fmt.Sprintf("http://%s:%d", host, port)
	for _, k := range append(credentialConfigKeys, hostConfigKeys...) {
		t.Setenv(envKey(k), "")
	}

	t.Run("found from a sub-directory, merged over the home file", func(t *testing.T) {
		home, repo := t.TempDir(), t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(repo, ".git"), 0o755))
		writeModeFile(t, filepath.Join(home, "cpictl.yaml"), "tmn-host: "+tenant+"\ntmn-userid: user\ntmn-password: secret\n", 0o600)
		writeModeFile(t, filepath.Join(repo, "cpictl.yaml"), "tmn-host: "+tenant+"\nundeploy:\n  maxCheckLimit: 2\n", 0o644)
		sub := filepath.Join(repo, "packages", "Orders")
		require.NoError(t, os.MkdirAll(sub, 0o755))

		code, stderr := projectRun(t, home, sub, "undeploy", "--artifact-ids", "A")
		assert.Equal(t, 0, code, stderr)
	})

	t.Run("connection without secrets, credentials from the environment", func(t *testing.T) {
		home, repo := t.TempDir(), t.TempDir()
		writeModeFile(t, filepath.Join(repo, "cpictl.yaml"), "tmn-host: "+tenant+"\ntmn-userid: user\n", 0o644)
		t.Setenv("CPICTL_TMN_PASSWORD", "secret")
		code, stderr := projectRun(t, home, repo, "undeploy", "--artifact-ids", "A")
		assert.Equal(t, 0, code, stderr)
		t.Setenv("CPICTL_TMN_PASSWORD", "")
	})

	// the home file has no runtime-oauth-host: its runtime secret goes to
	// oauth-host, and a project file must not name another one (asked by the
	// gateway team after the second security review of 0.35.1)
	t.Run("runtime token host the home file leaves unset", func(t *testing.T) {
		home, repo := t.TempDir(), t.TempDir()
		writeModeFile(t, filepath.Join(home, "cpictl.yaml"), "tmn-host: "+tenant+"\ntmn-userid: user\ntmn-password: secret\n"+
			"runtime-oauth-clientid: rt-client\nruntime-oauth-clientsecret: made-up-runtime-secret\n", 0o600)
		writeModeFile(t, filepath.Join(repo, "cpictl.yaml"), "runtime-oauth-host: tokens.attacker.example\n", 0o644)
		before := len(mock.Requests())
		code, stderr := projectRun(t, home, repo, "send", "--artifact-id", "A")
		assert.Equal(t, 2, code)
		assert.Contains(t, stderr, "sets runtime-oauth-host")
		assert.NotContains(t, stderr, "made-up-runtime-secret")
		assert.Len(t, mock.Requests(), before)
	})

	t.Run("secrets are refused", func(t *testing.T) {
		home, repo := t.TempDir(), t.TempDir()
		writeModeFile(t, filepath.Join(repo, "cpictl.yaml"), "tmn-host: "+tenant+"\ntmn-userid: user\ntmn-password: secret\n", 0o600)
		before := len(mock.Requests())
		code, stderr := projectRun(t, home, repo, "undeploy", "--artifact-ids", "A")
		assert.Equal(t, 2, code)
		assert.Contains(t, stderr, "keep secrets in")
		assert.Len(t, mock.Requests(), before)
	})

	t.Run("the production guard: tier from the home file, not from the project", func(t *testing.T) {
		home, repo := t.TempDir(), t.TempDir()
		writeModeFile(t, filepath.Join(home, "cpictl.yaml"), "tmn-host: "+tenant+"\ntmn-userid: user\ntmn-password: secret\ntier: prod\n", 0o600)
		code, stderr := projectRun(t, home, repo, "send", "--artifact-id", "A")
		assert.Equal(t, 2, code)
		assert.Contains(t, stderr, "production tenant")
		for _, k := range []string{"allow-production: true", "tier: dev"} {
			writeModeFile(t, filepath.Join(repo, "cpictl.yaml"), k+"\n", 0o644)
			code, stderr = projectRun(t, home, repo, "send", "--artifact-id", "A")
			assert.Equal(t, 2, code, k)
			assert.Contains(t, stderr, "must not change the production guard", k)
		}
	})

	t.Run("home credentials are not sent to a host from the project file", func(t *testing.T) {
		home, repo := t.TempDir(), t.TempDir()
		writeModeFile(t, filepath.Join(home, "cpictl.yaml"), "tmn-host: "+tenant+"\ntmn-userid: user\ntmn-password: secret\n", 0o600)
		writeModeFile(t, filepath.Join(repo, "cpictl.yaml"), "tmn-host: http://127.0.0.1:1\n", 0o644)
		code, stderr := projectRun(t, home, repo, "undeploy", "--artifact-ids", "A")
		assert.Equal(t, 2, code)
		assert.Contains(t, stderr, "only sent to its own hosts")
		assert.NotContains(t, stderr, "secret\"")
	})

	t.Run("an explicit file replaces the default files", func(t *testing.T) {
		home, repo := t.TempDir(), t.TempDir()
		writeModeFile(t, filepath.Join(repo, "cpictl.yaml"), "tmn-password: would-be-refused\n", 0o600)
		explicit := filepath.Join(t.TempDir(), "dev.yaml")
		writeModeFile(t, explicit, "tmn-host: "+tenant+"\ntmn-userid: user\ntmn-password: secret\n", 0o600)
		code, stderr := projectRun(t, home, repo, "undeploy", "--artifact-ids", "A", "--config", explicit)
		assert.Equal(t, 0, code, stderr)
	})

	t.Run("the search stops at the repository root and at home", func(t *testing.T) {
		home := t.TempDir()
		writeModeFile(t, filepath.Join(home, "cpictl.yaml"), "tmn-host: "+tenant+"\ntmn-userid: user\ntmn-password: secret\n", 0o600)
		outer := t.TempDir()
		writeModeFile(t, filepath.Join(outer, "cpictl.yaml"), "tmn-host: http://127.0.0.1:1\n", 0o644)
		repo := filepath.Join(outer, "repo")
		require.NoError(t, os.MkdirAll(filepath.Join(repo, ".git"), 0o755))
		code, stderr := projectRun(t, home, repo, "undeploy", "--artifact-ids", "A")
		assert.Equal(t, 0, code, "the file above the repository root is not used: %s", stderr)

		inHome := filepath.Join(home, "work")
		require.NoError(t, os.MkdirAll(inHome, 0o755))
		code, stderr = projectRun(t, home, inHome, "undeploy", "--artifact-ids", "A")
		assert.Equal(t, 0, code, "home's cpictl.yaml is the home file, not a project file: %s", stderr)
	})
}
