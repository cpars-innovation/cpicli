package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/logger"
	"github.com/cpars-innovation/cpicli/internal/mcp"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// annotationNoEnvelope marks commands that own stdout (no JSON envelope).
const annotationNoEnvelope = "cpicli/no-envelope"

func NewMCPCommand(version string) *cobra.Command {
	c := &cobra.Command{
		Use:   "mcp",
		Short: "Run the MCP server (stdio) for AI agents",
		Long: `Run a Model Context Protocol server on stdin/stdout.

The server uses the same tenant settings as every other command (flags,
CPICTL_* environment variables or cpictl.yaml). stdout carries the
protocol only; logs go to stderr as JSON lines.

Tools: list packages, artifacts, resources and parameters; create packages;
download, upload, validate, guideline check, deploy, undeploy (requires
confirm=true); runtime status and endpoints; send test messages; message logs,
steps, attachments and persisted messages; discovery of existing content;
pd_deploy (dry run unless dry_run=false). See docs/mcp.md.

send_test_message uses the --runtime-* credentials (default: the API
credentials), see 'cpictl send --help'.

Local paths given to tools are resolved against --root and may not leave it.

Limit the tools per server, e.g. for a QA or production tenant:
  --read-only                     no tool that changes the tenant or sends messages
  --tools list_*,get_*            only matching tools
  --disable-tools undeploy,pd_*   everything except these
  --opt-in delete_artifact        also offer an opt-in tool (unlock_artifact, delete_*), keeping the rest
  --toolset build,test            only the tools of these tasks
  --dynamic-toolsets              start with a few tools; the agent enables toolsets as needed
  --mode discover|operate|develop|full presets (combined with the above, the most
                                  restrictive wins)
Also as CPICTL_MODE, CPICTL_TOOLSET, CPICTL_DYNAMIC_TOOLSETS, CPICTL_READ_ONLY, CPICTL_TOOLS, CPICTL_DISABLE_TOOLS, CPICTL_OPT_IN. Disabled tools are not
listed; a call to one is an error result (errorCategory usage) that says why; a
pattern that matches no tool is an error.

Branch policy (optional, off by default):
  --branch-policy .cpi/branch-policy.yaml
                                  refuse tenant changes (errorCategory branch_readonly) while a
                                  repository is on a read-only branch; checked on every call

Loop policy (the model cannot change it):
  --loop-required                 tenant tools are refused (errorCategory loop_required)
                                  until loop_start opened a loop
  --loop-max-iterations, --loop-max-deploys, --loop-max-same-errors, --loop-max-minutes
                                  the highest limits loop_start accepts (higher: usage error);
                                  they also lower the defaults

Masking: values of sensitive names (authorization, cookie, password, secret, token,
credential, API key) in all name/value lists of results are masked as ***;
--mask-pattern adds name patterns, --redact-payloads replaces message bodies with
a placeholder, size and sha256. Local paths in results are relative to --root.
All flags can also be set in cpictl.yaml (e.g. loop-required: true) or as
CPICTL_* environment variables.`,
		Example: `  # Claude Code / any MCP client configuration
  {"mcpServers": {"cpi": {"command": "cpictl", "args": ["mcp", "--root", "/path/to/repo"],
    "env": {"CPICTL_TMN_HOST": "...", "CPICTL_OAUTH_HOST": "...",
            "CPICTL_OAUTH_CLIENTID": "...", "CPICTL_OAUTH_CLIENTSECRET": "..."}}}}`,
		SilenceUsage: true,
		Annotations:  map[string]string{annotationNoEnvelope: "true", annotationNoStats: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			// stdout is the protocol channel: logs always as JSON lines on stderr
			logger.Init(cmd.ErrOrStderr(), true, viper.GetBool("debug"))

			mode := config.GetString(cmd, "mode")
			versionMode, err := versioningMode(cmd)
			if err != nil {
				return err
			}
			root := config.GetString(cmd, "root")
			ledger := mcp.NewLedger(root)
			ledger.Policy = mcp.LoopPolicy{Required: config.GetBool(cmd, "loop-required"), Caps: mcp.LoopLimits{
				MaxIterations: config.GetInt(cmd, "loop-max-iterations"), SameErrorLimit: config.GetInt(cmd, "loop-max-same-errors"),
				WallClockMinutes: config.GetInt(cmd, "loop-max-minutes"), MaxDeploys: config.GetInt(cmd, "loop-max-deploys")}}
			for name, v := range map[string]int{"loop-max-iterations": ledger.Policy.Caps.MaxIterations, "loop-max-same-errors": ledger.Policy.Caps.SameErrorLimit,
				"loop-max-minutes": ledger.Policy.Caps.WallClockMinutes, "loop-max-deploys": ledger.Policy.Caps.MaxDeploys} {
				if v < 0 {
					return output.Usagef("--%s must not be negative (0: no cap)", name)
				}
			}
			maskPatterns := stringListSetting(cmd, "mask-pattern")
			redactPayloads := config.GetBool(cmd, "redact-payloads")
			redactor, err := mcp.NewRedactor(root, maskPatterns, redactPayloads)
			if err != nil {
				return output.Usagef("invalid --mask-pattern: %v", err)
			}
			exe := tenantExecuter(cmd)
			levels := mcp.NewLogLevelReverter(exe)
			all := ledger.Wrap(mcp.Tools(mcp.Config{
				Exe:          exe,
				LogLevels:    levels,
				Root:         config.GetString(cmd, "root"),
				PollInterval: time.Duration(config.GetInt(cmd, "poll-interval")) * time.Second,
				MaxChecks:    config.GetInt(cmd, "max-checks"),
				TenantHost:   config.GetString(cmd, "tmn-host"),

				NewEndpointExecuter: endpointExecuter(cmd),
				DenyFullSync:        mode == "develop",
				Versioning:          versionMode,
				CacheTTL:            time.Duration(config.GetInt(cmd, "cache-ttl")) * time.Second,
				Tier:                config.GetString(cmd, "tier"),
				AllowProduction:     config.GetBool(cmd, "allow-production"),
			}))
			var branchPolicy *mcp.BranchPolicy
			if file := config.GetString(cmd, "branch-policy"); file != "" {
				if branchPolicy, err = mcp.LoadBranchPolicy(file, root, config.GetString(cmd, "tier")); err != nil {
					return err
				}
				all = branchPolicy.Wrap(all)
				d := branchPolicy.Decide()
				log.Info().Bool("readOnly", d.ReadOnly).Strs("reasons", d.Reasons).Msgf("Branch policy %s", file)
			}
			readOnly, _ := cmd.Flags().GetBool("read-only")
			filter := mcp.ToolFilter{ReadOnly: readOnly, Allow: config.GetStringSlice(cmd, "tools"), Deny: config.GetStringSlice(cmd, "disable-tools"),
				OptIn: config.GetStringSlice(cmd, "opt-in")}
			toolsets := config.GetStringSlice(cmd, "toolset")
			dynamic := config.GetBool(cmd, "dynamic-toolsets")
			if len(toolsets) > 0 && !dynamic {
				names, err := mcp.ToolsetTools(toolsets)
				if err != nil {
					return err
				}
				filter.Allow = append(filter.Allow, names...)
			}
			tools, removedTools, err := mcp.ApplyFiltersWithReasons(all, mode, filter)
			if err != nil {
				return err
			}
			var removed []string
			reasons := map[string]string{}
			for _, r := range removedTools {
				removed = append(removed, r.Name)
				reasons[r.Name] = r.Reason
			}
			if len(removed) > 0 {
				log.Info().Msgf("Tools disabled by configuration: %s", strings.Join(removed, ", "))
			}
			// help reports the tools that are really available, so it is added after filtering
			var server *mcp.Server
			help := mcp.HelpInfo{Scope: activeScope, Tools: tools, Removed: removed, Reasons: reasons, Mode: mode, ReadOnly: readOnly,
				Commands: commandInfos(cmd.Root()), Loop: ledger.Policy, MaskPatterns: maskPatterns, RedactPayloads: redactPayloads,
				ServerInstructions: mcp.ModeInstructions(mode) + mcp.LoopPolicyInstructions(ledger.Policy), BranchPolicy: branchPolicy}
			if branchPolicy != nil {
				help.ServerInstructions += branchPolicy.Instructions()
			}
			if dynamic {
				help.ActiveToolsets = func() []string { return server.EnabledToolsets() }
			}
			tools = append(tools, mcp.HelpTool(help))
			var instructions string
			if len(toolsets) > 0 && !dynamic {
				// only the parts of these toolsets, and no list of the many
				// tools they leave out: smaller, and nothing to plan around
				instructions = mcp.ToolsetInstructions(toolsets) + help.ServerInstructions +
					"\n\nToolset: " + strings.Join(toolsets, ", ") + ". Only the tools of these tasks are available; call help to see them."
				if readOnly {
					instructions += " This server is read-only: it cannot change the tenant or send messages; ask the user to make changes."
				}
			} else {
				instructions = mcp.FilteredInstructions(mcp.Instructions, filter, removed) + help.ServerInstructions
			}
			if dynamic {
				instructions += mcp.DynamicInstructions()
			}
			server = mcp.NewServer("cpicli", version, instructions, tools)
			server.Removed, server.Redactor, server.Root = reasons, redactor, root
			if dynamic {
				if err := server.UseDynamicToolsets(toolsets); err != nil {
					return err
				}
			}
			log.Info().Bool("dynamicToolsets", dynamic).Msgf("MCP server started with %d tools", len(tools))
			err = server.Serve(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout())
			// time-boxed log levels (TRACE) are set back when the server stops
			levels.RevertAll()
			return err
		},
	}
	c.Flags().String("root", ".", "Directory that local paths of tool calls are confined to")
	addVersioningFlag(c)
	c.Flags().Int("poll-interval", 10, "Default seconds between deploy/undeploy status checks")
	c.Flags().Int("max-checks", 30, "Default maximum number of deploy/undeploy status checks")
	c.Flags().Int("cache-ttl", 60, "Seconds list_packages and list_artifacts results are reused (0: no cache); any tool that changes the tenant clears them")
	c.Flags().String("mode", "", "Preset: discover (read-only), operate (read tools + set_log_level), develop (all tools, no pd_deploy full_sync), full (all tools, no restrictions)")
	c.Flags().Bool("read-only", false, "Offer only tools that do not change the tenant or trigger processing")
	c.Flags().StringSlice("tools", nil, "Offer only these tools (names or patterns such as list_*)")
	c.Flags().StringSlice("toolset", nil, "Offer only the tools of these tasks: "+strings.Join(mcp.ToolsetNames(), ", ")+" (combined with --tools; the mode still applies)")
	c.Flags().Bool("dynamic-toolsets", false, "List only help, doctor, list_toolsets and enable_toolset at the start; the agent enables the toolsets it needs (the client must support tools/list_changed). --toolset then names the toolsets enabled at the start")
	c.Flags().StringSlice("opt-in", nil, "Also offer these opt-in tools (exact names: unlock_artifact, delete_*), without limiting the other tools as --tools does")
	c.Flags().StringSlice("disable-tools", nil, "Do not offer these tools (names or patterns); wins over --tools")
	c.Flags().String("branch-policy", "", "Policy file (YAML or JSON): tools that change the tenant are refused while a repository under --root is on a read-only branch (off without it)")
	c.Flags().Bool("loop-required", false, "Refuse tools that change the tenant or trigger processing (errorCategory loop_required) while no loop is open (loop_start)")
	c.Flags().Int("loop-max-iterations", 0, "Highest max_iterations loop_start accepts (0: no cap); also lowers the default")
	c.Flags().Int("loop-max-deploys", 0, "Highest max_deploys loop_start accepts (0: no cap); also lowers the default")
	c.Flags().Int("loop-max-same-errors", 0, "Highest same_error_limit loop_start accepts (0: no cap); also lowers the default")
	c.Flags().Int("loop-max-minutes", 0, "Highest wall_clock_minutes loop_start accepts (0: no cap); also lowers the default")
	c.Flags().StringArray("mask-pattern", nil, "Also mask the values of names (headers, properties, attributes, parameters) matching this regular expression (case-insensitive; repeatable)")
	c.Flags().Bool("redact-payloads", false, "Replace message bodies in results (test message responses, attachments, message store entries, trace payloads, data store entries, variables) with a placeholder, size and sha256")
	addRuntimeAuthFlags(c)
	return c
}

// stringListSetting returns a repeatable string flag. A YAML list in the
// config file is read as a list (bindFlags sets the flag to the list's text
// form, "[a b]").
func stringListSetting(cmd *cobra.Command, name string) []string {
	vals, _ := cmd.Flags().GetStringArray(name)
	if list, ok := viper.Get(name).([]any); ok && len(vals) == 1 && vals[0] == fmt.Sprintf("%v", list) {
		vals = make([]string, 0, len(list))
		for _, x := range list {
			vals = append(vals, fmt.Sprint(x))
		}
	}
	return vals
}
