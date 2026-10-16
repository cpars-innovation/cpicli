// Package ops: deploy.go is the single implementation of "deploy designtime
// artifacts to runtime and wait for the outcome" (and its inverse, undeploy).
// It is used by the deploy, undeploy, configure and orchestrator commands.
package ops

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/versioning"
	"github.com/rs/zerolog/log"
)

// Status is the final outcome for one artifact.
type Status string

const (
	StatusDeployed    Status = "DEPLOYED"     // deployment confirmed on runtime
	StatusSkipped     Status = "SKIPPED"      // same version already STARTED (compare-versions)
	StatusUndeployed  Status = "UNDEPLOYED"   // runtime artifact removed
	StatusNotDeployed Status = "NOT_DEPLOYED" // undeploy: nothing was deployed
	StatusFailed      Status = "FAILED"
	StatusTimeout     Status = "TIMEOUT"
)

// Succeeded reports whether s is a successful outcome.
func (s Status) Succeeded() bool {
	switch s {
	case StatusDeployed, StatusSkipped, StatusUndeployed, StatusNotDeployed:
		return true
	}
	return false
}

// Artifact identifies a designtime artifact to deploy or a runtime artifact
// to undeploy.
type Artifact struct {
	ID        string
	Type      string // Integration, MessageMapping, ScriptCollection, ValueMapping
	PackageID string // informational only
	// Force deploys even when Options.CompareVersions would skip it (the
	// same version is already STARTED).
	Force bool
	// AllowDowngrade allows this artifact's designtime version to be older
	// than the running one (as Options.AllowDowngrade for all artifacts).
	AllowDowngrade bool
	// Versioning overrides Options.Versioning for this artifact.
	Versioning versioning.Mode
	// ExpectedVersion, when set, is the version the designtime artifact must
	// have (the repository's Bundle-Version after a manifest upload); any
	// other version is refused.
	ExpectedVersion string
	// ModifiedAt, when set, is the designtime artifact's last change as the
	// caller saw it (configure reads it before writing parameters, which may
	// count as a change); nil: read it from the tenant before the deployment.
	ModifiedAt *time.Time
}

// Rules that decide whether an older designtime version may replace the
// running one (Result.Rule).
const (
	// RuleVersion: the version numbers decided (no older designtime version,
	// or one that is refused).
	RuleVersion = "version"
	// RuleModifiedAfterDeployment: the designtime version is lower, but the
	// designtime artifact was changed after the running one was deployed, so
	// it holds the newer content (e.g. the runtime came from a manual
	// deployment of an older build with a bumped version).
	RuleModifiedAfterDeployment = "modified after deployment"
	// RuleAllowDowngrade: allowed explicitly.
	RuleAllowDowngrade = "allowDowngrade"
	// RuleKeep: versioning keep, no downgrade guard.
	RuleKeep = versioning.RuleKeep
	// RuleManifest: versioning manifest, the repository's version is
	// deployed (not lower than the running one).
	RuleManifest = versioning.RuleManifest
	// RuleBump: versioning tenant-bump.
	RuleBump = versioning.RuleBump
	// RuleGuard: refused (a lower version, or not the repository's).
	RuleGuard = versioning.RuleGuard
)

// Result is the structured outcome for one artifact.
type Result struct {
	ID        string `json:"id"`
	Type      string `json:"type,omitempty"`
	PackageID string `json:"packageId,omitempty"`
	TaskID    string `json:"taskId,omitempty"`
	Status    Status `json:"status"`
	Version   string `json:"version,omitempty"`
	// DesigntimeVersion and RuntimeVersion (before the deployment) are
	// filled by Deploy.
	DesigntimeVersion string `json:"designtimeVersion,omitempty"`
	RuntimeVersion    string `json:"runtimeVersion,omitempty"`
	// Rule is what decided about a designtime version that differs from the
	// running one (RuleVersion, RuleModifiedAfterDeployment,
	// RuleAllowDowngrade); empty when nothing was running.
	Rule string `json:"rule,omitempty"`
	// Reason explains the rule (versions compared).
	Reason string `json:"reason,omitempty"`
	// Versioning is the versioning mode the artifact was deployed with.
	Versioning string `json:"versioning,omitempty"`
	// Skipped is "draft" when the artifact was not deployed because it is
	// in draft on the tenant (Status SKIPPED).
	Skipped string `json:"skipped,omitempty"`
	Error   string `json:"error,omitempty"`
	// Warnings are findings that did not fail the deployment, e.g. a build
	// and deploy task that failed while the runtime runs the new version.
	Warnings []string `json:"warnings,omitempty"`
	// RuntimeLocation is the runtime the artifact was found on when it is
	// not the Cloud Integration runtime the public API reports (an
	// integration cell, e.g. "integrationcell"); empty otherwise.
	RuntimeLocation string `json:"runtimeLocation,omitempty"`

	// Err is the underlying error for FAILED/TIMEOUT results (not serialised).
	Err error `json:"-"`
}

