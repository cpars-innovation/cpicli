package scope

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"partners":["SD_*"],"actions":["read","test"]}`)
	assert.NoError(t, g.Before(req("PUT", "/api/v1/BinaryParameters(Pid='CPICTL_Test',Id='SD_Cust.Map')", "")), "stylesheet under test of an SD partner")
	assert.NoError(t, g.Before(req("POST", "/api/v1/StringParameters", `{"Pid":"CPICTL_Stub","Id":"SD_A.billing"}`)), "stub answer of SD_A")
	assert.NoError(t, g.Before(req("GET", "/api/v1/Partners('CPICTL_Stub')", "")), "the partner itself")
	refused(t, g.Before(req("DELETE", "/api/v1/Partners('CPICTL_Test')", "")))
	refused(t, g.Before(req("POST", "/api/v1/StringParameters", `{"Pid":"CPICTL_TestX","Id":"x"}`)))
	refused(t, g.Before(req("PUT", "/api/v1/StringParameters(Pid='CPICTL_Testing_Prod',Id='x')", "")))
	assert.False(t, toolingFlow("CPICTL_TestingProd"), "flows need the CPICTL_Test_ prefix")
	assert.True(t, toolingFlow("CPICTL_Test_Harness"))
	assert.False(t, toolingFlow("CPICTL_Test_Harness_v2"), "only the exact tooling IDs (#65)")
}

// Only the tooling's own flows are tooling (#65): a copy of the harness under
// another ID would reach any ProcessDirect address without the harness
// check, and any other flow named CPICTL_Test_* is decided by its package.
func TestGuardToolingCopies(t *testing.T) {
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
	eps := `{"d":{"results":[
		{"Name":"CPICTL_Test_Harness_v2","EntryPoints":{"results":[{"Url":"https://rt/http/cpictl/test2"}]}},
		{"Name":"CPICTL_Test_Script_0011223344","EntryPoints":{"results":[{"Url":"https://rt/http/cpictl/script/0011223344"}]}},
		{"Name":"CPICTL_Test_Stub","EntryPoints":{"results":[{"Url":"https://rt/http/cpictl/stub/*"}]}}]}}`
	endpoints(g, eps)
	send := func(path string) *httpclnt.GuardRequest {
		return &httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt", Path: path,
			Header: map[string]string{"CpictlTargetAddress": "/fi/in"}}
	}
	r := refused(t, g.Before(send("/http/cpictl/test2")))
	assert.Contains(t, r.Error(), notAnEntryPoint)
	assert.NotContains(t, r.Error(), "Harness_v2", "the refusal names no flow")
	assert.Contains(t, r.Audit, "CPICTL_Test_Harness_v2")
	refused(t, g.Before(send("/http/cpictl/script/0011223344")))
	assert.NoError(t, g.Before(send("/http/cpictl/stub/SD_A.billing")), "the stub")
	for _, id := range []string{"CPICTL_Test_Harness_v2", "CPICTL_Test_Script_0011223344", "CPICTL_Test_X"} {
		refused(t, g.Before(req("GET", "/api/v1/IntegrationDesigntimeArtifacts(Id='"+id+"',Version='active')", "")))
	}
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
	endpoints(g, eps)
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
	runs(g, map[string]string{"SD_A": `{"d":{"Id":"SD_A","Status":"STARTED","Version":"1.0.1","DeployedOn":"/Date(1700000100000)/"}}`}, nil)
	eps := `{"d":{"results":[{"Name":"CPICTL_Test_Harness","EntryPoints":{"results":[{"Url":"https://rt/http/cpictl/test"}]}}]}}`
	endpoints(g, eps)
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

// endpoints makes g's Fetch answer the tenant-wide endpoint list with eps,
// read at the next decision.
func endpoints(g *Guard, eps string) {
	g.endpointCache().Invalidate()
	fetch := g.Fetch
	g.Fetch = func(ctx context.Context, path string) ([]byte, error) {
		if strings.HasPrefix(path, "/api/v1/ServiceEndpoints") {
			return []byte(eps), nil
		}
		return fetch(ctx, path)
	}
}

// runs makes g's Fetch answer the runtime artifacts in rt (Id -> entity) and
// the designtime artifacts in dt; a flow missing from dt is SD_A's
// designtime version 1.0.1, last changed before the deployment of rt.
func runs(g *Guard, rt, dt map[string]string) {
	fetch := g.Fetch
	g.Fetch = func(ctx context.Context, path string) ([]byte, error) {
		for id, e := range rt {
			if path == "/api/v1/IntegrationRuntimeArtifacts('"+id+"')" {
				return []byte(e), nil
			}
		}
		for id := range rt {
			if path == "/api/v1/IntegrationDesigntimeArtifacts(Id='"+id+"',Version='active')" {
				if e, ok := dt[id]; ok {
					return []byte(e), nil
				}
				return []byte(`{"d":{"Id":"` + id + `","Version":"1.0.1","ModifiedAt":"1700000000000"}}`), nil
			}
		}
		if strings.HasPrefix(path, "/api/v1/IntegrationRuntimeArtifacts(") {
			return nil, fmt.Errorf("404 not found")
		}
		return fetch(ctx, path)
	}
}

// The harness reaches a ProcessDirect address only while the allowed flow
// runs the content the address was read from (security review of 0.35.1,
// second report, finding 1): otherwise the address may be another team's
// running flow.
func TestGuardHarnessNeedsTheRunningFlow(t *testing.T) {
	eps := `{"d":{"results":[{"Name":"CPICTL_Test_Harness","EntryPoints":{"results":[{"Url":"https://rt/http/cpictl/test"}]}}]}}`
	call := &httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: "/http/cpictl/test",
		Header: map[string]string{"CpictlTargetAddress": "/sd/in"}}
	started := `{"d":{"Id":"SD_A","Status":"STARTED","Version":"1.0.1","DeployedOn":"/Date(1700000100000)/"}}`
	for _, c := range []struct {
		name   string
		rt, dt map[string]string
		why    string
	}{
		{"not deployed", nil, nil, "not deployed"},
		{"stopped", map[string]string{"SD_A": strings.Replace(started, "STARTED", "ERROR", 1)}, nil, "not running (status ERROR)"},
		{"older version runs", map[string]string{"SD_A": started},
			map[string]string{"SD_A": `{"d":{"Version":"1.0.2","ModifiedAt":"1700000000000"}}`}, "deploy it first"},
		{"changed after deployment", map[string]string{"SD_A": started},
			map[string]string{"SD_A": `{"d":{"Version":"1.0.1","ModifiedAt":"1700000200000"}}`}, "changed after its deployment"},
		{"no change time", map[string]string{"SD_A": started},
			map[string]string{"SD_A": `{"d":{"Version":"1.0.1"}}`}, "does not report"},
		{"another flow's answer", map[string]string{"SD_A": strings.Replace(started, `"Id":"SD_A"`, `"Id":"FI_B"`, 1)}, nil, "not deployed"},
		{"running as read", map[string]string{"SD_A": started}, nil, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
			g.ProcessDirectAddresses = func(_ context.Context, id string) ([]string, error) {
				return map[string][]string{"SD_A": {"/sd/in"}}[id], nil
			}
			runs(g, c.rt, c.dt)
			endpoints(g, eps)
			_, err := g.After(req("GET", "/api/v1/ServiceEndpoints?$expand=EntryPoints", ""), 200, nil, []byte(eps))
			require.NoError(t, err)
			if c.why == "" {
				assert.NoError(t, g.Before(call))
				return
			}
			assert.Contains(t, refused(t, g.Before(call)).Error(), c.why)
		})
	}
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
	endpoints(g, eps)
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
	endpoints(g, eps)
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

// The tenant navigates from a message or a runtime artifact into its
// children: content must not be reachable through a path checked as read
// (review of #38: verified on a tenant for Attachments('…')/$value).
func TestGuardMessageSubPathsAreAllowlisted(t *testing.T) {
	g := testGuard(t, grantsRW)
	logs := `{"d":{"results":[{"MessageGuid":"G2","IntegrationArtifact":{"Id":"FI_B","PackageId":"FI_Billing"}}]}}`
	_, err := g.After(req("GET", "/api/v1/MessageProcessingLogs", ""), 200, nil, []byte(logs))
	require.NoError(t, err)
	for _, p := range []string{
		"/api/v1/MessageProcessingLogs('G2')/Attachments('ATT_G2')/$value",
		"/api/v1/MessageProcessingLogs('G2')/MessageStoreEntries('E1')/$value",
		"/api/v1/MessageProcessingLogs('G2')/Runs('R1')/RunSteps(RunId='R1',ChildCount=1)/TraceMessages(1)/$value",
		"/api/v1/MessageProcessingLogs('G2')/Attachments('ATT_G2')",
		"/api/v1/MessageProcessingLogs('G2')/Runs('R1')",
		"/api/v1/MessageProcessingLogs('G2')/Unknown",
		"/api/v1/IntegrationRuntimeArtifacts('FI_B')/Something",
		"/api/v1/IntegrationRuntimeArtifacts('FI_B')/ErrorInformation/x",
	} {
		refused(t, g.Before(req("GET", p, "")))
	}
	refused(t, g.Before(req("DELETE", "/api/v1/MessageProcessingLogs('G2')/Attachments", "")))
	for _, p := range []string{"", "/ErrorInformation", "/ErrorInformation/$value", "/Attachments", "/MessageStoreEntries",
		"/Runs", "/CustomHeaderProperties", "/AdapterAttributes"} {
		assert.NoError(t, g.Before(req("GET", "/api/v1/MessageProcessingLogs('G2')"+p+"?$format=json", "")), p)
	}
	for _, p := range []string{"", "/ErrorInformation", "/ErrorInformation/$value"} {
		assert.NoError(t, g.Before(req("GET", "/api/v1/IntegrationRuntimeArtifacts('FI_B')"+p, "")), p)
	}
	// content below children and artifacts is a download wherever $value sits
	g.children = map[string]string{"R1": "FI_B", "E1": "FI_B"}
	refused(t, g.Before(req("GET", "/api/v1/MessageProcessingLogRuns('R1')/RunSteps(RunId='R1',ChildCount=1)/TraceMessages", "")))
	refused(t, g.Before(req("GET", "/api/v1/MessageStoreEntries('E1')/Attachments('x')/$value", "")))
	assert.NoError(t, g.Before(req("GET", "/api/v1/MessageStoreEntries('E1')", "")), "metadata is a read")
}

// Independent of the guard's own classification: any allowed request that
// names content ($value, TraceMessages) must have needed download.
func FuzzGuardContentNeedsDownload(f *testing.F) {
	for _, s := range []string{
		"MessageProcessingLogs('G1')/Attachments('A1')/$value",
		"MessageProcessingLogs('G1')/Runs('R1')/RunSteps(RunId='R1',ChildCount=1)/TraceMessages(1)/$value",
		"MessageProcessingLogs('G1')/ErrorInformation/$value",
		"MessageProcessingLogRuns('R1')/RunSteps(RunId='R1',ChildCount=1)/TraceMessages",
		"MessageStoreEntries('E1')/$value",
		"IntegrationDesigntimeArtifacts(Id='SD_A',Version='active')/Resources('x')/$value",
		"IntegrationDesigntimeArtifacts(Id='SD_A',Version='active')/$value/x",
		"IntegrationRuntimeArtifacts('SD_A')/ErrorInformation/$value",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, rest string) {
		// read everywhere, download nowhere: content must always be refused
		g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read"]}`)
		g.messages = map[string]string{"G1": "SD_A"}
		g.children = map[string]string{"R1": "SD_A", "E1": "SD_A", "A1": "SD_A"}
		p := "/api/v1/" + rest
		c, err := g.Check(req("GET", p, ""))
		if err != nil {
			return
		}
		path, _, _ := strings.Cut(p, "?")
		dec, _ := url.PathUnescape(path)
		if !strings.Contains(dec, "$value") && !strings.Contains(dec, "TraceMessages") {
			return
		}
		if regexp.MustCompile(`^/api/v1/(MessageProcessingLogs|IntegrationRuntimeArtifacts)\('[^/]*'\)/ErrorInformation/\$value$`).MatchString(dec) {
			return // the error text of a message or runtime artifact
		}
		for _, a := range c.Accesses {
			if a.Action == ActionDownload {
				return
			}
		}
		t.Fatalf("content allowed without download: %q (%+v)", p, c.Accesses)
	})
}

