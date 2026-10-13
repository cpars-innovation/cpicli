package lint

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Channel properties as in real content (CloudIntegration LessonsLearnt:
// Content Enricher, IDoc Splitter, Pagination with SFSF).

func odataQuery(extra map[string]string) map[string]string {
	p := map[string]string{"ComponentType": "HCIOData", "direction": "Receiver", "MessageProtocol": "OData V2",
		"cmdVariantUri": "ctype::AdapterVariant/cname::sap:HCIOData/tp::HTTP/mp::OData V2/direction::Receiver/version::1.4",
		"address":       "{{Northwind_URL}}", "operation": "Query(GET)", "resourcePath": "Products",
		"queryOptions": "$select=ProductID,ProductName", "customQueryOptions": "", "odatapagesize": "200", "pagination": "",
		"contentType": "application/atom+xml", "enableBatchProcessing": ""}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func idocReceiver(address string) map[string]string {
	return map[string]string{"ComponentType": "IDOC", "direction": "Receiver", "MessageProtocol": "IDoc SOAP",
		"cmdVariantUri": "ctype::AdapterVariant/cname::sap:IDOC/tp::HTTP/mp::IDoc SOAP/direction::Receiver/version::1.3.1",
		"address":       address, "IDocContentType": "application/x-sap.idoc", "authentication": "Client Certificate",
		"cleanupHeaders": "1", "allowChunking": "1", "requestTimeout": "60000"}
}

var idocSender = map[string]string{"ComponentType": "IDOC", "direction": "Sender", "urlPath": "/idoc/orders",
	"cmdVariantUri": "ctype::AdapterVariant/cname::sap:IDOC/tp::HTTP/mp::IDoc SOAP/direction::Sender/version::1.4.5"}

var jmsReceiver = map[string]string{"ComponentType": "JMS", "direction": "Receiver", "QueueName_outbound": "Q.Orders",
	"cmdVariantUri": "ctype::AdapterVariant/cname::sap:JMS/tp::Not Applicable/mp::Not Applicable/direction::Receiver/version::1.6.3"}

// sapModel is a test .iflw: channels (message flows) and processes.
type sapModel struct {
	channels  []string
	processes []string
}

// channel connects a step (or event) to a receiver or a sender to an event.
func (m *sapModel) channel(id, step string, p map[string]string) {
	src, tgt := step, "Participant_"+id
	if p["direction"] == "Sender" {
		src, tgt = "Participant_"+id, step
	}
	m.channels = append(m.channels, fmt.Sprintf(`<bpmn2:participant id="Participant_%s" name="%s"/><bpmn2:messageFlow id="%s" name="%s" sourceRef="%s" targetRef="%s">%s</bpmn2:messageFlow>`,
		id, id, id, id, src, tgt, props(p)))
}

// tEl is an element of a process: tag, id, name and properties.
type tEl struct {
	tag, id, name string
	props         map[string]string
}

// process adds a process whose elements are connected in order.
func (m *sapModel) process(id, kind string, els ...tEl) {
	var b strings.Builder
	fmt.Fprintf(&b, `<bpmn2:process id="%s" name="%s">%s`, id, id, props(map[string]string{"cmdVariantUri": "ctype::FlowElementVariant/cname::" + kind + "/version::1.1.2"}))
	for i, el := range els {
		var p string
		if el.props != nil {
			p = props(el.props)
		}
		fmt.Fprintf(&b, `<bpmn2:%s id="%s" name="%s">%s</bpmn2:%s>`, el.tag, el.id, el.name, p, el.tag)
		if i > 0 {
			fmt.Fprintf(&b, `<bpmn2:sequenceFlow id="SF_%s_%d" sourceRef="%s" targetRef="%s"/>`, id, i, els[i-1].id, el.id)
		}
	}
	b.WriteString(`</bpmn2:process>`)
	m.processes = append(m.processes, b.String())
}

func (m *sapModel) String() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn2:definitions xmlns:bpmn2="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:ifl="http:///com.sap.ifl.model/Ifl.xsd" id="Definitions_1">
<bpmn2:collaboration id="Collaboration_1" name="Default Collaboration">` + strings.Join(m.channels, "\n") + `</bpmn2:collaboration>
` + strings.Join(m.processes, "\n") + `
</bpmn2:definitions>
`
}

func start(id string) tEl { return tEl{tag: "startEvent", id: id, name: "Start"} }
func end(id string) tEl   { return tEl{tag: "endEvent", id: id, name: "End"} }
func call(id, name string) tEl {
	return tEl{tag: "serviceTask", id: id, name: name, props: map[string]string{"activityType": "ExternalCall",
		"cmdVariantUri": "ctype::FlowstepVariant/cname::ExternalCall/version::1.0.4"}}
}
func splitter(id string) tEl {
	return tEl{tag: "callActivity", id: id, name: "Split Orders", props: map[string]string{"activityType": "Splitter", "splitType": "IteratingSplitter",
		"exprType": "XPath", "splitExprValue": "/Orders/Order", "cmdVariantUri": "ctype::FlowstepVariant/cname::Camel/version::1.1"}}
}
func gather(id string) tEl {
	return tEl{tag: "callActivity", id: id, name: "Gather", props: map[string]string{"activityType": "Gather", "cmdVariantUri": "ctype::FlowstepVariant/cname::Gather/version::1.1"}}
}
func processCall(id, name, sub, target string) tEl {
	return tEl{tag: "callActivity", id: id, name: name, props: map[string]string{"activityType": "ProcessCallElement", "subActivityType": sub,
		"processId": target, "cmdVariantUri": "ctype::FlowstepVariant/cname::" + sub + "/version::1.1"}}
}
func modifier(id, body string) tEl {
	return tEl{tag: "callActivity", id: id, name: "Set Control Record", props: map[string]string{"activityType": "Enricher", "wrapContent": body,
		"cmdVariantUri": "ctype::FlowstepVariant/cname::Enricher/version::1.2.3"}}
}

// sapFlows writes flows (ID -> model and other files) and lints them.
func lintSAPFlows(t *testing.T, flows map[string]*sapModel, files map[string]map[string]string) *Result {
	t.Helper()
	root := t.TempDir()
	for id, m := range flows {
		dir := filepath.Join(root, "SAP", id)
		content := map[string]string{
			"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\nBundle-SymbolicName: " + id + "; singleton:=true\nBundle-Version: 1.0.0\nSAP-BundleType: IntegrationFlow\n",
			"src/main/resources/scenarioflows/integrationflow/" + id + ".iflw": m.String(),
		}
		for name, data := range files[id] {
			content["src/main/resources/"+name] = data
		}
		for name, data := range content {
			p := filepath.Join(dir, filepath.FromSlash(name))
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
			require.NoError(t, os.WriteFile(p, []byte(data), 0o644))
		}
	}
	res, err := Run(context.Background(), Options{Dir: root})
	require.NoError(t, err)
	return res
}

// simple is a flow whose one step calls the receiver channel.
func simple(p map[string]string) *sapModel {
	m := &sapModel{}
	m.channel("Channel", "Call", p)
	m.process("Process_1", "IntegrationProcess", start("Start"), call("Call", "Request Reply"), end("End"))
	return m
}

func rulesOf(res *Result, artifact string, prefix ...string) []string {
	var out []string
	for _, f := range res.Findings {
		if f.Artifact != artifact {
			continue
		}
		for _, p := range prefix {
			if strings.HasPrefix(f.Rule, p) {
				out = append(out, f.Rule)
			}
		}
	}
	sort.Strings(out)
	return out
}

func TestODataRules(t *testing.T) {
	sfsf := map[string]string{"ComponentType": "SuccessFactors", "direction": "Receiver", "MessageProtocol": "OData V2",
		"cmdVariantUri": "ctype::AdapterVariant/cname::sap:SuccessFactors/tp::HTTP/mp::OData V2/direction::Receiver/version::1.5",
		"operation":     "Query(GET)", "resourcePath": "User", "pagination": "1", "odatapagesize": "200", "urlSuffixSfOData": "{{SuccessFactors_urlSuffixSfOData_77}}"}

	// a create per record after an iterating splitter
	split := &sapModel{}
	split.channel("Create", "Call", odataQuery(map[string]string{"operation": "Create(POST)", "resourcePath": "A_SalesOrder"}))
	split.process("Process_1", "IntegrationProcess", start("Start"), splitter("Split"), call("Call", "Create Order"), gather("Gather"), end("End"))
	// an update per page in a looping process call
	loop := &sapModel{}
	loop.channel("Update", "Call", odataQuery(map[string]string{"operation": "Update(PUT)"}))
	loop.process("Process_1", "IntegrationProcess", start("Start"), processCall("Loop", "Per Record", "LoopingProcess", "Process_2"), end("End"))
	loop.process("Process_2", "LocalIntegrationProcess", start("Start2"), call("Call", "Update Product"), end("End2"))
	// a process call in the split path calls the process with the create
	nested := &sapModel{}
	nested.channel("Create", "Call", odataQuery(map[string]string{"operation": "Create(POST)", "resourcePath": "A_SalesOrder"}))
	nested.process("Process_1", "IntegrationProcess", start("Start"), splitter("Split"), processCall("PC", "Post", "NonLoopingProcess", "Process_2"), end("End"))
	nested.process("Process_2", "LocalIntegrationProcess", start("Start2"), call("Call", "Create Order"), end("End2"))
	// after the gather: once
	afterGather := &sapModel{}
	afterGather.channel("Create", "Call", odataQuery(map[string]string{"operation": "Create(POST)", "resourcePath": "A_SalesOrder"}))
	afterGather.process("Process_1", "IntegrationProcess", start("Start"), splitter("Split"), gather("Gather"), call("Call", "Create Order"), end("End"))
	// $batch in the loop
	batch := &sapModel{}
	batch.channel("Create", "Call", odataQuery(map[string]string{"operation": "Create(POST)", "enableBatchProcessing": "1"}))
	batch.process("Process_1", "IntegrationProcess", start("Start"), splitter("Split"), call("Call", "Create Order"), end("End"))
	// a query in the loop is no write
	readLoop := &sapModel{}
	readLoop.channel("Read", "Call", odataQuery(map[string]string{"queryOptions": "$top=10"}))
	readLoop.process("Process_1", "IntegrationProcess", start("Start"), splitter("Split"), call("Call", "Lookup"), end("End"))

	res := lintSAPFlows(t, map[string]*sapModel{
		"Query":          simple(odataQuery(nil)),
		"QueryTop":       simple(odataQuery(map[string]string{"queryOptions": "$select=ProductID&$top=100"})),
		"QueryPaged":     simple(odataQuery(map[string]string{"pagination": "1"})),
		"QueryByKey":     simple(odataQuery(map[string]string{"resourcePath": "Products(1)"})),
		"SFSF":           simple(sfsf),
		"Hardcoded":      simple(odataQuery(map[string]string{"queryOptions": "$filter=CompanyCode eq '1000'&sap-client=100&$top=10"})),
		"HardcodedPath":  simple(odataQuery(map[string]string{"resourcePath": "A_CompanyCode?sap-client=200", "operation": "Read(GET)"})),
		"ParamQuery":     simple(odataQuery(map[string]string{"queryOptions": "$filter=CompanyCode eq '{{CompanyCode}}'&$top=10"})),
		"OtherFilter":    simple(odataQuery(map[string]string{"queryOptions": "$filter=Country eq 'DE'&$top=10"})),
		"SplitCreate":    split,
		"LoopUpdate":     loop,
		"NestedCreate":   nested,
		"AfterGather":    afterGather,
		"BatchInLoop":    batch,
		"ReadInLoop":     readLoop,
		"CreateNoLoop":   simple(odataQuery(map[string]string{"operation": "Create(POST)"})),
		"ParamOperation": simple(odataQuery(map[string]string{"operation": "{{Operation}}"})),
	}, nil)
	odata := func(id string) []string { return rulesOf(res, id, "odata-") }

	paging := findingsOf(res, "odata-query-without-paging", "Query")
	require.Len(t, paging, 1)
	assert.Equal(t, SevWarning, paging[0].Severity)
	assert.Contains(t, paging[0].Message, "queries Products without $top and without paging")
	assert.Equal(t, "Channel", paging[0].Element)
	for _, id := range []string{"QueryTop", "QueryPaged", "QueryByKey", "SFSF", "CreateNoLoop", "ParamOperation", "ParamQuery", "OtherFilter"} {
		assert.Empty(t, odata(id), id)
	}

	assert.Equal(t, []string{"odata-hardcoded-query"}, odata("Hardcoded"))
	hc := findingsOf(res, "odata-hardcoded-query", "Hardcoded")[0]
	assert.Equal(t, SevInfo, hc.Severity)
	assert.Contains(t, hc.Message, "sap-client=100, CompanyCode eq '1000'")
	assert.Equal(t, []string{"odata-hardcoded-query"}, odata("HardcodedPath"), "Read(GET) needs no paging")

	for _, id := range []string{"SplitCreate", "LoopUpdate", "NestedCreate"} {
		assert.Equal(t, []string{"odata-single-calls-in-loop"}, odata(id), id)
	}
	assert.Contains(t, findingsOf(res, "odata-single-calls-in-loop", "SplitCreate")[0].Message, `after the splitter "Split Orders"`)
	assert.Contains(t, findingsOf(res, "odata-single-calls-in-loop", "LoopUpdate")[0].Message, `in the Looping Process Call "Per Record"`)
	assert.Contains(t, findingsOf(res, "odata-single-calls-in-loop", "NestedCreate")[0].Message, `called by "Post" after the splitter`)
	for _, id := range []string{"AfterGather", "BatchInLoop", "ReadInLoop"} {
		assert.Empty(t, odata(id), id)
	}
}

func TestIDocRules(t *testing.T) {
	noIdem := &sapModel{}
	noIdem.channel("IDOC", "Start", idocSender)
	noIdem.process("Process_1", "IntegrationProcess", start("Start"), call("Call", "Map"), end("End"))
	idem := &sapModel{}
	idem.channel("IDOC", "Start", idocSender)
	idem.process("Process_1", "IntegrationProcess", start("Start"), tEl{tag: "callActivity", id: "Idem", name: "Once per IDoc",
		props: map[string]string{"activityType": "ProcessCallElement", "subActivityType": "IdempotentProcessCall",
			"cmdVariantUri": "ctype::FlowstepVariant/cname::IdempotentProcessCall/version::1.1.2", "processId": "Process_2"}}, end("End"))
	idem.process("Process_2", "LocalIntegrationProcess", start("Start2"), end("End2"))
	jms := &sapModel{}
	jms.channel("IDOC", "Start", idocSender)
	jms.channel("Queue", "End", jmsReceiver)
	jms.process("Process_1", "IntegrationProcess", start("Start"), end("End"))

	cm := &sapModel{}
	cm.channel("IDOC", "End", idocReceiver("{{IDoc_URL}}"))
	cm.process("Process_1", "IntegrationProcess", start("Start"), modifier("CM", `<ORDERS05><IDOC BEGIN="1"><EDI_DC40 SEGMENT="1"><TABNAM>EDI_DC40</TABNAM>
<SNDPOR>SAPCOD</SNDPOR><SNDPRN>0HS6M0P</SNDPRN><RCVPOR>SAPCRM</RCVPOR><RCVPRN>${property.Receiver}</RCVPRN></EDI_DC40></IDOC></ORDERS05>`), end("End"))
	const xslLiteral = `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"><xsl:template match="/"><ORDERS05><IDOC><EDI_DC40><RCVPRN>S4HCLNT100</RCVPRN></EDI_DC40></IDOC></ORDERS05></xsl:template></xsl:stylesheet>`
	const xslParam = `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"><xsl:param name="RCVPRN"/><xsl:template match="/"><ORDERS05><IDOC><EDI_DC40><RCVPRN><xsl:value-of select="$RCVPRN"/></RCVPRN></EDI_DC40></IDOC></ORDERS05></xsl:template></xsl:stylesheet>`
	// message mapping bricks as the mapping editor writes them: a target
	// field filled by const (literal) or by getProperty(const) (not literal)
	mmap := func(arg string) string {
		return `<xiObj xmlns="urn:sap-com:xi"><content><tr:XiTrafo xmlns:tr="urn:sap-com:xi:mapping:xitrafo"><tr:MetaData><mappingtool><project><transformation>` +
			`<brick gid="0" path="/ns0:ORDERS05/IDOC/EDI_DC40" type="Dst"><arg><brick gid="0" path="/ns1:Order" type="Src"/></arg></brick>` +
			`<brick gid="0" path="/ns0:ORDERS05/IDOC/EDI_DC40/SNDPRN" type="Dst"><viewData x="200" y="40"/><arg>` + arg + `</arg><group/></brick>` +
			`</transformation></project></mappingtool></tr:MetaData></tr:XiTrafo></content></xiObj>`
	}
	constBrick := `<brick fname="const" fns="dflt" type="Func"><viewData x="76" y="45"/><bindings><param name="value"><value>LS_CPI</value></param></bindings></brick>`
	propBrick := `<brick fname="getProperty" fns="dflt" type="Func"><arg>` + constBrick + `</arg></brick>`

	res := lintSAPFlows(t, map[string]*sapModel{
		"NoIdempotency": noIdem,
		"Idempotent":    idem,
		"JMS":           jms,
		"Receiver":      simple(idocReceiver("https://{{S4_Host}}/sap/bc/srt/idoc?sap-client=100")),
		"LiteralHost":   simple(idocReceiver("http://s4.corp.example:8000/sap/bc/srt/idoc?sap-client={{S4_Client}}")),
		"Parameterised": simple(idocReceiver("https://{{S4_Host}}/sap/bc/srt/idoc?sap-client={{S4_Client}}")),
		"WholeParam":    simple(idocReceiver("{{IDoc_URL}}")),
		"FullyLiteral":  simple(idocReceiver("https://s4.corp.example/sap/bc/srt/idoc?sap-client=100")),
		"Template":      simple(idocReceiver("http://<ServerName>:<port>/sap/bc/srt/idoc?sap-client=<ClientNumber>")),
		"ContentMod":    cm,
		"XSLLiteral":    simple(idocReceiver("{{IDoc_URL}}")),
		"XSLParam":      simple(idocReceiver("{{IDoc_URL}}")),
		"MMapConst":     simple(idocReceiver("{{IDoc_URL}}")),
		"MMapProperty":  simple(idocReceiver("{{IDoc_URL}}")),
	}, map[string]map[string]string{
		"XSLLiteral":   {"mapping/Order_to_ORDERS05.xsl": xslLiteral},
		"XSLParam":     {"mapping/Order_to_ORDERS05.xsl": xslParam},
		"MMapConst":    {"mapping/Order_to_ORDERS05.mmap": mmap(constBrick)},
		"MMapProperty": {"mapping/Order_to_ORDERS05.mmap": mmap(propBrick)},
	})
	idoc := func(id string) []string { return rulesOf(res, id, "idoc-", "hardcoded-endpoint") }

	assert.Equal(t, []string{"idoc-no-idempotency"}, idoc("NoIdempotency"))
	assert.Equal(t, SevInfo, findingsOf(res, "idoc-no-idempotency", "NoIdempotency")[0].Severity)
	assert.Empty(t, idoc("Idempotent"))
	assert.Empty(t, idoc("JMS"))

	assert.Equal(t, []string{"idoc-sap-client-literal"}, idoc("Receiver"))
	lit := findingsOf(res, "idoc-sap-client-literal", "Receiver")[0]
	assert.Equal(t, SevWarning, lit.Severity)
	assert.Contains(t, lit.Message, "fixed sap-client 100")
	assert.Equal(t, []string{"idoc-sap-client-literal"}, idoc("LiteralHost"))
	assert.Contains(t, findingsOf(res, "idoc-sap-client-literal", "LiteralHost")[0].Message, "fixed host s4.corp.example:8000")
	assert.Empty(t, idoc("Parameterised"))
	assert.Empty(t, idoc("WholeParam"))
	assert.Equal(t, []string{"hardcoded-endpoint"}, idoc("FullyLiteral"), "one finding, not two")
	assert.Equal(t, []string{"hardcoded-endpoint"}, idoc("Template"))

	cmf := findingsOf(res, "idoc-control-record-literal", "ContentMod")
	require.Len(t, cmf, 1)
	assert.Contains(t, cmf[0].Message, `the content modifier "Set Control Record" sets the IDoc control record (EDI_DC40) to literal partner values: SNDPOR=SAPCOD, SNDPRN=0HS6M0P, RCVPOR=SAPCRM`)
	assert.NotContains(t, cmf[0].Message, "RCVPRN", "${property.Receiver} is not literal")
	assert.Equal(t, []string{"idoc-control-record-literal"}, idoc("XSLLiteral"))
	assert.Contains(t, findingsOf(res, "idoc-control-record-literal", "XSLLiteral")[0].Message, "RCVPRN=S4HCLNT100")
	assert.Empty(t, idoc("XSLParam"))
	assert.Equal(t, []string{"idoc-control-record-literal"}, idoc("MMapConst"))
	assert.Contains(t, findingsOf(res, "idoc-control-record-literal", "MMapConst")[0].Message, "the mapping Order_to_ORDERS05.mmap sets the IDoc control record (EDI_DC40) to literal partner values: SNDPRN=LS_CPI")
	assert.Empty(t, idoc("MMapProperty"))
}

func TestSAPRulesListed(t *testing.T) {
	ids := map[string]RuleInfo{}
	for _, r := range Rules() {
		ids[r.ID] = r
	}
	for id, group := range map[string]string{"odata-query-without-paging": GroupPerformance, "odata-single-calls-in-loop": GroupPerformance,
		"odata-hardcoded-query": GroupConfiguration, "idoc-sap-client-literal": GroupConfiguration, "idoc-control-record-literal": GroupConfiguration,
		"idoc-no-idempotency": GroupRobustness} {
		assert.Equal(t, group, ids[id].Group, id)
	}
}
