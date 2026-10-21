package cmd

import (
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func NewChangedSinceCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "changed-since",
		Short: "Has designtime content changed since a time? Lists the artifacts changed after it",
		Long: `Tells whether designtime content (integration flows, mappings, script
collections, value mappings) changed after --since, and lists what changed,
newest first. SAP has no single call for this, so the artifacts of all packages
are listed in parallel: a few small calls per package.

A change time cannot show a deletion. Keep the result's fingerprint and pass it
back with --fingerprint: a different one means something changed, also a
deleted artifact or package.`,
		Example: `  cpictl changed-since --since 1h
  cpictl changed-since --since 2026-10-21T08:00:00Z --packages 'SD_*' --output json
  cpictl changed-since --since 10m --fingerprint 3f2a9c0d1b7e4f60`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sinceArg, _ := cmd.Flags().GetString("since")
			since, err := ops.ParseTimeArg(sinceArg, time.Now())
			if err != nil || sinceArg == "" {
				return output.Usagef("--since needs a duration back from now (30m, 2h, 1d) or an RFC 3339 time")
			}
			fingerprint, _ := cmd.Flags().GetString("fingerprint")
			packages, _ := cmd.Flags().GetStringSlice("packages")
			parallel, _ := cmd.Flags().GetInt("parallel")
			res, err := ops.DesigntimeChangedSince(cmd.Context(), tenantExecuter(cmd), ops.ChangedSinceOptions{Since: since,
				Fingerprint: fingerprint, Packages: packages, Parallel: parallel})
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), res)
			for _, w := range res.Warnings {
				log.Warn().Msg(w)
			}
			for _, a := range res.ChangedArtifacts {
				log.Info().Msgf("%s  %-16s %-40s %-10s %s", a.ModifiedAt.Local().Format("2006-01-02 15:04"), a.Package, a.ID, a.Version, a.ModifiedBy)
			}
			state := "no change"
			if res.Changed {
				state = "changed"
			}
			log.Info().Msgf("%s since %s: %d of %d artifacts in %d packages changed; fingerprint %s", state,
				since.Local().Format(time.RFC3339), len(res.ChangedArtifacts), res.Artifacts, res.Packages, res.Fingerprint)
			return nil
		},
	}
	c.Flags().String("since", "", "A duration back from now (30m, 2h, 1d) or an RFC 3339 time (required)")
	c.Flags().String("fingerprint", "", "Fingerprint of an earlier call: a different one means changed, also by a deletion")
	c.Flags().StringSlice("packages", nil, "Only these packages (glob patterns)")
	c.Flags().Int("parallel", 8, "Packages listed at a time")
	return c
}
