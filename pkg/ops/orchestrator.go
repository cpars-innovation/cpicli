package ops

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	gosync "sync"
	"time"

	"github.com/cpars-innovation/cpicli/internal/deploy"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/internal/sync"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/manifest"
	"github.com/cpars-innovation/cpicli/pkg/models"
	"github.com/cpars-innovation/cpicli/pkg/snapshot"
	"github.com/cpars-innovation/cpicli/pkg/versioning"
	"github.com/rs/zerolog/log"
)

// DeployConfig is a deploy config of the orchestrator (docs/orchestrator.md).
type DeployConfig = models.DeployConfig

// LoadDeployConfig reads a deploy config: a file, a folder (every *.yml and
// *.yaml file, merged as with orchestrator --merge-configs: each file's
// deploymentPrefix is applied to its package and artifact IDs) or an http(s)
// URL (public, no authentication).
func LoadDeployConfig(path string) (*DeployConfig, error) {
	files, err := loadDeployConfigFiles(path)
	if err != nil {
		return nil, err
	}
	if len(files) == 1 {
		return files[0].Config, nil
	}
	return deploy.MergeConfigs(files)
}

func loadDeployConfigFiles(path string) ([]*deploy.DeployConfigFile, error) {
	loader := deploy.NewConfigLoader()
	if err := loader.DetectSource(path); err != nil {
		return nil, fmt.Errorf("deploy config %s: %w", path, err)
	}
	files, err := loader.LoadConfigs()
	if err != nil {
		return nil, fmt.Errorf("deploy config %s: %w", path, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("deploy config %s: no config files", path)
	}
	return files, nil
}

// OrchestratorMode is what the orchestrator does.
type OrchestratorMode string

// Orchestrator modes.
const (
	OrchestratorUpdateAndDeploy OrchestratorMode = "update-and-deploy"
	OrchestratorUpdateOnly      OrchestratorMode = "update-only"
	OrchestratorDeployOnly      OrchestratorMode = "deploy-only"
)

// Draft handling of the orchestrator: artifacts in draft on the tenant are
// skipped (not uploaded, not deployed, reported) or fail the run.
const (
	DraftHandlingSkip  = "SKIP"
	DraftHandlingError = "ERROR"
)

// OrchestratorOptions are the settings of an orchestrator run.
type OrchestratorOptions struct {
	// Mode defaults to OrchestratorUpdateAndDeploy.
	Mode OrchestratorMode
	// DeploymentPrefix overrides the config's deploymentPrefix when set:
	// package IDs get "<prefix>", artifact IDs "<prefix>_".
	DeploymentPrefix string
	// PackageFilter selects packages by ID; ArtifactFilter selects artifacts
	// by artifactId, artifactDir or the folder's source ID
	// (Bundle-SymbolicName), so a folder or source ID selects every ID
	// deployed from it. Empty: all.
	PackageFilter, ArtifactFilter []string
	// Versioning is the global versioning mode (packages and artifacts of
	// the config may override it).
	Versioning versioning.Mode
	// SnapshotState is the snapshot state file of the target tenant
	// (written by snapshot), used instead of downloading existing artifacts
	// for the comparison; "" downloads. With Host set, a state of another
	// tenant is not used.
	SnapshotState string
	Host          string
	// VerifyDownload downloads every existing artifact even when the
	// snapshot state covers it.
	VerifyDownload bool
	// DraftHandling is DraftHandlingSkip (default) or DraftHandlingError.
	DraftHandling string
	// Parallel is the number of uploads at the same time (default 8).
	Parallel int
	// WorkDir holds the prepared copies of the artifacts; "" uses a
	// temporary directory removed by Close.
	WorkDir string
	// Plan writes nothing to the tenant and collects what would be done
	// (see Orchestrator.Plan).
	Plan bool
}

// Artifact statuses of an orchestrator run (OrchestratorArtifact.Status).
const (
	ArtifactCreated      = "created"
	ArtifactUpdated      = "updated"
	ArtifactUnchanged    = "unchanged"
	ArtifactFailed       = "failed"
	ArtifactSkippedDraft = "skipped-draft"
	ArtifactNotUploaded  = "not-uploaded" // deploy only, or sync: false
)

// OrchestratorStats are the counters of an orchestrator run.
type OrchestratorStats struct {
	PackagesUpdated          int `json:"packagesUpdated"`
	PackagesDeployed         int `json:"packagesDeployed"`
	PackagesFailed           int `json:"packagesFailed"`
	PackagesFiltered         int `json:"packagesFiltered"`
	ArtifactsTotal           int `json:"artifactsTotal"`
	ArtifactsDeployedSuccess int `json:"artifactsDeployedSuccess"`
	ArtifactsDeployedFailed  int `json:"artifactsDeployedFailed"`
	ArtifactsFiltered        int `json:"artifactsFiltered"`
	UpdateFailures           int `json:"updateFailures"`
	DeployFailures           int `json:"deployFailures"`
	// ArtifactsChanged were created or updated, ArtifactsUnchanged had the
	// tenant's content already. Existing artifacts were compared with the
	// snapshot state (ComparedWithSnapshot) or downloaded (DownloadedForComparison).
	ArtifactsChanged        int `json:"artifactsChanged"`
	ArtifactsUnchanged      int `json:"artifactsUnchanged"`
	ComparedWithSnapshot    int `json:"comparedWithSnapshot"`
	DownloadedForComparison int `json:"downloadedForComparison"`
	// ArtifactsSkippedDraft were in draft on the tenant (draftHandling SKIP):
	// not uploaded, not deployed; SkippedDrafts lists them.
	ArtifactsSkippedDraft     int             `json:"artifactsSkippedDraft"`
	SkippedDrafts             []DraftSkip     `json:"skippedDrafts"`
	SuccessfulPackageUpdates  map[string]bool `json:"successfulPackageUpdates"`
	SuccessfulArtifactUpdates map[string]bool `json:"successfulArtifactUpdates"`
	SuccessfulArtifactDeploys map[string]bool `json:"successfulArtifactDeploys"`
	FailedPackageUpdates      map[string]bool `json:"failedPackageUpdates"`
	FailedArtifactUpdates     map[string]bool `json:"failedArtifactUpdates"`
	FailedArtifactDeploys     map[string]bool `json:"failedArtifactDeploys"`
	// changed and redeploy are keyed by the final artifact ID: content
	// created or updated, and content changed with the running version (to
	// be deployed with force; the runtime was not undeployed).
	changed, redeploy map[string]bool
	// skippedDraft and draftFailed (draftHandling ERROR) are keyed by the
	// final artifact ID; artifacts holds the status of each.
	skippedDraft map[string]bool
	draftFailed  int
	// uploaded holds the final artifact IDs the upload phase handled
	// (also in a plan).
	uploaded  map[string]bool
	artifacts map[string]*OrchestratorArtifact
}

// NewOrchestratorStats returns empty statistics.
func NewOrchestratorStats() *OrchestratorStats {
	return &OrchestratorStats{
		SuccessfulArtifactUpdates: map[string]bool{},
		SuccessfulPackageUpdates:  map[string]bool{},
		SuccessfulArtifactDeploys: map[string]bool{},
		FailedArtifactUpdates:     map[string]bool{},
		FailedPackageUpdates:      map[string]bool{},
		FailedArtifactDeploys:     map[string]bool{},
		changed:                   map[string]bool{},
		redeploy:                  map[string]bool{},
		skippedDraft:              map[string]bool{},
		uploaded:                  map[string]bool{},
		artifacts:                 map[string]*OrchestratorArtifact{},
		SkippedDrafts:             []DraftSkip{},
	}
}

// Changed tells whether the upload created or updated an artifact ID.
func (s *OrchestratorStats) Changed(id string) bool { return s.changed[id] }

// Redeploy tells whether an artifact ID's content changed with the running
// version: it must be deployed with force.
func (s *OrchestratorStats) Redeploy(id string) bool { return s.redeploy[id] }

// DraftFailures is the number of artifacts in draft that failed the run
// (DraftHandlingError).
func (s *OrchestratorStats) DraftFailures() int { return s.draftFailed }

// Artifacts returns the status of each artifact ID, by package and ID.
func (s *OrchestratorStats) Artifacts() []OrchestratorArtifact {
	out := make([]OrchestratorArtifact, 0, len(s.artifacts))
	for _, a := range s.artifacts {
		out = append(out, *a)
	}
	slices.SortFunc(out, func(a, b OrchestratorArtifact) int {
		return cmp.Or(cmp.Compare(a.Package, b.Package), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// Err is the error of a run with failures (partial when anything
// succeeded), nil without failures.
func (s *OrchestratorStats) Err() error {
	if s.draftFailed > 0 {
		return output.Failed(fmt.Errorf("%d artifact(s) in draft on the tenant (--draft-handling ERROR)", s.draftFailed))
	}
	if s.PackagesFailed > 0 || s.UpdateFailures > 0 || s.DeployFailures > 0 {
		err := fmt.Errorf("deployment completed with failures")
		if len(s.SuccessfulArtifactUpdates) > 0 || s.ArtifactsDeployedSuccess > 0 {
			return output.Partial(err)
		}
		return output.Failed(err)
	}
	return nil
}

// status returns the entry of an artifact ID (callers hold the lock).
func (s *OrchestratorStats) status(id, pkg, source string) *OrchestratorArtifact {
	if s.artifacts == nil {
		s.artifacts = map[string]*OrchestratorArtifact{}
	}
	a := s.artifacts[id]
	if a == nil {
		a = &OrchestratorArtifact{ID: id, Package: pkg, Source: source}
		s.artifacts[id] = a
	}
	return a
}

// DraftSkip is an artifact left alone because it is in draft on the tenant.
type DraftSkip struct {
	ID      string `json:"id"`
	Package string `json:"package"`
	// Designtime is what the tenant reports for the draft ("Active"),
	// Running the deployed version, LocalVersion the repository's
	// Bundle-Version.
	Designtime   string `json:"designtime,omitempty"`
	Running      string `json:"running,omitempty"`
	LocalVersion string `json:"localVersion,omitempty"`
}

// OrchestratorArtifact is the outcome for one deployed artifact ID.
type OrchestratorArtifact struct {
	ID      string `json:"id"`
	Package string `json:"package"`
	// Source is the artifact folder (for deployment copies and prefixes).
	Source string `json:"source,omitempty"`
	// Status is one of the Artifact* statuses.
	Status       string `json:"status"`
	Designtime   string `json:"designtime,omitempty"`
	LocalVersion string `json:"localVersion,omitempty"`
	// Deploy is the deployment result (DEPLOYED, SKIPPED, FAILED, ...);
	// empty when not deployed.
	Deploy string `json:"deploy,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// DeploymentTask is an artifact ready for deployment (orchestrator,
// configure, deferred deployments).
type DeploymentTask struct {
	ArtifactID string
	// ArtifactType is the type of the deploy or configure config
	// (IntegrationFlow, ValueMapping, ...; see DesigntimeType).
	ArtifactType string
	PackageID    string
	DisplayName  string
	// Force deploys even when the runtime already has the designtime
	// version (configuration changes do not change the version).
	Force bool
	// AllowDowngrade, ModifiedAt and Versioning are passed to Artifact.
	AllowDowngrade bool
	ModifiedAt     *time.Time
	Versioning     versioning.Mode
	// ExpectedVersion is the repository's Bundle-Version (versioning
	// manifest): the deployment refuses any other designtime version.
	ExpectedVersion string
}

// PlanItem is what a run would do with one artifact (orchestrator --plan,
// configure --dry-run, deploy --plan).
type PlanItem struct {
	Package  string `json:"package,omitempty"`
	Artifact string `json:"artifact"`
	Type     string `json:"type,omitempty"`
	// Upload is create, update, unchanged, fails (see error), skipped-draft
	// or empty (not synchronised).
	Upload   string `json:"upload,omitempty"`
	Compared string `json:"compared,omitempty"`
	// Designtime is the designtime version after the upload.
	Designtime    string `json:"designtime,omitempty"`
	VersionRule   string `json:"versionRule,omitempty"`
	Running       string `json:"running,omitempty"`
	RuntimeStatus string `json:"runtimeStatus,omitempty"`
	Deploy        bool   `json:"deploy"`
	Reason        string `json:"reason,omitempty"`
	Error         string `json:"error,omitempty"`
}

type planCollector struct {
	items []PlanItem
	index map[string]int
}

func newPlanCollector() *planCollector { return &planCollector{index: map[string]int{}} }

func (p *planCollector) upsert(id string, f func(*PlanItem)) {
	i, ok := p.index[id]
	if !ok {
		i = len(p.items)
		p.index[id] = i
		p.items = append(p.items, PlanItem{Artifact: id})
	}
	f(&p.items[i])
}

// PlanDeployments predicts the deployment of each task (read only).
func PlanDeployments(exe *httpclnt.HTTPExecuter, tasks []DeploymentTask) []PlanItem {
	plan := newPlanCollector()
	planDeployments(exe, tasks, plan)
	return plan.items
}

// planDeployments predicts the deployment of each task into the plan.
func planDeployments(exe *httpclnt.HTTPExecuter, tasks []DeploymentTask, plan *planCollector) {
	rt := cpi.NewRuntime(exe)
	for _, t := range tasks {
		plan.upsert(t.ArtifactID, func(it *PlanItem) {
			it.Package = cmp.Or(it.Package, t.PackageID)
			if it.Upload == "fails" {
				it.Reason = "not deployed: the upload fails"
				return
			}
			version := it.Designtime
			if it.Upload == "" { // not synchronised: the tenant's designtime version
				typ := DesigntimeType(t.ArtifactType)
				it.Type = cmp.Or(it.Type, typ)
				v, _, exists, err := cpi.NewDesigntimeArtifact(typ, exe).Get(t.ArtifactID, "active")
				if err != nil {
					it.Error = err.Error()
					return
				}
				if !exists {
					it.Error = "designtime artifact does not exist"
					return
				}
				version = v
				it.Designtime = v
			}
			if IsDraftVersion(version) {
				it.Deploy, it.Reason = false, DraftReason
				return
			}
			running, err := rt.GetArtifact(t.ArtifactID)
			if err != nil {
				it.Error = err.Error()
				return
			}
			if running != nil {
				it.Running, it.RuntimeStatus = running.Version, running.Status
			}
			changed := it.Upload == "create" || it.Upload == "update"
			var refused bool
			it.Deploy, it.Reason, refused = DeployDecision(running, version, changed, t.Versioning, t.AllowDowngrade)
			if refused {
				it.Error = it.Reason
			} else if t.Force {
				it.Deploy, it.Reason = true, "configuration changed: deployed again"
			} else if t.ExpectedVersion != "" && version != "" && version != t.ExpectedVersion {
				it.Deploy, it.Error = false, fmt.Sprintf("refused: designtime %s is not the repository's Bundle-Version %s", version, t.ExpectedVersion)
			}
		})
	}
}

// DesigntimeType maps an artifact type of a deploy or configure config
// (IntegrationFlow, "Value Mapping", ...) to the designtime type
// (Integration, ValueMapping, MessageMapping, ScriptCollection); unknown
// types are integration flows.
func DesigntimeType(artifactType string) string {
	switch strings.ToLower(artifactType) {
	case "integrationflow", "integration flow", "iflow":
		return "Integration"
	case "valuemapping", "value mapping":
		return "ValueMapping"
	case "messagemapping", "message mapping":
		return "MessageMapping"
	case "scriptcollection", "script collection":
		return "ScriptCollection"
	default:
		return "Integration"
	}
}

// PlanOrchestration is orchestrator --plan: what a run of the deploy config
// would upload and deploy, and why, per artifact. dir is the packages
// directory (packageDir of the config is relative to it). Nothing is written
// to the tenant; artifacts that would fail have Error set.
func PlanOrchestration(ctx context.Context, exe *httpclnt.HTTPExecuter, dir string, cfg *DeployConfig, o OrchestratorOptions) ([]PlanItem, error) {
	o.Plan = true
	orc, err := NewOrchestrator(exe, dir, o)
	if err != nil {
		return nil, err
	}
	defer orc.Close()
	tasks, err := orc.Process(ctx, cfg)
	if err != nil {
		return nil, err
	}
	orc.PlanDeployments(tasks)
	return orc.Plan(), nil
}

// Orchestrator runs deploy configs (cpictl orchestrator): Process uploads
// the artifacts (or plans the uploads) and returns the deployment tasks,
// which the caller deploys (Deploy) or plans (PlanDeployments).
type Orchestrator struct {
	exe  *httpclnt.HTTPExecuter
	dir  string
	opts OrchestratorOptions
	// Stats are the counters of the run.
	Stats    *OrchestratorStats
	baseline *snapshot.State
	plan     *planCollector
	drafts   *draftChecker
	workDir  string
	tempDir  string
	// slots limits the uploads running at the same time (Parallel), wg
	// waits for them, mu guards the statistics and the plan.
	slots chan struct{}
	wg    gosync.WaitGroup
	mu    gosync.Mutex
}

// NewOrchestrator prepares a run over the packages directory dir.
func NewOrchestrator(exe *httpclnt.HTTPExecuter, dir string, o OrchestratorOptions) (*Orchestrator, error) {
	o.Mode = cmp.Or(o.Mode, OrchestratorUpdateAndDeploy)
	switch o.Mode {
	case OrchestratorUpdateAndDeploy, OrchestratorUpdateOnly, OrchestratorDeployOnly:
	default:
		return nil, output.Usagef("orchestrator mode %q: update-and-deploy, update-only or deploy-only", o.Mode)
	}
	o.DraftHandling = strings.ToUpper(cmp.Or(o.DraftHandling, DraftHandlingSkip))
	if o.DraftHandling != DraftHandlingSkip && o.DraftHandling != DraftHandlingError {
		return nil, output.Usagef("draft handling %q: SKIP or ERROR", o.DraftHandling)
	}
	if o.Parallel == 0 {
		o.Parallel = 8
	}
	if o.Parallel < 1 {
		return nil, output.Usagef("parallel must be at least 1")
	}
	if err := deploy.ValidateDeploymentPrefix(o.DeploymentPrefix); err != nil {
		return nil, output.Usage(err)
	}
	orc := &Orchestrator{exe: exe, dir: dir, opts: o, Stats: NewOrchestratorStats(), drafts: newDraftChecker(exe),
		slots: make(chan struct{}, o.Parallel), workDir: o.WorkDir}
	if o.Plan {
		orc.plan = newPlanCollector()
	}
	if o.Mode != OrchestratorDeployOnly {
		if o.SnapshotState != "" {
			state, err := snapshot.LoadState(o.SnapshotState)
			if err != nil {
				return nil, output.Usagef("cannot read the snapshot state %s: %v", o.SnapshotState, err)
			}
			if tenant := cpi.TenantID(o.Host); o.Host != "" && state.Tenant != tenant {
				log.Warn().Msgf("Snapshot state %s is of tenant %q, not %s: existing artifacts are downloaded for the comparison", o.SnapshotState, state.Tenant, tenant)
			} else {
				log.Info().Msgf("Comparing with the snapshot state %s (%d artifacts) instead of downloading", o.SnapshotState, len(state.Artifacts))
				orc.baseline = state
			}
		}
		if orc.workDir == "" {
			tmp, err := os.MkdirTemp("", "cpictl-orchestrator-*")
			if err != nil {
				return nil, fmt.Errorf("failed to create temp directory: %w", err)
			}
			orc.workDir, orc.tempDir = tmp, tmp
		}
	}
	return orc, nil
}

// Close removes the temporary work directory.
func (orc *Orchestrator) Close() error {
	if orc.tempDir == "" {
		return nil
	}
	return os.RemoveAll(orc.tempDir)
}

// Plan returns what would be done (Plan option), in the order of the
// artifacts.
func (orc *Orchestrator) Plan() []PlanItem {
	if orc.plan == nil {
		return nil
	}
	return orc.plan.items
}

// PlanDeployments predicts the deployment of the tasks into the plan (read
// only); without the Plan option or in update-only mode it does nothing.
func (orc *Orchestrator) PlanDeployments(tasks []DeploymentTask) {
	if orc.plan == nil || orc.opts.Mode == OrchestratorUpdateOnly {
		return
	}
	planDeployments(orc.exe, tasks, orc.plan)
}

// RecordDeployments counts the results of deploying the tasks.
func (orc *Orchestrator) RecordDeployments(results []Result) {
	stats := orc.Stats
	failedByPackage := make(map[string]int)
	var packageOrder []string
	for _, r := range results {
		if _, seen := failedByPackage[r.PackageID]; !seen {
			packageOrder = append(packageOrder, r.PackageID)
			failedByPackage[r.PackageID] = 0
		}
		if st := stats.artifacts[r.ID]; st != nil {
			st.Deploy = string(r.Status)
		}
		if r.Status.Succeeded() {
			stats.ArtifactsDeployedSuccess++
			stats.SuccessfulArtifactDeploys[r.ID] = true
		} else {
			stats.ArtifactsDeployedFailed++
			stats.DeployFailures++
			stats.FailedArtifactDeploys[r.ID] = true
			failedByPackage[r.PackageID]++
		}
	}
	for _, packageID := range packageOrder {
		if failedByPackage[packageID] == 0 {
			stats.PackagesDeployed++
		} else {
			log.Warn().Msgf("⚠ Package %s: %d artifact deployment(s) failed", packageID, failedByPackage[packageID])
			stats.PackagesFailed++
		}
	}
}

func (orc *Orchestrator) locked(f func()) {
	orc.mu.Lock()
	defer orc.mu.Unlock()
	f()
}

// Process runs the update phase of a deploy config (uploads, or their plan)
// and returns the deployment tasks (none in update-only mode).
func (orc *Orchestrator) Process(ctx context.Context, config *DeployConfig) ([]DeploymentTask, error) {
	mode := orc.opts.Mode
	prefix := cmp.Or(orc.opts.DeploymentPrefix, config.DeploymentPrefix)
	stats := orc.Stats

	var deploymentTasks []DeploymentTask
	var collect []func()

	// Phase 1: Update all packages and artifacts
	if mode != OrchestratorDeployOnly {
		log.Info().Msg("")
		log.Info().Msg("═══════════════════════════════════════════════════════════════════════")
		log.Info().Msg("PHASE 1: UPDATING ALL PACKAGES AND ARTIFACTS")
		log.Info().Msg("═══════════════════════════════════════════════════════════════════════")
		log.Info().Msg("")
	}

	for _, pkg := range config.Packages {
		if err := ctx.Err(); err != nil {
			orc.wg.Wait()
			return nil, err
		}
		// Apply package filter
		if len(orc.opts.PackageFilter) > 0 && !slices.Contains(orc.opts.PackageFilter, pkg.ID) {
			log.Debug().Msgf("Skipping package %s (filtered)", pkg.ID)
			stats.PackagesFiltered++
			continue
		}

		if !pkg.Sync && !pkg.Deploy {
			log.Info().Msgf("Skipping package %s (sync=false, deploy=false)", pkg.ID)
			continue
		}

		log.Info().Msgf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		log.Info().Msgf("📦 Package: %s", pkg.ID)

		packageDir := filepath.Join(orc.dir, pkg.PackageDir)
		if !deploy.DirExists(packageDir) {
			log.Warn().Msgf("Package directory not found: %s", packageDir)
			continue
		}

		// Calculate final package ID and name
		finalPackageID := pkg.ID
		finalPackageName := cmp.Or(pkg.DisplayName, pkg.ID)
		if prefix != "" {
			finalPackageID = prefix + pkg.ID
			finalPackageName = prefix + " - " + finalPackageName
		}

		log.Info().Msgf("Package ID: %s", finalPackageID)
		log.Info().Msgf("Package Name: %s", finalPackageName)

		// Update package metadata
		if mode != OrchestratorDeployOnly && orc.plan != nil {
			log.Info().Msgf("[PLAN] package %s would be created or updated", finalPackageID)
		} else if mode != OrchestratorDeployOnly {
			if err := orc.updatePackage(&pkg, finalPackageID, finalPackageName); err != nil {
				log.Error().Msgf("Failed to update package %s: %v", pkg.ID, err)
				stats.FailedPackageUpdates[pkg.ID] = true
				stats.PackagesFailed++
				continue
			}
			stats.SuccessfulPackageUpdates[pkg.ID] = true
			stats.PackagesUpdated++
		}

		// Process artifacts for update
		if pkg.Sync && mode != OrchestratorDeployOnly {
			orc.updateArtifacts(ctx, &pkg, packageDir, finalPackageID, prefix)
		}

		// Collect deployment tasks once the uploads are done
		if pkg.Deploy && mode != OrchestratorUpdateOnly {
			pkg, packageDir, finalPackageID := pkg, packageDir, finalPackageID
			collect = append(collect, func() {
				deploymentTasks = append(deploymentTasks, orc.collectDeploymentTasks(&pkg, packageDir, finalPackageID, prefix)...)
			})
		}
	}

	orc.wg.Wait()
	for _, c := range collect {
		c()
	}
	return deploymentTasks, nil
}

func (orc *Orchestrator) updatePackage(pkg *models.Package, finalPackageID, finalPackageName string) error {
	log.Info().Msg("Updating package in tenant...")

	packageJSON := map[string]any{
		"d": map[string]any{
			"Id":          finalPackageID,
			"Name":        finalPackageName,
			"Description": cmp.Or(pkg.Description, finalPackageName),
			"ShortText":   cmp.Or(pkg.ShortText, finalPackageName),
		},
	}
	jsonData, err := json.MarshalIndent(packageJSON, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal package JSON: %w", err)
	}
	packageJSONPath := filepath.Join(orc.workDir, "modified", fmt.Sprintf("package_%s.json", pkg.ID))
	if err := os.MkdirAll(filepath.Dir(packageJSONPath), 0755); err != nil {
		return fmt.Errorf("failed to create package JSON directory: %w", err)
	}
	if err := os.WriteFile(packageJSONPath, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write package JSON: %w", err)
	}

	packageSynchroniser := sync.NewSyncer("tenant", "CPIPackage", orc.exe)
	if err := packageSynchroniser.Exec(sync.Request{PackageFile: packageJSONPath}); err != nil {
		log.Warn().Msgf("Package update warning (may not exist yet): %v", err)
		// Don't return error - package might not exist yet
		return nil
	}

	log.Info().Msg("  ✓ Package metadata updated")
	return nil
}

func (orc *Orchestrator) updateArtifacts(ctx context.Context, pkg *models.Package, packageDir, finalPackageID, prefix string) {
	stats := orc.Stats
	log.Info().Msg("Updating artifacts...")

	for _, artifact := range pkg.Artifacts {
		if ctx.Err() != nil {
			return
		}
		if !artifactSelected(&artifact, packageDir, orc.opts.ArtifactFilter) {
			log.Debug().Msgf("Skipping artifact %s (filtered)", artifact.Id)
			orc.locked(func() { stats.ArtifactsFiltered++ })
			continue
		}

		if !artifact.Sync {
			log.Debug().Msgf("Skipping artifact %s (sync=false)", artifact.DisplayName)
			continue
		}

		orc.locked(func() { stats.ArtifactsTotal++ })

		artifactDir := filepath.Join(packageDir, artifact.ArtifactDir)
		if !deploy.DirExists(artifactDir) {
			log.Warn().Msgf("Artifact directory not found: %s", artifactDir)
			continue
		}

		finalArtifactID := artifact.Id
		finalArtifactName := cmp.Or(artifact.DisplayName, artifact.Id)
		if prefix != "" {
			finalArtifactID = prefix + "_" + artifact.Id
		}

		// Uploads run on the shared pool (Parallel), each in its own work
		// directory with its own synchroniser
		artifact := artifact
		orc.wg.Add(1)
		go func() {
			defer orc.wg.Done()
			orc.slots <- struct{}{}
			defer func() { <-orc.slots }()
			jobDir := filepath.Join(orc.workDir, "jobs", finalArtifactID)
			orc.uploadOneArtifact(pkg, &artifact, artifactDir, jobDir, finalPackageID, finalArtifactID, finalArtifactName)
		}()
	}
}

// uploadOneArtifact prepares the artifact (final ID and name in the
// manifest, configOverrides in parameters.prop) in jobDir and uploads it.
func (orc *Orchestrator) uploadOneArtifact(pkg *models.Package, artifact *models.Artifact, artifactDir, jobDir,
	finalPackageID, finalArtifactID, finalArtifactName string) {
	stats, locked := orc.Stats, orc.locked

	log.Info().Msgf("  Updating: %s", finalArtifactID)
	localVersion, _ := manifest.Version(artifactDir)
	fail := func(err error) {
		log.Error().Msgf("Update failed for %s: %v", finalArtifactName, err)
		locked(func() {
			stats.UpdateFailures++
			stats.FailedArtifactUpdates[artifact.Id] = true
			st := stats.status(finalArtifactID, finalPackageID, artifact.ArtifactDir)
			st.Status, st.LocalVersion, st.Reason = ArtifactFailed, localVersion, err.Error()
		})
	}

	artifactType := DesigntimeType(artifact.Type)

	tempArtifactDir := filepath.Join(jobDir, "artifact")
	if err := deploy.CopyDir(artifactDir, tempArtifactDir); err != nil {
		log.Error().Msgf("Failed to copy artifact to temp: %v", err)
		locked(func() { stats.FailedArtifactUpdates[artifact.Id] = true })
		return
	}

	// Update MANIFEST.MF
	manifestPath := filepath.Join(tempArtifactDir, "META-INF", "MANIFEST.MF")
	modifiedManifestPath := filepath.Join(jobDir, "modified", "META-INF", "MANIFEST.MF")
	if deploy.FileExists(manifestPath) {
		if err := deploy.UpdateManifestBundleName(manifestPath, finalArtifactID, finalArtifactName, modifiedManifestPath); err != nil {
			log.Warn().Msgf("Failed to update MANIFEST.MF: %v", err)
		}
	}

	// Handle parameters.prop
	var modifiedParamsPath string
	paramsPath := deploy.FindParametersFile(tempArtifactDir)
	if paramsPath != "" && deploy.FileExists(paramsPath) {
		modifiedParamsPath = filepath.Join(jobDir, "modified", "parameters.prop")
		if len(artifact.ConfigOverrides) > 0 {
			if err := deploy.MergeParametersFile(paramsPath, artifact.ConfigOverrides, modifiedParamsPath); err != nil {
				log.Warn().Msgf("Failed to merge parameters: %v", err)
			} else {
				log.Debug().Msgf("Applied %d config overrides", len(artifact.ConfigOverrides))
			}
		} else if data, err := os.ReadFile(paramsPath); err == nil {
			// No overrides, copy to modified location
			os.MkdirAll(filepath.Dir(modifiedParamsPath), 0755)
			os.WriteFile(modifiedParamsPath, data, 0644)
		}
	}

	// Copy modified manifest to temp artifact dir for sync
	if deploy.FileExists(modifiedManifestPath) {
		if data, err := os.ReadFile(modifiedManifestPath); err == nil {
			os.WriteFile(filepath.Join(tempArtifactDir, "META-INF", "MANIFEST.MF"), data, 0644)
		}
	}

	// Copy modified parameters if exists
	if modifiedParamsPath != "" && deploy.FileExists(modifiedParamsPath) {
		actualParamsPath := deploy.FindParametersFile(tempArtifactDir)
		if data, err := os.ReadFile(modifiedParamsPath); err == nil {
			os.WriteFile(actualParamsPath, data, 0644)
		}
	}

	log.Debug().Msgf("Updating %s (type %s) in package %s", finalArtifactID, artifactType, finalPackageID)

	mode, err := resolveVersioning(artifact.Versioning, pkg.Versioning, orc.opts.Versioning)
	if err != nil {
		fail(err)
		return
	}
	synchroniser := sync.New(orc.exe)
	synchroniser.Baseline, synchroniser.VerifyDownload = orc.baseline, orc.opts.VerifyDownload
	synchroniser.DryRun = orc.plan != nil
	// a content change with the running version is deployed with force in
	// the deployment phase (or by deploy --pending) instead of undeploying
	// it now
	synchroniser.KeepRuntime = true
	synchroniser.Versioning = mode
	outcome, err := synchroniser.UploadArtifact(finalArtifactID, finalArtifactName, artifactType,
		finalPackageID, tempArtifactDir, jobDir, "", nil)
	if sync.IsDraft(err) {
		running := ""
		if rt, rerr := cpi.NewRuntime(orc.exe).GetArtifact(finalArtifactID); rerr == nil && rt != nil {
			running = rt.Version
		}
		locked(func() {
			orc.recordDraft(DraftSkip{ID: finalArtifactID, Package: finalPackageID, Designtime: "Active", Running: running,
				LocalVersion: localVersion}, artifact.ArtifactDir, artifactType, true)
			if orc.draftError() {
				stats.UpdateFailures++
				stats.FailedArtifactUpdates[artifact.Id] = true
			}
		})
		return
	}

	locked(func() {
		switch outcome.Compared {
		case "snapshot":
			stats.ComparedWithSnapshot++
		case "download":
			stats.DownloadedForComparison++
		}
		if orc.plan != nil {
			orc.plan.upsert(finalArtifactID, func(it *PlanItem) {
				it.Package, it.Type = finalPackageID, artifactType
				it.Upload = map[string]string{"CREATED": "create", "UPDATED": "update", "UNCHANGED": "unchanged"}[outcome.Action]
				it.Compared, it.Designtime, it.VersionRule = outcome.Compared, outcome.Version, outcome.VersionRule
				if err != nil {
					it.Upload, it.Error = "fails", err.Error()
				}
			})
		}
		if err != nil {
			return
		}
		stats.uploaded[finalArtifactID] = true
		if outcome.Action != "UNCHANGED" {
			stats.changed[finalArtifactID] = true
		}
		if outcome.Redeploy {
			stats.redeploy[finalArtifactID] = true
		}
		if orc.plan != nil {
			// a plan writes nothing: the counters stay at zero, the plan
			// items carry the outcome
			return
		}
		st := stats.status(finalArtifactID, finalPackageID, artifact.ArtifactDir)
		st.Status = map[string]string{"CREATED": ArtifactCreated, "UPDATED": ArtifactUpdated}[outcome.Action]
		st.Status = cmp.Or(st.Status, ArtifactUnchanged)
		st.Designtime, st.LocalVersion = outcome.Version, localVersion
		if outcome.Action == "UNCHANGED" {
			stats.ArtifactsUnchanged++
		} else {
			stats.ArtifactsChanged++
		}
		stats.SuccessfulArtifactUpdates[finalArtifactID] = true
	})
	if err != nil {
		fail(err)
		return
	}
	log.Info().Msgf("    ✓ %s: %s", finalArtifactID, strings.ToLower(outcome.Action))
}

func (orc *Orchestrator) draftError() bool { return orc.opts.DraftHandling == DraftHandlingError }

func (orc *Orchestrator) collectDeploymentTasks(pkg *models.Package, packageDir, finalPackageID, prefix string) []DeploymentTask {
	stats := orc.Stats
	var tasks []DeploymentTask

	for _, artifact := range pkg.Artifacts {
		// Skip if update failed
		if stats.FailedArtifactUpdates[artifact.Id] {
			log.Debug().Msgf("Skipping artifact %s (due to failed update)", artifact.Id)
			continue
		}
		if !artifactSelected(&artifact, packageDir, orc.opts.ArtifactFilter) {
			log.Debug().Msgf("Skipping artifact %s (filtered)", artifact.Id)
			continue
		}
		if !artifact.Deploy {
			log.Debug().Msgf("Skipping artifact %s (deploy=false)", artifact.DisplayName)
			continue
		}

		finalArtifactID := artifact.Id
		if prefix != "" {
			finalArtifactID = prefix + "_" + artifact.Id
		}
		artifactType := cmp.Or(artifact.Type, "IntegrationFlow")
		if stats.skippedDraft[finalArtifactID] {
			log.Debug().Msgf("Skipping deployment of %s (draft on the tenant)", finalArtifactID)
			continue
		}
		if !stats.uploaded[finalArtifactID] && orc.drafts != nil {
			// deployed without an upload in this run: is it a draft?
			typ := DesigntimeType(artifactType)
			draft, err := orc.drafts.isDraft(finalPackageID, typ, finalArtifactID)
			if err != nil {
				log.Warn().Msgf("Cannot tell whether %s is in draft: %v", finalArtifactID, err)
			}
			if draft {
				localVersion, _ := manifest.Version(filepath.Join(packageDir, artifact.ArtifactDir))
				running := ""
				if rt, rerr := cpi.NewRuntime(orc.exe).GetArtifact(finalArtifactID); rerr == nil && rt != nil {
					running = rt.Version
				}
				orc.recordDraft(DraftSkip{ID: finalArtifactID, Package: finalPackageID, Designtime: "Active", Running: running,
					LocalVersion: localVersion}, artifact.ArtifactDir, typ, false)
				if orc.draftError() {
					stats.ArtifactsDeployedFailed++
					stats.DeployFailures++
					stats.FailedArtifactDeploys[finalArtifactID] = true
				}
				continue
			}
			stats.status(finalArtifactID, finalPackageID, artifact.ArtifactDir).Status = ArtifactNotUploaded
		}

		mode, err := resolveVersioning(artifact.Versioning, pkg.Versioning, orc.opts.Versioning)
		if err != nil {
			log.Error().Msgf("Skipping deployment of %s: %v", artifact.Id, err)
			stats.ArtifactsDeployedFailed++
			stats.DeployFailures++
			stats.FailedArtifactDeploys[artifact.Id] = true
			continue
		}
		// manifest: every variant of the artifact directory deploys exactly
		// its Bundle-Version
		expected := ""
		if mode == versioning.Manifest {
			v, err := manifest.Version(filepath.Join(packageDir, artifact.ArtifactDir))
			if err != nil || v == "" {
				log.Error().Msgf("Skipping deployment of %s: versioning manifest needs Bundle-Version in %s", finalArtifactID, filepath.Join(packageDir, artifact.ArtifactDir, "META-INF", "MANIFEST.MF"))
				stats.ArtifactsDeployedFailed++
				stats.DeployFailures++
				stats.FailedArtifactDeploys[artifact.Id] = true
				continue
			}
			expected = v
		}
		tasks = append(tasks, DeploymentTask{
			ArtifactID:      finalArtifactID,
			ArtifactType:    artifactType,
			PackageID:       finalPackageID,
			DisplayName:     artifact.DisplayName,
			Force:           stats.redeploy[finalArtifactID],
			Versioning:      mode,
			ExpectedVersion: expected,
		})
	}
	return tasks
}

// recordDraft notes an artifact in draft on the tenant: skipped (SKIP) or
// failed (ERROR). Callers hold the lock (or run alone).
func (orc *Orchestrator) recordDraft(d DraftSkip, source, typ string, uploadStep bool) {
	stats := orc.Stats
	st := stats.status(d.ID, d.Package, source)
	st.Designtime, st.LocalVersion = d.Designtime, d.LocalVersion
	reason := "draft on the tenant: not uploaded, not deployed"
	if orc.draftError() {
		st.Status, st.Reason = ArtifactFailed, "draft on the tenant (--draft-handling ERROR): save the version in the Web UI first"
		stats.draftFailed++
		log.Error().Msgf("%s is in draft on the tenant (--draft-handling ERROR): save the version in the Web UI first", d.ID)
	} else {
		st.Status, st.Reason = ArtifactSkippedDraft, reason
		stats.ArtifactsSkippedDraft++
		if stats.skippedDraft == nil {
			stats.skippedDraft = map[string]bool{}
		}
		stats.skippedDraft[d.ID] = true
		stats.SkippedDrafts = append(stats.SkippedDrafts, d)
		slices.SortFunc(stats.SkippedDrafts, func(a, b DraftSkip) int { return cmp.Or(cmp.Compare(a.Package, b.Package), cmp.Compare(a.ID, b.ID)) })
		log.Warn().Msgf("%s is in draft on the tenant: not uploaded, not deployed", d.ID)
	}
	if orc.plan != nil {
		orc.plan.upsert(d.ID, func(it *PlanItem) {
			it.Package, it.Type, it.Deploy = d.Package, cmp.Or(it.Type, typ), false
			it.Designtime, it.Running = d.Designtime, d.Running
			if orc.draftError() {
				it.Upload, it.Error = "fails", st.Reason
				if !uploadStep {
					it.Upload = ""
				}
			} else {
				it.Upload, it.Reason = ArtifactSkippedDraft, reason
			}
		})
	}
}

// draftChecker tells whether a designtime artifact is in draft, with one
// request per package and artifact type.
type draftChecker struct {
	exe   *httpclnt.HTTPExecuter
	mu    gosync.Mutex
	cache map[string]map[string]bool
}

func newDraftChecker(exe *httpclnt.HTTPExecuter) *draftChecker {
	return &draftChecker{exe: exe, cache: map[string]map[string]bool{}}
}

func (d *draftChecker) isDraft(pkg, typ, id string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := pkg + "\x00" + typ
	drafts, ok := d.cache[key]
	if !ok {
		details, err := cpi.NewIntegrationPackage(d.exe).GetArtifactsData(pkg, typ)
		if err != nil {
			return false, err
		}
		drafts = map[string]bool{}
		for _, a := range details {
			drafts[a.Id] = a.IsDraft
		}
		d.cache[key] = drafts
	}
	return drafts[id], nil
}

// resolveVersioning validates and resolves the versioning of an artifact
// and its package in a deploy config.
func resolveVersioning(artifact, pkg string, global versioning.Mode) (versioning.Mode, error) {
	for _, v := range []string{artifact, pkg} {
		if _, err := versioning.Parse(v); err != nil {
			return versioning.Unset, output.Usage(err)
		}
	}
	return versioning.Resolve(artifact, pkg, global), nil
}

// artifactSelected applies the artifact filter to a deploy config entry: an
// entry matches by its artifactId, its artifactDir (the folder name, as
// change detection passes it) or the source ID of that folder
// (Bundle-SymbolicName), so a folder or source ID selects every ID deployed
// from the folder. A copy's own ID selects only the copy.
func artifactSelected(a *models.Artifact, packageDir string, filter []string) bool {
	if len(filter) == 0 || slices.Contains(filter, a.Id) {
		return true
	}
	dir := cmp.Or(a.ArtifactDir, a.Id)
	if slices.Contains(filter, dir) || slices.Contains(filter, filepath.Base(dir)) {
		return true
	}
	if data, err := os.ReadFile(filepath.Join(packageDir, dir, "META-INF", "MANIFEST.MF")); err == nil {
		source, _, _ := strings.Cut(manifest.Parse(data)["Bundle-SymbolicName"], ";")
		if source = strings.TrimSpace(source); source != "" && slices.Contains(filter, source) {
			return true
		}
	}
	return false
}
