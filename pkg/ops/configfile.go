package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/models"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

// ConfigureConfigFile is a loaded configure YAML file.
type ConfigureConfigFile struct {
	Config   *models.ConfigureConfig
	Source   string
	FileName string
}

// LoadConfigureFiles reads a configure YAML file or all *.yml/*.yaml files of
// a folder (not recursive).
func LoadConfigureFiles(path string) ([]*ConfigureConfigFile, error) {
	// Check if path is a file or directory
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to access path: %w", err)
	}

	if info.IsDir() {
		return loadConfigureFolder(path)
	}
	return loadConfigureFile(path)
}

func loadConfigureFile(path string) ([]*ConfigureConfigFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var cfg models.ConfigureConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	return []*ConfigureConfigFile{
		{
			Config:   &cfg,
			Source:   path,
			FileName: filepath.Base(path),
		},
	}, nil
}

func loadConfigureFolder(folderPath string) ([]*ConfigureConfigFile, error) {
	var configFiles []*ConfigureConfigFile

	entries, err := os.ReadDir(folderPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Match YAML files (*.yml, *.yaml)
		name := entry.Name()
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}

		filePath := filepath.Join(folderPath, name)
		data, err := os.ReadFile(filePath)
		if err != nil {
			log.Warn().Msgf("Failed to read config file %s: %v", name, err)
			continue
		}

		var cfg models.ConfigureConfig
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			log.Warn().Msgf("Failed to parse config file %s: %v", name, err)
			continue
		}

		configFiles = append(configFiles, &ConfigureConfigFile{
			Config:   &cfg,
			Source:   filePath,
			FileName: name,
		})
	}

	if len(configFiles) == 0 {
		return nil, fmt.Errorf("no valid configuration files found in folder: %s", folderPath)
	}

	log.Info().Msgf("Loaded %d configuration file(s) from folder", len(configFiles))
	return configFiles, nil
}

// MergeConfigureFiles merges the packages of all files; overridePrefix wins
// over the first file's deploymentPrefix.
func MergeConfigureFiles(configFiles []*ConfigureConfigFile, overridePrefix string) *models.ConfigureConfig {
	merged := &models.ConfigureConfig{
		Packages: []models.ConfigurePackage{},
	}

	// Use override prefix if provided, otherwise use first config's prefix
	if overridePrefix != "" {
		merged.DeploymentPrefix = overridePrefix
	} else if len(configFiles) > 0 && configFiles[0].Config.DeploymentPrefix != "" {
		merged.DeploymentPrefix = configFiles[0].Config.DeploymentPrefix
	}

	// Merge all packages from all config files
	for _, configFile := range configFiles {
		log.Info().Msgf("  Merging packages from: %s", configFile.FileName)
		merged.Packages = append(merged.Packages, configFile.Config.Packages...)
	}

	return merged
}

// Config diff changes.
const (
	ConfigUpdate     = "update"
	ConfigUnchanged  = "unchanged"
	ConfigUnknownKey = "unknown_key"
)

// ConfigDiffItem compares one parameter of a configure file with the tenant.
type ConfigDiffItem struct {
	PackageID  string `json:"packageId"`
	ArtifactID string `json:"artifactId"`
	Key        string `json:"key"`
	Local      string `json:"local"`
	Tenant     string `json:"tenant,omitempty"`
	Change     string `json:"change"`
}

// ConfigDiffResult is the outcome of ConfigDiff.
type ConfigDiffResult struct {
	Items   []ConfigDiffItem `json:"items"`
	Summary map[string]int   `json:"summary"`
	// Errors are artifacts whose parameters could not be read.
	Errors []string `json:"errors,omitempty"`
}

// ConfigFilter selects packages and artifacts by their IDs in the file
// (before the deployment prefix); empty selects all.
type ConfigFilter struct {
	Packages, Artifacts []string
}

func (f ConfigFilter) includes(list []string, id string) bool {
	return len(list) == 0 || slices.Contains(list, id)
}

// ConfigDiff compares the parameters of a configure file with the tenant:
// update (value differs), unchanged, or unknown_key (the artifact has no such
// parameter). IDs get cfg.DeploymentPrefix, as in configure.
func ConfigDiff(exe *httpclnt.HTTPExecuter, cfg *models.ConfigureConfig, f ConfigFilter) (*ConfigDiffResult, error) {
	res := &ConfigDiffResult{Items: []ConfigDiffItem{}, Summary: map[string]int{}}
	for _, pkg := range cfg.Packages {
		if !f.includes(f.Packages, pkg.ID) {
			continue
		}
		for _, a := range pkg.Artifacts {
			if !f.includes(f.Artifacts, a.ID) {
				continue
			}
			if err := CheckConfigurable(a.Type, a.Parameters); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", cfg.DeploymentPrefix+a.ID, err))
				continue
			}
			items, err := DiffArtifactConfig(exe, cfg.DeploymentPrefix+pkg.ID, cfg.DeploymentPrefix+a.ID, a.Version, a.Parameters)
			if err != nil {
				if httpclnt.IsAuthError(err) {
					return nil, err
				}
				res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", cfg.DeploymentPrefix+a.ID, err))
				continue
			}
			res.Items = append(res.Items, items...)
		}
	}
	for _, it := range res.Items {
		res.Summary[it.Change]++
	}
	if len(res.Errors) > 0 {
		return res, output.Partial(fmt.Errorf("%d artifact(s) could not be compared: %s", len(res.Errors), strings.Join(res.Errors, "; ")))
	}
	return res, nil
}

// CheckConfigurable reports parameters on an artifact type that has none:
// only integration flows have externalised parameters. Artifacts without
// parameters (script collections, mappings to deploy) are always fine.
func CheckConfigurable(artifactType string, params []models.ConfigurationParameter) error {
	if len(params) > 0 && artifactType != "" && artifactType != "Integration" {
		return output.Usagef("%s artifacts have no configurable parameters; remove 'parameters' (deploy works without them)", artifactType)
	}
	return nil
}

// DiffArtifactConfig compares the given parameters with the artifact's. An
// artifact without parameters is not read from the tenant.
func DiffArtifactConfig(exe *httpclnt.HTTPExecuter, packageID, artifactID, version string, params []models.ConfigurationParameter) ([]ConfigDiffItem, error) {
	if len(params) == 0 {
		return []ConfigDiffItem{}, nil
	}
	current, err := GetConfiguration(exe, artifactID, version)
	if err != nil {
		return nil, err
	}
	tenant := map[string]string{}
	for _, p := range current {
		tenant[p.Key] = p.Value
	}
	items := make([]ConfigDiffItem, 0, len(params))
	for _, p := range params {
		it := ConfigDiffItem{PackageID: packageID, ArtifactID: artifactID, Key: p.Key, Local: p.Value, Change: ConfigUnchanged}
		switch v, ok := tenant[p.Key]; {
		case !ok:
			it.Change = ConfigUnknownKey
		case v != p.Value:
			it.Change, it.Tenant = ConfigUpdate, v
		default:
			it.Tenant = v
		}
		items = append(items, it)
	}
	return items, nil
}
