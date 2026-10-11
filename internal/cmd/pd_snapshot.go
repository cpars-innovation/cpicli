package cmd

import (
	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/str"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/cpars-innovation/cpicli/pkg/pdrepo"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func NewPDSnapshotCommand() *cobra.Command {

	pdSnapshotCmd := &cobra.Command{
		Use:   "pd-snapshot",
		Short: "Download Partner Directory parameters into local files",
		Long: `Download all partner directory parameters from SAP CPI and save them locally.

This command retrieves both string and binary parameters from the SAP CPI Partner Directory
and organizes them in a local directory structure:

  {PID}/
    String.properties    - String parameters as key=value pairs
    Binary/              - Binary parameters as individual files
      {ParamId}.{ext}    - Binary parameter files
      _metadata.json     - Content type metadata

The snapshot operation supports two modes:
  - Replace mode (default): Overwrites existing local files
  - Add-only mode: Only adds new parameters, preserves existing values

See docs/partner-directory.md for the file format and full sync safety rules.`,
		Example: `  # Snapshot in add-only mode (don't overwrite existing values)
  cpictl pd-snapshot --replace=false

  # Snapshot only specific PIDs
  cpictl pd-snapshot --pids "SAP_SYSTEM_001,CUSTOMER_API"`,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			if err = runPDSnapshot(cmd); err != nil {
				cmd.SilenceUsage = true
			}
			return
		},
	}

	// Define flags
	// Note: These can be set in config file under 'pd-snapshot' key
	pdSnapshotCmd.Flags().String("resources-path", "./partner-directory",
		"Path to save partner directory parameters")
	pdSnapshotCmd.Flags().Bool("replace", true,
		"Replace existing values (false = add only missing values)")
	pdSnapshotCmd.Flags().StringSlice("pids", nil,
		"Comma separated list of Partner IDs to snapshot (e.g., 'PID1,PID2')")

	return pdSnapshotCmd
}

func runPDSnapshot(cmd *cobra.Command) error {
	serviceDetails := serviceDetails(cmd)

	log.Info().Msg("Executing Partner Directory Snapshot command")

	// Support reading from config file under 'pd-snapshot' key
	resourcesPath := config.GetStringWithFallback(cmd, "resources-path", "pd-snapshot.resources-path")
	replace := config.GetBoolWithFallback(cmd, "replace", "pd-snapshot.replace")
	pids := config.GetStringSliceWithFallback(cmd, "pids", "pd-snapshot.pids")

	log.Info().Msgf("Resources Path: %s", resourcesPath)
	log.Info().Msgf("Replace Mode: %v", replace)
	if len(pids) > 0 {
		log.Info().Msgf("Filter PIDs: %v", pids)
	}

	// Trim PIDs
	pids = str.TrimSlice(pids)

	// Initialise HTTP executer
	exe := cpi.InitHTTPExecuter(serviceDetails)

	// Initialise Partner Directory API
	pdAPI := cpi.NewPartnerDirectory(exe)

	// Initialise Partner Directory Repository
	pdRepo := pdrepo.New(resourcesPath)

	// Execute snapshot
	if _, err := ops.PDSnapshot(pdAPI, pdRepo, replace, pids); err != nil {
		return err
	}

	log.Info().Msg("🏆 Partner Directory Snapshot completed successfully")
	return nil
}
