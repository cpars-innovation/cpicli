package ops

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sapChannel is a message flow with the given properties.
func sapChannel(id string, props map[string]string) string {
	var b strings.Builder
	for _, k := range sortedMapKeys(props) {
		fmt.Fprintf(&b, "<ifl:property><key>%s</key><value>%s</value></ifl:property>", k, props[k])
	}
	return `<bpmn2:messageFlow id="` + id + `" name="` + id + `"><bpmn2:extensionElements>` + b.String() + `</bpmn2:extensionElements></bpmn2:messageFlow>`
}

// The channel properties are those of real content (CloudIntegration
// LessonsLearnt: Content Enricher, IDoc Splitter, Pagination with SFSF).
var (
	idocReceiverProps = map[string]string{"ComponentType": "IDOC", "direction": "Receiver", "MessageProtocol": "IDoc SOAP",
		"cmdVariantUri": "ctype::AdapterVariant/cname::sap:IDOC/tp::HTTP/mp::IDoc SOAP/direction::Receiver/version::1.3.1",
		"address":       "https://{{S4_Host}}/sap/bc/srt/idoc?sap-client=100", "IDocContentType": "application/x-sap.idoc",
		"authentication": "Client Certificate", "credentialName": "", "cleanupHeaders": "1", "allowChunking": "1", "requestTimeout": "60000"}
	idocSenderProps = map[string]string{"ComponentType": "IDOC", "direction": "Sender", "urlPath": "/idoc/orders",
		"cmdVariantUri": "ctype::AdapterVariant/cname::sap:IDOC/tp::HTTP/mp::IDoc SOAP/direction::Sender/version::1.4.5"}
	odataReceiverProps = map[string]string{"ComponentType": "HCIOData", "direction": "Receiver", "MessageProtocol": "OData V2",
		"cmdVariantUri": "ctype::AdapterVariant/cname::sap:HCIOData/tp::HTTP/mp::OData V2/direction::Receiver/version::1.4",
		"address":       "{{Northwind_URL}}", "operation": "Query(GET)", "resourcePath": "Products",
		"queryOptions": "$select=ProductID,ProductName&amp;$filter=Discontinued eq false", "customQueryOptions": "",
		"odatapagesize": "200", "pagination": "", "contentType": "application/atom+xml", "enableBatchProcessing": ""}
	sfsfReceiverProps = map[string]string{"ComponentType": "SuccessFactors", "direction": "Receiver", "MessageProtocol": "OData V2",
		"cmdVariantUri": "ctype::AdapterVariant/cname::sap:SuccessFactors/tp::HTTP/mp::OData V2/direction::Receiver/version::1.5",
		"operation":     "Query(GET)", "resourcePath": "User", "queryOptions": "", "fields": "userId,username",
		"pagination": "1", "odatapagesize": "200", "urlSuffixSfOData": "{{SuccessFactors_urlSuffixSfOData_77}}", "address": ""}
	odataV4CreateProps = map[string]string{"ComponentType": "HCIOData", "direction": "Receiver",
		"cmdVariantUri": "ctype::AdapterVariant/cname::sap:HCIOData/tp::HTTP/mp::OData V4/direction::Receiver/version::1.23.1",
		"address":       "https://s4.example.com/sap/opu/odata4/sap/api_salesorder/srvd_a2x/sap/salesorder/0001/", "operation": "Create(POST)",
		"resourcePath": "SalesOrder"}
)

func sapIFlow() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn2:definitions xmlns:bpmn2="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:ifl="http:///com.sap.ifl.model/Ifl.xsd">
  <bpmn2:collaboration id="Collaboration_1">` +
		sapChannel("IDOC_In", idocSenderProps) + sapChannel("IDOC_Out", idocReceiverProps) + sapChannel("Northwind", odataReceiverProps) +
		sapChannel("SFSF", sfsfReceiverProps) + sapChannel("S4", odataV4CreateProps) + `
  </bpmn2:collaboration>
  <bpmn2:process id="Process_1">
    <bpmn2:callActivity id="CallActivity_1" name="Set Input Message">
      <bpmn2:extensionElements>
        <ifl:property><key>wrapContent</key><value>&lt;CRMXIF_PARTNER_SAVE_M03 xmlns="urn:sap-com:document:sap:idoc:soap:messages"&gt;&lt;IDOC BEGIN="1"&gt;&lt;EDI_DC40 SEGMENT="1"&gt;&lt;IDOCTYP&gt;CRMXIF_PARTNER_SAVE_M03&lt;/IDOCTYP&gt;&lt;/EDI_DC40&gt;&lt;/IDOC&gt;&lt;/CRMXIF_PARTNER_SAVE_M03&gt;</value></ifl:property>
        <ifl:property><key>cmdVariantUri</key><value>ctype::FlowstepVariant/cname::Enricher/version::1.2.3</value></ifl:property>
      </bpmn2:extensionElements>
    </bpmn2:callActivity>
    <bpmn2:exclusiveGateway id="Router_1" name="Route"/>
    <bpmn2:sequenceFlow id="SequenceFlow_1" name="Orders" sourceRef="Router_1" targetRef="CallActivity_1">
      <bpmn2:conditionExpression>/ORDERS05/IDOC/EDI_DC40/MESTYP = 'ORDERS'</bpmn2:conditionExpression>
    </bpmn2:sequenceFlow>
  </bpmn2:process>
