package ops

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func endpointMock(t *testing.T, in *cpitest.Inbound) (*cpitest.Tenant, string, EndpointExecuterFunc) {
	t.Helper()
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"Orders": {Type: "Integration", DesignVersion: "1"}})
	host, port := mock.HostPort()
	url := fmt.Sprintf("http://%s:%d/http/orders", host, port)
	mock.Artifacts["Orders"].EndpointURL = url
	mock.Inbound = map[string]*cpitest.Inbound{"/http/orders": in}
	newExe := func(u string) (*httpclnt.HTTPExecuter, string, error) {
		return cpi.NewEndpointExecuter(&cpi.ServiceDetails{Userid: "rt-user", Password: "rt-secret"}, u)
	}
	return mock, url, newExe
}

func TestSendTestMessage(t *testing.T) {
	mock, url, newExe := endpointMock(t, &cpitest.Inbound{Response: `{"ok":true}`, ContentType: "application/json", MessageGuid: "G1", RequireCSRF: true})
	mock.MessageLogSteps = [][]cpitest.MessageLog{
		{{Guid: "G1", Artifact: "Orders", Status: "PROCESSING"}},
		{{Guid: "G1", Artifact: "Orders", Status: "COMPLETED"}},
	}
	sent, err := SendTestMessage(context.Background(), mock.Executer(), newExe, TestMessage{
		ArtifactID: "Orders", Body: []byte(`<order/>`), ContentType: "application/xml",
		Headers: map[string]string{"X-Test": "1"}, Wait: time.Second, PollInterval: time.Millisecond,
	})
	require.NoError(t, err)
	assert.Equal(t, url, sent.URL)
	assert.Equal(t, 200, sent.HTTPStatus)
	assert.Equal(t, "G1", sent.MessageGuid)
	assert.Equal(t, `{"ok":true}`, sent.Response.Text)
	require.NotNil(t, sent.Log)
	assert.Equal(t, "COMPLETED", sent.Log.Status)

	require.Len(t, mock.Received, 1)
	got := mock.Received[0]
	assert.Equal(t, "POST", got.Method)
	assert.Equal(t, "<order/>", got.Body)
	assert.Equal(t, "application/xml", got.Header.Get("Content-Type"))
	assert.Equal(t, "1", got.Header.Get("X-Test"))
	// CSRF fetched from the endpoint only after it asked for a token
	assert.Equal(t, []string{"POST /http/orders", "GET /http/orders", "POST /http/orders"}, filter(mock.Requests(), "/http/"))
}

func filter(reqs []string, substr string) []string {
	var out []string
	for _, r := range reqs {
		if strings.Contains(r, substr) {
			out = append(out, r)
		}
	}
	return out
}

func TestSendTestMessageFailures(t *testing.T) {
	t.Run("iflow error is failed", func(t *testing.T) {
		mock, _, newExe := endpointMock(t, &cpitest.Inbound{Status: 500, Response: "mapping failed", MessageGuid: "G2"})
		sent, err := SendTestMessage(context.Background(), mock.Executer(), newExe, TestMessage{ArtifactID: "Orders"})
		require.Error(t, err)
		assert.Equal(t, exitcode.DeployFailed, output.ExitCode(err))
		assert.Contains(t, err.Error(), "G2")
		assert.Equal(t, "mapping failed", sent.Response.Text)
	})
	t.Run("missing role is auth", func(t *testing.T) {
		mock, _, newExe := endpointMock(t, &cpitest.Inbound{Status: 403})
		_, err := SendTestMessage(context.Background(), mock.Executer(), newExe, TestMessage{ArtifactID: "Orders"})
		require.Error(t, err)
		assert.Equal(t, exitcode.Auth, output.ExitCode(err))
	})
	t.Run("message failed", func(t *testing.T) {
		mock, _, newExe := endpointMock(t, &cpitest.Inbound{MessageGuid: "G3"})
		mock.MessageLogSteps = [][]cpitest.MessageLog{{{Guid: "G3", Artifact: "Orders", Status: "FAILED", ErrorText: "boom"}}}
		sent, err := SendTestMessage(context.Background(), mock.Executer(), newExe, TestMessage{ArtifactID: "Orders", Wait: time.Second, PollInterval: time.Millisecond})
		require.Error(t, err)
		assert.Equal(t, exitcode.DeployFailed, output.ExitCode(err))
		assert.Equal(t, "boom", sent.Log.ErrorText)
	})
	t.Run("only listed endpoints", func(t *testing.T) {
		mock, _, newExe := endpointMock(t, &cpitest.Inbound{})
		_, err := SendTestMessage(context.Background(), mock.Executer(), newExe, TestMessage{ArtifactID: "Orders", URL: "https://evil.example.com/http/orders"})
		require.Error(t, err)
		assert.Equal(t, exitcode.Usage, output.ExitCode(err))
		assert.Empty(t, mock.Received)
	})
	t.Run("below a listed endpoint", func(t *testing.T) {
		mock, _, newExe := endpointMock(t, &cpitest.Inbound{})
		host, port := mock.HostPort()
		url := fmt.Sprintf("http://%s:%d/cxf/out", host, port)
		mock.Artifacts["Orders"].EndpointURL = url
		mock.Inbound = map[string]*cpitest.Inbound{"/cxf/out": {}}
		sent, err := SendTestMessage(context.Background(), mock.Executer(), newExe, TestMessage{ArtifactID: "Orders", URL: url + "/test?x=1"})
		require.NoError(t, err)
		assert.Equal(t, url+"/test?x=1", sent.URL)
		sent, err = SendTestMessage(context.Background(), mock.Executer(), newExe, TestMessage{ArtifactID: "Orders", Path: "test"})
		require.NoError(t, err)
		assert.Equal(t, url+"/test", sent.URL)
		assert.Contains(t, filter(mock.Requests(), "/cxf/"), "POST /cxf/out/test")
		for _, bad := range []string{url + "x", url + "/../admin", strings.Replace(url, "/cxf/out", "/cxf/other", 1)} {
			_, err = SendTestMessage(context.Background(), mock.Executer(), newExe, TestMessage{ArtifactID: "Orders", URL: bad})
			assert.Equal(t, exitcode.Usage, output.ExitCode(err), bad)
		}
		for _, bad := range []string{"https://evil.example.com/x", "../admin", "//evil"} {
			_, err = SendTestMessage(context.Background(), mock.Executer(), newExe, TestMessage{ArtifactID: "Orders", Path: bad})
			assert.Equal(t, exitcode.Usage, output.ExitCode(err), bad)
		}
	})
	t.Run("not deployed", func(t *testing.T) {
		mock, _, newExe := endpointMock(t, &cpitest.Inbound{})
		_, err := SendTestMessage(context.Background(), mock.Executer(), newExe, TestMessage{ArtifactID: "Unknown"})
		assert.Equal(t, exitcode.Usage, output.ExitCode(err))
	})
	t.Run("reserved header", func(t *testing.T) {
		mock, _, newExe := endpointMock(t, &cpitest.Inbound{})
		_, err := SendTestMessage(context.Background(), mock.Executer(), newExe, TestMessage{ArtifactID: "Orders", Headers: map[string]string{"Authorization": "x"}})
		assert.Equal(t, exitcode.Usage, output.ExitCode(err))
		assert.Empty(t, mock.Received)
	})
}

