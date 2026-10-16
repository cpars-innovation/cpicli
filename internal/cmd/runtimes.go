package cmd

import (
	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func NewRuntimesCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "runtimes",
		Short: "List the tenant's runtimes (Cloud Integration, integration cells) and what is deployed on each",
		Long: `A tenant can have more than one runtime: the Cloud Integration runtime and
integration cells. A flow bound to an integration cell deploys there, but SAP's
public API lists only the Cloud Integration runtime, so such a flow looks
undeployed to every other command. This command reads the runtime list of the
Web UI (Monitor > Manage Integration Content) instead, which the tenant answers
for the API key too. deploy uses the same list when a deployment succeeded but
the flow is not on the Cloud Integration runtime.`,
		Example: `  cpictl runtimes
  cpictl runtimes --artifacts
  cpictl runtimes --artifacts --runtime integrationcell --output json`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			withArtifacts, _ := cmd.Flags().GetBool("artifacts")
			only := config.GetString(cmd, "runtime")
			runtimes, err := ops.ListRuntimes(tenantExecuter(cmd), withArtifacts || only != "", only)
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), map[string]any{"runtimes": runtimes})
			for _, r := range runtimes {
				log.Info().Msgf("%-20s %-24s %s", r.ID, r.Type, r.State)
				for _, a := range r.Artifacts {
					on := ""
					if a.DeployedOn != nil {
						on = a.DeployedOn.Local().Format("2006-01-02 15:04")
					}
					log.Info().Msgf("  %-40s %-10s %-9s %-8s %s", a.ID, a.Version, a.Status, a.DeployState, on)
				}
			}
			return nil
		},
	}
	c.AddCommand(newRuntimesWatchCommand())
	c.Flags().Bool("artifacts", false, "Also list the artifacts deployed on each runtime")
	c.Flags().String("runtime", "", "Only this runtime (ID from the list, e.g. integrationcell); implies --artifacts")
	return c
}
