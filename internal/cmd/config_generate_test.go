package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/deploy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupPackagesDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "packages", "Pkg1")
	writeFile(t, filepath.Join(pkgDir, "Pkg1.json"),
		`{"d":{"Id":"Pkg1","Name":"Package One","Description":"desc","ShortText":"short"}}`)
	writeFile(t, filepath.Join(pkgDir, "Flow1", "META-INF", "MANIFEST.MF"),
		"Manifest-Version: 1.0\nBundle-Name: Flow One\nSAP-BundleType: IntegrationFlow\n")
	return dir
}

// config-generate must read an existing config with the same model (and
// defaults) as the orchestrator, and keep what it does not manage.
func TestConfigGenerate_PreservesOrchestratorSemantics(t *testing.T) {
	dir := setupPackagesDir(t)
	out := filepath.Join(dir, "001-deploy-config.yml")
	// sync/deploy omitted: the orchestrator treats them as true
	writeFile(t, out, `deploymentPrefix: DEV
orchestrator:
  packagesDir: ./packages
  parallelDeployments: 2
packages:
  - integrationSuiteId: Pkg1
    artifacts:
      - artifactId: Flow1
        artifactDir: Flow1
        configOverrides:
          Timeout: 30
`)

	require.NoError(t, NewConfigGenerator(filepath.Join(dir, "packages"), out, nil, nil).Generate())

	loader := deploy.NewConfigLoader()
	require.NoError(t, loader.DetectSource(out))
	files, err := loader.LoadConfigs()
	require.NoError(t, err)
	cfg := files[0].Config

	assert.Equal(t, "DEV", cfg.DeploymentPrefix)
	require.NotNil(t, cfg.Orchestrator, "orchestrator section must be preserved")
	assert.Equal(t, 2, cfg.Orchestrator.ParallelDeployments)

	require.Len(t, cfg.Packages, 1)
	pkg := cfg.Packages[0]
	assert.True(t, pkg.Sync, "omitted sync must stay true")
	assert.True(t, pkg.Deploy, "omitted deploy must stay true")
	require.Len(t, pkg.Artifacts, 1)
	assert.True(t, pkg.Artifacts[0].Sync)
	assert.True(t, pkg.Artifacts[0].Deploy)
	assert.Equal(t, 30, pkg.Artifacts[0].ConfigOverrides["Timeout"])
}

func TestConfigGenerate_ExtractsPackageMetadata(t *testing.T) {
	dir := setupPackagesDir(t)
	out := filepath.Join(dir, "cfg.yml")
	g := NewConfigGenerator(filepath.Join(dir, "packages"), out, nil, nil)
	require.NoError(t, g.Generate())

	loader := deploy.NewConfigLoader()
	require.NoError(t, loader.DetectSource(out))
	files, err := loader.LoadConfigs()
	require.NoError(t, err)
	pkg := files[0].Config.Packages[0]

	assert.Equal(t, "Pkg1", pkg.ID)
	assert.Equal(t, "Pkg1", pkg.PackageDir)
	assert.Equal(t, "Package One", pkg.DisplayName)
	assert.Equal(t, "desc", pkg.Description)
	assert.Equal(t, "short", pkg.ShortText)
	assert.Equal(t, "Flow One", pkg.Artifacts[0].DisplayName)
	assert.Equal(t, "IntegrationFlow", pkg.Artifacts[0].Type)
	assert.Equal(t, 1, g.Stats.PackagePropertiesExtracted)

	data, err := os.ReadFile(out)
	require.NoError(t, err)
	body := string(data)[strings.Index(string(data), "\npackages:"):] // skip the comment header
	assert.NotContains(t, body, "configOverrides", "empty overrides are omitted as before")
	assert.NotContains(t, body, "deploymentPrefix", "empty prefix is omitted as before")
}

func TestConfigGenerate_Filters(t *testing.T) {
	dir := setupPackagesDir(t)
	writeFile(t, filepath.Join(dir, "packages", "Pkg2", "Flow2", "META-INF", "MANIFEST.MF"), "Manifest-Version: 1.0\n")
	out := filepath.Join(dir, "cfg.yml")
	g := NewConfigGenerator(filepath.Join(dir, "packages"), out, []string{"Pkg2"}, nil)
	require.NoError(t, g.Generate())
	assert.Equal(t, 1, g.Stats.PackagesFiltered)
	assert.Equal(t, 1, g.Stats.PackagesAdded)
}

// The output's parent directory is created (it used to fail with exit 4).
func TestConfigGenerate_CreatesOutputDir(t *testing.T) {
	dir := setupPackagesDir(t)
	out := filepath.Join(dir, "configs", "dev", "deploy-config.yml")
	require.NoError(t, NewConfigGenerator(filepath.Join(dir, "packages"), out, nil, nil).Generate())
	data, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Contains(t, string(data), "Pkg1")
}
