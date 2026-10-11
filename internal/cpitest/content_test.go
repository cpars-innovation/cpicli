package cpitest_test

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/file"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/cpars-innovation/cpicli/pkg/versioning"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exportDemo writes a demo artifact's archive into an artifact directory.
func exportDemo(t *testing.T, id string) string {
	t.Helper()
	src := demo(t, "dev")
	dir := t.TempDir()
	zipPath := filepath.Join(dir, id+".zip")
	require.NoError(t, os.WriteFile(zipPath, src.Artifacts[id].Zip, 0o600))
	out := filepath.Join(dir, id)
	require.NoError(t, file.UnzipSource(zipPath, out))
	return out
}

// An empty live tenant filled by uploads behaves like the seeded one: the
// version is set from the manifest (versioning manifest), resources and
// parameters come from the archive, a deploy registers the HTTPS endpoint and
// a message sent to it is logged.
func TestUploadFidelity(t *testing.T) {
	m := cpitest.NewTenant(t, nil)
	m.Live, m.FilterMessageLogs = true, true
	m.Packages = []cpitest.Package{{ID: "CustomerB", Name: "Customer B", Version: "1.0.0"}}
	exe := m.Executer()

	for _, id := range []string{"Orders_In", "Orders_Route", "Billing_Post"} {
		res, err := ops.UploadArtifact(exe, ops.UploadRequest{ID: id, Type: "Integration", PackageID: "CustomerB", Dir: exportDemo(t, id), Versioning: versioning.Manifest})
		require.NoError(t, err, id)
		assert.NotEmpty(t, res.Action, id)
	}
	assert.Equal(t, "1.2.0", m.Artifacts["Orders_In"].DesignVersion, "the manifest version, set after the upload")

	resources, err := ops.ListResources(exe, "Orders_In", "active")
	require.NoError(t, err)
	names := []string{}
	for _, r := range resources {
		names = append(names, r.Name)
	}
	assert.Contains(t, names, "setKeys.groovy")

	cfg := cpi.NewConfiguration(exe)
	params, err := cfg.Get("Billing_Post", "active")
	require.NoError(t, err)
	values := map[string]string{}
	for _, p := range params.Root.Results {
		values[p.ParameterKey] = p.ParameterValue
	}
	assert.Equal(t, "https://finance-dev.example.com/api/invoices", values["FINANCE_URL"], "parameters.prop unescaped")
	assert.Equal(t, "60000", values["Timeout"])
	require.NoError(t, cfg.Update("Billing_Post", "active", "Timeout", "90000"))

	// a new upload keeps the configured value of a key that still exists
	_, err = ops.UploadArtifact(exe, ops.UploadRequest{ID: "Billing_Post", Type: "Integration", PackageID: "CustomerB", Dir: exportDemo(t, "Billing_Post")})
	require.NoError(t, err)
	assert.Equal(t, "90000", m.Artifacts["Billing_Post"].Parameters["Timeout"])

	endpoints, err := ops.ListServiceEndpoints(exe, "Orders_In")
	require.NoError(t, err)
	assert.Empty(t, endpoints, "no endpoint before the deploy")
	require.NoError(t, cpi.NewIntegration(exe).Deploy("Orders_In"))
	endpoints, err = ops.ListServiceEndpoints(exe, "Orders_In")
	require.NoError(t, err)
	require.Len(t, endpoints, 1)

	newExe := func(endpoint string) (*httpclnt.HTTPExecuter, string, error) {
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, "", err
		}
		return m.Executer().ForEndpoint(u.Path), u.Path, nil
	}
	// Orders_In calls Orders_Route over ProcessDirect: not deployed yet, so the message fails as on a tenant
	_, err = ops.SendTestMessage(context.Background(), exe, newExe, ops.TestMessage{ArtifactID: "Orders_In", Body: []byte("<Order/>")})
	require.ErrorContains(t, err, "HTTP 500")
	failed, err := ops.QueryMessageLogs(exe, ops.MessageLogQuery{ArtifactID: "Orders_In", Statuses: []string{"FAILED"}})
	require.NoError(t, err)
	require.Len(t, failed.Logs, 1)

	require.NoError(t, cpi.NewIntegration(exe).Deploy("Orders_Route"))
	sent, err := ops.SendTestMessage(context.Background(), exe, newExe, ops.TestMessage{ArtifactID: "Orders_In", Body: []byte("<Order/>")})
	require.NoError(t, err)
	logs, err := ops.QueryMessageLogs(exe, ops.MessageLogQuery{ArtifactID: "Orders_In", Since: time.Now().Add(-time.Minute)})
	require.NoError(t, err)
	require.NotEmpty(t, logs.Logs)
	assert.Equal(t, sent.MessageGuid, logs.Logs[0].MessageGuid)
	assert.Equal(t, "COMPLETED", logs.Logs[0].Status)
	route, err := ops.QueryMessageLogs(exe, ops.MessageLogQuery{ArtifactID: "Orders_Route"})
	require.NoError(t, err)
	require.Len(t, route.Logs, 1, "Orders_Route ran for the second message")
	assert.Equal(t, sent.MessageGuid, route.Logs[0].PredecessorMessageGuid)

	require.NoError(t, cpi.NewRuntime(exe).UnDeploy("Orders_In"))
	endpoints, err = ops.ListServiceEndpoints(exe, "Orders_In")
	require.NoError(t, err)
	assert.Empty(t, endpoints, "undeploy removes the endpoint")
}

