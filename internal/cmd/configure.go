package cmd

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/deploy"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/models"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/cpars-innovation/cpicli/pkg/versioning"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// ConfigureStats tracks configuration processing statistics
type ConfigureStats struct {
	PackagesProcessed         int `json:"packagesProcessed"`
	PackagesWithErrors        int `json:"packagesWithErrors"`
	ArtifactsProcessed        int `json:"artifactsProcessed"`
	ArtifactsConfigured       int `json:"artifactsConfigured"`
	ArtifactsDeployed         int `json:"artifactsDeployed"`
	ArtifactsFailed           int `json:"artifactsFailed"`
	ParametersUpdated         int `json:"parametersUpdated"`
	ParametersUnchanged       int `json:"parametersUnchanged"`
	ArtifactsUnchanged        int `json:"artifactsUnchanged"`
	ParametersFailed          int `json:"parametersFailed"`
	BatchRequestsExecuted     int `json:"batchRequestsExecuted"`
	IndividualRequestsUsed    int `json:"individualRequestsUsed"`
	DeploymentTasksQueued     int `json:"deploymentTasksQueued"`
	DeploymentTasksSuccessful int `json:"deploymentTasksSuccessful"`
	DeploymentTasksFailed     int `json:"deploymentTasksFailed"`
}

// ConfigurationTask represents a configuration update task
type ConfigurationTask struct {
	PackageID   string
	ArtifactID  string
	Version     string
	Parameters  []models.ConfigurationParameter
	UseBatch    bool
	BatchSize   int
	DisplayName string
}

