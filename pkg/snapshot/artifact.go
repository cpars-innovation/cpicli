package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	gosync "sync"
	"time"

	"github.com/cpars-innovation/cpicli/internal/file"
	"github.com/cpars-innovation/cpicli/pkg/content"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/manifest"
	"github.com/cpars-innovation/cpicli/pkg/versioning"
	"github.com/rs/zerolog/log"
)

// Snapshot statuses of an artifact.
const (
	StatusNew           = "new"            // on the tenant, not local
	StatusChanged       = "changed"        // tenant version, ModifiedAt or content differs from local
	StatusUnchanged     = "unchanged"      // nothing to do
	StatusDeleted       = "deleted"        // local (from an earlier snapshot), no longer on the tenant
	StatusLocalModified = "local-modified" // local edits since the last snapshot: not overwritten
	StatusLocalOnly     = "local-only"     // local, not on the tenant, never snapshotted (a new artifact)
	StatusDerived       = "derived"        // a deployment copy of another artifact: not written
	StatusDraft         = "draft"          // in draft on the tenant and skipped (DraftHandling SKIP)
)

// Statuses lists the statuses in the order of the summary line.
var Statuses = []string{StatusNew, StatusChanged, StatusUnchanged, StatusDeleted, StatusLocalModified, StatusLocalOnly, StatusDerived, StatusDraft}

// Snapshot actions (what was done, or would be done in a dry run).
const (
	ActionWrite  = "write"
	ActionDelete = "delete"
	ActionSkip   = "skip"
	ActionNone   = "none"
)

// Item is the outcome for one artifact.
type Item struct {
	Package  string `json:"package"`
	Artifact string `json:"artifact"`
	Status   string `json:"status"`
	Action   string `json:"action"`
	// Source of a derived copy: "<package>/<artifactDir>".
	Source  string `json:"source,omitempty"`
	Note    string `json:"note,omitempty"`
	Warning string `json:"warning,omitempty"`
	// Draft is set for an artifact in draft on the tenant: status "draft"
	// when it was skipped, "new (draft)" or "changed (draft)" when it was
	// written (DraftHandling ADD).
	Draft bool `json:"draft,omitempty"`
	// OrphanParameters are parameters.prop keys the tenant keeps that
	// parameters.propdef no longer declares (not written unless
	// KeepOrphanParameters).
	OrphanParameters []string `json:"orphanParameters,omitempty"`
}

// Label is the status with the draft marker.
func (it Item) Label() string {
	if it.Draft && it.Status != StatusDraft {
		return it.Status + " (draft)"
	}
	return it.Status
}

// DerivedSource is the artifact folder a deployment copy is made from.
type DerivedSource struct {
	Label string // "<package>/<artifactDir>"
	Dir   string // local folder
	// Reason: "artifactDir" (deployed under another ID) or the deploymentPrefix.
	Reason string
}

// DerivedIndex describes the deployment copies of a deploy config.
type DerivedIndex struct {
	// Artifacts maps the tenant ID of a copy to its source.
	Artifacts map[string]DerivedSource
	// Packages maps a prefixed tenant package ID to the configured package.
	Packages map[string]string
	// Overrides lists the configOverrides keys per source artifact ID: the
	// tenant's parameters.prop has the deployed values for them, the
	// repository keeps its own.
	Overrides map[string]map[string]bool
}

type derivedCheck struct {
	packageID string
	artifact  *cpi.ArtifactDetails
	source    DerivedSource
	sig       ArtifactState
	workDir   string
	index     int // in items
}

// add records an item; report false defers the progress callback (a
// derived copy, reported after its check).
func (r *run) add(it Item, report bool) int {
	r.mu.Lock()
	r.items = append(r.items, it)
	index := len(r.items) - 1
	if !r.o.DryRun {
		switch it.Action {
		case ActionWrite, ActionDelete:
			r.paths[it.Package+"/"+it.Artifact] = true
		}
	}
	r.mu.Unlock()
	ev := log.Info()
	switch {
	case it.Warning != "":
		ev = log.Warn()
	case it.Status == StatusLocalModified:
		ev = log.Warn()
	case it.Status == StatusUnchanged:
		ev = log.Debug()
	}
	msg := fmt.Sprintf("%-15s %s/%s (%s)", it.Label(), it.Package, it.Artifact, it.Action)
	if it.Source != "" {
		msg += " source: " + it.Source
	}
	if it.Note != "" {
		msg += " - " + it.Note
	}
	if len(it.OrphanParameters) > 0 {
		msg += " - orphan parameters: " + strings.Join(it.OrphanParameters, ", ")
	}
	if it.Warning != "" {
		msg += " ⚠ " + it.Warning
	}
	ev.Str("artifact", it.Artifact).Str("status", it.Status).Msg(msg)
	if report {
		r.progress(it)
	}
	return index
}

