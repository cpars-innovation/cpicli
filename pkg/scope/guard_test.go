package scope

import (
	"context"
	"encoding/json"
	"net/url"
	"path"
	"regexp"
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
	refused(t, g.Before(req("GET", "/api/v1/IntegrationDesigntimeArtifacts", "")))                   // no tenant-wide artifact list
	refused(t, g.Before(req("GET", "/api/v1/0DesigntimeArtifacts(Id='SD_A',Version='active')", ""))) // only the known designtime types
	refused(t, g.Before(req("GET", "/api/v1/IntegrationPackages('SD_Orders')/XDesigntimeArtifacts", "")))
	refused(t, g.Before(req("GET", "/api/v1/IntegrationDesigntimeArtifacts(Id='SD_A',Version='a%20b')", "")))
}

// batchReq builds a $batch request as cpictl writes it: one changeset with
// the given request lines.
func batchReq(lines ...string) *httpclnt.GuardRequest {
	var b strings.Builder
	b.WriteString("--batch_1\r\nContent-Type: multipart/mixed; boundary=cs_1\r\n\r\n")
	for _, l := range lines {
		b.WriteString("--cs_1\r\nContent-Type: application/http\r\nContent-Transfer-Encoding: binary\r\n\r\n")
		b.WriteString(l + "\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n{}\r\n")
	}
	b.WriteString("--cs_1--\r\n\r\n--batch_1--\r\n")
	r := req("POST", "/api/v1/$batch", b.String())
	r.Header = map[string]string{"Content-Type": "multipart/mixed; boundary=batch_1"}
	return r
}

func TestGuardChecksEveryBatchOperation(t *testing.T) {
	g := testGuard(t, sdRW)
	op := func(id string) string {
		return "PUT /api/v1/IntegrationDesigntimeArtifacts(Id='" + id + "',Version='active')/$links/Configurations('P') HTTP/1.1"
	}
	assert.NoError(t, g.Before(batchReq(op("SD_A"))))
	r := refused(t, g.Before(batchReq(op("SD_A"), op("FI_B"))))
	assert.Contains(t, r.Error(), "FI_B")
	// anything the guard cannot parse exactly refuses the whole batch (#2)
	for _, line := range []string{
		"DELETE /api/v1/IntegrationPackages('FI_Billing') HTTP/1.0",
		"delete /api/v1/IntegrationPackages('FI_Billing') HTTP/1.1",
		"DELETE  /api/v1/IntegrationPackages('FI_Billing')  HTTP/1.1",
	} {
		refused(t, g.Before(batchReq(op("SD_A"), line)))
	}
	noType := batchReq(op("SD_A"))
	noType.Header = nil
	refused(t, g.Before(noType))
	refused(t, g.Before(batchReq("GET /api/v1/IntegrationPackages('SD_Orders')?$expand=IntegrationDesigntimeArtifacts HTTP/1.1")))
}

// Lists the guard filters are read-only (#3); expansions that could carry
// excluded entities are refused (#6).
func TestGuardListsAreReadOnly(t *testing.T) {
	g := testGuard(t, sdRW)
	for _, r := range []*httpclnt.GuardRequest{
		req("POST", "/api/v1/IntegrationRuntimeArtifacts", "{}"),
		req("DELETE", "/api/v1/MessageProcessingLogs", ""),
		req("POST", "/api/v1/ServiceEndpoints", "{}"),
		req("DELETE", "/api/v1/IntegrationPackages", ""),
		req("PUT", "/api/v1/IntegrationPackages('SD_Orders')/IntegrationDesigntimeArtifacts", "{}"),
	} {
		refused(t, g.Before(r))
	}
	// creating through a package's artifact collection: the package decides
	assert.NoError(t, g.Before(req("POST", "/api/v1/IntegrationPackages('SD_Orders')/IntegrationDesigntimeArtifacts", `{"Id":"SD_New"}`)))
	refused(t, g.Before(req("POST", "/api/v1/IntegrationPackages('SD_Orders')/IntegrationDesigntimeArtifacts", `{"Id":"SD_New","PackageId":"FI_Billing"}`)))
	refused(t, g.Before(req("POST", "/api/v1/IntegrationPackages('FI_Billing')/IntegrationDesigntimeArtifacts", `{"Id":"X"}`)))
	refused(t, g.Before(req("GET", "/api/v1/IntegrationPackages?$expand=IntegrationDesigntimeArtifacts", "")))
	refused(t, g.Before(req("GET", "/api/v1/IntegrationPackages('SD_Orders')?$expand=IntegrationDesigntimeArtifacts", "")))
	assert.NoError(t, g.Before(req("GET", "/api/v1/ServiceEndpoints?$expand=EntryPoints", "")))
}

