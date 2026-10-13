package ops

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/testtools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var fastDeploy = Options{Interval: time.Millisecond, MaxChecks: 5}

func liveMock(t *testing.T) *cpitest.Tenant {
	t.Helper()
	m := cpitest.NewTenant(t, nil)
	m.Live, m.FilterMessageLogs = true, true
	return m
}

func TestInstallTestTools(t *testing.T) {
	m := liveMock(t)
	exe := m.Executer()

	status, err := TestToolsStatus(context.Background(), exe)
	require.NoError(t, err)
	require.Len(t, status, 2)
	for _, s := range status {
		assert.False(t, s.Installed, s.ID)
		assert.False(t, s.Deployed, s.ID)
	}

	res, err := InstallTestTools(context.Background(), exe, TestToolsOptions{Only: testtools.ToolStub})
	require.NoError(t, err)
	assert.Equal(t, "CREATED", res.Package.Action)
	require.Len(t, res.Artifacts, 1)
	assert.Equal(t, testtools.StubID, res.Artifacts[0].ID)
	assert.Equal(t, "CREATED", res.Artifacts[0].Upload.Action)
	assert.Nil(t, res.Artifacts[0].Deploy, "not deployed without Deploy")
	require.Len(t, m.Packages, 1)
	assert.Equal(t, testtools.PackageID, m.Packages[0].ID)
	assert.Equal(t, testtools.PackageName, m.Packages[0].Name)
	assert.NotEmpty(t, m.Packages[0].ShortText)

	res, err = InstallTestTools(context.Background(), exe, TestToolsOptions{Deploy: true, DeployOptions: fastDeploy})
	require.NoError(t, err)
	assert.Equal(t, "EXISTS", res.Package.Action)
	require.Len(t, res.Artifacts, 2)
	byID := map[string]TestToolResult{}
	for _, a := range res.Artifacts {
		byID[a.ID] = a
		require.NotNil(t, a.Deploy, a.ID)
		assert.Equal(t, StatusDeployed, a.Deploy.Status, a.ID)
		assert.Equal(t, "1.0.0", a.Version)
	}
	assert.Equal(t, "CREATED", byID[testtools.HarnessID].Upload.Action)
	assert.Equal(t, "UNCHANGED", byID[testtools.StubID].Upload.Action)
	for _, id := range []string{testtools.HarnessID, testtools.StubID} {
		v, err := ValidateArtifact(exe, id, "active")
		require.NoError(t, err, id)
		assert.Equal(t, "PASSED", v.Status, v.Details)
	}

	status, err = TestToolsStatus(context.Background(), exe)
	require.NoError(t, err)
	for _, s := range status {
		assert.True(t, s.Installed && s.Deployed && s.UpToDate, "%+v", s)
		assert.Equal(t, "1.0.0", s.RuntimeVersion)
	}
	assert.Equal(t, []string{m.URL() + "/http/cpictl/test"}, status[0].Endpoints)
	assert.Equal(t, m.URL()+"/http/cpictl/stub", status[1].StubURL)

	_, err = InstallTestTools(context.Background(), exe, TestToolsOptions{Only: "mapping"})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
}

func TestStubResponseValidation(t *testing.T) {
	m := liveMock(t)
	exe := m.Executer()
	for _, tc := range []struct {
		rule string
		r    StubResponse
	}{
		{"a/b", StubResponse{}},
		{"", StubResponse{}},
		{"ok", StubResponse{Status: 99}},
		{"ok", StubResponse{Status: 600}},
		{"ok", StubResponse{Headers: map[string]string{"Bad Header": "x"}}},
	} {
		err := SetStubResponse(exe, tc.rule, tc.r)
		assert.Equal(t, exitcode.Usage, output.ExitCode(err), "%q %+v", tc.rule, tc.r)
	}
	assert.Equal(t, exitcode.Usage, output.ExitCode(ClearStubResponse(exe, "x y")))
	assert.Empty(t, m.PDStrings)

	require.NoError(t, SetStubResponse(exe, "erp.v1", StubResponse{Body: "[]"}))
	assert.JSONEq(t, `{"status":200,"contentType":"application/json","body":"[]","headers":{}}`, m.PDStrings[testtools.StubPID+"/erp.v1"])
	got, err := GetStubResponse(exe, "erp.v1")
	require.NoError(t, err)
	assert.Equal(t, &StubResponse{Status: 200, ContentType: "application/json", Body: "[]", Headers: map[string]string{}}, got)

	require.NoError(t, SetStubResponse(exe, "erp.v1", StubResponse{Status: 404, ContentType: "text/plain", Body: "no"}))
	assert.Contains(t, m.PDStrings[testtools.StubPID+"/erp.v1"], `"status":404`)

	require.NoError(t, ClearStubResponse(exe, "erp.v1"))
	assert.NotContains(t, m.PDStrings, testtools.StubPID+"/erp.v1")
	require.NoError(t, ClearStubResponse(exe, "erp.v1"), "clearing a rule without an answer is fine")
	got, err = GetStubResponse(exe, "erp.v1")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func send(t *testing.T, url, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	return resp
}

// StubCalls finds the requests to the stub, sent directly and by a receiver
// of a demo flow pointed at the stub, oldest first.
func TestStubCalls(t *testing.T) {
	m := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(m, "dev", time.Now()))
	exe := m.Executer()
	_, err := InstallTestTools(context.Background(), exe, TestToolsOptions{Deploy: true, DeployOptions: fastDeploy})
	require.NoError(t, err)
	since := time.Now().Add(-time.Second)

	calls, err := StubCalls(context.Background(), exe, since)
	require.NoError(t, err)
	assert.Empty(t, calls)

	resp := send(t, m.URL()+"/http/cpictl/stub/partner/acme?mode=test", `{"id":1}`,
		map[string]string{"Content-Type": "application/json", "X-Test-Case": "direct", "Cookie": "session=1"})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	time.Sleep(5 * time.Millisecond)

	require.NoError(t, cpi.NewConfiguration(exe).Update("Orders_Route", "active", "ERP_URL", m.URL()+"/http/cpictl/stub/erp/orders"))
	resp = send(t, m.URL()+"/http/orders/in", "<Order><OrderNo>4711</OrderNo></Order>", map[string]string{"Content-Type": "application/xml"})
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	calls, err = StubCalls(context.Background(), exe, since)
	require.NoError(t, err)
	require.Len(t, calls, 2)
	direct, chained := calls[0], calls[1]
	assert.True(t, direct.Time.Before(chained.Time), "oldest first")

	assert.Equal(t, "partner", direct.Rule)
	assert.Equal(t, "partner/acme", direct.Path)
	assert.Equal(t, "mode=test", direct.Query)
	assert.Equal(t, "POST", direct.Method)
	assert.Equal(t, `{"id":1}`, direct.Body)
	assert.Equal(t, "direct", direct.Headers["X-Test-Case"])
	assert.NotContains(t, direct.Headers, "Cookie")
	assert.NotEmpty(t, direct.MessageGuid)

	assert.Equal(t, "erp", chained.Rule)
	assert.Equal(t, "erp/orders", chained.Path)
	assert.Contains(t, chained.Body, "<OrderNo>4711</OrderNo>")
	assert.NotEqual(t, direct.MessageGuid, chained.MessageGuid)

	later, err := StubCalls(context.Background(), exe, time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.Empty(t, later, "since filters")
}
