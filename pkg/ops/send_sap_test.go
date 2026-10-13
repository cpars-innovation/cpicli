package ops

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/idoc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sapDemo is the demo's dev tier and an endpoint executer for it.
func sapDemo(t *testing.T) (*cpitest.Tenant, EndpointExecuterFunc) {
	t.Helper()
	m := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(m, "dev", time.Now()))
	return m, func(endpoint string) (*httpclnt.HTTPExecuter, string, error) {
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, "", err
		}
		p := u.EscapedPath()
		if u.RawQuery != "" {
			p += "?" + u.RawQuery
		}
		return m.Executer().ForEndpoint(u.EscapedPath()), p, nil
	}
}

func TestSendIDoc(t *testing.T) {
	m, newExe := sapDemo(t)
	xml, err := idoc.Template(idoc.TemplateOptions{IDocType: "ORDERS05", MessageType: "ORDERS", SenderPartner: "S4HCLNT100", ReceiverPartner: "CPI", DocNum: "0000000000000815"})
	require.NoError(t, err)
	sent, err := SendTestMessage(context.Background(), m.Executer(), newExe, TestMessage{ArtifactID: "Orders_IDoc_In", IDoc: xml,
		Wait: time.Second, PollInterval: time.Millisecond})
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(sent.URL, "/cxf/orders/idoc"), sent.URL)
	assert.Equal(t, 200, sent.HTTPStatus)
	require.Len(t, sent.IDocs, 1)
	assert.Equal(t, "0000000000000815", sent.IDocs[0].DOCNUM)
	assert.Equal(t, "ORDERS05", sent.IDocs[0].IDOCTYP)
	require.NotNil(t, sent.Log)
	assert.Equal(t, "0000000000000815", sent.Log.ApplicationMessageID)
	got := m.Received[len(m.Received)-1]
	assert.Equal(t, idoc.ContentType, got.Header.Get("Content-Type"))
	assert.Equal(t, "POST", got.Method)

	for _, bad := range []TestMessage{
		{ArtifactID: "Orders_IDoc_In", IDoc: []byte("<Order/>")},
		{ArtifactID: "Orders_IDoc_In", IDoc: xml, Method: "PUT"},
		{ArtifactID: "Orders_IDoc_In", IDoc: xml, Body: []byte("x")},
		{ArtifactID: "Orders_IDoc_In", IDoc: xml, OData: &ODataRequest{}},
		{ArtifactID: "Orders_IDoc_In", IDoc: xml, ProcessDirectAddress: "/x"},
		{ArtifactID: "Orders_In", IDoc: xml}, // an HTTPS flow has no IDoc endpoint
	} {
		_, err := SendTestMessage(context.Background(), m.Executer(), newExe, bad)
		assert.Equal(t, exitcode.Usage, output.ExitCode(err), "%v", err)
	}
}

func TestSendOData(t *testing.T) {
	m, newExe := sapDemo(t)
	sent, err := SendTestMessage(context.Background(), m.Executer(), newExe, TestMessage{ArtifactID: "Orders_OData_Service",
		OData: &ODataRequest{EntitySet: "SalesOrders", Query: "$filter=Customer eq 'C1'&$top=5"}})
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(sent.URL, "/gw/odata/Demo/Orders;v=1/SalesOrders?$filter=Customer%20eq%20%27C1%27&$top=5"), sent.URL)
	assert.JSONEq(t, `{"d":{"results":[]}}`, sent.Response.Text)
	got := m.Received[len(m.Received)-1]
	assert.Equal(t, "GET", got.Method)
	assert.Equal(t, "application/json", got.Header.Get("Accept"))

	sent, err = SendTestMessage(context.Background(), m.Executer(), newExe, TestMessage{ArtifactID: "Orders_OData_Service",
		Body: []byte(`{"OrderNo":"4711"}`), OData: &ODataRequest{Method: "post", EntitySet: "SalesOrders"}})
	require.NoError(t, err)
	assert.Equal(t, 201, sent.HTTPStatus)
	got = m.Received[len(m.Received)-1]
	assert.Equal(t, "application/json", got.Header.Get("Content-Type"))
	assert.Equal(t, `{"OrderNo":"4711"}`, got.Body)

	sent, err = SendTestMessage(context.Background(), m.Executer(), newExe, TestMessage{ArtifactID: "Orders_OData_Service",
		OData: &ODataRequest{EntitySet: "SalesOrders", Key: "'4711'"}})
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(sent.URL, "/SalesOrders('4711')"), sent.URL)

	sent, err = SendTestMessage(context.Background(), m.Executer(), newExe, TestMessage{ArtifactID: "Orders_OData_Service",
		OData: &ODataRequest{EntitySet: "$metadata"}})
	require.NoError(t, err)
	assert.Contains(t, sent.Response.Text, "EntityContainer")

	for _, bad := range []TestMessage{
		{ArtifactID: "Orders_OData_Service", OData: &ODataRequest{Method: "MERGE"}},
		{ArtifactID: "Orders_OData_Service", OData: &ODataRequest{EntitySet: "SalesOrders('1')"}},
		{ArtifactID: "Orders_OData_Service", OData: &ODataRequest{Key: "1"}},
		{ArtifactID: "Orders_OData_Service", Body: []byte("x"), OData: &ODataRequest{EntitySet: "SalesOrders"}},
		{ArtifactID: "Orders_In", OData: &ODataRequest{EntitySet: "SalesOrders"}},
	} {
		_, err := SendTestMessage(context.Background(), m.Executer(), newExe, bad)
		assert.Equal(t, exitcode.Usage, output.ExitCode(err), "%v", err)
	}
	// the OData flow's endpoint is still found without OData options
	url, err := ResolveEndpoint(m.Executer(), "Orders_OData_Service", "")
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(url, "/gw/odata/Demo/Orders;v=1"), url)
}
