package cmd

import (
	"context"
	"os"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/mcp"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/mask"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// CLI output redaction (cpicli#72): an agent with a shell reads cpictl's
// stdout and stderr, so with --redact they get what cpictl mcp gives:
// message bodies and error texts withheld, business keys masked, and every
// line through the value masker (pkg/mask).

// annotationPayload marks a command whose result is a message body (a
// payload, an attachment, a data store entry, a variable, a test message's
// response): withheld under --redact.
const annotationPayload = "cpicli/payload"

// hintCLI ends a withheld placeholder in CLI output.
const hintCLI = "run cpictl with --no-redact to include it"

// redaction is the run's redaction state, set once the configuration is
// read (PersistentPreRunE).
type redaction struct {
	on       bool
	out, err *mask.Writer
	redactor *mcp.Redactor
}

type redactionKey struct{}

func withRedaction(ctx context.Context, r *redaction) context.Context {
	return context.WithValue(ctx, redactionKey{}, r)
}

func redactionOf(ctx context.Context) *redaction {
	r, _ := ctx.Value(redactionKey{}).(*redaction)
	if r == nil {
		return &redaction{}
	}
	return r
}

func addRedactFlags(c *cobra.Command) {
	f := c.PersistentFlags()
	f.Bool("redact", false, "Redact output for AI agents: withhold message bodies and error texts, mask business keys, personal data and secrets (on by default in gateway mode or with redact: true in the profile)")
	f.Bool("no-redact", false, "Turn output redaction off, also where it is on by default")
	f.String("mask-config", "", "Masking rules for --redact and cpictl mcp (YAML or JSON: builtIns, names, rules of kind path or regex); default: every built-in detector")
}

// redactOn decides redaction for a run: gateway mode always; otherwise
// --no-redact turns it off, --redact, CPICTL_REDACT or redact: true in a
// config file turn it on.
// In gateway mode redaction is always on: --no-redact (from a flag, the
// environment or a profile) is ignored there, with a warning, and a mask
// config can only add masking (cpicli#75). Until the gateway masks its
// answers itself (cpictl-gateway#28), this client-side masking is the only
// layer, so it must not be undone from the client.
func redactOn(cmd *cobra.Command) bool {
	if config.GetString(cmd, "gateway") != "" {
		if config.GetBool(cmd, "no-redact") {
			log.Warn().Msg("--no-redact is ignored in gateway mode: the gateway's masking policy applies")
		}
		return true
	}
	if config.GetBool(cmd, "no-redact") {
		return false
	}
	return config.GetBool(cmd, "redact")
}

// applyRedaction turns redaction on for the run when redactOn says so: the
// result document passes the redactor, text on stdout and stderr the
// masker. cpictl mcp masks its own results (stdout is its protocol).
func applyRedaction(cmd *cobra.Command) error {
	r := redactionOf(cmd.Context())
	if r.out == nil {
		return nil
	}
	if !redactOn(cmd) || ownDataCommand(cmd) {
		agentHint(cmd)
		return nil
	}
	cfg, _, err := loadMaskConfig(config.GetString(cmd, "mask-config"))
	if err != nil {
		return err
	}
	_, masker, err := gatewayMaskConfig(cmd, cfg)
	if err != nil {
		return err
	}
	red, err := mcp.NewRedactor("", nil, true, masker)
	if err != nil {
		return err
	}
	red.Hint = hintCLI
	r.on, r.redactor = true, red
	r.out.Masker, r.err.Masker = masker, masker
	r.err.Enable(true)
	// stdout is masked for every command but cpictl mcp (its protocol;
	// it masks its own results), also for streams like runtimes watch
	protocol := commandName(cmd) == "mcp"
	r.out.Enable(!protocol && (outputFormat(cmd) != output.FormatJSON || cmd.Annotations[annotationNoEnvelope] == "true"))
	return nil
}

// ownDataCommand: commands that show the user's own sign-in and settings,
// not tenant data (auth, whoami, profile, version).
func ownDataCommand(cmd *cobra.Command) bool {
	first, _, _ := strings.Cut(commandName(cmd), " ")
	return first == "auth" || first == "whoami" || first == "profile" || first == "version"
}

// redactResult is the result document's result under redaction.
func redactResult(ctx context.Context, cmd *cobra.Command, v any) any {
	r := redactionOf(ctx)
	if !r.on || v == nil {
		return v
	}
	out, _ := r.redactor.ApplyPayloads(v, cmd != nil && cmd.Annotations[annotationPayload] == "true")
	return out
}

// redactError masks an error text under redaction.
func redactError(ctx context.Context, s string) string {
	r := redactionOf(ctx)
	if !r.on {
		return s
	}
	out, _ := r.redactor.Masker.Text(s)
	return out
}

// messageText is a message's error text for text output: withheld under
// redaction.
func messageText(cmd *cobra.Command, s string) string {
	if s == "" || !redactionOf(cmd.Context()).on {
		return s
	}
	return "[error text withheld; " + hintCLI + "]"
}

// businessKey is a custom header's value for text output: masked under
// redaction.
func businessKey(cmd *cobra.Command, s string) string {
	if s == "" || !redactionOf(cmd.Context()).on {
		return s
	}
	return mask.Dots
}

// agentHint tells an agent (best effort, by the markers agents set) once
// per run that redaction is off.
func agentHint(cmd *cobra.Command) {
	if config.GetBool(cmd, "no-redact") || isOffline(cmd) || cmd.Annotations[annotationNoEnvelope] == "true" {
		return
	}
	for _, e := range os.Environ() {
		name, _, _ := strings.Cut(e, "=")
		if name == "CLAUDECODE" || name == "CURSOR_AGENT" || name == "GEMINI_CLI" || strings.HasPrefix(name, "CODEX_") {
			log.Warn().Msg("cpictl runs under an AI agent with output redaction off: tenant data in this output goes to the agent's model. Use --redact (or redact: true in the profile); see docs/ai-agents.md")
			return
		}
	}
}