func NewConfigureCommand() *cobra.Command {
	var (
		configPath          string
		deploymentPrefix    string
		packageFilter       string
		artifactFilter      string
		dryRun              bool
		deployRetries       int
		deployDelaySeconds  int
		parallelDeployments int
		batchSize           int
		disableBatch        bool
		force               bool
		offline             bool
	)

	configureCmd := &cobra.Command{
		Use:          "configure",
		Short:        "Set artifact parameters from YAML files and optionally deploy",
		SilenceUsage: true,
		Long: `Set externalised parameters of many artifacts from YAML files and optionally
deploy them afterwards.

Phase 1 compares each artifact's parameters with the tenant and writes only the
ones that differ (OData $batch by default, falling back to single requests); an
unknown key fails the artifact before anything is written. Phase 2 deploys the
artifacts marked with deploy: true that had a change, package by package with up
to --parallel-deployments concurrent deployments. --force writes and deploys
everything as before.

--config-path accepts a file or a folder (all *.yml/*.yaml files, not
recursive). Generate files with the current tenant values with
'cpictl configure pull'. File format: docs/configure.md.

All flags can be set in the config file under 'configure'.`,
		Example: `  # Configure artifacts from a config file
  cpictl configure --config-path ./config/dev-config.yml

  # Configure and deploy
  cpictl configure --config-path ./config/prod-config.yml

  # Dry run: compare with the tenant (what would be written and deployed)
  cpictl configure --config-path ./config.yml --dry-run

  # Only show the file, without reading the tenant
  cpictl configure --config-path ./config.yml --dry-run --offline

  # Write everything and redeploy, even if the tenant has the values
  cpictl configure --config-path ./config.yml --force

  # Apply deployment prefix
  cpictl configure --config-path ./config.yml --deployment-prefix DEV_

  # Disable batch processing
  cpictl configure --config-path ./config.yml --disable-batch`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load from viper config if available (CLI flags override config file)
			if !cmd.Flags().Changed("config-path") && viper.IsSet("configure.configPath") {
				configPath = viper.GetString("configure.configPath")
			}
			if !cmd.Flags().Changed("deployment-prefix") && viper.IsSet("configure.deploymentPrefix") {
				deploymentPrefix = viper.GetString("configure.deploymentPrefix")
			}
			if !cmd.Flags().Changed("package-filter") && viper.IsSet("configure.packageFilter") {
				packageFilter = viper.GetString("configure.packageFilter")
			}
			if !cmd.Flags().Changed("artifact-filter") && viper.IsSet("configure.artifactFilter") {
				artifactFilter = viper.GetString("configure.artifactFilter")
			}
			if !cmd.Flags().Changed("dry-run") && viper.IsSet("configure.dryRun") {
				dryRun = viper.GetBool("configure.dryRun")
			}
			if !cmd.Flags().Changed("deploy-retries") && viper.IsSet("configure.deployRetries") {
				deployRetries = viper.GetInt("configure.deployRetries")
			}
			if !cmd.Flags().Changed("deploy-delay") && viper.IsSet("configure.deployDelaySeconds") {
				deployDelaySeconds = viper.GetInt("configure.deployDelaySeconds")
			}
			if !cmd.Flags().Changed("parallel-deployments") && viper.IsSet("configure.parallelDeployments") {
				parallelDeployments = viper.GetInt("configure.parallelDeployments")
			}
			if !cmd.Flags().Changed("batch-size") && viper.IsSet("configure.batchSize") {
				batchSize = viper.GetInt("configure.batchSize")
			}
			if !cmd.Flags().Changed("disable-batch") && viper.IsSet("configure.disableBatch") {
				disableBatch = viper.GetBool("configure.disableBatch")
			}

			// Validate required parameters
			if configPath == "" {
				return output.Usagef("--config-path is required (set via CLI flag or in config file under 'configure.configPath')")
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
			if batchSize == 0 {
				batchSize = httpclnt.DefaultBatchSize
			}

			if offline && !dryRun {
				return output.Usagef("--offline only works with --dry-run")
			}
			// --allow-downgrade, else configure.allowDowngrade, else the
			// deploy command's deploy.allowDowngrade
			allowDowngrade, _ := cmd.Flags().GetBool("allow-downgrade")
			if !cmd.Flags().Changed("allow-downgrade") {
				switch {
				case viper.IsSet("configure.allowDowngrade"):
					allowDowngrade = viper.GetBool("configure.allowDowngrade")
				case viper.IsSet("deploy.allowDowngrade"):
					allowDowngrade = viper.GetBool("deploy.allowDowngrade")
				}
			}
			versionMode, err := versioningMode(cmd)
			if err != nil {
				return err
			}
			return runConfigure(cmd, configPath, deploymentPrefix, packageFilter, artifactFilter,
				configureMode{dryRun: dryRun, force: force, offline: offline, allowDowngrade: allowDowngrade, versioning: versionMode, parallel: config.GetIntWithFallback(cmd, "parallel", "configure.parallel")}, deployRetries, deployDelaySeconds, parallelDeployments, batchSize, disableBatch)
		},
	}

	// Flags
	configureCmd.Flags().StringVarP(&configPath, "config-path", "c", "", "Path to configuration YAML file (config: configure.configPath)")
	configureCmd.Flags().StringVarP(&deploymentPrefix, "deployment-prefix", "p", "", "Deployment prefix for artifact IDs (config: configure.deploymentPrefix)")
	configureCmd.Flags().StringVar(&packageFilter, "package-filter", "", "Comma-separated list of packages to include (config: configure.packageFilter)")
	configureCmd.Flags().StringVar(&artifactFilter, "artifact-filter", "", "Comma-separated list of artifacts to include (config: configure.artifactFilter)")
	configureCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be done without making changes, including which artifacts would be deployed and why (config: configure.dryRun)")
	configureCmd.Flags().BoolVar(&dryRun, "plan", false, "Same as --dry-run")
	addDeferFlags(configureCmd)
	configureCmd.Flags().Int("parallel", 8, "Artifacts whose parameters are read at the same time (config: configure.parallel)")
	configureCmd.Flags().IntVar(&deployRetries, "deploy-retries", defaultDeployChecks, "Deployment status checks per artifact (config: configure.deployRetries)")
	configureCmd.Flags().IntVar(&deployDelaySeconds, "deploy-delay", defaultDeployDelaySeconds, "Seconds between deployment status checks (config: configure.deployDelaySeconds)")
	configureCmd.Flags().IntVar(&parallelDeployments, "parallel-deployments", defaultParallelDeployments, "Deployments at the same time per package (config: configure.parallelDeployments)")
	configureCmd.Flags().IntVar(&batchSize, "batch-size", httpclnt.DefaultBatchSize, "Parameters per batch request (config: configure.batchSize)")
	configureCmd.Flags().BoolVar(&force, "force", false, "Write all parameters and deploy all marked artifacts, even if the tenant already has the values")
	configureCmd.Flags().BoolVar(&offline, "offline", false, "With --dry-run: only show the file contents, do not read the tenant")
	configureCmd.Flags().BoolVar(&disableBatch, "disable-batch", false, "Disable batch processing, use individual requests (config: configure.disableBatch)")
	addVersioningFlag(configureCmd)
	configureCmd.Flags().Bool("allow-downgrade", false, "Deploy designtime versions older than the running ones, for artifacts and packages without allowDowngrade in the file (config: configure.allowDowngrade, else deploy.allowDowngrade)")
	configureCmd.AddCommand(NewConfigurePullCommand())

	return configureCmd
}

