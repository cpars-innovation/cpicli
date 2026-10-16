package cpitest

import (
	"encoding/xml"
	"net/http"
	"time"
)

// runtimeLocations are the runtimes besides Cloud Integration that artifacts
// are bound to or run on.
func (m *Tenant) runtimeLocations() []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range sortedKeys(m.Artifacts) {
		a := m.Artifacts[id]
		for _, l := range []string{a.RuntimeLocation, m.runtimeOf(a).location()} {
			if l != "" && l != cloudIntegration && !seen[l] {
				seen[l] = true
				out = append(out, l)
			}
		}
	}
	return out
}

// runtimeOf is what the runtime shows for a, without counting a read.
func (m *Tenant) runtimeOf(a *Artifact) *Runtime {
	if a.triggered && len(a.AfterDeploy) > 0 {
		return pick(a.AfterDeploy, a.runtimeGets)
	}
	return a.Runtime
}

func (rt *Runtime) location() string {
	if rt == nil || rt.Location == "" {
		return cloudIntegration
	}
	return rt.Location
}

// writeComponentsList answers the Web UI's IntegrationComponentsListCommand
// for one runtime location, in the tenant's XML shape.
func (m *Tenant) writeComponentsList(w http.ResponseWriter, location string) {
	type tag struct {
		Name  string `xml:"name,attr"`
		Value string `xml:"value,attr"`
	}
	type info struct {
		DeployState string `xml:"deployState"`
		DeployedOn  string `xml:"deployedOn,omitempty"`
		ID          string `xml:"id"`
		Name        string `xml:"name"`
		Semantic    string `xml:"semanticState"`
		Symbolic    string `xml:"symbolicName"`
		Location    string `xml:"runtimeLocationId"`
		Tags        []tag  `xml:"tags"`
	}
	type response struct {
		XMLName   xml.Name `xml:"com.sap.it.op.tmn.commands.dashboard.webui.IntegrationComponentsListResponse"`
		Artifacts []info   `xml:"artifactInformations"`
	}
	var res response
	for _, id := range sortedKeys(m.Artifacts) {
		a := m.Artifacts[id]
		rt := m.runtimeOf(a)
		if rt == nil || rt.location() != location {
			continue
		}
		state := "DEPLOYED"
		if rt.Status == "ERROR" {
			state = "FAILED"
		}
		profile := "iflmap"
		if location != cloudIntegration {
			profile = location
		}
		in := info{DeployState: state, ID: "guid-" + id, Name: id, Semantic: rt.Status, Symbolic: id, Location: location,
			Tags: []tag{{"SAP-BundleType", "IntegrationFlow"}, {"SAP-WorkspaceProfile", profile}, {"Bundle-Version", rt.Version}}}
		if !rt.DeployedOn.IsZero() {
			in.DeployedOn = rt.DeployedOn.UTC().Format(time.RFC3339Nano)
		}
		res.Artifacts = append(res.Artifacts, in)
	}
	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(res)
}
