package ops

import (
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// RuntimeInfo is one runtime of the tenant (Cloud Integration or an
// integration cell), with its deployed artifacts when asked for.
type RuntimeInfo struct {
	cpi.RuntimeLocation
	Artifacts []LocatedArtifactInfo `json:"artifacts,omitempty"`
}

// LocatedArtifactInfo is a deployed artifact on one runtime.
type LocatedArtifactInfo struct {
	ID               string     `json:"id"`
	Name             string     `json:"name,omitempty"`
	Version          string     `json:"version,omitempty"`
	Status           string     `json:"status"`      // STARTED, ERROR, ...
	DeployState      string     `json:"deployState"` // DEPLOYED, FAILED
	WorkspaceProfile string     `json:"workspaceProfile,omitempty"`
	DeployedBy       string     `json:"deployedBy,omitempty"`
	DeployedOn       *time.Time `json:"deployedOn,omitempty"`
}

// ListRuntimes lists the tenant's runtimes and, with artifacts, what is
// deployed on each (only runtime location if set). The public API reports
// only the Cloud Integration runtime; this reads the Web UI's runtime list
// (Monitor > Manage Integration Content), which a tenant answers for an API
// key too.
func ListRuntimes(exe *httpclnt.HTTPExecuter, artifacts bool, only string) ([]RuntimeInfo, error) {
	rt := cpi.NewRuntime(exe)
	locs, err := rt.Locations()
	if err != nil {
		return nil, err
	}
	out := []RuntimeInfo{}
	for _, l := range locs {
		if only != "" && l.ID != only {
			continue
		}
		info := RuntimeInfo{RuntimeLocation: l}
		if artifacts {
			arts, err := rt.LocationArtifacts(l.ID)
			if err != nil {
				return nil, err
			}
			info.Artifacts = []LocatedArtifactInfo{}
			for _, a := range arts {
				ai := LocatedArtifactInfo{ID: a.Id, Name: a.Name, Version: a.Version, Status: a.Status, DeployState: a.DeployState,
					WorkspaceProfile: a.WorkspaceProfile, DeployedBy: a.DeployedBy}
				if !a.DeployedOn.IsZero() {
					on := a.DeployedOn
					ai.DeployedOn = &on
				}
				info.Artifacts = append(info.Artifacts, ai)
			}
		}
		out = append(out, info)
	}
	if only != "" && len(out) == 0 {
		return nil, output.Usagef("no runtime %q on this tenant (runtimes: %s)", only, locationIDs(locs))
	}
	return out, nil
}

func locationIDs(locs []cpi.RuntimeLocation) string {
	s := ""
	for i, l := range locs {
		if i > 0 {
			s += ", "
		}
		s += l.ID
	}
	return s
}
