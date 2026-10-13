package cpitest

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testIDoc = `<?xml version="1.0" encoding="UTF-8"?>
<ORDERS05><IDOC BEGIN="1"><EDI_DC40 SEGMENT="1"><TABNAM>EDI_DC40</TABNAM><DOCNUM>0000000000004711</DOCNUM><IDOCTYP>ORDERS05</IDOCTYP>
<MESTYP>ORDERS</MESTYP><SNDPRN>S4HCLNT100</SNDPRN><RCVPRN>CPI</RCVPRN></EDI_DC40><E1EDK01 SEGMENT="1"><BELNR>4500017001</BELNR></E1EDK01></IDOC></ORDERS05>`

// sapDemo is the demo's dev tier with receivers that never fail.
func sapDemo(t *testing.T) *Tenant {
	t.Helper()
	m := NewTenant(t, nil)
	require.NoError(t, SeedDemo(m, "dev", time.Now()))
	for name, s := range m.systems {
		s.FailRate = 0
		m.systems[name] = s
	}
	return m
}

type rtResponse struct {
	status int
	header http.Header
	body   string
}

// callRuntime calls a runtime path of the mock with Basic Auth.
func callRuntime(t *testing.T, m *Tenant, method, path, contentType, body, user, password string) rtResponse {
	t.Helper()
	req, err := http.NewRequest(method, m.URL()+path, strings.NewReader(body))
	require.NoError(t, err)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.SetBasicAuth(user, password)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return rtResponse{resp.StatusCode, resp.Header, string(b)}
}

func logOf(t *testing.T, m *Tenant, guid string) MessageLog {
	t.Helper()
	for _, l := range m.MessageLogs() {
		if l.Guid == guid {
			return l
		}
	}
	t.Fatalf("no message log %s", guid)
	return MessageLog{}
}

// The demo's SAP-style flows pass the Check with the editor's versions.
func TestDemoSAPFlowsPassCheck(t *testing.T) {
	m := sapDemo(t)
	for _, id := range []string{"Orders_IDoc_In", "Orders_OData_Service"} {
		a := m.Artifacts[id]
		require.NotNil(t, a, id)
		c, err := readArtifactZip(a.Zip)
		require.NoError(t, err)
		assert.Empty(t, checkFlow(c, DefaultComponents()), id)
		assert.True(t, a.running(), id)
	}
}

// The IDoc sender: IDoc XML at /cxf/<address>, plain or in a SOAP envelope;
// the flow gets the IDoc XML and the IDoc headers, the log the DOCNUM.
func TestIDocSender(t *testing.T) {
	m := sapDemo(t)
	assert.Equal(t, inboundIDoc, m.Inbound["/cxf/orders/idoc"].Kind)

	r := callRuntime(t, m, http.MethodPost, "/cxf/orders/idoc", "application/x-sap.idoc", testIDoc, "u", "p")
	require.Equal(t, http.StatusOK, r.status, r.body)
	assert.Empty(t, r.body, "the IDoc sender answers 200 without a body")
	guid := r.header.Get("SAP_MessageProcessingLogID")
	require.NotEmpty(t, guid)
	l := logOf(t, m, guid)
	assert.Equal(t, "Orders_IDoc_In", l.Artifact)
	assert.Equal(t, "COMPLETED", l.Status)
	assert.Equal(t, "0000000000004711", l.ApplicationID, "DOCNUM, the flow sets no ApplicationMessageId")
	got := m.Received[len(m.Received)-1]
	assert.Equal(t, "ORDERS05", got.Header.Get("SapIDocType"))
	assert.Equal(t, "0000000000004711", got.Header.Get("SapIDocDbId"))
	assert.Equal(t, "0000000000004711", got.Header.Get("SapIDocTransferId"))
	require.Len(t, got.IDocs, 1)
	assert.Equal(t, "S4HCLNT100", got.IDocs[0].SNDPRN)
	assert.Equal(t, "ORDERS", got.IDocs[0].MESTYP)

	soap := `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body>` +
		strings.TrimPrefix(testIDoc, `<?xml version="1.0" encoding="UTF-8"?>`+"\n") + `</soapenv:Body></soapenv:Envelope>`
	r = callRuntime(t, m, http.MethodPost, "/cxf/orders/idoc", "text/xml", soap, "u", "p")
	require.Equal(t, http.StatusOK, r.status, r.body)
	got = m.Received[len(m.Received)-1]
	assert.True(t, strings.HasPrefix(got.Body, "<ORDERS05>"), "the envelope is removed: %s", got.Body)
	assert.Equal(t, "0000000000004711", logOf(t, m, r.header.Get("SAP_MessageProcessingLogID")).ApplicationID)

	r = callRuntime(t, m, http.MethodPost, "/cxf/orders/idoc", "application/xml", "<Order/>", "u", "p")
	assert.Equal(t, http.StatusBadRequest, r.status)
	assert.Contains(t, r.body, "EDI_DC40")
	assert.Equal(t, http.StatusMethodNotAllowed, callRuntime(t, m, http.MethodGet, "/cxf/orders/idoc", "", "", "u", "p").status)
}

