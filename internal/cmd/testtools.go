package cmd

import (
	"cmp"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/cpars-innovation/cpicli/pkg/testtools"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// NewTestCommand groups the test commands: the tenant-side test tooling
// (tools, stub).
func NewTestCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "test",
		Short: "Test tooling on the tenant: test harness and receiver stub",
	}
	addTestToolingCommands(c)
	return c
}

// addTestToolingCommands adds 'tools' and 'stub' to the test command.
func addTestToolingCommands(test *cobra.Command) {
	test.AddCommand(NewTestToolsCommand(), NewTestStubCommand())
}

// NewTestToolsCommand installs and inspects the test harness and the
// receiver stub.
func NewTestToolsCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "tools",
		Short: "Install the test harness and the receiver stub, or show their state",
	}

	install := &cobra.Command{
		Use:   "install",
		Short: "Upload (and deploy) the test harness and the receiver stub",
		Long: `Upload the test tooling that ships with cpictl into a package of its own
(created when missing) and, with --deploy, deploy it:

  ` + testtools.HarnessID + `  HTTPS /cpictl/test -> Request Reply -> ProcessDirect ${header.CpictlTargetAddress}
                       (send --process-direct, test runs of ProcessDirect flows)
  ` + testtools.StubID + `     HTTPS /cpictl/stub/* : records each request (message log attachment
                       ` + testtools.StubAttachment + `) and answers per rule from the Partner Directory
                       (PID ` + testtools.StubPID + `, see 'test stub set')

Existing artifacts are updated to the version of this cpictl (versioning
manifest); a running one keeps running until it is deployed again. Install it on
development tenants only and never transport the package. On a tenant whose
tier is prod it is refused unless --allow-production.`,
		Example: `  cpictl test tools install --deploy
  cpictl test tools install --only stub --deploy
  cpictl test tools install --package MyTestTooling`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ops.GuardProduction(config.GetString(cmd, "tier"), config.GetBool(cmd, "allow-production"), "installing test tooling"); err != nil {
				return err
			}
			deploy, _ := cmd.Flags().GetBool("deploy")
			delay, _ := cmd.Flags().GetInt("delay-length")
			checks, _ := cmd.Flags().GetInt("max-check-limit")
			res, err := ops.InstallTestTools(cmd.Context(), tenantExecuter(cmd), ops.TestToolsOptions{
				PackageID: config.GetString(cmd, "package"), Only: config.GetString(cmd, "only"), Deploy: deploy,
				DeployOptions: ops.Options{Interval: time.Duration(delay) * time.Second, MaxChecks: checks},
			})
			if res != nil {
				output.SetResult(cmd.Context(), res)
				log.Info().Msgf("Package %s: %s", res.Package.ID, res.Package.Action)
				for _, a := range res.Artifacts {
					switch {
					case a.Error != "":
						log.Error().Msgf("%s: %s", a.ID, a.Error)
					case a.Deploy != nil:
						log.Info().Msgf("%s %s: %s, %s", a.ID, a.Version, a.Upload.Action, a.Deploy.Status)
					default:
						log.Info().Msgf("%s %s: %s (not deployed: add --deploy)", a.ID, a.Version, a.Upload.Action)
					}
				}
			}
			return err
		},
	}
	install.Flags().String("package", testtools.PackageID, "Package of the test tooling (letters and digits only; created when missing)")
	install.Flags().String("only", "", "Install only one tool: harness or stub (default both)")
	install.Flags().Bool("deploy", false, "Deploy the artifacts and wait for them")
	install.Flags().Int("delay-length", defaultDeployDelaySeconds, "Seconds between deployment status checks")
	install.Flags().Int("max-check-limit", defaultDeployChecks, "Deployment status checks per artifact")

	status := &cobra.Command{
		Use:   "status",
		Short: "Show whether the test harness and the receiver stub are installed and deployed",
		Long: `Show per tool whether it is installed and deployed, the running version
compared with the one this cpictl ships (upToDate), its endpoints and, for the
receiver stub, the base URL to give receivers of flows under test
(<stubUrl>/<rule>/...).`,
		Example:      `  cpictl test tools status`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ops.TestToolsStatus(cmd.Context(), tenantExecuter(cmd))
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), map[string]any{"tools": res})
			for _, s := range res {
				state := "not installed"
				switch {
				case s.Deployed:
					state = "deployed " + s.RuntimeVersion + " (" + s.RuntimeStatus + ")"
					if !s.UpToDate {
						state += ", this cpictl ships " + s.Version + ": run 'cpictl test tools install --deploy'"
					}
				case s.Installed:
					state = "installed " + s.DesigntimeVersion + ", not deployed"
				}
				log.Info().Msgf("%-20s %s", s.ID, state)
				if s.StubURL != "" {
					log.Info().Msgf("%-20s stub URL %s/<rule>/...", "", s.StubURL)
				}
			}
			return nil
		},
	}
	c.AddCommand(install, status)
	return c
}

