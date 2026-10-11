package sync

import (
	"bufio"
	"bytes"
	"fmt"
	"net/textproto"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/file"
	"github.com/cpars-innovation/cpicli/internal/manifest"
	"github.com/cpars-innovation/cpicli/internal/str"
	"github.com/cpars-innovation/cpicli/internal/versioning"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/snapshot"
	"github.com/go-errors/errors"
	"github.com/magiconair/properties"
	"github.com/rs/zerolog/log"
)

type Synchroniser struct {
	exe *httpclnt.HTTPExecuter
	ip  *cpi.IntegrationPackage
	// Versioning decides the designtime version on upload (see
	// docs/versioning.md); Unset keeps the tenant's version.
	Versioning versioning.Mode
	// Baseline, when set, is the snapshot state of the target tenant: an
	// upload compares the local content with the content the snapshot saw
	// instead of downloading the artifact again, as long as the tenant's
	// version and ModifiedAt are still the snapshot's. Artifacts the state
	// does not cover are downloaded. VerifyDownload always downloads.
	Baseline       *snapshot.State
	VerifyDownload bool
	// DryRun makes UploadArtifact read only: it compares and reports what it
	// would do (Action, the version the upload would give) without writing.
	DryRun bool
	// KeepRuntime: an upload never undeploys the runtime artifact. A content
	// change with a version equal to the running one, or changed configured
	// parameters, set Redeploy in the outcome instead (a later forced
	// deployment replaces it, without downtime).
	KeepRuntime bool
}

func New(exe *httpclnt.HTTPExecuter) *Synchroniser {
	s := new(Synchroniser)
	s.exe = exe
	s.ip = cpi.NewIntegrationPackage(exe)
	return s
}

// PackageToGit writes the package details file <artifactsDir>/<id>.json when
// it differs from the tenant's (snapshot.WritePackageDetails).
func (s *Synchroniser) PackageToGit(packageDataFromTenant *cpi.PackageSingleData, packageId string, workDir string, artifactsDir string) error {
	_, err := snapshot.WritePackageDetails(artifactsDir, packageId, packageDataFromTenant)
	return err
}

func (s *Synchroniser) VerifyDownloadablePackage(packageId string) (packageDataFromTenant *cpi.PackageSingleData, readOnly bool, packageExists bool, err error) {
	// Verify the package is downloadable (not read only)
	packageDataFromTenant, readOnly, packageExists, err = s.ip.Get(packageId)
	if err != nil {
		return nil, false, false, err
	}
	if !packageExists {
		return nil, false, false, fmt.Errorf("Package %v does not exist", packageId)
	}
	if readOnly {
		log.Warn().Msgf("Skipping package %v as it is Configure-only and cannot be downloaded", packageId)
	}
	return
}

func (s *Synchroniser) ArtifactsToGit(packageId string, workDir string, artifactsDir string, includedIds []string, excludedIds []string, draftHandling string, dirNamingType string, scriptCollectionMap []string) error {
	// Get all design time artifacts of package
	log.Info().Msgf("Getting artifacts in integration package %v", packageId)
	artifacts, err := s.ip.GetAllArtifacts(packageId)
	if err != nil {
		return err
	}

	// Create temp directories in working dir
	err = os.MkdirAll(workDir+"/download", os.ModePerm)
	if err != nil {
		return errors.Wrap(err, 0)
	}

	filtered, err := filterArtifacts(artifacts, includedIds, excludedIds)
	if err != nil {
		return err
	}

	for _, artifact := range filtered {
		if err := s.artifactToGit(packageId, workDir, artifactsDir, draftHandling, dirNamingType, scriptCollectionMap, artifact); err != nil {
			return fmt.Errorf("%s: %w", artifact.Id, err)
		}
	}

	// Clean up working directory
	err = os.RemoveAll(workDir + "/download")
	if err != nil {
		return errors.Wrap(err, 0)
	}

	log.Info().Msg("---------------------------------------------------------------------------------")
	log.Info().Msgf("🏆 Completed processing of artifacts in integration package %v", packageId)
	return nil
}

