package ops

import (
	"fmt"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"

	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/versioning"
)

// DeployDecision predicts what a deployment does for one artifact (plan
// mode), with the rules of Deploy: runtime is the running artifact (nil: not
// deployed), version the designtime version after the upload, changed
// whether the upload changes its content or configuration.
func DeployDecision(runtime *cpi.RuntimeArtifact, version string, changed bool, mode versioning.Mode, allowDowngrade bool) (deploy bool, reason string, refused bool) {
	switch {
	case runtime == nil:
		return true, "not deployed yet", false
	case version == "":
		return true, "designtime version assigned by the tenant on creation", false
	case changed && runtime.Version == version:
		return true, fmt.Sprintf("content changed, same version %s: deployed again", version), false
	}
	cmp := CompareVersions(version, runtime.Version)
	switch {
	case cmp < 0 && allowDowngrade:
		return true, fmt.Sprintf("designtime %s lower than running %s, allowDowngrade", version, runtime.Version), false
	case cmp < 0 && (mode == versioning.Manifest || mode == versioning.TenantBump):
		return false, fmt.Sprintf("refused: designtime %s lower than running %s (raise Bundle-Version)", version, runtime.Version), true
	case cmp < 0 && mode == versioning.Keep:
		return true, fmt.Sprintf("designtime %s, running %s (keep: no guard)", version, runtime.Version), false
	case cmp < 0:
		return true, fmt.Sprintf("designtime %s lower than running %s: deployed only if changed after the deployment (else refused)", version, runtime.Version), false
	case cmp > 0:
		return true, fmt.Sprintf("designtime %s, running %s", version, runtime.Version), false
	case runtime.Status != "STARTED":
		return true, fmt.Sprintf("running %s with status %s", runtime.Version, runtime.Status), false
	}
	return false, fmt.Sprintf("running %s already (STARTED)", runtime.Version), false
}

// DeployPlan is the predicted deployment of one artifact.
type DeployPlan struct {
	ID            string `json:"id"`
	Designtime    string `json:"designtime,omitempty"`
	Running       string `json:"running,omitempty"`
	RuntimeStatus string `json:"runtimeStatus,omitempty"`
	Deploy        bool   `json:"deploy"`
	Reason        string `json:"reason"`
	// Skipped is "draft" for an artifact in draft on the tenant.
	Skipped string `json:"skipped,omitempty"`
	Error   string `json:"error,omitempty"`
}

// PlanDeploy predicts Deploy for the artifacts without triggering anything
// (compare-versions semantics: an artifact already running its designtime
// version is not deployed).
func PlanDeploy(exe *httpclnt.HTTPExecuter, artifactType string, ids []string, mode versioning.Mode, allowDowngrade bool) ([]DeployPlan, error) {
	dt := cpi.NewDesigntimeArtifact(artifactType, exe)
	rt := cpi.NewRuntime(exe)
	out := make([]DeployPlan, 0, len(ids))
	failed := 0
	for _, id := range ids {
		p := DeployPlan{ID: id}
		version, _, exists, err := dt.Get(id, "active")
		switch {
		case err != nil:
			p.Error = err.Error()
		case !exists:
			p.Error = "designtime artifact does not exist"
		case IsDraftVersion(version):
			p.Designtime, p.Skipped, p.Reason = version, SkippedDraft, DraftReason
		default:
			p.Designtime = version
			running, err := rt.GetArtifact(id)
			if err != nil {
				p.Error = err.Error()
				break
			}
			if running != nil {
				p.Running, p.RuntimeStatus = running.Version, running.Status
			}
			var refused bool
			p.Deploy, p.Reason, refused = DeployDecision(running, version, false, mode, allowDowngrade)
			if refused {
				p.Error = p.Reason
			}
		}
		if p.Error != "" {
			failed++
		}
		out = append(out, p)
	}
	if failed > 0 {
		return out, output.Failed(fmt.Errorf("%d of %d artifact(s) would fail", failed, len(ids)))
	}
	return out, nil
}