// configureMode selects how configure compares and writes.
type configureMode struct {
	dryRun bool
	// force writes every parameter and deploys every marked artifact;
	// otherwise only parameters that differ from the tenant are written and
	// only artifacts with a change are deployed.
	force bool
	// offline (dry run only) does not read the tenant.
	offline bool
	// allowDowngrade is the default for artifacts and packages without
	// allowDowngrade in the configure file.
	allowDowngrade bool
	// parallel is the number of parameter reads at the same time.
	parallel int
	// versioning is the default for artifacts and packages without
	// versioning in the configure file.
	versioning versioning.Mode
}

func runConfigure(cmd *cobra.Command, configPath, deploymentPrefix, packageFilterStr, artifactFilterStr string,
	mode configureMode, deployRetries, deployDelaySeconds, parallelDeployments, batchSize int, disableBatch bool) error {
	dryRun := mode.dryRun

	log.Info().Msg("Starting artifact configuration")

	// Validate deployment prefix
	if deploymentPrefix != "" {
		if err := deploy.ValidateDeploymentPrefix(deploymentPrefix); err != nil {
			return output.Usage(err)
		}
	}

	// Parse filters
	packageFilter := parseFilter(packageFilterStr)
	artifactFilter := parseFilter(artifactFilterStr)

	// Load configuration from file or folder
	log.Info().Msgf("Loading configuration from: %s", configPath)
	configFiles, err := ops.LoadConfigureFiles(configPath)
	if err != nil {
		return output.Usage(fmt.Errorf("failed to load configuration: %w", err))
	}

	log.Info().Msgf("Loaded %d configuration file(s)", len(configFiles))
	log.Info().Msgf("Deployment prefix: %s", deploymentPrefix)
	log.Info().Msgf("Dry run: %v", dryRun)
	log.Info().Msgf("Batch processing: %v (size: %d)", !disableBatch, batchSize)

	// Merge all configurations
	configData := ops.MergeConfigureFiles(configFiles, deploymentPrefix)

	// Apply deployment prefix if specified
	if deploymentPrefix != "" {
		configData.DeploymentPrefix = deploymentPrefix
	}

	// Initialize stats
	stats := &ConfigureStats{}

	// Get service details
	serviceDetails := serviceDetails(cmd)
	exe := cpi.InitHTTPExecuter(serviceDetails)

	// Phase 1: Configure all artifacts
	log.Info().Msg("")
	log.Info().Msg("═══════════════════════════════════════════════════════════════════════")
	log.Info().Msg("PHASE 1: CONFIGURING ARTIFACTS")
	log.Info().Msg("═══════════════════════════════════════════════════════════════════════")

	deploymentTasks, diff, err := configureAllArtifacts(exe, configData, packageFilter, artifactFilter,
		stats, mode, batchSize, disableBatch)
	if err != nil {
		return err
	}

	// Phase 2: Deploy artifacts if requested
	deployments := []ops.Result{}
	deferDeploy, _ := cmd.Flags().GetBool("defer-deploy")
	if deferDeploy && !dryRun {
		if len(deploymentTasks) > 0 {
			err := deferDeployments(pendingFile(cmd), serviceDetails.Host, deploymentTasks, func(t DeploymentTask) string {
				if t.Force {
					return "configure: configuration changed"
				}
				return "configure: deploy if the runtime lacks the designtime version"
			})
			if err != nil {
				return err
			}
		}
	} else if len(deploymentTasks) > 0 && !dryRun {
		log.Info().Msg("")
		log.Info().Msg("═══════════════════════════════════════════════════════════════════════")
		log.Info().Msg("PHASE 2: DEPLOYING CONFIGURED ARTIFACTS")
		log.Info().Msg("═══════════════════════════════════════════════════════════════════════")
		log.Info().Msgf("Deploying %d artifacts with max %d parallel deployments per package",
			len(deploymentTasks), parallelDeployments)

		deployments = deployConfiguredArtifacts(cmd.Context(), exe, deploymentTasks, deployRetries, deployDelaySeconds,
			parallelDeployments, stats)
	}

	// dry run: what the deployment phase would do
	var plan []PlanItem
	if dryRun && !mode.offline && len(deploymentTasks) > 0 {
		plan = ops.PlanDeployments(exe, deploymentTasks)
		if err := logPlan(plan); err != nil {
			log.Warn().Msg(err.Error())
		}
	}

	// Print summary
	printConfigureSummary(stats, dryRun)
	output.SetResult(cmd.Context(), configureResult{DryRun: dryRun, Stats: stats, Deployments: deployments, Diff: diff, Plan: plan})

	// Return error if there were failures
	if stats.ArtifactsFailed > 0 || stats.DeploymentTasksFailed > 0 {
		err := fmt.Errorf("configuration/deployment completed with errors")
		if stats.ArtifactsConfigured > 0 || stats.DeploymentTasksSuccessful > 0 {
			return output.Partial(err)
		}
		return output.Failed(err)
	}

	return nil
}

