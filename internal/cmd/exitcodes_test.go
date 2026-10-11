package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var updateGolden = flag.Bool("update", false, "update golden files in testdata/")

type cliRun struct {
	code   int
	stdout string
	stderr string
}

// runMain executes the CLI exactly like main() does (minus os.Exit), with an
// isolated HOME so that no cpictl.yaml and no tenant env vars are used.
func runMain(t *testing.T, args ...string) cliRun {
	t.Helper()
	return runMainWith(t, nil, args...)
}

// runMainWith is runMain with a hook that runs after the environment was
// isolated.
func runMainWith(t *testing.T, after func(), args ...string) cliRun {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GITHUB_STEP_SUMMARY", "") // never the real job summary of a CI run
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "CPICTL_") || strings.HasPrefix(kv, "FLASHPIPE_") {
			t.Setenv(strings.SplitN(kv, "=", 2)[0], "")
		}
	}
	if after != nil {
		after()
	}
	viper.Reset()
	t.Cleanup(viper.Reset)
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), args, &stdout, &stderr, "test", "test")
	return cliRun{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func basicAuth(mock *cpitest.Tenant) []string {
	host, port := mock.HostPort()
	return []string{"--tmn-host", "http://" + host + ":" + strconv.Itoa(port), "--tmn-userid", "user", "--tmn-password", "secret"}
}

func goldenCompare(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		require.NoError(t, os.MkdirAll("testdata", 0755))
		require.NoError(t, os.WriteFile(path, got, 0644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "golden file missing, run: go test ./internal/cmd -run %s -update", t.Name())
	assert.Equal(t, string(want), string(got), "golden file %s differs (run with -update to accept)", path)
}

// TestExitCodes is the golden test for the exit code contract:
// 0 ok, 2 usage/config, 3 auth, 4 tenant HTTP error, 5 deploy/validate failed,
// 6 timeout, 7 partial failure. Every scenario runs with --output json and
// also verifies the output contract: exactly one JSON document on stdout and
// JSON lines on stderr.
func TestExitCodes(t *testing.T) {
	now := time.Now()
	started := []*cpitest.Runtime{{Version: "1", Status: "STARTED", DeployedOn: now}}
	ok := func() *cpitest.Artifact {
		return &cpitest.Artifact{Type: "Integration", DesignVersion: "1", TaskStatuses: []string{"SUCCESS"}, AfterDeploy: started}
	}
	fast := []string{"--delay-length", "0", "--max-check-limit", "2"}

	brokenPD := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(brokenPD, "PID1", "String.properties"), 0755))

	scenarios := []struct {
		name string
		args func(t *testing.T) []string
	}{
		{"deploy ok", func(t *testing.T) []string {
			m := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": ok()})
			return append(append([]string{"deploy", "--artifact-ids", "A"}, fast...), basicAuth(m)...)
		}},
		{"deploy skipped (same version running)", func(t *testing.T) []string {
			m := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": {Type: "Integration", DesignVersion: "1",
				Runtime: &cpitest.Runtime{Version: "1", Status: "STARTED"}}})
			return append(append([]string{"deploy", "--artifact-ids", "A"}, fast...), basicAuth(m)...)
		}},
		{"undeploy ok", func(t *testing.T) []string {
			m := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": {Runtime: &cpitest.Runtime{Version: "1", Status: "STARTED"}}})
			return append(append([]string{"undeploy", "--artifact-ids", "A"}, fast...), basicAuth(m)...)
		}},
		{"usage: unknown flag", func(t *testing.T) []string {
			return []string{"deploy", "--bogus"}
		}},
		{"usage: unknown command", func(t *testing.T) []string {
			return []string{"bogus"}
		}},
		{"usage: missing required flag", func(t *testing.T) []string {
			m := cpitest.NewTenant(t, nil)
			return append([]string{"deploy"}, basicAuth(m)...)
		}},
		{"usage: missing credentials", func(t *testing.T) []string {
			return []string{"deploy", "--artifact-ids", "A", "--tmn-host", "localhost"}
		}},
		{"usage: invalid artifact type", func(t *testing.T) []string {
			m := cpitest.NewTenant(t, nil)
			return append([]string{"deploy", "--artifact-ids", "A", "--artifact-type", "Bogus"}, basicAuth(m)...)
		}},
		{"usage: invalid output format", func(t *testing.T) []string {
			m := cpitest.NewTenant(t, nil)
			return append([]string{"deploy", "--artifact-ids", "A", "--output", "xml"}, basicAuth(m)...)
		}},
		{"usage: configure without config path", func(t *testing.T) []string {
			m := cpitest.NewTenant(t, nil)
			return append([]string{"configure"}, basicAuth(m)...)
		}},
		{"auth: tenant returns 401", func(t *testing.T) []string {
			m := cpitest.NewTenant(t, nil)
			m.StatusOverride = http.StatusUnauthorized
			return append(append([]string{"deploy", "--artifact-ids", "A"}, fast...), basicAuth(m)...)
		}},
		{"auth: oauth token request fails", func(t *testing.T) []string {
			m := cpitest.NewTenant(t, nil) // no /oauth/token endpoint
			host, port := m.HostPort()
			hp := "http://" + host + ":" + strconv.Itoa(port)
			return append([]string{"deploy", "--artifact-ids", "A", "--tmn-host", hp,
				"--oauth-host", hp, "--oauth-clientid", "id", "--oauth-clientsecret", "secret"}, fast...)
		}},
		{"tenant http: 500", func(t *testing.T) []string {
			m := cpitest.NewTenant(t, nil)
			m.StatusOverride = http.StatusInternalServerError
			return append(append([]string{"deploy", "--artifact-ids", "A"}, fast...), basicAuth(m)...)
		}},
		{"tenant http: unreachable", func(t *testing.T) []string {
			return append([]string{"undeploy", "--artifact-ids", "A", "--tmn-host", "http://127.0.0.1:1",
				"--tmn-userid", "user", "--tmn-password", "secret"}, fast...)
		}},
		{"deploy failed: build task FAIL", func(t *testing.T) []string {
			m := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": {Type: "Integration", DesignVersion: "1",
				TaskStatuses: []string{"FAIL"}}})
			return append(append([]string{"deploy", "--artifact-ids", "A"}, fast...), basicAuth(m)...)
		}},
		{"deploy failed: designtime artifact missing", func(t *testing.T) []string {
			m := cpitest.NewTenant(t, map[string]*cpitest.Artifact{})
			return append(append([]string{"deploy", "--artifact-ids", "A"}, fast...), basicAuth(m)...)
		}},
		{"deploy failed: validation failed", func(t *testing.T) []string {
			m := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": {Type: "Integration", DesignVersion: "1",
				ValidationResult: "Check execution result: Failed"}})
			return append([]string{"validate", "--artifact-id", "A"}, basicAuth(m)...)
		}},
		{"deploy failed: certificate expires soon (--fail-on-expiry)", func(t *testing.T) []string {
			m := cpitest.NewTenant(t, nil)
			m.Keystore = []cpitest.KeystoreEntry{{Alias: "partner", NotAfter: now.Add(24 * time.Hour)}}
			return append([]string{"keystore", "list", "--expiring-within", "30d", "--fail-on-expiry"}, basicAuth(m)...)
		}},
		{"usage: credential without secret source", func(t *testing.T) []string {
			m := cpitest.NewTenant(t, nil)
			return append([]string{"credentials", "set-user", "--name", "A", "--user", "u"}, basicAuth(m)...)
		}},
		{"timeout: deploy still starting", func(t *testing.T) []string {
			m := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": {Type: "Integration", DesignVersion: "1",
				AfterDeploy: []*cpitest.Runtime{{Version: "1", Status: "STARTING", DeployedOn: now}}}})
			return append(append([]string{"deploy", "--artifact-ids", "A"}, fast...), basicAuth(m)...)
		}},
		{"timeout: undeploy never completes", func(t *testing.T) []string {
			m := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": {Runtime: &cpitest.Runtime{Status: "STARTED"}, UndeployAfter: -1}})
			return append(append([]string{"undeploy", "--artifact-ids", "A"}, fast...), basicAuth(m)...)
		}},
		{"timeout: logs --wait without final message", func(t *testing.T) []string {
			m := cpitest.NewTenant(t, nil)
			m.MessageLogSteps = [][]cpitest.MessageLog{{{Guid: "g1", Artifact: "A", Status: "PROCESSING", Start: now}}}
			return append([]string{"logs", "--artifact-id", "A", "--wait", "1ms"}, basicAuth(m)...)
		}},
		{"usage: logs with invalid status", func(t *testing.T) []string {
			m := cpitest.NewTenant(t, nil)
			return append([]string{"logs", "--status", "BROKEN"}, basicAuth(m)...)
		}},
		{"partial: deploy one of two", func(t *testing.T) []string {
			m := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": ok()})
			return append(append([]string{"deploy", "--artifact-ids", "A,B"}, fast...), basicAuth(m)...)
		}},
		{"partial: pd-deploy with unreadable PID", func(t *testing.T) []string {
			m := cpitest.NewTenant(t, nil)
			return append([]string{"pd-deploy", "--resources-path", brokenPD}, basicAuth(m)...)
		}},
	}

	var golden strings.Builder
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			args := sc.args(t)
			if !slices.Contains(args, "--output") {
				args = append(args, "--output", "json")
			}
			res := runMain(t, args...)

			if !slices.Contains(args, "json") {
				// e.g. an invalid --output value: text mode, nothing on stdout
				assert.Empty(t, res.stdout)
				fmt.Fprintf(&golden, "%-45s exit=%d (text output)\n", sc.name, res.code)
				return
			}

			// stdout: exactly one JSON envelope matching the exit code
			var env output.Envelope
			dec := json.NewDecoder(strings.NewReader(res.stdout))
			require.NoError(t, dec.Decode(&env), "stdout must be a JSON document, got: %q", res.stdout)
			assert.False(t, dec.More(), "stdout must contain exactly one JSON document")
			assert.Equal(t, res.code, env.ExitCode)
			assert.Equal(t, res.code == 0, env.OK)
			assert.Equal(t, output.Category(res.code), env.ErrorCategory, "errorCategory names the exit code")
			assert.Equal(t, output.SchemaVersion, env.SchemaVersion)

			// stderr: JSON lines only
			for line := range strings.SplitSeq(strings.TrimSpace(res.stderr), "\n") {
				if line != "" {
					assert.True(t, json.Valid([]byte(line)), "stderr line is not JSON: %q", line)
				}
			}
			fmt.Fprintf(&golden, "%-45s exit=%d command=%q\n", sc.name, res.code, env.Command)
		})
	}
	goldenCompare(t, "TestExitCodes.golden", []byte(golden.String()))
}

