package models

// OrchestratorConfig represents orchestrator-specific settings
type OrchestratorConfig struct {
	PackagesDir      string `yaml:"packagesDir"`
	DeployConfig     string `yaml:"deployConfig"`
	DeploymentPrefix string `yaml:"deploymentPrefix,omitempty"`
	PackageFilter    string `yaml:"packageFilter,omitempty"`
	ArtifactFilter   string `yaml:"artifactFilter,omitempty"`
	ConfigPattern    string `yaml:"configPattern,omitempty"`
	MergeConfigs     bool   `yaml:"mergeConfigs,omitempty"`
	KeepTemp         bool   `yaml:"keepTemp,omitempty"`
	Mode             string `yaml:"mode,omitempty"` // "update-and-deploy", "update-only", "deploy-only"
	// Deployment settings
	DeployRetries       int `yaml:"deployRetries,omitempty"`
	DeployDelaySeconds  int `yaml:"deployDelaySeconds,omitempty"`
	ParallelDeployments int `yaml:"parallelDeployments,omitempty"`
}

// DeployConfig represents the complete deployment configuration
type DeployConfig struct {
	DeploymentPrefix string              `yaml:"deploymentPrefix,omitempty"`
	Packages         []Package           `yaml:"packages"`
	Orchestrator     *OrchestratorConfig `yaml:"orchestrator,omitempty"`
}

// Package represents a SAP CPI package
type Package struct {
	ID          string `yaml:"integrationSuiteId"`
	PackageDir  string `yaml:"packageDir,omitempty"`
	DisplayName string `yaml:"displayName,omitempty"`
	Description string `yaml:"description,omitempty"`
	ShortText   string `yaml:"short_text,omitempty"`
	Sync        bool   `yaml:"sync"`
	Deploy      bool   `yaml:"deploy"`
	// Versioning overrides --versioning for this package (manifest, keep,
	// tenant-bump). Only for exceptions: the mode belongs to the pipeline.
	Versioning string     `yaml:"versioning,omitempty"`
	Artifacts  []Artifact `yaml:"artifacts"`
	// MergedPrefix is the deploymentPrefix of the config file the package
	// came from when several files were merged (its ID and artifact IDs
	// carry the prefix already); not part of the file.
	MergedPrefix string `yaml:"-"`
}

func (p *Package) UnmarshalYAML(unmarshal func(any) error) error {
	// Set defaults
	type rawPackage Package
	raw := rawPackage{
		Sync:   true,
		Deploy: true,
	}

	if err := unmarshal(&raw); err != nil {
		return err
	}

	*p = Package(raw)
	return nil
}

// Artifact represents a SAP CPI artifact (Integration Flow, Script Collection, etc.)
type Artifact struct {
	Id              string         `yaml:"artifactId"`
	ArtifactDir     string         `yaml:"artifactDir"`
	DisplayName     string         `yaml:"displayName,omitempty"`
	Type            string         `yaml:"type"`
	Sync            bool           `yaml:"sync"`
	Deploy          bool           `yaml:"deploy"`
	ConfigOverrides map[string]any `yaml:"configOverrides,omitempty"`
	// Versioning overrides the package's and --versioning for this artifact.
	Versioning string `yaml:"versioning,omitempty"`
}

func (a *Artifact) UnmarshalYAML(unmarshal func(any) error) error {
	// Set defaults
	type rawArtifact Artifact
	raw := rawArtifact{
		Sync:   true,
		Deploy: true,
	}

	if err := unmarshal(&raw); err != nil {
		return err
	}

	*a = Artifact(raw)
	return nil
}

// PackageMetadata represents metadata extracted from {PackageName}.json
type PackageMetadata struct {
	ID          string `json:"Id"`
	Name        string `json:"Name"`
	Description string `json:"Description"`
	ShortText   string `json:"ShortText"`
}

// PackageJSON represents the structure of {PackageName}.json files
type PackageJSON struct {
	D PackageMetadata `json:"d"`
}
