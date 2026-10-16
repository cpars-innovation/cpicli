package ops

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cpars-innovation/cpicli/internal/file"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/iflow"
)

// Compare statuses of an artifact.
const (
	CompareSame             = "same"
	CompareOnlyA            = "only_a"
	CompareOnlyB            = "only_b"
	CompareContentDiffers   = "content_differs"
	CompareVersionDiffers   = "version_differs"
	CompareParametersDiffer = "parameters_differ"
)

// CompareSide is one side of a comparison: a local content tree
// (<package>/<artifact>, as snapshot writes it) or a tenant.
type CompareSide struct {
	// Label names the side in the result ("TEST", "git:main", a path).
	Label string
	// Dir is a local content tree; empty for a tenant.
	Dir string
	// Exe is the tenant (when Dir is empty): its artifacts are downloaded
	// and normalized as snapshot writes them.
	Exe *httpclnt.HTTPExecuter
}

// CompareOptions select and shape the comparison.
type CompareOptions struct {
	// Packages and Artifacts are patterns (path.Match); empty: all.
	Packages, Artifacts []string
	// Diff adds unified diffs of changed text files.
	Diff bool
	// Values adds parameter values (default: keys only).
	Values bool
	// Model adds a semantic diff (iflow.DiffModels: per element and per
	// property) of each changed .iflw model, in addition to Diff.
	Model bool
	// Parallel downloads per tenant (0: 8).
	Parallel int
	// WorkDir holds the downloaded tenant trees (default: a temporary
	// directory that is removed).
	WorkDir string
}

// CompareSideInfo is what a tenant side reports about an artifact.
type CompareSideInfo struct {
	Designtime    string     `json:"designtime,omitempty"`
	Draft         bool       `json:"draft,omitempty"`
	ModifiedAt    *time.Time `json:"modifiedAt,omitempty"`
	ModifiedBy    string     `json:"modifiedBy,omitempty"`
	Running       string     `json:"running,omitempty"`
	RuntimeStatus string     `json:"runtimeStatus,omitempty"`
}

// FileChange is one differing file of an artifact.
type FileChange struct {
	Path string `json:"path"`
	// Change: added (only in B), removed (only in A), changed.
	Change string `json:"change"`
	Diff   string `json:"diff,omitempty"`
	// Binary or too large for a diff.
	NoDiff string `json:"noDiff,omitempty"`
	// Model is the semantic diff of a changed .iflw model (Model option).
	Model *iflow.ModelDiff `json:"model,omitempty"`
	// ModelError is why a model could not be compared (not a BPMN model).
	ModelError string `json:"modelError,omitempty"`
}

// ParameterChange is one differing parameters.prop key.
type ParameterChange struct {
	Key string `json:"key"`
	// Change: differs, only_a, only_b.
	Change string `json:"change"`
	A      string `json:"a,omitempty"`
	B      string `json:"b,omitempty"`
}

// CompareItem is the comparison of one artifact.
type CompareItem struct {
	Package  string `json:"package"`
	Artifact string `json:"artifact"`
	Type     string `json:"type,omitempty"`
	// Status: same, only_a, only_b, content_differs, version_differs
	// (same content), parameters_differ (same content and version).
	Status string `json:"status"`
	// Differences lists every aspect that differs (content, version,
	// parameters), also those Status does not name; empty when same.
	Differences []string          `json:"differences,omitempty"`
	VersionA    string            `json:"versionA,omitempty"`
	VersionB    string            `json:"versionB,omitempty"`
	A           *CompareSideInfo  `json:"a,omitempty"`
	B           *CompareSideInfo  `json:"b,omitempty"`
	Files       []FileChange      `json:"files,omitempty"`
	Parameters  []ParameterChange `json:"parameters,omitempty"`
}

// CompareResult is the outcome of Compare.
type CompareResult struct {
	A       string         `json:"a"`
	B       string         `json:"b"`
	Items   []CompareItem  `json:"items"`
	Summary map[string]int `json:"summary"`
	// Errors of artifacts that could not be read (tenant sides).
	Errors []string `json:"errors,omitempty"`
}