func TestEndpointExecuterRejectsPlainHTTP(t *testing.T) {
	_, _, err := cpi.NewEndpointExecuter(&cpi.ServiceDetails{}, "http://tenant.example.com/http/x")
	require.Error(t, err)
	_, path, err := cpi.NewEndpointExecuter(&cpi.ServiceDetails{}, "https://tenant.example.com/http/x?a=1")
	require.NoError(t, err)
	assert.Equal(t, "/http/x?a=1", path)
}

func TestCreatePackage(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.Packages = []cpitest.Package{{ID: "Existing", Name: "Existing one"}}

	res, err := CreatePackage(mock.Executer(), PackageRequest{ID: "Orders", Name: "Orders", Description: "d"})
	require.NoError(t, err)
	assert.Equal(t, "CREATED", res.Action)
	assert.Equal(t, 1, mock.Count("POST /api/v1/IntegrationPackages"))

	res, err = CreatePackage(mock.Executer(), PackageRequest{ID: "Existing"})
	require.NoError(t, err)
	assert.Equal(t, PackageResult{ID: "Existing", Name: "Existing one", Action: "EXISTS"}, *res)
	assert.Equal(t, 1, mock.Count("POST /api/v1/IntegrationPackages"))

	// the tenant accepts letters and digits only
	for _, id := range []string{"bad id'", "CPICTL_Test_Tools", "SD.Orders", "SD-Orders", ""} {
		_, err = CreatePackage(mock.Executer(), PackageRequest{ID: id})
		assert.Equal(t, exitcode.Usage, output.ExitCode(err), id)
	}
	assert.Equal(t, 1, mock.Count("POST /api/v1/IntegrationPackages"), "nothing invalid reaches the tenant")
}

// A flow just deployed is STARTED before the tenant lists its endpoint: the
// send waits for it; a flow that is not deployed fails at once.
func TestSendWaitsForTheEndpointOfAStartedFlow(t *testing.T) {
	defer func(w, p time.Duration) { endpointWait, endpointPoll = w, p }(endpointWait, endpointPoll)
	endpointWait, endpointPoll = time.Second, time.Millisecond
	mock, url, newExe := endpointMock(t, &cpitest.Inbound{Response: "ok", MessageGuid: "G1"})
	mock.Artifacts["Orders"].Runtime = &cpitest.Runtime{Version: "1", Status: "STARTED"}
	mock.Artifacts["Orders"].EndpointHidden = 2
	var steps []string
	sent, err := SendTestMessage(context.Background(), mock.Executer(), newExe, TestMessage{ArtifactID: "Orders", Body: []byte("x"),
		Progress: func(p Progress) { steps = append(steps, p.Message) }})
	require.NoError(t, err)
	assert.Equal(t, url, sent.URL)
	assert.Equal(t, []string{"Orders is started, waiting for the tenant to list its endpoint", "Orders is started, waiting for the tenant to list its endpoint"}, steps)

	mock.Artifacts["Orders"].Runtime = nil
	mock.Artifacts["Orders"].EndpointHidden = 1
	steps = nil
	_, err = SendTestMessage(context.Background(), mock.Executer(), newExe, TestMessage{ArtifactID: "Orders", Body: []byte("x"),
		Progress: func(p Progress) { steps = append(steps, p.Message) }})
	assert.ErrorContains(t, err, "has no endpoint")
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
	assert.Empty(t, steps, "not deployed: no wait")
}
