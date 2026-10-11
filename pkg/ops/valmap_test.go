package ops

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValueMappings(t *testing.T) {
	mock := demoTenant(t)
	exe := mock.Executer()

	vms, err := ListValueMappings(exe, "")
	require.NoError(t, err)
	require.Len(t, vms, 1)
	assert.Equal(t, cpi.ValueMappingInfo{ID: "Billing_CountryCodes", Version: "1.0.0", PackageID: "Billing", Name: "Billing country codes"}, vms[0])
	vms, err = ListValueMappings(exe, "Orders")
	require.NoError(t, err)
	assert.Empty(t, vms)

	vm, err := GetValueMapping(exe, "Billing_CountryCodes", ValMapFilter{})
	require.NoError(t, err)
	require.Len(t, vm.Schemas, 1)
	s := vm.Schemas[0]
	assert.Equal(t, cpi.ValMapSchema{SrcAgency: "ERP", SrcID: "Country", TgtAgency: "Finance", TgtID: "CountryCode", State: "Configured"}, s.ValMapSchema)
	require.Len(t, s.Values, 3)
	assert.Equal(t, cpi.ValMap{ID: "5f0c1a8e2b7d4c3e9a6f1b2c3d4e5f60", SrcValue: "DE", TgtValue: "DEU"}, s.Values[0])
	assert.Empty(t, s.Defaults)
	assert.Contains(t, mock.Requests(), "GET /api/v1/ValueMappingDesigntimeArtifacts(Id='Billing_CountryCodes',Version='active')/ValMapSchema(SrcAgency='ERP',SrcId='Country',TgtAgency='Finance',TgtId='CountryCode')/DefaultValMaps")

	vm, err = GetValueMapping(exe, "Billing_CountryCodes", ValMapFilter{TgtAgency: "Bank"})
	require.NoError(t, err)
	assert.Empty(t, vm.Schemas)

	// a missing value mapping is a usage error (the tenant answers 500)
	_, err = GetValueMapping(exe, "Nope", ValMapFilter{})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
	assert.ErrorContains(t, err, "no value mapping Nope")
}

func TestSetValueMapping(t *testing.T) {
	mock := demoTenant(t)
	exe := mock.Executer()
	req := SetValueMappingRequest{ID: "Billing_CountryCodes", SrcAgency: "ERP", SrcID: "Country", TgtAgency: "Finance", TgtID: "CountryCode"}

	// change an existing source value
	r := req
	r.SrcValue, r.TgtValue = "DE", "GER"
	res, err := SetValueMapping(exe, r)
	require.NoError(t, err)
	assert.Equal(t, "changed", res.Action)
	assert.Equal(t, "DEU", res.Previous)
	assert.Equal(t, "5f0c1a8e2b7d4c3e9a6f1b2c3d4e5f60", res.ValMapID)
	assert.Contains(t, mock.Requests(), "POST /api/v1/UpsertValMaps")

	// add a new one
	r.SrcValue, r.TgtValue = "FR", "FRA"
	res, err = SetValueMapping(exe, r)
	require.NoError(t, err)
	assert.Equal(t, "added", res.Action)
	assert.Empty(t, res.ValMapID)

	vm, err := GetValueMapping(exe, "Billing_CountryCodes", ValMapFilter{})
	require.NoError(t, err)
	values := map[string]string{}
	for _, v := range vm.Schemas[0].Values {
		values[v.SrcValue] = v.TgtValue
	}
	assert.Equal(t, map[string]string{"DE": "GER", "AT": "AUT", "CH": "CHE", "FR": "FRA"}, values)

	// the archive (download) has the change too
	zr, err := zip.NewReader(bytes.NewReader(mock.Artifacts["Billing_CountryCodes"].Zip), int64(len(mock.Artifacts["Billing_CountryCodes"].Zip)))
	require.NoError(t, err)
	f, err := zr.Open("value_mapping.xml")
	require.NoError(t, err)
	data, _ := io.ReadAll(f)
	assert.Contains(t, string(data), "<value>FRA</value>")

	// same value: nothing written
	n := mock.Count("POST /api/v1/UpsertValMaps")
	res, err = SetValueMapping(exe, r)
	require.NoError(t, err)
	assert.Equal(t, "unchanged", res.Action)
	assert.Equal(t, n, mock.Count("POST /api/v1/UpsertValMaps"))

	// unknown schema or value mapping, missing arguments
	r.TgtAgency = "Bank"
	_, err = SetValueMapping(exe, r)
	assert.ErrorContains(t, err, "has no schema ERP:Country -> Bank:CountryCode (it has: ERP:Country -> Finance:CountryCode)")
	r.ID = "Nope"
	_, err = SetValueMapping(exe, r)
	assert.ErrorContains(t, err, "no value mapping Nope")
	_, err = SetValueMapping(exe, SetValueMappingRequest{ID: "Billing_CountryCodes", SrcValue: "x"})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
}

func TestUpsertValMapRequest(t *testing.T) {
	mock := demoTenant(t)
	s := cpi.ValMapSchema{SrcAgency: "ERP", SrcID: "Country", TgtAgency: "Finance", TgtID: "CountryCode"}
	require.NoError(t, cpi.UpsertValMap(mock.Executer(), "Billing_CountryCodes", s, "", "it's", "a b"))
	vals, err := cpi.ValMaps(mock.Executer(), "Billing_CountryCodes", s, false)
	require.NoError(t, err)
	assert.Equal(t, "it's", vals[3].SrcValue)
	assert.Equal(t, "a b", vals[3].TgtValue)
	// unknown pair: the tenant answers 404
	s.TgtID = "Other"
	assert.Error(t, cpi.UpsertValMap(mock.Executer(), "Billing_CountryCodes", s, "", "x", "y"))
}