// Messages of the shared test tooling are decided by the flow they tested,
// found through the correlation ID they share with it (#48): with "test" on
// SD, another team's harness runs, stub recordings and XSLT runs stay hidden.
func TestGuardToolingMessagesByTargetFlow(t *testing.T) {
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
	base := g.Fetch
	groups := map[string]string{
		"C1": `[{"MessageGuid":"H1","IntegrationArtifact":{"Id":"CPICTL_Test_Harness"}},{"MessageGuid":"T1","IntegrationArtifact":{"Id":"SD_A"}},{"MessageGuid":"S1","IntegrationArtifact":{"Id":"CPICTL_Test_Stub"}}]`,
		"C2": `[{"MessageGuid":"H2","IntegrationArtifact":{"Id":"CPICTL_Test_Harness"}},{"MessageGuid":"T2","IntegrationArtifact":{"Id":"FI_B"}},{"MessageGuid":"S2","IntegrationArtifact":{"Id":"CPICTL_Test_Stub"}}]`,
		"C3": `[{"MessageGuid":"X1","IntegrationArtifact":{"Id":"CPICTL_Test_XSLT"}}]`,
	}
	correlationOf := map[string]string{"H1": "C1", "S1": "C1", "H2": "C2", "S2": "C2", "X1": "C3"}
	var fetched []string
	var fmu sync.Mutex
	g.Fetch = func(ctx context.Context, path string) ([]byte, error) {
		fmu.Lock()
		fetched = append(fetched, path)
		fmu.Unlock()
		for c, rows := range groups {
			if strings.Contains(path, "%27"+c+"%27") {
				return []byte(`{"d":{"results":` + rows + `}}`), nil
			}
		}
		for guid, c := range correlationOf {
			if strings.HasPrefix(path, "/api/v1/MessageProcessingLogs('"+guid+"')") {
				flow := map[byte]string{'H': "CPICTL_Test_Harness", 'S': "CPICTL_Test_Stub", 'X': "CPICTL_Test_XSLT"}[guid[0]]
				return []byte(`{"d":{"MessageGuid":"` + guid + `","CorrelationId":"` + c + `","IntegrationArtifact":{"Id":"` + flow + `"}}}`), nil
			}
		}
		return base(ctx, path)
	}

	logs := `{"d":{"results":[
		{"MessageGuid":"H1","CorrelationId":"C1","IntegrationArtifact":{"Id":"CPICTL_Test_Harness"}},
		{"MessageGuid":"S1","IntegrationArtifact":{"Id":"CPICTL_Test_Stub"}},
		{"MessageGuid":"H2","CorrelationId":"C2","IntegrationArtifact":{"Id":"CPICTL_Test_Harness"}},
		{"MessageGuid":"X1","CorrelationId":"C3","IntegrationArtifact":{"Id":"CPICTL_Test_XSLT"}}]}}`
	r := req("GET", "/api/v1/MessageProcessingLogs?$filter=IntegrationArtifact/Id%20eq%20'CPICTL_Test_Stub'", "")
	require.NoError(t, g.Before(r))
	out, err := g.After(r, 200, nil, []byte(logs))
	require.NoError(t, err)
	assert.Contains(t, string(out), `"H1"`, "a harness run of SD_A")
	assert.Contains(t, string(out), `"S1"`, "the stub call of that run (correlation ID looked up: not in the list)")
	assert.NotContains(t, string(out), `"H2"`, "another team's harness run")
	assert.NotContains(t, string(out), `"X1"`, "a run that tested no flow: nothing says whose it was")

	// the stub's recording of SD's run: with "test" on SD_A, no download needed
	r = req("GET", "/api/v1/MessageProcessingLogs('S1')/Attachments", "")
	require.NoError(t, g.Before(r))
	_, err = g.After(r, 200, nil, []byte(`{"d":{"results":[{"Id":"ATT_S1"}]}}`))
	require.NoError(t, err)
	assert.NoError(t, g.Before(req("GET", "/api/v1/MessageProcessingLogAttachments('ATT_S1')/$value", "")))

	// asked for directly, by GUID: decided the same way
	for _, guid := range []string{"S2", "H2", "X1"} {
		refused(t, g.Before(req("GET", "/api/v1/MessageProcessingLogs('"+guid+"')/Attachments", "")))
	}
	assert.NoError(t, g.Before(req("GET", "/api/v1/MessageProcessingLogs('H1')/Attachments", "")))

	// without "test", tooling messages are hidden even for the flow's package
	ro := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read"]}`)
	ro.Fetch = g.Fetch
	out, err = ro.After(req("GET", "/api/v1/MessageProcessingLogs", ""), 200, nil, []byte(logs))
	require.NoError(t, err)
	assert.NotContains(t, string(out), `"H1"`)

	for _, p := range fetched {
		assert.NotContains(t, p, "'C1' or", "the correlation ID is quoted, never spliced: %s", p)
	}
}

