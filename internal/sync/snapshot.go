package sync

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
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/manifest"
	"github.com/cpars-innovation/cpicli/pkg/versioning"
	"github.com/rs/zerolog/log"
)

// Snapshot statuses of an artifact.
const (
	SnapNew           = "new"            // on the tenant, not local
	SnapChanged       = "changed"        // tenant version, ModifiedAt or content differs from local
	SnapUnchanged     = "unchanged"      // nothing to do
	SnapDeleted       = "deleted"        // local (from an earlier snapshot), no longer on the tenant
	SnapLocalModified = "local-modified" // local edits since the last snapshot: not overwritten
	SnapLocalOnly     = "local-only"     // local, not on the tenant, never snapshotted (a new artifact)
	SnapDerived       = "derived"        // a deployment copy of another artifact: not written
)

// Snapshot actions (what was done, or would be done in a dry run).
const (
	ActWrite  = "write"
	ActDelete = "delete"
	ActSkip   = "skip"
	ActNone   = "none"
)

// SnapshotItem is the outcome for one artifact.
type SnapshotItem struct {
	Package  string `json:"package"`
	Artifact string `json:"artifact"`
	Status   string `json:"status"`
	Action   string `json:"action"`
	// Source of a derived copy: "<package>/<artifactDir>".
	Source  string `json:"source,omitempty"`
	Note    string `json:"note,omitempty"`
	Warning string `json:"warning,omitempty"`
	// Draft is set for an artifact in draft on the tenant that was written
	// (draftHandling ADD): status "new (draft)" or "changed (draft)".
	Draft bool `json:"draft,omitempty"`
	// OrphanParameters are parameters.prop keys the tenant keeps that
	// parameters.propdef no longer declares (not written unless
	// KeepOrphanParameters).
	OrphanParameters []string `json:"orphanParameters,omitempty"`
}

// Label is the status with the draft marker.
func (it SnapshotItem) Label() string {
	if it.Draft {
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

// SnapshotOptions make ArtifactsToGit write a repository in the tenant
// layout: one exact copy per artifact, local edits protected.
type SnapshotOptions struct {
	DryRun         bool
	OverwriteLocal bool
	Prune          bool
	// KeepOrphanParameters writes parameters.prop keys that
	// parameters.propdef does not declare (reported either way).
	KeepOrphanParameters bool
	// Derived: deployment copies that are not written (nil: none).
	Derived *DerivedIndex

	mu       gosync.Mutex
	items    []SnapshotItem
	deferred []derivedCheck
}

type derivedCheck struct {
	packageID string
	artifact  *cpi.ArtifactDetails
	source    DerivedSource
	sig       ArtifactState
	workDir   string
	index     int // in items
}

func (o *SnapshotOptions) add(it SnapshotItem) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.items = append(o.items, it)
	ev := log.Info()
	switch {
	case it.Warning != "":
		ev = log.Warn()
	case it.Status == SnapLocalModified:
		ev = log.Warn()
	case it.Status == SnapUnchanged:
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
	return len(o.items) - 1
}

// Items returns the outcomes, sorted by package and artifact.
func (o *SnapshotOptions) Items() []SnapshotItem {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := slices.Clone(o.items)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Package != out[j].Package {
			return out[i].Package < out[j].Package
		}
		return out[i].Artifact < out[j].Artifact
	})
	return out
}