// progress calls the Progress callback, one call at a time.
func (r *run) progress(it Item) {
	if r.o.Progress == nil {
		return
	}
	r.progressMu.Lock()
	defer r.progressMu.Unlock()
	r.o.Progress(it)
}

// sortedItems returns the outcomes, sorted by package and artifact.
func (r *run) sortedItems() []Item {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := slices.Clone(r.items)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Package != out[j].Package {
			return out[i].Package < out[j].Package
		}
		return out[i].Artifact < out[j].Artifact
	})
	return out
}

func (r *run) derivedSource(packageID, artifactID string) (DerivedSource, bool) {
	if r.o.Derived == nil {
		return DerivedSource{}, false
	}
	if src, ok := r.o.Derived.Artifacts[artifactID]; ok {
		return src, true
	}
	if pkg, ok := r.o.Derived.Packages[packageID]; ok {
		return DerivedSource{Label: pkg + "/?", Reason: "package"}, true
	}
	return DerivedSource{}, false
}

func sameSignature(prev, sig ArtifactState) bool {
	return !sig.ModifiedAt.IsZero() && !prev.ModifiedAt.IsZero() && prev.Type == sig.Type && prev.Version == sig.Version &&
		prev.ModifiedAt.Equal(sig.ModifiedAt) && prev.ConfigHash == sig.ConfigHash
}

// localModified reports whether the local folder differs from what the last
// snapshot left there.
func localModified(prev ArtifactState, dir string) bool {
	if prev.FilesHash != "" {
		h, err := content.TreeHash(dir)
		return err != nil || h != prev.FilesHash
	}
	if prev.ContentHash != "" { // state of an earlier cpictl
		h, err := localContentHash(dir)
		return err != nil || h != prev.ContentHash
	}
	return false
}

func hasManifest(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "META-INF", "MANIFEST.MF"))
	return err == nil
}

// skipDraft reports a draft that is not written (DraftHandling SKIP) and
// keeps it in the state: the entry of the last saved version, marked as
// draft, or a new entry without hashes.
func (r *run) skipDraft(packageId string, artifact *cpi.ArtifactDetails) {
	r.add(Item{Package: packageId, Artifact: artifact.Id, Status: StatusDraft, Action: ActionSkip, Draft: true,
		Note: "draft on the tenant (--draft-handling SKIP)"}, true)
	if r.o.DryRun {
		return
	}
	key := packageId + "/" + artifact.Id
	st, ok := r.state.get(key)
	if !ok {
		st = ArtifactState{Type: artifact.ArtifactType, Version: artifact.Version, ModifiedAt: artifact.ModifiedAt.UTC()}
	}
	if !st.Draft {
		st.Draft = true
		r.state.set(key, st)
	}
}

