package cmd

import (
	"context"
	"fmt"
	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/internal/str"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"strings"
	"time"
)

func NewDeployCommand() *cobra.Command {

	deployCmd := &cobra.Command{
		Use:          "deploy",
		Short:        "Deploy designtime artifacts and wait for the result",
		SilenceUsage: true,
		Long: `Deploy artifact from designtime to
runtime of SAP Integration Suite tenant.

Configuration:
  Settings can be loaded from the global config file (--config) under the
  'deploy' section. CLI flags override config file settings.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			// Validate the artifact type
			artifactType := config.GetStringWithFallback(cmd, "artifact-type", "deploy.artifactType")
			if !cpi.IsValidArtifactType(artifactType) {
				return fmt.Errorf("invalid value for --artifact-type = %v", artifactType)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			if err = runDeploy(cmd); err != nil {
				cmd.SilenceUsage = true
			}
			return
		},
	}

	// Define cobra flags, the default value has the lowest (least significant) precedence
	// Note: These can be set in config file under 'deploy' key
	deployCmd.Flags().StringSlice("artifact-ids", nil, "Comma separated list of artifact IDs (config: deploy.artifactIds)")
	deployCmd.Flags().Int("delay-length", defaultDeployDelaySeconds, "Seconds between deployment status checks (config: deploy.delayLength)")
	deployCmd.Flags().Int("max-check-limit", defaultDeployChecks, "Deployment status checks per artifact (config: deploy.maxCheckLimit)")
	// To set to false, use --compare-versions=false
	addVersioningFlag(deployCmd)
	deployCmd.Flags().Bool("allow-downgrade", false, "Deploy even if the designtime version is older than the running version (config: deploy.allowDowngrade)")
	deployCmd.Flags().Bool("compare-versions", true, "Perform version comparison of design time against runtime before deployment (config: deploy.compareVersions)")
	deployCmd.Flags().Bool("pending", false, "Deploy the pending deployments that orchestrator and configure --defer-deploy collected (each artifact once); failed ones stay in the file")
	deployCmd.Flags().String("pending-file", "", "Pending deployments file (default: .cpi/pending-deploy.json)")
	deployCmd.Flags().Int("parallel-deployments", defaultParallelDeployments, "With --pending: deployments at the same time per package (config: deploy.parallelDeployments)")
	deployCmd.Flags().Bool("plan", false, "Only report per artifact whether it would be deployed and why; nothing is triggered")
	deployCmd.Flags().String("artifact-type", "Integration", "Artifact type. Allowed values: Integration, MessageMapping, ScriptCollection, ValueMapping (config: deploy.artifactType)")

	return deployCmd
}

func runDeploy(cmd *cobra.Command) error {
	serviceDetails := serviceDetails(cmd)

	// Support reading from config file under 'deploy' key
	artifactType := config.GetStringWithFallback(cmd, "artifact-type", "deploy.artifactType")
	log.Info().Msgf("Executing deploy %v command", artifactType)

	artifactIds := str.TrimSlice(config.GetStringSliceWithFallback(cmd, "artifact-ids", "deploy.artifactIds"))
	delayLength := config.GetIntWithFallback(cmd, "delay-length", "deploy.delayLength")
	maxCheckLimit := config.GetIntWithFallback(cmd, "max-check-limit", "deploy.maxCheckLimit")
	compareVersions := config.GetBoolWithFallback(cmd, "compare-versions", "deploy.compareVersions")
	allowDowngrade := config.GetBoolWithFallback(cmd, "allow-downgrade", "deploy.allowDowngrade")
	mode, err := versioningMode(cmd)
	if err != nil {
		return err
	}

	exe := cpi.InitHTTPExecuter(serviceDetails)
	planOnly, _ := cmd.Flags().GetBool("plan")
	if pending, _ := cmd.Flags().GetBool("pending"); pending {
		if len(artifactIds) > 0 {
			return output.Usagef("--pending and --artifact-ids cannot be combined")
		}
		parallel := config.GetIntWithFallback(cmd, "parallel-deployments", "deploy.parallelDeployments")
		return runPendingDeploy(cmd, exe, serviceDetails, planOnly, maxCheckLimit, delayLength, parallel)
	}

	artifacts := make([]ops.Artifact, 0, len(artifactIds))
	for _, id := range nonEmpty(artifactIds) {
		artifacts = append(artifacts, ops.Artifact{ID: id, Type: artifactType})
	}
	if planOnly && len(artifacts) > 0 {
		plan, err := ops.PlanDeploy(exe, artifactType, nonEmpty(artifactIds), mode, allowDowngrade)
		items := make([]PlanItem, 0, len(plan))
		for _, p := range plan {
			items = append(items, PlanItem{Artifact: p.ID, Designtime: p.Designtime, Running: p.Running, RuntimeStatus: p.RuntimeStatus,
				Deploy: p.Deploy, Reason: p.Reason, Error: p.Error})
		}
		output.SetResult(cmd.Context(), planResult{Plan: items})
		if perr := logPlan(items); perr != nil {
			return perr
		}
		return err
	}
	if len(artifacts) == 0 {
		// Validated here rather than with MarkFlagRequired so that the config
		// file fallback (deploy.artifactIds) works
		return output.Usagef("required flag \"artifact-ids\" not set (or config deploy.artifactIds)")
	}

	results := ops.Deploy(cmd.Context(), ops.NewTenant(exe), artifacts, ops.Options{
		Interval:        time.Duration(delayLength) * time.Second,
		MaxChecks:       maxCheckLimit,
		CompareVersions: compareVersions,
		AllowDowngrade:  allowDowngrade,
		Versioning:      mode,
		// All artifacts are triggered and polled concurrently, as before
		Parallelism: len(artifacts),
	})
	logResults(results)
	emitResults(cmd.Context(), results)
	output.SetResult(cmd.Context(), artifactResults{Results: results})
	if err := ops.Err(results); err != nil {
		return err
	}
	log.Info().Msg("🏆 Artifact(s) deployment completed successfully")
	return nil
}

// runPendingDeploy deploys the pending deployments once each and keeps
// only the failed ones in the file.
func runPendingDeploy(cmd *cobra.Command, exe *httpclnt.HTTPExecuter, serviceDetails *cpi.ServiceDetails, planOnly bool,
	maxChecks, delaySeconds, parallel int) error {
	path := pendingFile(cmd)
	p, err := loadPending(path)
	if err != nil {
		return err
	}
	if len(p.Artifacts) == 0 {
		log.Info().Msgf("No pending deployments in %s", path)
		output.SetResult(cmd.Context(), artifactResults{Results: []ops.Result{}})
		return nil
	}
	if tenant := cpi.TenantID(serviceDetails.Host); p.Tenant != tenant {
		return output.Usagef("%s holds pending deployments for tenant %s, not %s", path, p.Tenant, tenant)
	}
	tasks := p.tasks()
	for _, t := range tasks {
		log.Info().Msgf("📋 %s (%s)%s: %s", t.ArtifactID, t.PackageID, map[bool]string{true: " [force]", false: ""}[t.Force],
			strings.Join(p.Artifacts[t.ArtifactID].Reasons, "; "))
	}
	if planOnly {
		items := ops.PlanDeployments(exe, tasks)
		output.SetResult(cmd.Context(), planResult{Plan: items})
		return logPlan(items)
	}
	results := deployTasks(cmd.Context(), exe, tasks, true, maxChecks, delaySeconds, parallel)
	logResults(results)
	emitResults(cmd.Context(), results)
	output.SetResult(cmd.Context(), artifactResults{Results: results})
	drafts := 0
	for _, r := range results {
		switch {
		case r.Skipped == ops.SkippedDraft:
			drafts++ // deployed by a later run, once the version is saved
		case r.Status.Succeeded():
			delete(p.Artifacts, r.ID)
		}
	}
	if err := p.save(path); err != nil {
		return err
	}
	if len(p.Artifacts) > 0 {
		log.Warn().Msgf("%d deployment(s) stay in %s (%d failed, %d in draft on the tenant)", len(p.Artifacts), path, len(p.Artifacts)-drafts, drafts)
	}
	return ops.Err(results)
}

// artifactResults is the JSON result of deploy and undeploy.
type artifactResults struct {
	Results []ops.Result `json:"results"`
}

// logResults logs one line per artifact result.
func logResults(results []ops.Result) {
	for _, r := range results {
		event := log.Info()
		if !r.Status.Succeeded() {
			event = log.Error()
		}
		event = event.Str("artifact", r.ID).Str("status", string(r.Status)).Str("version", r.Version).Str("taskId", r.TaskID)
		rule := ""
		if r.Rule != "" {
			event = event.Str("rule", r.Rule)
			rule = " [rule: " + r.Rule + "]"
			if r.Reason != "" {
				event = event.Str("reason", r.Reason)
				rule = " [rule: " + r.Rule + ": " + r.Reason + "]"
			}
		}
		if r.Versioning != "" {
			event = event.Str("versioning", r.Versioning)
		}
		event.Msgf("%s: %s%s%s", r.ID, r.Status, rule, errSuffix(r.Error))
	}
}

func errSuffix(msg string) string {
	if msg == "" {
		return ""
	}
	return " - " + msg
}

// emitResults writes an item event (--events) per deployment result.
func emitResults(ctx context.Context, results []ops.Result) {
	for _, r := range results {
		output.Emit(ctx, output.EventItem, r)
	}
}
