package ops

import (
	"context"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func demoTenant(t *testing.T) *cpitest.Tenant {
	t.Helper()
	mock := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(mock, "dev", time.Now()))
	mock.Live = true
	return mock
}

func TestDeleteArtifact(t *testing.T) {
	mock := demoTenant(t)
	exe := mock.Executer()
	ctx := context.Background()
	opts := Options{Interval: time.Millisecond, MaxChecks: 5}

	// deployed: refused unless undeploy first, nothing deleted
	_, err := DeleteArtifact(ctx, exe, DeleteRequest{ArtifactID: "Returns_In", Options: opts})
	require.Error(t, err)
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
	assert.ErrorContains(t, err, "Returns_In is deployed")
	assert.NotContains(t, mock.Requests(), "DELETE /api/v1/IntegrationDesigntimeArtifacts(Id='Returns_In',Version='active')")

	// the type is found on the tenant; undeploy first, then delete
	res, err := DeleteArtifact(ctx, exe, DeleteRequest{ArtifactID: "Returns_In", UndeployFirst: true, Options: opts})
	require.NoError(t, err)
	assert.Equal(t, &DeleteResult{ArtifactID: "Returns_In", Type: "Integration", Undeployed: "0.1.0", Deleted: true}, res)
	assert.Contains(t, mock.Requests(), "DELETE /api/v1/IntegrationRuntimeArtifacts('Returns_In')")
	assert.Contains(t, mock.Requests(), "DELETE /api/v1/IntegrationDesigntimeArtifacts(Id='Returns_In',Version='active')")
	assert.Nil(t, mock.Artifacts["Returns_In"])

	// other types; a wrong type or a missing artifact is a usage error
	_, err = DeleteArtifact(ctx, exe, DeleteRequest{ArtifactID: "Billing_CountryCodes", Type: "ScriptCollection"})
	assert.ErrorContains(t, err, "no ScriptCollection artifact Billing_CountryCodes")
	res, err = DeleteArtifact(ctx, exe, DeleteRequest{ArtifactID: "Billing_CountryCodes", Type: "ValueMapping", UndeployFirst: true, Options: opts})
	require.NoError(t, err)
	assert.Equal(t, "ValueMapping", res.Type)
	assert.Contains(t, mock.Requests(), "DELETE /api/v1/ValueMappingDesigntimeArtifacts(Id='Billing_CountryCodes',Version='active')")

	_, err = DeleteArtifact(ctx, exe, DeleteRequest{ArtifactID: "Nope"})
	assert.ErrorContains(t, err, "no designtime artifact Nope")
	_, err = DeleteArtifact(ctx, exe, DeleteRequest{ArtifactID: "Orders_In", Type: "Flow"})
	assert.ErrorContains(t, err, "invalid artifact type")
	_, err = DeleteArtifact(ctx, exe, DeleteRequest{})
	assert.Error(t, err)
}

// A deployed artifact keeps running when only its designtime artifact goes
// (the mock behaves like that); the guard prevents it unless asked.
func TestDeleteNotDeployedArtifact(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"S": {Type: "ScriptCollection", DesignVersion: "1.0.0", Package: "P"}})
	res, err := DeleteArtifact(context.Background(), mock.Executer(), DeleteRequest{ArtifactID: "S"})
	require.NoError(t, err)
	assert.Equal(t, &DeleteResult{ArtifactID: "S", Type: "ScriptCollection", Deleted: true}, res)
	assert.NotContains(t, mock.Requests(), "DELETE /api/v1/IntegrationRuntimeArtifacts('S')")
}

