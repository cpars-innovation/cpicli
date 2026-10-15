package ops

import (
	"context"
	"fmt"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// DeleteRequest names a designtime artifact to delete.
type DeleteRequest struct {
	ArtifactID string
	// Type is Integration, MessageMapping, ScriptCollection or ValueMapping;
	// empty: the type the tenant has an artifact with this ID of.
	Type string
	// UndeployFirst undeploys a deployed artifact (and waits until it is
	// gone) before deleting it; without it a deployed artifact is refused.
	UndeployFirst bool
	// Options control the undeploy polling.
	Options Options
}

// DeleteResult is the outcome of DeleteArtifact.
type DeleteResult struct {
	ArtifactID string `json:"artifactId"`
	Type       string `json:"type"`
	// Undeployed is the version that was running and was undeployed first.
	Undeployed string `json:"undeployed,omitempty"`
	Deleted    bool   `json:"deleted"`
}

// DeleteArtifact deletes a designtime artifact (Delete in the Web UI's
// package view). A deployed artifact is refused unless UndeployFirst: the
// tenant would keep running it without a designtime artifact.
func DeleteArtifact(ctx context.Context, exe *httpclnt.HTTPExecuter, req DeleteRequest) (*DeleteResult, error) {
	exe = exe.WithContext(ctx)
	if req.ArtifactID == "" {
		return nil, output.Usagef("artifact ID is required")
	}
	typ, err := resolveArtifactType(exe, req.ArtifactID, req.Type)
	if err != nil {
		return nil, err
	}
	return deleteArtifact(ctx, exe, req.ArtifactID, typ, req.UndeployFirst, req.Options)
}

func deleteArtifact(ctx context.Context, exe *httpclnt.HTTPExecuter, id, typ string, undeployFirst bool, opts Options) (*DeleteResult, error) {
	res := &DeleteResult{ArtifactID: id, Type: typ}
	running, err := cpi.NewRuntime(exe).GetArtifact(id)
	if err != nil {
		return nil, err
	}
	if running != nil {
		if !undeployFirst {
			return nil, output.Usagef("%s is deployed (version %s, %s): undeploy it first or add --undeploy-first (MCP: undeploy_first)", id, running.Version, running.Status)
		}
		results := Undeploy(ctx, NewTenant(exe), []Artifact{{ID: id, Type: typ}}, opts)
		if err := Err(results); err != nil {
			return nil, fmt.Errorf("undeploying %s before deleting it: %s", id, results[0].Error)
		}
		res.Undeployed = running.Version
	}
	if err := cpi.DeleteDesigntimeArtifact(exe, typ, id); err != nil {
		return res, err
	}
	res.Deleted = true
	return res, nil
}

// probeOrder is the order types are tried in: a tenant's
// MessageMappingDesigntimeArtifacts(Id=...) answers for an artifact of any
// type (seen on a real tenant), the other types' endpoints only for their
// own, so MessageMapping comes last.
var probeOrder = []string{"Integration", "ScriptCollection", "ValueMapping", "MessageMapping"}

// resolveArtifactType checks that the designtime artifact exists; without a
// type it finds the type the artifact has.
func resolveArtifactType(exe *httpclnt.HTTPExecuter, id, typ string) (string, error) {
	if typ != "" && !cpi.IsValidArtifactType(typ) {
		return "", output.Usagef("invalid artifact type %q (valid: %s)", typ, strings.Join(cpi.ArtifactTypes, ", "))
	}
	for _, t := range probeOrder {
		if typ != "" && t != typ && typ != "MessageMapping" {
			continue // a strict type answers only for itself
		}
		_, exists, err := cpi.GetDesigntimeInfo(exe, t, id, "active")
		if err != nil {
			return "", err
		}
		switch {
		case !exists:
		case typ == "" || t == typ:
			return t, nil
		default:
			// asked for a message mapping, but it is another type
			return "", output.Usagef("%s is of type %s, not %s", id, t, typ)
		}
	}
	if typ != "" {
		return "", output.Usagef("no %s artifact %s on the tenant", typ, id)
	}
	return "", output.Usagef("no designtime artifact %s on the tenant", id)
}

// DeletePackageRequest names a package to delete.
type DeletePackageRequest struct {
	PackageID string
	// WithArtifacts deletes the package's artifacts too; without it a package
	// that still has artifacts is refused.
	WithArtifacts bool
	// UndeployFirst undeploys deployed artifacts of the package first.
	UndeployFirst bool
	Options       Options
}

// DeletePackageResult is the outcome of DeletePackage.
type DeletePackageResult struct {
	PackageID string         `json:"packageId"`
	Artifacts []DeleteResult `json:"artifacts"`
	Deleted   bool           `json:"deleted"`
}

// DeletePackage deletes an integration package. Its artifacts are deleted
// one by one first (only with WithArtifacts); deployed ones are refused
// before anything is deleted unless UndeployFirst.
func DeletePackage(ctx context.Context, exe *httpclnt.HTTPExecuter, req DeletePackageRequest) (*DeletePackageResult, error) {
	exe = exe.WithContext(ctx)
	if req.PackageID == "" {
		return nil, output.Usagef("package ID is required")
	}
	ip := cpi.NewIntegrationPackage(exe)
	if _, _, exists, err := ip.Get(req.PackageID); err != nil {
		return nil, err
	} else if !exists {
		return nil, output.Usagef("no package %s on the tenant", req.PackageID)
	}
	arts, err := ip.GetAllArtifacts(req.PackageID)
	if err != nil {
		return nil, err
	}
	res := &DeletePackageResult{PackageID: req.PackageID, Artifacts: []DeleteResult{}}
	if len(arts) > 0 {
		ids := make([]string, 0, len(arts))
		for _, a := range arts {
			ids = append(ids, a.Id)
		}
		if !req.WithArtifacts {
			return nil, output.Usagef("package %s still has %d artifact(s) (%s): add --with-artifacts (MCP: with_artifacts) to delete them too",
				req.PackageID, len(arts), strings.Join(ids, ", "))
		}
		if !req.UndeployFirst {
			rt := cpi.NewRuntime(exe)
			var deployed []string
			for _, a := range arts {
				running, err := rt.GetArtifact(a.Id)
				if err != nil {
					return nil, err
				}
				if running != nil {
					deployed = append(deployed, a.Id)
				}
			}
			if len(deployed) > 0 {
				return nil, output.Usagef("package %s has deployed artifacts (%s): undeploy them first or add --undeploy-first (MCP: undeploy_first)",
					req.PackageID, strings.Join(deployed, ", "))
			}
		}
		for _, a := range arts {
			r, err := deleteArtifact(ctx, exe, a.Id, a.ArtifactType, req.UndeployFirst, req.Options)
			if r != nil {
				res.Artifacts = append(res.Artifacts, *r)
			}
			if err != nil {
				return res, fmt.Errorf("deleting %s of package %s: %w", a.Id, req.PackageID, err)
			}
		}
	}
	if err := cpi.DeletePackage(exe, req.PackageID); err != nil {
		return res, err
	}
	res.Deleted = true
	return res, nil
}
