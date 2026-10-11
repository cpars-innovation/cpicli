package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/internal/sync"
	"github.com/cpars-innovation/cpicli/internal/versioning"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/snapshot"
)

// UploadRequest describes a local artifact directory to upload.
type UploadRequest struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"` // defaults to ID
	Type      string `json:"type"`           // Integration, MessageMapping, ScriptCollection, ValueMapping
	PackageID string `json:"packageId"`
	Dir       string `json:"dir"` // contains META-INF/MANIFEST.MF and src/main/resources
	// Versioning decides the designtime version (see docs/versioning.md).
	Versioning versioning.Mode `json:"-"`
	// DryRun compares and reports what the upload would do without writing.
	DryRun bool `json:"-"`
	// SkipDraft returns a result with Skipped "draft" instead of an error
	// when the tenant's artifact is in draft.
	SkipDraft bool `json:"-"`
	// Baseline is the snapshot state of the target tenant (snapshot.Take,
	// snapshot.LoadState): when it covers the artifact and the tenant's
	// version and ModifiedAt are still the snapshot's, the local content is
	// compared with the hash the snapshot recorded instead of downloading
	// the artifact (Compared "snapshot"; otherwise "download" with
	// CompareReason). Use only a state of the same tenant.
	Baseline *snapshot.State `json:"-"`
	// KeepRuntime: the upload never undeploys the runtime artifact. A
	// content change with the running version sets Redeploy instead (deploy
	// it afterwards with force; it keeps running until then). Without it,
	// such a runtime artifact is undeployed (RuntimeUndeployed).
	KeepRuntime bool `json:"-"`
}

// UploadResult is the outcome of UploadArtifact.
type UploadResult struct {
	ID        string `json:"id"`
	PackageID string `json:"packageId"`
	sync.UploadOutcome
	// Skipped is "draft" when the artifact is in draft on the tenant and
	// was not uploaded (SkipDraft); Reason says why.
	Skipped string `json:"skipped,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// UploadArtifact creates the designtime artifact or updates it if the local
// content differs. It does not deploy.
func UploadArtifact(exe *httpclnt.HTTPExecuter, req UploadRequest) (*UploadResult, error) {
	if !cpi.IsValidArtifactType(req.Type) {
		return nil, output.Usagef("invalid artifact type %q (valid types: %s)", req.Type, strings.Join(cpi.ArtifactTypes, ", "))
	}
	if req.ID == "" || req.PackageID == "" || req.Dir == "" {
		return nil, output.Usagef("artifact id, package id and directory are required")
	}
	if info, err := os.Stat(req.Dir); err != nil || !info.IsDir() {
		return nil, output.Usagef("artifact directory %q does not exist", req.Dir)
	}
	if req.Name == "" {
		// as the CLI: the display name from the manifest, else the ID
		if mf, err := os.ReadFile(filepath.Join(req.Dir, "META-INF", "MANIFEST.MF")); err == nil {
			req.Name = parseManifest(mf)["Bundle-Name"]
		}
	}
	if req.Name == "" {
		req.Name = req.ID
	}
	workDir, err := os.MkdirTemp("", "cpicli-upload-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create work directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	syncer := sync.New(exe)
	syncer.Versioning = req.Versioning
	syncer.DryRun = req.DryRun
	syncer.Baseline, syncer.KeepRuntime = req.Baseline, req.KeepRuntime
	outcome, err := syncer.UploadArtifact(req.ID, req.Name, req.Type, req.PackageID, req.Dir, workDir, "", nil)
	if req.SkipDraft && sync.IsDraft(err) {
		outcome.Action = "SKIPPED"
		return &UploadResult{ID: req.ID, PackageID: req.PackageID, UploadOutcome: outcome, Skipped: SkippedDraft,
			Reason: "draft on the tenant (someone is editing it in the Web UI): save the version first; not uploaded"}, nil
	}
	// the outcome (versionRule, version) is returned with the error as well
	return &UploadResult{ID: req.ID, PackageID: req.PackageID, UploadOutcome: outcome}, err
}