// Options control polling and concurrency.
type Options struct {
	// Interval between two status checks.
	Interval time.Duration
	// MaxChecks is the maximum number of status checks per artifact when
	// neither Timeout nor Deadline is set.
	MaxChecks int
	// Timeout is how long Deploy and Undeploy wait for all artifacts
	// together, from the call (0: MaxChecks per artifact). Deadline, when
	// set, is the absolute end instead (several calls sharing one budget).
	Timeout  time.Duration
	Deadline time.Time
	// CompareVersions skips deployment when the same version is already STARTED.
	CompareVersions bool
	// AllowDowngrade deploys a designtime version that is older than the
	// running one. Without it such a deployment fails before it is
	// triggered: an older designtime version usually means the tenant copy
	// was never updated and would replace a newer runtime.
	AllowDowngrade bool
	// Versioning is the mode for artifacts without their own (see
	// docs/versioning.md): manifest and tenant-bump refuse any designtime
	// version lower than the running one, keep never refuses, Unset refuses
	// it unless the designtime artifact was changed after the running
	// deployment.
	Versioning versioning.Mode
	// Parallelism is the number of artifacts processed concurrently
	// (<= 0 means all at once).
	Parallelism int
	// ClockSkew is the tolerance applied when comparing the tenant's DeployedOn
	// with the local trigger time (only used when no previous deployment
	// timestamp is known). Defaults to 2 minutes.
	ClockSkew time.Duration
	// Progress, when set, receives every status check ("artifact X:
	// STARTING, check 3/60"); Total is MaxChecks times the number of
	// artifacts.
	Progress ProgressFunc

	progress    *progress
	totalChecks int
	runtime     *runtimeSnapshot
}

// prepare sets the deadline, the shared runtime snapshot and the progress
// total of one Deploy or Undeploy call.
func (o Options) prepare(tenant Tenant, n int) Options {
	if o.Deadline.IsZero() && o.Timeout > 0 {
		o.Deadline = time.Now().Add(o.Timeout)
	}
	o.progress, o.runtime = newProgress(o.Progress), newRuntimeSnapshot(tenant, o.Interval)
	o.totalChecks = o.checks() * n
	return o
}

// checks is the number of checks per artifact shown in progress messages.
func (o Options) checks() int {
	if !o.Deadline.IsZero() && o.Interval > 0 {
		return max(int(time.Until(o.Deadline)/o.Interval), 1)
	}
	return o.MaxChecks
}

// more tells whether check number check may run: always the first, then
// until the deadline or MaxChecks.
func (o Options) more(check int) bool {
	if check == 1 {
		return true
	}
	if !o.Deadline.IsZero() {
		return time.Now().Before(o.Deadline)
	}
	return check <= o.MaxChecks
}

// pause is the wait before the next check: the interval, shortened to the
// time left before the deadline.
func (o Options) pause() time.Duration {
	if !o.Deadline.IsZero() {
		return max(min(o.Interval, time.Until(o.Deadline)), 0)
	}
	return o.Interval
}

// limit describes how long was waited, for timeout messages.
func (o Options) limit(checks int) string {
	if !o.Deadline.IsZero() {
		return fmt.Sprintf("the deploy timeout (%d checks)", checks)
	}
	return fmt.Sprintf("%d checks", o.MaxChecks)
}

// designtimeModified is implemented by tenants that report when a
// designtime artifact was last changed (the CPI API does); without it the
// version rule decides alone.
type designtimeModified interface {
	DesigntimeModifiedAt(artifactType, id string) (time.Time, error)
}

// Tenant is the subset of the CPI API the deployer needs.
type Tenant interface {
	DesigntimeVersion(artifactType, id string) (version string, exists bool, err error)
	TriggerDeploy(artifactType, id string) (taskID string, err error)
	BuildAndDeployStatus(taskID string) (string, error)
	RuntimeArtifact(id string) (*cpi.RuntimeArtifact, error) // nil if not deployed
	RuntimeErrorInfo(id string) (string, error)
	Undeploy(id string) error
}