// The tooling's partners are shared by teams: under a scope, a stub rule or a
// stylesheet under test belongs to the flow or partner its ID starts with
// (#48 follow-up).
func TestGuardToolingParametersByOwner(t *testing.T) {
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"partners":["SD_*"],"actions":["read","test"]}`)
	for _, r := range []*httpclnt.GuardRequest{
		req("POST", "/api/v1/StringParameters", `{"Pid":"CPICTL_Stub","Id":"FI_B.billing"}`), // another team's flow
		req("POST", "/api/v1/StringParameters", `{"Pid":"CPICTL_Stub","Id":"billing"}`),      // no owner
		req("PUT", "/api/v1/StringParameters(Pid='CPICTL_Stub',Id='FI_B.billing')", ""),
		req("GET", "/api/v1/StringParameters(Pid='CPICTL_Stub',Id='FI_B.billing')", ""),
		req("DELETE", "/api/v1/StringParameters(Pid='CPICTL_Stub',Id='FI_B.billing')", ""),
		req("GET", "/api/v1/BinaryParameters(Pid='CPICTL_Test',Id='FI_Bank.Map')", ""),
		req("GET", "/api/v1/Partners('CPICTL_Stub')/StringParameters", ""),                            // all rules at once
		req("POST", "/api/v1/StringParameters", `{"Pid":"CPICTL_Stub","Id":"CPICTL_Test_Harness.x"}`), // the tooling owns nothing
	} {
		refused(t, g.Before(r))
	}
	assert.Contains(t, refused(t, g.Before(req("PUT", "/api/v1/StringParameters(Pid='CPICTL_Stub',Id='billing')", ""))).Error(),
		"<flowId>.billing", "says how to name it")
	assert.NoError(t, g.Before(req("GET", "/api/v1/StringParameters(Pid='CPICTL_Stub',Id='SD_A.billing')", "")))
	assert.NoError(t, g.Before(req("DELETE", "/api/v1/StringParameters(Pid='CPICTL_Stub',Id='SD_A.billing')", "")))

	pd := `{"d":{"results":[{"Pid":"CPICTL_Stub","Id":"SD_A.billing"},{"Pid":"CPICTL_Stub","Id":"FI_B.billing"},
		{"Pid":"CPICTL_Stub","Id":"billing"},{"Pid":"CPICTL_Test","Id":"SD_Cust.Map"},{"Pid":"CPICTL_Test","Id":"FI_Bank.Map"}]}}`
	for _, list := range []string{"StringParameters", "BinaryParameters"} {
		out, err := g.After(req("GET", "/api/v1/"+list, ""), 200, nil, []byte(pd))
		require.NoError(t, err)
		assert.Contains(t, string(out), "SD_A.billing")
		assert.Contains(t, string(out), "SD_Cust.Map")
		assert.NotContains(t, string(out), "FI_B.billing")
		assert.NotContains(t, string(out), "FI_Bank.Map")
		assert.NotContains(t, string(out), `"billing"`)
	}
}

// A list of many other teams' tooling runs costs one lookup per message and
// per correlation group, not two per row on every request (#51).
func TestGuardToolingLookupsAreBounded(t *testing.T) {
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
	base := g.Fetch
	var calls atomic.Int64
	g.Fetch = func(ctx context.Context, path string) ([]byte, error) {
		if strings.HasPrefix(path, "/api/v1/MessageProcessingLogs") {
			calls.Add(1)
			if strings.Contains(path, "CorrelationId+eq") {
				// another team's flow in every group
				return []byte(`{"d":{"results":[{"MessageGuid":"X","IntegrationArtifact":{"Id":"FI_B"}}]}}`), nil
			}
			guid := path[len("/api/v1/MessageProcessingLogs('"):strings.Index(path, "')")]
			return []byte(`{"d":{"MessageGuid":"` + guid + `","CorrelationId":"C` + guid[len(guid)-1:] + `"}}`), nil
		}
		return base(ctx, path)
	}
	var rows []string
	for i := range 200 {
		rows = append(rows, fmt.Sprintf(`{"MessageGuid":"G%03d%d","IntegrationArtifact":{"Id":"CPICTL_Test_Stub"}}`, i, i%5))
	}
	logs := `{"d":{"results":[` + strings.Join(rows, ",") + `]}}`
	list := func() string {
		out, err := g.After(req("GET", "/api/v1/MessageProcessingLogs?$top=200", ""), 200, nil, []byte(logs))
		require.NoError(t, err)
		return string(out)
	}
	out := list()
	assert.NotContains(t, out, "CPICTL_Test_Stub", "other teams' runs stay hidden")
	first := calls.Load()
	assert.LessOrEqual(t, first, int64(maxToolingLookups+5), "at most one lookup per message (capped) and one per group")
	// the rows beyond the cap were hidden in that answer only: the next
	// request looks them up (and then nothing more)
	list()
	second := calls.Load()
	assert.LessOrEqual(t, second-first, int64(maxToolingLookups))
	list()
	assert.Equal(t, second, calls.Load(), "all known now: a request within the cache time asks SAP nothing")
}

// A test message cannot choose its correlation ID under a scope: it would
// join another team's test run, whose tooling messages are decided by their
// correlation group.
func TestGuardRefusesChosenCorrelation(t *testing.T) {
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
	eps := `{"d":{"results":[{"Name":"SD_A","EntryPoints":{"results":[{"Url":"https://rt/http/sd"}]}}]}}`
	endpoints(g, eps)
	_, err := g.After(req("GET", "/api/v1/ServiceEndpoints?$expand=EntryPoints", ""), 200, nil, []byte(eps))
	require.NoError(t, err)
	send := func(h map[string]string) *httpclnt.GuardRequest {
		return &httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt", Path: "/http/sd", Header: h}
	}
	assert.NoError(t, g.Before(send(map[string]string{"Content-Type": "application/xml"})))
	for _, h := range []string{"SAP_MplCorrelationId", "sap_mplcorrelationid", "SAP_MessageProcessingLogID"} {
		r := refused(t, g.Before(send(map[string]string{h: "AGrSWKfjlBddQU_blYCHKdnThF-G"})))
		assert.Contains(t, r.Error(), "correlation group")
	}
}

// IDs in a payload are not recorded (#52): a team that shapes its own
// payload as {"Id":"<foreign ID>"} must not be able to download that ID,
// and payloads are streamed (no answer check).
func TestGuardPayloadIDsAreNotRecorded(t *testing.T) {
	g := testGuard(t, grantsRW)
	logs := `{"d":{"results":[{"MessageGuid":"G1","IntegrationArtifact":{"Id":"SD_A","PackageId":"SD_Orders"}}]}}`
	_, err := g.After(req("GET", "/api/v1/MessageProcessingLogs", ""), 200, nil, []byte(logs))
	require.NoError(t, err)
	list := req("GET", "/api/v1/MessageProcessingLogs('G1')/Attachments", "")
	require.NoError(t, g.Before(list))
	_, err = g.After(list, 200, nil, []byte(`{"d":{"results":[{"Id":"ATT_G1"}]}}`))
	require.NoError(t, err)

	victim := req("GET", "/api/v1/MessageProcessingLogAttachments('ATT_FI_SECRET')/$value", "")
	refused(t, g.Before(victim))
	for _, own := range []string{
		"/api/v1/MessageProcessingLogAttachments('ATT_G1')/$value",
	} {
		r := req("GET", own, "")
		require.NoError(t, g.Before(r))
		c, err := g.Check(r)
		require.NoError(t, err)
		assert.False(t, c.ReadsAnswer, "a payload is streamed, not checked")
		_, err = g.After(r, 200, nil, []byte(`{"Id":"ATT_FI_SECRET","MessageGuid":"G_FI","RunId":"R_FI"}`))
		require.NoError(t, err)
	}
	refused(t, g.Before(victim))
	refused(t, g.Before(req("GET", "/api/v1/MessageStoreEntries('ATT_FI_SECRET')/$value", "")))

	c, err := g.Check(list)
	require.NoError(t, err)
	assert.True(t, c.ReadsAnswer, "SAP's lists are still recorded")
}

// A known artifact keeps the access of its package only while the map is
// fresh: after it moved to another team's package, a stale entry must not
// still allow deploying it (security review of 0.35.1, finding 4).
func TestGuardKnownArtifactExpires(t *testing.T) {
	g := testGuard(t, sdRW)
	deploy := req("POST", "/api/v1/DeployIntegrationDesigntimeArtifact?Id='SD_A'&Version='active'", "")
	require.NoError(t, g.Before(deploy))

	inner := g.Fetch
	g.Fetch = func(ctx context.Context, path string) ([]byte, error) {
		switch {
		case strings.HasPrefix(path, "/api/v1/IntegrationPackages('SD_Orders')/IntegrationDesigntimeArtifacts"):
			return []byte(`{"d":{"results":[]}}`), nil
		case strings.HasPrefix(path, "/api/v1/IntegrationPackages('FI_Billing')/IntegrationDesigntimeArtifacts"):
			return []byte(`{"d":{"results":[{"Id":"FI_B"},{"Id":"SD_A"}]}}`), nil
		}
		return inner(ctx, path)
	}
	assert.NoError(t, g.Before(deploy), "within the refresh time the known owner stands")
	g.mu.Lock()
	g.builtAt = g.builtAt.Add(-time.Hour)
	g.mu.Unlock()
	refused(t, g.Before(deploy))
}

// A run step's trace messages (compound key RunId='…',ChildCount=n) belong
// to the run's message; another message's run stays refused (security
// review of 0.35.1, second report, finding 6).
func TestGuardRunStepTraceMessages(t *testing.T) {
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","download"]}`)
	logs := `{"d":{"results":[{"MessageGuid":"G1","IntegrationArtifact":{"Id":"SD_A","PackageId":"SD_Orders"}}]}}`
	_, err := g.After(req("GET", "/api/v1/MessageProcessingLogs", ""), 200, nil, []byte(logs))
	require.NoError(t, err)
	runs := req("GET", "/api/v1/MessageProcessingLogs('G1')/Runs", "")
	require.NoError(t, g.Before(runs))
	_, err = g.After(runs, 200, nil, []byte(`{"d":{"results":[{"Id":"R1"}]}}`))
	require.NoError(t, err)

	assert.NoError(t, g.Before(req("GET", "/api/v1/MessageProcessingLogRunSteps(RunId='R1',ChildCount=1)/TraceMessages", "")))
	refused(t, g.Before(req("GET", "/api/v1/MessageProcessingLogRunSteps(RunId='R_FI',ChildCount=1)/TraceMessages", "")))
	refused(t, g.Before(req("GET", "/api/v1/MessageProcessingLogRunSteps(ChildCount=1)/TraceMessages", "")))
}

