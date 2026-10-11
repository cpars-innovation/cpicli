package cmd

import (
	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func NewLocksCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "locks",
		Short: "List the edit locks of artifacts, remove one (unlock)",
		Long: `An artifact opened for editing in the Web UI is locked for everyone else until the
editor saves or cancels. A browser closed while editing leaves the lock behind.
unlock removes it, as "Unlock" in the Web UI does: whoever held the lock loses
unsaved changes.`,
		Example: `  cpictl locks list
  cpictl locks list --package-id Orders
  cpictl locks unlock --artifact-id Orders_In --confirm`,
	}
	list := &cobra.Command{
		Use: "list", Short: "List edit locks (who, since when)", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			locks, err := ops.ListLocks(tenantExecuter(cmd), config.GetString(cmd, "artifact-id"), config.GetString(cmd, "package-id"))
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), map[string]any{"locks": locks})
			for _, l := range locks {
				since := ""
				if l.CreatedAt != nil {
					since = l.CreatedAt.Local().Format("2006-01-02 15:04")
				}
				log.Info().Msgf("%-40s %-30s %-16s %s", l.ArtifactID, l.PackageID, since, l.CreatedBy)
			}
			log.Info().Msgf("%d lock(s)", len(locks))
			return nil
		},
	}
	list.Flags().String("artifact-id", "", "Only this artifact's lock")
	list.Flags().String("package-id", "", "Only locks in this package")

	unlock := &cobra.Command{
		Use: "unlock", Short: "Remove the edit lock of an artifact (requires --confirm)", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := config.GetString(cmd, "artifact-id")
			if ok, _ := cmd.Flags().GetBool("confirm"); !ok {
				return output.Usagef("unlocking %s discards its editor's unsaved changes; add --confirm", id)
			}
			res, err := ops.Unlock(tenantExecuter(cmd), id)
			if res != nil {
				output.SetResult(cmd.Context(), res)
				for _, l := range res.Removed {
					log.Info().Msgf("Unlocked %s (locked by %s)", l.ArtifactID, l.CreatedBy)
				}
			}
			return err
		},
	}
	unlock.Flags().String("artifact-id", "", "Artifact to unlock")
	unlock.Flags().Bool("confirm", false, "Confirm: the editor holding the lock loses unsaved changes")

	c.AddCommand(list, unlock)
	return c
}
