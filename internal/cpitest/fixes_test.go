package cpitest_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// get reads an API path of the mock as JSON.
func get(t *testing.T, exe *httpclnt.HTTPExecuter, path string, v any) int {
	t.Helper()
	resp, err := exe.Exec(http.MethodGet, path, nil, map[string]string{"Accept": "application/json"})
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if v != nil && resp.StatusCode == http.StatusOK {
		require.NoError(t, json.Unmarshal(body, v), string(body))
	}
	return resp.StatusCode
}

// The demo seed: no log in the future on any tier, errors name the host the
// flow calls on that tier, failed postings have a trace and the attachment
// the script writes.
func TestDemoSeedFixes(t *testing.T) {
	now := time.Now()
	for _, tier := range cpitest.DemoTiers {
		m := cpitest.NewTenant(t, nil)
		require.NoError(t, cpitest.SeedDemo(m, tier, now))
		var failed *cpitest.MessageLog
		logs := m.MessageLogs()
		for i, l := range logs {
			assert.False(t, l.Start.After(now) || l.End.After(now), "%s: %s %s starts or ends in the future", tier, l.Artifact, l.Start)
			if l.Artifact == "Billing_Post" && l.Status == "FAILED" && failed == nil {
				failed = &logs[i]
			}
		}
		require.NotNil(t, failed, tier)
		host := map[string]string{"dev": "finance-dev", "test": "finance-qa", "prod": "finance-www"}[tier]
		assert.Contains(t, failed.ErrorText, host+".example.com", "the parameter's host on %s", tier)
		last := failed.Steps[len(failed.Steps)-1]
		require.Len(t, last.Traces, 1, tier)

		var atts struct {
			D struct{ Results []struct{ Id, Name string } } `json:"d"`
		}
		exe := m.Executer()
		require.Equal(t, http.StatusOK, get(t, exe, "/api/v1/MessageProcessingLogs('"+failed.Guid+"')/Attachments", &atts))
		require.Len(t, atts.D.Results, 1, tier)
		assert.Equal(t, "Invoice", atts.D.Results[0].Name, "toInvoice.groovy logs the message")
		resp, err := exe.Exec(http.MethodGet, "/api/v1/MessageProcessingLogAttachments('"+atts.D.Results[0].Id+"')/$value", nil, nil)
		require.NoError(t, err)
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Contains(t, string(body), "<OrderNo>")
	}
}

// Data stores and log files are empty lists on a tenant without content, as
// with the demo seed (not 404).
func TestEmptyTenantListsNoDataStores(t *testing.T) {
	m := cpitest.NewTenant(t, nil)
	m.Live = true
	for _, path := range []string{"/api/v1/DataStores", "/api/v1/LogFiles"} {
		var res struct {
			D struct{ Results []any } `json:"d"`
		}
		assert.Equal(t, http.StatusOK, get(t, m.Executer(), path, &res), path)
		assert.NotNil(t, res.D.Results, path)
	}
}

// A message sent to an endpoint runs along ProcessDirect and JMS: one log
// per flow, one correlation ID, predecessor links over ProcessDirect, the
// SAP_ApplicationID of the request as ApplicationMessageId and in the key
// header the scripts write.
func TestLiveSendPropagates(t *testing.T) {
	m := demo(t, "dev")
	req, err := http.NewRequest(http.MethodPost, m.URL()+"/http/orders/in", strings.NewReader("<Order/>"))
	require.NoError(t, err)
	req.SetBasicAuth("user", "secret")
	req.Header.Set("SAP_ApplicationID", "4711")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	corr := resp.Header.Get("SAP_MplCorrelationId")
	require.NotEmpty(t, corr)
	byFlow := map[string]cpitest.MessageLog{}
	for _, l := range m.MessageLogs() {
		if l.CorrelationID == corr {
			byFlow[l.Artifact] = l
		}
	}
	require.Len(t, byFlow, 4, "%v", byFlow)
	in, route, bin, post := byFlow["Orders_In"], byFlow["Orders_Route"], byFlow["Billing_In"], byFlow["Billing_Post"]
	assert.Equal(t, resp.Header.Get("SAP_MessageProcessingLogID"), in.Guid)
	assert.Equal(t, in.Guid, route.Predecessor)
	assert.Equal(t, bin.Guid, post.Predecessor)
	assert.Equal(t, "4711", in.ApplicationID)
	assert.Equal(t, "4711", in.Headers["OrderNo"])
	assert.Equal(t, "4711", bin.Headers["OrderNo"])
}

