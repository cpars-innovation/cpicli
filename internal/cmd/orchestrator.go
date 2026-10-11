package cmd

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/deploy"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// The orchestrator runs on pkg/ops (ops.Orchestrator); these names are
// kept for the command, its result and its tests.
type (
	OperationMode   = ops.OrchestratorMode
	ProcessingStats = ops.OrchestratorStats
	DraftSkip       = ops.DraftSkip
	ArtifactStatus  = ops.OrchestratorArtifact
	PlanItem        = ops.PlanItem
)

const (
	ModeUpdateAndDeploy = ops.OrchestratorUpdateAndDeploy
	ModeUpdateOnly      = ops.OrchestratorUpdateOnly
	ModeDeployOnly      = ops.OrchestratorDeployOnly
)

// Artifact statuses of the orchestrator.
const (
	StatusCreated      = ops.ArtifactCreated
	StatusUpdated      = ops.ArtifactUpdated
	StatusUnchanged    = ops.ArtifactUnchanged
	StatusFailed       = ops.ArtifactFailed
	StatusSkippedDraft = ops.ArtifactSkippedDraft
	StatusNotUploaded  = ops.ArtifactNotUploaded
)

// draftHandling of the orchestrator.
const (
	draftSkip  = ops.DraftHandlingSkip
	draftError = ops.DraftHandlingError
)

