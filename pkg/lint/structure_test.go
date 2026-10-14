package lint

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bpmn wraps processes and collaboration content in an .iflw document.
func bpmn(collab, processes string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn2:definitions xmlns:bpmn2="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:ifl="http:///com.sap.ifl.model/Ifl.xsd" id="Definitions_1">
<bpmn2:collaboration id="Collaboration_1">` + collab + `</bpmn2:collaboration>
` + processes + `
</bpmn2:definitions>
`
}

// chain builds the content of a process: a start, the given elements in a
// row (tag:id:kind) and the closing flows. Elements are linked in order.
func chain(prefix string, els ...string) string {
	var b strings.Builder
	ids := make([]string, len(els))
	for i, e := range els {
		ids[i] = strings.SplitN(e, ":", 3)[1]
	}
	flow := func(i int) string { return fmt.Sprintf("%s_F%d", prefix, i) }
	for i, e := range els {
		p := strings.SplitN(e, ":", 3)
		tag, id, kind := p[0], p[1], p[2]
		var ref strings.Builder
		if i > 0 {
			ref.WriteString("<bpmn2:incoming>" + flow(i-1) + "</bpmn2:incoming>")
		}
		if i < len(els)-1 {
			ref.WriteString("<bpmn2:outgoing>" + flow(i) + "</bpmn2:outgoing>")
		}
		pr := map[string]string{}
		switch {
		case strings.HasPrefix(kind, "call="):
			pr = map[string]string{"activityType": "ProcessCallElement", "processId": strings.TrimPrefix(kind, "call="),
				"cmdVariantUri": "ctype::FlowstepVariant/cname::NonLoopingProcess/version::1.0.3"}
		case strings.HasPrefix(kind, "header="):
			pr = map[string]string{"activityType": "Enricher", "cmdVariantUri": "ctype::FlowstepVariant/cname::Enricher/version::1.5.1",
				"headerTable": "<row><cell id='Action'>Create</cell><cell id='Name'>" + strings.TrimPrefix(kind, "header=") + "</cell></row>"}
		case kind == "script":
			pr = map[string]string{"activityType": "Script", "script": "x.groovy", "cmdVariantUri": "ctype::FlowstepVariant/cname::GroovyScript/version::1.1.2"}
		case kind != "":
			pr = map[string]string{"cmdVariantUri": "ctype::FlowstepVariant/cname::" + kind + "/version::1.0.0"}
		}
		fmt.Fprintf(&b, `<bpmn2:%s id="%s" name="%s">%s%s</bpmn2:%s>`+"\n", tag, id, id, props(pr), ref.String(), tag)
	}
	for i := 0; i < len(els)-1; i++ {
		fmt.Fprintf(&b, `<bpmn2:sequenceFlow id="%s" sourceRef="%s" targetRef="%s"/>`+"\n", flow(i), ids[i], ids[i+1])
	}
	return b.String()
}

func process(id, kind, content string) string {
	return `<bpmn2:process id="` + id + `" name="` + id + `">` + props(map[string]string{"cmdVariantUri": "ctype::FlowElementVariant/cname::" + kind + "/version::1.1.0"}) + "\n" + content + "</bpmn2:process>\n"
}

func exceptionSub(id, content string) string {
	return `<bpmn2:subProcess id="` + id + `" name="` + id + `">` + props(map[string]string{"cmdVariantUri": "ctype::FlowElementVariant/cname::ErrorEventSubProcessTemplate/version::1.1.0"}) + "\n" + content + "</bpmn2:subProcess>\n"
}

func TestStructureRules(t *testing.T) {
	root := t.TempDir()
	model := func(id, s string) tFlow {
		return tFlow{pkg: "P", id: id, files: map[string]string{"scenarioflows/integrationflow/" + id + ".iflw": s}}
	}
	flows := []tFlow{
		// an exception subprocess with nothing in it ending in a Message End;
		// a local process nobody calls, with a receiver only it uses
		model("Swallow", bpmn(
			`<bpmn2:participant id="Rcv" name="Backend"/><bpmn2:messageFlow id="MF1" sourceRef="Req" targetRef="Rcv"/>`,
			process("Process_1", "IntegrationProcess",
				chain("m", "startEvent:S:", "callActivity:CM:header=OrderNo", "endEvent:E:MessageEndEvent")+
					exceptionSub("Sub_1", chain("x", "startEvent:XS:ErrorStartEvent", "endEvent:XE:MessageEndEvent")))+
				process("Process_Dead", "LocalIntegrationProcess",
					chain("d", "startEvent:DS:", "serviceTask:Req:ExternalCall", "endEvent:DE:EndEvent")))),
		// SAP's pattern: the exception subprocess calls a local process with
		// an Error End before its Message End; the main process calls a
		// linear local process once
		model("Raise", bpmn("",
			process("Process_1", "IntegrationProcess",
				chain("m", "startEvent:S:", "callActivity:Call:call=Process_Lin", "endEvent:E:MessageEndEvent")+
					exceptionSub("Sub_1", chain("x", "startEvent:XS:ErrorStartEvent", "callActivity:XC:call=Process_Err", "endEvent:XE:MessageEndEvent")))+
				process("Process_Lin", "LocalIntegrationProcess",
					chain("l", "startEvent:LS:", "callActivity:L1:header=orderNo", "callActivity:L2:script", "endEvent:LE:EndEvent"))+
				process("Process_Err", "LocalIntegrationProcess",
					chain("e", "startEvent:ES:", "endEvent:EE:ErrorEndEvent")))),
	}
	flows[1].scripts = map[string]string{"x.groovy": "message.setHeader(\"ORDERNO\", 1)\n"}
	for _, f := range flows {
		f.write(t, root)
	}
	res, err := Run(context.Background(), Options{Dir: root})
	require.NoError(t, err)
	has := func(rule, artifact string, n int) []Finding {
		t.Helper()
		got := findingsOf(res, rule, artifact)
		assert.Len(t, got, n, "%s in %s: %v", rule, artifact, got)
		return got
	}
	f := has("exception-subprocess-message-end", "Swallow", 1)
	assert.Contains(t, f[0].Message, "does nothing")
	has("exception-subprocess-message-end", "Raise", 0)
	f = has("dead-local-process", "Swallow", 1)
	assert.Equal(t, "Process_Dead", f[0].Element)
	assert.Contains(t, f[0].Message, "Backend")
	has("dead-local-process", "Raise", 0)
	f = has("inline-local-process", "Raise", 1)
	assert.Equal(t, "Process_Lin", f[0].Element, "Process_Err is called once too, but ends in an Error End")
	has("inline-local-process", "Swallow", 0)
	f = has("duplicate-header-case", "Raise", 1)
	assert.Contains(t, f[0].Message, "ORDERNO")
	assert.Contains(t, f[0].Message, "orderNo")
	has("duplicate-header-case", "Swallow", 0)
}