// Tooling partners: readable and their parameters writable with "test",
// never deleted, and only the real tooling IDs (#5).
func TestGuardToolingPartners(t *testing.T) {
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
	assert.NoError(t, g.Before(req("PUT", "/api/v1/BinaryParameters(Pid='CPICTL_Test',Id='SD.Map')", "")), "stylesheet under test")
	assert.NoError(t, g.Before(req("POST", "/api/v1/StringParameters", `{"Pid":"CPICTL_Stub","Id":"rule"}`)), "stub answer")
	refused(t, g.Before(req("DELETE", "/api/v1/Partners('CPICTL_Test')", "")))
	refused(t, g.Before(req("POST", "/api/v1/StringParameters", `{"Pid":"CPICTL_TestX","Id":"x"}`)))
	refused(t, g.Before(req("PUT", "/api/v1/StringParameters(Pid='CPICTL_Testing_Prod',Id='x')", "")))
	assert.False(t, toolingFlow("CPICTL_TestingProd"), "flows need the CPICTL_Test_ prefix")
	assert.True(t, toolingFlow("CPICTL_Test_Harness"))
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
		{"Name":"FI_B","EntryPoints":{"results":[{"Url":"https://rt/http/fi/post"}]}}]}}`
	r := req("GET", "/api/v1/ServiceEndpoints?$expand=EntryPoints", "")
	out, err := g.After(r, 200, nil, []byte(eps))
	require.NoError(t, err)
	assert.NotContains(t, string(out), "FI_B")
	assert.NoError(t, g.Before(send))
	assert.NoError(t, g.Before(&httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: "/http/sd/orders/sub?x=1"}))
	refused(t, g.Before(&httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: "/http/fi/post"}))
	// a sibling that merely starts with an allowed entry point (#4)
	refused(t, g.Before(&httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: "/http/sd/orders_fi"}))
	// without the test action no message is sent at all
	g3 := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read"]}`)
	refused(t, g3.Before(send))
}

// The test harness reaches any ProcessDirect address: under a scope only
// addresses of flows in the scope (#1).
func TestGuardHarnessTargets(t *testing.T) {
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
	g.ProcessDirectAddresses = func(_ context.Context, id string) ([]string, error) {
		return map[string][]string{"SD_A": {"/sd/in"}}[id], nil
	}
	eps := `{"d":{"results":[{"Name":"CPICTL_Test_Harness","EntryPoints":{"results":[{"Url":"https://rt/http/cpictl/test"}]}}]}}`
	_, err := g.After(req("GET", "/api/v1/ServiceEndpoints?$expand=EntryPoints", ""), 200, nil, []byte(eps))
	require.NoError(t, err)
	call := func(addr string) *httpclnt.GuardRequest {
		return &httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: "/http/cpictl/test",
			Header: map[string]string{"CpictlTargetAddress": addr}}
	}
	assert.NoError(t, g.Before(call("/sd/in")))
	r := refused(t, g.Before(call("/fi/billing/in")))
	assert.Contains(t, r.Error(), "ProcessDirect addresses of flows in the scope")
	refused(t, g.Before(call("")))
	g.ProcessDirectAddresses = nil
	g.pdFlows = nil
	refused(t, g.Before(call("/sd/in")))
}

// With grants, actions differ per package: reading FI's messages and seeing
// its entry points must not allow downloading their payloads or sending to
// them.
const grantsRW = `{"version":1,"grants":[{"packages":["SD_*"],"actions":["read","download","test"]},{"packages":["FI_*"],"actions":["read"]}]}`

func TestGuardGrantsDecideMessageContentPerFlow(t *testing.T) {
	g := testGuard(t, grantsRW)
	logs := `{"d":{"results":[{"MessageGuid":"G1","IntegrationArtifact":{"Id":"SD_A","PackageId":"SD_Orders"}},
		{"MessageGuid":"G2","IntegrationArtifact":{"Id":"FI_B","PackageId":"FI_Billing"}}]}}`
	out, err := g.After(req("GET", "/api/v1/MessageProcessingLogs", ""), 200, nil, []byte(logs))
	require.NoError(t, err)
	assert.Contains(t, string(out), "G2", "FI is readable")
	for _, guid := range []string{"G1", "G2"} {
		r := req("GET", "/api/v1/MessageProcessingLogs('"+guid+"')/Attachments", "")
		require.NoError(t, g.Before(r))
		_, err := g.After(r, 200, nil, []byte(`{"d":{"results":[{"Id":"ATT_`+guid+`"}]}}`))
		require.NoError(t, err)
	}
	assert.NoError(t, g.Before(req("GET", "/api/v1/MessageProcessingLogAttachments('ATT_G1')/$value", "")))
	refused(t, g.Before(req("GET", "/api/v1/MessageProcessingLogAttachments('ATT_G2')/$value", "")))
	assert.NoError(t, g.Before(req("GET", "/api/v1/MessageProcessingLogAttachments('ATT_G2')", "")), "its metadata is read")
}