// configureResult is the JSON result of configure.
type configureResult struct {
	DryRun      bool            `json:"dryRun"`
	Stats       *ConfigureStats `json:"stats"`
	Deployments []ops.Result    `json:"deployments"`
	// Diff compares every parameter with the tenant (not with --force or
	// --offline).
	Diff []ops.ConfigDiffItem `json:"diff,omitempty"`
	// Plan is what the deployment phase would do (dry run).
	Plan []PlanItem `json:"plan,omitempty"`
}

type diffResult struct {
	diff []ops.ConfigDiffItem
	err  error
}

// prefetchConfigDiffs reads the current parameters of every artifact that
// will be compared, mode.parallel at a time; the sequential pass then only
// writes. Keyed by the final artifact ID.
func prefetchConfigDiffs(exe *httpclnt.HTTPExecuter, cfg *models.ConfigureConfig, packageFilter, artifactFilter []string,
	mode configureMode) map[string]diffResult {
	out := map[string]diffResult{}
	if mode.force || mode.offline || mode.parallel <= 1 {
		return out
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, mode.parallel)
	for _, pkg := range cfg.Packages {
		if len(packageFilter) > 0 && !shouldInclude(pkg.ID, packageFilter) {
			continue
		}
		packageID := cfg.DeploymentPrefix + pkg.ID
		for _, artifact := range pkg.Artifacts {
			if len(artifactFilter) > 0 && !shouldInclude(artifact.ID, artifactFilter) {
				continue
			}
			if len(artifact.Parameters) == 0 || !cpi.IsValidArtifactType(artifact.Type) || ops.CheckConfigurable(artifact.Type, artifact.Parameters) != nil {
				continue
			}
			artifactID := cfg.DeploymentPrefix + artifact.ID
			artifact := artifact
			wg.Add(1)
			go func() {
				defer wg.Done()
				slots <- struct{}{}
				defer func() { <-slots }()
				diff, err := ops.DiffArtifactConfig(exe, packageID, artifactID, artifact.Version, artifact.Parameters)
				mu.Lock()
				out[artifactID] = diffResult{diff, err}
				mu.Unlock()
			}()
		}
	}
	wg.Wait()
	return out
}