// snapshotArtifact decides about one tenant artifact and, unless DryRun,
// writes the local folder.
func (r *run) snapshotArtifact(packageId, workDir, artifactsDir string, artifact *cpi.ArtifactDetails) error {
	o := r.o
	if artifact.IsDraft {
		switch o.DraftHandling {
		case "SKIP":
			r.skipDraft(packageId, artifact)
			return nil
		case "ERROR":
			return fmt.Errorf("Artifact %v is in draft version. Save Version in Web UI first!", artifact.Id)
		}
	}
	dir := filepath.Join(artifactsDir, artifact.Id)
	key := packageId + "/" + artifact.Id

	sig, sigErr := tenantSignature(r.exe, artifact)
	if sigErr != nil {
		log.Warn().Msgf("Cannot read the configuration of %v (%v): comparing the content", artifact.Id, sigErr)
		sig.ModifiedAt = time.Time{}
	}

	if src, ok := r.derivedSource(packageId, artifact.Id); ok {
		it := Item{Package: packageId, Artifact: artifact.Id, Status: StatusDerived, Action: ActionSkip, Source: src.Label}
		if hasManifest(dir) {
			it.Note = "a local folder exists but is never deployed"
			if o.Prune && !o.DryRun {
				if err := os.RemoveAll(dir); err != nil {
					return err
				}
				it.Action, it.Note = ActionDelete, "local folder removed (--prune): it is never deployed"
			} else if o.Prune {
				it.Action = ActionDelete
			} else {
				it.Note += "; --prune removes it"
			}
		}
		if src.Dir == "" {
			it.Warning = "prefixed package: the artifact is not in the deploy config"
			r.add(it, true)
			return nil
		}
		prev, ok := r.state.get(key)
		if ok && prev.Derived != "" && sameSignature(prev, sig) {
			r.add(it, true)
			return nil // checked before, unchanged since
		}
		idx := r.add(it, false)
		r.mu.Lock()
		r.deferred = append(r.deferred, derivedCheck{packageID: packageId, artifact: artifact, source: src, sig: sig, workDir: workDir, index: idx})
		r.mu.Unlock()
		return nil
	}

	prev, havePrev := r.state.get(key)
	if havePrev && prev.Draft && !artifact.IsDraft && !o.DryRun {
		// the draft was saved or discarded since
		prev.Draft = false
		r.state.set(key, prev)
	}
	localExists := hasManifest(dir)
	modified := havePrev && prev.Derived == "" && localExists && localModified(prev, dir)
	tenantUnchanged := havePrev && prev.Derived == "" && sigErr == nil && sameSignature(prev, sig)
	it := Item{Package: packageId, Artifact: artifact.Id, Draft: artifact.IsDraft}

	switch {
	case !localExists:
		it.Status = StatusNew
	case modified && tenantUnchanged && !o.OverwriteLocal:
		it.Status, it.Action, it.Note = StatusLocalModified, ActionSkip, "local edits, tenant unchanged since the last snapshot"
		r.add(it, true)
		return nil
	case !modified && tenantUnchanged && (o.Incremental || o.DryRun):
		it.Status, it.Action = StatusUnchanged, ActionNone
		r.add(it, true)
		r.skipped.Add(1)
		return nil
	}

	prepared, uploadHash, orphans, err := r.prepareSnapshot(packageId, workDir, dir, artifact)
	if err != nil {
		return err
	}
	it.OrphanParameters = orphans
	tenantTree, err := content.TreeHash(prepared)
	if err != nil {
		return err
	}
	record := func() error {
		if o.DryRun {
			return nil
		}
		st := sig
		st.UploadHash, st.Draft = uploadHash, artifact.IsDraft
		if st.FilesHash, err = content.TreeHash(dir); err != nil {
			return err
		}
		st.ContentHash, _ = localContentHash(dir)
		r.state.set(key, st)
		return nil
	}

	if localExists {
		localTree, err := content.TreeHash(dir)
		if err != nil {
			return err
		}
		switch {
		case localTree == tenantTree:
			it.Status, it.Action = StatusUnchanged, ActionNone
			r.add(it, true)
			return record()
		case modified:
			// the tenant changed too: is it the local edit, deployed?
			if local, err := content.UploadHash(os.DirFS(dir), artifact.ArtifactType); err == nil && local == uploadHash && uploadHash != "" {
				it.Status, it.Action, it.Note = StatusUnchanged, ActionNone, "the local edits are deployed on the tenant; local files kept"
				r.add(it, true)
				return record()
			}
			if !o.OverwriteLocal {
				it.Status, it.Action, it.Note = StatusLocalModified, ActionSkip, "local edits, and the tenant changed too: not overwritten (--overwrite-local)"
				r.add(it, true)
				return nil
			}
			it.Status, it.Note = StatusChanged, "local edits overwritten (--overwrite-local)"
		default:
			it.Status = StatusChanged
			if !havePrev {
				it.Note = "no earlier snapshot of this artifact: local edits cannot be told apart"
			}
		}
	}
	it.Action = ActionWrite
	r.add(it, true)
	if o.DryRun {
		return nil
	}
	if err := file.ReplaceDir(prepared, dir); err != nil {
		return err
	}
	return record()
}

