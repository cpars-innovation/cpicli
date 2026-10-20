package ops

import (
	"fmt"
	"slices"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/internal/str"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/pdrepo"
	"github.com/rs/zerolog/log"
)

// PDDeployResult is the result of PDDeploy.
type PDDeployResult struct {
	DryRun bool             `json:"dryRun"`
	String *cpi.BatchResult `json:"string"`
	Binary *cpi.BatchResult `json:"binary"`
	// Deleted are the parameters full sync deleted (DryRun: would delete).
	Deleted *cpi.BatchResult `json:"deleted,omitempty"`
	// Keys is the per-key outcome of a single-key deployment (opts.Keys).
	Keys []PDKeyResult `json:"keys,omitempty"`
}

// PDDeployOptions control PDDeploy.
type PDDeployOptions struct {
	// Replace updates existing parameters whose value differs (false: add only).
	Replace bool
	// FullSync deletes remote parameters of the managed PIDs that do not exist
	// locally. A PID whose local files cannot be read is never synced.
	FullSync bool
	DryRun   bool
	// PIDs restricts the operation to these partner IDs (empty: all local PIDs).
	PIDs []string
	// Keys ("<pid>:<id>") deploys only these parameters, always as a merge
	// (create or update, never delete); cannot be combined with FullSync.
	Keys []string
}

