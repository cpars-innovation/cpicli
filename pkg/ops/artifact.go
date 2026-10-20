package ops

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/internal/sync"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/snapshot"
	"github.com/cpars-innovation/cpicli/pkg/versioning"
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
	if err != nil {
		err = explainUploadError(exe, req.ID, err)
	}
	// the outcome (versionRule, version) is returned with the error as well
	return &UploadResult{ID: req.ID, PackageID: req.PackageID, UploadOutcome: outcome}, err
}

// explainUploadError adds what the tenant said to a refused upload and, when
// the artifact is open in the Web UI, who holds the edit lock: the tenant
// answers an update of a locked artifact with a bare 400.
func explainUploadError(exe *httpclnt.HTTPExecuter, artifactID string, err error) error {
	code := httpclnt.StatusCode(err)
	if code < 400 || httpclnt.IsAuthError(err) {
		return err
	}
	if code >= 500 {
		// a refused upload is often a 500 with the reason in the body
		// (invalid header field (line N), ...)
		return WithTenantText(err)
	}
	if httpclnt.IsGatewayRefusal(err) {
		return err
	}
	var notes []string
	var he *httpclnt.HTTPError
	if errors.As(err, &he) {
		if msg := tenantErrorText(he.Body); msg != "" {
			notes = append(notes, "the tenant says: "+msg)
		}
	}
	if locks, lerr := ListLocks(exe, artifactID, ""); lerr == nil {
		for _, l := range locks {
			since := ""
			if l.CreatedAt != nil {
				since = " since " + l.CreatedAt.UTC().Format(time.RFC3339)
			}
			notes = append(notes, fmt.Sprintf("%s is locked by %s%s (open in the Web UI): close the editor there, or remove the lock with cpictl locks unlock --artifact-id %s --confirm (unsaved changes are lost)",
				artifactID, cmp.Or(l.CreatedBy, "someone"), since, artifactID))
		}
	}
	if len(notes) == 0 {
		return err
	}
	return fmt.Errorf("%w; %s", err, strings.Join(notes, "; "))
}

// ExplainUploadError is explainUploadError for callers outside ops (the
// update artifact command).
func ExplainUploadError(exe *httpclnt.HTTPExecuter, artifactID string, err error) error {
	return explainUploadError(exe, artifactID, err)
}

// WithTenantText adds the tenant's error message to an HTTP error
// ("... response code = 500; the tenant says: Failed to deploy Value
// Mapping id: X"); other errors are returned as they are.
func WithTenantText(err error) error {
	var he *httpclnt.HTTPError
	if !errors.As(err, &he) {
		return err
	}
	if _, refused := he.GatewayRefusal(); refused {
		return err // the gateway's reason is in the message already: the tenant said nothing
	}
	msg := tenantErrorText(he.Body)
	if msg == "" {
		return err
	}
	return fmt.Errorf("%w; the tenant says: %s", err, msg)
}

// tenantErrorText is the message of an OData error body ({"error":{"message":
// {"value":...}}} or {"error":{"message":"..."}}), else the body's text,
// shortened.
func tenantErrorText(body []byte) string {
	var odata struct {
		Error struct {
			Message json.RawMessage `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &odata) == nil && len(odata.Error.Message) > 0 {
		var s string
		var v struct {
			Value string `json:"value"`
		}
		if json.Unmarshal(odata.Error.Message, &s) == nil && s != "" {
			return shorten(s)
		}
		if json.Unmarshal(odata.Error.Message, &v) == nil && v.Value != "" {
			return shorten(v.Value)
		}
	}
	text := strings.TrimSpace(string(body))
	if text == "" || strings.HasPrefix(text, "<") {
		return ""
	}
	return shorten(text)
}

func shorten(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
