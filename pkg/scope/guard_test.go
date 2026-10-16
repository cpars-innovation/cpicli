package scope

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTenant answers the guard's own lookups: SD_Orders holds SD_A,
// FI_Billing holds FI_B.
func testGuard(t *testing.T, scopeJSON string) *Guard {
	t.Helper()
	sc, err := Parse([]byte(scopeJSON))
	require.NoError(t, err)
	return &Guard{Scope: sc, Fetch: func(_ context.Context, path string) ([]byte, error) {
		list := func(ids ...string) []byte {
			var rows []map[string]string
			for _, id := range ids {
				rows = append(rows, map[string]string{"Id": id})
			}
			b, _ := json.Marshal(map[string]any{"d": map[string]any{"results": rows}})
			return b
		}
		switch {
		case path == "/api/v1/IntegrationPackages":
			return list("SD_Orders", "FI_Billing"), nil
		case strings.HasPrefix(path, "/api/v1/IntegrationPackages('SD_Orders')/IntegrationDesigntimeArtifacts"):
			return list("SD_A"), nil
		case strings.HasPrefix(path, "/api/v1/IntegrationPackages('FI_Billing')/IntegrationDesigntimeArtifacts"):
			return list("FI_B"), nil
		}
		return list(), nil
	}}
}

func req(method, path, body string) *httpclnt.GuardRequest {
	return &httpclnt.GuardRequest{Ctx: context.Background(), Method: method, Base: "https://tmn:443", Path: path, Body: []byte(body)}
}

func refused(t *testing.T, err error) *Refusal {
	t.Helper()
	var r *Refusal
	require.ErrorAs(t, err, &r)
	return r
}

const sdRW = `{"version":1,"packages":["SD_*"],"partners":["SD_*"],"actions":["read","upload","deploy","configure","pd-write"]}`

func TestGuardArtifactsByPackage(t *testing.T) {
	g := testGuard(t, sdRW)
	assert.NoError(t, g.Before(req("GET", "/api/v1/IntegrationDesigntimeArtifacts(Id='SD_A',Version='active')", "")))
	assert.NoError(t, g.Before(req("POST", "/api/v1/DeployIntegrationDesigntimeArtifact?Id='SD_A'&Version='active'", "")))
	r := refused(t, g.Before(req("POST", "/api/v1/DeployIntegrationDesigntimeArtifact?Id='FI_B'&Version='active'", "")))
	assert.Equal(t, "scope: deploy of artifact FI_B is not granted", r.Error(), "no package named for an artifact outside")
	refused(t, g.Before(req("DELETE", "/api/v1/IntegrationDesigntimeArtifacts(Id='SD_A',Version='active')", "")))
	refused(t, g.Before(req("GET", "/api/v1/IntegrationDesigntimeArtifacts(Id='SD_A',Version='active')/$value", "")))
	// a new artifact is decided by its target package
	assert.NoError(t, g.Before(req("POST", "/api/v1/IntegrationDesigntimeArtifacts", `{"Id":"SD_New","PackageId":"SD_Orders"}`)))
	refused(t, g.Before(req("POST", "/api/v1/IntegrationDesigntimeArtifacts", `{"Id":"SD_New","PackageId":"FI_Billing"}`)))
}

func TestGuardRefusesHostileIDsAndUnknownPaths(t *testing.T) {
	g := testGuard(t, sdRW)
	refused(t, g.Before(req("GET", "/api/v1/IntegrationDesigntimeArtifacts(Id='SD_A%27)%2FX',Version='active')", "")))
	r := refused(t, g.Before(req("GET", "/api/v1/SomethingNew", "")))
	assert.Contains(t, r.Error(), "is not available under a scope")
	refused(t, g.Before(req("GET", "/apiportal/api/1.0/Management.svc/APIProxies", "")))
	refused(t, g.Before(req("GET", "/api/v1/IntegrationDesigntimeArtifacts", ""))) // no tenant-wide artifact list
}

func TestGuardChecksEveryBatchOperation(t *testing.T) {
	g := testGuard(t, sdRW)
	batch := func(id string) string {
		return "--b\r\nContent-Type: application/http\r\n\r\nPUT IntegrationDesigntimeArtifacts(Id='" + id + "',Version='active')/$links/Configurations('P') HTTP/1.1\r\n\r\n{}\r\n--b--"
	}
	assert.NoError(t, g.Before(req("POST", "/api/v1/$batch", batch("SD_A"))))
	r := refused(t, g.Before(req("POST", "/api/v1/$batch", batch("SD_A")+"\n"+batch("FI_B"))))
	assert.Contains(t, r.Error(), "FI_B")
}