// An allowed flow's entry point does not cover another team's entry point
// below it (wildcard or plain prefix): the most specific entry point of
// the whole tenant decides, also when the caller listed only its own flow;
// without the tenant's entry points nothing is sent (security review of
// 0.35.1, second report, finding 2).
func TestGuardEndpointOfAnotherTeamBelowAnAllowedOne(t *testing.T) {
	for _, own := range []string{"https://rt/http/api/*", "https://rt/http/api"} {
		g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
		all := `{"d":{"results":[{"Name":"SD_A","EntryPoints":{"results":[{"Url":"` + own + `"}]}},
			{"Name":"FI_B","EntryPoints":{"results":[{"Url":"https://rt/http/api/fi_secret"}]}}]}}`
		inner := g.Fetch
		failing := false
		g.Fetch = func(ctx context.Context, path string) ([]byte, error) {
			if strings.HasPrefix(path, "/api/v1/ServiceEndpoints") {
				if failing {
					return nil, fmt.Errorf("made-up outage")
				}
				return []byte(all), nil
			}
			return inner(ctx, path)
		}
		// the caller lists only its own flow
		mine := `{"d":{"results":[{"Name":"SD_A","EntryPoints":{"results":[{"Url":"` + own + `"}]}}]}}`
		_, err := g.After(req("GET", "/api/v1/ServiceEndpoints?$expand=EntryPoints&$filter=Name%20eq%20'SD_A'", ""), 200, nil, []byte(mine))
		require.NoError(t, err)
		send := func(path string) error {
			return g.Before(&httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: path})
		}
		refused(t, send("/http/api/fi_secret"))
		refused(t, send("/http/api/fi_secret/deeper"))
		assert.NoError(t, send("/http/api/orders"), own)

		failing = true
		g.endpointCache().Invalidate()
		r := refused(t, send("/http/api/orders"))
		assert.Contains(t, r.Error(), "could not be read")
	}
}

