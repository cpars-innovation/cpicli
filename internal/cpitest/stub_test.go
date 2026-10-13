package cpitest_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/cpars-innovation/cpicli/pkg/testtools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// installTools installs and deploys the test tooling into a live mock.
func installTools(t *testing.T, m *cpitest.Tenant) {
	t.Helper()
	res, err := ops.InstallTestTools(context.Background(), m.Executer(), ops.TestToolsOptions{Deploy: true,
		DeployOptions: ops.Options{Interval: time.Millisecond, MaxChecks: 5}})
	require.NoError(t, err)
	require.Len(t, res.Artifacts, 2)
}

func post(t *testing.T, url, contentType, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", contentType)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// stubLogs are the live logs of the stub, newest first.
func stubLogs(m *cpitest.Tenant) []cpitest.MessageLog {
	var out []cpitest.MessageLog
	for _, l := range m.MessageLogs() {
		if l.Artifact == testtools.StubID {
			out = append(out, l)
		}
	}
	return out
}

// The installed tooling passes the tenant's Check, and the deployed stub
// answers per Partner Directory rule and logs each request as the script
// does: attachment with the recorded request, custom headers rule and path.
func TestStubEmulation(t *testing.T) {
	m := cpitest.NewTenant(t, nil)
	m.Live, m.FilterMessageLogs = true, true
	installTools(t, m)
	for _, id := range []string{testtools.HarnessID, testtools.StubID} {
		v, err := ops.ValidateArtifact(m.Executer(), id, "active")
		require.NoError(t, err, id)
		assert.Equal(t, "PASSED", v.Status, v.Details)
	}
	eps, err := ops.ListServiceEndpoints(m.Executer(), testtools.StubID)
	require.NoError(t, err)
	require.Len(t, eps, 1)
	assert.Equal(t, m.URL()+"/http/cpictl/stub/*", eps[0].EntryPoints[0].URL)

	// no rule: 200 {}
	resp := post(t, m.URL()+"/http/cpictl/stub/orders/42?x=1", "application/xml", "<Order/>",
		map[string]string{"X-Test-Case": "happy", "Authorization": "Basic dXNlcjpzZWNyZXQ="})
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "{}", string(body))
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	assert.NotEmpty(t, resp.Header.Get("SAP_MessageProcessingLogID"))

	logs := stubLogs(m)
	require.Len(t, logs, 1)
	l := logs[0]
	assert.Equal(t, "COMPLETED", l.Status)
	assert.Equal(t, map[string]string{testtools.StubRuleHeader: "orders", testtools.StubPathHeader: "orders/42"}, l.Headers)
	var rec map[string]any
	require.NoError(t, json.Unmarshal([]byte(l.Attachments[testtools.StubAttachment]), &rec))
	assert.Equal(t, "orders", rec["rule"])
	assert.Equal(t, "POST", rec["method"])
	assert.Equal(t, "orders/42", rec["path"])
	assert.Equal(t, "x=1", rec["query"])
	assert.Equal(t, "<Order/>", rec["body"])
	headers := rec["headers"].(map[string]any)
	assert.Equal(t, "happy", headers["X-Test-Case"])
	assert.NotContains(t, headers, "Authorization", "sensitive headers are not recorded")

	// a configured rule
	require.NoError(t, ops.SetStubResponse(m.Executer(), "orders", ops.StubResponse{Status: 503, ContentType: "text/plain", Body: "down",
		Headers: map[string]string{"Retry-After": "5"}}))
	resp = post(t, m.URL()+"/http/cpictl/stub/orders", "text/plain", "x", nil)
	body, _ = io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, "down", string(body))
	assert.Equal(t, "5", resp.Header.Get("Retry-After"))
	assert.Len(t, stubLogs(m), 2, "the stub message completes whatever it answers")
}

// The harness forwards to the ProcessDirect address in CpictlTargetAddress:
// the flow behind it runs with the harness message's correlation ID.
func TestHarnessEmulation(t *testing.T) {
	m := demo(t, "dev")
	installTools(t, m)
	resp := post(t, m.URL()+"/http/cpictl/test", "application/xml", "<Invoice><OrderNo>7</OrderNo></Invoice>",
		map[string]string{testtools.HarnessAddressHeader: "/billing/post"})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	corr := resp.Header.Get("SAP_MplCorrelationId")
	require.NotEmpty(t, corr)
	var artifacts []string
	for _, l := range m.MessageLogs() {
		if l.CorrelationID == corr {
			artifacts = append(artifacts, l.Artifact)
		}
	}
	assert.ElementsMatch(t, []string{testtools.HarnessID, "Billing_Post"}, artifacts)

	resp = post(t, m.URL()+"/http/cpictl/test", "application/xml", "<x/>", map[string]string{testtools.HarnessAddressHeader: "/nothing/here"})
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode, "no consumer for the address")
}

// A receiver of another flow whose address is the mock's own stub URL calls
// the stub: the call is recorded with the flow's correlation ID, and a
// configured error status fails the calling flow.
func TestStubAsReceiver(t *testing.T) {
	m := demo(t, "dev")
	installTools(t, m)
	exe := m.Executer()
	require.NoError(t, cpi.NewConfiguration(exe).Update("Orders_Route", "active", "ERP_URL", m.URL()+"/http/cpictl/stub/erp/orders"))

	resp := post(t, m.URL()+"/http/orders/in", "application/xml", "<Order><OrderNo>4711</OrderNo></Order>", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	corr := resp.Header.Get("SAP_MplCorrelationId")
	logs := stubLogs(m)
	require.Len(t, logs, 1)
	assert.Equal(t, corr, logs[0].CorrelationID)
	assert.Equal(t, "erp", logs[0].Headers[testtools.StubRuleHeader])
	var rec map[string]any
	require.NoError(t, json.Unmarshal([]byte(logs[0].Attachments[testtools.StubAttachment]), &rec))
	assert.Equal(t, "POST", rec["method"])
	assert.Equal(t, "erp/orders", rec["path"])
	assert.Contains(t, rec["body"], "<OrderNo>4711</OrderNo>")

	require.NoError(t, ops.SetStubResponse(exe, "erp", ops.StubResponse{Status: 500, Body: `{"error":"posting period closed"}`}))
	resp = post(t, m.URL()+"/http/orders/in", "application/xml", "<Order><OrderNo>4712</OrderNo></Order>", nil)
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	var failed *cpitest.MessageLog
	for _, l := range m.MessageLogs() {
		if l.Artifact == "Orders_Route" && l.CorrelationID == resp.Header.Get("SAP_MplCorrelationId") {
			failed = &l
		}
	}
	require.NotNil(t, failed)
	assert.Equal(t, "FAILED", failed.Status)
	assert.Contains(t, failed.ErrorText, "HTTP 500")
	assert.Len(t, stubLogs(m), 2)
}