func TestDeletePackage(t *testing.T) {
	mock := demoTenant(t)
	exe := mock.Executer()
	ctx := context.Background()
	opts := Options{Interval: time.Millisecond, MaxChecks: 5}

	_, err := DeletePackage(ctx, exe, DeletePackageRequest{PackageID: "Partners"})
	assert.ErrorContains(t, err, "package Partners still has 1 artifact(s) (Partner_Notify)")
	_, err = DeletePackage(ctx, exe, DeletePackageRequest{PackageID: "Partners", WithArtifacts: true})
	assert.ErrorContains(t, err, "deployed artifacts (Partner_Notify)")
	assert.NotNil(t, mock.Artifacts["Partner_Notify"], "nothing deleted before the checks passed")

	res, err := DeletePackage(ctx, exe, DeletePackageRequest{PackageID: "Partners", WithArtifacts: true, UndeployFirst: true, Options: opts})
	require.NoError(t, err)
	assert.True(t, res.Deleted)
	require.Len(t, res.Artifacts, 1)
	assert.Equal(t, DeleteResult{ArtifactID: "Partner_Notify", Type: "Integration", Undeployed: "1.0.0", Deleted: true}, res.Artifacts[0])
	assert.Contains(t, mock.Requests(), "DELETE /api/v1/IntegrationPackages('Partners')")

	pkgs, err := cpi.NewIntegrationPackage(exe).GetPackagesList()
	require.NoError(t, err)
	assert.NotContains(t, pkgs, "Partners")

	// an empty package needs no flags
	require.NoError(t, cpi.NewIntegrationPackage(exe).Create(&cpi.PackageSingleData{Root: cpi.PackageFields{Id: "Empty", Name: "Empty", ShortText: "x"}}))
	res, err = DeletePackage(ctx, exe, DeletePackageRequest{PackageID: "Empty"})
	require.NoError(t, err)
	assert.Equal(t, &DeletePackageResult{PackageID: "Empty", Artifacts: []DeleteResult{}, Deleted: true}, res)

	_, err = DeletePackage(ctx, exe, DeletePackageRequest{PackageID: "Empty"})
	assert.ErrorContains(t, err, "no package Empty")
}

// deploy works for every designtime type (Deploy<Type>DesigntimeArtifact).
func TestDeployAllTypes(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"I": {Type: "Integration", DesignVersion: "1.0.0"},
		"M": {Type: "MessageMapping", DesignVersion: "1.0.0"},
		"S": {Type: "ScriptCollection", DesignVersion: "1.0.0"},
		"V": {Type: "ValueMapping", DesignVersion: "1.0.0"},
	})
	mock.Live = true
	var list []Artifact
	for id, typ := range map[string]string{"I": "Integration", "M": "MessageMapping", "S": "ScriptCollection", "V": "ValueMapping"} {
		list = append(list, Artifact{ID: id, Type: typ})
	}
	results := Deploy(context.Background(), NewTenant(mock.Executer()), list, fastOpts())
	require.NoError(t, Err(results))
	for _, typ := range cpi.ArtifactTypes {
		assert.Contains(t, mock.Requests(), "POST /api/v1/Deploy"+typ+"DesigntimeArtifact")
	}
	// a type the artifact does not have is refused like on the tenant
	results = Deploy(context.Background(), NewTenant(mock.Executer()), []Artifact{{ID: "V", Type: "Integration"}}, fastOpts())
	assert.Error(t, Err(results))
}

// A tenant's MessageMappingDesigntimeArtifacts(Id=...) answers for any
// artifact: the type of a value mapping must not be guessed as message
// mapping, and a flow asked for as a message mapping is refused.
func TestDeleteResolvesTypeLikeATenant(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"VM1":  {Type: "ValueMapping", DesignVersion: "1.0.0", Package: "P"},
		"Flow": {Type: "Integration", DesignVersion: "1.0.0", Package: "P"},
	})
	res, err := DeleteArtifact(context.Background(), mock.Executer(), DeleteRequest{ArtifactID: "VM1"})
	require.NoError(t, err)
	assert.Equal(t, "ValueMapping", res.Type)
	assert.True(t, res.Deleted)

	_, err = DeleteArtifact(context.Background(), mock.Executer(), DeleteRequest{ArtifactID: "Flow", Type: "MessageMapping"})
	assert.ErrorContains(t, err, "Flow is of type Integration, not MessageMapping")
	assert.NotNil(t, mock.Artifacts["Flow"], "not deleted")
}
