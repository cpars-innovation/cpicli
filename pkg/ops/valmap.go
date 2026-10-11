package ops

import (
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// ListValueMappings lists the value mapping artifacts, optionally only those
// of a package.
func ListValueMappings(exe *httpclnt.HTTPExecuter, packageID string) ([]cpi.ValueMappingInfo, error) {
	all, err := cpi.ValueMappings(exe)
	if err != nil {
		return nil, err
	}
	out := []cpi.ValueMappingInfo{}
	for _, v := range all {
		if packageID == "" || v.PackageID == packageID {
			out = append(out, v)
		}
	}
	return out, nil
}

// ValMapSchemaValues is a schema of a value mapping with its value pairs and
// default values.
type ValMapSchemaValues struct {
	cpi.ValMapSchema
	Values   []cpi.ValMap `json:"values"`
	Defaults []cpi.ValMap `json:"defaults"`
}

// ValueMapping is the content of a value mapping artifact.
type ValueMapping struct {
	ID      string               `json:"id"`
	Schemas []ValMapSchemaValues `json:"schemas"`
}

// ValMapFilter limits GetValueMapping to the schemas matching every
// non-empty field.
type ValMapFilter struct {
	SrcAgency, SrcID, TgtAgency, TgtID string
}

func (f ValMapFilter) matches(s cpi.ValMapSchema) bool {
	return (f.SrcAgency == "" || f.SrcAgency == s.SrcAgency) && (f.SrcID == "" || f.SrcID == s.SrcID) &&
		(f.TgtAgency == "" || f.TgtAgency == s.TgtAgency) && (f.TgtID == "" || f.TgtID == s.TgtID)
}

// GetValueMapping reads the schemas of a value mapping (agency/identifier
// pairs) with their value pairs, as the Web UI's value mapping editor shows
// them.
func GetValueMapping(exe *httpclnt.HTTPExecuter, id string, filter ValMapFilter) (*ValueMapping, error) {
	if id == "" {
		return nil, output.Usagef("value mapping ID is required")
	}
	schemas, err := valMapSchemas(exe, id)
	if err != nil {
		return nil, err
	}
	out := &ValueMapping{ID: id, Schemas: []ValMapSchemaValues{}}
	for _, s := range schemas {
		if !filter.matches(s) {
			continue
		}
		values, err := cpi.ValMaps(exe, id, s, false)
		if err != nil {
			return nil, err
		}
		defaults, err := cpi.ValMaps(exe, id, s, true)
		if err != nil {
			return nil, err
		}
		out.Schemas = append(out.Schemas, ValMapSchemaValues{ValMapSchema: s, Values: values, Defaults: defaults})
	}
	return out, nil
}

// SetValueMappingRequest sets the target value of a source value in one
// schema of a value mapping.
type SetValueMappingRequest struct {
	ID                                 string
	SrcAgency, SrcID, TgtAgency, TgtID string
	SrcValue, TgtValue                 string
}

// SetValueMappingResult is the outcome of SetValueMapping.
type SetValueMappingResult struct {
	ID     string           `json:"id"`
	Schema cpi.ValMapSchema `json:"schema"`
	// Action is added, changed or unchanged.
	Action   string `json:"action"`
	ValMapID string `json:"valMapId,omitempty"`
	SrcValue string `json:"srcValue"`
	TgtValue string `json:"tgtValue"`
	Previous string `json:"previous,omitempty"`
	Note     string `json:"note,omitempty"`
}

// SetValueMapping adds a value pair to a schema of a value mapping or
// changes the target value of an existing source value (UpsertValMaps). The
// schema (agency/identifier pair) must exist. The runtime uses the new value
// after the value mapping is deployed.
func SetValueMapping(exe *httpclnt.HTTPExecuter, req SetValueMappingRequest) (*SetValueMappingResult, error) {
	if req.ID == "" {
		return nil, output.Usagef("value mapping ID is required")
	}
	if req.SrcAgency == "" || req.SrcID == "" || req.TgtAgency == "" || req.TgtID == "" {
		return nil, output.Usagef("source agency, source identifier, target agency and target identifier are required")
	}
	if req.SrcValue == "" {
		return nil, output.Usagef("source value is required")
	}
	schemas, err := valMapSchemas(exe, req.ID)
	if err != nil {
		return nil, err
	}
	want := cpi.ValMapSchema{SrcAgency: req.SrcAgency, SrcID: req.SrcID, TgtAgency: req.TgtAgency, TgtID: req.TgtID}
	var schema *cpi.ValMapSchema
	names := make([]string, 0, len(schemas))
	for i, s := range schemas {
		names = append(names, s.String())
		if s.SrcAgency == want.SrcAgency && s.SrcID == want.SrcID && s.TgtAgency == want.TgtAgency && s.TgtID == want.TgtID {
			schema = &schemas[i]
		}
	}
	if schema == nil {
		return nil, output.Usagef("value mapping %s has no schema %s (it has: %s); a new agency/identifier pair is added in the Web UI or by uploading the artifact",
			req.ID, want, strings.Join(names, "; "))
	}
	values, err := cpi.ValMaps(exe, req.ID, *schema, false)
	if err != nil {
		return nil, err
	}
	res := &SetValueMappingResult{ID: req.ID, Schema: *schema, Action: "added", SrcValue: req.SrcValue, TgtValue: req.TgtValue}
	for _, v := range values {
		if v.SrcValue == req.SrcValue {
			res.ValMapID, res.Previous, res.Action = v.ID, v.TgtValue, "changed"
			if v.TgtValue == req.TgtValue {
				res.Action = "unchanged"
				return res, nil
			}
			break
		}
	}
	if err := cpi.UpsertValMap(exe, req.ID, *schema, res.ValMapID, req.SrcValue, req.TgtValue); err != nil {
		return nil, err
	}
	res.Note = "deploy the value mapping (artifact type ValueMapping) for the runtime to use the change"
	return res, nil
}

// valMapSchemas reads the schemas of a value mapping; a missing value
// mapping is a usage error (the tenant answers 500 without a message).
func valMapSchemas(exe *httpclnt.HTTPExecuter, id string) ([]cpi.ValMapSchema, error) {
	if _, exists, err := cpi.GetDesigntimeInfo(exe, "ValueMapping", id, "active"); err != nil {
		return nil, err
	} else if !exists {
		return nil, output.Usagef("no value mapping %s on the tenant (cpictl valmap list shows them)", id)
	}
	return cpi.ValMapSchemas(exe, id)
}
