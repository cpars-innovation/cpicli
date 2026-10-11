// Package models holds the file models of cpictl: deploy configs
// (orchestrator) and configure files.
package models

// ConfigureConfig represents the complete configuration file structure
type ConfigureConfig struct {
	DeploymentPrefix string             `yaml:"deploymentPrefix"`
	Packages         []ConfigurePackage `yaml:"packages"`
}

// ConfigurePackage represents a package containing artifacts to configure
type ConfigurePackage struct {
	ID          string `yaml:"integrationSuiteId"`
	DisplayName string `yaml:"displayName,omitempty"`
	Deploy      bool   `yaml:"deploy,omitempty"` // Deploy all artifacts in package after configuration
	// AllowDowngrade lets the artifacts of this package deploy a designtime
	// version older than the running one (nil: the --allow-downgrade flag).
	AllowDowngrade *bool `yaml:"allowDowngrade,omitempty"`
	// Versioning overrides --versioning for this package (manifest, keep,
	// tenant-bump). Only for exceptions: the mode belongs to the pipeline.
	Versioning string              `yaml:"versioning,omitempty"`
	Artifacts  []ConfigureArtifact `yaml:"artifacts"`
}

func (p *ConfigurePackage) UnmarshalYAML(unmarshal func(any) error) error {
	// Set defaults
	type rawPackage ConfigurePackage
	raw := rawPackage{
		Deploy: false, // By default, don't deploy unless explicitly requested
	}

	if err := unmarshal(&raw); err != nil {
		return err
	}

	*p = ConfigurePackage(raw)
	return nil
}

// ConfigureArtifact represents an artifact with its configuration parameters
type ConfigureArtifact struct {
	ID          string                   `yaml:"artifactId"`
	DisplayName string                   `yaml:"displayName,omitempty"`
	Type        string                   `yaml:"type"`                 // Integration, MessageMapping, ScriptCollection, ValueMapping
	Version     string                   `yaml:"version,omitempty"`    // Artifact version, defaults to "active"
	Deploy      bool                     `yaml:"deploy,omitempty"`     // Deploy this specific artifact after configuration
	Parameters  []ConfigurationParameter `yaml:"parameters,omitempty"` // List of configuration parameters to update
	Batch       *BatchSettings           `yaml:"batch,omitempty"`      // Optional batch processing settings
	// AllowDowngrade lets this artifact deploy a designtime version older
	// than the running one; wins over the package's setting (nil: package,
	// then the --allow-downgrade flag).
	AllowDowngrade *bool `yaml:"allowDowngrade,omitempty"`
	// Versioning overrides the package's and --versioning for this artifact.
	Versioning string `yaml:"versioning,omitempty"`
}

// EffectiveAllowDowngrade resolves allowDowngrade: artifact, then package,
// then the global flag.
func EffectiveAllowDowngrade(pkg ConfigurePackage, a ConfigureArtifact, global bool) bool {
	switch {
	case a.AllowDowngrade != nil:
		return *a.AllowDowngrade
	case pkg.AllowDowngrade != nil:
		return *pkg.AllowDowngrade
	}
	return global
}

func (a *ConfigureArtifact) UnmarshalYAML(unmarshal func(any) error) error {
	// Set defaults
	type rawArtifact ConfigureArtifact
	raw := rawArtifact{
		Version: "active",
		Deploy:  false, // By default, don't deploy unless explicitly requested
	}

	if err := unmarshal(&raw); err != nil {
		return err
	}

	*a = ConfigureArtifact(raw)
	return nil
}

// ConfigurationParameter represents a single configuration parameter to update
type ConfigurationParameter struct {
	Key   string `yaml:"key"`
	Value string `yaml:"value"`
}

// BatchSettings allows per-artifact batch configuration
type BatchSettings struct {
	Enabled   bool `yaml:"enabled"`             // Enable batch processing for this artifact
	BatchSize int  `yaml:"batchSize,omitempty"` // Number of parameters per batch request
}

func (b *BatchSettings) UnmarshalYAML(unmarshal func(any) error) error {
	// Set defaults
	type rawBatch BatchSettings
	raw := rawBatch{
		Enabled:   true,
		BatchSize: 90, // Default batch size from batch.go
	}

	if err := unmarshal(&raw); err != nil {
		return err
	}

	*b = BatchSettings(raw)
	return nil
}