// The JSON result document of a partially failed multi-artifact deploy.
func TestDeployJSONResultGolden(t *testing.T) {
	m := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", DesignVersion: "1.0.2", TaskStatuses: []string{"SUCCESS"},
			AfterDeploy: []*cpitest.Runtime{{Version: "1.0.2", Status: "STARTED", DeployedOn: time.Now()}}},
		"C": {Type: "Integration", DesignVersion: "2.0.0", TaskStatuses: []string{"DEPLOYING"}},
	})
	res := runMain(t, append([]string{"deploy", "--artifact-ids", "A,B,C", "--delay-length", "0", "--max-check-limit", "2",
		"--output", "json"}, basicAuth(m)...)...)
	assert.Equal(t, 7, res.code)
	assert.Regexp(t, `"durationMs": \d+,`, res.stdout)
	stdout := regexp.MustCompile(`"durationMs": \d+`).ReplaceAllString(res.stdout, `"durationMs": 0`)
	goldenCompare(t, "TestDeployJSONResult.golden", []byte(stdout))
}

// Every command logs how long it took; --output json also puts it in the envelope.
func TestCommandTimer(t *testing.T) {
	m := cpitest.NewTenant(t, map[string]*cpitest.Artifact{})
	res := runMain(t, append([]string{"deploy", "--artifact-ids", "A", "--delay-length", "0"}, basicAuth(m)...)...)
	assert.Regexp(t, `⏱ deploy failed in \d+(\.\d+)?(ms|s)`, res.stderr)

	res = runMain(t, "skills", "list")
	assert.Equal(t, 0, res.code)
	assert.Contains(t, res.stderr, "⏱ skills list finished in")

	res = runMain(t, "--help")
	assert.NotContains(t, res.stderr, "⏱", "help is no command run")
}