func NewOrchestratorCommand() *cobra.Command {
	var (
		packagesDir         string
		deployConfig        string
		deploymentPrefix    string
		packageFilter       string
		artifactFilter      string
		keepTemp            bool
		configPattern       string
		mergeConfigs        bool
		updateMode          bool
		updateOnlyMode      bool
		deployOnlyMode      bool
		deployRetries       int
		deployDelaySeconds  int
		parallelDeployments int
	)

	orchestratorCmd := &cobra.Command{
		Use:          "orchestrator",
		Short:        "Update and deploy many packages from a local directory tree",
		SilenceUsage: true,
		Long: `Orchestrate the complete deployment lifecycle for SAP CPI artifacts.

This command handles:
  - Updates artifacts in SAP CPI tenant with modified MANIFEST.MF and parameters
  - Deploys artifacts to make them active (in parallel for faster execution)
  - Supports deployment prefixes for multi-environment scenarios
  - Intelligent artifact grouping by type for efficient deployment
  - Filter by specific packages or artifacts
  - Load configs from files, folders, or remote URLs
  - Configure via YAML file for repeatable deployments

Configuration Sources:
  The --deploy-config flag accepts:
  - Single file:      ./001-deploy-config.yml
  - Folder:           ./configs (processes all matching files alphabetically)
  - Remote URL:       https://raw.githubusercontent.com/org/repo/main/config.yml (public, no authentication)

  All flags can also be set in the global config file (--config) under
  the 'orchestrator' key; CLI flags override config file settings.

Operation Modes:
  --update          Update and deploy artifacts (default)
  --update-only     Only update artifacts, don't deploy
  --deploy-only     Only deploy artifacts, don't update

Deployment Strategy:
  1. Update Phase: All packages and artifacts are updated first
  2. Deploy Phase: All artifacts are deployed in parallel
     - Deployments are triggered concurrently per package
     - Status is polled for all deployments simultaneously
     - Configurable parallelism and retry settings

Configuration:
  Settings can be loaded from the global config file (--config) under the
  'orchestrator' section. CLI flags override config file settings.`,
		Example: `  # Update and deploy with config from global cpictl.yaml
  cpictl orchestrator --update

  # Load specific config file
  cpictl orchestrator --config ./my-config.yml --update

  # Override settings via CLI flags
  cpictl orchestrator --config ./my-config.yml \
    --deployment-prefix DEV --parallel-deployments 5`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Determine operation mode
			mode := ModeUpdateAndDeploy
			if updateOnlyMode {
				mode = ModeUpdateOnly
			} else if deployOnlyMode {
				mode = ModeDeployOnly
			}

			// Load from viper config if available (CLI flags override config file)
			if !cmd.Flags().Changed("packages-dir") && viper.IsSet("orchestrator.packagesDir") {
				packagesDir = viper.GetString("orchestrator.packagesDir")
			}
			if !cmd.Flags().Changed("deploy-config") && viper.IsSet("orchestrator.deployConfig") {
				deployConfig = viper.GetString("orchestrator.deployConfig")
			}
			if !cmd.Flags().Changed("deployment-prefix") && viper.IsSet("orchestrator.deploymentPrefix") {
				deploymentPrefix = viper.GetString("orchestrator.deploymentPrefix")
			}
			if !cmd.Flags().Changed("package-filter") && viper.IsSet("orchestrator.packageFilter") {
				packageFilter = viper.GetString("orchestrator.packageFilter")
			}
			if !cmd.Flags().Changed("artifact-filter") && viper.IsSet("orchestrator.artifactFilter") {
				artifactFilter = viper.GetString("orchestrator.artifactFilter")
			}
			if !cmd.Flags().Changed("config-pattern") && viper.IsSet("orchestrator.configPattern") {
				configPattern = viper.GetString("orchestrator.configPattern")
			}
			if !cmd.Flags().Changed("merge-configs") && viper.IsSet("orchestrator.mergeConfigs") {
				mergeConfigs = viper.GetBool("orchestrator.mergeConfigs")
			}
			if !cmd.Flags().Changed("keep-temp") && viper.IsSet("orchestrator.keepTemp") {
				keepTemp = viper.GetBool("orchestrator.keepTemp")
			}
			if !updateMode && !updateOnlyMode && !deployOnlyMode && viper.IsSet("orchestrator.mode") {
				switch viper.GetString("orchestrator.mode") {
				case "update-and-deploy":
					mode = ModeUpdateAndDeploy
				case "update-only":
					mode = ModeUpdateOnly
				case "deploy-only":
					mode = ModeDeployOnly
				}
			}
			if !cmd.Flags().Changed("deploy-retries") && viper.IsSet("orchestrator.deployRetries") {
				deployRetries = viper.GetInt("orchestrator.deployRetries")
			}
			if !cmd.Flags().Changed("deploy-delay") && viper.IsSet("orchestrator.deployDelaySeconds") {
				deployDelaySeconds = viper.GetInt("orchestrator.deployDelaySeconds")
			}
			if !cmd.Flags().Changed("parallel-deployments") && viper.IsSet("orchestrator.parallelDeployments") {
				parallelDeployments = viper.GetInt("orchestrator.parallelDeployments")
			}

			// Validate required parameters
			if deployConfig == "" {
				return output.Usagef("--deploy-config is required (set via CLI flag or in config file under 'orchestrator.deployConfig')")
			}

			// Set defaults for deployment settings
			if deployRetries == 0 {
				deployRetries = defaultDeployChecks
			}
			if deployDelaySeconds == 0 {
				deployDelaySeconds = defaultDeployDelaySeconds
			}
			if parallelDeployments == 0 {
				parallelDeployments = defaultParallelDeployments
			}

			return runOrchestrator(cmd, mode, packagesDir, deployConfig,
				deploymentPrefix, packageFilter, artifactFilter, keepTemp,
				configPattern, mergeConfigs, deployRetries, deployDelaySeconds, parallelDeployments)
		},
	}

	// Flags
	orchestratorCmd.Flags().StringVarP(&packagesDir, "packages-dir", "d", "", "Directory containing packages (config: orchestrator.packagesDir)")
	orchestratorCmd.Flags().StringVarP(&deployConfig, "deploy-config", "c", "", "Path to deployment config file/folder/URL (config: orchestrator.deployConfig)")
	orchestratorCmd.Flags().StringVarP(&deploymentPrefix, "deployment-prefix", "p", "", "Deployment prefix for package/artifact IDs (config: orchestrator.deploymentPrefix)")
	orchestratorCmd.Flags().StringVar(&packageFilter, "package-filter", "", "Comma-separated list of packages to include (config: orchestrator.packageFilter)")
	orchestratorCmd.Flags().StringVar(&artifactFilter, "artifact-filter", "", "Comma-separated artifacts to include: artifact IDs, artifact folders or source IDs (a folder or source ID selects every ID deployed from it) (config: orchestrator.artifactFilter)")
	orchestratorCmd.Flags().BoolVar(&keepTemp, "keep-temp", false, "Keep temporary directory after execution (config: orchestrator.keepTemp)")
	orchestratorCmd.Flags().StringVar(&configPattern, "config-pattern", "*.y*ml", "File pattern for config files in folders (config: orchestrator.configPattern)")
	orchestratorCmd.Flags().BoolVar(&mergeConfigs, "merge-configs", false, "Merge multiple configs into single deployment (config: orchestrator.mergeConfigs)")
	orchestratorCmd.Flags().BoolVar(&updateMode, "update", false, "Update and deploy artifacts")
	orchestratorCmd.Flags().BoolVar(&updateOnlyMode, "update-only", false, "Only update artifacts, don't deploy")
	orchestratorCmd.Flags().BoolVar(&deployOnlyMode, "deploy-only", false, "Only deploy artifacts, don't update")
	orchestratorCmd.Flags().IntVar(&deployRetries, "deploy-retries", defaultDeployChecks, "Deployment status checks per artifact (config: orchestrator.deployRetries)")
	orchestratorCmd.Flags().IntVar(&deployDelaySeconds, "deploy-delay", defaultDeployDelaySeconds, "Seconds between deployment status checks (config: orchestrator.deployDelaySeconds)")
	addVersioningFlag(orchestratorCmd)
	orchestratorCmd.Flags().String("snapshot-state", "", "Snapshot state of the target tenant (written by snapshot) used instead of downloading artifacts for the comparison (config: orchestrator.snapshotState; default: .cpi/snapshot-state.json in the current directory or above --packages-dir; \"off\": always download)")
	addDeferFlags(orchestratorCmd)
	orchestratorCmd.Flags().Int("parallel", 8, "Artifacts uploaded at the same time, across all packages (config: orchestrator.parallel)")
	orchestratorCmd.Flags().Bool("plan", false, "Only show what would be uploaded and deployed, and why; nothing is written to the tenant")
	orchestratorCmd.Flags().Bool("verify-download", false, "Download every existing artifact for the comparison, even when the snapshot state covers it (config: orchestrator.verifyDownload)")
	orchestratorCmd.Flags().String("draft-handling", draftSkip, "Artifacts in draft on the tenant (someone edits them in the Web UI): SKIP (not uploaded, not deployed, reported) or ERROR (the run fails) (config: orchestrator.draftHandling)")
	orchestratorCmd.Flags().Bool("fail-on-draft", false, "Same as --draft-handling ERROR")
	orchestratorCmd.Flags().IntVar(&parallelDeployments, "parallel-deployments", defaultParallelDeployments, "Deployments at the same time per package (config: orchestrator.parallelDeployments)")

	return orchestratorCmd
}

