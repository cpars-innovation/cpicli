package ops

import (
	"fmt"
	"slices"

	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/pdrepo"
	"github.com/rs/zerolog/log"
)

// PDSnapshotResult is the outcome of PDSnapshot.
type PDSnapshotResult struct {
	// StringParameters and BinaryParameters are the numbers downloaded.
	StringParameters int `json:"stringParameters"`
	BinaryParameters int `json:"binaryParameters"`
	// PIDs are the Partner IDs written, sorted.
	PIDs []string `json:"pids"`
}

// PDSnapshot downloads the Partner Directory parameters (cpictl
// pd-snapshot) into the local files of repo: string parameters into
// <PID>/String.properties, binary ones into <PID>/Binary/. replace
// overwrites existing values, else only missing parameters are added. pids
// limits the snapshot to these Partner IDs (empty: all).
func PDSnapshot(api *cpi.PartnerDirectory, repo *pdrepo.Repo, replace bool, pids []string) (*PDSnapshotResult, error) {
	log.Info().Msg("Starting Partner Directory Snapshot...")
	res := &PDSnapshotResult{PIDs: []string{}}

	stringParams, binaries, err := snapshotParams(api, pids)
	if err != nil {
		return nil, err
	}
	byPID := groupByPID(stringParams, pids, func(p cpi.StringParameter) string { return p.Pid })
	log.Info().Msgf("Found %d Partner IDs with string parameters: %v", len(byPID), sortedKeys(byPID))
	for _, pid := range sortedKeys(byPID) {
		log.Info().Msgf("Processing string parameters for PID: %s (%d parameters)", pid, len(byPID[pid]))
		if err := repo.WriteStringParameters(pid, byPID[pid], replace); err != nil {
			return nil, fmt.Errorf("failed to download string parameters: failed to write string parameters for PID %s: %w", pid, err)
		}
		res.StringParameters += len(byPID[pid])
		res.PIDs = append(res.PIDs, pid)
	}
	log.Info().Msgf("Downloaded %d string parameters", res.StringParameters)

	binByPID := groupByPID(binaries, pids, func(p cpi.BinaryParameter) string { return p.Pid })
	log.Info().Msgf("Found %d Partner IDs with binary parameters: %v", len(binByPID), sortedKeys(binByPID))
	for _, pid := range sortedKeys(binByPID) {
		log.Info().Msgf("Processing binary parameters for PID: %s (%d parameters)", pid, len(binByPID[pid]))
		if err := repo.WriteBinaryParameters(pid, binByPID[pid], replace); err != nil {
			return nil, fmt.Errorf("failed to download binary parameters: failed to write binary parameters for PID %s: %w", pid, err)
		}
		res.BinaryParameters += len(binByPID[pid])
		if !slices.Contains(res.PIDs, pid) {
			res.PIDs = append(res.PIDs, pid)
		}
	}
	log.Info().Msgf("Downloaded %d binary parameters", res.BinaryParameters)
	slices.Sort(res.PIDs)
	for _, pid := range pids {
		if !slices.Contains(res.PIDs, pid) {
			// nothing came back: no parameters, or (under a scope or through
			// the cpictl gateway) no access to the partner
			log.Warn().Msgf("PID %s: no parameters found (or no access to this partner)", pid)
		}
	}
	return res, nil
}

// snapshotParams reads all parameters, or with pids each partner's by a
// filter on the tenant: under a scope a list of all partners is filtered
// page by page, and asking for the partners themselves is cheaper anyway.
func snapshotParams(api *cpi.PartnerDirectory, pids []string) ([]cpi.StringParameter, []cpi.BinaryParameter, error) {
	if len(pids) == 0 {
		strs, err := api.GetStringParameters("Pid,Id,Value")
		if err != nil {
			return nil, nil, fmt.Errorf("failed to download string parameters: %w", err)
		}
		bins, err := api.GetBinaryParameters("")
		if err != nil {
			return nil, nil, fmt.Errorf("failed to download binary parameters: %w", err)
		}
		return strs, bins, nil
	}
	var strs []cpi.StringParameter
	var bins []cpi.BinaryParameter
	for _, pid := range slices.Compact(slices.Sorted(slices.Values(pids))) {
		s, b, err := api.ListParametersSelect(pid, "Pid,Id,Value", "")
		if err != nil {
			return nil, nil, fmt.Errorf("failed to download the parameters of %s: %w", pid, err)
		}
		strs, bins = append(strs, s...), append(bins, b...)
	}
	return strs, bins, nil
}

// groupByPID groups parameters by Partner ID, keeping only the PIDs of
// filter (empty: all).
func groupByPID[P any](params []P, filter []string, pid func(P) string) map[string][]P {
	out := map[string][]P{}
	for _, p := range params {
		if id := pid(p); len(filter) == 0 || slices.Contains(filter, id) {
			out[id] = append(out[id], p)
		}
	}
	return out
}