// compareTree is a side made available as a local tree.
type compareTree struct {
	dir  string
	info map[string]*CompareSideInfo // package/artifact -> tenant info
	errs []string
}

// Compare compares two sides per artifact: content as an upload compares it
// (without Bundle-Version and parameters.prop), version, parameters, and
// per file. Tenants are only read.
func Compare(ctx context.Context, a, b CompareSide, o CompareOptions) (*CompareResult, error) {
	work := o.WorkDir
	if work == "" {
		tmp, err := os.MkdirTemp("", "cpictl-compare-*")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tmp)
		work = tmp
	}
	trees := make([]*compareTree, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, side := range []CompareSide{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			trees[i], errs[i] = side.tree(ctx, filepath.Join(work, fmt.Sprintf("side%d", i)), o)
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	res := &CompareResult{A: a.Label, B: b.Label, Items: []CompareItem{}, Summary: map[string]int{}}
	res.Errors = append(trees[0].errs, trees[1].errs...)

	type entry struct {
		la  LocalArtifact
		key string
	}
	index := func(t *compareTree) (map[string]entry, error) {
		out := map[string]entry{}
		err := WalkLocalArtifacts(ctx, t.dir, func(la LocalArtifact) {
			if la.PackageID == "" || !MatchAny(o.Packages, la.PackageID) || !MatchAny(o.Artifacts, la.ID) {
				return
			}
			// keyed by artifact ID: the same artifact in another package
			// folder is still the same artifact
			out[la.ID] = entry{la, la.PackageID + "/" + la.ID}
		}, func(error) {})
		return out, err
	}
	ia, err := index(trees[0])
	if err != nil {
		return nil, err
	}
	ib, err := index(trees[1])
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for id := range ia {
		ids[id] = true
	}
	for id := range ib {
		ids[id] = true
	}
	for _, id := range sortedKeys(ids) {
		ea, okA := ia[id]
		eb, okB := ib[id]
		it := CompareItem{Artifact: id}
		if okA {
			it.Package, it.Type, it.A = ea.la.PackageID, ea.la.Type, trees[0].info[ea.key]
			it.VersionA = sideVersion(ea.la.Dir, it.A)
		}
		if okB {
			it.Package, it.Type, it.B = cmpOr(it.Package, eb.la.PackageID), cmpOr(it.Type, eb.la.Type), trees[1].info[eb.key]
			it.VersionB = sideVersion(eb.la.Dir, it.B)
		}
		switch {
		case !okB:
			it.Status = CompareOnlyA
		case !okA:
			it.Status = CompareOnlyB
		default:
			if err := compareArtifact(ea.la, eb.la, a.Label, b.Label, o, &it); err != nil {
				return nil, fmt.Errorf("%s: %w", id, err)
			}
		}
		res.Summary[it.Status]++
		res.Items = append(res.Items, it)
	}
	sort.SliceStable(res.Items, func(i, j int) bool {
		if res.Items[i].Package != res.Items[j].Package {
			return res.Items[i].Package < res.Items[j].Package
		}
		return res.Items[i].Artifact < res.Items[j].Artifact
	})
	if len(res.Errors) > 0 {
		return res, output.Partial(fmt.Errorf("%d artifact(s) could not be read", len(res.Errors)))
	}
	return res, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// sideVersion is the tenant's designtime version (the exported manifest can
// lag behind it), else the Bundle-Version of the folder.
func sideVersion(dir string, info *CompareSideInfo) string {
	if info != nil && info.Designtime != "" && !IsDraftVersion(info.Designtime) {
		return info.Designtime
	}
	v, _ := manifestVersionOf(dir)
	return v
}

func manifestVersionOf(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "META-INF", "MANIFEST.MF"))
	if err != nil {
		return "", err
	}
	return parseManifest(data)["Bundle-Version"], nil
}