func configureAllArtifacts(exe *httpclnt.HTTPExecuter, cfg *models.ConfigureConfig,
	packageFilter, artifactFilter []string, stats *ConfigureStats, mode configureMode,
	batchSize int, disableBatch bool) ([]DeploymentTask, []ops.ConfigDiffItem, error) {

	dryRun := mode.dryRun
	var deploymentTasks []DeploymentTask
	var allDiff []ops.ConfigDiffItem
	configuration := cpi.NewConfiguration(exe)
	prefetched := prefetchConfigDiffs(exe, cfg, packageFilter, artifactFilter, mode)

	for _, pkg := range cfg.Packages {
		stats.PackagesProcessed++

		// Apply deployment prefix to package ID
		packageID := pkg.ID
		if cfg.DeploymentPrefix != "" {
			packageID = cfg.DeploymentPrefix + packageID
		}

		// Apply package filter
		if len(packageFilter) > 0 && !shouldInclude(pkg.ID, packageFilter) {
			log.Info().Msgf("Skipping package %s (filtered out)", packageID)
			continue
		}

		log.Info().Msg("")
		log.Info().Msgf("📦 Processing package: %s", packageID)
		if pkg.DisplayName != "" {
			log.Info().Msgf("   Display Name: %s", pkg.DisplayName)
		}

		packageHasError := false

		for _, artifact := range pkg.Artifacts {
			stats.ArtifactsProcessed++

			// Apply deployment prefix to artifact ID
			artifactID := artifact.ID
			if cfg.DeploymentPrefix != "" {
				artifactID = cfg.DeploymentPrefix + artifactID
			}

			// Apply artifact filter
			if len(artifactFilter) > 0 && !shouldInclude(artifact.ID, artifactFilter) {
				log.Info().Msgf("   Skipping artifact %s (filtered out)", artifactID)
				continue
			}

			log.Info().Msg("")
			log.Info().Msgf("   🔧 Configuring artifact: %s", artifactID)
			if artifact.DisplayName != "" {
				log.Info().Msgf("      Display Name: %s", artifact.DisplayName)
			}
			log.Info().Msgf("      Type: %s", artifact.Type)
			log.Info().Msgf("      Version: %s", artifact.Version)
			log.Info().Msgf("      Parameters: %d", len(artifact.Parameters))

			// Validate artifact type
			if !cpi.IsValidArtifactType(artifact.Type) {
				log.Error().Msgf("      ❌ Invalid artifact type: %s (valid types: %v)", artifact.Type, cpi.ArtifactTypes)
				stats.ArtifactsFailed++
				packageHasError = true
				continue
			}

			if err := ops.CheckConfigurable(artifact.Type, artifact.Parameters); err != nil {
				log.Error().Msgf("      ❌ %v", err)
				stats.ArtifactsFailed++
				packageHasError = true
				continue
			}
			deploy := artifact.Deploy || pkg.Deploy
			allowDowngrade := models.EffectiveAllowDowngrade(pkg, artifact, mode.allowDowngrade)
			versionMode, err := resolveVersioning(artifact.Versioning, pkg.Versioning, mode.versioning)
			if err != nil {
				log.Error().Msgf("      ❌ %v", err)
				stats.ArtifactsFailed++
				packageHasError = true
				continue
			}

			// nothing to configure (script collections, mappings, flows without
			// parameters): never read or write parameters, deploy when asked
			// and the runtime does not run the designtime version yet
			if len(artifact.Parameters) == 0 {
				log.Info().Msg("      No parameters: nothing to configure")
				stats.ArtifactsUnchanged++
				if deploy {
					stats.DeploymentTasksQueued++
					if dryRun {
						log.Info().Msg("      [DRY RUN] Would deploy unless the runtime already has the designtime version")
					}
					deploymentTasks = append(deploymentTasks, DeploymentTask{ArtifactID: artifactID, ArtifactType: artifact.Type,
						PackageID: packageID, DisplayName: artifact.DisplayName, AllowDowngrade: allowDowngrade, Versioning: versionMode})
					log.Info().Msg("      📋 Queued for deployment (skipped if the runtime already has the designtime version)")
				}
				continue
			}

			parameters := artifact.Parameters
			if !mode.force && !mode.offline {
				r, ok := prefetched[artifactID]
				if !ok {
					r.diff, r.err = ops.DiffArtifactConfig(exe, packageID, artifactID, artifact.Version, artifact.Parameters)
				}
				diff, err := r.diff, r.err
				if err != nil {
					log.Error().Msgf("      ❌ Failed to read the current parameters: %v", err)
					stats.ArtifactsFailed++
					packageHasError = true
					continue
				}
				allDiff = append(allDiff, diff...)
				var changed, unknown []string
				parameters = nil
				for i, d := range diff {
					switch d.Change {
					case ops.ConfigUpdate:
						changed = append(changed, d.Key)
						parameters = append(parameters, artifact.Parameters[i])
						log.Info().Msgf("        ~ %s: %q -> %q", d.Key, d.Tenant, d.Local)
					case ops.ConfigUnknownKey:
						unknown = append(unknown, d.Key)
					default:
						stats.ParametersUnchanged++
					}
				}
				if len(unknown) > 0 {
					// a typo must not leave a half-applied configuration
					log.Error().Msgf("      ❌ Unknown parameter key(s): %s; nothing written for this artifact", strings.Join(unknown, ", "))
					stats.ArtifactsFailed++
					stats.ParametersFailed += len(unknown)
					packageHasError = true
					continue
				}
				if len(changed) == 0 {
					log.Info().Msg("      ✅ Tenant already has these values: nothing to write")
					stats.ArtifactsUnchanged++
					// a newer designtime version still needs a deployment
					if deploy {
						stats.DeploymentTasksQueued++
						if dryRun {
							log.Info().Msg("      [DRY RUN] Would deploy unless the runtime already has the designtime version")
						}
						deploymentTasks = append(deploymentTasks, DeploymentTask{ArtifactID: artifactID, ArtifactType: artifact.Type,
							PackageID: packageID, DisplayName: artifact.DisplayName, AllowDowngrade: allowDowngrade, Versioning: versionMode})
						log.Info().Msg("      📋 Queued for deployment (skipped if the runtime already has the designtime version)")
					}
					continue
				}
			}

			if dryRun {
				log.Info().Msg("      [DRY RUN] Would update the following parameters:")
				for _, param := range parameters {
					log.Info().Msgf("        - %s = %s", param.Key, param.Value)
				}
				stats.ArtifactsConfigured++
				stats.ParametersUpdated += len(parameters)

				// Queue for deployment if requested
				if deploy {
					stats.DeploymentTasksQueued++
					log.Info().Msgf("      [DRY RUN] Would deploy after configuration")
					deploymentTasks = append(deploymentTasks, DeploymentTask{ArtifactID: artifactID, ArtifactType: artifact.Type,
						PackageID: packageID, DisplayName: artifact.DisplayName, Force: true, AllowDowngrade: allowDowngrade, Versioning: versionMode})
				}
				continue
			}

			// Remember when the designtime artifact was last changed before the
			// parameters are written (a parameter change may count as one):
			// the downgrade rule compares it with the runtime deployment time.
			var modifiedAt *time.Time
			if deploy && len(parameters) > 0 {
				var t time.Time
				if info, exists, err := cpi.GetDesigntimeInfo(exe, artifact.Type, artifactID, "active"); err == nil && exists {
					t = info.ModifiedAt
				}
				modifiedAt = &t
			}

			// Determine batch settings
			useBatch := !disableBatch
			effectiveBatchSize := batchSize

			if artifact.Batch != nil {
				useBatch = artifact.Batch.Enabled && !disableBatch
				if artifact.Batch.BatchSize > 0 {
					effectiveBatchSize = artifact.Batch.BatchSize
				}
			}

			// Update configuration parameters
			var configErr error
			if useBatch && len(parameters) > 0 {
				configErr = updateParametersBatch(exe, configuration, artifactID, artifact.Version,
					parameters, effectiveBatchSize, stats)
			} else {
				configErr = updateParametersIndividual(configuration, artifactID, artifact.Version,
					parameters, stats)
			}

			if configErr != nil {
				log.Error().Msgf("      ❌ Failed to configure artifact: %v", configErr)
				stats.ArtifactsFailed++
				packageHasError = true
				continue
			}

			stats.ArtifactsConfigured++
			log.Info().Msgf("      ✅ Successfully configured %d parameters", len(parameters))

			// Queue for deployment if requested; the configuration change does
			// not change the version, so the deployment is forced
			if deploy {
				deploymentTasks = append(deploymentTasks, DeploymentTask{
					ArtifactID:   artifactID,
					ArtifactType: artifact.Type,
					PackageID:    packageID,
					DisplayName:  artifact.DisplayName,
					Force:        true,
					// the designtime change time from before the parameters were
					// written: writing them may count as a change
					AllowDowngrade: allowDowngrade,
					ModifiedAt:     modifiedAt,
					Versioning:     versionMode,
				})
				stats.DeploymentTasksQueued++
				log.Info().Msgf("      📋 Queued for deployment")
			}
		}

		if packageHasError {
			stats.PackagesWithErrors++
		}
	}

	return deploymentTasks, allDiff, nil
}

