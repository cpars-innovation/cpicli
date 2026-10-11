package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestDeployConfigDefaults(t *testing.T) {
	var cfg DeployConfig
	require.NoError(t, yaml.Unmarshal([]byte(`deploymentPrefix: DEV
packages:
  - integrationSuiteId: Pkg
    artifacts:
      - {artifactId: A, artifactDir: A, configOverrides: {Host: x}}
      - {artifactId: B, deploy: false}
`), &cfg))
	require.Len(t, cfg.Packages, 1)
	p := cfg.Packages[0]
	assert.True(t, p.Sync && p.Deploy)
	assert.True(t, p.Artifacts[0].Sync && p.Artifacts[0].Deploy)
	assert.Equal(t, map[string]any{"Host": "x"}, p.Artifacts[0].ConfigOverrides)
	assert.False(t, p.Artifacts[1].Deploy)
}

func TestConfigureConfigDefaults(t *testing.T) {
	var cfg ConfigureConfig
	require.NoError(t, yaml.Unmarshal([]byte(`packages:
  - integrationSuiteId: Pkg
    allowDowngrade: true
    artifacts:
      - artifactId: A
        type: Integration
        batch: {}
        parameters: [{key: Host, value: x}]
`), &cfg))
	a := cfg.Packages[0].Artifacts[0]
	assert.Equal(t, "active", a.Version)
	assert.Equal(t, []ConfigurationParameter{{Key: "Host", Value: "x"}}, a.Parameters)
	assert.Equal(t, &BatchSettings{Enabled: true, BatchSize: 90}, a.Batch)
	assert.True(t, EffectiveAllowDowngrade(cfg.Packages[0], a, false))
	no := false
	a.AllowDowngrade = &no
	assert.False(t, EffectiveAllowDowngrade(cfg.Packages[0], a, true), "the artifact wins")
}
