// Package snapshot writes the integration packages of a tenant into a local
// folder in the tenant layout (<package>/<artifact>/...), one exact copy per
// artifact with local edits protected, and records what it saw in a State.
// It is what `cpictl snapshot` runs; see docs/snapshot.md for the rules and
// docs/go-api.md for examples.
package snapshot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	gosync "sync"
	"sync/atomic"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/internal/str"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/rs/zerolog/log"
)

// DefaultParallel is the number of artifacts downloaded at the same time
// when Options.Parallel is not set.
const DefaultParallel = 8

// StateFile is the state file below the snapshot folder when neither
// Options.StatePath nor Options.State is given.
const StateFile = ".cpi/snapshot-state.json"

// Options control a snapshot. The zero value is a full snapshot of every
// package, drafts skipped, with the state in <dir>/.cpi/snapshot-state.json.
type Options struct {
	// Packages limits the snapshot to these package IDs; Exclude leaves
	// these out (only one of both). Local package folders outside the
	// selection are left alone.
	Packages, Exclude []string
	// DraftHandling for artifacts in draft on the tenant: SKIP (default,
	// status "draft"), ADD (written with a numeric version) or ERROR (the
	// package fails).
	DraftHandling string
	// Incremental skips the download of artifacts whose version,
	// ModifiedAt, configured parameters and local copy did not change
	// since the last snapshot.
	Incremental bool
	// DryRun only reports what the snapshot would do: no files, no state.
	DryRun bool
	// OverwriteLocal overwrites artifacts with local edits since the last
	// snapshot (default: skipped as local-modified).
	OverwriteLocal bool
	// Prune removes local folders of artifacts and packages deleted on the
	// tenant and of derived copies (never when edited locally).
	Prune bool
	// KeepOrphanParameters writes parameters.prop keys that
	// parameters.propdef does not declare (reported either way).
	KeepOrphanParameters bool
	// SkipPackageDetails does not write <package>/<package>.json.
	SkipPackageDetails bool
	// Parallel is the number of artifacts downloaded at the same time,
	// across all packages (<1: DefaultParallel).
	Parallel int
	// StatePath is the state file read before and written after the run
	// (not in a dry run, nor when every package failed). Empty: State is
	// used as is when set (nothing is read or written), else
	// <dir>/.cpi/snapshot-state.json.
	StatePath string
	// State is the state of the last snapshot, used instead of reading
	// StatePath; it is updated in place.
	State *State
	// Tenant identifies the tenant (cpi.TenantID of the host): a state of
	// another tenant is replaced by an empty one. Empty: not checked.
	Tenant string
	// WorkDir is where a temporary folder for the downloads is created
	// (empty: the system's temporary directory). The folder is removed
	// after the run unless KeepWorkDir.
	WorkDir     string
	KeepWorkDir bool
	// Derived: deployment copies that are not written (nil: none).
	Derived *DerivedIndex
	// Progress, when set, is called once per artifact as soon as its
	// outcome is known (one call at a time, from the snapshot's
	// goroutines). Derived copies are reported after their comparison
	// with the source.
	Progress func(Item)
}

// Result is the outcome of a snapshot.
type Result struct {
	Packages   int      `json:"packages"`
	Succeeded  int      `json:"succeeded"`
	Downloaded int64    `json:"artifactsDownloaded"`
	Skipped    int64    `json:"artifactsSkipped"`
	Seconds    float64  `json:"seconds"`
	Failed     []string `json:"failed,omitempty"`
	DryRun     bool     `json:"dryRun,omitempty"`
	// Counts per status, Items per artifact (sorted by package and
	// artifact), Warnings: derived copies edited on the tenant and similar.
	Counts   map[string]int `json:"counts"`
	Warnings int            `json:"warnings"`
	Items    []Item         `json:"artifacts"`
	// Paths are the files and folders the snapshot wrote or removed,
	// relative to the snapshot folder (slash-separated, sorted): artifact
	// and package folders, package details, the state file when it is
	// inside the folder. Commit with CommitOptions.Paths to stage only them.
	Paths []string `json:"paths,omitempty"`
	// State is the state after the run.
	State *State `json:"-"`
}

