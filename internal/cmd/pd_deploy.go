package cmd

import (
	"fmt"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/internal/str"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/cpars-innovation/cpicli/pkg/pdrepo"
	"github.com/cpars-innovation/cpicli/pkg/scope"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func NewPDDeployCommand() *cobra.Command {

	pdDeployCmd := &cobra.Command{
		Use:   "pd-deploy",
		Short: "Upload Partner Directory parameters from local files",
		Long: `Upload all partner directory parameters from local files to SAP CPI.

This command reads partner directory parameters from a local directory structure
and uploads them to the SAP CPI Partner Directory:

  {PID}/
    String.properties    - String parameters as key=value pairs
    Binary/              - Binary parameters as individual files
      {ParamId}.{ext}    - Binary parameter files
      _metadata.json     - Content type metadata

The deploy operation supports several modes:
  - Replace mode (default): Updates existing parameters with local values
  - Add-only mode: Only creates new parameters, skips existing ones
  - Full sync mode: Deletes remote parameters not present locally (local is source of truth)

See docs/partner-directory.md for the file format and full sync safety rules.`,
		Example: `  # Deploy in add-only mode (don't update existing parameters)
  cpictl pd-deploy --replace=false

  # Deploy with full sync (delete remote parameters not in local)
  cpictl pd-deploy --full-sync

  # Deploy only specific PIDs
  cpictl pd-deploy --pids "SAP_SYSTEM_001,CUSTOMER_API"

  # Dry run to see what would be changed
  cpictl pd-deploy --dry-run

  # Only one mapping (no other parameter is touched)
  cpictl pd-deploy --keys ONE_OMS:now_email`,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			if err = runPDDeploy(cmd); err != nil {
				cmd.SilenceUsage = true
			}
			return
		},
	}

	// Define flags
	// Note: These can be set in config file under 'pd-deploy' key
	pdDeployCmd.Flags().String("resources-path", "./partner-directory",
		"Path to partner directory parameters")
	pdDeployCmd.Flags().Bool("replace", true,
		"Replace existing values (false = add only missing values)")
	pdDeployCmd.Flags().Bool("full-sync", false,
		"Delete remote parameters not present locally (local is source of truth)")
	pdDeployCmd.Flags().Bool("dry-run", false,
		"Show what would be changed without making changes")
	pdDeployCmd.Flags().StringSlice("pids", nil,
		"Comma separated list of Partner IDs to deploy (e.g., 'PID1,PID2')")
	pdDeployCmd.Flags().StringSlice("keys", nil,
		"Deploy only these parameters, PID:ID (create or update, never delete; not with --full-sync)")

	return pdDeployCmd
}

