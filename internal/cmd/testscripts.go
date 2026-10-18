package cmd

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/internal/str"
	"github.com/cpars-innovation/cpicli/pkg/lint"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/cpars-innovation/cpicli/pkg/scripttest"
	"github.com/cpars-innovation/cpicli/pkg/testrun"
	"github.com/cpars-innovation/cpicli/pkg/testtools"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func newTestScriptsCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "scripts",
		Short: "Run the tests of script collection scripts and report them per function (offline)",
		Long: `Run the project's Gradle build for the script collection tests (./gradlew test
when the project has a wrapper, else gradle test; --command for another one),
then read the JUnit reports and the JaCoCo coverage report and report:

  - every test with its status and duration;
  - every public function of every collection script with its line coverage,
    the test files that call it and their outcome (passed, failed, untested).

The build and the layout are the build skill's script-tests.md; the tests folder
and the coverage report are the lint configuration's (scriptTests in
.cpi/lint.yaml), so lint's collection-script-tests and collection-script-coverage
rules see the same results. --no-run only reads the reports of an earlier run.
Nothing is sent to the tenant. Exit code 5 when a test failed.`,
		Example: `  cpictl test scripts
  cpictl test scripts --no-run --output json
  cpictl test scripts --command "gradle test --offline"`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			project := config.GetString(cmd, "project")
			rules := config.GetString(cmd, "rules")
			if !filepath.IsAbs(rules) {
				rules = filepath.Join(project, rules)
			}
			cfg, err := lint.LoadConfig(rules)
			if err != nil {
				return err
			}
			noRun, _ := cmd.Flags().GetBool("no-run")
			opts := scripttest.Options{Project: project, Content: config.GetString(cmd, "content"), Config: cfg, NoRun: noRun,
				Command: strings.Fields(config.GetString(cmd, "command"))}
			if config.GetString(cmd, "output") != "json" {
				opts.Output = os.Stderr // the build's progress; JSON output keeps stdout clean
			}
			res, err := scripttest.Run(cmd.Context(), opts)
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), res)
			for _, t := range res.Tests {
				if t.Status != scripttest.StatusPassed {
					log.Warn().Msgf("%-8s %s.%s %s", t.Status, t.Class, t.Name, t.Message)
				}
			}
			for _, s := range res.Scripts {
				log.Info().Msgf("%s %s", s.File, percent(s.LineCoverage))
				for _, f := range s.Functions {
					log.Info().Msgf("  %-30s %-9s %s %s", f.Name, f.Status, percent(f.LineCoverage), strings.Join(f.TestFiles, ", "))
				}
			}
			for _, w := range res.Warnings {
				log.Warn().Msg(w)
			}
			log.Info().Msgf("%d passed, %d failed, %d error(s), %d skipped", res.Passed, res.Failed, res.Errors, res.Skipped)
			return res.Err()
		},
	}
	c.Flags().String("project", ".", "Project root: the folder with .cpi and the Gradle build")
	c.Flags().String("content", "", "Content tree with the script collections (default <project>/packages)")
	c.Flags().String("rules", ".cpi/lint.yaml", "Lint configuration with scriptTests (relative to --project)")
	c.Flags().String("command", "", "Test command (default ./gradlew test, or gradle test without a wrapper)")
	c.Flags().Bool("no-run", false, "Only read the reports of an earlier run")

	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Write the Gradle build and the SAP script API stand-ins the script tests run with",
		Long: `Write build.gradle and the stand-ins of SAP's script API (tests/stubs: Message v1
and v2, the message log, ITApiFactory with secure store, keystore, Partner
Directory, value mapping and data store, SOAP faults; tests/stubs/README.md
explains them) into the project, as the build skill's script-tests.md
describes. Existing files are kept unless --force. The stand-ins are test code:
never upload them.`,
		Example: `  cpictl test scripts init
  cpictl test scripts init --project ./my-repo --force`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")
			res, err := scripttest.Init(config.GetString(cmd, "project"), force)
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), res)
			for _, f := range res.Written {
				log.Info().Msgf("wrote %s", f)
			}
			for _, f := range res.Skipped {
				log.Info().Msgf("kept  %s (exists; --force replaces it)", f)
			}
			return nil
		},
	}
	initCmd.Flags().String("project", ".", "Project root")
	initCmd.Flags().Bool("force", false, "Replace existing files")
	c.AddCommand(initCmd)
	c.AddCommand(newTestScriptCasesCommand())
	return c
}

func newTestScriptCasesCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "cases",
		Short: "Run script cases (YAML) on the SAP script API stand-ins, or with --online on the tenant",
		Long: `Run the script cases of <dir>: each calls one function of a script with a message
and checks the outcome (body, headers, properties, return value, exception,
message log). The format is shared with CPI Studio
(docs/script-case.schema.json):

  <dir>/<FlowId>/scripts/<script file>/<case>.yaml   a script step of a flow
  <dir>/collections/<CollectionId>/<case>.yaml        a function of a script collection

The scripts run on the stand-ins of 'cpictl test scripts init' through the
Gradle task cpictlScriptCases of its build.gradle; the case's pd,
value_mappings, secure and keystore are what SAP's services answer. Nothing is
sent to a tenant.

--online runs the same cases on the tenant's runtime instead: per script and
function, cpictl generates a test flow in the test tooling package (HTTPS ->
the case's headers and properties -> the script -> the result as JSON),
deploys it, sends the cases and deletes the flows afterwards (--keep keeps
them). Cases with call.args, pd, value_mappings, secure, keystore, log or
returns are skipped online (local only); max_duration is not checked.
Refused on production tiers. Exit code 5 when a case failed.`,
		Example: `  cpictl test scripts cases
  cpictl test scripts cases --flow Orders_In --case trims --junit scripts.xml
  cpictl test scripts cases --flow 'Orders_*' --tag smoke
  cpictl test scripts cases --online --flow Orders_In`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "!online"},
		RunE: func(cmd *cobra.Command, args []string) error {
			f := testrun.Filter{Flows: nonEmpty(str.TrimSlice(config.GetStringSlice(cmd, "flow"))), Tags: nonEmpty(str.TrimSlice(config.GetStringSlice(cmd, "tag"))),
				Cases: nonEmpty(str.TrimSlice(config.GetStringSlice(cmd, "case")))}
			project := config.GetString(cmd, "project")
			dir := config.GetString(cmd, "dir")
			if dir == "" {
				dir = filepath.Join(project, testrun.DefaultDir)
			}
			cases, err := testrun.LoadScriptCases(dir, f)
			if err != nil {
				return err
			}
			if len(cases) == 0 {
				return output.Usagef("no script case matches (write them as %s/<FlowId>/scripts/<script>/<case>.yaml or %s/%s/<CollectionId>/<case>.yaml)",
					filepath.ToSlash(dir), filepath.ToSlash(dir), testrun.ScriptCollectionsDir)
			}
			ctx := cmd.Context()
			var res *testrun.Result
			if config.GetBool(cmd, "online") {
				timeout, err := parseDeployTimeout(config.GetString(cmd, "deploy-timeout"))
				if err != nil {
					return err
				}
				res, err = scripttest.RunCasesOnline(ctx, cases, scripttest.OnlineOptions{
					Content: cmp.Or(config.GetString(cmd, "content"), filepath.Join(project, "packages")),
					Exe:     tenantExecuter(cmd), NewExe: endpointExecuter(cmd),
					Tier: config.GetString(cmd, "tier"), AllowProduction: config.GetBool(cmd, "allow-production"),
					Keep: config.GetBool(cmd, "keep"), Role: config.GetString(cmd, "role"),
					Deploy: ops.Options{Timeout: timeout},
					Progress: func(cr testrun.CaseResult) {
						output.Emit(ctx, output.EventItem, cr)
						logCase(cr)
					},
				})
				if err != nil {
					return err
				}
			} else {
				opts := scripttest.CaseOptions{Project: project, Content: config.GetString(cmd, "content"), Command: strings.Fields(config.GetString(cmd, "command"))}
				if config.GetString(cmd, "output") != "json" {
					opts.Output = os.Stderr // the build's progress; JSON output keeps stdout clean
				}
				res, err = scripttest.RunCases(ctx, cases, opts)
				if err != nil {
					return err
				}
				for _, cr := range res.Cases {
					output.Emit(ctx, output.EventItem, cr)
					logCase(cr)
				}
			}
			output.SetResult(ctx, res)
			logTestSummary(res)
			if junit := config.GetString(cmd, "junit"); junit != "" {
				f, err := os.Create(junit)
				if err != nil {
					return output.Usagef("--junit: %v", err)
				}
				werr := testrun.WriteJUnit(f, res)
				if cerr := f.Close(); werr == nil {
					werr = cerr
				}
				if werr != nil {
					return werr
				}
				log.Info().Msgf("JUnit report written to %s", junit)
			}
			return res.Err()
		},
	}
	c.Flags().String("project", ".", "Project root: the folder with .cpi and the Gradle build of 'test scripts init'")
	c.Flags().String("dir", "", "Directory of the test cases (default <project>/"+testrun.DefaultDir+")")
	c.Flags().String("content", "", "Content tree with the flows and script collections (default <project>/packages)")
	c.Flags().StringSlice("flow", nil, "Only these flows, scripts (<FlowId>/<script>) or collections (patterns allowed; repeatable)")
	c.Flags().StringSlice("case", nil, "Only these cases (file name without .yaml; patterns allowed; repeatable)")
	c.Flags().StringSlice("tag", nil, "Only cases with one of these tags (repeatable)")
	c.Flags().String("command", "", "Runner command (default ./gradlew -q cpictlScriptCases, or gradle without a wrapper); the plan and results files are appended as -P properties")
	c.Flags().String("junit", "", "Write a JUnit XML report to this file")
	c.Flags().Bool("online", false, "Run on the tenant's runtime: a generated test flow per script and function, deployed for the run")
	c.Flags().Bool("keep", false, "With --online: keep the generated test flows deployed")
	c.Flags().String("role", "", "With --online: user role the runtime client needs to call the test flows (default "+testtools.DefaultRole+")")
	c.Flags().String("deploy-timeout", "10m", "With --online: how long the deployment of the test flows may take")
	addRuntimeAuthFlags(c)
	return c
}

// percent formats a coverage value ("-" when unknown).
func percent(p *float64) string {
	if p == nil {
		return "coverage -"
	}
	return fmt.Sprintf("coverage %.0f%%", *p)
}