// NewTenant returns a Tenant backed by the CPI OData API.
func NewTenant(exe *httpclnt.HTTPExecuter) Tenant {
	return &apiTenant{exe: exe, rt: cpi.NewRuntime(exe)}
}

type apiTenant struct {
	exe *httpclnt.HTTPExecuter
	rt  *cpi.Runtime
}

func (t *apiTenant) DesigntimeVersion(artifactType, id string) (string, bool, error) {
	dt := cpi.NewDesigntimeArtifact(artifactType, t.exe)
	if dt == nil {
		return "", false, fmt.Errorf("unsupported artifact type %q (valid types: %s)", artifactType, strings.Join(cpi.ArtifactTypes, ", "))
	}
	version, _, exists, err := dt.Get(id, "active")
	return version, exists, err
}

func (t *apiTenant) DesigntimeModifiedAt(artifactType, id string) (time.Time, error) {
	info, exists, err := cpi.GetDesigntimeInfo(t.exe, artifactType, id, "active")
	if err != nil || !exists {
		return time.Time{}, err
	}
	return info.ModifiedAt, nil
}

func (t *apiTenant) TriggerDeploy(artifactType, id string) (string, error) {
	return cpi.TriggerDeploy(id, artifactType, t.exe)
}

func (t *apiTenant) BuildAndDeployStatus(taskID string) (string, error) {
	return t.rt.GetBuildAndDeployStatus(taskID)
}

func (t *apiTenant) RuntimeArtifact(id string) (*cpi.RuntimeArtifact, error) {
	return t.rt.GetArtifact(id)
}

// otherRuntimes is implemented by a Tenant that can see runtimes the public
// API does not report: a flow bound to an integration cell deploys there,
// but IntegrationRuntimeArtifacts lists only the Cloud Integration runtime.
type otherRuntimes interface {
	// OtherRuntimeArtifact finds id on a runtime other than Cloud
	// Integration (nil: on none).
	OtherRuntimeArtifact(id string) (*cpi.LocatedArtifact, error)
}

// OtherRuntimeArtifact looks id up on every runtime location but Cloud
// Integration, through the Web UI's runtime list.
func (t *apiTenant) OtherRuntimeArtifact(id string) (*cpi.LocatedArtifact, error) {
	locs, err := t.rt.Locations()
	if err != nil {
		return nil, err
	}
	for _, l := range locs {
		if l.ID == cpi.CloudIntegrationLocation {
			continue
		}
		arts, err := t.rt.LocationArtifacts(l.ID)
		if err != nil {
			return nil, err
		}
		for i := range arts {
			if arts[i].Id == id {
				return &arts[i], nil
			}
		}
	}
	return nil, nil
}

func (t *apiTenant) RuntimeErrorInfo(id string) (string, error) {
	return t.rt.GetErrorInfo(id)
}

func (t *apiTenant) Undeploy(id string) error {
	return t.rt.UnDeploy(id)
}

// Deploy deploys all artifacts and waits for each outcome. Up to
// Parallelism artifacts are checked and triggered at a time; the waiting is
// shared: every artifact is watched until its outcome, Timeout or
// Deadline, all from one runtime list per interval. Results are returned in
// input order; an individual failure never stops the others.
func Deploy(ctx context.Context, tenant Tenant, artifacts []Artifact, opts Options) []Result {
	opts = opts.prepare(tenant, len(artifacts))
	return forEachTriggered(ctx, artifacts, opts.Parallelism, func(a Artifact, triggered func()) Result {
		return deployOne(ctx, tenant, a, opts, triggered)
	})
}

// Undeploy removes all artifacts from runtime and waits until each is gone.
func Undeploy(ctx context.Context, tenant Tenant, artifacts []Artifact, opts Options) []Result {
	opts = opts.prepare(tenant, len(artifacts))
	return forEachTriggered(ctx, artifacts, opts.Parallelism, func(a Artifact, triggered func()) Result {
		return undeployOne(ctx, tenant, a, opts, triggered)
	})
}

// forEachTriggered runs fn for every artifact; at most parallelism of them
// at a time until they call triggered (the request to the tenant is sent),
// then they wait without holding a slot.
func forEachTriggered(ctx context.Context, artifacts []Artifact, parallelism int, fn func(Artifact, func()) Result) []Result {
	results := make([]Result, len(artifacts))
	if parallelism <= 0 || parallelism > len(artifacts) {
		parallelism = len(artifacts)
	}
	sem := make(chan struct{}, max(parallelism, 1))
	var wg sync.WaitGroup
	for i, a := range artifacts {
		wg.Go(func() {
			sem <- struct{}{}
			var once sync.Once
			release := func() { once.Do(func() { <-sem }) }
			defer release()
			r := fn(a, release)
			r.ID, r.Type, r.PackageID = a.ID, a.Type, a.PackageID
			results[i] = r
		})
	}
	wg.Wait()
	return results
}