func runOrchestrator(cmd *cobra.Command, mode OperationMode, packagesDir, deployConfigPath,
	deploymentPrefix, packageFilterStr, artifactFilterStr string, keepTemp bool,
	configPattern string, mergeConfigs bool, deployRetries, deployDelaySeconds, parallelDeployments int) error {

	log.Info().Msg("Starting orchestrator")
	versionMode, err := versioningMode(cmd)
	if err != nil {
		return err
	}
	log.Info().Msgf("Deployment Strategy: Two-phase with parallel deployment")
	log.Info().Msgf("  Phase 1: Update all artifacts")
	log.Info().Msgf("  Phase 2: Deploy all artifacts in parallel (max %d concurrent)", parallelDeployments)

	// Validate deployment prefix
	if err := deploy.ValidateDeploymentPrefix(deploymentPrefix); err != nil {
		return output.Usage(err)
	}

	// Parse filters
	packageFilter := parseFilter(packageFilterStr)
	artifactFilter := parseFilter(artifactFilterStr)

	// Setup config loader
	configLoader := deploy.NewConfigLoader()
	configLoader.Debug = viper.GetBool("debug")
	configLoader.FilePattern = configPattern

	if err := configLoader.DetectSource(deployConfigPath); err != nil {
		return output.Usage(fmt.Errorf("failed to detect config source: %w", err))
	}

	log.Info().Msgf("Loading config from: %s (type: %s)", deployConfigPath, configLoader.Source)
	configFiles, err := configLoader.LoadConfigs()
	if err != nil {
		return output.Usage(fmt.Errorf("failed to load deployment config: %w", err))
	}

	log.Info().Msgf("Loaded %d config file(s)", len(configFiles))

	// Create temporary work directory if needed
	var workDir string
	if mode != ModeDeployOnly {
		tempDir, err := os.MkdirTemp("", "cpictl-orchestrator-*")
		if err != nil {
			return fmt.Errorf("failed to create temp directory: %w", err)
		}
		workDir = tempDir

		if !keepTemp {
			defer os.RemoveAll(tempDir)
		} else {
			log.Info().Msgf("Temporary directory: %s", tempDir)
		}
	}

	log.Info().Msgf("Mode: %s", mode)
	log.Info().Msgf("Packages Directory: %s", packagesDir)

	if len(packageFilter) > 0 {
		log.Info().Msgf("Package filter: %s", strings.Join(packageFilter, ", "))
	}
	if len(artifactFilter) > 0 {
		log.Info().Msgf("Artifact filter: %s", strings.Join(artifactFilter, ", "))
	}

	// Get service details once (shared across all operations). Values from the
	// config file and environment are already bound to the flags.
	serviceDetails := serviceDetails(cmd)
	if serviceDetails.Host == "" {
		return output.Usagef("CPI host (tmn-host) is required but not provided")
	}

	log.Debug().Msg("CPI credentials successfully loaded:")
	log.Debug().Msgf("  Host: %s", serviceDetails.Host)
	if serviceDetails.OauthHost != "" {
		log.Debug().Msgf("  OAuth Host: %s", serviceDetails.OauthHost)
		log.Debug().Msg("  Auth Method: OAuth")
	} else {
		log.Debug().Msg("  Auth Method: Basic Auth")
	}

	parallel := config.GetIntWithFallback(cmd, "parallel", "orchestrator.parallel")
	if parallel < 1 {
		return output.Usagef("--parallel must be at least 1")
	}
	draftHandling := strings.ToUpper(config.GetStringWithFallback(cmd, "draft-handling", "orchestrator.draftHandling"))
	if failOnDraft, _ := cmd.Flags().GetBool("fail-on-draft"); failOnDraft {
		draftHandling = draftError
	}
	if draftHandling != draftSkip && draftHandling != draftError {
		return output.Usagef("--draft-handling %q: SKIP or ERROR", draftHandling)
	}
	exe := cpi.InitHTTPExecuter(serviceDetails)
	opts := ops.OrchestratorOptions{Mode: mode, PackageFilter: packageFilter, ArtifactFilter: artifactFilter,
		Versioning: versionMode, Host: serviceDetails.Host, DraftHandling: draftHandling, Parallel: parallel, WorkDir: workDir}
	if plan, _ := cmd.Flags().GetBool("plan"); plan {
		opts.Plan = true
		log.Info().Msg("PLAN: nothing is written to the tenant")
	}
	if mode != ModeDeployOnly {
		if opts.SnapshotState, err = baselinePath(cmd, packagesDir); err != nil {
			return err
		}
		opts.VerifyDownload = config.GetBoolWithFallback(cmd, "verify-download", "orchestrator.verifyDownload")
	}
	orc, err := ops.NewOrchestrator(exe, packagesDir, opts)
	if err != nil {
		return err
	}
	defer orc.Close()
	stats := orc.Stats

	// Collect all deployment tasks (will be executed in phase 2)
	var deploymentTasks []DeploymentTask

	// Process configs
	if mergeConfigs && len(configFiles) > 1 {
		log.Info().Msg("Merging multiple configs into single deployment")

		if deploymentPrefix != "" {
			log.Warn().Msg("Note: --deployment-prefix is ignored when merging configs with their own prefixes")
		}

		mergedConfig, err := deploy.MergeConfigs(configFiles)
		if err != nil {
			return output.Usage(fmt.Errorf("failed to merge configs: %w", err))
		}

		tasks, err := orc.Process(cmd.Context(), mergedConfig)
		if err != nil {
			return err
		}
		deploymentTasks = append(deploymentTasks, tasks...)
	} else {
		for _, configFile := range configFiles {
			if len(configFiles) > 1 {
				log.Info().Msgf("Processing Config: %s", configFile.FileName)
			}

			// Override deployment prefix if specified via CLI
			if deploymentPrefix != "" {
				configFile.Config.DeploymentPrefix = deploymentPrefix
			}

			log.Info().Msgf("Deployment Prefix: %s", configFile.Config.DeploymentPrefix)

			tasks, err := orc.Process(cmd.Context(), configFile.Config)
			if err != nil {
				log.Error().Msgf("Failed to process config %s: %v", configFile.FileName, err)
				continue
			}
			deploymentTasks = append(deploymentTasks, tasks...)
		}
	}

	if deferDeploy, _ := cmd.Flags().GetBool("defer-deploy"); deferDeploy && !opts.Plan && mode != ModeUpdateOnly {
		printSummary(stats)
		output.SetResult(cmd.Context(), newOrchestratorResult(mode, stats, []ops.Result{}, nil))
		err := deferDeployments(pendingFile(cmd), serviceDetails.Host, deploymentTasks, func(t DeploymentTask) string {
			switch {
			case stats.Redeploy(t.ArtifactID):
				return "orchestrator: content changed, same version as running"
			case stats.Changed(t.ArtifactID):
				return "orchestrator: content changed"
			}
			return "orchestrator: deploy if the runtime lacks the designtime version"
		})
		if err != nil {
			return err
		}
		return stats.Err()
	}

	if opts.Plan {
		orc.PlanDeployments(deploymentTasks)
		output.SetResult(cmd.Context(), newOrchestratorResult(mode, stats, []ops.Result{}, orc.Plan()))
		logSkippedDrafts(stats)
		return logPlan(orc.Plan())
	}

	// Phase 2: Deploy all artifacts in parallel (if not update-only mode)
	deployments := []ops.Result{}
	if mode != ModeUpdateOnly && len(deploymentTasks) > 0 {
		log.Info().Msg("")
		log.Info().Msg("═══════════════════════════════════════════════════════════════════════")
		log.Info().Msg("PHASE 2: DEPLOYING ALL ARTIFACTS IN PARALLEL")
		log.Info().Msg("═══════════════════════════════════════════════════════════════════════")
		log.Info().Msgf("Total artifacts to deploy: %d", len(deploymentTasks))
		log.Info().Msgf("Max concurrent deployments: %d", parallelDeployments)
		log.Info().Msg("")

		// Version comparison is kept from the previous implementation:
		// artifacts whose runtime version equals the designtime version are
		// not redeployed.
		deployments = deployTasks(cmd.Context(), exe, deploymentTasks, true, deployRetries, deployDelaySeconds, parallelDeployments)
		orc.RecordDeployments(deployments)
	}

	// Print summary
	printSummary(stats)
	output.SetResult(cmd.Context(), newOrchestratorResult(mode, stats, deployments, nil))

	return stats.Err()
}

