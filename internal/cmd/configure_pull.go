package cmd

import (
	"errors"
	"fmt"
	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/models"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func NewConfigurePullCommand() *cobra.Command {
	var outputDir string
	var packageIDs []string

	cmd := &cobra.Command{
		Use:          "pull",
		Short:        "Write current tenant parameter values into configure YAML files",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			outputDir = config.GetStringWithFallback(cmd, "output-dir", "configure.pull.outputDir")
			packageIDs = config.GetStringSliceWithFallback(cmd, "package-ids", "configure.pull.packageIds")
			return runConfigurePull(cmd, outputDir, packageIDs)
		},
	}
	cmd.Flags().StringVarP(&outputDir, "output-dir", "o", ".", "Directory for one YAML file per package")
	cmd.Flags().StringSliceVar(&packageIDs, "package-ids", nil, "Package IDs to pull (default: all packages)")
	cmd.Flags().Int("parallel", 8, "Tenant reads at the same time (config: configure.pull.parallel)")
	return cmd
}

func runConfigurePull(cmd *cobra.Command, outputDir string, packageIDs []string) error {
	exe := cpi.InitHTTPExecuter(serviceDetails(cmd))
	packages := cpi.NewIntegrationPackage(exe)
	configuration := cpi.NewConfiguration(exe)

	availablePackageIDs, err := packages.GetPackagesList()
	if err != nil {
		return err
	}
	if len(packageIDs) == 0 {
		packageIDs = availablePackageIDs
	} else {
		var missing []string
		packageIDs, missing = filterExistingPackages(packageIDs, availablePackageIDs)
		for _, packageID := range missing {
			log.Warn().Msgf("Skipping package %s because it does not exist", packageID)
		}
	}
	sort.Strings(packageIDs)

	type pulledFile struct {
		path string
		data []byte
	}
	for _, packageID := range packageIDs {
		if packageID == "" || filepath.Base(packageID) != packageID || strings.ContainsAny(packageID, `<>:"/\|?*`) {
			return fmt.Errorf("invalid package ID %q", packageID)
		}
	}
	parallel := config.GetIntWithFallback(cmd, "parallel", "configure.pull.parallel")
	if parallel < 1 {
		return output.Usagef("--parallel must be at least 1")
	}
	// every tenant call takes one of the shared slots: packages and the
	// artifacts of a large package are read in parallel
	slots := make(chan struct{}, parallel)
	call := func(f func()) {
		slots <- struct{}{}
		defer func() { <-slots }()
		f()
	}
	outputs := make([]pulledFile, len(packageIDs))
	errs := make([]error, len(packageIDs))
	var wg sync.WaitGroup
	for i, packageID := range packageIDs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var artifacts []*cpi.ArtifactDetails
			var err error
			call(func() { artifacts, err = packages.GetArtifactsData(packageID, "Integration") })
			if err != nil {
				errs[i] = fmt.Errorf("get artifacts for package %s: %w", packageID, err)
				return
			}
			params := make(map[string]*cpi.ParametersData, len(artifacts))
			paramErrs := make(map[string]error)
			var mu sync.Mutex
			var awg sync.WaitGroup
			for _, a := range artifacts {
				awg.Add(1)
				go func() {
					defer awg.Done()
					var p *cpi.ParametersData
					var err error
					call(func() { p, err = configuration.Get(a.Id, "active") })
					mu.Lock()
					params[a.Id], paramErrs[a.Id] = p, err
					mu.Unlock()
				}()
			}
			awg.Wait()
			cfg, err := pulledConfigureConfig(packageID, artifacts, func(id, _ string) (*cpi.ParametersData, error) {
				return params[id], paramErrs[id]
			})
			if err != nil {
				errs[i] = err
				return
			}
			data, err := yaml.Marshal(cfg)
			if err != nil {
				errs[i] = err
				return
			}
			outputs[i] = pulledFile{filepath.Join(outputDir, packageID+".yml"), data}
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return err
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return err
	}
	for _, f := range outputs {
		if err := os.WriteFile(f.path, f.data, 0644); err != nil {
			return err
		}
		log.Info().Msgf("Configuration written to %s", f.path)
	}
	return nil
}

func filterExistingPackages(requested, available []string) (existing, missing []string) {
	availableSet := make(map[string]bool, len(available))
	for _, packageID := range available {
		availableSet[packageID] = true
	}
	for _, packageID := range requested {
		if availableSet[packageID] {
			existing = append(existing, packageID)
		} else {
			missing = append(missing, packageID)
		}
	}
	return
}

func pulledConfigureConfig(packageID string, artifacts []*cpi.ArtifactDetails, getConfiguration func(string, string) (*cpi.ParametersData, error)) (*models.ConfigureConfig, error) {
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Id < artifacts[j].Id })
	pkg := models.ConfigurePackage{ID: packageID}
	for _, artifact := range artifacts {
		parameters, err := getConfiguration(artifact.Id, "active")
		if err != nil {
			return nil, fmt.Errorf("get configuration for artifact %s: %w", artifact.Id, err)
		}
		sort.Slice(parameters.Root.Results, func(i, j int) bool {
			return parameters.Root.Results[i].ParameterKey < parameters.Root.Results[j].ParameterKey
		})
		pulled := models.ConfigureArtifact{ID: artifact.Id, Type: "Integration"}
		for _, parameter := range parameters.Root.Results {
			pulled.Parameters = append(pulled.Parameters, models.ConfigurationParameter{
				Key: parameter.ParameterKey, Value: parameter.ParameterValue,
			})
		}
		pkg.Artifacts = append(pkg.Artifacts, pulled)
	}
	return &models.ConfigureConfig{Packages: []models.ConfigurePackage{pkg}}, nil
}
