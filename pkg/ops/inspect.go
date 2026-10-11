package ops

import (
	"sort"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// RuntimeStatus is the runtime state of one artifact.
type RuntimeStatus struct {
	ID         string     `json:"id"`
	Deployed   bool       `json:"deployed"`
	Version    string     `json:"version,omitempty"`
	Status     string     `json:"status,omitempty"`
	Type       string     `json:"type,omitempty"`
	DeployedBy string     `json:"deployedBy,omitempty"`
	DeployedOn *time.Time `json:"deployedOn,omitempty"`
	// ErrorInfo is filled for artifacts in status ERROR (best effort).
	ErrorInfo string `json:"errorInfo,omitempty"`
	Error     string `json:"error,omitempty"`
	Err       error  `json:"-"`
}

// GetRuntimeStatus reads the runtime state of each artifact. Lookup errors
// are reported per artifact.
func GetRuntimeStatus(exe *httpclnt.HTTPExecuter, ids []string) []RuntimeStatus {
	rt := cpi.NewRuntime(exe)
	statuses := make([]RuntimeStatus, 0, len(ids))
	for _, id := range ids {
		s := RuntimeStatus{ID: id}
		artifact, err := rt.GetArtifact(id)
		switch {
		case err != nil:
			s.Err, s.Error = err, err.Error()
		case artifact != nil:
			s.Deployed = true
			s.Version, s.Status, s.Type, s.DeployedBy = artifact.Version, artifact.Status, artifact.Type, artifact.DeployedBy
			if !artifact.DeployedOn.IsZero() {
				on := artifact.DeployedOn
				s.DeployedOn = &on
			}
			if artifact.Status == "ERROR" {
				s.ErrorInfo, _ = rt.GetErrorInfo(id)
			}
		}
		statuses = append(statuses, s)
	}
	return statuses
}

// Package is an integration package on the tenant.
type Package struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
}

// ListPackages returns all integration packages sorted by ID.
func ListPackages(exe *httpclnt.HTTPExecuter) ([]Package, error) {
	data, err := cpi.NewIntegrationPackage(exe).GetPackagesData()
	if err != nil {
		return nil, err
	}
	packages := make([]Package, 0, len(data))
	for _, p := range data {
		packages = append(packages, Package{ID: p.Root.Id, Name: p.Root.Name, Version: p.Root.Version, Description: p.Root.Description})
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].ID < packages[j].ID })
	return packages, nil
}

// DesigntimeArtifact is a designtime artifact of a package.
type DesigntimeArtifact struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Version string `json:"version"`
	IsDraft bool   `json:"isDraft"`
	// ModifiedBy and ModifiedAt are the last change of the designtime
	// artifact (ModifiedBy, else CreatedBy); empty when the tenant does not
	// report them.
	ModifiedBy string     `json:"modifiedBy,omitempty"`
	ModifiedAt *time.Time `json:"modifiedAt,omitempty"`
}

// ListArtifacts returns all designtime artifacts (all types) of a package.
func ListArtifacts(exe *httpclnt.HTTPExecuter, packageID string) ([]DesigntimeArtifact, error) {
	details, err := cpi.NewIntegrationPackage(exe).GetAllArtifacts(packageID)
	if err != nil {
		return nil, err
	}
	artifacts := make([]DesigntimeArtifact, 0, len(details))
	for _, d := range details {
		a := DesigntimeArtifact{ID: d.Id, Name: d.Name, Type: d.ArtifactType, Version: d.Version, IsDraft: d.IsDraft, ModifiedBy: d.ModifiedBy}
		if !d.ModifiedAt.IsZero() {
			modified := d.ModifiedAt
			a.ModifiedAt = &modified
		}
		artifacts = append(artifacts, a)
	}
	return artifacts, nil
}