// narrowPDsToScope limits a deploy to the partners the scope lets it change:
// the process's scope, or in gateway mode the caller's scope on this SAP
// tenant as the gateway's /whoami reports it. Without --pids the local
// partners outside it are skipped with a warning (a full sync of a whole
// local tree then touches only the granted partners, instead of collecting
// refusals); a --pids entry outside it is a usage error before any change.
// Without a scope, or when /whoami cannot be read, the PIDs stay as given
// and the gateway decides each request.
func narrowPDsToScope(cmd *cobra.Command, repo *pdrepo.Repo, pids []string, dryRun bool) ([]string, error) {
	s := activeScope
	if s == nil {
		gw := config.GetString(cmd, "gateway")
		if gw == "" || isOffline(cmd) {
			return pids, nil
		}
		who, err := fetchWhoAmI(cmd, gw)
		if err != nil {
			log.Debug().Msgf("pd deploy: the gateway's grant is not known (%v); each request is decided by the gateway", err)
			return pids, nil
		}
		t, ok := who.Tenants[gatewayTenant(gw)]
		if !ok || t.Scope == nil {
			return nil, pdScopeRefusalf("pd deploy: the gateway grants you nothing on this SAP tenant (cpictl auth status)")
		}
		s = t.Scope
	}
	action := scope.ActionPDWrite
	if dryRun {
		action = scope.ActionRead
	}
	allowed := func(pid string) bool { return s.Allows(scope.Resource{Kind: scope.KindPartner, ID: pid}, action) }
	if len(pids) > 0 {
		var outside []string
		for _, pid := range pids {
			if !allowed(pid) {
				outside = append(outside, pid)
			}
		}
		if len(outside) > 0 {
			return nil, pdScopeRefusalf("pd deploy: %s of partner(s) %s is not granted (cpictl auth status shows your partners)", action, strings.Join(outside, ", "))
		}
		return pids, nil
	}
	local, err := repo.GetLocalPIDs()
	if err != nil {
		return pids, nil // reported by the deploy itself
	}
	var keep, skipped []string
	for _, pid := range local {
		if allowed(pid) {
			keep = append(keep, pid)
		} else {
			skipped = append(skipped, pid)
		}
	}
	if len(skipped) > 0 {
		log.Warn().Msgf("pd deploy: skipping %d local partner(s) outside your grant: %s", len(skipped), strings.Join(skipped, ", "))
	}
	if len(keep) == 0 && len(local) > 0 {
		return nil, pdScopeRefusalf("pd deploy: none of the local partners is granted %s (cpictl auth status shows your partners)", action)
	}
	return keep, nil
}

// pdScopeRefusal is a deploy refused before any request because the scope
// does not grant it: exit code 9 and category scope, like the guard's own
// refusals.
type pdScopeRefusal string

func (e pdScopeRefusal) Error() string { return string(e) }
func (e pdScopeRefusal) ExitCode() int { return exitcode.Scope }

func pdScopeRefusalf(format string, args ...any) error {
	return pdScopeRefusal(fmt.Sprintf(format, args...))
}

func runPDDeploy(cmd *cobra.Command) error {
	serviceDetails := serviceDetails(cmd)

	log.Info().Msg("Executing Partner Directory Deploy command")

	// Support reading from config file under 'pd-deploy' key
	resourcesPath := config.GetStringWithFallback(cmd, "resources-path", "pd-deploy.resources-path")
	replace := config.GetBoolWithFallback(cmd, "replace", "pd-deploy.replace")
	fullSync := config.GetBoolWithFallback(cmd, "full-sync", "pd-deploy.full-sync")
	dryRun := config.GetBoolWithFallback(cmd, "dry-run", "pd-deploy.dry-run")
	pids := config.GetStringSliceWithFallback(cmd, "pids", "pd-deploy.pids")
	keys := config.GetStringSlice(cmd, "keys")

	log.Info().Msgf("Resources Path: %s", resourcesPath)
	log.Info().Msgf("Replace Mode: %v", replace)
	log.Info().Msgf("Full Sync Mode: %v", fullSync)
	log.Info().Msgf("Dry Run: %v", dryRun)
	if len(pids) > 0 {
		log.Info().Msgf("Filter PIDs: %v", pids)
	}

	// Initialise HTTP executer
	exe := cpi.InitHTTPExecuter(serviceDetails)

	// Initialise Partner Directory API
	pdAPI := cpi.NewPartnerDirectory(exe)

	// Initialise Partner Directory Repository
	pdRepo := pdrepo.New(resourcesPath)

	// Trim PIDs
	pids = str.TrimSlice(pids)
	if len(keys) == 0 {
		var err error
		if pids, err = narrowPDsToScope(cmd, pdRepo, pids, dryRun); err != nil {
			return err
		}
	}

	// Execute deploy
	summary, err := ops.PDDeploy(pdAPI, pdRepo, ops.PDDeployOptions{Replace: replace, FullSync: fullSync, DryRun: dryRun, PIDs: pids, Keys: keys})
	if summary != nil {
		output.SetResult(cmd.Context(), summary)
	}
	if err != nil {
		return err
	}

	log.Info().Msg("🏆 Partner Directory Deploy completed successfully")
	return nil
}