// orchestratorResult is the JSON result of orchestrator.
type orchestratorResult struct {
	Mode  string           `json:"mode"`
	Stats *ProcessingStats `json:"stats"`
	// Artifacts is the status of each deployed artifact ID, Counts the
	// number per status (created, updated, unchanged, failed, skippedDraft,
	// notUploaded) and deployment (deployed, deployFailed).
	Artifacts   []ArtifactStatus `json:"artifacts"`
	Counts      map[string]int   `json:"counts"`
	Deployments []ops.Result     `json:"deployments"`
	// Plan lists per artifact what would be done (--plan).
	Plan []PlanItem `json:"plan,omitempty"`
}

func newOrchestratorResult(mode OperationMode, stats *ProcessingStats, deployments []ops.Result, plan []PlanItem) orchestratorResult {
	res := orchestratorResult{Mode: string(mode), Stats: stats, Deployments: deployments, Plan: plan, Artifacts: stats.Artifacts(),
		Counts: map[string]int{"created": 0, "updated": 0, "unchanged": 0, "failed": 0, "skippedDraft": 0, "notUploaded": 0, "deployed": 0, "deployFailed": 0}}
	key := map[string]string{StatusCreated: "created", StatusUpdated: "updated", StatusUnchanged: "unchanged", StatusFailed: "failed",
		StatusSkippedDraft: "skippedDraft", StatusNotUploaded: "notUploaded"}
	for _, a := range res.Artifacts {
		if k := key[a.Status]; k != "" {
			res.Counts[k]++
		}
		switch {
		case a.Deploy == "":
		case ops.Status(a.Deploy).Succeeded():
			res.Counts["deployed"]++
		default:
			res.Counts["deployFailed"]++
		}
	}
	slices.SortFunc(stats.SkippedDrafts, func(a, b DraftSkip) int { return cmp.Or(cmp.Compare(a.Package, b.Package), cmp.Compare(a.ID, b.ID)) })
	return res
}