// SkippedDraft marks an artifact left alone because it is in draft on the
// tenant: someone is editing it in the Web UI.
const (
	SkippedDraft = "draft"
	DraftReason  = "draft on the tenant (someone is editing it in the Web UI): save the version first; not deployed"
)

// IsDraftVersion reports whether a designtime version is the tenant's
// marker of a draft ("Active" instead of a version number).
func IsDraftVersion(v string) bool { return strings.EqualFold(v, "active") }

func fail(r Result, err error) Result {
	r.Status, r.Err, r.Error = StatusFailed, err, err.Error()
	return r
}

func timeout(r Result, err error) Result {
	r.Status, r.Err, r.Error = StatusTimeout, err, err.Error()
	return r
}

func deployOne(ctx context.Context, tenant Tenant, a Artifact, opts Options, triggered func()) Result {
	var r Result
	logger := log.With().Str("artifact", a.ID).Logger()

	if !cpi.IsValidArtifactType(a.Type) {
		return fail(r, fmt.Errorf("unsupported artifact type %q (valid types: %s)", a.Type, strings.Join(cpi.ArtifactTypes, ", ")))
	}
	version, exists, err := tenant.DesigntimeVersion(a.Type, a.ID)
	if err != nil {
		return fail(r, err)
	}
	if !exists {
		return fail(r, fmt.Errorf("designtime artifact %v does not exist", a.ID))
	}
	r.Version, r.DesigntimeVersion = version, version
	if IsDraftVersion(version) {
		r.Status, r.Skipped, r.Reason = StatusSkipped, SkippedDraft, DraftReason
		logger.Warn().Msgf("%v is in draft on the tenant: not deployed", a.ID)
		return r
	}

	// The runtime state before the trigger is the baseline used to tell a
	// fresh deployment from the previous one that is still visible.
	before, err := opts.runtime.get(a.ID)
	if err != nil {
		return fail(r, err)
	}
	mode := a.Versioning
	if mode == versioning.Unset {
		mode = opts.Versioning
	}
	r.Versioning = string(mode)
	if before != nil {
		r.RuntimeVersion = before.Version
	}
	if err := decideVersion(tenant, a, before, version, mode, opts, &r); err != nil {
		logger.Error().Str("rule", r.Rule).Msgf("%v: %v", a.ID, r.Reason)
		return fail(r, err)
	}
	logger.Info().Str("rule", r.Rule).Msgf("Version %s of %v: rule %s (%s)", version, a.ID, r.Rule, r.Reason)
	if opts.CompareVersions && !a.Force && before != nil && before.Status == "STARTED" && before.Version == version {
		logger.Info().Msgf("Artifact %v with version %v already deployed. Skipping runtime deployment", a.ID, version)
		r.Status = StatusSkipped
		return r
	}

	logger.Info().Str("version", version).Msgf("🚀 Deploying artifact %v with version %v%s", a.ID, version, versioningNote(mode))
	triggeredAt := time.Now()
	r.TaskID, err = tenant.TriggerDeploy(a.Type, a.ID)
	triggered()
	if err != nil {
		return fail(r, WithTenantText(err))
	}
	logger.Info().Str("taskId", r.TaskID).Msgf("Artifact %v deployment triggered", a.ID)

	return waitForDeployment(ctx, tenant, a, r, before, triggeredAt, opts)
}

func versioningNote(mode versioning.Mode) string {
	if mode == versioning.Unset {
		return ""
	}
	return " (versioning " + string(mode) + ")"
}