// PDDeploy uploads local Partner Directory parameters to the tenant. Any
// parameter/deletion error yields a partial-failure error together with the
// result.
func PDDeploy(pdAPI *cpi.PartnerDirectory, pdRepo *pdrepo.Repo, opts PDDeployOptions) (*PDDeployResult, error) {
	replace, fullSync, dryRun, pidsFilter := opts.Replace, opts.FullSync, opts.DryRun, str.TrimSlice(opts.PIDs)
	if keys := str.TrimSlice(opts.Keys); len(keys) > 0 {
		if fullSync {
			return nil, output.Usagef("keys cannot be combined with full sync (a single-key deployment never deletes)")
		}
		if len(pidsFilter) > 0 {
			return nil, output.Usagef("use either keys or pids")
		}
		results, err := pdDeployKeys(pdAPI, pdRepo, keys, dryRun)
		if results == nil {
			return nil, err
		}
		return &PDDeployResult{DryRun: dryRun, Keys: results}, err
	}
	log.Info().Msg("Starting Partner Directory Deploy...")

	// Get locally managed PIDs
	managedPIDs, err := pdRepo.GetLocalPIDs()
	if err != nil {
		return nil, fmt.Errorf("failed to get local PIDs: %w", err)
	}

	// Filter managed PIDs if filter is specified
	if len(pidsFilter) > 0 {
		filteredPIDs := filterPIDs(managedPIDs, pidsFilter)
		if len(filteredPIDs) == 0 {
			return nil, output.Usagef("no PIDs match the filter: %v", pidsFilter)
		}
		managedPIDs = filteredPIDs
		log.Info().Msgf("Filtered to %d PIDs: %v", len(managedPIDs), managedPIDs)
	}

	if fullSync && len(managedPIDs) > 0 {
		log.Warn().Msg("Full sync will delete remote parameters not in local files!")
		log.Warn().Msgf("Managed PIDs (only these will be affected):\n  - %s",
			strings.Join(managedPIDs, "\n  - "))
		log.Warn().Msg("Parameters in other PIDs will NOT be touched.")

		if dryRun {
			log.Info().Msg("DRY RUN MODE: No deletions will be performed")
		}
	}

	// Push string parameters
	stringResults, err := deployStringParameters(pdAPI, pdRepo, replace, dryRun, pidsFilter)
	if err != nil {
		return nil, fmt.Errorf("failed to deploy string parameters: %w", err)
	}

	// Push binary parameters
	binaryResults, err := deployBinaryParameters(pdAPI, pdRepo, replace, dryRun, pidsFilter)
	if err != nil {
		return nil, fmt.Errorf("failed to deploy binary parameters: %w", err)
	}

	// Collected failures make the command exit non-zero (partial failure)
	var failures []string

	// Full sync - delete remote entries not in local (only for managed PIDs)
	var deletionResults *cpi.BatchResult
	if fullSync {
		if dryRun {
			log.Info().Msg("DRY RUN: full sync would delete the remote parameters below (not present locally)")
		} else {
			log.Info().Msg("Executing full sync - deleting remote entries not present locally...")
		}
		var deletionErr error
		deletionResults, deletionErr = deleteRemoteEntriesNotInLocal(pdAPI, pdRepo, managedPIDs, dryRun)
		if deletionErr != nil {
			log.Error().Msgf("Error during full sync deletion: %v", deletionErr)
			failures = append(failures, fmt.Sprintf("full sync: %v", deletionErr))
		} else {
			verb := "Deleted"
			if dryRun {
				verb = "Would delete"
			}
			log.Info().Msgf("Parameters %s: %d", verb, len(deletionResults.Deleted))
			if len(deletionResults.Deleted) > 0 {
				log.Info().Msgf("%s parameters:", verb)
				for _, deleted := range deletionResults.Deleted {
					log.Info().Msgf("  - %s", deleted)
				}
			}
			if len(deletionResults.Errors) > 0 {
				log.Info().Msgf("Deletion Errors: %d", len(deletionResults.Errors))
				for _, err := range deletionResults.Errors {
					log.Warn().Msg(err)
				}
				failures = append(failures, deletionResults.Errors...)
			}
		}
	}

	// Log summary
	log.Info().Msgf("String Parameters - Created: %d, Updated: %d, Unchanged: %d, Errors: %d",
		len(stringResults.Created), len(stringResults.Updated), len(stringResults.Unchanged), len(stringResults.Errors))
	log.Info().Msgf("Binary Parameters - Created: %d, Updated: %d, Unchanged: %d, Errors: %d",
		len(binaryResults.Created), len(binaryResults.Updated), len(binaryResults.Unchanged), len(binaryResults.Errors))

	if fullSync && deletionResults != nil {
		log.Info().Msgf("Full Sync - Deleted: %d, Errors: %d",
			len(deletionResults.Deleted), len(deletionResults.Errors))
		if len(deletionResults.Deleted) > 0 {
			log.Info().Msgf("Deleted: %s", strings.Join(deletionResults.Deleted, ", "))
		}
	}

	if len(stringResults.Errors) > 0 || len(binaryResults.Errors) > 0 {
		log.Warn().Msg("Errors encountered during deploy:")
		for _, err := range stringResults.Errors {
			log.Warn().Msgf("String: %s", err)
		}
		for _, err := range binaryResults.Errors {
			log.Warn().Msgf("Binary: %s", err)
		}
	}

	if dryRun {
		log.Info().Msg("DRY RUN completed - no changes were made!")
	}

	for _, e := range stringResults.Errors {
		failures = append(failures, "string: "+e)
	}
	for _, e := range binaryResults.Errors {
		failures = append(failures, "binary: "+e)
	}
	summary := &PDDeployResult{DryRun: dryRun, String: stringResults, Binary: binaryResults, Deleted: deletionResults}
	if len(failures) > 0 {
		return summary, output.Partial(fmt.Errorf("partner directory deploy completed with %d error(s): %s",
			len(failures), strings.Join(failures, "; ")))
	}

	return summary, nil
}