</bpmn2:definitions>`
}

const idocXSD = `<?xml version="1.0" encoding="UTF-8"?>
<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema">
  <xsd:element name="ZMATMAS05">
    <xsd:complexType><xsd:sequence><xsd:element name="IDOC">
      <xsd:complexType><xsd:sequence><xsd:element name="EDI_DC40"/><xsd:element name="E1MARAM"/><xsd:element name="VALUE01"/></xsd:sequence></xsd:complexType>
    </xsd:element></xsd:sequence></xsd:complexType>
  </xsd:element>
</xsd:schema>`

func TestAnalyzeIFlowSAPChannels(t *testing.T) {
	fsys := fstest.MapFS{}
	for name, content := range map[string]string{
		"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\nBundle-SymbolicName: SAP_Orders\nBundle-Version: 1.0.0\n",
		"src/main/resources/scenarioflows/integrationflow/SAP_Orders.iflw":           sapIFlow(),
		"src/main/resources/parameters.prop":                                         "Northwind_URL=http\\://services.odata.org/V2/Northwind/Northwind.svc/\nS4_Host=s4.example.com\n",
		"src/main/resources/edmx/services_odata_org_V2_Northwind_Northwind_svc.edmx": "<edmx:Edmx/>",
		"src/main/resources/mapping/ORDERS05_to_SalesOrder.mmap":                     `<brick path="/ns0:ORDERS05/IDOC/EDI_DC40/SNDPRN" type="Src"/>`,
		"src/main/resources/xsd/ZMATMAS05.xsd":                                       idocXSD,
		// no control record: names alone are not IDoc types
		"src/main/resources/xsd/TEST01.xsd": `<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema"><xsd:element name="TEST01"/></xsd:schema>`,
	} {
		fsys[name] = &fstest.MapFile{Data: []byte(content)}
	}
	f, err := AnalyzeIFlow(fsys)
	require.NoError(t, err)

	require.Len(t, f.Triggers, 1)
	assert.Equal(t, &IDocChannel{Direction: "Sender", Path: "/idoc/orders"}, f.Triggers[0].IDoc)
	byAdapter := map[string]Trigger{}
	for _, r := range f.Receivers {
		byAdapter[r.Adapter+":"+r.Address] = r
	}
	idoc := byAdapter["IDOC:https://{{S4_Host}}/sap/bc/srt/idoc?sap-client=100"]
	assert.Equal(t, &IDocChannel{Direction: "Receiver", Path: "/sap/bc/srt/idoc", SAPClient: "100", ContentType: "application/x-sap.idoc"}, idoc.IDoc)
	assert.Nil(t, idoc.OData)

	northwind := byAdapter["HCIOData:{{Northwind_URL}}"].OData
	require.NotNil(t, northwind)
	assert.Equal(t, &ODataChannel{Version: "V2", Operation: "Query(GET)", ResourcePath: "Products", EntitySet: "Products",
		QueryOptions: "$select=ProductID,ProductName&$filter=Discontinued eq false", QueryOptionKeys: []string{"$filter", "$select"},
		EDMX: "edmx/services_odata_org_V2_Northwind_Northwind_svc.edmx", Host: "services.odata.org"}, northwind)

	sfsf := byAdapter["SuccessFactors:"].OData
	require.NotNil(t, sfsf)
	assert.True(t, sfsf.Paging)
	assert.Equal(t, "200", sfsf.PageSize)
	assert.Equal(t, "User", sfsf.EntitySet)
	assert.Empty(t, sfsf.Host)

	s4 := byAdapter["HCIOData:"+odataV4CreateProps["address"]].OData
	require.NotNil(t, s4)
	assert.Equal(t, "V4", s4.Version)
	assert.Equal(t, "s4.example.com", s4.Host)
	assert.Empty(t, s4.EDMX)

	assert.Equal(t, []IDocType{
		{Type: "CRMXIF_PARTNER_SAVE_M03", Sources: []string{"step Set Input Message"}},
		{Type: "ORDERS05", Sources: []string{"condition Orders", "mapping/ORDERS05_to_SalesOrder.mmap"}},
		{Type: "ZMATMAS05", Sources: []string{"xsd/ZMATMAS05.xsd"}},
	}, f.IDocTypes)
	assert.Equal(t, "s4.example.com", f.AddressParameters["S4_Host"])
}

