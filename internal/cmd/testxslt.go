package cmd

import (
	"os"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/internal/str"
	"github.com/cpars-innovation/cpicli/pkg/testrun"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func newTestXSLTCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "xslt",
		Short: "Run the tests of XSLT mappings stored in the Partner Directory (offline, with Saxon)",
		Long: `Run the XSLT cases under <dir>/pd/<PID>/<ID>/<case>.yaml: each transforms its input
with the stylesheet of the Partner Directory binary parameter <PID>/<ID> (the file
pd-snapshot wrote to <pd-dir>/<PID>/Binary/<ID>.xsl, or the case's xslt) and
checks the output (output, output_contains, output_not_contains, output_xpath)
or the expected error (error_contains). The case's headers and properties are
passed as stylesheet parameters (xsl:param), as Cloud Integration's XSLT mapping
does.

The transformation runs with Saxon on this machine: --saxon-jar (java -jar
<jar>, also CPICTL_SAXON_JAR) or --command. Nothing is sent to a tenant.

--online runs the same cases on the tenant's XSLT engine instead: the
stylesheet is uploaded as the Partner Directory binary CPICTL_Test/<PID>.<ID>
(removed afterwards unless --keep-uploads) and each case is sent to the XSLT
test flow CPICTL_Test_XSLT ('cpictl test tools install --deploy'). Refused on a
production tier unless --allow-production.

Exit code 5 when a case failed.`,
		Example: `  cpictl test xslt --saxon-jar ~/lib/saxon-he-12.5.jar
  cpictl test xslt --flow 'SAP_ERP/*' --tag smoke --junit xslt.xml
  cpictl test xslt --command "saxon"
  cpictl test xslt --online --flow 'SAP_ERP/*'`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			online := config.GetBool(cmd, "online")
			command := strings.Fields(config.GetString(cmd, "command"))
			if jar := config.GetString(cmd, "saxon-jar"); len(command) == 0 && jar != "" {
				command = []string{"java", "-jar", jar}
			}
			if len(command) == 0 && !online {
				return output.Usagef("no XSLT processor: --saxon-jar <saxon-he.jar> (or CPICTL_SAXON_JAR), --command, or --online")
			}
			parallel := config.GetInt(cmd, "parallel")
			if parallel < 1 || parallel > 32 {
				return output.Usagef("--parallel must be between 1 and 32")
			}
			f := testrun.Filter{Flows: nonEmpty(str.TrimSlice(config.GetStringSlice(cmd, "flow"))), Tags: nonEmpty(str.TrimSlice(config.GetStringSlice(cmd, "tag"))),
				Cases: nonEmpty(str.TrimSlice(config.GetStringSlice(cmd, "case")))}
			cases, err := testrun.LoadXSLT(config.GetString(cmd, "dir"), f)
			if err != nil {
				return err
			}
			if len(cases) == 0 {
				return output.Usagef("no XSLT case matches (write them as %s/%s/<PID>/<ID>/<case>.yaml)", config.GetString(cmd, "dir"), testrun.XSLTDir)
			}
			ctx := cmd.Context()
			opts := testrun.XSLTOptions{PDDir: config.GetString(cmd, "pd-dir"), Command: command, Parallel: parallel,
				Progress: func(c testrun.CaseResult) {
					output.Emit(ctx, output.EventItem, c)
					logCase(c)
				}}
			if online {
				opts.Online, opts.Keep = true, config.GetBool(cmd, "keep-uploads")
				opts.Exe, opts.NewExe = tenantExecuter(cmd), endpointExecuter(cmd)
				opts.Tier, opts.AllowProduction = config.GetString(cmd, "tier"), config.GetBool(cmd, "allow-production")
			}
			res, err := testrun.RunXSLT(ctx, cases, opts)
			if err != nil {
				return err
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
	c.Flags().String("dir", testrun.DefaultDir, "Directory of the test cases (XSLT cases in <dir>/pd/<PID>/<ID>/<case>.yaml)")
	c.Flags().String("pd-dir", "partner-directory", "Partner Directory folder of pd-snapshot (the stylesheets)")
	c.Flags().StringSlice("flow", nil, "Only these parameters (<PID>/<ID>, patterns allowed; repeatable)")
	c.Flags().StringSlice("case", nil, "Only these cases (file name without .yaml; patterns allowed; repeatable)")
	c.Flags().StringSlice("tag", nil, "Only cases with one of these tags (repeatable)")
	c.Flags().String("saxon-jar", "", "Saxon-HE jar, run with java -jar (config: CPICTL_SAXON_JAR)")
	c.Flags().String("command", "", "XSLT processor command instead of --saxon-jar; Saxon's -s: -xsl: -o: and name=value arguments are appended")
	c.Flags().Int("parallel", 1, "Cases run at the same time")
	c.Flags().String("junit", "", "Write a JUnit XML report to this file")
	c.Flags().Bool("online", false, "Run on the tenant's XSLT engine through the XSLT test flow (cpictl test tools install)")
	c.Flags().Bool("keep-uploads", false, "With --online: keep the uploaded stylesheets in the Partner Directory (PID CPICTL_Test)")
	addRuntimeAuthFlags(c)
	return c
}