// artifactToGit downloads one artifact and writes it into the Git directory
// when it differs (sync --target git; snapshots are pkg/snapshot).
func (s *Synchroniser) artifactToGit(packageId, workDir, artifactsDir, draftHandling, dirNamingType string, scriptCollectionMap []string, artifact *cpi.ArtifactDetails) error {
	var err error
	log.Info().Msg("---------------------------------------------------------------------------------")
	log.Info().Msgf("📢 Begin processing for artifact %v", artifact.Id)
	// Check if artifact is in draft version
	if artifact.IsDraft {
		switch draftHandling {
		case "SKIP":
			log.Warn().Msgf("Artifact %v is in draft version, and will be skipped", artifact.Id)
			return nil
		case "ADD":
			log.Info().Msgf("Artifact %v is in draft version, and will be added", artifact.Id)
		case "ERROR":
			return fmt.Errorf("Artifact %v is in draft version. Save Version in Web UI first!", artifact.Id)
		}
	}
	var directoryName string
	if dirNamingType == "NAME" {
		directoryName = artifact.Name
	} else {
		directoryName = artifact.Id
	}
	gitArtifactPath := fmt.Sprintf("%v/%v", artifactsDir, directoryName)

	// Download artifact content
	dt := cpi.NewDesigntimeArtifact(artifact.ArtifactType, s.exe)
	targetDownloadFile := fmt.Sprintf("%v/download/%v.zip", workDir, artifact.Id)
	err = dt.Download(targetDownloadFile, artifact.Id)
	if err != nil {
		return err
	}
	// Unzip artifact contents
	log.Debug().Msgf("Target artifact directory name - %v", directoryName)
	downloadedArtifactPath := fmt.Sprintf("%v/download/%v", workDir, directoryName)
	err = file.UnzipSource(targetDownloadFile, downloadedArtifactPath)
	if err != nil {
		return err
	}
	log.Info().Msgf("Downloaded artifact unzipped to %v", downloadedArtifactPath)
	// stable between downloads: no timestamp line, sorted keys
	if err := file.NormalizeParametersFile(downloadedArtifactPath); err != nil {
		return err
	}

	// The download's Bundle-Version is not the designtime version (often
	// 1.0.0): keep the repository's version, or take the tenant's when it
	// is higher (a version saved in the Web UI).
	repoVersion, _ := manifest.Version(gitArtifactPath)
	exportVersion := versioning.Max(repoVersion, artifact.Version)
	if exportVersion != "" {
		if err := manifest.SetVersion(downloadedArtifactPath, exportVersion); err != nil {
			return err
		}
		log.Info().Str("artifact", artifact.Id).Msgf("Bundle-Version %v (repository %v, tenant designtime %v)", exportVersion, orNone(repoVersion), orNone(artifact.Version))
	}
	if file.Exists(fmt.Sprintf("%v/META-INF/MANIFEST.MF", gitArtifactPath)) {
		// (1) If artifact already exists in Git, then compare and update
		log.Info().Msg("Comparing content from tenant against Git")

		// Diff artifact contents
		dirDiffer, err := dt.CompareContent(downloadedArtifactPath, gitArtifactPath, scriptCollectionMap, "git")
		if err != nil {
			return err
		}

		if dirDiffer {
			log.Info().Msg("🏆 Changes detected and will be updated to Git")
			// Update the changes into the Git directory
			err = dt.CopyContent(downloadedArtifactPath, gitArtifactPath)
			if err != nil {
				return err
			}
		} else {
			log.Info().Msg("🏆 No changes detected. Update to Git not required")
		}

	} else { // (2) If artifact does not exist in Git, then add it
		log.Info().Msgf("🏆 Artifact %v does not exist, and will be added to Git", artifact.Id)
		// Update the script collection in IFlow BPMN2 XML before syncing to Git
		if artifact.ArtifactType == "Integration" {
			err = file.UpdateBPMN(downloadedArtifactPath, scriptCollectionMap)
			if err != nil {
				return err
			}
		}
		err = file.ReplaceDir(downloadedArtifactPath, gitArtifactPath)
		if err != nil {
			return err
		}
	}

	return nil
}