func updateParametersBatch(exe *httpclnt.HTTPExecuter, configuration *cpi.Configuration,
	artifactID, version string, parameters []models.ConfigurationParameter,
	batchSize int, stats *ConfigureStats) error {

	log.Info().Msgf("      Using batch operations (batch size: %d)", batchSize)

	// Get current configuration to verify parameters exist
	currentConfig, err := configuration.Get(artifactID, version)
	if err != nil {
		return fmt.Errorf("failed to get current configuration: %w", err)
	}

	// Build batch request
	batch := exe.NewBatchRequest()
	validParams := 0

	for _, param := range parameters {
		// Verify parameter exists
		existingParam := cpi.FindParameterByKey(param.Key, currentConfig.Root.Results)
		if existingParam == nil {
			log.Warn().Msgf("      ⚠️  Parameter %s not found in artifact, skipping", param.Key)
			stats.ParametersFailed++
			continue
		}

		// Add to batch
		requestBody := fmt.Sprintf(`{"ParameterValue":"%s"}`, escapeJSON(param.Value))
		urlPath := fmt.Sprintf("/api/v1/IntegrationDesigntimeArtifacts(Id=%s,Version=%s)/$links/Configurations(%s)",
			cpi.PathKey(artifactID), cpi.PathKey(version), cpi.PathKey(param.Key))

		log.Debug().Msgf("      Adding batch operation: %s %s", "PUT", urlPath)

		batch.AddOperation(httpclnt.BatchOperation{
			Method:    "PUT",
			Path:      urlPath,
			Body:      []byte(requestBody),
			ContentID: fmt.Sprintf("param_%d", validParams),
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
		})
		validParams++
	}

	if validParams == 0 {
		return fmt.Errorf("no valid parameters to update")
	}

	// Execute batch in chunks
	log.Debug().Msgf("      Executing batch request with %d parameters (batch size: %d)", validParams, batchSize)
	resp, err := batch.ExecuteInBatches(batchSize)
	if err != nil {
		log.Warn().Msgf("      ⚠️  Batch operation failed: %v, falling back to individual requests", err)
		log.Debug().Msgf("      Batch failure likely due to SAP CPI API compatibility. Consider using --disable-batch flag or batch.enabled=false in config")
		return updateParametersIndividual(configuration, artifactID, version, parameters, stats)
	}

	stats.BatchRequestsExecuted++

	// Process batch results
	successCount := 0
	failCount := 0

	for _, opResp := range resp.Operations {
		if opResp.Error != nil {
			failCount++
			stats.ParametersFailed++
		} else if opResp.StatusCode >= 200 && opResp.StatusCode < 300 {
			successCount++
			stats.ParametersUpdated++
		} else {
			failCount++
			stats.ParametersFailed++
		}
	}

	if failCount > 0 {
		return fmt.Errorf("%d parameters failed to update in batch", failCount)
	}

	return nil
}

