package lint

import (
	"testing"

	"github.com/cpars-innovation/cpicli/pkg/models"
	"github.com/stretchr/testify/assert"
)

func TestDeploymentCopiesOf(t *testing.T) {
	cfg := &models.DeployConfig{Packages: []models.Package{
		{ID: "Pkg", Artifacts: []models.Artifact{
			{Id: "Flow", ArtifactDir: "Flow"},
			{Id: "Flow_B", ArtifactDir: "Flow", ConfigOverrides: map[string]any{"Port": 1, "Host": "b"}},
		}},
		{ID: "DEVPkg", MergedPrefix: "DEV", Artifacts: []models.Artifact{{Id: "DEV_Flow", ArtifactDir: "Flow"}}},
	}}
	assert.Equal(t, map[string][]DeploymentCopy{"Pkg/Flow": {{ID: "Flow"}, {ID: "Flow_B", Overrides: []string{"Host", "Port"}}}},
		DeploymentCopiesOf(cfg))
	cfg.DeploymentPrefix = "QA"
	assert.Empty(t, DeploymentCopiesOf(cfg), "environment copies")
	assert.Empty(t, DeploymentCopiesOf(nil))
}