func TestGuardGrantsDecideEndpointsPerFlow(t *testing.T) {
	g := testGuard(t, grantsRW)
	eps := `{"d":{"results":[{"Name":"SD_A","EntryPoints":{"results":[{"Url":"https://rt/http/sd"}]}},
		{"Name":"FI_B","EntryPoints":{"results":[{"Url":"https://rt/http/fi"}]}}]}}`
	out, err := g.After(req("GET", "/api/v1/ServiceEndpoints?$expand=EntryPoints", ""), 200, nil, []byte(eps))
	require.NoError(t, err)
	assert.Contains(t, string(out), "FI_B", "FI's endpoints are readable")
	send := func(p string) *httpclnt.GuardRequest {
		return &httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: p}
	}
	assert.NoError(t, g.Before(send("/http/sd")))
	r := refused(t, g.Before(send("/http/fi")))
	assert.Equal(t, "scope: test of artifact FI_B is not granted", r.Error())
}

func TestGuardCheckReportsAccesses(t *testing.T) {
	g := testGuard(t, sdRW)
	c, err := g.Check(req("POST", "/api/v1/DeployIntegrationDesigntimeArtifact?Id='SD_A'&Version='active'", ""))
	require.NoError(t, err)
	assert.Equal(t, []Access{{Resource: Resource{Kind: KindArtifact, ID: "SD_A"}, Action: ActionDeploy}}, c.Accesses)
	assert.False(t, c.ReadsAnswer)
	c, err = g.Check(req("GET", "/api/v1/IntegrationPackages", ""))
	require.NoError(t, err)
	assert.Empty(t, c.Accesses)
	assert.True(t, c.ReadsAnswer, "a list is filtered")
	c, err = g.Check(req("GET", "/api/v1/IntegrationDesigntimeArtifacts(Id='SD_A',Version='active')/$value", ""))
	refused(t, err)
	assert.Empty(t, c.Accesses)
}

