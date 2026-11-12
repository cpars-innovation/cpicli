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

	"github.com/cpars-innovation/cpicli/pkg/content"
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
	cmd.Flags().Bool("declared-only", false, "Write only the parameters each flow declares (its parameters.propdef; one download per flow): leaves out values the tenant keeps for parameters a newer flow version no longer has (config: configure.pull.declaredOnly)")
	return cmd
}

// ConfigurePullResult lists the files written and the parameters left out.
type ConfigurePullResult struct {
	Files []string `json:"files"`
	// LeftOut are parameters the tenant returned that the files do not get:
	// SAP's own (SAP_ProfileId), and with --declared-only those the flow no
	// longer declares.
	LeftOut []PulledLeftOut `json:"leftOut"`
}

// PulledLeftOut is one artifact's parameters left out of a pull.
type PulledLeftOut struct {
	Package  string   `json:"package"`
	Artifact string   `json:"artifact"`
	Keys     []string `json:"keys"`
	// Reason: "sap" (SAP's own SAP_* parameter, not declared by the flow) or
	// "undeclared" (the flow's parameters.propdef does not have it).
	Reason string `json:"reason"`
}

func runConfigurePull(cmd *cobra.Command, outputDir string, packageIDs []string) error {
	declaredOnly := config.GetBoolWithFallback(cmd, "declared-only", "configure.pull.declaredOnly")
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
	leftOut := make([][]PulledLeftOut, len(packageIDs))
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
			declared := make(map[string]map[string]bool, len(artifacts))
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
					var names map[string]bool
					if err == nil && declaredOnly {
						var data []byte
						call(func() { data, err = cpi.GetContent(exe, "Integration", a.Id, "active") })
						if err == nil {
							var ok bool
							if names, ok, err = content.PropdefNamesFromZip(data); err == nil && !ok {
								// no propdef to go by: keep what the tenant has rather
								// than drop every value (cpicli#89)
								log.Warn().Msgf("%s: no parameters.propdef in the flow, its parameters are written as the tenant has them", a.Id)
								names = nil
							}
						}
						if err != nil {
							err = fmt.Errorf("read the declared parameters of %s: %w", a.Id, err)
						}
					}
					mu.Lock()
					params[a.Id], paramErrs[a.Id], declared[a.Id] = p, err, names
					mu.Unlock()
				}()
			}
			awg.Wait()
			cfg, left, err := pulledConfigureConfig(packageID, artifacts, func(id, _ string) (*cpi.ParametersData, error) {
				return params[id], paramErrs[id]
			}, func(id string) map[string]bool { return declared[id] })
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
			leftOut[i] = left
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return err
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return err
	}
	res := ConfigurePullResult{Files: []string{}, LeftOut: []PulledLeftOut{}}
	for i, f := range outputs {
		if err := os.WriteFile(f.path, f.data, 0644); err != nil {
			return err
		}
		res.Files = append(res.Files, f.path)
		log.Info().Msgf("Configuration written to %s", f.path)
		for _, l := range leftOut[i] {
			res.LeftOut = append(res.LeftOut, l)
			what := "SAP's own"
			if l.Reason == "undeclared" {
				what = "not declared by the flow"
			}
			log.Info().Msgf("Left out of %s: %s (%s)", l.Artifact, strings.Join(l.Keys, ", "), what)
		}
	}
	output.SetResult(cmd.Context(), res)
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

// sapOwnParameters are the parameters SAP's Configurations API returns for
// a flow that are SAP's, not the flow's (SAP_ProfileId: the runtime
// profile). Only these are left out without reading the flow: another
// SAP_* name may be one the flow declares (cpicli#89).
var sapOwnParameters = map[string]bool{"SAP_ProfileId": true}

// pulledConfigureConfig builds a package's configure file. SAP's own
// parameters (sapOwnParameters) are left out unless the flow declares them:
// they are not the flow's configuration, and configure would refuse them
// (cpicli#87). declared gives an artifact's declared parameters (nil: not
// read, everything else is kept); a parameter it lacks is left out.
func pulledConfigureConfig(packageID string, artifacts []*cpi.ArtifactDetails, getConfiguration func(string, string) (*cpi.ParametersData, error),
	declared func(string) map[string]bool) (*models.ConfigureConfig, []PulledLeftOut, error) {
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Id < artifacts[j].Id })
	pkg := models.ConfigurePackage{ID: packageID}
	var left []PulledLeftOut
	for _, artifact := range artifacts {
		parameters, err := getConfiguration(artifact.Id, "active")
		if err != nil {
			return nil, nil, fmt.Errorf("get configuration for artifact %s: %w", artifact.Id, err)
		}
		names := declared(artifact.Id)
		var sap, undeclared []string
		kept := parameters.Root.Results[:0:0]
		for _, p := range parameters.Root.Results {
			switch {
			case names != nil && names[p.ParameterKey]:
				kept = append(kept, p)
			case sapOwnParameters[p.ParameterKey]:
				sap = append(sap, p.ParameterKey)
			case names != nil:
				undeclared = append(undeclared, p.ParameterKey)
			default:
				kept = append(kept, p)
			}
		}
		parameters.Root.Results = kept
		for reason, keys := range map[string][]string{"sap": sap, "undeclared": undeclared} {
			if len(keys) > 0 {
				sort.Strings(keys)
				left = append(left, PulledLeftOut{Package: packageID, Artifact: artifact.Id, Keys: keys, Reason: reason})
			}
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
	sort.Slice(left, func(i, j int) bool {
		return left[i].Artifact < left[j].Artifact || left[i].Artifact == left[j].Artifact && left[i].Reason < left[j].Reason
	})
	return &models.ConfigureConfig{Packages: []models.ConfigurePackage{pkg}}, left, nil
}