func compareArtifact(a, b LocalArtifact, labelA, labelB string, o CompareOptions, it *CompareItem) error {
	ha, err := file.UploadHash(os.DirFS(a.Dir), a.Type)
	if err != nil {
		return err
	}
	hb, err := file.UploadHash(os.DirFS(b.Dir), b.Type)
	if err != nil {
		return err
	}
	pa, _ := os.ReadFile(filepath.Join(a.Dir, filepath.FromSlash(file.ParametersFile)))
	pb, _ := os.ReadFile(filepath.Join(b.Dir, filepath.FromSlash(file.ParametersFile)))
	it.Parameters = parameterChanges(PropertyValues(pa), PropertyValues(pb), o.Values)
	if ha != hb {
		it.Differences = append(it.Differences, "content")
	}
	if CompareVersions(it.VersionA, it.VersionB) != 0 {
		it.Differences = append(it.Differences, "version")
	}
	if len(it.Parameters) > 0 {
		it.Differences = append(it.Differences, "parameters")
	}
	switch {
	case ha != hb:
		it.Status = CompareContentDiffers
	case CompareVersions(it.VersionA, it.VersionB) != 0:
		it.Status = CompareVersionDiffers
	case len(it.Parameters) > 0:
		it.Status = CompareParametersDiffer
	default:
		it.Status = CompareSame
		return nil
	}
	files, err := file.TreeDiff(a.Dir, b.Dir, func(rel string) bool { return rel == file.ParametersFile })
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, rel := range files {
		da, errA := os.ReadFile(filepath.Join(a.Dir, filepath.FromSlash(rel)))
		db, errB := os.ReadFile(filepath.Join(b.Dir, filepath.FromSlash(rel)))
		fc := FileChange{Path: rel, Change: "changed"}
		switch {
		case errA != nil:
			fc.Change = "added"
		case errB != nil:
			fc.Change = "removed"
		}
		if o.Diff {
			if !file.IsTextFile(rel) {
				fc.NoDiff = "binary"
			} else if d, ok := file.UnifiedDiff(string(da), string(db), labelA+"/"+rel, labelB+"/"+rel, 3); ok {
				fc.Diff = d
			} else {
				fc.NoDiff = "too large"
			}
		}
		if o.Model && fc.Change == "changed" && strings.HasSuffix(rel, ".iflw") {
			if md, err := iflow.DiffModels(da, db); err != nil {
				fc.ModelError = err.Error()
			} else {
				fc.Model = md
			}
		}
		it.Files = append(it.Files, fc)
	}
	return nil
}

func parameterChanges(a, b map[string]string, values bool) []ParameterChange {
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	var out []ParameterChange
	for _, k := range sortedKeys(keys) {
		va, okA := a[k]
		vb, okB := b[k]
		c := ParameterChange{Key: k}
		switch {
		case !okB:
			c.Change = "only_a"
		case !okA:
			c.Change = "only_b"
		case va != vb:
			c.Change = "differs"
		default:
			continue
		}
		if values {
			c.A, c.B = va, vb
		}
		out = append(out, c)
	}
	return out
}

// tree makes a side available as a local tree: a directory as it is, a
// tenant downloaded into dir.
func (s CompareSide) tree(ctx context.Context, dir string, o CompareOptions) (*compareTree, error) {
	if s.Dir != "" {
		if info, err := os.Stat(s.Dir); err != nil || !info.IsDir() {
			return nil, output.Usagef("%s is not a directory", s.Dir)
		}
		return &compareTree{dir: s.Dir, info: map[string]*CompareSideInfo{}}, nil
	}
	if s.Exe == nil {
		return nil, output.Usagef("side %s: neither a directory nor a tenant", s.Label)
	}
	return downloadTenantTree(ctx, s.Exe, dir, o)
}

