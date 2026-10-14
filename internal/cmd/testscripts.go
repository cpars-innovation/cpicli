package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/lint"
	"github.com/cpars-innovation/cpicli/pkg/scripttest"
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
	return c
}

// percent formats a coverage value ("-" when unknown).
func percent(p *float64) string {
	if p == nil {
		return "coverage -"
	}
	return fmt.Sprintf("coverage %.0f%%", *p)
}