func TestIDocTypeHeuristics(t *testing.T) {
	assert.Equal(t, []string{"ORDERS05", "DEBMAS07"}, idocTypesIn("/ns0:ORDERS05/IDOC/E1EDK01 and /DEBMAS07/IDOC"))
	assert.Equal(t, []string{"ZORDERS05"}, idocTypesIn("${xpath./*/IDOC/EDI_DC40/IDOCTYP} = 'ZORDERS05'"))
	assert.Equal(t, []string{"ZORDERS05"}, idocTypesIn("//EDI_DC40/IDOCTYP != \"ZORDERS05\""))
	assert.Empty(t, idocTypesIn("/Orders/Order and IDOCTYP = 'ORDERS' and /EDI_DC40/IDOC"), "a message type (MESTYP-like name) is no IDoc type")
	assert.Empty(t, idocTypesIn("/Root/STEP01/Item"), "no IDOC below it")

	assert.Equal(t, []string{"MATMAS05"}, idocTypesInFile("mapping/MATMAS05_to_Product.xsl", []byte("<EDI_DC40/>")))
	assert.Empty(t, idocTypesInFile("mapping/MATMAS05_to_Product.xsl", []byte("<Product/>")), "no control record")
	assert.Empty(t, idocTypesInFile("script/MATMAS05.groovy", []byte("EDI_DC40")), "not a mapping or schema")
	assert.Equal(t, []string{"ZMATMAS05", "ZMATMAS05"}, idocTypesInFile("xsd/ZMATMAS05.xsd", []byte(idocXSD)), "file name and root element; nested VALUE01 is no type")
}

func TestODataHelpers(t *testing.T) {
	for op, kind := range map[string]string{"Query(GET)": ODataRead, "Read(GET)": ODataRead, "Create(POST)": ODataWrite, "Update(PUT)": ODataWrite,
		"Merge(MERGE)": ODataWrite, "Patch(PATCH)": ODataWrite, "Delete(DELETE)": ODataWrite, "Function Import": ODataWrite, "{{Operation}}": "", "": ""} {
		assert.Equal(t, kind, ODataOperationKind(op, false), op)
	}
	assert.Equal(t, ODataWrite, ODataOperationKind("", true), "$batch")
	assert.Equal(t, "A_SalesOrder", entitySet("/A_SalesOrder('1')/to_Item"))
	assert.Equal(t, "Products", entitySet("Products?$top=1"))
	assert.Empty(t, entitySet("{{Entity}}"))
	assert.Nil(t, ODataChannelOf(map[string]string{"cmdVariantUri": "ctype::AdapterVariant/cname::sap:SuccessFactors/tp::HTTP/mp::SOAP/direction::Receiver"}), "SuccessFactors SOAP")
	assert.Nil(t, IDocChannelOf(map[string]string{"ComponentType": "SOAP"}))

	host, p, q := SplitAddress("http://<ServerName>:<port>/sap/bc/srt/idoc?sap-client=<ClientNumber>")
	assert.Equal(t, []string{"<ServerName>:<port>", "/sap/bc/srt/idoc", "sap-client=<ClientNumber>"}, []string{host, p, q})
	assert.Equal(t, "<ClientNumber>", QueryValue(q, "sap-client"))
	assert.Empty(t, edmxFor("{{URL}}", []string{"edmx/x.edmx"}))
}