// downloadTenantTree downloads the selected artifacts of a tenant into
// dir/<package>/<artifact>, normalized as snapshot writes them, with
// versions, draft flags and runtime states.
func downloadTenantTree(ctx context.Context, exe *httpclnt.HTTPExecuter, dir string, o CompareOptions) (*compareTree, error) {
	parallel := o.Parallel
	if parallel <= 0 {
		parallel = 8
	}
	t := &compareTree{dir: dir, info: map[string]*CompareSideInfo{}}
	ip := cpi.NewIntegrationPackage(exe)
	packages, err := ip.GetPackagesList()
	if err != nil {
		return nil, err
	}
	type job struct {
		pkg string
		a   *cpi.ArtifactDetails
	}
	var jobs []job
	for _, p := range packages {
		if !MatchAny(o.Packages, p) {
			continue
		}
		arts, err := ip.GetAllArtifacts(p)
		if err != nil {
			return nil, err
		}
		for _, a := range arts {
			if MatchAny(o.Artifacts, a.Id) {
				jobs = append(jobs, job{p, a})
			}
		}
	}
	rt := cpi.NewRuntime(exe)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, parallel)
	for _, j := range jobs {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			info := &CompareSideInfo{Designtime: j.a.Version, Draft: j.a.IsDraft, ModifiedBy: j.a.ModifiedBy}
			if !j.a.ModifiedAt.IsZero() {
				m := j.a.ModifiedAt.UTC()
				info.ModifiedAt = &m
			}
			err := func() error {
				if r, err := rt.GetArtifact(j.a.Id); err == nil && r != nil {
					info.Running, info.RuntimeStatus = r.Version, r.Status
				}
				data, err := cpi.DownloadArtifact(exe, j.a.ArtifactType, j.a.Id, "active")
				if err != nil {
					return err
				}
				zip := filepath.Join(dir, ".zip", j.pkg, j.a.Id+".zip")
				if err := os.MkdirAll(filepath.Dir(zip), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(zip, data, 0o644); err != nil {
					return err
				}
				target := filepath.Join(dir, j.pkg, j.a.Id)
				if err := file.UnzipSource(zip, target); err != nil {
					return err
				}
				return file.NormalizeTree(target)
			}()
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				t.errs = append(t.errs, fmt.Sprintf("%s/%s: %v", j.pkg, j.a.Id, err))
				return
			}
			t.info[j.pkg+"/"+j.a.Id] = info
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_ = os.RemoveAll(filepath.Join(dir, ".zip"))
	sort.Strings(t.errs)
	return t, nil
}

// GitTree extracts ref (and only subPath, if given) of the Git repository
// at repo into dest, as `git archive` produces it.
func GitTree(ctx context.Context, repo, ref, subPath, dest string) error {
	if err := CheckGitRef(ref); err != nil {
		return err
	}
	args := []string{"-C", repo, "archive", "--format=tar", ref}
	if subPath != "" {
		args = append(args, "--", subPath)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	tr := tar.NewReader(out)
	var extractErr error
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			extractErr = err
			break
		}
		name := path.Clean(h.Name)
		if strings.HasPrefix(name, "../") || path.IsAbs(name) {
			extractErr = fmt.Errorf("unsafe path %q in the archive", h.Name)
			break
		}
		if subPath != "" {
			rel := strings.TrimPrefix(strings.TrimPrefix(name, path.Clean(subPath)), "/")
			if rel == name && name != path.Clean(subPath) {
				continue
			}
			name = rel
		}
		if name == "" || name == "." {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			extractErr = os.MkdirAll(target, 0o755)
		case tar.TypeReg:
			if extractErr = os.MkdirAll(filepath.Dir(target), 0o755); extractErr == nil {
				var f *os.File
				if f, extractErr = os.Create(target); extractErr == nil {
					_, extractErr = io.Copy(f, tr)
					f.Close()
				}
			}
		}
		if extractErr != nil {
			break
		}
	}
	_, _ = io.Copy(io.Discard, out)
	if err := cmd.Wait(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return output.Usagef("git archive %s: %s", ref, msg)
	}
	return extractErr
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