// decideVersion applies the versioning rule to the designtime version that
// is about to be deployed; it sets r.Rule and r.Reason and returns an error
// when the deployment is refused.
func decideVersion(tenant Tenant, a Artifact, before *cpi.RuntimeArtifact, version string, mode versioning.Mode, opts Options, r *Result) error {
	running := ""
	if before != nil {
		running = before.Version
	}
	cmp := 1
	if running != "" {
		cmp = CompareVersions(version, running)
	}
	relation := map[int]string{-1: "lower than", 0: "equal to", 1: "higher than"}[cmp]
	if running == "" {
		relation = "not running yet"
	}
	allowed := opts.AllowDowngrade || a.AllowDowngrade

	// the repository's version must be what the upload set
	if a.ExpectedVersion != "" && version != a.ExpectedVersion {
		r.Rule, r.Reason = RuleGuard, fmt.Sprintf("designtime %s is not the repository's Bundle-Version %s", version, a.ExpectedVersion)
		return fmt.Errorf("designtime version %s is not the repository's Bundle-Version %s (versioning %s): the upload did not set it", version, a.ExpectedVersion, mode)
	}

	switch mode {
	case versioning.Keep:
		r.Rule, r.Reason = RuleKeep, "versioning keep: no version guard"
		if running != "" {
			r.Reason += fmt.Sprintf(" (designtime %s, running %s)", version, running)
		}
		return nil
	case versioning.Manifest, versioning.TenantBump:
		r.Rule = RuleManifest
		if mode == versioning.TenantBump {
			r.Rule = RuleBump
		}
		if cmp < 0 {
			if allowed {
				r.Rule, r.Reason = RuleAllowDowngrade, fmt.Sprintf("designtime %s %s running %s, allowDowngrade", version, relation, running)
				return nil
			}
			r.Rule, r.Reason = RuleGuard, fmt.Sprintf("designtime %s %s running %s", version, relation, running)
			return fmt.Errorf("designtime version %s is lower than running %s (versioning %s; raise Bundle-Version in the repository, e.g. cpictl version bump, or set allowDowngrade: true on the artifact or package)",
				version, running, mode)
		}
		if running == "" {
			r.Reason = fmt.Sprintf("designtime %s, not running yet", version)
		} else {
			r.Reason = fmt.Sprintf("designtime %s %s running %s", version, relation, running)
		}
		return nil
	}

	// no versioning mode: the version guard with the timestamp exception
	if before == nil {
		return nil
	}
	r.Rule, r.Reason = RuleVersion, fmt.Sprintf("designtime %s %s running %s", version, relation, running)
	if running == "" || cmp >= 0 {
		return nil
	}
	var reason string
	r.Rule, reason = downgradeRule(tenant, a, before, opts)
	r.Reason += reason
	if r.Rule == RuleVersion {
		return fmt.Errorf("designtime version %s is older than running %s%s (allow with allowDowngrade: true on the artifact or package in the configure file, --allow-downgrade, or allow_downgrade)",
			version, running, reason)
	}
	return nil
}

// downgradeRule decides about a designtime version that is older than the
// running one. reason explains the timestamps for the log and the error.
func downgradeRule(tenant Tenant, a Artifact, before *cpi.RuntimeArtifact, opts Options) (rule, reason string) {
	if opts.AllowDowngrade || a.AllowDowngrade {
		return RuleAllowDowngrade, ""
	}
	if before.DeployedOn.IsZero() {
		return RuleVersion, "; the runtime does not report its deployment time"
	}
	var modified time.Time
	switch {
	case a.ModifiedAt != nil:
		modified = *a.ModifiedAt
	default:
		dm, ok := tenant.(designtimeModified)
		if !ok {
			return RuleVersion, ""
		}
		var err error
		if modified, err = dm.DesigntimeModifiedAt(a.Type, a.ID); err != nil {
			log.Debug().Str("artifact", a.ID).Msgf("Designtime modification time of %v not available: %v", a.ID, err)
			return RuleVersion, "; the designtime modification time is not available"
		}
	}
	if modified.IsZero() {
		return RuleVersion, "; the tenant does not report when the designtime artifact was modified"
	}
	stamps := fmt.Sprintf("designtime modified %s, runtime deployed %s", modified.UTC().Format(time.RFC3339), before.DeployedOn.UTC().Format(time.RFC3339))
	if modified.After(before.DeployedOn) {
		return RuleModifiedAfterDeployment, ": " + stamps
	}
	return RuleVersion, " and was not modified since that deployment (" + stamps + ")"
}

