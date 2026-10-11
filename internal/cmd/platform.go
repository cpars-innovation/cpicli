package cmd

import (
	"fmt"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func NewConnectionsCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "connections",
		Short: "List the integration connections the tenant computes: sender host -> flows -> receiver host",
		Long: `The tenant computes which systems its deployed integration flows connect: per
connection the sender and receiver host and the flows in between (as in the
Web UI's "Integration Connections"). --refresh makes the tenant recompute them
from the deployed flows instead of answering from its cache.`,
		Example: `  cpictl connections
  cpictl connections --filter erp --refresh`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			refresh, _ := cmd.Flags().GetBool("refresh")
			res, err := ops.ListConnections(tenantExecuter(cmd), refresh, config.GetString(cmd, "filter"))
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), res)
			for _, c := range res.Connections {
				var flows []string
				for _, f := range c.Flows {
					flows = append(flows, f.ID)
				}
				log.Info().Msgf("%s -> %s via %v", c.SenderHost, c.ReceiverHost, flows)
			}
			log.Info().Msgf("%d connection(s), %d host(s)", len(res.Connections), len(res.Hosts))
			return nil
		},
	}
	c.Flags().String("filter", "", "Only connections with a host or flow containing this")
	c.Flags().Bool("refresh", false, "Recompute the connections from the deployed flows")
	return c
}

func newLogArchiveCommand() *cobra.Command {
	c := &cobra.Command{
		Use: "archive", Short: "Download the latest log files of a type as one zip", SilenceUsage: true,
		Example: `  cpictl log-files archive --type http --out logs/http.zip
  cpictl log-files archive --type trace --out logs/trace.zip`,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ops.DownloadLogArchive(tenantExecuter(cmd), config.GetString(cmd, "type"), config.GetString(cmd, "out"))
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), res)
			log.Info().Msgf("%s log archive (%d bytes) written to %s", res.Archive.LogFileType, res.Bytes, res.File)
			return nil
		},
	}
	c.Flags().String("type", "http", "Log file type: http or trace")
	c.Flags().String("out", "", "Zip file to write")
	return c
}

func NewTenantSettingsCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "tenant-settings",
		Short: "Show tenant-wide runtime settings: external logging, archiving and its last runs",
		Example: `  cpictl tenant-settings
  cpictl tenant-settings external-logging --on --confirm
  cpictl tenant-settings archiving --on --confirm`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			res := ops.GetTenantSettings(tenantExecuter(cmd))
			output.SetResult(cmd.Context(), res)
			log.Info().Msgf("External logging: %s", onOff(res.ExternalLogging))
			log.Info().Msgf("Archiving:        %s", onOff(res.Archiving))
			for _, r := range res.ArchivingRuns {
				log.Info().Msgf("Archiving run %s: %s, %d of %d logs archived, %d failed", r.RunStart, r.RunStatus, r.MplsArchived, r.MplsToBeArchived, r.MplsArchivingFailed)
			}
			for _, w := range res.Warnings {
				log.Warn().Msg(w)
			}
			return nil
		},
	}
	ext := &cobra.Command{
		Use: "external-logging", Short: "Switch external logging of message processing logs (requires --confirm)", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			on, _ := cmd.Flags().GetBool("on")
			off, _ := cmd.Flags().GetBool("off")
			if on == off {
				return output.Usagef("give --on or --off")
			}
			if ok, _ := cmd.Flags().GetBool("confirm"); !ok {
				return output.Usagef("switching external logging changes the whole tenant; add --confirm")
			}
			if err := cpi.SetExternalLogging(tenantExecuter(cmd), on); err != nil {
				return err
			}
			output.SetResult(cmd.Context(), map[string]any{"externalLogging": on})
			log.Info().Msgf("External logging switched %s", map[bool]string{true: "on", false: "off"}[on])
			return nil
		},
	}
	ext.Flags().Bool("on", false, "Switch on")
	ext.Flags().Bool("off", false, "Switch off")
	ext.Flags().Bool("confirm", false, "Confirm the change")
	arch := &cobra.Command{
		Use: "archiving", Short: "Switch on archiving of message processing logs (requires --confirm; the API cannot switch it off)", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if on, _ := cmd.Flags().GetBool("on"); !on {
				return output.Usagef("give --on (the API offers no deactivation)")
			}
			if ok, _ := cmd.Flags().GetBool("confirm"); !ok {
				return output.Usagef("archiving changes the whole tenant and needs a document store; add --confirm")
			}
			if err := cpi.ActivateArchiving(tenantExecuter(cmd)); err != nil {
				return err
			}
			output.SetResult(cmd.Context(), map[string]any{"archiving": true})
			log.Info().Msg("Archiving switched on")
			return nil
		},
	}
	arch.Flags().Bool("on", false, "Switch on")
	arch.Flags().Bool("confirm", false, "Confirm the change")
	c.AddCommand(ext, arch)
	return c
}

func onOff(b *bool) string {
	if b == nil {
		return "unknown"
	}
	return fmt.Sprint(map[bool]string{true: "on", false: "off"}[*b])
}