// Two flows listening on the same address: like a real tenant, the second
// deployment ends in ERROR (the usual mistake after copying a flow) and the
// runtime error information says which flow owns the address. Undeploying the
// first flow frees it.
func TestDeploySameAddressTwice(t *testing.T) {
	m := cpitest.NewTenant(t, nil)
	m.Live, m.FilterMessageLogs = true, true
	m.Packages = []cpitest.Package{{ID: "CustomerB", Name: "Customer B", Version: "1.0.0"}}
	exe := m.Executer()
	for _, id := range []string{"Orders_In", "Orders_Copy"} {
		_, err := ops.UploadArtifact(exe, ops.UploadRequest{ID: id, Type: "Integration", PackageID: "CustomerB", Dir: exportDemo(t, "Orders_In")})
		require.NoError(t, err, id)
	}
	opts := ops.Options{Interval: time.Millisecond, MaxChecks: 5}
	deploy := func(id string) ops.Result {
		return ops.Deploy(context.Background(), ops.NewTenant(exe), []ops.Artifact{{ID: id, Type: "Integration"}}, opts)[0]
	}
	undeploy := func(id string) ops.Result {
		return ops.Undeploy(context.Background(), ops.NewTenant(exe), []ops.Artifact{{ID: id, Type: "Integration"}}, opts)[0]
	}

	require.Equal(t, ops.StatusDeployed, deploy("Orders_In").Status)
	res := deploy("Orders_Copy")
	assert.Equal(t, ops.StatusFailed, res.Status)
	assert.Contains(t, res.Error, "ended with status ERROR")
	assert.Contains(t, res.Error, "the address /orders/in is already used by Orders_In")
	info, err := cpi.NewRuntime(exe).GetErrorInfo("Orders_Copy")
	require.NoError(t, err)
	assert.Equal(t, "the address /orders/in is already used by Orders_In", info)
	assert.Equal(t, "Orders_In", m.Inbound["/http/orders/in"].Artifact, "the first flow keeps the address")

	require.Equal(t, ops.StatusUndeployed, undeploy("Orders_In").Status)
	assert.Equal(t, ops.StatusDeployed, deploy("Orders_Copy").Status, "the address is free again")
	assert.Equal(t, "Orders_Copy", m.Inbound["/http/orders/in"].Artifact)
	_, err = cpi.NewRuntime(exe).GetErrorInfo("Orders_Copy")
	assert.Error(t, err, "no error information any more (204)")
}