// run is one snapshot.
type run struct {
	ctx        context.Context
	exe        *httpclnt.HTTPExecuter
	o          *Options
	state      *State
	slots      chan struct{} // download slots shared by all packages
	downloaded atomic.Int64
	skipped    atomic.Int64

	mu         gosync.Mutex
	items      []Item
	deferred   []derivedCheck
	paths      map[string]bool
	progressMu gosync.Mutex
}

// Take snapshots the tenant into dir (one folder per package). A failing
// package does not stop the others: the result is returned with an error
// that lists the failures (exit code 7 when some packages succeeded).
// Use an executer from cpi.InitHTTPExecuter, which retries throttled
// reads. Cancelling ctx stops starting further packages and artifacts.
func Take(ctx context.Context, exe *httpclnt.HTTPExecuter, dir string, o Options) (*Result, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, output.Usagef("snapshot: no directory given")
	}
	switch o.DraftHandling {
	case "":
		o.DraftHandling = "SKIP"
	case "SKIP", "ADD", "ERROR":
	default:
		return nil, output.Usagef("invalid draft handling %q (SKIP, ADD or ERROR)", o.DraftHandling)
	}
	if len(o.Packages) > 0 && len(o.Exclude) > 0 {
		return nil, output.Usagef("snapshot: packages to include and to exclude are mutually exclusive")
	}
	if o.Parallel < 1 {
		o.Parallel = DefaultParallel
	}
	statePath := o.StatePath
	state := o.State
	if state == nil {
		if statePath == "" {
			statePath = filepath.Join(dir, filepath.FromSlash(StateFile))
		}
		var err error
		if state, err = LoadState(statePath); err != nil {
			return nil, output.Usagef("cannot read the snapshot state %s: %v (delete it for a full snapshot)", statePath, err)
		}
	}
	if o.Tenant != "" && state.Tenant != o.Tenant {
		if state.Tenant != "" && len(state.Artifacts) > 0 {
			log.Warn().Msgf("Snapshot state %s belongs to tenant %s, not %s: starting a new state", statePath, state.Tenant, o.Tenant)
			state.mu.Lock()
			state.Artifacts, state.byID = map[string]ArtifactState{}, nil
			state.mu.Unlock()
		}
		state.Tenant = o.Tenant
	}
	if o.DryRun {
		log.Info().Msg("DRY RUN: no files and no state are written")
	}

	workDir, err := newWorkDir(o.WorkDir)
	if err != nil {
		return nil, err
	}
	if o.KeepWorkDir {
		log.Info().Msgf("Work directory %s is kept", workDir)
	} else {
		defer os.RemoveAll(workDir)
	}

	r := &run{ctx: ctx, exe: exe, o: &o, state: state, slots: make(chan struct{}, o.Parallel), paths: map[string]bool{}}
	res, err := r.packages(dir, workDir)
	if res == nil {
		return nil, err
	}
	res.State = state
	if !o.DryRun && statePath != "" && (err == nil || res.Succeeded > 0) {
		// what succeeded is kept, also when some packages failed
		if serr := state.Save(statePath); serr != nil {
			return res, serr
		}
		r.addPath(dir, statePath)
	}
	for p := range r.paths {
		res.Paths = append(res.Paths, p)
	}
	sort.Strings(res.Paths)
	return res, err
}

// newWorkDir creates the run's own folder in base (or the system's
// temporary directory), so that removing it never touches other files.
func newWorkDir(base string) (string, error) {
	if base != "" {
		if err := os.MkdirAll(base, 0o755); err != nil {
			return "", fmt.Errorf("work directory %s: %w", base, err)
		}
	}
	dir, err := os.MkdirTemp(base, "cpictl-snapshot-")
	if err != nil {
		return "", fmt.Errorf("work directory: %w", err)
	}
	return dir, nil
}

