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

// shipped is the version of a tool this cpictl ships.
func shipped(id string) string {
	for _, a := range testtools.Artifacts() {
		if a.ID == id {
			return a.Version
		}
	}
	return ""
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
		assert.Equal(t, shipped(a.ID), a.Version)
		assert.Equal(t, testtools.DefaultRole, a.Role, a.ID)
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
		assert.Equal(t, shipped(s.ID), s.RuntimeVersion)
		assert.Equal(t, testtools.DefaultRole, s.Role, s.ID)
	}
	assert.Equal(t, []string{m.URL() + "/http/cpictl/test"}, status[0].Endpoints)
	assert.Equal(t, m.URL()+"/http/cpictl/stub", status[1].StubURL)

	_, err = InstallTestTools(context.Background(), exe, TestToolsOptions{Only: "mapping"})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
}

// A custom role is set on both senders before the deploy, stays across later
// installs without a role, and is reported by the status.
func TestInstallTestToolsRole(t *testing.T) {
	m := liveMock(t)
	exe := m.Executer()

	for _, role := range []string{"ACME Test", "x/y", "a'b"} {
		_, err := InstallTestTools(context.Background(), exe, TestToolsOptions{Role: role})
		assert.Equal(t, exitcode.Usage, output.ExitCode(err), role)
	}
	assert.Empty(t, m.Packages, "an invalid role is refused before anything is written")

	res, err := InstallTestTools(context.Background(), exe, TestToolsOptions{Role: "ACME.Test", Deploy: true, DeployOptions: fastDeploy})
	require.NoError(t, err)
	for _, a := range res.Artifacts {
		assert.Equal(t, "ACME.Test", a.Role, a.ID)
		assert.Equal(t, StatusDeployed, a.Deploy.Status, a.ID)
		v, err := ValidateArtifact(exe, a.ID, "active")
		require.NoError(t, err, a.ID)
		assert.Equal(t, "PASSED", v.Status, v.Details)
	}
	assert.Equal(t, "ACME.Test", m.Artifacts[testtools.HarnessID].Parameters[testtools.HarnessRoleParameter])
	assert.Equal(t, "ACME.Test", m.Artifacts[testtools.StubID].Parameters[testtools.StubRoleParameter])

	res, err = InstallTestTools(context.Background(), exe, TestToolsOptions{Only: testtools.ToolHarness})
	require.NoError(t, err)
	assert.Equal(t, "ACME.Test", res.Artifacts[0].Role, "the configured role stays")

	status, err := TestToolsStatus(context.Background(), exe)
	require.NoError(t, err)
	for _, s := range status {
		assert.Equal(t, "ACME.Test", s.Role, s.ID)
	}
	assert.Equal(t, testtools.HarnessRoleParameter, status[0].RoleParameter)
	assert.Equal(t, testtools.StubRoleParameter, status[1].RoleParameter)

	m.Artifacts[testtools.StubID].ConfigUpdateStatus = http.StatusInternalServerError
	res, err = InstallTestTools(context.Background(), exe, TestToolsOptions{Role: "ACME.Other", Deploy: true, DeployOptions: fastDeploy})
	assert.Equal(t, exitcode.Partial, output.ExitCode(err))
	for _, a := range res.Artifacts {
		if a.ID == testtools.StubID {
			assert.Contains(t, a.Error, "setting "+testtools.StubRoleParameter)
			assert.Nil(t, a.Deploy, "not deployed with the old role")
		} else {
			assert.Equal(t, "ACME.Other", a.Role)
		}
	}
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

// A tool in another package: the error names that package.
func TestInstallTestToolsOtherPackage(t *testing.T) {
	m := liveMock(t)
	exe := m.Executer()
	_, err := InstallTestTools(context.Background(), exe, TestToolsOptions{PackageID: "ZZSandbox", Only: testtools.ToolHarness})
	require.NoError(t, err)

	res, err := InstallTestTools(context.Background(), exe, TestToolsOptions{Only: testtools.ToolHarness})
	require.Error(t, err)
	require.Len(t, res.Artifacts, 1)
	assert.Contains(t, res.Artifacts[0].Error, "CPICTL_Test_Harness already exists in package ZZSandbox, not in CPICTLTesting")

	// installing again where it is: unchanged, no version conflict
	res, err = InstallTestTools(context.Background(), exe, TestToolsOptions{PackageID: "ZZSandbox", Only: testtools.ToolHarness, Deploy: true, DeployOptions: fastDeploy})
	require.NoError(t, err)
	assert.Equal(t, "UNCHANGED", res.Artifacts[0].Upload.Action)
	assert.Equal(t, StatusDeployed, res.Artifacts[0].Deploy.Status)
}

// Conditional answers ("when") and delays: the first matching entry answers,
// the recorded call names it and carries the processing time.
func TestStubWhenAndDelay(t *testing.T) {
	m := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(m, "dev", time.Now()))
	exe := m.Executer()
	_, err := InstallTestTools(context.Background(), exe, TestToolsOptions{Only: testtools.ToolStub, Deploy: true, DeployOptions: fastDeploy})
	require.NoError(t, err)
	since := time.Now().Add(-time.Second)

	rejected := "rejected"
	require.NoError(t, SetStubResponse(exe, "erp", StubResponse{Body: "ok", Headers: map[string]string{"X-A": "1"}, When: []StubWhen{
		{Name: "bad-order", BodyContains: "BAD", Status: 422, Body: &rejected},
		{PathContains: "slow", Header: map[string]string{"x-mode": "test"}, Status: 504, DelayMs: 30000},
	}}))
	url := m.URL() + "/http/cpictl/stub/erp/"
	resp := send(t, url+"orders", "<o>BAD</o>", nil)
	assert.Equal(t, 422, resp.StatusCode)
	resp = send(t, url+"slow", "<o/>", map[string]string{"X-Mode": "test"})
	assert.Equal(t, 504, resp.StatusCode)
	resp = send(t, url+"slow", "<o/>", map[string]string{"X-Mode": "other"})
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "1", resp.Header.Get("X-A"))

	calls, err := StubCalls(context.Background(), exe, since)
	require.NoError(t, err)
	require.Len(t, calls, 3)
	assert.Equal(t, "bad-order", calls[0].Matched)
	assert.Equal(t, "when[1]", calls[1].Matched)
	assert.GreaterOrEqual(t, calls[1].DurationMs, int64(30000), "the delay is processing time")
	assert.Equal(t, "default", calls[2].Matched)
	assert.Less(t, calls[2].DurationMs, int64(30000))

	for _, bad := range []StubResponse{
		{DelayMs: 60001},
		{When: []StubWhen{{Status: 500}}},
		{When: []StubWhen{{PathContains: "x", Status: 700}}},
		{When: []StubWhen{{Name: "a", PathContains: "x"}, {Name: "a", BodyContains: "y"}}},
		{When: []StubWhen{{Header: map[string]string{"bad name": "1"}}}},
	} {
		assert.Equal(t, exitcode.Usage, output.ExitCode(SetStubResponse(exe, "erp", bad)), "%+v", bad)
	}
	got, err := GetStubResponse(exe, "erp")
	require.NoError(t, err)
	require.Len(t, got.When, 2, "round trip")
	assert.Equal(t, 30000, got.When[1].DelayMs)
}