func waitForDeployment(ctx context.Context, tenant Tenant, a Artifact, r Result, before *cpi.RuntimeArtifact,
	triggeredAt time.Time, opts Options) Result {

	logger := log.With().Str("artifact", a.ID).Logger()
	skew := opts.ClockSkew
	if skew == 0 {
		skew = 2 * time.Minute
	}

	taskDone := r.TaskID == "" // nothing to poll without a task ID
	taskConfirmed := false
	taskStatus, runtimeStatus := "", ""
	var lastErr error
	otherErr := errNotLookedUp
	var otherStale *cpi.LocatedArtifact // found elsewhere, but the previous deployment
	checks := opts.checks()
	report := func(check int, state string) {
		opts.progress.report(opts.totalChecks, "artifact %s: %s, check %d/%d", a.ID, state, check, checks)
	}
	// note logs a state at info level when it changed, else at debug
	// level: with many artifacts only changes are worth reading
	lastNote := ""
	note := func(state, format string, args ...any) {
		ev := logger.Debug()
		if state != lastNote {
			ev, lastNote = logger.Info(), state
		}
		ev.Msgf(format, args...)
	}

	check := 0
	for check = 1; opts.more(check); check++ {
		if err := sleep(ctx, opts.pause()); err != nil {
			return fail(r, fmt.Errorf("deployment of %v not confirmed: %w", a.ID, err))
		}

		// 1) Build and deploy task status
		if !taskDone {
			status, err := tenant.BuildAndDeployStatus(r.TaskID)
			switch {
			case err != nil && httpclnt.IsAuthError(err):
				return fail(r, err)
			case err != nil && httpclnt.StatusCode(err) == 404:
				// Task unknown (or endpoint unavailable): rely on runtime status only
				logger.Debug().Msgf("Build and deploy status of task %v not available, using runtime status", r.TaskID)
				taskDone = true
			case err != nil:
				lastErr = err
				note("task-error", "Check %d - failed to get build and deploy status: %v", check, err)
				report(check, "build and deploy status unavailable")
				continue
			default:
				taskStatus = status
				note("task "+status, "Check %d - build and deploy status of %v = %s", check, a.ID, status)
				switch strings.ToUpper(status) {
				case "SUCCESS":
					taskDone, taskConfirmed = true, true
				case "FAIL", "FAILED", "ERROR":
					report(check, "build and deploy task "+status)
					info := errorInfoSuffix(ctx, tenant, a.ID, opts.Interval)
					// a first deploy on a fresh tenant can end FAIL while the
					// runtime runs the new version (#24): report both
					if rt, rerr := opts.runtime.get(a.ID); rerr == nil && rt != nil && rt.Status == "STARTED" && isFresh(rt, before, triggeredAt, skew, false) {
						if rt.Version != "" {
							r.Version = rt.Version
						}
						r.Status = StatusDeployed
						r.Warnings = append(r.Warnings, fmt.Sprintf("the build and deploy task %v ended with status %s, but the runtime runs %v %s (STARTED)%s; deploy again if in doubt",
							r.TaskID, status, a.ID, rt.Version, info))
						logger.Warn().Msg(r.Warnings[len(r.Warnings)-1])
						return r
					}
					return fail(r, fmt.Errorf("artifact %v build/deploy task %v ended with status %s%s",
						a.ID, r.TaskID, status, info))
				default:
					report(check, "build and deploy task "+status)
					continue
				}
			}
		}

		// 2) Runtime status
		rt, err := opts.runtime.get(a.ID)
		if err != nil {
			if httpclnt.IsAuthError(err) {
				return fail(r, err)
			}
			lastErr = err
			note("runtime-error", "Check %d - failed to get runtime status: %v", check, err)
			report(check, "runtime status unavailable")
			continue
		}
		if rt == nil {
			runtimeStatus = "NOT_DEPLOYED"
			if taskConfirmed {
				// the task succeeded: the flow may run on an integration cell
				if res, done := onOtherRuntime(tenant, a, r, triggeredAt, skew, &otherErr, &otherStale); done {
					return res
				}
			}
			note("absent", "Check %d - artifact %v not yet visible on runtime", check, a.ID)
			report(check, "not yet on runtime")
			continue
		}
		runtimeStatus = rt.Status
		if !isFresh(rt, before, triggeredAt, skew, taskConfirmed) {
			note("previous", "Check %d - runtime still shows the previous deployment of %v (status %s)", check, a.ID, rt.Status)
			report(check, "runtime still shows the previous deployment ("+rt.Status+")")
			continue
		}
		note("runtime "+rt.Status, "Check %d - runtime status of %v = %s", check, a.ID, rt.Status)
		report(check, rt.Status)
		switch rt.Status {
		case "STARTED":
			if rt.Version != "" {
				r.Version = rt.Version
			}
			r.Status = StatusDeployed
			logger.Info().Msgf("Artifact %v deployed successfully", a.ID)
			return r
		case "STARTING":
			continue
		default:
			return fail(r, fmt.Errorf("artifact %v deployment unsuccessful, ended with status %s%s",
				a.ID, rt.Status, errorInfoSuffix(ctx, tenant, a.ID, opts.Interval)))
		}
	}

	err := fmt.Errorf("deployment status of %v remained unfinished after %s (task status %q, runtime status %q)",
		a.ID, opts.limit(check-1), taskStatus, runtimeStatus)
	if lastErr != nil {
		err = fmt.Errorf("%w; last error: %v", err, lastErr)
	}
	switch {
	case taskConfirmed && (runtimeStatus == "NOT_DEPLOYED" || runtimeStatus == ""):
		// the task succeeded but the public API never listed the flow
		where := "on no other runtime either (Web UI runtime list)"
		if otherStale != nil {
			where = fmt.Sprintf("runtime %s still shows the deployment of %s (version %s)", otherStale.Location,
				otherStale.DeployedOn.UTC().Format(time.RFC3339), otherStale.Version)
		} else if otherErr != nil {
			where = fmt.Sprintf("the other runtimes could not be read (%v)", otherErr)
		}
		err = fmt.Errorf("%w; the build and deploy task succeeded, but %v is not on the Cloud Integration runtime and %s. "+
			"A flow bound to an integration cell runs there, which the public API does not list: see cpictl runtimes", err, a.ID, where)
	case runtimeStatus == "NOT_DEPLOYED" || runtimeStatus == "":
		// the deployment never reached the runtime: not a flow problem
		err = fmt.Errorf("%w; it never appeared on the runtime: check in the Web UI (Monitor > Manage Integration Content) that the tenant's runtime is up and accepts deployments. "+
			"An integration cell can refuse a flow at deploy time without the API saying so (one refused a script using Thread: lint unsupported-script-class)", err)
	}
	return timeout(r, err)
}