func deployStringParameters(pdAPI *cpi.PartnerDirectory, pdRepo *pdrepo.Repo, replace bool, dryRun bool, pidsFilter []string) (*cpi.BatchResult, error) {
	log.Debug().Msg("Loading string parameters from local files")

	// Get local PIDs
	localPIDs, err := pdRepo.GetLocalPIDs()
	if err != nil {
		return nil, err
	}

	// Filter if needed
	if len(pidsFilter) > 0 {
		localPIDs = filterPIDs(localPIDs, pidsFilter)
	}

	results := &cpi.BatchResult{
		Created:   []string{},
		Updated:   []string{},
		Unchanged: []string{},
		Errors:    []string{},
	}

	// Load and deploy parameters for each PID
	for _, pid := range localPIDs {
		parameters, err := pdRepo.ReadStringParameters(pid)
		if err != nil {
			results.Errors = append(results.Errors, fmt.Sprintf("Failed to read %s: %v", pid, err))
			continue
		}

		for _, param := range parameters {
			key := fmt.Sprintf("%s/%s", param.Pid, param.ID)

			if dryRun {
				// Just check if it exists and report what would happen
				existing, err := pdAPI.GetStringParameter(param.Pid, param.ID)
				if err != nil {
					results.Errors = append(results.Errors, fmt.Sprintf("%s: %v", key, err))
					continue
				}

				if existing == nil {
					results.Created = append(results.Created, key)
					log.Info().Msgf("[DRY RUN] Would create: %s", key)
				} else if replace && existing.Value != param.Value {
					results.Updated = append(results.Updated, key)
					log.Info().Msgf("[DRY RUN] Would update: %s", key)
				} else {
					results.Unchanged = append(results.Unchanged, key)
				}
				continue
			}

			// Check if parameter exists
			existing, err := pdAPI.GetStringParameter(param.Pid, param.ID)
			if err != nil {
				results.Errors = append(results.Errors, fmt.Sprintf("%s: %v", key, err))
				continue
			}

			if existing == nil {
				// Create new parameter
				if err := pdAPI.CreateStringParameter(param); err != nil {
					results.Errors = append(results.Errors, fmt.Sprintf("%s: %v", key, err))
				} else {
					results.Created = append(results.Created, key)
					log.Debug().Msgf("Created: %s", key)
				}
			} else if replace && existing.Value != param.Value {
				// Update existing parameter
				if err := pdAPI.UpdateStringParameter(param); err != nil {
					results.Errors = append(results.Errors, fmt.Sprintf("%s: %v", key, err))
				} else {
					results.Updated = append(results.Updated, key)
					log.Debug().Msgf("Updated: %s", key)
				}
			} else {
				results.Unchanged = append(results.Unchanged, key)
			}
		}
	}

	return results, nil
}

func deployBinaryParameters(pdAPI *cpi.PartnerDirectory, pdRepo *pdrepo.Repo, replace bool, dryRun bool, pidsFilter []string) (*cpi.BatchResult, error) {
	log.Debug().Msg("Loading binary parameters from local files")

	// Get local PIDs
	localPIDs, err := pdRepo.GetLocalPIDs()
	if err != nil {
		return nil, err
	}

	// Filter if needed
	if len(pidsFilter) > 0 {
		localPIDs = filterPIDs(localPIDs, pidsFilter)
	}

	results := &cpi.BatchResult{
		Created:   []string{},
		Updated:   []string{},
		Unchanged: []string{},
		Errors:    []string{},
	}

	// Load and deploy parameters for each PID
	for _, pid := range localPIDs {
		parameters, err := pdRepo.ReadBinaryParameters(pid)
		if err != nil {
			results.Errors = append(results.Errors, fmt.Sprintf("Failed to read %s: %v", pid, err))
			continue
		}

		for _, param := range parameters {
			key := fmt.Sprintf("%s/%s", param.Pid, param.ID)

			if dryRun {
				// Just check if it exists and report what would happen
				existing, err := pdAPI.GetBinaryParameter(param.Pid, param.ID)
				if err != nil {
					results.Errors = append(results.Errors, fmt.Sprintf("%s: %v", key, err))
					continue
				}

				if existing == nil {
					results.Created = append(results.Created, key)
					log.Info().Msgf("[DRY RUN] Would create: %s", key)
				} else if replace && existing.Value != param.Value {
					results.Updated = append(results.Updated, key)
					log.Info().Msgf("[DRY RUN] Would update: %s", key)
				} else {
					results.Unchanged = append(results.Unchanged, key)
				}
				continue
			}

			// Check if parameter exists
			existing, err := pdAPI.GetBinaryParameter(param.Pid, param.ID)
			if err != nil {
				results.Errors = append(results.Errors, fmt.Sprintf("%s: %v", key, err))
				continue
			}

			if existing == nil {
				// Create new parameter
				if err := pdAPI.CreateBinaryParameter(param); err != nil {
					results.Errors = append(results.Errors, fmt.Sprintf("%s: %v", key, err))
				} else {
					results.Created = append(results.Created, key)
					log.Debug().Msgf("Created: %s", key)
				}
			} else if replace && existing.Value != param.Value {
				// Update existing parameter
				if err := pdAPI.UpdateBinaryParameter(param); err != nil {
					results.Errors = append(results.Errors, fmt.Sprintf("%s: %v", key, err))
				} else {
					results.Updated = append(results.Updated, key)
					log.Debug().Msgf("Updated: %s", key)
				}
			} else {
				results.Unchanged = append(results.Unchanged, key)
			}
		}
	}

	return results, nil
}

