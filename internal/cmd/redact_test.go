package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --redact gives an agent with a shell what cpictl mcp gives: payloads and
// error texts withheld, business keys and personal data masked, in text
// and JSON output, on stdout and stderr (cpicli#72).
func TestCLIRedaction(t *testing.T) {
	now := time.Now().UTC()
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", DesignVersion: "1", Runtime: &cpitest.Runtime{Version: "1", Status: "STARTED"},
			Resources: map[string]cpitest.Resource{"s.groovy": {Type: "groovy", Content: []byte("// owner jane.doe@example.com\nprintln 1")}}},
	})
	failed := cpitest.MessageLog{Guid: "g1", Artifact: "A", Status: "FAILED", Start: now, End: now,
		ErrorText:   "HTTP 500 from erp: customer Jane Doe, IBAN DE89370400440532013000",
		Headers:     map[string]string{"OrderNumber": "ORD-4711"},
		Attachments: map[string]string{"in.xml": "<customer>Jane Doe</customer>"},
		Steps:       []cpitest.Step{{StepID: "s1", ModelStepID: "Mapping_1", Status: "FAILED", Error: "customer Jane Doe unknown"}}}
	values := []string{"Jane Doe", "DE89370400440532013000", "ORD-4711", "jane.doe@example.com"}
	run := func(args ...string) cliRun {
		mock.MessageLogSteps = [][]cpitest.MessageLog{{failed}}
		return runMain(t, append(args, basicAuth(mock)...)...)
	}
	for _, args := range [][]string{
		{"logs", "get", "--message-guid", "g1", "--redact"},
		{"logs", "get", "--message-guid", "g1", "--redact", "--output", "json"},
		{"logs", "steps", "--message-guid", "g1", "--redact"},
		{"logs", "steps", "--message-guid", "g1", "--redact", "--output", "json"},
		{"logs", "attachment", "--id", "att-g1-in.xml", "--redact"},
		{"logs", "attachment", "--id", "att-g1-in.xml", "--redact", "--output", "json"},
		{"resources", "get", "--artifact-id", "A", "--name", "s.groovy", "--type", "groovy", "--redact"},
	} {
		res := run(args...)
		require.Equal(t, 0, res.code, "%v\n%s", args, res.stderr)
		for _, v := range values {
			assert.NotContains(t, res.stdout, v, "%v stdout", args)
			assert.NotContains(t, res.stderr, v, "%v stderr", args)
		}
	}
	res := run("logs", "attachment", "--id", "att-g1-in.xml", "--redact")
	assert.Contains(t, res.stdout, "[payload withheld: 29 bytes; run cpictl with --no-redact")
	res = run("logs", "get", "--message-guid", "g1", "--redact", "--output", "json")
	assert.Contains(t, res.stdout, "[error text withheld")
	assert.Contains(t, res.stdout, `"OrderNumber"`, "the business key's name stays")
	res = run("resources", "get", "--artifact-id", "A", "--name", "s.groovy", "--type", "groovy", "--redact")
	assert.Equal(t, "// owner ••••@••••\nprintln 1", res.stdout, "content is masked line by line, not withheld")

	// off by default outside gateway mode, and --no-redact wins over redact: true
	res = run("logs", "get", "--message-guid", "g1")
	assert.Contains(t, res.stderr, "Jane Doe")
	env := func() { t.Setenv("CPICTL_REDACT", "true") }
	mock.MessageLogSteps = [][]cpitest.MessageLog{{failed}}
	res = runMainWith(t, env, append([]string{"logs", "get", "--message-guid", "g1"}, basicAuth(mock)...)...)
	assert.NotContains(t, res.stderr, "Jane Doe", "CPICTL_REDACT")
	mock.MessageLogSteps = [][]cpitest.MessageLog{{failed}}
	res = runMainWith(t, env, append([]string{"logs", "get", "--message-guid", "g1", "--no-redact"}, basicAuth(mock)...)...)
	assert.Contains(t, res.stderr, "Jane Doe")
}

// Under an agent with redaction off, cpictl says so once on stderr.
func TestCLIRedactionAgentHint(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	res := runMainWith(t, func() { t.Setenv("CLAUDECODE", "1") }, append([]string{"status"}, basicAuth(mock)...)...)
	assert.Equal(t, 1, strings.Count(res.stderr, "output redaction off"), res.stderr)
	res = runMainWith(t, func() { t.Setenv("CLAUDECODE", "1") }, append([]string{"status", "--redact"}, basicAuth(mock)...)...)
	assert.NotContains(t, res.stderr, "output redaction off")
}

// Under --redact a script's secret lines are masked in text output too.
func TestCLIRedactionScriptLines(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", DesignVersion: "1",
			Resources: map[string]cpitest.Resource{"s.groovy": {Type: "groovy", Content: []byte("def apiKey = \"k9Xq2LmP4vR7tZ1w\"\nprintln 1")}}},
	})
	res := runMain(t, append([]string{"resources", "get", "--artifact-id", "A", "--name", "s.groovy", "--type", "groovy", "--redact"}, basicAuth(mock)...)...)
	require.Equal(t, 0, res.code, res.stderr)
	assert.Equal(t, "••••\nprintln 1", res.stdout)
}

// cpictl mask: files or stdin through the masker, counts on stderr; mask
// config checks rules.
func TestMaskCommand(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "p.json")
	require.NoError(t, os.WriteFile(f, []byte(`{"customerNo":"C-77","mail":"jane@example.com","amount":12.50}`), 0o600))
	res := runMain(t, "mask", "--name", "^customerNo$", f)
	require.Equal(t, 0, res.code, res.stderr)
	assert.Equal(t, `{"customerNo":"••••","mail":"••••@••••","amount":12.50}`, res.stdout)
	assert.Contains(t, res.stderr, "Masked 2 values")
	assert.NotContains(t, res.stderr, "jane@example.com")

	res = runMain(t, "mask", "--lines", "--rule", `regex:ORD-\d+`, f)
	require.Equal(t, 0, res.code, res.stderr)
	assert.Equal(t, `{"customerNo":"C-77","mail":"••••@••••","amount":12.50}`, res.stdout, "lines: no key rules beyond the built-in names")

	res = runMain(t, "mask", "config", "--rule", "regex:a*", "--output", "json")
	assert.Equal(t, 2, res.code, "a rule that matches empty text")
	res = runMain(t, "mask", "config", "--detector", "email", "--output", "json")
	require.Equal(t, 0, res.code, res.stderr)
	assert.Contains(t, res.stdout, `"detectors": [`)
	assert.Contains(t, res.stdout, `"email"`)
}