// Another team's wildcard entry point does not capture a deeper entry point
// of an allowed flow, and the issue's own case (cpicli#53): /http/shared of
// an allowed flow does not reach /http/shared/billing of another team.
func TestGuardEndpointMostSpecificWins(t *testing.T) {
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
	all := `{"d":{"results":[{"Name":"FI_B","EntryPoints":{"results":[{"Url":"https://rt/http/*"},{"Url":"https://rt/http/shared/billing"}]}},
		{"Name":"SD_A","EntryPoints":{"results":[{"Url":"https://rt/http/sd/orders"},{"Url":"https://rt/http/shared"}]}}]}}`
	inner := g.Fetch
	g.Fetch = func(ctx context.Context, path string) ([]byte, error) {
		if strings.HasPrefix(path, "/api/v1/ServiceEndpoints") {
			return []byte(all), nil
		}
		return inner(ctx, path)
	}
	send := func(path string) error {
		return g.Before(&httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: path})
	}
	assert.NoError(t, send("/http/sd/orders"))
	assert.NoError(t, send("/http/sd/orders/x"))
	assert.NoError(t, send("/http/shared"))
	// another team's endpoint is refused like one that does not exist: the
	// same text, without the flow (cpicli#54)
	other := refused(t, send("/http/shared/billing")).Error()
	none := refused(t, send("/nowhere")).Error()
	assert.Equal(t, strings.Replace(none, "/nowhere", "/http/shared/billing", 1), other)
	assert.NotContains(t, other, "FI_B")
	assert.NotContains(t, refused(t, send("/http/sd/other")).Error(), "FI_B") // FI_B's wildcard
}