// A failing receiver fails the IDoc with HTTP 500 and the error text.
func TestIDocSenderReceiverFails(t *testing.T) {
	m := sapDemo(t)
	m.systems["order-api"] = SystemSpec{Match: "orders-api-*.example.com", FailRate: 1, Status: 503, Error: "HTTP {status} from {host}"}
	r := callRuntime(t, m, http.MethodPost, "/cxf/orders/idoc", "application/x-sap.idoc", testIDoc, "u", "p")
	assert.Equal(t, http.StatusInternalServerError, r.status)
	assert.Contains(t, r.body, "HTTP 503 from orders-api-dev.example.com")
	assert.Equal(t, "FAILED", logOf(t, m, r.header.Get("SAP_MessageProcessingLogID")).Status)
}

// The OData V2 sender: the service root from the EDMX (Demo/Orders),
// $metadata, the service document and requests to the entity set, which
// run the flow (its IDoc receiver calls ERP).
func TestODataSender(t *testing.T) {
	m := sapDemo(t)
	const root = "/gw/odata/Demo/Orders;v=1"
	require.NotNil(t, m.Inbound[root])
	assert.Equal(t, inboundOData, m.Inbound[root].Kind)
	assert.True(t, strings.HasSuffix(m.Artifacts["Orders_OData_Service"].EndpointURL, root))

	var eps struct {
		D struct {
			Results []struct{ Name, Protocol string }
		}
	}
	resp, err := m.Executer().Exec(http.MethodGet, "/api/v1/ServiceEndpoints?$expand=EntryPoints", nil, nil)
	require.NoError(t, err)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&eps))
	_ = resp.Body.Close()
	protocols := map[string]string{}
	for _, e := range eps.D.Results {
		protocols[e.Name] = e.Protocol
	}
	assert.Equal(t, map[string]string{"Orders_In": "REST", "Returns_In": "REST", "Orders_IDoc_In": "SOAP", "Orders_OData_Service": "ODATA"}, protocols)

	r := callRuntime(t, m, http.MethodGet, root+"/$metadata", "", "", "u", "p")
	require.Equal(t, http.StatusOK, r.status)
	assert.Contains(t, r.body, `<EntitySet Name="SalesOrders"`)
	r = callRuntime(t, m, http.MethodGet, root+"/", "", "", "u", "p")
	assert.JSONEq(t, `{"d":{"EntitySets":["SalesOrders"]}}`, r.body)

	received := len(m.Received)
	r = callRuntime(t, m, http.MethodGet, root+"/SalesOrders?$top=2", "", "", "u", "p")
	require.Equal(t, http.StatusOK, r.status, r.body)
	assert.JSONEq(t, `{"d":{"results":[]}}`, r.body)
	l := logOf(t, m, r.header.Get("SAP_MessageProcessingLogID"))
	assert.Equal(t, "Orders_OData_Service", l.Artifact)
	assert.Equal(t, "COMPLETED", l.Status)
	r = callRuntime(t, m, http.MethodGet, root+"/SalesOrders('4711')", "", "", "u", "p")
	assert.JSONEq(t, `{"d":{}}`, r.body)

	r = callRuntime(t, m, http.MethodPost, root+"/SalesOrders", "application/json", `{"OrderNo":"4711"}`, "u", "p")
	require.Equal(t, http.StatusCreated, r.status, r.body)
	assert.Equal(t, `{"OrderNo":"4711"}`, r.body)
	got := m.Received[len(m.Received)-1]
	assert.Equal(t, "POST", got.Method)
	assert.Equal(t, root+"/SalesOrders", got.Path)
	assert.Equal(t, `{"OrderNo":"4711"}`, got.Body)
	assert.Equal(t, http.StatusNoContent, callRuntime(t, m, http.MethodDelete, root+"/SalesOrders('4711')", "", "", "u", "p").status)
	assert.Len(t, m.Received, received+4)

	r = callRuntime(t, m, http.MethodGet, root+"/Invoices", "", "", "u", "p")
	assert.Equal(t, http.StatusNotFound, r.status)
	assert.Contains(t, r.body, "Resource not found for the segment 'Invoices'")
	assert.Equal(t, http.StatusNotFound, callRuntime(t, m, http.MethodGet, "/gw/odata/Demo/Other;v=1/SalesOrders", "", "", "u", "p").status)

	// a configured response is what the flow answers
	m.Inbound[root].Response = `{"d":{"results":[{"OrderNo":"1"}]}}`
	r = callRuntime(t, m, http.MethodGet, root+"/SalesOrders", "", "", "u", "p")
	assert.Equal(t, `{"d":{"results":[{"OrderNo":"1"}]}}`, r.body)
	assert.Equal(t, "application/json", r.header.Get("Content-Type"))
}