// logSkippedDrafts lists the artifacts left alone because of drafts.
func logSkippedDrafts(stats *ProcessingStats) {
	if len(stats.SkippedDrafts) == 0 {
		return
	}
	log.Warn().Msgf("%d artifact(s) in draft on the tenant, not uploaded and not deployed:", len(stats.SkippedDrafts))
	for _, d := range stats.SkippedDrafts {
		log.Warn().Msgf("  - %s (package %s, tenant %s, running %s, repository %s)", d.ID, d.Package, d.Designtime, cmp.Or(d.Running, "-"), cmp.Or(d.LocalVersion, "-"))
	}
}

// logPlan prints the plan and returns an error when anything would fail.
func logPlan(items []PlanItem) error {
	uploads, deploys, failures, drafts := 0, 0, 0, 0
	for _, it := range items {
		if it.Upload == StatusSkippedDraft {
			drafts++
		}
		if it.Upload == "create" || it.Upload == "update" {
			uploads++
		}
		if it.Deploy {
			deploys++
		}
		what := cmp.Or(it.Upload, "-")
		dep := "no deploy"
		if it.Deploy {
			dep = "DEPLOY"
		}
		ev := log.Info()
		if it.Error != "" {
			failures++
			ev = log.Error()
		}
		ev.Msgf("[PLAN] %-40s upload: %-13s %-9s %s%s", it.Artifact, what, dep, it.Reason, map[bool]string{true: " ❌ " + it.Error, false: ""}[it.Error != ""])
	}
	log.Info().Msgf("[PLAN] %d artifact(s): %d upload(s), %d deployment(s), %d failure(s), %d skipped (draft)", len(items), uploads, deploys, failures, drafts)
	if failures > 0 {
		return output.Failed(fmt.Errorf("plan: %d artifact(s) would fail", failures))
	}
	return nil
}