func filterArtifacts(artifacts []*cpi.ArtifactDetails, includedIds []string, excludedIds []string) ([]*cpi.ArtifactDetails, error) {
	var output []*cpi.ArtifactDetails

	if len(includedIds) > 0 {
		for _, id := range includedIds {
			artifact := cpi.FindArtifactById(id, artifacts)
			if artifact != nil {
				output = append(output, artifact)
			} else {
				return nil, fmt.Errorf("Artifact %v in --ids-include does not exist", id)
			}
		}
		return output, nil
	} else if len(excludedIds) > 0 {
		for _, id := range excludedIds {
			artifact := cpi.FindArtifactById(id, artifacts)
			if artifact == nil {
				return nil, fmt.Errorf("Artifact %v in --ids-exclude does not exist", id)
			}
		}
		for _, artifact := range artifacts {
			if !slices.Contains(excludedIds, artifact.Id) {
				output = append(output, artifact)
			}
		}
		return output, nil
	}
	return artifacts, nil
}

func (s *Synchroniser) ArtifactsToTenant(packageId string, workDir string, artifactsDir string, includedIds []string, excludedIds []string) error {
	// Get directory list
	baseSourceDir := filepath.Clean(artifactsDir)
	entries, err := os.ReadDir(baseSourceDir)
	if err != nil {
		return errors.Wrap(err, 0)
	}

	artifactDirFound := false
	for _, entry := range entries {
		manifestPath := fmt.Sprintf("%v/%v/META-INF/MANIFEST.MF", baseSourceDir, entry.Name())
		if entry.IsDir() && file.Exists(manifestPath) {
			artifactDirFound = true
			artifactDir := fmt.Sprintf("%v/%v", baseSourceDir, entry.Name())
			log.Info().Msg("---------------------------------------------------------------------------------")
			log.Info().Msgf("Processing directory %v", artifactDir)
			paramFile := fmt.Sprintf("%v/src/main/resouces/parameters/prop", artifactDir)

			headers, err := GetManifestHeaders(manifestPath)
			if err != nil {
				return err
			}

			artifactId := headers.Get("Bundle-SymbolicName")
			// remove spaces then remove ;singleton:=true
			artifactId = strings.ReplaceAll(artifactId, " ", "")
			artifactId = strings.ReplaceAll(artifactId, ";singleton:=true", "")

			// Filter in/out artifacts
			if len(includedIds) > 0 {
				if !slices.Contains(includedIds, artifactId) {
					log.Debug().Msgf("Skipping artifact %v as it is not in --ids-include", artifactId)
					continue
				}
			}
			if len(excludedIds) > 0 {
				if slices.Contains(excludedIds, artifactId) {
					log.Warn().Msgf("Skipping artifact %v as it is in --ids-exclude", artifactId)
					continue
				}
			}

			artifactName := headers.Get("Bundle-Name")
			// remove spaces due to length of bundle name exceeding MANIFEST.MF width
			artifactName = str.TrimManifestField(artifactName, 72)
			artifactType := headers.Get("SAP-BundleType")
			if artifactType == "IntegrationFlow" {
				artifactType = "Integration"
			}

			log.Info().Msgf("📢 Begin processing for artifact %v", artifactId)
			err = s.SingleArtifactToTenant(artifactId, artifactName, artifactType, packageId, artifactDir, workDir, paramFile, nil)
			if err != nil {
				return err
			}
		}
	}
	if !artifactDirFound {
		log.Warn().Msgf("No directory with artifact contents found in %v", baseSourceDir)
	}
	return nil
}

// GetManifestHeaders reads the headers of a MANIFEST.MF. Manifests from a
// tenant end without the blank line that closes a header block; that is
// accepted.
func GetManifestHeaders(manifestPath string) (textproto.MIMEHeader, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	data = append(bytes.TrimRight(data, "\r\n"), "\r\n\r\n"...)
	headers, err := textproto.NewReader(bufio.NewReader(bytes.NewReader(data))).ReadMIMEHeader()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", manifestPath, err)
	}
	return headers, nil
}

