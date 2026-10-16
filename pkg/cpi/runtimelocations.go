package cpi

import (
	"encoding/json"
	"encoding/xml"
	"net/url"
	"time"
)

// The runtimes of a tenant (Cloud Integration and integration cells) are not
// in the public /api/v1: a flow bound to an integration cell deploys there,
// but IntegrationRuntimeArtifacts lists only the Cloud Integration runtime.
// These are the Web UI's own calls (Monitor > Manage Integration Content);
// the tenant answers them for an API key too.
const (
	runtimeLocationsPath = "/Operations/com.sap.it.op.srv.web.cf.RuntimeLocationListCommand"
	componentsListPath   = "/Operations/com.sap.it.op.tmn.commands.dashboard.webui.IntegrationComponentsListCommand"
	// CloudIntegrationLocation is the runtime location ID of the Cloud
	// Integration runtime, the one the public API reports on.
	CloudIntegrationLocation = "cloudintegration"
)

// RuntimeLocation is one runtime of the tenant.
type RuntimeLocation struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
	Type        string `json:"type,omitempty"`   // "SAP Cloud Integration", "BTP Integration Cell"
	TypeID      string `json:"typeId,omitempty"` // sapcloudintegration, btpintegrationcell
	State       string `json:"state,omitempty"`  // Active
}

// LocatedArtifact is a deployed artifact on one runtime location.
type LocatedArtifact struct {
	RuntimeArtifact
	// Location is the runtime location ID (cloudintegration, integrationcell).
	Location string
	// DeployState is DEPLOYED or FAILED; Status (STARTED, ERROR, ...) is the
	// semantic state.
	DeployState string
	// WorkspaceProfile is the design-time runtime binding of the flow
	// (iflmap: Cloud Integration; integrationcell).
	WorkspaceProfile string
}

// Locations lists the tenant's runtimes.
func (r *Runtime) Locations() ([]RuntimeLocation, error) {
	resp, err := readOnlyCall(runtimeLocationsPath, "List runtime locations", r.exe)
	if err != nil {
		return nil, err
	}
	body, err := r.exe.ReadRespBody(resp)
	if err != nil {
		return nil, err
	}
	var data struct {
		RuntimeLocations []RuntimeLocation `json:"runtimeLocations"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}
	return data.RuntimeLocations, nil
}

// LocationArtifacts lists the artifacts deployed on a runtime location (the
// command answers XML only).
func (r *Runtime) LocationArtifacts(location string) ([]LocatedArtifact, error) {
	resp, err := readOnlyCallWithBodyAndAcceptType(componentsListPath+"?runtimeLocationId="+url.QueryEscape(location), nil,
		"List artifacts of runtime "+location, "application/xml", r.exe)
	if err != nil {
		return nil, err
	}
	body, err := r.exe.ReadRespBody(resp)
	if err != nil {
		return nil, err
	}
	return parseComponentsList(body, location)
}

func parseComponentsList(body []byte, location string) ([]LocatedArtifact, error) {
	var data struct {
		Artifacts []struct {
			ID           string `xml:"id"`
			SymbolicName string `xml:"symbolicName"`
			Name         string `xml:"name"`
			Version      string `xml:"version"`
			DeployState  string `xml:"deployState"`
			Semantic     string `xml:"semanticState"`
			Location     string `xml:"runtimeLocationId"`
			DeployedBy   string `xml:"deployedBy"`
			DeployedOn   string `xml:"deployedOn"`
			Tags         []struct {
				Name  string `xml:"name,attr"`
				Value string `xml:"value,attr"`
			} `xml:"tags"`
		} `xml:"artifactInformations"`
	}
	if err := xml.Unmarshal(body, &data); err != nil {
		return nil, err
	}
	out := make([]LocatedArtifact, 0, len(data.Artifacts))
	for _, a := range data.Artifacts {
		la := LocatedArtifact{RuntimeArtifact: RuntimeArtifact{Id: a.SymbolicName, Version: a.Version, Name: a.Name,
			Status: a.Semantic, DeployedBy: a.DeployedBy}, Location: a.Location, DeployState: a.DeployState}
		if la.Location == "" {
			la.Location = location
		}
		la.DeployedOn, _ = time.Parse(time.RFC3339Nano, a.DeployedOn)
		for _, t := range a.Tags {
			switch t.Name {
			case "SAP-WorkspaceProfile":
				la.WorkspaceProfile = t.Value
			case "Bundle-Version":
				if la.Version == "" {
					la.Version = t.Value
				}
			case "SAP-BundleType":
				la.Type = t.Value
			}
		}
		if la.Status == "" && la.DeployState == "FAILED" {
			la.Status = "ERROR"
		}
		out = append(out, la)
	}
	return out, nil
}