// A guard without Fetch cannot read every flow's entry points or a flow's
// runtime state: test messages are refused, not decided on partial data.
func TestGuardWithoutFetchRefusesTestMessages(t *testing.T) {
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
	eps := `{"d":{"results":[{"Name":"SD_A","EntryPoints":{"results":[{"Url":"https://rt/http/sd/orders"}]}}]}}`
	endpoints(g, eps)
	_, err := g.After(req("GET", "/api/v1/ServiceEndpoints?$expand=EntryPoints", ""), 200, nil, []byte(eps))
	require.NoError(t, err)
	g.Fetch = nil
	g.endpointCache().Invalidate()
	r := refused(t, g.Before(&httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: "/http/sd/orders"}))
	assert.Contains(t, r.Error(), "could not be read")
	why, _ := g.runsAsRead(context.Background(), "SD_A")
	assert.Contains(t, why, "no Fetch")
}

// The artifact map stays available when a rebuild fails or its request is
// cancelled, rebuilds once for many callers, and is not trusted for ever
// (cpicli#53, availability points from the gateway's verification).
func TestGuardArtifactMapRebuilds(t *testing.T) {
	g := testGuard(t, sdRW)
	deploy := req("POST", "/api/v1/DeployIntegrationDesigntimeArtifact?Id='SD_A'&Version='active'", "")
	require.NoError(t, g.Before(deploy))
	expire := func(by time.Duration) {
		g.mu.Lock()
		g.builtAt = g.builtAt.Add(-by)
		g.failedAt = time.Time{}
		g.mu.Unlock()
	}

	// a failed rebuild keeps the last good map, and pauses before the next try
	inner := g.Fetch
	var lists atomic.Int32
	failing := atomic.Bool{}
	failing.Store(true)
	g.Fetch = func(ctx context.Context, path string) ([]byte, error) {
		if path == "/api/v1/IntegrationPackages" {
			lists.Add(1)
		}
		if failing.Load() && strings.Contains(path, "IntegrationPackages('SD_Orders')/ValueMapping") {
			return nil, fmt.Errorf("HTTP 503")
		}
		return inner(ctx, path)
	}
	expire(time.Minute)
	assert.NoError(t, g.Before(deploy), "the last good map stands")
	assert.NoError(t, g.Before(deploy))
	assert.EqualValues(t, 1, lists.Load(), "no new try within mapRetry")

	// beyond mapMaxStale the old map is not trusted
	expire(mapMaxStale)
	refused(t, g.Before(deploy))

	// a cancelled request does not fail the rebuild
	failing.Store(false)
	expire(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := *deploy
	cancelled.Ctx = ctx
	assert.NoError(t, g.Before(&cancelled))

	// many callers, one rebuild
	expire(time.Hour)
	lists.Store(0)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); assert.NoError(t, g.Before(deploy)) }()
	}
	wg.Wait()
	assert.EqualValues(t, 1, lists.Load())
}

// The runtime decodes a path and drops path parameters before it routes: the
// guard matches the same way, so an escaped or parameterised URL below an
// allowed entry point cannot reach another team's endpoint (cpicli#55).
func TestGuardEndpointMatchedAsTheRuntimeRoutes(t *testing.T) {
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
	endpoints(g, `{"d":{"results":[
		{"Name":"SD_A","EntryPoints":{"results":[{"Url":"https://rt/http/shared/*"},{"Url":"https://rt/cxf/*"},{"Url":"https://rt/gw/odata/SAP/SD_SRV;v=1"}]}},
		{"Name":"FI_B","EntryPoints":{"results":[{"Url":"https://rt/http/shared/billing"},{"Url":"https://rt/cxf/billing"},{"Url":"https://rt/gw/odata/SAP/FI_SRV;v=1"}]}}]}}`)
	send := func(path string) error {
		return g.Before(&httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: path})
	}
	for _, p := range []string{"/http/shared/bill%69ng", "/http/shared/billing;x", "/http/shared/%62illing/sub",
		"/cxf/bill%69ng", "/cxf/billing;jsessionid=1", "/gw/odata/SAP/FI_SRV;v=1/Invoices"} {
		r := refused(t, send(p))
		assert.NotContains(t, r.Error(), "FI_B", p)
	}
	for _, p := range []string{"/http/shared/orders", "/http/shared/ord%65rs", "/cxf/orders", "/gw/odata/SAP/SD_SRV;v=1/Orders"} {
		assert.NoError(t, send(p), p)
	}
}

// Only the tenant's current list decides: an entry point seen earlier for a
// flow that has moved since does not keep its address (cpicli#55).
func TestGuardEndpointFromAnEarlierListDoesNotCount(t *testing.T) {
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test","deploy"]}`)
	before := `{"d":{"results":[{"Name":"SD_A","EntryPoints":{"results":[{"Url":"https://rt/http/shared/billing/x"}]}}]}}`
	endpoints(g, before)
	_, err := g.After(req("GET", "/api/v1/ServiceEndpoints?$expand=EntryPoints", ""), 200, nil, []byte(before))
	require.NoError(t, err)
	send := &httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: "/http/shared/billing/x"}
	require.NoError(t, g.Before(send))

	// SD_A moved, FI_B took the range; the client's own (filtered) list still
	// shows nothing new
	endpoints(g, `{"d":{"results":[{"Name":"SD_A","EntryPoints":{"results":[{"Url":"https://rt/http/orders"}]}},
		{"Name":"FI_B","EntryPoints":{"results":[{"Url":"https://rt/http/shared/billing/*"}]}}]}}`)
	_, err = g.After(req("GET", "/api/v1/ServiceEndpoints?$expand=EntryPoints", ""), 200, nil, []byte(before))
	require.NoError(t, err)
	refused(t, g.Before(send))
}

// One endpoint refresh for many callers; a panicking artifact rebuild does
// not leave the guard waiting for ever (cpicli#55).
func TestGuardRefreshesOnceAndSurvivesAPanic(t *testing.T) {
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test","deploy"]}`)
	var lists atomic.Int32
	endpoints(g, `{"d":{"results":[{"Name":"SD_A","EntryPoints":{"results":[{"Url":"https://rt/http/sd"}]}}]}}`)
	inner := g.Fetch
	g.Fetch = func(ctx context.Context, path string) ([]byte, error) {
		if strings.HasPrefix(path, "/api/v1/ServiceEndpoints") {
			lists.Add(1)
			time.Sleep(20 * time.Millisecond)
		}
		return inner(ctx, path)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NoError(t, g.Before(&httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: "/http/sd"}))
		}()
	}
	wg.Wait()
	assert.EqualValues(t, 1, lists.Load())

	deploy := req("POST", "/api/v1/DeployIntegrationDesigntimeArtifact?Id='SD_A'&Version='active'", "")
	panicking := atomic.Bool{}
	panicking.Store(true)
	g.Fetch = func(ctx context.Context, path string) ([]byte, error) {
		if panicking.Load() && path == "/api/v1/IntegrationPackages" {
			panic("made-up failure")
		}
		return inner(ctx, path)
	}
	g.mu.Lock()
	g.builtAt = time.Time{}
	g.mu.Unlock()
	refused(t, g.Before(deploy))
	panicking.Store(false)
	g.mu.Lock()
	g.failedAt = time.Time{}
	g.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- g.Before(deploy) }()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the guard waits for a rebuild that ended in a panic")
	}
}