// baselinePath is the snapshot state used instead of downloads: the
// --snapshot-state file, else .cpi/snapshot-state.json in the current
// directory or a parent of packagesDir ("" for none). A state of another
// tenant is not used (ops.OrchestratorOptions.Host).
func baselinePath(cmd *cobra.Command, packagesDir string) (string, error) {
	path := config.GetStringWithFallback(cmd, "snapshot-state", "orchestrator.snapshotState")
	if path == "off" {
		return "", nil
	}
	if path != "" {
		if !deploy.FileExists(path) {
			return "", output.Usagef("--snapshot-state %s does not exist", path)
		}
		return path, nil
	}
	candidates := []string{filepath.Join(".cpi", "snapshot-state.json")}
	if abs, err := filepath.Abs(packagesDir); err == nil {
		for dir := abs; ; dir = filepath.Dir(dir) {
			candidates = append(candidates, filepath.Join(dir, ".cpi", "snapshot-state.json"))
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	for _, c := range candidates {
		if deploy.FileExists(c) {
			return c, nil
		}
	}
	log.Info().Msg("No snapshot state found: existing artifacts are downloaded for the comparison (run snapshot first to skip that)")
	return "", nil
}

func parseFilter(filterStr string) []string {
	if filterStr == "" {
		return nil
	}
	parts := strings.Split(filterStr, ",")
	var result []string
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func shouldInclude(id string, filter []string) bool {
	if len(filter) == 0 {
		return true
	}
	return slices.Contains(filter, id)
}

func printSummary(stats *ProcessingStats) {
	log.Info().Msg("")
	log.Info().Msg("═══════════════════════════════════════════════════════════════════════")
	log.Info().Msg("📊 DEPLOYMENT SUMMARY")
	log.Info().Msg("═══════════════════════════════════════════════════════════════════════")
	log.Info().Msgf("Packages Updated:   %d", stats.PackagesUpdated)
	log.Info().Msgf("Packages Deployed:  %d", stats.PackagesDeployed)
	log.Info().Msgf("Packages Failed:    %d", stats.PackagesFailed)
	log.Info().Msgf("Packages Filtered:  %d", stats.PackagesFiltered)
	log.Info().Msg("───────────────────────────────────────────────────────────────────────")
	log.Info().Msgf("Artifacts Total:         %d", stats.ArtifactsTotal)
	log.Info().Msgf("Artifacts Updated:       %d (%d changed, %d unchanged)", len(stats.SuccessfulArtifactUpdates), stats.ArtifactsChanged, stats.ArtifactsUnchanged)
	if stats.ComparedWithSnapshot+stats.DownloadedForComparison > 0 {
		log.Info().Msgf("Compared:                %d with the snapshot, %d downloaded", stats.ComparedWithSnapshot, stats.DownloadedForComparison)
	}
	log.Info().Msgf("Artifacts Deployed OK:   %d", stats.ArtifactsDeployedSuccess)
	log.Info().Msgf("Artifacts Deployed Fail: %d", stats.ArtifactsDeployedFailed)
	log.Info().Msgf("Artifacts Filtered:      %d", stats.ArtifactsFiltered)
	if stats.ArtifactsSkippedDraft > 0 {
		log.Info().Msgf("Skipped (draft):         %d", stats.ArtifactsSkippedDraft)
	}
	log.Info().Msg("───────────────────────────────────────────────────────────────────────")
	logSkippedDrafts(stats)

	if stats.UpdateFailures > 0 {
		log.Warn().Msgf("⚠ Update Failures: %d", stats.UpdateFailures)
		log.Info().Msg("Failed Artifact Updates:")
		for artifactID := range stats.FailedArtifactUpdates {
			log.Info().Msgf("  - %s", artifactID)
		}
	}

	if stats.DeployFailures > 0 {
		log.Warn().Msgf("⚠ Deploy Failures: %d", stats.DeployFailures)
		log.Info().Msg("Failed Artifact Deployments:")
		for artifactID := range stats.FailedArtifactDeploys {
			log.Info().Msgf("  - %s", artifactID)
		}
	}

	if stats.UpdateFailures == 0 && stats.DeployFailures == 0 && stats.DraftFailures() == 0 {
		log.Info().Msg("✓ All operations completed successfully!")
	}

	log.Info().Msg("═══════════════════════════════════════════════════════════════════════")
}