// addPath records a written path below dir (others are ignored).
func (r *run) addPath(dir, path string) {
	absDir, err1 := filepath.Abs(dir)
	abs, err2 := filepath.Abs(path)
	if err1 != nil || err2 != nil {
		return
	}
	rel, err := filepath.Rel(absDir, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return
	}
	r.mu.Lock()
	r.paths[filepath.ToSlash(rel)] = true
	r.mu.Unlock()
}

func (r *run) packages(artifactsBaseDir, workDir string) (*Result, error) {
	o := r.o
	log.Info().Msg("---------------------------------------------------------------------------------")
	log.Info().Msg("📢 Begin taking a snapshot of the tenant")

	// Get packages from the tenant - details of all packages are returned in this single call,
	// so no additional call per package is needed
	packages, err := cpi.NewIntegrationPackage(r.exe).GetPackagesData()
	if err != nil {
		return nil, err
	}
	if len(packages) == 0 {
		return nil, fmt.Errorf("No packages found in the tenant")
	}

	mode := "full"
	if o.Incremental {
		mode = "incremental"
	}
	log.Info().Msgf("Processing %d packages (%s, %d in parallel)", len(packages), mode, o.Parallel)
	tenantPackages := map[string]bool{}
	started := time.Now()

	res := &Result{}
	var mu gosync.Mutex
	sem := make(chan struct{}, o.Parallel)
	var wg gosync.WaitGroup
	filtered := 0
	for i, packageDataFromTenant := range packages {
		id := packageDataFromTenant.Root.Id
		tenantPackages[id] = true
		// Filter in/out packages before any call to the tenant
		if str.FilterIDs(id, o.Packages, o.Exclude) {
			filtered++
			continue
		}
		if packageDataFromTenant.Root.Mode == "READ_ONLY" {
			log.Warn().Msgf("Skipping package %v as it is Configure-only and cannot be downloaded", id)
			continue
		}
		res.Packages++
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			err := r.ctx.Err()
			if err == nil {
				log.Info().Str("package", id).Msgf("Processing package %d/%d - ID: %v", i+1, len(packages), id)
				err = r.snapshotPackage(packageDataFromTenant, id, filepath.Join(workDir, id), filepath.Join(artifactsBaseDir, id))
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				log.Error().Str("package", id).Msgf("❌ Package %v failed: %v", id, err)
				res.Failed = append(res.Failed, fmt.Sprintf("%s: %v", id, err))
				return
			}
			res.Succeeded++
		}()
	}
	wg.Wait()
	if filtered > 0 {
		which := "not in --ids-include"
		if len(o.Packages) == 0 {
			which = "in --ids-exclude"
		}
		log.Info().Msgf("%d package(s) %s", filtered, which)
	}
	// deployment copies are compared with their sources once those are written
	if err := r.checkDerivedCopies(); err != nil {
		res.Failed = append(res.Failed, err.Error())
	}
	// packages that exist only locally
	if err := r.localPackages(artifactsBaseDir, tenantPackages); err != nil {
		res.Failed = append(res.Failed, err.Error())
	}
	res.DryRun = o.DryRun
	res.Items = r.sortedItems()
	res.Counts = map[string]int{}
	for _, it := range res.Items {
		res.Counts[it.Status]++
		if it.Warning != "" {
			res.Warnings++
		}
	}
	res.Skipped, res.Downloaded = r.skipped.Load(), r.downloaded.Load()
	res.Seconds = time.Since(started).Round(100 * time.Millisecond).Seconds()
	sort.Strings(res.Failed)

	log.Info().Msg("---------------------------------------------------------------------------------")
	log.Info().Msgf("🏆 Snapshot%s: %d package(s) done, %d failed; %d artifact(s) downloaded, %d unchanged and skipped; %.1fs (%d in parallel)",
		map[bool]string{true: " (dry run)", false: ""}[o.DryRun], res.Succeeded, len(res.Failed), res.Downloaded, res.Skipped, res.Seconds, o.Parallel)
	log.Info().Msgf("Artifacts: %s; %d warning(s)", FormatCounts(res.Counts), res.Warnings)
	if len(res.Failed) > 0 {
		err := fmt.Errorf("%d of %d package(s) failed: %s", len(res.Failed), res.Packages, strings.Join(res.Failed, "; "))
		if cerr := r.ctx.Err(); cerr != nil {
			err = fmt.Errorf("snapshot stopped (%w): %v", cerr, err)
		}
		if res.Succeeded > 0 {
			return res, output.Partial(err)
		}
		return res, err
	}
	return res, nil
}