// OData services that differ only in ";v=" are different services: each
// version belongs to its own flow, in either list order; two flows listing
// the same URL, or an entry point the guard cannot read, make the URLs they
// cover ambiguous (cpicli#56).
func TestGuardEndpointParametersOfEntryPoints(t *testing.T) {
	sd := `{"Name":"SD_A","EntryPoints":{"results":[{"Url":"https://rt/gw/odata/SAP/SRV;v=1"},{"Url":"https://rt/http/shared/*"}]}}`
	fi := `{"Name":"FI_B","EntryPoints":{"results":[{"Url":"https://rt/gw/odata/SAP/SRV;v=2"}]}}`
	for _, list := range []string{sd + "," + fi, fi + "," + sd} {
		g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
		endpoints(g, `{"d":{"results":[`+list+`]}}`)
		send := func(path string) error {
			return g.Before(&httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: path})
		}
		assert.NoError(t, send("/gw/odata/SAP/SRV;v=1/Orders"), list)
		assert.NoError(t, send("/gw/odata/SAP/SRV;v=1"), list)
		for _, p := range []string{"/gw/odata/SAP/SRV;v=2/Invoices", "/gw/odata/SAP/SRV%3Bv=2/Invoices", "/gw/odata/SAP/SRV/Invoices",
			"/gw/odata/SAP/SRV;v=1;v=2/Invoices"} {
			r := refused(t, send(p))
			assert.NotContains(t, r.Error(), "FI_B", p)
		}
	}

	// two flows list the same URL: neither owns it
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
	endpoints(g, `{"d":{"results":[{"Name":"SD_A","EntryPoints":{"results":[{"Url":"https://rt/http/same"}]}},
		{"Name":"FI_B","EntryPoints":{"results":[{"Url":"https://rt/http/same"}]}}]}}`)
	refused(t, g.Before(&httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: "/http/same"}))

	// an entry point the guard cannot read covers what it might cover
	g = testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
	endpoints(g, `{"d":{"results":[{"Name":"SD_A","EntryPoints":{"results":[{"Url":"https://rt/http/shared/*"},{"Url":"https://rt/http/shared/own"}]}},
		{"Name":"FI_B","EntryPoints":{"results":[{"Url":"https://rt/http/shared/a%2Fb"}]}}]}}`)
	send := func(path string) error {
		return g.Before(&httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: path})
	}
	refused(t, send("/http/shared/a/b"))
	refused(t, send("/http/shared/a/b/c"))
	assert.NoError(t, send("/http/shared/other"), "no reading of a%2Fb covers it")
	assert.NoError(t, send("/http/shared/own"), "a more specific entry point still decides")
}

// A failed read of the endpoint list is not repeated for every message
// (cpicli#56).
func TestGuardEndpointReadBacksOff(t *testing.T) {
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
	var reads atomic.Int32
	inner := g.Fetch
	g.Fetch = func(ctx context.Context, path string) ([]byte, error) {
		if strings.HasPrefix(path, "/api/v1/ServiceEndpoints") {
			reads.Add(1)
			return nil, fmt.Errorf("HTTP 503")
		}
		return inner(ctx, path)
	}
	send := &httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: "/http/sd"}
	for range 5 {
		refused(t, g.Before(send))
	}
	assert.EqualValues(t, 1, reads.Load())
}

// A URL whose segment differs from another team's entry point only in its
// parameters is not decided by a broader entry point; the host is matched
// in any case; odd foreign entry points are ambiguous (cpicli#57).
func TestGuardEndpointHardening(t *testing.T) {
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
	endpoints(g, `{"d":{"results":[
		{"Name":"SD_A","EntryPoints":{"results":[{"Url":"https://rt/gw/odata/SAP"},{"Url":"https://rt/gw/odata/SAP/OWN;v=1"},{"Url":"https://rt/http/*"}]}},
		{"Name":"FI_B","EntryPoints":{"results":[{"Url":"https://rt/gw/odata/SAP/SRV;v=2"},{"Url":"https://rt/gw/odata/SAP/OWN;v=2"},
			{"Url":"https://rt/http/odd/./x"},{"Url":"https://rt/http/q?x=1"},{"Url":"https://rt/http/e//x"}]}}]}}`)
	send := func(base, path string) error {
		return g.Before(&httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: base, Path: path})
	}
	// and what a runtime might also read as v=2 (cpicli#65)
	for _, v := range []string{";V=2", ";v=02", ";v=2;x=1", ";x=1;v=2", ";v=2;", ";v=%202",
		"", ";v=", ";v=%2B2", ";v=2.0", ";v='2'", ";v=%EF%BC%92", ";v=1;v=2", "; v=1",
		";v%3D1", ";v=%31", ";%76=1", ";v=%33"} {
		refused(t, send("https://rt:443", "/gw/odata/SAP/SRV"+v+"/Invoices"))
	}
	assert.NoError(t, send("https://rt:443", "/gw/odata/SAP/SRV;v=3/Invoices"), "a version that provably differs")
	assert.NoError(t, send("https://rt:443", "/gw/odata/SAP/OWN;v=1/Orders"), "the exact parameters still decide")
	refused(t, send("https://rt:443", "/gw/odata/SAP/OWN;v=%31/Orders")) // encoded: proves nothing, not even its own
	assert.NoError(t, send("https://rt:443", "/gw/odata/SAP/Other"), "a name nobody else has")
	assert.NoError(t, send("https://RT:443", "/http/mine"), "host names are not case-sensitive")
	for _, p := range []string{"/http/odd/x", "/http/q", "/http/e/x"} {
		refused(t, send("https://rt:443", p))
	}
	assert.NoError(t, send("https://rt:443", "/http/odd/y"), "no reading of /http/odd/./x covers it")
}