func TestBuildGraphSAP(t *testing.T) {
	flow := func(id string) IFlowFacts {
		return IFlowFacts{ID: id, PackageID: "SAP", Triggers: []Trigger{}, Receivers: []Trigger{}}
	}
	ordersIn := flow("Orders_IDoc_In")
	ordersIn.Triggers = []Trigger{{Adapter: "IDOC", Address: "/idoc/orders", IDoc: &IDocChannel{Direction: "Sender", Path: "/idoc/orders"}}}
	ordersIn.Receivers = []Trigger{{Adapter: "HCIOData", Address: "https://s4.example.com/sap/opu/odata4/salesorder/",
		OData: &ODataChannel{Version: "V4", Operation: "Create(POST)", ResourcePath: "SalesOrder", EntitySet: "SalesOrder", Host: "s4.example.com"}}}
	ordersIn.IDocTypes = []IDocType{{Type: "ORDERS05", Sources: []string{"mapping/ORDERS05_to_SalesOrder.mmap"}}}

	material := flow("Material_Out")
	material.Receivers = []Trigger{{Adapter: "IDOC", Address: "https://{{S4_Host}}/sap/bc/srt/idoc?sap-client=100",
		IDoc: &IDocChannel{Direction: "Receiver", Path: "/sap/bc/srt/idoc", SAPClient: "100"}}}
	material.IDocTypes = []IDocType{{Type: "MATMAS05", Sources: []string{"xsd/MATMAS05.xsd"}}}

	read := flow("Products_Read")
	read.Receivers = []Trigger{{Adapter: "HCIOData", Address: "{{Northwind_URL}}",
		OData: &ODataChannel{Version: "V2", Operation: "Query(GET)", ResourcePath: "Products", EntitySet: "Products", Host: "services.odata.org"}},
		{Adapter: "SuccessFactors", OData: &ODataChannel{Version: "V2", Operation: "Query(GET)", EntitySet: "User", Paging: true}},
		{Adapter: "HCIOData", Address: "{{Other_URL}}", OData: &ODataChannel{Version: "V2", Operation: "Query(GET)", EntitySet: "Customers"}},
		{Adapter: "HCIOData", Address: "{{Other_URL}}", OData: &ODataChannel{Version: "V2", Operation: "{{Operation}}", EntitySet: "Unknown"}}}
	read.AddressParameters = map[string]string{"Northwind_URL": "http://services.odata.org/V2/Northwind/Northwind.svc/"}
	update := flow("Products_Update")
	update.Receivers = []Trigger{{Adapter: "HCIOData", Address: "http://services.odata.org/V2/Northwind/Northwind.svc/",
		OData: &ODataChannel{Version: "V2", Operation: "Update(PUT)", ResourcePath: "Products(1)", EntitySet: "Products", Host: "services.odata.org"}}}
	mappingOnly := flow("Mapping_Only")
	mappingOnly.IDocTypes = []IDocType{{Type: "ORDERS05", Sources: []string{"condition Orders"}}}

	g := BuildGraph(&Discovery{Source: "test", Packages: []DiscoveredPackage{{ID: "SAP"}},
		IFlows: []IFlowFacts{ordersIn, material, read, update, mappingOnly}})
	edges := edgeSet(g.Edges)
	assert.Contains(t, edges, "iflow:Orders_IDoc_In -receives_idoc-> idoc_type:ORDERS05")
	assert.Contains(t, edges, "iflow:Material_Out -sends_idoc-> idoc_type:MATMAS05")
	assert.Contains(t, edges, "iflow:Orders_IDoc_In -writes_odata-> odata_entity:s4.example.com/SalesOrder")
	assert.Contains(t, edges, "iflow:Products_Read -reads_odata-> odata_entity:services.odata.org/Products")
	assert.Contains(t, edges, "iflow:Products_Update -writes_odata-> odata_entity:services.odata.org/Products")
	assert.Contains(t, edges, "iflow:Products_Read -reads_odata-> odata_entity:SuccessFactors/User")
	assert.Contains(t, edges, "iflow:Products_Read -reads_odata-> odata_entity:{{Other_URL}}/Customers@Products_Read", "unresolved: this flow's only")
	assert.NotContains(t, strings.Join(edges, "\n"), "Unknown", "unknown operation: no edge")
	assert.NotContains(t, strings.Join(edges, "\n"), "iflow:Mapping_Only -", "no IDoc channel: no IDoc edge")

	for _, e := range g.Edges {
		switch {
		case e.Type == EdgeReceivesIDoc:
			assert.Equal(t, "mapping/ORDERS05_to_SalesOrder.mmap", e.Via)
		case e.Type == EdgeWritesOData && e.From == "iflow:Products_Update":
			assert.Equal(t, "Update(PUT)", e.Via)
			assert.Equal(t, "HCIOData", e.Adapter)
		}
	}
	products, ok := g.Node("odata_entity:services.odata.org/Products")
	require.True(t, ok)
	assert.Equal(t, map[string]string{"version": "V2", "entitySet": "Products", "host": "services.odata.org", "operations": "Query(GET), Update(PUT)"}, products.Attrs)
	mo, _ := g.Node("iflow:Mapping_Only")
	assert.Equal(t, "ORDERS05", mo.Attrs["idocTypes"])

	res, err := g.Search("ORDERS05", nil, 10)
	require.NoError(t, err)
	require.NotEmpty(t, res.Matches)
	assert.Equal(t, "idoc_type:ORDERS05", res.Matches[0].ID, "exact key first")
	ids := []string{}
	for _, m := range res.Matches {
		ids = append(ids, m.ID)
	}
	assert.Contains(t, ids, "iflow:Mapping_Only", "found through its idocTypes attribute")
	nb, err := g.Neighbors("ORDERS05", NeighborOptions{Direction: "in"})
	require.NoError(t, err)
	assert.Equal(t, []string{"iflow:Orders_IDoc_In -receives_idoc-> idoc_type:ORDERS05"}, edgeSet(nb.Edges))
	_, err = g.Neighbors("ORDERS05", NeighborOptions{EdgeTypes: []string{EdgeSendsIDoc, EdgeReadsOData}})
	assert.NoError(t, err, "new edge types are valid")
}