// UploadOutcome describes what UploadArtifact changed on the tenant.
type UploadOutcome struct {
	Action string `json:"action"` // CREATED, UPDATED or UNCHANGED
	// RuntimeUndeployed is true when a running artifact with the same version
	// was undeployed so that the changed content can be deployed again.
	RuntimeUndeployed bool `json:"runtimeUndeployed,omitempty"`
	// Version is the designtime version after the upload, VersionReason why
	// (versioning mode and the versions it was derived from).
	Version       string `json:"version,omitempty"`
	VersionReason string `json:"versionReason,omitempty"`
	// VersionSet is true when the designtime version was set (SaveAsVersion).
	VersionSet bool `json:"versionSet,omitempty"`
	// VersionRule is the rule that chose the version: manifest, bump, keep,
	// tenant (no mode) or guard (refused).
	VersionRule string `json:"versionRule,omitempty"`
	// Compared says how an existing artifact was compared: "snapshot" (the
	// baseline, no download) or "download".
	Compared string `json:"compared,omitempty"`
	// CompareReason explains a download when a baseline was given.
	CompareReason string `json:"compareReason,omitempty"`
	// DryRun is true when nothing was written (Synchroniser.DryRun): Action
	// and Version are what the upload would do.
	DryRun bool `json:"dryRun,omitempty"`
	// Redeploy is true when the content changed but the version equals the
	// running one and the runtime was kept (KeepRuntime): it must be
	// deployed with force.
	Redeploy bool `json:"redeploy,omitempty"`
}

func (s *Synchroniser) SingleArtifactToTenant(artifactId, artifactName, artifactType, packageId, artifactDir, workDir, parametersFile string, scriptMap []string) error {
	_, err := s.UploadArtifact(artifactId, artifactName, artifactType, packageId, artifactDir, workDir, parametersFile, scriptMap)
	return err
}