// Requests a server could read differently from the guard are refused,
// also inside a $batch.
func TestGuardRefusesAmbiguousRequests(t *testing.T) {
	g := testGuard(t, sdRW)
	art := "/api/v1/IntegrationDesigntimeArtifacts(Id='SD_A',Version='active')"
	for _, p := range []string{
		art + "/../../IntegrationPackages('FI_Billing')",
		art + "/%2e%2e/%2E%2E/IntegrationPackages('FI_Billing')",
		art + "/..;/..;/IntegrationPackages('FI_Billing')",
		art + "/.%2e/x",
		art + "/x%2F..%2F..%2FIntegrationPackages('FI_Billing')",
		art + "/x%5C..",
		art + "/x\\..",
		art + "/%252e%252e/x",
		art + "//x",
		art + ";jsessionid=1",
		"/api/v1/IntegrationPackages('SD_Orders');x=1",
		"/api/v1/IntegrationPackages('SD_Orders')/%00",
		"/api/v1/./IntegrationPackages('SD_Orders')",
		"/api/v1/DeployIntegrationDesigntimeArtifact?Id='SD_A'&Id='FI_B'&Version='active'",
		"/api/v1/DeployIntegrationDesigntimeArtifact?Id='SD_A'&ID='FI_B'&Version='active'",
		"/api/v1/DeployIntegrationDesigntimeArtifact?Id='SD_A';Id='FI_B'",
		"api/v1/IntegrationPackages",
	} {
		_, err := g.Check(req("POST", p, ""))
		refused(t, err)
	}
	for _, h := range []map[string]string{{"X-HTTP-Method": "DELETE"}, {"x-http-method-override": "DELETE"}, {"Content-Encoding": "gzip"}} {
		r := req("POST", "/api/v1/DeployIntegrationDesigntimeArtifact?Id='SD_A'&Version='active'", "")
		r.Header = h
		refused(t, g.Before(r))
	}
	ok := req("POST", "/api/v1/DeployIntegrationDesigntimeArtifact?Id='SD_A'&Version='active'", "")
	ok.Header = map[string]string{"Content-Encoding": "identity"}
	assert.NoError(t, g.Before(ok))

	// bodies that repeat a key: the tenant may take the other one
	for _, body := range []string{
		`{"Id":"SD_New","PackageId":"SD_Orders","PackageId":"FI_Billing"}`,
		`{"Id":"SD_New","PackageId":"SD_Orders","packageid":"FI_Billing"}`,
		`{"Id":"FI_X","Id":"SD_New","PackageId":"SD_Orders"}`,
		`{"Id":"SD_New","PackageId":"SD_Orders"}{"PackageId":"FI_Billing"}`,
	} {
		refused(t, g.Before(req("POST", "/api/v1/IntegrationDesigntimeArtifacts", body)))
	}
	assert.NoError(t, g.Before(req("POST", "/api/v1/IntegrationDesigntimeArtifacts", `{"Id":"SD_New","PackageId":"SD_Orders","Nested":{"Id":"x","id":"y"}}`)))

	// inside a $batch: paths and the operations' own headers
	refused(t, g.Before(batchReq("PUT /api/v1/IntegrationDesigntimeArtifacts(Id='SD_A',Version='active')/../../IntegrationPackages('FI_Billing') HTTP/1.1")))
	refused(t, g.Before(batchReq("PUT /api/v1/IntegrationDesigntimeArtifacts(Id='SD_A',Version='active')/$links/Configurations('P') HTTP/1.1\r\nX-HTTP-Method: DELETE")))

	// runtime endpoints may carry ";" (OData services), never dot segments
	eps := `{"d":{"results":[{"Name":"SD_A","EntryPoints":{"results":[{"Url":"https://rt/gw/odata/SD/Orders;v=1"}]}}]}}`
	_, err := g.After(req("GET", "/api/v1/ServiceEndpoints?$expand=EntryPoints", ""), 200, nil, []byte(eps))
	require.NoError(t, err)
	g.Scope.Actions = append(g.Scope.Actions, ActionTest)
	send := func(p string) *httpclnt.GuardRequest {
		return &httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: p}
	}
	assert.NoError(t, g.Before(send("/gw/odata/SD/Orders;v=1/Items")))
	refused(t, g.Before(send("/gw/odata/SD/Orders;v=1/../../../http/fi")))
	refused(t, g.Before(send("/gw/odata/SD/Orders;v=1/..;/x")))
}

// Whatever the guard allows is already a normal path (nothing for a server
// to resolve differently) and never addresses FI_Billing's content.
func FuzzGuardPaths(f *testing.F) {
	for _, s := range []string{
		"IntegrationDesigntimeArtifacts(Id='SD_A',Version='active')/$value",
		"IntegrationDesigntimeArtifacts(Id='SD_A',Version='active')/../../IntegrationPackages('FI_Billing')",
		"IntegrationDesigntimeArtifacts(Id='SD_A%27)%2F..%2F(%27FI_B',Version='active')",
		"IntegrationPackages('SD_Orders')/IntegrationDesigntimeArtifacts",
		"DeployIntegrationDesigntimeArtifact?Id='SD_A'&Version='active'",
		"MessageProcessingLogs('G1')/Attachments",
		"$batch",
	} {
		f.Add("GET", s)
		f.Add("POST", s)
	}
	f.Fuzz(func(t *testing.T, method, rest string) {
		g := testGuard(t, sdRW)
		g.Scope.Actions = append(g.Scope.Actions, ActionDownload, ActionDelete)
		p := "/api/v1/" + rest
		if g.Before(req(method, p, `{"Id":"SD_New","PackageId":"SD_Orders"}`)) != nil {
			return
		}
		raw, _, _ := strings.Cut(p, "?")
		var dec []string
		for _, seg := range strings.Split(raw, "/") {
			d, err := url.PathUnescape(seg)
			if err != nil {
				t.Fatalf("allowed with a bad escape: %q", p)
			}
			dec = append(dec, d)
		}
		decoded := strings.Join(dec, "/")
		if clean := path.Clean(decoded); clean != strings.TrimSuffix(decoded, "/") && clean+"/" != decoded {
			t.Fatalf("allowed a path a server would normalise: %q -> %q", p, clean)
		}
		// below the addressed entity, segments are its navigations: decide
		// on the entity itself
		entity, _, _ := strings.Cut(strings.TrimPrefix(decoded, "/api/v1/"), "/")
		entity = regexp.MustCompile(`,Version='[^']*'`).ReplaceAllString(entity, "")
		if strings.Contains(entity, "FI_") {
			t.Fatalf("allowed a path addressing FI content: %q", p)
		}
	})
}