// prepareSnapshot downloads an artifact and turns it into what the local
// folder should contain: line endings and parameters.prop normalized,
// Bundle-Version of the repository kept (or the tenant's when higher), the
// configOverrides keys of parameters.prop taken from the local folder.
// It returns the folder and the upload hash of the tenant's content.
func (r *run) prepareSnapshot(packageId, workDir, localDir string, artifact *cpi.ArtifactDetails) (string, string, []string, error) {
	dir, err := r.downloadNormalized(workDir, artifact)
	if err != nil {
		return "", "", nil, err
	}
	uploadHash := uploadHashOf(dir, artifact.ArtifactType)
	repoVersion, _ := manifest.Version(localDir)
	if !numericVersion(repoVersion) {
		repoVersion = "" // e.g. "Active" written for a draft by an earlier cpictl
	}
	tenantVersion := artifact.Version
	if artifact.IsDraft || !numericVersion(tenantVersion) {
		// a draft has no version number: the last saved one we know of
		tenantVersion = ""
		if prev, ok := r.state.get(packageId + "/" + artifact.Id); ok && numericVersion(prev.Version) {
			tenantVersion = prev.Version
		}
		if rt, err := cpi.NewRuntime(r.exe).GetArtifact(artifact.Id); err == nil && rt != nil && numericVersion(rt.Version) {
			tenantVersion = versioning.Max(tenantVersion, rt.Version)
		}
	}
	v := versioning.Max(repoVersion, tenantVersion)
	if v == "" {
		if current, _ := manifest.Version(dir); !numericVersion(current) {
			v = "1.0.0"
		}
	}
	if v != "" {
		if err := manifest.SetVersion(dir, v); err != nil && !os.IsNotExist(err) {
			return "", "", nil, err
		}
	}
	if r.o.Derived != nil {
		if keys := r.o.Derived.Overrides[artifact.Id]; len(keys) > 0 {
			tp := filepath.Join(dir, filepath.FromSlash(content.ParametersFile))
			if tenant, err := os.ReadFile(tp); err == nil {
				local, _ := os.ReadFile(filepath.Join(localDir, filepath.FromSlash(content.ParametersFile)))
				if err := os.WriteFile(tp, content.RestoreProperties(tenant, local, keys), 0o644); err != nil {
					return "", "", nil, err
				}
			}
		}
	}
	orphans, err := content.DropOrphanParameters(dir, r.o.KeepOrphanParameters)
	if err != nil {
		return "", "", nil, err
	}
	return dir, uploadHash, orphans, nil
}

// numericVersion reports whether v is a version number ("1.0.6"), not
// empty and not the tenant's draft marker "Active".
func numericVersion(v string) bool {
	if v == "" {
		return false
	}
	for _, seg := range strings.Split(v, ".") {
		if seg == "" || strings.Trim(seg, "0123456789") != "" {
			return false
		}
	}
	return true
}

// downloadNormalized downloads and extracts an artifact into workDir and
// normalizes it (content.NormalizeTree).
func (r *run) downloadNormalized(workDir string, artifact *cpi.ArtifactDetails) (string, error) {
	dt := cpi.NewDesigntimeArtifact(artifact.ArtifactType, r.exe)
	zip := filepath.Join(workDir, "download", artifact.Id+".zip")
	if err := os.MkdirAll(filepath.Dir(zip), 0o755); err != nil {
		return "", err
	}
	if err := dt.Download(zip, artifact.Id); err != nil {
		return "", err
	}
	r.downloaded.Add(1)
	dir := filepath.Join(workDir, "download", artifact.Id)
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := file.UnzipSource(zip, dir); err != nil {
		return "", err
	}
	return dir, content.NormalizeTree(dir)
}

func uploadHashOf(dir, artifactType string) string {
	h, err := content.UploadHash(os.DirFS(dir), artifactType)
	if err != nil {
		return ""
	}
	return h
}

// overrideFiles are what a deployment copy may change against its source:
// configOverrides (parameters.prop), the ID and name (MANIFEST.MF,
// metainfo.prop, .project).
var overrideFiles = map[string]bool{content.ParametersFile: true, "META-INF/MANIFEST.MF": true, "metainfo.prop": true, ".project": true}

const iflowDir = "src/main/resources/scenarioflows/integrationflow/"

// checkDerivedCopies compares the deployment copies that changed since the
// last snapshot with their source folders (after all artifacts are
// written): a difference beyond the files a deployment changes means
// someone edited the copy on the tenant.
func (r *run) checkDerivedCopies() error {
	r.mu.Lock()
	checks := r.deferred
	r.deferred = nil
	r.mu.Unlock()
	var wg gosync.WaitGroup
	var mu gosync.Mutex
	var failed []string
	for _, c := range checks {
		wg.Add(1)
		r.slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-r.slots }()
			warning, note, err := r.checkDerived(c)
			if err != nil {
				mu.Lock()
				failed = append(failed, fmt.Sprintf("%s: %v", c.artifact.Id, err))
				mu.Unlock()
			}
			r.mu.Lock()
			it := &r.items[c.index]
			if warning != "" {
				it.Warning = warning
				log.Warn().Str("artifact", it.Artifact).Msgf("derived %s/%s (source %s): %s", it.Package, it.Artifact, it.Source, warning)
			}
			if note != "" {
				it.Note = strings.TrimPrefix(it.Note+"; "+note, "; ")
			}
			done := *it
			r.mu.Unlock()
			r.progress(done)
		}()
	}
	wg.Wait()
	if len(failed) > 0 {
		sort.Strings(failed)
		return fmt.Errorf("%d derived copy check(s) failed: %s", len(failed), strings.Join(failed, "; "))
	}
	return nil
}

