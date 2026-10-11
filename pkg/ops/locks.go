package ops

import (
	"fmt"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// ListLocks lists the edit locks of the tenant, optionally only those of an
// artifact or a package.
func ListLocks(exe *httpclnt.HTTPExecuter, artifactID, packageID string) ([]cpi.DesigntimeLock, error) {
	locks, err := cpi.DesigntimeLocks(exe)
	if err != nil {
		return nil, err
	}
	out := []cpi.DesigntimeLock{}
	for _, l := range locks {
		if (artifactID == "" || l.ArtifactID == artifactID) && (packageID == "" || l.PackageID == packageID) {
			out = append(out, l)
		}
	}
	return out, nil
}

// UnlockResult is the outcome of Unlock.
type UnlockResult struct {
	Removed []cpi.DesigntimeLock `json:"removed"`
}

// Unlock removes the edit lock of an artifact (as "Unlock" in the Web UI's
// package view). Whoever held it loses unsaved changes. A missing lock is a
// usage error, so a typo is not reported as success.
func Unlock(exe *httpclnt.HTTPExecuter, artifactID string) (*UnlockResult, error) {
	if artifactID == "" {
		return nil, output.Usagef("artifact ID is required")
	}
	locks, err := ListLocks(exe, artifactID, "")
	if err != nil {
		return nil, err
	}
	if len(locks) == 0 {
		return nil, output.Usagef("%s is not locked", artifactID)
	}
	res := &UnlockResult{Removed: []cpi.DesigntimeLock{}}
	for _, l := range locks {
		if err := cpi.DeleteDesigntimeLock(exe, l.ResourceID); err != nil {
			return res, fmt.Errorf("unlocking %s (held by %s): %w", artifactID, l.CreatedBy, err)
		}
		res.Removed = append(res.Removed, l)
	}
	return res, nil
}