func TestFormatElapsed(t *testing.T) {
	assert.Equal(t, "350ms", formatElapsed(350*time.Millisecond+200*time.Microsecond))
	assert.Equal(t, "12.3s", formatElapsed(12340*time.Millisecond))
	assert.Equal(t, "4m5s", formatElapsed(4*time.Minute+5200*time.Millisecond))
}

// In text mode stdout stays empty (logs go to stderr) and the exit code
// contract is the same.
func TestTextModeKeepsStdoutClean(t *testing.T) {
	m := cpitest.NewTenant(t, map[string]*cpitest.Artifact{})
	res := runMain(t, append([]string{"deploy", "--artifact-ids", "A", "--delay-length", "0"}, basicAuth(m)...)...)
	assert.Equal(t, 5, res.code)
	assert.Empty(t, res.stdout)
	assert.Contains(t, res.stderr, "designtime artifact A does not exist")
}

func TestVersionFlag(t *testing.T) {
	res := runMain(t, "--version")
	assert.Equal(t, 0, res.code)
	assert.Equal(t, "cpictl version test (built test)\n", res.stdout)
}

func TestConfigGenerateWorksOffline(t *testing.T) {
	dir := setupPackagesDir(t)
	out := filepath.Join(dir, "cfg.yml")
	// no tenant settings at all: config-generate does not talk to a tenant
	res := runMain(t, "config-generate", "--packages-dir", filepath.Join(dir, "packages"), "--output-file", out, "--output", "json")
	require.Equal(t, 0, res.code, res.stderr)
	assert.FileExists(t, out)
	assert.Contains(t, res.stdout, `"outputFile"`)

	// --output is the format flag only
	res = runMain(t, "config-generate", "--packages-dir", filepath.Join(dir, "packages"), "--output", out)
	assert.Equal(t, 2, res.code)
}

func TestLegacyFlashPipeSettingsAreReported(t *testing.T) {
	res := runMain(t, "deploy", "--artifact-ids", "A", "--output", "json")
	assert.Equal(t, 2, res.code)
	assert.NotContains(t, res.stdout, "FLASHPIPE")

	t.Setenv("FLASHPIPE_TMN_HOST", "tenant.example.com")
	var stdout, stderr bytes.Buffer
	viper.Reset()
	code := Run(context.Background(), []string{"deploy", "--artifact-ids", "A", "--output", "json"}, &stdout, &stderr, "test", "test")
	assert.Equal(t, 2, code)
	assert.Contains(t, stdout.String(), "FLASHPIPE_* environment variables are not read")
	assert.Contains(t, stdout.String(), "FLASHPIPE_TMN_HOST")
}