func TestGuardFiltersLists(t *testing.T) {
	g := testGuard(t, sdRW)
	pkgs := `{"d":{"__count":"2","results":[{"Id":"SD_Orders"},{"Id":"FI_Billing"}]}}`
	r := req("GET", "/api/v1/IntegrationPackages", "")
	require.NoError(t, g.Before(r))
	out, err := g.After(r, 200, nil, []byte(pkgs))
	require.NoError(t, err)
	assert.Contains(t, string(out), "SD_Orders")
	assert.NotContains(t, string(out), "FI_Billing")
	assert.NotContains(t, string(out), "__count", "the total would reveal hidden packages")

	logs := `{"d":{"results":[{"MessageGuid":"G1","IntegrationArtifact":{"Id":"SD_A","PackageId":"SD_Orders"}},
		{"MessageGuid":"G2","IntegrationArtifact":{"Id":"FI_B","PackageId":"FI_Billing"}}]}}`
	out, err = g.After(req("GET", "/api/v1/MessageProcessingLogs?$top=10", ""), 200, nil, []byte(logs))
	require.NoError(t, err)
	assert.Contains(t, string(out), "G1")
	assert.NotContains(t, string(out), "G2")

	pd := `{"d":{"results":[{"Pid":"SD_Cust","Id":"x"},{"Pid":"FI_Bank","Id":"y"},{"Pid":"CPICTL_Test","Id":"stub"}]}}`
	out, err = g.After(req("GET", "/api/v1/StringParameters", ""), 200, nil, []byte(pd))
	require.NoError(t, err)
	assert.Contains(t, string(out), "SD_Cust")
	assert.NotContains(t, string(out), "FI_Bank")
	assert.NotContains(t, string(out), "CPICTL_Test", "tooling partners only with the test action")
}

func TestGuardPartnersAndTenantAreas(t *testing.T) {
	g := testGuard(t, sdRW)
	assert.NoError(t, g.Before(req("PUT", "/api/v1/StringParameters(Pid='SD_Cust',Id='x')", "")))
	refused(t, g.Before(req("PUT", "/api/v1/StringParameters(Pid='FI_Bank',Id='x')", "")))
	refused(t, g.Before(req("POST", "/api/v1/StringParameters", `{"Pid":"FI_Bank","Id":"x"}`)))
	r := refused(t, g.Before(req("GET", "/api/v1/KeystoreEntries", "")))
	assert.Contains(t, r.Error(), "tenant-wide keystore")
	g2 := testGuard(t, `{"version":1,"tenant":["variables:read"],"actions":["read"]}`)
	assert.NoError(t, g2.Before(req("GET", "/api/v1/Variables", "")))
	refused(t, g2.Before(req("DELETE", "/api/v1/Variables(VariableName='x',IntegrationFlow='y')", "")))
}

func TestGuardRuntimeEndpointsNeedASeenEntryPoint(t *testing.T) {
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
	send := &httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: "/http/sd/orders"}
	refused(t, g.Before(send))
	// the tenant lists entry points without the default port the executer writes
	eps := `{"d":{"results":[{"Name":"SD_A","EntryPoints":{"results":[{"Url":"https://rt/http/sd/orders"}]}},
		{"Name":"FI_B","EntryPoints":{"results":[{"Url":"https://rt:443/http/fi/post"}]}}]}}`
	r := req("GET", "/api/v1/ServiceEndpoints?$expand=EntryPoints", "")
	out, err := g.After(r, 200, nil, []byte(eps))
	require.NoError(t, err)
	assert.NotContains(t, string(out), "FI_B")
	assert.NoError(t, g.Before(send))
	refused(t, g.Before(&httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: "/http/fi/post"}))
	// without the test action no message is sent at all
	g3 := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read"]}`)
	refused(t, g3.Before(send))
}

// Child IDs (attachments, runs, traces) are accepted only when they came with
// an allowed answer; content needs "download", except the test tooling's own
// recordings, which come with "test".
func TestGuardMessageChildren(t *testing.T) {
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
	logs := `{"d":{"results":[{"MessageGuid":"G_SD","IntegrationArtifact":{"Id":"SD_A","PackageId":"SD_Orders"}},
		{"MessageGuid":"G_STUB","IntegrationArtifact":{"Id":"CPICTL_Test_Stub","PackageId":"Tools"}}]}}`
	_, err := g.After(req("GET", "/api/v1/MessageProcessingLogs", ""), 200, nil, []byte(logs))
	require.NoError(t, err)
	for guid, att := range map[string]string{"G_SD": "ATT_SD", "G_STUB": "ATT_STUB"} {
		r := req("GET", "/api/v1/MessageProcessingLogs('"+guid+"')/Attachments", "")
		require.NoError(t, g.Before(r))
		_, err = g.After(r, 200, nil, []byte(`{"d":{"results":[{"Id":"`+att+`"}]}}`))
		require.NoError(t, err)
	}
	assert.NoError(t, g.Before(req("GET", "/api/v1/MessageProcessingLogAttachments('ATT_STUB')/$value", "")), "tooling recording with test")
	refused(t, g.Before(req("GET", "/api/v1/MessageProcessingLogAttachments('ATT_SD')/$value", ""))) // content needs download
	refused(t, g.Before(req("GET", "/api/v1/MessageProcessingLogAttachments('NEVER_SEEN')/$value", "")))
}