func updateParametersIndividual(configuration *cpi.Configuration, artifactID, version string,
	parameters []models.ConfigurationParameter, stats *ConfigureStats) error {

	log.Info().Msgf("      Using individual requests")

	failCount := 0
	successCount := 0

	for _, param := range parameters {
		err := configuration.Update(artifactID, version, param.Key, param.Value)
		if err != nil {
			log.Error().Msgf("      ❌ Failed to update parameter %s: %v", param.Key, err)
			stats.ParametersFailed++
			failCount++
		} else {
			stats.ParametersUpdated++
			stats.IndividualRequestsUsed++
			successCount++
		}
	}

	if failCount > 0 {
		return fmt.Errorf("%d parameters failed to update", failCount)
	}

	return nil
}

func deployConfiguredArtifacts(ctx context.Context, exe *httpclnt.HTTPExecuter, tasks []DeploymentTask,
	deployRetries, deployDelaySeconds, parallelDeployments int, stats *ConfigureStats) []ops.Result {

	// Tasks with configuration changes are forced (the version does not
	// change); the others are skipped when the runtime has the version.
	results := deployTasks(ctx, exe, tasks, true, deployRetries, deployDelaySeconds, parallelDeployments)
	emitResults(ctx, results)
	for _, r := range results {
		if r.Status.Succeeded() {
			stats.DeploymentTasksSuccessful++
			stats.ArtifactsDeployed++
		} else {
			stats.DeploymentTasksFailed++
		}
	}
	return results
}

