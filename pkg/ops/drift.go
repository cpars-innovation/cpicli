package ops

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/cpars-innovation/cpicli/internal/file"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// Drift states.
const (
	DriftInSync      = "in_sync"
	DriftTenantNewer = "tenant_newer"
	DriftLocalNewer  = "local_newer"
	DriftDiverged    = "diverged"
	DriftNotOnTenant = "not_on_tenant"
	DriftError       = "error"
)

// DriftItem compares one local artifact with the tenant.
type DriftItem struct {
	ArtifactID        string `json:"artifactId"`
	Type              string `json:"type"`
	PackageID         string `json:"packageId,omitempty"`
	Path              string `json:"path"`
	LocalVersion      string `json:"localVersion,omitempty"`
	DesigntimeVersion string `json:"designtimeVersion,omitempty"`
	RuntimeVersion    string `json:"runtimeVersion,omitempty"`
	// State: in_sync (same content), tenant_newer / local_newer (content
	// differs and that side has the higher version), diverged (content
	// differs with the same or uncomparable versions), not_on_tenant, error.
	State string `json:"state"`
	// RuntimeOutdated is true when the deployed version differs from the
	// designtime version.
	RuntimeOutdated bool   `json:"runtimeOutdated,omitempty"`
	Error           string `json:"error,omitempty"`
	err             error
}

// DriftResult is the outcome of Drift.
type DriftResult struct {
	Items   []DriftItem    `json:"items"`
	Summary map[string]int `json:"summary"`
}

// Drift compares the artifacts of a local content tree (optionally only one
// package folder) with their designtime and runtime state: content (as
// upload would compare it) and versions. Run it before upload_artifact so
// that edits made on the tenant are not overwritten. The tenant is only read;
// parallel artifacts are compared at the same time (0: 8).
func Drift(ctx context.Context, exe *httpclnt.HTTPExecuter, dir, packageID string, parallel int) (*DriftResult, error) {
	exe = exe.WithContext(ctx)
	if parallel <= 0 {
		parallel = 8
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, output.Usagef("%s is not a directory", dir)
	}
	var artifacts []LocalArtifact
	if err := WalkLocalArtifacts(ctx, dir, func(a LocalArtifact) {
		if packageID == "" || a.PackageID == packageID {
			artifacts = append(artifacts, a)
		}
	}, func(error) {}); err != nil {
		return nil, err
	}
	if len(artifacts) == 0 {
		return nil, output.Usagef("no artifacts (directories with META-INF/MANIFEST.MF) found in %s", dir)
	}
	rt := cpi.NewRuntime(exe)
	items := make([]DriftItem, len(artifacts))
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for i, a := range artifacts {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			items[i] = driftOne(exe, rt, a)
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	res := &DriftResult{Items: items, Summary: map[string]int{}}
	var failed int
	for _, it := range items {
		res.Summary[it.State]++
		if httpclnt.IsAuthError(it.err) {
			return nil, it.err
		}
		if it.err != nil {
			failed++
		}
	}
	if failed > 0 {
		return res, output.Partial(fmt.Errorf("%d of %d artifact(s) could not be compared", failed, len(items)))
	}
	return res, nil
}

func driftOne(exe *httpclnt.HTTPExecuter, rt *cpi.Runtime, a LocalArtifact) DriftItem {
	it := DriftItem{ArtifactID: a.ID, Type: a.Type, PackageID: a.PackageID, Path: a.Rel}
	fail := func(err error) DriftItem {
		it.State, it.Error, it.err = DriftError, err.Error(), err
		return it
	}
	if mf, err := os.ReadFile(a.Dir + "/META-INF/MANIFEST.MF"); err == nil {
		it.LocalVersion = parseManifest(mf)["Bundle-Version"]
	}
	localHash, err := file.ContentHash(os.DirFS(a.Dir))
	if err != nil {
		return fail(err)
	}
	dt := cpi.NewDesigntimeArtifact(a.Type, exe)
	if dt == nil {
		return fail(fmt.Errorf("unsupported artifact type %q", a.Type))
	}
	version, _, exists, err := dt.Get(a.ID, "active")
	if err != nil {
		return fail(err)
	}
	if !exists {
		it.State = DriftNotOnTenant
		return it
	}
	it.DesigntimeVersion = version
	if r, err := rt.GetArtifact(a.ID); err == nil && r != nil {
		it.RuntimeVersion = r.Version
		it.RuntimeOutdated = r.Version != version
	}
	zipped, err := cpi.DownloadArtifact(exe, a.Type, a.ID, "active")
	if err != nil {
		return fail(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	if err != nil {
		return fail(err)
	}
	root, err := archiveRoot(zr)
	if err != nil {
		return fail(err)
	}
	tenantHash, err := file.ContentHash(root)
	if err != nil {
		return fail(err)
	}
	switch c := CompareVersions(it.LocalVersion, version); {
	case tenantHash == localHash:
		it.State = DriftInSync
	case it.LocalVersion == "" || c == 0:
		it.State = DriftDiverged
	case c < 0:
		it.State = DriftTenantNewer
	default:
		it.State = DriftLocalNewer
	}
	return it
}