// errNotLookedUp: the other runtimes were not asked (yet).
var errNotLookedUp = errors.New("not looked up")

// onOtherRuntime looks for the deployment of a on a runtime the public API
// does not report. done is true when the outcome is final; *lookupErr keeps
// the last lookup error (nil after a successful lookup), *stale the artifact
// found there when it is still the previous deployment.
func onOtherRuntime(tenant Tenant, a Artifact, r Result, triggeredAt time.Time, skew time.Duration, lookupErr *error, stale **cpi.LocatedArtifact) (Result, bool) {
	o, ok := tenant.(otherRuntimes)
	if !ok {
		return r, false
	}
	la, err := o.OtherRuntimeArtifact(a.ID)
	*lookupErr = err
	if err != nil {
		log.Debug().Str("artifact", a.ID).Msgf("other runtimes not readable: %v", err)
		return r, false
	}
	*stale = nil
	if la == nil {
		return r, false
	}
	if !la.DeployedOn.IsZero() && la.DeployedOn.Before(triggeredAt.Add(-skew)) {
		*stale = la // still the previous deployment
		return r, false
	}
	r.RuntimeLocation = la.Location
	switch {
	case la.Status == "STARTED":
		if la.Version != "" {
			r.Version = la.Version
		}
		r.Status = StatusDeployed
		log.Info().Str("artifact", a.ID).Msgf("Artifact %v deployed successfully on runtime %s", a.ID, la.Location)
		return r, true
	case la.Status == "ERROR" || la.DeployState == "FAILED":
		return fail(r, fmt.Errorf("artifact %v deployment on runtime %s unsuccessful, ended with status %s (%s): see Monitor > Manage Integration Content, runtime %s, in the Web UI",
			a.ID, la.Location, la.Status, la.DeployState, la.Location)), true
	}
	return r, false // STARTING
}

// isFresh reports whether the runtime artifact reflects the deployment that
// was triggered at triggeredAt, rather than the one that was running before.
// This prevents reporting success for a redeploy of the same version while
// the old STARTED artifact is still visible.
func isFresh(rt, before *cpi.RuntimeArtifact, triggeredAt time.Time, skew time.Duration, taskConfirmed bool) bool {
	if before == nil {
		return true // nothing was deployed before the trigger
	}
	if !rt.DeployedOn.IsZero() {
		if !before.DeployedOn.IsZero() {
			// Both timestamps come from the tenant clock: no skew involved
			return rt.DeployedOn.After(before.DeployedOn)
		}
		return !rt.DeployedOn.Before(triggeredAt.Add(-skew))
	}
	// No timestamp reported: only accept evidence that something changed
	return taskConfirmed || rt.Version != before.Version
}

