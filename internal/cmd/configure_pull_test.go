package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestPulledConfigureConfig(t *testing.T) {
	artifacts := []*cpi.ArtifactDetails{{Id: "FlowB"}, {Id: "FlowA"}}
	get := func(id, version string) (*cpi.ParametersData, error) {
		data := &cpi.ParametersData{}
		data.Root.Results = []*cpi.ParameterData{
			{ParameterKey: "Z_KEY", ParameterValue: id + "-z"},
			{ParameterKey: "A_KEY", ParameterValue: id + "-a"},
		}
		return data, nil
	}

	cfg, left, err := pulledConfigureConfig("Package", artifacts, get, func(string) map[string]bool { return nil })
	require.NoError(t, err)
	assert.Empty(t, left)
	data, err := yaml.Marshal(cfg)
	require.NoError(t, err)

	assert.Equal(t, `deploymentPrefix: ""
packages:
    - integrationSuiteId: Package
      artifacts:
        - artifactId: FlowA
          type: Integration
          parameters:
            - key: A_KEY
              value: FlowA-a
            - key: Z_KEY
              value: FlowA-z
        - artifactId: FlowB
          type: Integration
          parameters:
            - key: A_KEY
              value: FlowB-a
            - key: Z_KEY
              value: FlowB-z
`, string(data))
}

func TestFilterExistingPackages(t *testing.T) {
	existing, missing := filterExistingPackages(
		[]string{"PackageA", "Missing", "PackageB"},
		[]string{"PackageA", "PackageB"},
	)

	assert.Equal(t, []string{"PackageA", "PackageB"}, existing)
	assert.Equal(t, []string{"Missing"}, missing)
}

// SAP's own parameters are left out of a pull, and with the declared
// parameters known, so are those the flow no longer has (cpicli#87).
func TestPulledConfigureConfigLeavesOut(t *testing.T) {
	artifacts := []*cpi.ArtifactDetails{{Id: "OrderIntake"}, {Id: "Declares"}}
	get := func(id, version string) (*cpi.ParametersData, error) {
		data := &cpi.ParametersData{}
		data.Root.Results = []*cpi.ParameterData{
			{ParameterKey: "PROCESS_DIRECT_SRC_ENDPOINT", ParameterValue: "/orders/in"},
			{ParameterKey: "SAP_ProfileId", ParameterValue: "iflmap"},
			{ParameterKey: "SAP_Custom", ParameterValue: "x"},
			{ParameterKey: "Removed_Param", ParameterValue: "old"},
		}
		return data, nil
	}
	// declared parameters not read: only SAP's own are left out
	cfg, left, err := pulledConfigureConfig("P", artifacts, get, func(string) map[string]bool { return nil })
	require.NoError(t, err)
	data, _ := yaml.Marshal(cfg)
	assert.NotContains(t, string(data), "SAP_ProfileId")
	assert.NotContains(t, string(data), "SAP_Custom")
	assert.Contains(t, string(data), "Removed_Param")
	assert.Equal(t, []PulledLeftOut{
		{Package: "P", Artifact: "Declares", Keys: []string{"SAP_Custom", "SAP_ProfileId"}, Reason: "sap"},
		{Package: "P", Artifact: "OrderIntake", Keys: []string{"SAP_Custom", "SAP_ProfileId"}, Reason: "sap"},
	}, left)

	// --declared-only: what the flow declares stays, also an SAP_* name it declares
	declared := map[string]map[string]bool{
		"OrderIntake": {"PROCESS_DIRECT_SRC_ENDPOINT": true},
		"Declares":    {"PROCESS_DIRECT_SRC_ENDPOINT": true, "SAP_Custom": true},
	}
	cfg, left, err = pulledConfigureConfig("P", artifacts, get, func(id string) map[string]bool { return declared[id] })
	require.NoError(t, err)
	data, _ = yaml.Marshal(cfg)
	assert.NotContains(t, string(data), "Removed_Param")
	assert.NotContains(t, string(data), "SAP_ProfileId")
	assert.Equal(t, 1, strings.Count(string(data), "SAP_Custom"), "declared by Declares only")
	assert.Equal(t, []PulledLeftOut{
		{Package: "P", Artifact: "Declares", Keys: []string{"SAP_ProfileId"}, Reason: "sap"},
		{Package: "P", Artifact: "Declares", Keys: []string{"Removed_Param"}, Reason: "undeclared"},
		{Package: "P", Artifact: "OrderIntake", Keys: []string{"SAP_Custom", "SAP_ProfileId"}, Reason: "sap"},
		{Package: "P", Artifact: "OrderIntake", Keys: []string{"Removed_Param"}, Reason: "undeclared"},
	}, left)
}

// configure pull against the tenant (cpicli#87): SAP_ProfileId never lands in
// the file; --declared-only also leaves out what the flow no longer
// declares, and the result says what was left out.
func TestConfigurePullLeavesOutAgainstTheTenant(t *testing.T) {
	propdef := `<?xml version="1.0" encoding="UTF-8"?><parameters><parameter><name>PROCESS_DIRECT_SRC_ENDPOINT</name><type>xsd:string</type></parameter></parameters>`
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"OrderIntake": {Type: "Integration", DesignVersion: "1.0.0", Package: "P", Name: "OrderIntake",
			Parameters: map[string]string{"PROCESS_DIRECT_SRC_ENDPOINT": "/orders/in", "SAP_ProfileId": "iflmap", "Removed_Param": "old"},
			Zip:        zipOf(t, map[string]string{"src/main/resources/parameters.propdef": propdef})},
	})
	mock.Packages = []cpitest.Package{{ID: "P"}}
	for _, c := range []struct {
		args    []string
		removed bool
	}{{nil, false}, {[]string{"--declared-only"}, true}} {
		dir := t.TempDir()
		r := runMain(t, append(append([]string{"configure", "pull", "--output-dir", dir, "--output", "json"}, c.args...), basicAuth(mock)...)...)
		require.Equal(t, 0, r.code, r.stderr)
		data, err := os.ReadFile(filepath.Join(dir, "P.yml"))
		require.NoError(t, err)
		assert.Contains(t, string(data), "/orders/in")
		assert.NotContains(t, string(data), "SAP_ProfileId")
		assert.Equal(t, !c.removed, strings.Contains(string(data), "Removed_Param"), c.args)
		var doc struct {
			Result ConfigurePullResult `json:"result"`
		}
		require.NoError(t, json.Unmarshal([]byte(r.stdout), &doc))
		want := []PulledLeftOut{{Package: "P", Artifact: "OrderIntake", Keys: []string{"SAP_ProfileId"}, Reason: "sap"}}
		if c.removed {
			want = append(want, PulledLeftOut{Package: "P", Artifact: "OrderIntake", Keys: []string{"Removed_Param"}, Reason: "undeclared"})
		}
		assert.Equal(t, want, doc.Result.LeftOut)
		assert.Equal(t, []string{filepath.Join(dir, "P.yml")}, doc.Result.Files)
	}
}