func printConfigureSummary(stats *ConfigureStats, dryRun bool) {
	log.Info().Msg("")
	log.Info().Msg("═══════════════════════════════════════════════════════════════════════")
	if dryRun {
		log.Info().Msg("DRY RUN SUMMARY")
	} else {
		log.Info().Msg("CONFIGURATION SUMMARY")
	}
	log.Info().Msg("═══════════════════════════════════════════════════════════════════════")
	log.Info().Msgf("Packages processed:          %d", stats.PackagesProcessed)
	log.Info().Msgf("Packages with errors:        %d", stats.PackagesWithErrors)
	log.Info().Msgf("Artifacts processed:         %d", stats.ArtifactsProcessed)
	log.Info().Msgf("Artifacts configured:        %d", stats.ArtifactsConfigured)
	log.Info().Msgf("Artifacts failed:            %d", stats.ArtifactsFailed)
	log.Info().Msgf("Parameters updated:          %d", stats.ParametersUpdated)
	log.Info().Msgf("Parameters failed:           %d", stats.ParametersFailed)

	if !dryRun {
		log.Info().Msg("")
		log.Info().Msg("Performance:")
		log.Info().Msgf("Batch requests executed:     %d", stats.BatchRequestsExecuted)
		log.Info().Msgf("Individual requests used:    %d", stats.IndividualRequestsUsed)
	}

	if stats.DeploymentTasksQueued > 0 {
		log.Info().Msg("")
		log.Info().Msg("Deployment:")
		log.Info().Msgf("Deployment tasks queued:     %d", stats.DeploymentTasksQueued)
		if !dryRun {
			log.Info().Msgf("Deployments successful:      %d", stats.DeploymentTasksSuccessful)
			log.Info().Msgf("Deployments failed:          %d", stats.DeploymentTasksFailed)
			log.Info().Msgf("Artifacts deployed:          %d", stats.ArtifactsDeployed)
		}
	}

	log.Info().Msg("═══════════════════════════════════════════════════════════════════════")

	if stats.ArtifactsFailed > 0 || stats.DeploymentTasksFailed > 0 {
		log.Error().Msg("❌ Configuration/Deployment completed with errors")
	} else if dryRun {
		log.Info().Msg("✅ Dry run completed successfully")
	} else {
		log.Info().Msg("✅ Configuration/Deployment completed successfully")
	}
}

func escapeJSON(s string) string {
	// Simple JSON string escaping
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}