// UploadArtifact creates or updates a designtime artifact from a local
// directory and reports what was done. The content comparison ignores
// Bundle-Version (the tenant's download does not carry the real version);
// s.Versioning decides the version afterwards.
func (s *Synchroniser) UploadArtifact(artifactId, artifactName, artifactType, packageId, artifactDir, workDir, parametersFile string, scriptMap []string) (UploadOutcome, error) {
	outcome := UploadOutcome{Action: "UNCHANGED"}
	dt := cpi.NewDesigntimeArtifact(artifactType, s.exe)

	repoVersion, _ := manifest.Version(artifactDir)
	if s.Versioning == versioning.Manifest && repoVersion == "" {
		return outcome, fmt.Errorf("versioning manifest: %s has no Bundle-Version in META-INF/MANIFEST.MF", artifactDir)
	}

	exists, err := artifactExists(artifactId, artifactType, packageId, dt, s.ip)
	if err != nil {
		return outcome, err
	}
	designtimeBefore := ""
	contentChanged := false

	if !exists && s.DryRun {
		outcome.Action, outcome.DryRun = "CREATED", true
		outcome.Version, outcome.VersionRule, outcome.VersionReason = s.plannedVersion(artifactId, repoVersion, "", true)
		log.Info().Msgf("[PLAN] %v would be created (version %v)", artifactId, orNone(outcome.Version))
		return outcome, nil
	}
	if !exists {
		log.Info().Msgf("Artifact %v will be created", artifactId)
		if artifactType == "Integration" {
			err = file.UpdateBPMN(artifactDir, scriptMap)
			if err != nil {
				return outcome, err
			}
		}

		err = prepareUploadDir(workDir, artifactDir, dt)
		if err != nil {
			return outcome, err
		}

		err = createArtifact(artifactId, artifactName, packageId, workDir+"/upload", dt)
		if err != nil {
			return outcome, err
		}

		outcome.Action = "CREATED"
		contentChanged = true
		log.Info().Msg("🏆 Designtime artifact created successfully")
	} else {
		log.Info().Msg("Checking if designtime artifact needs to be updated")
		var changesFound bool
		designtimeBefore, changesFound, err = s.compareWithTenant(artifactId, artifactType, artifactDir, workDir, repoVersion, scriptMap, dt, &outcome)
		if err != nil {
			return outcome, err
		}

		if changesFound && s.Versioning == versioning.Manifest {
			// same version, different content: refuse before anything is written
			running := ""
			if rt, err := cpi.NewRuntime(s.exe).GetArtifact(artifactId); err != nil {
				return outcome, err
			} else if rt != nil {
				running = rt.Version
			}
			if repoVersion == designtimeBefore || repoVersion == running {
				outcome.VersionRule = versioning.RuleGuard
				return outcome, fmt.Errorf("versioning manifest: the content of %v differs from the tenant's, but Bundle-Version %v equals the tenant's version (designtime %v, running %v): raise Bundle-Version in the repository (cpictl version bump --changed)",
					artifactId, repoVersion, designtimeBefore, orNone(running))
			}
		}

		if s.DryRun {
			outcome.Action, outcome.DryRun = map[bool]string{true: "UPDATED", false: "UNCHANGED"}[changesFound], true
			outcome.Version, outcome.VersionRule, outcome.VersionReason = s.plannedVersion(artifactId, repoVersion, designtimeBefore, changesFound)
			log.Info().Msgf("[PLAN] %v: %v (version %v)", artifactId, map[bool]string{true: "would be updated", false: "unchanged"}[changesFound], orNone(outcome.Version))
			return outcome, nil
		}
		if changesFound {
			log.Info().Msg("Changes found in designtime artifact. Designtime artifact will be updated in CPI tenant")
			err = prepareUploadDir(workDir, artifactDir, dt)
			if err != nil {
				return outcome, err
			}
			err = updateArtifact(artifactId, artifactName, packageId, workDir+"/upload", dt)
			if err != nil {
				return outcome, err
			}
			outcome.Action = "UPDATED"
			contentChanged = true
			log.Info().Msg("🏆 Designtime artifact updated successfully")
		} else {
			log.Info().Msg("🏆 No changes detected. Designtime artifact does not need to be updated")
		}
	}

	if err := s.applyVersioning(artifactType, artifactId, repoVersion, designtimeBefore, contentChanged, &outcome); err != nil {
		return outcome, err
	}

	if exists && contentChanged {
		r := cpi.NewRuntime(s.exe)
		runtimeVersion, _, err := r.Get(artifactId)
		if err != nil {
			return outcome, err
		}
		if runtimeVersion == outcome.Version {
			if s.Versioning == versioning.Manifest {
				log.Warn().Msgf("Content of %v changed but Bundle-Version %v equals the running version: run 'cpictl version bump --changed' before promoting", artifactId, outcome.Version)
			}
			if s.KeepRuntime {
				log.Info().Msgf("Content of %v changed with the running version %v: to be deployed again (the runtime keeps running until then)", artifactId, outcome.Version)
				outcome.Redeploy = true
				return outcome, s.afterUpload(exists, artifactType, artifactId, parametersFile, &outcome)
			}
			log.Info().Msg("Undeploying existing runtime artifact with same version number due to changes in design")
			err = r.UnDeploy(artifactId)
			if err != nil {
				return outcome, err
			}
			outcome.RuntimeUndeployed = true
		}
	}

	return outcome, s.afterUpload(exists, artifactType, artifactId, parametersFile, &outcome)
}

// afterUpload updates the configured parameters from parametersFile.
func (s *Synchroniser) afterUpload(exists bool, artifactType, artifactId, parametersFile string, outcome *UploadOutcome) error {
	if exists && artifactType == "Integration" && file.Exists(parametersFile) {
		log.Info().Msg("Updating configured parameter(s) of Integration designtime artifact where necessary")
		redeploy, err := updateConfiguration(artifactId, parametersFile, s.exe, s.KeepRuntime)
		if redeploy {
			outcome.Redeploy = true
		}
		return err
	}
	return nil
}

