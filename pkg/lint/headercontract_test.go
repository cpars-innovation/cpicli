package lint

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pdAdapter(id, direction, address string) string {
	return `<bpmn2:messageFlow id="` + id + `" sourceRef="x" targetRef="y">` + props(map[string]string{
		"ComponentType": "ProcessDirect", "direction": direction, "address": address,
		"cmdVariantUri": "ctype::AdapterVariant/cname::ProcessDirect/vendor::SAP/version::1.0.0"}) + `</bpmn2:messageFlow>`
}

func TestHeaderEmptyVsAbsent(t *testing.T) {
	root := t.TempDir()
	emptyRow := "<row><cell id='Action'>Create</cell><cell id='Type'>constant</cell><cell id='Value'></cell><cell id='Default'></cell><cell id='Name'>PD_ID</cell><cell id='Datatype'></cell></row>"
	setRow := "<row><cell id='Action'>Create</cell><cell id='Type'>constant</cell><cell id='Value'>4711</cell><cell id='Name'>PD_ID</cell></row>"
	caller := func(id, row, address string) tFlow {
		return tFlow{pkg: "P", id: id, files: map[string]string{"scenarioflows/integrationflow/" + id + ".iflw": bpmn(pdAdapter("MF1", "Receiver", address),
			process("Process_1", "IntegrationProcess", chain("m", "startEvent:S:", "callActivity:CM:Enricher", "endEvent:E:MessageEndEvent")))}}
	}
	flows := []tFlow{
		caller("Caller", emptyRow, "/route/script"),
		caller("CallerRouter", emptyRow, "/route/condition"),
		caller("CallerSet", setRow, "/route/script"),
		caller("CallerTruthy", emptyRow, "/route/truthy"),
		{pkg: "P", id: "CalleeScript", files: map[string]string{"scenarioflows/integrationflow/CalleeScript.iflw": bpmn(pdAdapter("MF1", "Sender", "/route/script"),
			process("Process_1", "IntegrationProcess", chain("m", "startEvent:S:", "callActivity:G:script", "endEvent:E:MessageEndEvent")))},
			scripts: map[string]string{"x.groovy": "def pid = message.getHeader(\"PD_ID\", String)\nif (pid != null) { lookup(pid) }\n"}},
		{pkg: "P", id: "CalleeTruthy", files: map[string]string{"scenarioflows/integrationflow/CalleeTruthy.iflw": bpmn(pdAdapter("MF1", "Sender", "/route/truthy"),
			process("Process_1", "IntegrationProcess", chain("m", "startEvent:S:", "callActivity:G:script", "endEvent:E:MessageEndEvent")))},
			scripts: map[string]string{"x.groovy": "def pid = message.getHeader(\"PD_ID\", String)\nif (pid) { lookup(pid) }\n"}},
		{pkg: "P", id: "CalleeRouter", files: map[string]string{"scenarioflows/integrationflow/CalleeRouter.iflw": bpmn(pdAdapter("MF1", "Sender", "/route/condition"),
			process("Process_1", "IntegrationProcess", `<bpmn2:startEvent id="S" name="S"><bpmn2:outgoing>F1</bpmn2:outgoing></bpmn2:startEvent>
<bpmn2:exclusiveGateway id="R" name="Has partner"><bpmn2:incoming>F1</bpmn2:incoming><bpmn2:outgoing>F2</bpmn2:outgoing></bpmn2:exclusiveGateway>
<bpmn2:endEvent id="E" name="E"><bpmn2:incoming>F2</bpmn2:incoming></bpmn2:endEvent>
<bpmn2:sequenceFlow id="F1" sourceRef="S" targetRef="R"/>
<bpmn2:sequenceFlow id="F2" sourceRef="R" targetRef="E"><bpmn2:conditionExpression><![CDATA[${header.PD_ID} != null]]></bpmn2:conditionExpression></bpmn2:sequenceFlow>
`))}},
	}
	// the content modifier rows
	for i, row := range []string{emptyRow, emptyRow, setRow, emptyRow} {
		f := flows[i]
		key := "scenarioflows/integrationflow/" + f.id + ".iflw"
		f.files[key] = strings.Replace(f.files[key], `<bpmn2:callActivity id="CM" name="CM">`, `<bpmn2:callActivity id="CM" name="CM">`+props(map[string]string{
			"activityType": "Enricher", "cmdVariantUri": "ctype::FlowstepVariant/cname::Enricher/version::1.5.1", "headerTable": row}), 1)
	}
	for _, f := range flows {
		f.write(t, root)
	}
	res, err := Run(context.Background(), Options{Dir: root})
	require.NoError(t, err)
	got := findingsOf(res, "header-empty-vs-absent", "Caller")
	require.Len(t, got, 1, "%v", res.Findings)
	assert.Contains(t, got[0].Message, `content modifier "CM" sets the header PD_ID to an empty value, and CalleeScript (ProcessDirect /route/script) tests it against null in script x.groovy`)
	assert.Equal(t, []string{"CalleeScript"}, got[0].Related)
	got = findingsOf(res, "header-empty-vs-absent", "CallerRouter")
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Message, `the condition of "Has partner"`)
	assert.Empty(t, findingsOf(res, "header-empty-vs-absent", "CallerSet"), "a value is set")
	assert.Empty(t, findingsOf(res, "header-empty-vs-absent", "CallerTruthy"), "Groovy truth treats empty as missing")
}
