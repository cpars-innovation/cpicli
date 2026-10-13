package cmd

import (
	"cmp"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/internal/str"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/cpars-innovation/cpicli/pkg/testrun"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// NewTestCommand groups the test case runner and the tenant-side test
// tooling (tools, stub).
func NewTestCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "test",
		Short: "Run the test cases of deployed integration flows (.cpi/tests); test harness and receiver stub",
		Long: `Test cases are YAML files in .cpi/tests/<FlowId>/<case>.yaml: the message to send
and the expected outcome (HTTP status, message status, response, error, custom
headers, steps, duration, receiver stub calls). See docs/testing.md (Test runs)
and docs/test-case.schema.json.`,
	}
	c.AddCommand(newTestListCommand(), newTestRunCommand(), newTestScriptsCommand())
	addTestToolingCommands(c)
	return c
}

func addTestFilterFlags(c *cobra.Command) {
	c.Flags().String("dir", testrun.DefaultDir, "Directory of the test cases (<dir>/<FlowId>/<case>.yaml)")
	c.Flags().StringSlice("flow", nil, "Only the cases of these flows (names or patterns such as Orders_*; repeatable)")
	c.Flags().StringSlice("tag", nil, "Only cases with one of these tags (repeatable)")
}

func testFilter(cmd *cobra.Command) testrun.Filter {
	f := testrun.Filter{Flows: nonEmpty(str.TrimSlice(config.GetStringSlice(cmd, "flow"))), Tags: nonEmpty(str.TrimSlice(config.GetStringSlice(cmd, "tag")))}
	if cmd.Flags().Lookup("case") != nil {
		f.Cases = nonEmpty(str.TrimSlice(config.GetStringSlice(cmd, "case")))
	}
	return f
}

func newTestListCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "list",
		Short: "List the test cases (offline)",
		Long: `List the test cases under --dir per flow, with their trigger (http, idoc, odata,
process_direct), tags and skip reasons, and the parameter keys of each flow's
config.yaml. The files are checked strictly: an unknown key or invalid YAML is a
usage error naming file:line. Nothing is sent.`,
		Example: `  cpictl test list
  cpictl test list --flow Orders_In --tag smoke --output json`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			cases, err := testrun.Load(config.GetString(cmd, "dir"), testFilter(cmd))
			if err != nil {
				return err
			}
			l := testrun.List(cases)
			output.SetResult(cmd.Context(), l)
			for _, f := range l.Flows {
				cfg := ""
				if len(f.ConfigKeys) > 0 {
					cfg = " (config.yaml: " + strings.Join(f.ConfigKeys, ", ") + ")"
				}
				log.Info().Msgf("%s: %d case(s)%s", f.Flow, len(f.Cases), cfg)
				for _, c := range f.Cases {
					note := c.Description
					if c.Skip != "" {
						note = "SKIP: " + c.Skip
					}
					tags := ""
					if len(c.Tags) > 0 {
						tags = " [" + strings.Join(c.Tags, ", ") + "]"
					}
					log.Info().Msgf("  %-30s %-14s%s %s", c.Case, c.Trigger, tags, note)
				}
			}
			log.Info().Msgf("%d test case(s) in %d flow(s)", l.Cases, len(l.Flows))
			return nil
		},
	}
	addTestFilterFlags(c)
	return c
}

func newTestRunCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "run",
		Short: "Run test cases against the deployed flows and check the expectations",
		Long: `Run the test cases under --dir: each case sends its message to the deployed flow
(as 'cpictl send'), waits for the message processing log and checks every
expectation. A failed case gets diagnostics: the message GUID, its error, the
failing step and the message path across flows.

The run triggers real processing, including receiver calls: use a development
tenant. On a tenant whose tier is prod it is refused unless --allow-production.

--apply-config sets each flow's config.yaml values (e.g. receiver URLs pointing
at the receiver stub) before its cases, redeploys the flow, and afterwards sets
the previous values back and redeploys again, also when the cases fail or the
run is cancelled. The result lists the keys applied and restored, never values.
Without it, a config.yaml whose values differ from the tenant is a warning.

Cases run one at a time unless --parallel; cases with stub_calls always run
alone. --junit writes a JUnit XML report, --events one item event per case.

Exit codes: 0 every case passed (or was skipped); 5 a case failed (or a
configuration could not be restored); 7 some cases passed and others could not
be run; otherwise the cause of the cases that could not be run (2 usage, 3 auth,
4 tenant HTTP error, ...).`,
		Example: `  cpictl test run
  cpictl test run --flow Orders_In --case valid-order --junit report.xml
  cpictl test run --tag smoke --parallel 4 --events -
  cpictl test run --flow Orders_Route --apply-config`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			tier, allow := config.GetString(cmd, "tier"), config.GetBool(cmd, "allow-production")
			if err := ops.GuardProduction(tier, allow, "a test run"); err != nil {
				return err
			}
			parallel := config.GetInt(cmd, "parallel")
			if parallel < 1 || parallel > 32 {
				return output.Usagef("--parallel must be between 1 and 32")
			}
			cases, err := testrun.Load(config.GetString(cmd, "dir"), testFilter(cmd))
			if err != nil {
				return err
			}
			if len(cases) == 0 {
				return output.Usagef("no test case matches (see cpictl test list)")
			}
			junit := config.GetString(cmd, "junit")
			ctx := cmd.Context()
			res, err := testrun.Run(ctx, tenantExecuter(cmd), endpointExecuter(cmd), cases, testrun.Options{
				Parallel: parallel, Harness: config.GetString(cmd, "harness"), ApplyConfig: config.GetBool(cmd, "apply-config"),
				Tier: tier, AllowProduction: allow,
				Progress: func(c testrun.CaseResult) {
					output.Emit(ctx, output.EventItem, c)
					logCase(c)
				},
			})
			if err != nil {
				return err
			}
			output.SetResult(ctx, res)
			for _, w := range res.Warnings {
				log.Warn().Msg(w)
			}
			logTestSummary(res)
			if junit != "" {
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
	addTestFilterFlags(c)
	c.Flags().StringSlice("case", nil, "Only these cases (file name without .yaml, or Flow/case; patterns allowed; repeatable)")
	c.Flags().Int("parallel", 1, "Cases run at the same time (cases with stub_calls always run alone)")
	c.Flags().String("junit", "", "Write a JUnit XML report to this file")
	c.Flags().Bool("apply-config", false, "Set each flow's config.yaml values, redeploy, run its cases, then restore the previous values and redeploy")
	c.Flags().String("harness", ops.DefaultHarnessID, "Test harness flow ID (cases with process_direct_address)")
	addRuntimeAuthFlags(c)
	return c
}

// logCase logs the outcome of a case when it is final.
func logCase(c testrun.CaseResult) {
	id := c.Flow + "/" + c.Case
	switch c.Status {
	case testrun.StatusPassed:
		log.Info().Msgf("✅ %s passed (%s, message %s)", id, formatMs(c.DurationMs), c.MessageGuid)
	case testrun.StatusSkipped:
		log.Info().Msgf("⏭ %s skipped: %s", id, c.SkipReason)
	case testrun.StatusFailed:
		for _, f := range c.Failures {
			log.Error().Str("case", id).Str("expectation", f.Expectation).Msgf("❌ %s: %s", id, f.Message)
		}
		if d := c.Diagnostics; d != nil {
			step := ""
			if d.FailedStep != nil {
				step = fmt.Sprintf(", failing step %s (%s)", d.FailedStep.ModelStepID, d.FailedStep.Activity)
			}
			log.Error().Str("case", id).Msgf("   message %s %s%s", d.MessageGuid, d.MessageStatus, step)
		}
	default:
		log.Error().Str("case", id).Msgf("⚠ %s could not be run: %s", id, c.Error)
	}
}

// logTestSummary logs a table of the cases and the totals.
func logTestSummary(r *testrun.Result) {
	log.Info().Msgf("%-40s %-8s %6s %-10s %9s  %s", "CASE", "RESULT", "HTTP", "STATUS", "TIME", "MESSAGE")
	for _, c := range r.Cases {
		http := "-"
		if c.HTTPStatus != 0 {
			http = fmt.Sprint(c.HTTPStatus)
		}
		log.Info().Msgf("%-40s %-8s %6s %-10s %9s  %s", c.Flow+"/"+c.Case, c.Status, http, cmp.Or(c.MessageStatus, "-"),
			formatMs(c.DurationMs), cmp.Or(c.MessageGuid, "-"))
	}
	for _, ch := range r.Config {
		msg := fmt.Sprintf("%s: config applied %s, restored %s", ch.Flow, keys(ch.Applied), keys(ch.Restored))
		if ch.RestoreError != "" {
			log.Error().Msg(msg + ": restore failed: " + ch.RestoreError)
			continue
		}
		log.Info().Msg(msg)
	}
	log.Info().Msgf("%d passed, %d failed, %d error(s), %d skipped in %s", r.Passed, r.Failed, r.Errors, r.Skipped,
		formatElapsed(time.Duration(r.DurationMs)*time.Millisecond))
}

func keys(k []string) string {
	if len(k) == 0 {
		return "-"
	}
	return strings.Join(k, ", ")
}