// applyVersioning sets the designtime version according to s.Versioning and
// records version and reason in outcome.
func (s *Synchroniser) applyVersioning(artifactType, artifactId, repoVersion, designtimeBefore string, contentChanged bool, outcome *UploadOutcome) error {
	current, _, _, err := cpi.NewDesigntimeArtifact(artifactType, s.exe).Get(artifactId, "active")
	if err != nil {
		return err
	}
	outcome.Version = current
	setVersion := func(version, reason string) error {
		if version != current {
			lower := versioning.Compare(version, current) < 0
			if lower {
				log.Warn().Str("artifact", artifactId).Msgf("Lowering the designtime version of %v from %v to %v", artifactId, current, version)
			}
			if err := cpi.SaveAsVersion(s.exe, artifactType, artifactId, version); err != nil {
				outcome.VersionRule = versioning.RuleGuard
				if lower {
					return fmt.Errorf("versioning %s: the tenant refused to set %v to %v, below its designtime version %v: raise Bundle-Version above %v: %w",
						s.Versioning, artifactId, version, current, current, err)
				}
				return fmt.Errorf("versioning %s: cannot set %v to %v: %w", s.Versioning, artifactId, version, err)
			}
			// verify: the deployment must use exactly this version
			got, _, _, err := cpi.NewDesigntimeArtifact(artifactType, s.exe).Get(artifactId, "active")
			if err != nil {
				return err
			}
			if got != version {
				outcome.VersionRule = versioning.RuleGuard
				return fmt.Errorf("versioning %s: %v was saved as version %v but the tenant reports %v%s", s.Versioning, artifactId, version, got,
					map[bool]string{true: " (the tenant may not allow a version below the current one)", false: ""}[lower])
			}
			outcome.VersionSet = true
		}
		outcome.Version, outcome.VersionReason = version, reason
		return nil
	}

	switch s.Versioning {
	case versioning.Manifest:
		outcome.VersionRule = versioning.RuleManifest
		reason := "manifest: Bundle-Version of the repository"
		if current != repoVersion {
			reason += fmt.Sprintf(" (tenant had %s)", current)
		}
		if err := setVersion(repoVersion, reason); err != nil {
			return err
		}
	case versioning.TenantBump:
		outcome.VersionRule = versioning.RuleBump
		if !contentChanged {
			outcome.VersionReason = "tenant-bump: content unchanged, version kept"
			break
		}
		running := ""
		if rt, err := cpi.NewRuntime(s.exe).GetArtifact(artifactId); err != nil {
			return err
		} else if rt != nil {
			running = rt.Version
		}
		base := designtimeBefore
		if base == "" {
			base = current // created: the version the tenant gave it
		}
		highest := versioning.Max(base, running)
		next, err := versioning.Bump(highest, "patch")
		if err != nil {
			return fmt.Errorf("versioning tenant-bump: %w", err)
		}
		if err := setVersion(next, fmt.Sprintf("tenant-bump: max(designtime %s, runtime %s)+1", base, orNone(running))); err != nil {
			return err
		}
	case versioning.Keep:
		outcome.VersionRule, outcome.VersionReason = versioning.RuleKeep, "keep: the tenant's version"
	default:
		outcome.VersionRule, outcome.VersionReason = versioning.RuleTenant, "no versioning mode: the tenant's version"
	}
	log.Info().Str("artifact", artifactId).Str("version", outcome.Version).Str("rule", outcome.VersionRule).
		Msgf("Version of %v: %v (rule %v: %v)", artifactId, outcome.Version, outcome.VersionRule, outcome.VersionReason)
	return nil
}

// plannedVersion is the designtime version an upload would leave (DryRun):
// the rules of applyVersioning without writing. Empty: assigned by the
// tenant (a created artifact without a versioning mode).
func (s *Synchroniser) plannedVersion(artifactId, repoVersion, designtime string, changed bool) (version, rule, reason string) {
	switch s.Versioning {
	case versioning.Manifest:
		return repoVersion, versioning.RuleManifest, "manifest: Bundle-Version of the repository"
	case versioning.TenantBump:
		if !changed {
			return designtime, versioning.RuleBump, "tenant-bump: content unchanged, version kept"
		}
		running := ""
		if rt, err := cpi.NewRuntime(s.exe).GetArtifact(artifactId); err == nil && rt != nil {
			running = rt.Version
		}
		highest := versioning.Max(designtime, running)
		if highest == "" {
			return "", versioning.RuleBump, "tenant-bump: assigned by the tenant on creation, then raised"
		}
		next, err := versioning.Bump(highest, "patch")
		if err != nil {
			return "", versioning.RuleBump, err.Error()
		}
		return next, versioning.RuleBump, fmt.Sprintf("tenant-bump: max(designtime %s, runtime %s)+1", orNone(designtime), orNone(running))
	case versioning.Keep:
		return designtime, versioning.RuleKeep, "keep: the tenant's version"
	}
	return designtime, versioning.RuleTenant, "no versioning mode: the tenant's version"
}

func orNone(v string) string {
	if v == "" {
		return "none"
	}
	return v
}