// Every save records when and by whom, as on a tenant: ModifiedBy is the
// API user (Basic Auth) or OAuth client.
func TestUploadSetsModified(t *testing.T) {
	m := demo(t, "dev")
	m.OAuth = true
	before := m.Artifacts["Orders_In"].ModifiedAt
	require.False(t, before.IsZero())
	zipData := m.Artifacts["Orders_In"].Zip

	upload := func(exe *httpclnt.HTTPExecuter) {
		body, _ := json.Marshal(map[string]string{"Name": "Orders_In", "ArtifactContent": base64.StdEncoding.EncodeToString(zipData)})
		resp, err := exe.Exec(http.MethodPut, "/api/v1/IntegrationDesigntimeArtifacts(Id='Orders_In',Version='active')", bytes.NewReader(body),
			map[string]string{"Content-Type": "application/json"})
		require.NoError(t, err)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}
	upload(m.Executer())
	a := m.Artifacts["Orders_In"]
	assert.Equal(t, "user", a.ModifiedBy, "the Basic Auth user")
	assert.True(t, a.ModifiedAt.After(before))

	host, port := m.HostPort()
	oauth := httpclnt.New(host, "/oauth/token", "sb-ci|it!b1", "secret", "", "", host, "http", port, false)
	upload(oauth)
	assert.Equal(t, "sb-ci|it!b1", m.Artifacts["Orders_In"].ModifiedBy, "the OAuth client")

	var one struct {
		D struct{ ModifiedBy, ModifiedAt string } `json:"d"`
	}
	require.Equal(t, http.StatusOK, get(t, m.Executer(), "/api/v1/IntegrationDesigntimeArtifacts(Id='Orders_In',Version='active')", &one))
	assert.Equal(t, "sb-ci|it!b1", one.D.ModifiedBy)
	assert.NotEmpty(t, one.D.ModifiedAt)
}

// With Auth the mock tells the API key from the runtime key: runtime
// endpoints refuse the API credentials (403) and wrong ones (401), the API
// refuses the runtime key, and the token endpoint issues tokens only to the
// configured clients.
func TestRuntimeCredentials(t *testing.T) {
	m := demo(t, "dev")
	m.OAuth = true
	m.Auth = &cpitest.Auth{APIUser: "api-user", APIPassword: "api-pw", APIClient: cpitest.Client{ID: "api-client", Secret: "api-secret"},
		Runtime: cpitest.Client{ID: "rt-client", Secret: "rt-secret"}}
	host, port := m.HostPort()
	basic := func(user, pw string) *httpclnt.HTTPExecuter {
		return httpclnt.New("", "", "", "", user, pw, host, "http", port, false)
	}
	oauth := func(id, secret string) *httpclnt.HTTPExecuter {
		return httpclnt.New(host, "/oauth/token", id, secret, "", "", host, "http", port, false)
	}
	status := func(exe *httpclnt.HTTPExecuter, method, path string) int {
		t.Helper()
		var body io.Reader
		if method == http.MethodPost {
			body = strings.NewReader("<Order/>")
		}
		resp, err := exe.Exec(method, path, body, nil)
		if err != nil {
			var re *oauth2.RetrieveError // the token endpoint refused the client
			require.ErrorAs(t, err, &re, "%s %s", method, path)
			return re.Response.StatusCode
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	for name, c := range map[string]struct {
		exe          *httpclnt.HTTPExecuter
		api, runtime int
	}{
		"API user":             {basic("api-user", "api-pw"), 200, 403},
		"API client (Basic)":   {basic("api-client", "api-secret"), 200, 403},
		"API client (OAuth)":   {oauth("api-client", "api-secret"), 200, 403},
		"runtime (Basic)":      {basic("rt-client", "rt-secret"), 403, 200},
		"runtime (OAuth)":      {oauth("rt-client", "rt-secret"), 403, 200},
		"wrong password":       {basic("api-user", "wrong"), 401, 401},
		"no such client (UAA)": {oauth("nobody", "x"), 401, 401},
	} {
		assert.Equal(t, c.api, status(c.exe, http.MethodGet, "/api/v1/IntegrationPackages"), name+" on the API")
		assert.Equal(t, c.runtime, status(c.exe.ForEndpoint("/http/orders/in"), http.MethodPost, "/http/orders/in"), name+" on a runtime endpoint")
	}
}