// deleteRemoteEntriesNotInLocal deletes the remote parameters of the
// managed PIDs that are not present locally; dryRun only lists them.
func deleteRemoteEntriesNotInLocal(pdAPI *cpi.PartnerDirectory, pdRepo *pdrepo.Repo, managedPIDs []string, dryRun bool) (*cpi.BatchResult, error) {
	results := &cpi.BatchResult{
		Deleted: []string{},
		Errors:  []string{},
	}

	// Load local parameters for managed PIDs. A PID is only eligible for deletion
	// when BOTH its string and binary parameters were read successfully: an
	// unreadable local file must never be interpreted as "no local parameters",
	// which would delete every remote parameter of that PID.
	localStringParams := make(map[string]map[string]bool) // PID -> ID -> exists
	localBinaryParams := make(map[string]map[string]bool)
	var syncablePIDs []string

	for _, pid := range managedPIDs {
		stringParams, err := pdRepo.ReadStringParameters(pid)
		if err != nil {
			msg := fmt.Sprintf("Full sync aborted for PID %s: failed to read local string parameters: %v", pid, err)
			log.Error().Msg(msg)
			results.Errors = append(results.Errors, msg)
			continue
		}
		binaryParams, err := pdRepo.ReadBinaryParameters(pid)
		if err != nil {
			msg := fmt.Sprintf("Full sync aborted for PID %s: failed to read local binary parameters: %v", pid, err)
			log.Error().Msg(msg)
			results.Errors = append(results.Errors, msg)
			continue
		}

		localStringParams[pid] = make(map[string]bool, len(stringParams))
		for _, param := range stringParams {
			localStringParams[pid][param.ID] = true
		}
		localBinaryParams[pid] = make(map[string]bool, len(binaryParams))
		for _, param := range binaryParams {
			localBinaryParams[pid][param.ID] = true
		}
		syncablePIDs = append(syncablePIDs, pid)
	}

	if len(syncablePIDs) == 0 {
		return results, nil
	}

	// Get all remote string parameters
	remoteStringParams, err := pdAPI.GetStringParameters("Pid,Id")
	if err != nil {
		return nil, fmt.Errorf("failed to get remote string parameters: %w", err)
	}

	// Delete string parameters not in local for managed PIDs
	for _, param := range remoteStringParams {
		if !slices.Contains(syncablePIDs, param.Pid) {
			continue // Skip PIDs we don't manage or could not read locally
		}

		if !localStringParams[param.Pid][param.ID] {
			key := fmt.Sprintf("%s/%s", param.Pid, param.ID)
			if dryRun {
				results.Deleted = append(results.Deleted, key)
			} else if err := pdAPI.DeleteStringParameter(param.Pid, param.ID); err != nil {
				results.Errors = append(results.Errors, fmt.Sprintf("Failed to delete string %s: %v", key, err))
			} else {
				results.Deleted = append(results.Deleted, key)
				log.Debug().Msgf("Deleted string parameter: %s", key)
			}
		}
	}

	// Get all remote binary parameters
	remoteBinaryParams, err := pdAPI.GetBinaryParameters("Pid,Id")
	if err != nil {
		return nil, fmt.Errorf("failed to get remote binary parameters: %w", err)
	}

	// Delete binary parameters not in local for managed PIDs
	for _, param := range remoteBinaryParams {
		if !slices.Contains(syncablePIDs, param.Pid) {
			continue // Skip PIDs we don't manage or could not read locally
		}

		if !localBinaryParams[param.Pid][param.ID] {
			key := fmt.Sprintf("%s/%s", param.Pid, param.ID)
			if dryRun {
				results.Deleted = append(results.Deleted, key)
			} else if err := pdAPI.DeleteBinaryParameter(param.Pid, param.ID); err != nil {
				results.Errors = append(results.Errors, fmt.Sprintf("Failed to delete binary %s: %v", key, err))
			} else {
				results.Deleted = append(results.Deleted, key)
				log.Debug().Msgf("Deleted binary parameter: %s", key)
			}
		}
	}

	return results, nil
}

func filterPIDs(pids []string, filter []string) []string {
	if len(filter) == 0 {
		return pids
	}

	result := make([]string, 0)
	for _, pid := range pids {
		if slices.Contains(filter, pid) {
			result = append(result, pid)
		}
	}
	return result
}