// FormatCounts is the summary line of the counts per status ("2 new, 1
// changed"; "none").
func FormatCounts(counts map[string]int) string {
	var parts []string
	for _, k := range Statuses {
		if counts[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// snapshotPackage writes the package details and the artifacts of one
// package.
func (r *run) snapshotPackage(details *cpi.PackageSingleData, packageId, workDir, artifactsDir string) error {
	derivedPackage := false
	if r.o.Derived != nil {
		_, derivedPackage = r.o.Derived.Packages[packageId]
	}
	if !r.o.SkipPackageDetails && !r.o.DryRun && !derivedPackage {
		written, err := WritePackageDetails(artifactsDir, packageId, details)
		if err != nil {
			return err
		}
		if written {
			r.addPath(filepath.Dir(artifactsDir), filepath.Join(artifactsDir, packageId+".json"))
		}
	}

	log.Info().Msgf("Getting artifacts in integration package %v", packageId)
	artifacts, err := cpi.NewIntegrationPackage(r.exe).GetAllArtifacts(packageId)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(workDir, "download"), os.ModePerm); err != nil {
		return err
	}

	// the artifacts of all packages share the download slots, so one large
	// package does not run alone at the end
	var wg gosync.WaitGroup
	var mu gosync.Mutex
	var failed []string
	for _, artifact := range artifacts {
		r.slots <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-r.slots }()
			err := r.ctx.Err()
			if err == nil {
				err = r.snapshotArtifact(packageId, workDir, artifactsDir, artifact)
			}
			if err != nil {
				mu.Lock()
				failed = append(failed, fmt.Sprintf("%s: %v", artifact.Id, err))
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(failed) > 0 {
		sort.Strings(failed)
		return fmt.Errorf("%d artifact(s) failed: %s", len(failed), strings.Join(failed, "; "))
	}
	tenantIDs := make(map[string]bool, len(artifacts))
	for _, a := range artifacts {
		tenantIDs[a.Id] = true
	}
	if err := r.leftovers(packageId, artifactsDir, tenantIDs); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(workDir, "download")); err != nil {
		return err
	}
	log.Info().Msg("---------------------------------------------------------------------------------")
	log.Info().Msgf("🏆 Completed processing of artifacts in integration package %v", packageId)
	return nil
}

// localPackages reports (and with Prune removes) the artifacts of package
// folders whose package is no longer on the tenant.
func (r *run) localPackages(base string, tenantPackages map[string]bool) error {
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		id := e.Name()
		if !e.IsDir() || strings.HasPrefix(id, ".") || tenantPackages[id] || str.FilterIDs(id, r.o.Packages, r.o.Exclude) {
			continue
		}
		dir := filepath.Join(base, id)
		if err := r.leftovers(id, dir, nil); err != nil {
			return err
		}
		if r.o.Prune && !r.o.DryRun {
			// the package folder goes when only its details file is left
			rest, _ := os.ReadDir(dir)
			if len(rest) == 0 || (len(rest) == 1 && rest[0].Name() == id+".json") {
				if err := os.RemoveAll(dir); err != nil {
					return err
				}
				r.addPath(base, dir)
				log.Info().Msgf("Package folder %s removed (--prune): the package is gone from the tenant", id)
			}
		}
	}
	return nil
}