func (o *SnapshotOptions) derivedSource(packageID, artifactID string) (DerivedSource, bool) {
	if o.Derived == nil {
		return DerivedSource{}, false
	}
	if src, ok := o.Derived.Artifacts[artifactID]; ok {
		return src, true
	}
	if pkg, ok := o.Derived.Packages[packageID]; ok {
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
		h, err := file.TreeHash(dir)
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

// snapshotArtifact decides about one tenant artifact and, unless DryRun,
// writes the local folder.
func (s *Synchroniser) snapshotArtifact(packageId, workDir, artifactsDir, draftHandling string, scriptCollectionMap []string, artifact *cpi.ArtifactDetails) error {
	o := s.Snap
	if artifact.IsDraft {
		switch draftHandling {
		case "SKIP":
			o.add(SnapshotItem{Package: packageId, Artifact: artifact.Id, Status: SnapUnchanged, Action: ActSkip, Note: "draft on the tenant (--draft-handling SKIP)"})
			return nil
		case "ERROR":
			return fmt.Errorf("Artifact %v is in draft version. Save Version in Web UI first!", artifact.Id)
		}
	}
	dir := filepath.Join(artifactsDir, artifact.Id)
	key := packageId + "/" + artifact.Id

	sig, sigErr := ArtifactState{}, error(nil)
	if s.State != nil {
		sig, sigErr = tenantSignature(s.exe, artifact)
		if sigErr != nil {
			log.Warn().Msgf("Cannot read the configuration of %v (%v): comparing the content", artifact.Id, sigErr)
			sig.ModifiedAt = time.Time{}
		}
	}

	if src, ok := o.derivedSource(packageId, artifact.Id); ok {
		it := SnapshotItem{Package: packageId, Artifact: artifact.Id, Status: SnapDerived, Action: ActSkip, Source: src.Label}
		if hasManifest(dir) {
			it.Note = "a local folder exists but is never deployed"
			if o.Prune && !o.DryRun {
				if err := os.RemoveAll(dir); err != nil {
					return err
				}
				it.Action, it.Note = ActDelete, "local folder removed (--prune): it is never deployed"
			} else if o.Prune {
				it.Action = ActDelete
			} else {
				it.Note += "; --prune removes it"
			}
		}
		if src.Dir == "" {
			it.Warning = "prefixed package: the artifact is not in the deploy config"
			o.add(it)
			return nil
		}
		idx := o.add(it)
		prev, ok := s.stateGet(key)
		if ok && prev.Derived != "" && sameSignature(prev, sig) {
			return nil // checked before, unchanged since
		}
		o.mu.Lock()
		o.deferred = append(o.deferred, derivedCheck{packageID: packageId, artifact: artifact, source: src, sig: sig, workDir: workDir, index: idx})
		o.mu.Unlock()
		return nil
	}

	prev, havePrev := s.stateGet(key)
	localExists := hasManifest(dir)
	modified := havePrev && prev.Derived == "" && localExists && localModified(prev, dir)
	tenantUnchanged := havePrev && prev.Derived == "" && sigErr == nil && sameSignature(prev, sig)
	it := SnapshotItem{Package: packageId, Artifact: artifact.Id, Draft: artifact.IsDraft}

	switch {
	case !localExists:
		it.Status = SnapNew
	case modified && tenantUnchanged && !o.OverwriteLocal:
		it.Status, it.Action, it.Note = SnapLocalModified, ActSkip, "local edits, tenant unchanged since the last snapshot"
		o.add(it)
		return nil
	case !modified && tenantUnchanged && (s.Incremental || o.DryRun):
		it.Status, it.Action = SnapUnchanged, ActNone
		o.add(it)
		s.skipped.Add(1)
		return nil
	}

	prepared, uploadHash, orphans, err := s.prepareSnapshot(packageId, workDir, dir, scriptCollectionMap, artifact)
	if err != nil {
		return err
	}
	it.OrphanParameters = orphans
	tenantTree, err := file.TreeHash(prepared)
	if err != nil {
		return err
	}
	record := func() error {
		if o.DryRun || s.State == nil {
			return nil
		}
		st := sig
		st.UploadHash, st.Draft = uploadHash, artifact.IsDraft
		if st.FilesHash, err = file.TreeHash(dir); err != nil {
			return err
		}
		st.ContentHash, _ = localContentHash(dir)
		s.State.set(key, st)
		return nil
	}

	if localExists {
		localTree, err := file.TreeHash(dir)
		if err != nil {
			return err
		}
		switch {
		case localTree == tenantTree:
			it.Status, it.Action = SnapUnchanged, ActNone
			o.add(it)
			return record()
		case modified:
			// the tenant changed too: is it the local edit, deployed?
			if local, err := file.UploadHash(os.DirFS(dir), artifact.ArtifactType); err == nil && local == uploadHash && uploadHash != "" {
				it.Status, it.Action, it.Note = SnapUnchanged, ActNone, "the local edits are deployed on the tenant; local files kept"
				o.add(it)
				return record()
			}
			if !o.OverwriteLocal {
				it.Status, it.Action, it.Note = SnapLocalModified, ActSkip, "local edits, and the tenant changed too: not overwritten (--overwrite-local)"
				o.add(it)
				return nil
			}
			it.Status, it.Note = SnapChanged, "local edits overwritten (--overwrite-local)"
		default:
			it.Status = SnapChanged
			if !havePrev {
				it.Note = "no earlier snapshot of this artifact: local edits cannot be told apart"
			}
		}
	}
	it.Action = ActWrite
	o.add(it)
	if o.DryRun {
		return nil
	}
	if err := file.ReplaceDir(prepared, dir); err != nil {
		return err
	}
	return record()
}

func (s *Synchroniser) stateGet(key string) (ArtifactState, bool) {
	if s.State == nil {
		return ArtifactState{}, false
	}
	return s.State.get(key)
}

// prepareSnapshot downloads an artifact and turns it into what the local
// folder should contain: line endings and parameters.prop normalized,
// Bundle-Version of the repository kept (or the tenant's when higher), the
// configOverrides keys of parameters.prop taken from the local folder.
// It returns the folder and the upload hash of the tenant's content.
func (s *Synchroniser) prepareSnapshot(packageId, workDir, localDir string, scriptCollectionMap []string, artifact *cpi.ArtifactDetails) (string, string, []string, error) {
	dir, err := s.downloadNormalized(workDir, artifact)
	if err != nil {
		return "", "", nil, err
	}
	uploadHash := ""
	if len(scriptCollectionMap) == 0 {
		uploadHash = uploadHashOf(dir, artifact.ArtifactType)
	} else if artifact.ArtifactType == "Integration" {
		if err := file.UpdateBPMN(dir, scriptCollectionMap); err != nil {
			return "", "", nil, err
		}
	}
	repoVersion, _ := manifest.Version(localDir)
	if !numericVersion(repoVersion) {
		repoVersion = "" // e.g. "Active" written for a draft by an earlier cpictl
	}
	tenantVersion := artifact.Version
	if artifact.IsDraft || !numericVersion(tenantVersion) {
		// a draft has no version number: the last saved one we know of
		tenantVersion = ""
		if prev, ok := s.stateGet(packageId + "/" + artifact.Id); ok && numericVersion(prev.Version) {
			tenantVersion = prev.Version
		}
		if rt, err := cpi.NewRuntime(s.exe).GetArtifact(artifact.Id); err == nil && rt != nil && numericVersion(rt.Version) {
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
	if o := s.Snap; o != nil && o.Derived != nil {
		if keys := o.Derived.Overrides[artifact.Id]; len(keys) > 0 {
			tp := filepath.Join(dir, filepath.FromSlash(file.ParametersFile))
			if tenant, err := os.ReadFile(tp); err == nil {
				local, _ := os.ReadFile(filepath.Join(localDir, filepath.FromSlash(file.ParametersFile)))
				if err := os.WriteFile(tp, file.RestoreProperties(tenant, local, keys), 0o644); err != nil {
					return "", "", nil, err
				}
			}
		}
	}
	keep := s.Snap != nil && s.Snap.KeepOrphanParameters
	orphans, err := file.DropOrphanParameters(dir, keep)
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
// normalizes it (file.NormalizeTree).
func (s *Synchroniser) downloadNormalized(workDir string, artifact *cpi.ArtifactDetails) (string, error) {
	dt := cpi.NewDesigntimeArtifact(artifact.ArtifactType, s.exe)
	zip := filepath.Join(workDir, "download", artifact.Id+".zip")
	if err := os.MkdirAll(filepath.Dir(zip), 0o755); err != nil {
		return "", err
	}
	if err := dt.Download(zip, artifact.Id); err != nil {
		return "", err
	}
	s.downloaded.Add(1)
	dir := filepath.Join(workDir, "download", artifact.Id)
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := file.UnzipSource(zip, dir); err != nil {
		return "", err
	}
	return dir, file.NormalizeTree(dir)
}

func uploadHashOf(dir, artifactType string) string {
	h, err := file.UploadHash(os.DirFS(dir), artifactType)
	if err != nil {
		return ""
	}
	return h
}

// overrideFiles are what a deployment copy may change against its source:
// configOverrides (parameters.prop), the ID and name (MANIFEST.MF,
// metainfo.prop, .project).
var overrideFiles = map[string]bool{file.ParametersFile: true, "META-INF/MANIFEST.MF": true, "metainfo.prop": true, ".project": true}

const iflowDir = "src/main/resources/scenarioflows/integrationflow/"

// CheckDerived compares the deployment copies that changed since the last
// snapshot with their source folders (after all artifacts are written):
// a difference beyond the files a deployment changes means someone edited
// the copy on the tenant.
func (s *Synchroniser) CheckDerived() error {
	o := s.Snap
	if o == nil {
		return nil
	}
	o.mu.Lock()
	checks := o.deferred
	o.deferred = nil
	o.mu.Unlock()
	var wg gosync.WaitGroup
	var mu gosync.Mutex
	var failed []string
	for _, c := range checks {
		wg.Add(1)
		run := func() {
			defer wg.Done()
			warning, note, err := s.checkDerived(c)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed = append(failed, fmt.Sprintf("%s: %v", c.artifact.Id, err))
				return
			}
			o.mu.Lock()
			it := &o.items[c.index]
			if warning != "" {
				it.Warning = warning
				log.Warn().Str("artifact", it.Artifact).Msgf("derived %s/%s (source %s): %s", it.Package, it.Artifact, it.Source, warning)
			}
			if note != "" {
				it.Note = strings.TrimPrefix(it.Note+"; "+note, "; ")
			}
			o.mu.Unlock()
		}
		if s.ArtifactSlots == nil {
			run()
			continue
		}
		s.ArtifactSlots <- struct{}{}
		go func() {
			defer func() { <-s.ArtifactSlots }()
			run()
		}()
	}
	wg.Wait()
	if len(failed) > 0 {
		sort.Strings(failed)
		return fmt.Errorf("%d derived copy check(s) failed: %s", len(failed), strings.Join(failed, "; "))
	}
	return nil
}

func (s *Synchroniser) checkDerived(c derivedCheck) (warning, note string, err error) {
	if !hasManifest(c.source.Dir) {
		return "source folder " + c.source.Label + " not found locally", "", nil
	}
	workDir := filepath.Join(c.workDir, "derived")
	copyDir, err := s.downloadNormalized(workDir, c.artifact)
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(copyDir)
	diff, err := file.TreeDiff(c.source.Dir, copyDir, func(rel string) bool {
		return overrideFiles[rel] || strings.HasPrefix(rel, iflowDir)
	})
	if err != nil {
		return "", "", err
	}
	// the model may be named after the copy's ID: compare the models by content
	if !sameModels(c.source.Dir, copyDir) {
		diff = append(diff, iflowDir+"*.iflw")
	}
	if s.State != nil && !s.Snap.DryRun {
		st := c.sig
		st.Derived = c.source.Label
		st.UploadHash = uploadHashOf(copyDir, c.artifact.ArtifactType)
		s.State.set(c.packageID+"/"+c.artifact.Id, st)
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

// SnapshotLeftovers reports (and with Prune removes) the local artifact
// folders of a package that are not on the tenant (tenantIDs nil: the whole
// package is gone).
func (s *Synchroniser) SnapshotLeftovers(packageId, artifactsDir string, tenantIDs map[string]bool) error {
	o := s.Snap
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
		prev, havePrev := s.stateGet(key)
		it := SnapshotItem{Package: packageId, Artifact: id, Action: ActNone}
		switch {
		case !havePrev:
			it.Status, it.Note = SnapLocalOnly, "not on the tenant and never snapshotted (a new artifact?): kept"
		case prev.Derived != "":
			it.Status, it.Source, it.Note = SnapDerived, prev.Derived, "the deployment copy is gone from the tenant"
			if o.Prune {
				it.Action = ActDelete
			}
		case localModified(prev, dir):
			it.Status, it.Note = SnapLocalModified, "deleted on the tenant, but edited locally: kept"
		case o.Prune:
			it.Status, it.Action, it.Note = SnapDeleted, ActDelete, "removed (--prune)"
		default:
			it.Status, it.Note = SnapDeleted, "--prune removes it"
		}
		o.add(it)
		if it.Action == ActDelete && !o.DryRun {
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
			if s.State != nil {
				s.State.remove(key)
			}
		}
	}
	// state entries of artifacts that are neither local nor on the tenant
	if s.State != nil && !o.DryRun {
		for _, key := range s.State.keys(packageId) {
			id := strings.TrimPrefix(key, packageId+"/")
			if !tenantIDs[id] && !seen[id] {
				s.State.remove(key)
			}
		}
	}
	return nil
}