// errorInfoSuffix fetches the runtime error information (best effort). The
// tenant sometimes needs a moment before it is available.
func errorInfoSuffix(ctx context.Context, tenant Tenant, id string, wait time.Duration) string {
	if sleep(ctx, wait) != nil {
		return ""
	}
	msg, err := tenant.RuntimeErrorInfo(id)
	if err != nil || msg == "" {
		return ""
	}
	// the tenant's text has one issue per line ("... outlined below ...\n
	// Source element is not assigned\nTarget element is not assigned")
	var lines []string
	for _, l := range strings.Split(msg, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return ". Error message = " + strings.Join(lines, " | ")
}

func undeployOne(ctx context.Context, tenant Tenant, a Artifact, opts Options, triggered func()) Result {
	var r Result
	logger := log.With().Str("artifact", a.ID).Logger()

	current, err := opts.runtime.get(a.ID)
	if err != nil {
		return fail(r, err)
	}
	if current == nil {
		logger.Info().Msgf("Artifact %v is not deployed. Nothing to undeploy", a.ID)
		r.Status = StatusNotDeployed
		return r
	}
	r.Version = current.Version

	err = tenant.Undeploy(a.ID)
	triggered()
	if err != nil {
		if httpclnt.StatusCode(err) == 404 {
			r.Status = StatusNotDeployed
			return r
		}
		return fail(r, err)
	}
	logger.Info().Msgf("Artifact %v undeployment triggered", a.ID)

	status := current.Status
	var lastErr error
	checks, check := opts.checks(), 0
	for check = 1; opts.more(check); check++ {
		if err := sleep(ctx, opts.pause()); err != nil {
			return fail(r, fmt.Errorf("undeployment of %v not confirmed: %w", a.ID, err))
		}
		rt, err := opts.runtime.get(a.ID)
		if err != nil {
			if httpclnt.IsAuthError(err) {
				return fail(r, err)
			}
			lastErr = err
			logger.Warn().Msgf("Check %d - failed to get runtime status: %v", check, err)
			opts.progress.report(opts.totalChecks, "artifact %s: runtime status unavailable, check %d/%d", a.ID, check, checks)
			continue
		}
		if rt == nil {
			opts.progress.report(opts.totalChecks, "artifact %s: UNDEPLOYED, check %d/%d", a.ID, check, checks)
			logger.Info().Msgf("Artifact %v undeployed successfully", a.ID)
			r.Status = StatusUndeployed
			return r
		}
		status = rt.Status
		logger.Debug().Msgf("Check %d - artifact %v still on runtime (status %s)", check, a.ID, rt.Status)
		opts.progress.report(opts.totalChecks, "artifact %s: still on runtime (%s), check %d/%d", a.ID, rt.Status, check, checks)
	}
	err = fmt.Errorf("artifact %v still present on runtime after %s (status %q)", a.ID, opts.limit(check-1), status)
	if lastErr != nil {
		err = fmt.Errorf("%w; last error: %v", err, lastErr)
	}
	return timeout(r, err)
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Error summarises failed results; it is nil when every result succeeded.
type Error struct {
	Results []Result
}

// Err returns an *Error if any result did not succeed.
func Err(results []Result) error {
	for _, r := range results {
		if !r.Status.Succeeded() {
			return &Error{Results: results}
		}
	}
	return nil
}

// Failed returns the results that did not succeed.
func (e *Error) Failed() []Result {
	var failed []Result
	for _, r := range e.Results {
		if !r.Status.Succeeded() {
			failed = append(failed, r)
		}
	}
	return failed
}

// ExitCode implements the exit code contract: 7 if some artifacts succeeded,
// otherwise the most specific cause over all failures, in the order
// auth (3) > tenant HTTP error (4) > deploy failed (5) > timeout (6).
func (e *Error) ExitCode() int {
	failed := e.Failed()
	if len(failed) == 0 {
		return exitcode.OK
	}
	if len(failed) < len(e.Results) {
		return exitcode.Partial
	}
	// refused by the access scope (no tenant call was made): exit code 9
	scopeOnly := true
	for _, r := range failed {
		var coder output.ExitCoder
		scopeOnly = scopeOnly && errors.As(r.Err, &coder) && coder.ExitCode() == exitcode.Scope
	}
	if scopeOnly {
		return exitcode.Scope
	}
	code := exitcode.Timeout
	for _, r := range failed {
		c := exitcode.Timeout
		if r.Status == StatusFailed {
			c = output.TransportExitCode(r.Err, exitcode.DeployFailed)
		}
		code = min(code, c)
	}
	return code
}

func (e *Error) Error() string {
	failed := e.Failed()
	parts := make([]string, 0, len(failed))
	for _, r := range failed {
		parts = append(parts, fmt.Sprintf("%s: %s (%s)", r.ID, r.Status, r.Error))
	}
	return fmt.Sprintf("%d of %d artifact(s) failed - %s", len(failed), len(e.Results), strings.Join(parts, "; "))
}
