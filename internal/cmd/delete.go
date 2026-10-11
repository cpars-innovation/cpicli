package cmd

import (
	"time"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func NewDeleteCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "delete",
		Short: "Delete a designtime artifact or an integration package from the tenant",
		Long: `Delete a designtime artifact or a whole integration package, as Delete in the Web
UI. This cannot be undone: keep the content in Git (cpictl snapshot, download)
before deleting it.

A deployed artifact is refused unless --undeploy-first, which undeploys it, waits
until it is gone and then deletes it. A package that still has artifacts is
refused unless --with-artifacts.`,
		Example: `  cpictl delete artifact --artifact-id Orders_Old --confirm
  cpictl delete artifact --artifact-id Country_Codes --type ValueMapping --undeploy-first --confirm
  cpictl delete package --package-id Sandbox --with-artifacts --undeploy-first --confirm`,
	}
	artifact := &cobra.Command{
		Use: "artifact", Short: "Delete a designtime artifact (requires --confirm)", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := config.GetString(cmd, "artifact-id")
			if ok, _ := cmd.Flags().GetBool("confirm"); !ok {
				return output.Usagef("deleting %s cannot be undone; add --confirm", id)
			}
			undeploy, _ := cmd.Flags().GetBool("undeploy-first")
			res, err := ops.DeleteArtifact(cmd.Context(), tenantExecuter(cmd), ops.DeleteRequest{
				ArtifactID: id, Type: config.GetString(cmd, "type"), UndeployFirst: undeploy, Options: deleteOptions(cmd)})
			if res != nil {
				output.SetResult(cmd.Context(), res)
				logDeleted(*res)
			}
			return err
		},
	}
	artifact.Flags().String("artifact-id", "", "Artifact to delete")
	artifact.Flags().String("type", "", "Artifact type: Integration, MessageMapping, ScriptCollection, ValueMapping (default: the type the tenant has)")

	pkg := &cobra.Command{
		Use: "package", Short: "Delete an integration package (requires --confirm)", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := config.GetString(cmd, "package-id")
			if ok, _ := cmd.Flags().GetBool("confirm"); !ok {
				return output.Usagef("deleting package %s cannot be undone; add --confirm", id)
			}
			withArtifacts, _ := cmd.Flags().GetBool("with-artifacts")
			undeploy, _ := cmd.Flags().GetBool("undeploy-first")
			res, err := ops.DeletePackage(cmd.Context(), tenantExecuter(cmd), ops.DeletePackageRequest{
				PackageID: id, WithArtifacts: withArtifacts, UndeployFirst: undeploy, Options: deleteOptions(cmd)})
			if res != nil {
				output.SetResult(cmd.Context(), res)
				for _, a := range res.Artifacts {
					logDeleted(a)
				}
				if res.Deleted {
					log.Info().Msgf("Deleted package %s", res.PackageID)
				}
			}
			return err
		},
	}
	pkg.Flags().String("package-id", "", "Package to delete")
	pkg.Flags().Bool("with-artifacts", false, "Delete the package's artifacts too")

	for _, s := range []*cobra.Command{artifact, pkg} {
		s.Flags().Bool("undeploy-first", false, "Undeploy deployed artifacts first (otherwise they are refused)")
		s.Flags().Bool("confirm", false, "Confirm: deleting cannot be undone")
		s.Flags().Int("delay-length", 10, "Seconds between undeployment status checks")
		s.Flags().Int("max-check-limit", 30, "Undeployment status checks per artifact")
	}
	c.AddCommand(artifact, pkg)
	return c
}

func deleteOptions(cmd *cobra.Command) ops.Options {
	delay, _ := cmd.Flags().GetInt("delay-length")
	checks, _ := cmd.Flags().GetInt("max-check-limit")
	return ops.Options{Interval: time.Duration(delay) * time.Second, MaxChecks: checks}
}

func logDeleted(r ops.DeleteResult) {
	if r.Undeployed != "" {
		log.Info().Msgf("Undeployed %s (version %s)", r.ArtifactID, r.Undeployed)
	}
	if r.Deleted {
		log.Info().Msgf("Deleted %s %s", r.Type, r.ArtifactID)
	}
}
