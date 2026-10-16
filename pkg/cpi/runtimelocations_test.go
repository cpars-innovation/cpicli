package cpi

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The shape a tenant answers (version only as a tag there).
func TestParseComponentsList(t *testing.T) {
	body := `<?xml version="1.0" ?><com.sap.it.op.tmn.commands.dashboard.webui.IntegrationComponentsListResponse>` +
		`<artifactInformations><deployState>DEPLOYED</deployState><deployedOn>2026-10-16T07:17:42.906Z</deployedOn>` +
		`<id>65398a47</id><name>Verify</name><semanticState>STARTED</semanticState><symbolicName>Studio_Verify_PD</symbolicName>` +
		`<runtimeLocationId>integrationcell</runtimeLocationId><tags name="SAP-BundleType" value="IntegrationFlow"/>` +
		`<tags name="SAP-WorkspaceProfile" value="integrationcell"/><tags name="Bundle-Version" value="1.0.1"/></artifactInformations>` +
		`<artifactInformations><deployState>FAILED</deployState><symbolicName>Broken</symbolicName></artifactInformations>` +
		`</com.sap.it.op.tmn.commands.dashboard.webui.IntegrationComponentsListResponse>`
	arts, err := parseComponentsList([]byte(body), "integrationcell")
	require.NoError(t, err)
	require.Len(t, arts, 2)
	a := arts[0]
	assert.Equal(t, "Studio_Verify_PD", a.Id)
	assert.Equal(t, "1.0.1", a.Version)
	assert.Equal(t, "STARTED", a.Status)
	assert.Equal(t, "integrationcell", a.Location)
	assert.Equal(t, "integrationcell", a.WorkspaceProfile)
	assert.Equal(t, "IntegrationFlow", a.Type)
	assert.Equal(t, time.Date(2026, 10, 16, 7, 17, 42, 906e6, time.UTC), a.DeployedOn)
	assert.Equal(t, "ERROR", arts[1].Status, "FAILED without a semantic state")
	assert.Equal(t, "integrationcell", arts[1].Location)
}
