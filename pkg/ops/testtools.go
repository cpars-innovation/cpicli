package ops

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/testtools"
	"github.com/cpars-innovation/cpicli/pkg/versioning"
)

// TestToolsOptions control InstallTestTools.
type TestToolsOptions struct {
	// PackageID is the package of the tooling (default testtools.PackageID);
	// it is created when missing.
	PackageID string
	// Only installs one tool: "harness" or "stub" (default both).
	Only string
	// Deploy deploys the artifacts after the upload and waits for them.
	Deploy bool
	// DeployOptions poll the deployment (interval, checks, progress).
	DeployOptions Options
}

// TestToolResult is the outcome for one tooling artifact.
type TestToolResult struct {
	ID   string `json:"id"`
	Tool string `json:"tool"`
	// Version is the version this cpictl ships.
	Version string        `json:"version"`
	Upload  *UploadResult `json:"upload,omitempty"`
	Deploy  *Result       `json:"deploy,omitempty"`
	Error   string        `json:"error,omitempty"`
}

// TestToolsInstallResult is the outcome of InstallTestTools.
type TestToolsInstallResult struct {
	Package   *PackageResult   `json:"package"`
	Artifacts []TestToolResult `json:"artifacts"`
}

// InstallTestTools uploads the embedded test tooling (test harness and
// receiver stub) into its package, creating the package when missing, and
// deploys it with opts.Deploy. Versions come from the embedded manifests
// (versioning manifest); a running artifact keeps running until it is
// deployed again.
func InstallTestTools(ctx context.Context, exe *httpclnt.HTTPExecuter, opts TestToolsOptions) (*TestToolsInstallResult, error) {
	exe = exe.WithContext(ctx)
	arts, err := testtools.Select(opts.Only)
	if err != nil {
		return nil, output.Usage(err)
	}
	pkgID := cmp.Or(opts.PackageID, testtools.PackageID)
	pkg, err := CreatePackage(exe, PackageRequest{ID: pkgID, Name: testtools.PackageName, ShortText: testtools.PackageShortText,
		Description: "Test harness (" + testtools.HarnessID + ") and receiver stub (" + testtools.StubID + ") installed by cpictl test tools install. Development tenants only; do not transport."})
	if err != nil {
		return nil, err
	}
	res := &TestToolsInstallResult{Package: pkg}
	failed := 0
	var deploy []Artifact
	for _, a := range arts {
		r := TestToolResult{ID: a.ID, Tool: a.Tool, Version: a.Version}
		up, err := uploadTestTool(exe, a.ID, pkgID)
		r.Upload = up
		if err != nil {
			r.Error = err.Error()
			failed++
		} else {
			deploy = append(deploy, Artifact{ID: a.ID, Type: "Integration", PackageID: pkgID})
		}
		res.Artifacts = append(res.Artifacts, r)
	}
	if opts.Deploy && len(deploy) > 0 {
		dopts := opts.DeployOptions
		dopts.Versioning, dopts.AllowDowngrade = versioning.Manifest, true // the tooling of this cpictl
		for _, d := range Deploy(ctx, NewTenant(exe), deploy, dopts) {
			for i := range res.Artifacts {
				if res.Artifacts[i].ID == d.ID {
					res.Artifacts[i].Deploy = &d
					if !d.Status.Succeeded() {
						res.Artifacts[i].Error = cmp.Or(d.Error, string(d.Status))
						failed++
					}
				}
			}
		}
	}
	return res, batchError(failed, len(arts))
}

// batchError is the error of a batch: partial when some items failed,
// failed when all did.
func batchError(failed, total int) error {
	switch {
	case failed == 0:
		return nil
	case failed >= total:
		return output.Failed(fmt.Errorf("all %d item(s) failed", total))
	}
	return output.Partial(fmt.Errorf("%d of %d item(s) failed", failed, total))
}

func uploadTestTool(exe *httpclnt.HTTPExecuter, id, pkgID string) (*UploadResult, error) {
	dir, err := os.MkdirTemp("", "cpictl-testtools-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if err := testtools.WriteDir(id, dir); err != nil {
		return nil, err
	}
	return UploadArtifact(exe, UploadRequest{ID: id, Type: "Integration", PackageID: pkgID, Dir: dir, Versioning: versioning.Manifest, KeepRuntime: true})
}

// TestToolStatus is the state of one tooling artifact on the tenant.
type TestToolStatus struct {
	ID   string `json:"id"`
	Tool string `json:"tool"`
	// Version is the version this cpictl ships.
	Version string `json:"version"`
	// Installed: the designtime artifact exists (DesigntimeVersion).
	Installed         bool   `json:"installed"`
	DesigntimeVersion string `json:"designtimeVersion,omitempty"`
	Deployed          bool   `json:"deployed"`
	RuntimeVersion    string `json:"runtimeVersion,omitempty"`
	RuntimeStatus     string `json:"runtimeStatus,omitempty"`
	// UpToDate: the running version is the one this cpictl ships.
	UpToDate bool `json:"upToDate"`
	// Endpoints are the entry point URLs from ServiceEndpoints.
	Endpoints []string `json:"endpoints,omitempty"`
	// StubURL is the stub's base URL for receivers: append /<rule>/...
	StubURL string `json:"stubUrl,omitempty"`
}

// TestToolsStatus reports whether the test harness and the receiver stub
// are installed and deployed, their versions and endpoints.
func TestToolsStatus(ctx context.Context, exe *httpclnt.HTTPExecuter) ([]TestToolStatus, error) {
	exe = exe.WithContext(ctx)
	tenant := NewTenant(exe)
	out := []TestToolStatus{}
	for _, a := range testtools.Artifacts() {
		s := TestToolStatus{ID: a.ID, Tool: a.Tool, Version: a.Version}
		v, exists, err := tenant.DesigntimeVersion("Integration", a.ID)
		if err != nil {
			return nil, err
		}
		s.Installed, s.DesigntimeVersion = exists, v
		rt, err := tenant.RuntimeArtifact(a.ID)
		if err != nil {
			return nil, err
		}
		if rt != nil {
			s.Deployed, s.RuntimeVersion, s.RuntimeStatus = true, rt.Version, rt.Status
			s.UpToDate = rt.Version == a.Version && rt.Status == "STARTED"
			eps, err := cpi.NewContent(exe).ServiceEndpoints(a.ID)
			if err != nil {
				return nil, err
			}
			for _, ep := range eps {
				for _, e := range ep.EntryPoints {
					if ep.ArtifactID == a.ID {
						s.Endpoints = append(s.Endpoints, e.URL)
					}
				}
			}
			if a.Tool == testtools.ToolStub {
				for _, u := range s.Endpoints {
					if base, ok := strings.CutSuffix(u, "/*"); ok || strings.HasSuffix(u, testtools.StubPath) {
						s.StubURL = base
						break
					}
				}
			}
		}
		out = append(out, s)
	}
	return out, nil
}