// A refusal tells the caller only the generic text; Audit says what decided,
// for logs the caller cannot read (cpicli#63).
func TestGuardRefusalAudit(t *testing.T) {
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
	endpoints(g, `{"d":{"results":[{"Name":"SD_A","EntryPoints":{"results":[{"Url":"https://rt/http/shared/*"},{"Url":"https://rt/http/same"}]}},
		{"Name":"FI_B","EntryPoints":{"results":[{"Url":"https://rt/http/shared/billing"},{"Url":"https://rt/http/same"},{"Url":"https://rt/gw/SRV;v=2"}]}}]}}`)
	send := func(path string) *Refusal {
		return refused(t, g.Before(&httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: path}))
	}
	for path, audit := range map[string]string{
		"/http/shared/billing": "flow FI_B",
		"/http/same":           "flows FI_B, SD_A cover the URL equally",
		"/gw/SRV;V=2/x":        "FI_B https://rt/gw/SRV;v=2",
		"/nowhere":             "no entry point covers the URL",
	} {
		r := send(path)
		assert.Contains(t, r.Audit, audit, path)
		assert.NotContains(t, r.Error(), "FI_B", path)
		assert.NotContains(t, r.Error(), r.Audit, path)
	}
}

// Guards of one tenant can share the endpoint list: one read for all of them
// (cpicli#63).
func TestGuardSharedEndpointCache(t *testing.T) {
	var reads atomic.Int32
	cache := &EndpointCache{Fetch: func(_ context.Context, path string) ([]byte, error) {
		reads.Add(1)
		return []byte(`{"d":{"results":[{"Name":"SD_A","EntryPoints":{"results":[{"Url":"https://rt/http/sd"}]}}]}}`), nil
	}}
	for range 5 {
		g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
		g.Endpoints = cache
		assert.NoError(t, g.Before(&httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: "/http/sd"}))
	}
	assert.EqualValues(t, 1, reads.Load())
	cache.Invalidate()
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
	g.Endpoints = cache
	assert.NoError(t, g.Before(&httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: "/http/sd"}))
	assert.EqualValues(t, 2, reads.Load())
}

// Invalidate during a running read: the read's (older) list is not stored,
// and its callers read again (cpicli#65).
func TestEndpointCacheInvalidateDuringRead(t *testing.T) {
	var reads atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	var cache *EndpointCache
	cache = &EndpointCache{Fetch: func(_ context.Context, path string) ([]byte, error) {
		if reads.Add(1) == 1 {
			started <- struct{}{}
			<-release // the deploy happens while this read runs
			return []byte(`{"d":{"results":[{"Name":"OLD","EntryPoints":{"results":[{"Url":"https://rt/http/x"}]}}]}}`), nil
		}
		return []byte(`{"d":{"results":[{"Name":"NEW","EntryPoints":{"results":[{"Url":"https://rt/http/x"}]}}]}}`), nil
	}}
	done := make(chan []endpointEntry)
	go func() {
		e, err := cache.get(context.Background())
		assert.NoError(t, err)
		done <- e
	}()
	<-started
	cache.Invalidate()
	close(release)
	got := <-done
	require.Len(t, got, 1)
	assert.Equal(t, "NEW", got[0].flow)
	again, err := cache.get(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "NEW", again[0].flow)
	assert.EqualValues(t, 2, reads.Load())
}

// The harness address map is read from designtime content: a flow that
// changed (and was deployed) after the map was built is read again, so its
// old address no longer reaches it (cpicli#65).
func TestGuardHarnessMapFollowsTheContent(t *testing.T) {
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
	addr := atomic.Value{}
	addr.Store("/sd/in")
	g.ProcessDirectAddresses = func(_ context.Context, id string) ([]string, error) {
		if id == "SD_A" {
			return []string{addr.Load().(string)}, nil
		}
		return nil, nil
	}
	ms := func(t time.Time) string { return fmt.Sprintf("/Date(%d)/", t.UnixMilli()) }
	old := time.Now().Add(-time.Hour)
	set := func(modified, deployed time.Time) {
		runs(g, map[string]string{"SD_A": `{"d":{"Id":"SD_A","Status":"STARTED","Version":"1.0.1","DeployedOn":"` + ms(deployed) + `"}}`},
			map[string]string{"SD_A": `{"d":{"Version":"1.0.1","ModifiedAt":"` + ms(modified) + `"}}`})
	}
	set(old, old.Add(time.Second))
	endpoints(g, `{"d":{"results":[{"Name":"CPICTL_Test_Harness","EntryPoints":{"results":[{"Url":"https://rt/http/cpictl/test"}]}}]}}`)
	call := func(a string) error {
		return g.Before(&httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: "/http/cpictl/test",
			Header: map[string]string{"CpictlTargetAddress": a}})
	}
	require.NoError(t, call("/sd/in"))

	// SD_A now listens on /sd/new: changed and deployed after the map
	addr.Store("/sd/new")
	now := time.Now().Add(time.Second)
	set(now, now.Add(time.Second))
	r := refused(t, call("/sd/in"))
	assert.Contains(t, r.Audit, "no longer listens")
	assert.NoError(t, call("/sd/new"))
}

// A list invalidated during every read is refused after a few reads, not
// read for ever (cpicli#65).
func TestEndpointCacheInvalidatedOnEveryRead(t *testing.T) {
	var reads atomic.Int32
	var cache *EndpointCache
	cache = &EndpointCache{Fetch: func(_ context.Context, path string) ([]byte, error) {
		reads.Add(1)
		cache.Invalidate() // a deploy during each read
		return []byte(`{"d":{"results":[]}}`), nil
	}}
	_, err := cache.get(context.Background())
	assert.ErrorContains(t, err, "changed during each read")
	assert.EqualValues(t, maxDiscardedReads, reads.Load())
}

// Without ProcessDirectAddresses (cpictl without --scope-harness, the
// gateway) every harness call is refused, and says why (cpicli#65).
func TestGuardHarnessOff(t *testing.T) {
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
	endpoints(g, `{"d":{"results":[{"Name":"CPICTL_Test_Harness","EntryPoints":{"results":[{"Url":"https://rt/http/cpictl/test"}]}}]}}`)
	r := refused(t, g.Before(&httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: "https://rt:443", Path: "/http/cpictl/test",
		Header: map[string]string{"CpictlTargetAddress": "/sd/in"}}))
	assert.Contains(t, r.Error(), "test harness (ProcessDirect tests) is off under this scope")
}
