package ops

import (
	"context"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionMatrix(t *testing.T) {
	git := t.TempDir()
	writeTree(t, git, "P", "Orders", cmpFlowFiles("Orders", "1.0.7", "x", ""))
	writeTree(t, git, "P", "Billing", cmpFlowFiles("Billing", "1.0.2", "x", ""))
	tier := func(arts map[string]*cpitest.Artifact) MatrixTier {
		m := cpitest.NewTenant(t, arts)
		m.Packages = []cpitest.Package{{ID: "P", Version: "1.0.0"}}
		return MatrixTier{Exe: m.Executer()}
	}
	dev := tier(map[string]*cpitest.Artifact{
		"Orders":  {Type: "Integration", DesignVersion: "1.0.7", Package: "P", Name: "Orders", Runtime: &cpitest.Runtime{Version: "1.0.7", Status: "STARTED"}, ModifiedBy: "dev.user"},
		"Billing": {Type: "Integration", DesignVersion: "Active", Package: "P", Name: "Billing"},
		"Only":    {Type: "Integration", DesignVersion: "1.0.0", Package: "P", Name: "Only"},
	})
	dev.Name = "DEV"
	prod := tier(map[string]*cpitest.Artifact{
		"Orders":  {Type: "Integration", DesignVersion: "1.0.5", Package: "P", Name: "Orders", Runtime: &cpitest.Runtime{Version: "1.0.5", Status: "ERROR"}},
		"Billing": {Type: "Integration", DesignVersion: "1.0.2", Package: "P", Name: "Billing", Runtime: &cpitest.Runtime{Version: "1.0.2", Status: "STARTED"}},
	})
	prod.Name = "PROD"

	m, err := BuildVersionMatrix(context.Background(), []MatrixTier{dev, prod}, MatrixOptions{Dir: git})
	require.NoError(t, err)
	assert.Equal(t, []string{"DEV", "PROD"}, m.Tiers)
	rows := map[string]MatrixRow{}
	for _, r := range m.Rows {
		rows[r.Artifact] = r
	}
	o := rows["Orders"]
	assert.Equal(t, "1.0.7", o.Git)
	assert.Equal(t, MatrixCell{Designtime: "1.0.7", Running: "1.0.7", Status: "STARTED", ModifiedBy: "dev.user"}, o.Tiers["DEV"])
	assert.Equal(t, "ERROR", o.Tiers["PROD"].Status)
	assert.True(t, o.Differs)
	assert.Equal(t, []string{"PROD"}, o.Behind, "ready to promote")
	b := rows["Billing"]
	assert.True(t, b.Tiers["DEV"].Draft)
	assert.Empty(t, b.Behind, "a draft is not compared")
	only := rows["Only"]
	assert.Empty(t, only.Git)
	assert.Equal(t, []string{"PROD"}, only.Behind, "not on PROD yet")
	require.Len(t, only.Cells, 2, "every tier, in tier order")
	assert.Equal(t, MatrixTierCell{Tier: "DEV", Present: true, MatrixCell: only.Tiers["DEV"]}, only.Cells[0])
	assert.Equal(t, MatrixTierCell{Tier: "PROD"}, only.Cells[1])

	m, err = BuildVersionMatrix(context.Background(), []MatrixTier{dev, prod}, MatrixOptions{Dir: git, Artifacts: []string{"Billing"}})
	require.NoError(t, err)
	require.Len(t, m.Rows, 1)
}