func (r *run) checkDerived(c derivedCheck) (warning, note string, err error) {
	if !hasManifest(c.source.Dir) {
		return "source folder " + c.source.Label + " not found locally", "", nil
	}
	workDir := filepath.Join(c.workDir, "derived")
	copyDir, err := r.downloadNormalized(workDir, c.artifact)
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(copyDir)
	diff, err := content.TreeDiff(c.source.Dir, copyDir, func(rel string) bool {
		return overrideFiles[rel] || strings.HasPrefix(rel, iflowDir)
	})
	if err != nil {
		return "", "", err
	}
	// the model may be named after the copy's ID: compare the models by content
	if !sameModels(c.source.Dir, copyDir) {
		diff = append(diff, iflowDir+"*.iflw")
	}
	if !r.o.DryRun {
		st := c.sig
		st.Derived = c.source.Label
		st.UploadHash = uploadHashOf(copyDir, c.artifact.ArtifactType)
		r.state.set(c.packageID+"/"+c.artifact.Id, st)
	}
	if len(diff) == 0 {
		return "", "same content as the source (apart from the deployment changes)", nil
	}
	if len(diff) > 5 {
		diff = append(diff[:5], fmt.Sprintf("and %d more", len(diff)-5))
	}
	return "the tenant copy differs from its source in " + strings.Join(diff, ", ") +
		": it was edited on the tenant, and that edit is lost with the next deployment from the source", "", nil
}

func sameModels(a, b string) bool {
	read := func(dir string) []string {
		var out []string
		entries, _ := os.ReadDir(filepath.Join(dir, filepath.FromSlash(iflowDir)))
		for _, e := range entries {
			if data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(iflowDir), e.Name())); err == nil {
				out = append(out, strings.ReplaceAll(string(data), "\r\n", "\n"))
			}
		}
		sort.Strings(out)
		return out
	}
	return slices.Equal(read(a), read(b))
}

// leftovers reports (and with Prune removes) the local artifact folders of
// a package that are not on the tenant (tenantIDs nil: the whole package is
// gone).
func (r *run) leftovers(packageId, artifactsDir string, tenantIDs map[string]bool) error {
	o := r.o
	entries, err := os.ReadDir(artifactsDir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	seen := map[string]bool{}
	for _, e := range entries {
		id := e.Name()
		if !e.IsDir() || strings.HasPrefix(id, ".") || tenantIDs[id] {
			continue
		}
		dir := filepath.Join(artifactsDir, id)
		if !hasManifest(dir) {
			continue
		}
		seen[id] = true
		key := packageId + "/" + id
		prev, havePrev := r.state.get(key)
		it := Item{Package: packageId, Artifact: id, Action: ActionNone}
		switch {
		case !havePrev:
			it.Status, it.Note = StatusLocalOnly, "not on the tenant and never snapshotted (a new artifact?): kept"
		case prev.Derived != "":
			it.Status, it.Source, it.Note = StatusDerived, prev.Derived, "the deployment copy is gone from the tenant"
			if o.Prune {
				it.Action = ActionDelete
			}
		case localModified(prev, dir):
			it.Status, it.Note = StatusLocalModified, "deleted on the tenant, but edited locally: kept"
		case o.Prune:
			it.Status, it.Action, it.Note = StatusDeleted, ActionDelete, "removed (--prune)"
		default:
			it.Status, it.Note = StatusDeleted, "--prune removes it"
		}
		r.add(it, true)
		if it.Action == ActionDelete && !o.DryRun {
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
			r.state.remove(key)
		}
	}
	// state entries of artifacts that are neither local nor on the tenant
	if !o.DryRun {
		for _, key := range r.state.keys(packageId) {
			id := strings.TrimPrefix(key, packageId+"/")
			if !tenantIDs[id] && !seen[id] {
				r.state.remove(key)
			}
		}
	}
	return nil
}
