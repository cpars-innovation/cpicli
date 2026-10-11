package cpi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/go-errors/errors"
)

// ValueMappingInfo is a value mapping designtime artifact.
type ValueMappingInfo struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	PackageID   string `json:"packageId,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// ValMapSchema is one agency/identifier pair of a value mapping (the
// "bidirectional mapping" of the Web UI's value mapping editor).
type ValMapSchema struct {
	SrcAgency string `json:"srcAgency"`
	SrcID     string `json:"srcId"`
	TgtAgency string `json:"tgtAgency"`
	TgtID     string `json:"tgtId"`
	State     string `json:"state,omitempty"`
}

func (s ValMapSchema) String() string {
	return fmt.Sprintf("%s:%s -> %s:%s", s.SrcAgency, s.SrcID, s.TgtAgency, s.TgtID)
}

// ValMap is a value pair of a schema; ID identifies it for updates.
type ValMap struct {
	ID       string `json:"id"`
	SrcValue string `json:"srcValue"`
	TgtValue string `json:"tgtValue"`
}

// ValueMappings lists the value mapping artifacts of the tenant
// (/api/v1/ValueMappingDesigntimeArtifacts).
func ValueMappings(exe *httpclnt.HTTPExecuter) ([]ValueMappingInfo, error) {
	var rows []struct{ Id, Version, PackageId, Name, Description string }
	if err := getAllResults(exe, "/api/v1/ValueMappingDesigntimeArtifacts", "Get value mappings", &rows); err != nil {
		return nil, err
	}
	out := make([]ValueMappingInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, ValueMappingInfo{ID: r.Id, Version: r.Version, PackageID: r.PackageId, Name: r.Name, Description: r.Description})
	}
	return out, nil
}

func valueMappingPath(id string) string {
	return "/api/v1/ValueMappingDesigntimeArtifacts(Id=" + pathKey(id) + ",Version='active')"
}

func (s ValMapSchema) path(id string) string {
	return fmt.Sprintf("%s/ValMapSchema(SrcAgency=%s,SrcId=%s,TgtAgency=%s,TgtId=%s)", valueMappingPath(id),
		pathKey(s.SrcAgency), pathKey(s.SrcID), pathKey(s.TgtAgency), pathKey(s.TgtID))
}

// ValMapSchemas lists the agency/identifier pairs of a value mapping
// (ValueMappingDesigntimeArtifacts(Id,Version)/ValMapSchema).
func ValMapSchemas(exe *httpclnt.HTTPExecuter, id string) ([]ValMapSchema, error) {
	var rows []struct{ SrcAgency, SrcId, TgtAgency, TgtId, State string }
	if err := getAllResults(exe, valueMappingPath(id)+"/ValMapSchema", "Get value mapping schemas of "+id, &rows); err != nil {
		return nil, err
	}
	out := make([]ValMapSchema, 0, len(rows))
	for _, r := range rows {
		out = append(out, ValMapSchema{SrcAgency: r.SrcAgency, SrcID: r.SrcId, TgtAgency: r.TgtAgency, TgtID: r.TgtId, State: r.State})
	}
	return out, nil
}

// ValMaps lists the value pairs of a schema (.../ValMaps), or with defaults
// its default values (.../DefaultValMaps: what a lookup of an unmapped value
// returns).
func ValMaps(exe *httpclnt.HTTPExecuter, id string, s ValMapSchema, defaults bool) ([]ValMap, error) {
	nav := "ValMaps"
	if defaults {
		nav = "DefaultValMaps"
	}
	var rows []struct {
		Id    string
		Value struct{ SrcValue, TgtValue string }
	}
	if err := getAllResults(exe, s.path(id)+"/"+nav, fmt.Sprintf("Get %s of %s (%s)", nav, id, s), &rows); err != nil {
		return nil, err
	}
	out := make([]ValMap, 0, len(rows))
	for _, r := range rows {
		out = append(out, ValMap{ID: r.Id, SrcValue: r.Value.SrcValue, TgtValue: r.Value.TgtValue})
	}
	return out, nil
}

// UpsertValMap adds a value pair to a schema of a value mapping, or changes
// the pair valMapID (function import UpsertValMaps). The schema must exist;
// the change takes effect at runtime after the value mapping is deployed.
func UpsertValMap(exe *httpclnt.HTTPExecuter, id string, s ValMapSchema, valMapID, srcValue, tgtValue string) error {
	q := []string{
		"Id=" + queryEscape(odataString(id)), "Version=" + queryEscape(odataString("active")),
		"SrcAgency=" + queryEscape(odataString(s.SrcAgency)), "SrcId=" + queryEscape(odataString(s.SrcID)),
		"TgtAgency=" + queryEscape(odataString(s.TgtAgency)), "TgtId=" + queryEscape(odataString(s.TgtID)),
	}
	if valMapID != "" {
		q = append(q, "ValMapId="+queryEscape(odataString(valMapID)))
	}
	q = append(q, "SrcValue="+queryEscape(odataString(srcValue)), "TgtValue="+queryEscape(odataString(tgtValue)), "IsConfigured=false")
	_, err := postAction(exe, "/api/v1/UpsertValMaps?"+strings.Join(q, "&"), fmt.Sprintf("Set value mapping %s (%s)", id, s),
		http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNoContent)
	return err
}

// getAllResults reads an OData v2 collection into v (a pointer to a slice),
// following __next links.
func getAllResults(exe *httpclnt.HTTPExecuter, urlPath, callType string, v any) error {
	var all []json.RawMessage
	for page := 0; urlPath != ""; page++ {
		if page >= maxPages {
			return fmt.Errorf("%s: more than %d pages", callType, maxPages)
		}
		resp, err := readOnlyCall(urlPath, callType, exe)
		if err != nil {
			return err
		}
		body, err := exe.ReadRespBody(resp)
		if err != nil {
			return err
		}
		var data struct {
			D struct {
				Results []json.RawMessage `json:"results"`
				Next    string            `json:"__next"`
			} `json:"d"`
		}
		if err := json.Unmarshal(body, &data); err != nil {
			return errors.Wrap(err, 0)
		}
		all = append(all, data.D.Results...)
		urlPath = ""
		if data.D.Next != "" {
			urlPath = relativeNext(data.D.Next)
		}
	}
	if len(all) == 0 {
		return nil
	}
	b, err := json.Marshal(all)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// DeleteDesigntimeArtifact deletes the designtime artifact of the given type
// (Integration, MessageMapping, ScriptCollection, ValueMapping). A deployed
// artifact keeps running until it is undeployed.
func DeleteDesigntimeArtifact(exe *httpclnt.HTTPExecuter, artifactType, id string) error {
	return deleteEntity(exe, fmt.Sprintf("/api/v1/%sDesigntimeArtifacts(Id=%s,Version='active')", artifactType, pathKey(id)),
		fmt.Sprintf("Delete %s designtime artifact %s", artifactType, id))
}

// DeletePackage deletes an integration package.
func DeletePackage(exe *httpclnt.HTTPExecuter, id string) error {
	return deleteEntity(exe, "/api/v1/IntegrationPackages("+pathKey(id)+")", "Delete integration package "+id)
}

// deleteEntity sends DELETE and accepts any 2xx answer (the tenant answers
// 200 or 202 depending on the entity).
func deleteEntity(exe *httpclnt.HTTPExecuter, urlPath, callType string) error {
	resp, err := exe.Exec(http.MethodDelete, urlPath, http.NoBody, map[string]string{"Accept": "application/json"})
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, err = exe.LogError(resp, callType)
		return err
	}
	_, err = exe.ReadRespBody(resp)
	return err
}