// DraftError is returned when the tenant's designtime artifact is in draft:
// someone is editing it in the Web UI, so it is neither overwritten nor
// deployed.
type DraftError struct {
	ID, Package string
}

func (e *DraftError) Error() string {
	return fmt.Sprintf("Artifact %v is in Draft state. Save Version of artifact in Web UI first!", e.ID)
}

// IsDraft reports whether err says the artifact is in draft on the tenant.
func IsDraft(err error) bool {
	var d *DraftError
	return errors.As(err, &d)
}

func artifactExists(artifactId string, artifactType string, packageId string, dt cpi.DesigntimeArtifact, ip *cpi.IntegrationPackage) (bool, error) {
	_, _, exists, err := dt.Get(artifactId, "active")
	if err != nil {
		return false, err
	}
	if exists {
		log.Info().Msgf("Active version of artifact %v exists", artifactId)
		//  Check if version is in draft mode
		var details []*cpi.ArtifactDetails
		details, err = ip.GetArtifactsData(packageId, artifactType)
		if err != nil {
			return false, err
		}
		artifact := cpi.FindArtifactById(artifactId, details)
		if artifact == nil {
			return false, fmt.Errorf("Artifact %v not found in package %v", artifactId, packageId)
		}
		if artifact.IsDraft {
			return false, &DraftError{ID: artifactId, Package: packageId}
		}
		return true, nil
	} else {
		log.Info().Msgf("Active version of artifact %v does not exist", artifactId)
		return false, nil
	}
}

func prepareUploadDir(workDir string, artifactDir string, dt cpi.DesigntimeArtifact) error {
	// Clean up previous uploads
	uploadDir := workDir + "/upload"
	err := os.RemoveAll(uploadDir)
	if err != nil {
		return errors.Wrap(err, 0)
	}
	return dt.CopyContent(artifactDir, uploadDir)
}

func createArtifact(artifactId string, artifactName string, packageId string, artifactDir string, dt cpi.DesigntimeArtifact) error {
	err := dt.Create(artifactId, artifactName, packageId, artifactDir)
	if err != nil {
		return err
	}
	return nil
}

func updateArtifact(artifactId string, artifactName string, packageId string, artifactDir string, dt cpi.DesigntimeArtifact) error {
	err := dt.Update(artifactId, artifactName, packageId, artifactDir)
	if err != nil {
		return err
	}
	return nil
}

// compareWithTenant reports the designtime version and whether the local
// content differs from the tenant's: against the Baseline when it covers the
// artifact and the tenant did not change since, else by downloading it.
func (s *Synchroniser) compareWithTenant(artifactId, artifactType, artifactDir, workDir, repoVersion string, scriptMap []string,
	dt cpi.DesigntimeArtifact, outcome *UploadOutcome) (designtime string, changed bool, err error) {
	reason := ""
	switch {
	case s.Baseline == nil:
	case s.VerifyDownload:
		reason = "--verify-download"
	case len(scriptMap) > 0:
		reason = "script collection mapping"
	default:
		info, exists, err := cpi.GetDesigntimeInfo(s.exe, artifactType, artifactId, "active")
		if err != nil {
			return "", false, err
		}
		base, ok := s.Baseline.Artifact(artifactId)
		switch {
		case !exists:
			reason = "not found on the tenant"
		case !ok || base.UploadHash == "":
			reason = "not in the snapshot state"
		case info.ModifiedAt.IsZero() || base.ModifiedAt.IsZero():
			reason = "the tenant reports no modification time"
		case base.Version != info.Version || !base.ModifiedAt.Equal(info.ModifiedAt.UTC()):
			reason = fmt.Sprintf("changed on the tenant since the snapshot (version %v, modified %v; snapshot %v, %v)",
				info.Version, info.ModifiedAt.UTC().Format(time.RFC3339), base.Version, base.ModifiedAt.Format(time.RFC3339))
		default:
			if artifactType == "Integration" {
				if err := file.UpdateBPMN(artifactDir, scriptMap); err != nil {
					return "", false, err
				}
			}
			local, err := file.UploadHash(os.DirFS(artifactDir), artifactType)
			if err != nil {
				return "", false, err
			}
			outcome.Compared = "snapshot"
			changed = local != base.UploadHash
			log.Info().Str("artifact", artifactId).Msgf("Compared with the snapshot (tenant unchanged since: version %v, modified %v): content %v",
				info.Version, base.ModifiedAt.Format(time.RFC3339), map[bool]string{true: "changed", false: "unchanged"}[changed])
			return info.Version, changed, nil
		}
		log.Info().Str("artifact", artifactId).Msgf("Downloading %v for the comparison: %v", artifactId, reason)
	}

	outcome.Compared, outcome.CompareReason = "download", reason
	designtime, _, _, err = dt.Get(artifactId, "active")
	if err != nil {
		return "", false, err
	}
	zipFile := fmt.Sprintf("%v/%v.zip", workDir, artifactId)
	if err := dt.Download(zipFile, artifactId); err != nil {
		return "", false, err
	}
	changed, err = compareArtifactContents(workDir, zipFile, artifactDir, repoVersion, scriptMap, dt)
	return designtime, changed, err
}