// The IDoc receiver is a receiver like the others: the system its host
// belongs to decides latency and failures, in sent messages and in runs
// started by the admin API; its credential must exist.
func TestIDocReceiver(t *testing.T) {
	m := sapDemo(t)
	m.systems["erp"] = SystemSpec{Match: "erp-*.example.com", FailRate: 1, Status: 500, Error: "IDoc not accepted by {host}"}
	r := callRuntime(t, m, http.MethodPost, "/gw/odata/Demo/Orders;v=1/SalesOrders", "application/json", `{"OrderNo":"1"}`, "u", "p")
	assert.Equal(t, http.StatusInternalServerError, r.status)
	assert.Contains(t, r.body, "IDoc not accepted by erp-dev.example.com")
	l := logOf(t, m, r.header.Get("SAP_MessageProcessingLogID"))
	require.NotEmpty(t, l.Steps)
	assert.Equal(t, "Send to IDOC", l.Steps[len(l.Steps)-1].Activity)

	req, err := http.NewRequest(http.MethodPost, m.URL()+"/_mock/run", strings.NewReader(`{"artifact":"Orders_OData_Service","key":"4711"}`))
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	var run struct{ Runs []map[string]string }
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&run))
	_ = resp.Body.Close()
	require.Len(t, run.Runs, 1)
	assert.Equal(t, "FAILED", run.Runs[0]["status"])
	assert.Contains(t, run.Runs[0]["error"], "erp-dev.example.com")

	delete(m.systems, "erp")
	delete(m.Credentials["UserCredentials"], "ERP_User")
	r = callRuntime(t, m, http.MethodPost, "/gw/odata/Demo/Orders;v=1/SalesOrders", "application/json", `{}`, "u", "p")
	assert.Contains(t, r.body, "credential ERP_User not found")
}

// The demo traffic includes IDocs; their ApplicationMessageId is the IDoc
// number.
func TestDemoIDocTraffic(t *testing.T) {
	m := NewTenant(t, nil)
	require.NoError(t, SeedDemo(m, "test", time.Now()))
	n := 0
	for _, l := range m.MessageLogs() {
		if l.Artifact == "Orders_IDoc_In" {
			n++
			assert.Len(t, l.ApplicationID, 16, l.ApplicationID)
			assert.True(t, strings.HasPrefix(l.ApplicationID, "00000000010"), l.ApplicationID)
		}
	}
	assert.Positive(t, n)
}

// With --credentials strict the IDoc and OData endpoints take the runtime
// key only, like /http and /cxf.
func TestSAPSendersStrictCredentials(t *testing.T) {
	m := sapDemo(t)
	m.Auth = &Auth{APIUser: "api", APIPassword: "api-secret", Runtime: Client{ID: "rt", Secret: "rt-secret"}}
	for _, c := range []struct{ method, path, ct, body string }{
		{http.MethodPost, "/cxf/orders/idoc", "application/x-sap.idoc", testIDoc},
		{http.MethodGet, "/gw/odata/Demo/Orders;v=1/SalesOrders", "", ""},
	} {
		assert.Equal(t, http.StatusForbidden, callRuntime(t, m, c.method, c.path, c.ct, c.body, "api", "api-secret").status, c.path)
		assert.Equal(t, http.StatusUnauthorized, callRuntime(t, m, c.method, c.path, c.ct, c.body, "rt", "wrong").status, c.path)
		assert.Equal(t, http.StatusOK, callRuntime(t, m, c.method, c.path, c.ct, c.body, "rt", "rt-secret").status, c.path)
	}
}

// Without an EDMX or address the OData service is SAP/<artifact ID>; an
// address names it.
func TestODataServicePath(t *testing.T) {
	a := &Artifact{Resources: map[string]Resource{}}
	ch := channel{Adapter: adapterODataSender}
	assert.Equal(t, "/gw/odata/SAP/Flow;v=1", odataServicePath("Flow", a, ch))
	ch.Address = "/Sales/Orders;v=2"
	assert.Equal(t, "/gw/odata/Sales/Orders;v=2", odataServicePath("Flow", a, ch))
	ch.Address = "Sales/Orders"
	assert.Equal(t, "/gw/odata/Sales/Orders;v=1", odataServicePath("Flow", a, ch))
	assert.Equal(t, adapterODataSender, adapterOf(map[string]string{"cmdVariantUri": "ctype::AdapterVariant/cname::sap:ODataSender/tp::HTTP/mp::OData V2/direction::Sender/version::1.3.1"}))
	assert.Equal(t, "HTTPS", adapterOf(map[string]string{"ComponentType": "HTTPS"}))
}