// NewTestStubCommand configures the receiver stub's answers.
func NewTestStubCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "stub",
		Short: "Configure the answers of the receiver stub per rule",
		Long: `The receiver stub (` + testtools.StubID + `, see 'test tools install') answers a request
to <runtime>/http/cpictl/stub/<rule>/... with the response configured for <rule>,
a Partner Directory string parameter (PID ` + testtools.StubPID + `, ID = rule, JSON value
{"status":200,"contentType":"application/json","body":"...","headers":{}}).
Without one it answers 200 with {}. Every request is recorded in the stub's
message log (attachment ` + testtools.StubAttachment + `, custom headers ` + testtools.StubRuleHeader + ` and
` + testtools.StubPathHeader + `).`,
	}

	set := &cobra.Command{
		Use:   "set <rule>",
		Short: "Set the stub's answer for a rule",
		Example: `  cpictl test stub set erp --status 500 --body '{"error":"down"}'
  cpictl test stub set orders --body-file order-response.json --content-type application/json --header X-Request-Id=42`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ops.GuardProduction(config.GetString(cmd, "tier"), config.GetBool(cmd, "allow-production"), "installing test tooling"); err != nil {
				return err
			}
			body, err := readBody(cmd)
			if err != nil {
				return err
			}
			status, _ := cmd.Flags().GetInt("status")
			r := ops.StubResponse{Status: status, ContentType: config.GetString(cmd, "content-type"), Body: string(body), Headers: map[string]string{}}
			for _, h := range config.GetStringSlice(cmd, "header") {
				k, v, ok := strings.Cut(h, "=")
				if !ok || k == "" {
					return output.Usagef("invalid --header %q, expected name=value", h)
				}
				r.Headers[k] = v
			}
			if err := ops.SetStubResponse(tenantExecuter(cmd), args[0], r); err != nil {
				return err
			}
			output.SetResult(cmd.Context(), map[string]any{"rule": args[0], "pid": testtools.StubPID, "action": "SET"})
			log.Info().Msgf("Stub rule %s: HTTP %d", args[0], cmp.Or(status, 200))
			return nil
		},
	}
	set.Flags().Int("status", 200, "HTTP status of the answer (100-599)")
	set.Flags().String("body", "", "Body of the answer")
	set.Flags().String("body-file", "", "Read the body from this file ('-' for stdin)")
	set.Flags().String("content-type", "application/json", "Content-Type of the answer")
	set.Flags().StringSlice("header", nil, "Additional response header name=value (repeatable)")

	clear := &cobra.Command{
		Use:          "clear <rule>",
		Short:        "Remove the stub's answer for a rule (it then answers 200 with {})",
		Example:      `  cpictl test stub clear erp`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ops.ClearStubResponse(tenantExecuter(cmd), args[0]); err != nil {
				return err
			}
			output.SetResult(cmd.Context(), map[string]any{"rule": args[0], "pid": testtools.StubPID, "action": "CLEARED"})
			log.Info().Msgf("Stub rule %s cleared", args[0])
			return nil
		},
	}
	c.AddCommand(set, clear)
	return c
}