// compareArtifactContents compares the local directory with the downloaded
// artifact. Bundle-Version is not compared: the download does not carry the
// designtime version, and versions are handled by the versioning mode.
func compareArtifactContents(workDir string, zipFile string, artifactDir string, repoVersion string, scriptMap []string, dt cpi.DesigntimeArtifact) (bool, error) {
	tgtDir := fmt.Sprintf("%v/download", workDir)
	err := os.RemoveAll(tgtDir)
	if err != nil {
		return false, errors.Wrap(err, 0)
	}

	log.Info().Msgf("Unzipping downloaded designtime artifact %v to %v/download", zipFile, workDir)
	err = file.UnzipSource(zipFile, tgtDir)
	if err != nil {
		return false, err
	}
	if repoVersion != "" {
		if err := manifest.SetVersion(tgtDir, repoVersion); err != nil && !os.IsNotExist(err) {
			return false, err
		}
	}

	return dt.CompareContent(artifactDir, tgtDir, scriptMap, "tenant")
}

// updateConfiguration sets the configured parameters that parametersFile
// changes. A deployed artifact with changed parameters is undeployed, or
// with keepRuntime reported as to be deployed again (redeploy).
func updateConfiguration(artifactId string, parametersFile string, exe *httpclnt.HTTPExecuter, keepRuntime bool) (redeploy bool, err error) {
	// Get configured parameters from tenant
	c := cpi.NewConfiguration(exe)
	tenantParameters, err := c.Get(artifactId, "active")
	if err != nil {
		return false, err
	}

	// Get parameters from parameters.prop file
	log.Info().Msgf("Getting parameters from %v file", parametersFile)
	fileParameters := properties.MustLoadFile(parametersFile, properties.UTF8).Map()

	log.Info().Msg("Comparing parameters and updating where necessary")
	atLeastOneUpdated := false
	for _, result := range tenantParameters.Root.Results {
		if result.DataType != "custom:schedule" { // TODO - handle translation to Cron
			// Skip updating for schedulers which require translation to Cron values
			fileValue := fileParameters[result.ParameterKey]
			if fileValue != "" && fileValue != result.ParameterValue {
				log.Info().Msgf("Parameter %v to be updated from %v to %v", result.ParameterKey, result.ParameterValue, fileValue)
				err = c.Update(artifactId, "active", result.ParameterKey, fileValue)
				if err != nil {
					return false, err
				}
				atLeastOneUpdated = true
			}
		}
	}
	if atLeastOneUpdated {
		r := cpi.NewRuntime(exe)
		version, _, err := r.Get(artifactId)
		if err != nil {
			return false, err
		}
		if version == "NOT_DEPLOYED" {
			log.Info().Msg("🏆 No existing runtime artifact deployed")
		} else if keepRuntime {
			log.Info().Msgf("Configured parameters of %v changed: to be deployed again (the runtime keeps running until then)", artifactId)
			return true, nil
		} else {
			log.Info().Msg("🏆 Undeploying existing runtime artifact due to changes in configured parameters")
			err = r.UnDeploy(artifactId)
			if err != nil {
				return false, err
			}
		}
	} else {
		log.Info().Msg("🏆 No updates required for configured parameters")
	}
	return false, nil
}
