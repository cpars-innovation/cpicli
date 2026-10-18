package testtools

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed scriptflow
var scriptFlowFiles embed.FS

const (
	// ScriptIDPrefix starts the ID of a generated script test flow (one per
	// script and function, deployed for an online run and deleted after).
	ScriptIDPrefix = IDPrefix + "Script_"
	// ScriptPathPrefix starts its address: ScriptPathPrefix + the ID's hash.
	ScriptPathPrefix = "/cpictl/script/"
	// ScriptCaseHeader carries the case's headers and properties as JSON
	// {"headers":{...},"properties":{...}}.
	ScriptCaseHeader = "CpictlCase"
	// ScriptRoleParameter is the externalised user role of its HTTPS sender.
	ScriptRoleParameter = "CPICTL_Script_Role"
)

// ScriptLanguage is the script step a generated flow calls.
type ScriptLanguage string

const (
	GroovyV1   ScriptLanguage = "groovy"    // script API version 1 (GroovyScript 1.1.2)
	GroovyV2   ScriptLanguage = "groovy-v2" // version 2, Groovy 4 (GroovyScript 2.0.0, script/v2/)
	JavaScript ScriptLanguage = "javascript"
)

// ScriptFlow is a generated flow that runs one function of a script on the
// tenant: HTTPS -> set the case's headers and properties -> the script
// under test -> the result as JSON.
type ScriptFlow struct {
	// Owner is the flow or script collection the script comes from, Script
	// its path below src/main/resources/script (e.g. "v2/Keys.groovy").
	Owner, Script, Function string
	Language                ScriptLanguage
	// Files are the owner's files below src/main/resources/script (by
	// slash path) and below src/main/resources/lib ("../lib/<name>"),
	// copied into the flow.
	Files map[string][]byte
	// Role is the user role of the HTTPS sender (default DefaultRole).
	Role string
	// Version is the Bundle-Version (default 1.0.0). A run gives each
	// upload a new one, so changed content replaces a flow kept earlier.
	Version string
}

var reFunction = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// ID is the flow's ID: ScriptIDPrefix and a hash of owner, script and
// function, so a run reuses the flow of an earlier run that was kept.
func (f ScriptFlow) ID() string {
	sum := sha256.Sum256([]byte(f.Owner + "\x00" + f.Script + "\x00" + f.Function))
	return ScriptIDPrefix + hex.EncodeToString(sum[:5])
}

// Path is the flow's address (below <runtime>/http).
func (f ScriptFlow) Path() string {
	return ScriptPathPrefix + strings.TrimPrefix(f.ID(), ScriptIDPrefix)
}

// WriteDir writes the flow's files (the layout of an artifact folder) to
// dir.
func (f ScriptFlow) WriteDir(dir string) error {
	if !reFunction.MatchString(f.Function) {
		return fmt.Errorf("invalid function name %q", f.Function)
	}
	variant, component, sub := "ctype::FlowstepVariant/cname::GroovyScript/version::1.1.2", "1.1", "GroovyScript"
	switch f.Language {
	case GroovyV1:
	case GroovyV2:
		variant, component = "ctype::FlowstepVariant/cname::GroovyScript/version::2.0.0", "2.0"
	case JavaScript:
		// verified on a tenant: the 2.0.0 step does not find the file
		variant, sub = "ctype::FlowstepVariant/cname::JavaScript/version::1.1.2", "JavaScript"
	default:
		return fmt.Errorf("unknown script language %q", f.Language)
	}
	id := f.ID()
	res := filepath.Join(dir, "src", "main", "resources")
	files := map[string][]byte{}
	for name, data := range f.Files {
		clean := path.Clean(name)
		if path.IsAbs(clean) || strings.HasPrefix(clean, "../../") || clean == ".." {
			return fmt.Errorf("invalid file name %q", name)
		}
		if strings.HasPrefix(clean, "cpictl_") {
			return fmt.Errorf("%s: the cpictl_ prefix is reserved for the test flow's own scripts", name)
		}
		files[path.Join("script", clean)] = data
	}
	for _, own := range []string{"cpictl_prepare.groovy", "cpictl_envelope.groovy"} {
		data, err := scriptFlowFiles.ReadFile("scriptflow/" + own)
		if err != nil {
			return err
		}
		files["script/"+own] = data
	}
	propdef, err := scriptFlowFiles.ReadFile("scriptflow/parameters.propdef")
	if err != nil {
		return err
	}
	role := f.Role
	if role == "" {
		role = DefaultRole
	}
	files["parameters.propdef"] = propdef
	files["parameters.prop"] = []byte(ScriptRoleParameter + "=" + role + "\n")
	files["scenarioflows/integrationflow/"+id+".iflw"] = []byte(scriptFlowModel(id, f.Path(), f.Script, f.Function, variant, component, sub))
	name := "cpictl script test " + f.Owner + " " + f.Script + " " + f.Function
	version := f.Version
	if version == "" {
		version = "1.0.0"
	}
	extra := map[string][]byte{
		"META-INF/MANIFEST.MF": []byte("Manifest-Version: 1.0\r\nBundle-ManifestVersion: 2\r\nBundle-Name: " + manifestValue(name) +
			"\r\nBundle-SymbolicName: " + id + "; singleton:=true\r\nBundle-Version: " + version + "\r\nSAP-BundleType: IntegrationFlow\r\n" +
			"SAP-NodeType: IFLMAP\r\nSAP-RuntimeProfile: iflmap\r\nOrigin-Bundle-Name: " + manifestValue(name) + "\r\nOrigin-Bundle-SymbolicName: " + id + "\r\n"),
		"metainfo.prop": []byte("description=cpictl script test of " + f.Owner + " " + f.Script + " " + f.Function +
			" (generated by cpictl test scripts cases --online, deleted after the run; do not transport)\n"),
	}
	for name, data := range files {
		if err := writeFile(filepath.Join(res, filepath.FromSlash(name)), data); err != nil {
			return err
		}
	}
	for name, data := range extra {
		if err := writeFile(filepath.Join(dir, filepath.FromSlash(name)), data); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(name string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	return os.WriteFile(name, data, 0o644)
}

// manifestValue makes an artifact name: letters, digits, space, '.', '-'
// and '_' (the tenant refuses others), not ending with '.', short enough
// for one MANIFEST.MF line.
func manifestValue(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == ' ' || r == '.' || r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			return r
		}
		return '_'
	}, s)
	if len(s) > 56 {
		s = s[:56]
	}
	return strings.TrimRight(s, ".")
}

// scriptFlowModel is the .iflw: the property sets and versions of the XSLT
// test flow (which passes a real tenant's Check) with three script steps.
func scriptFlowModel(id, urlPath, script, function, variant, component, sub string) string {
	x := html.EscapeString
	prop := func(k, v string) string {
		return "<ifl:property><key>" + k + "</key><value>" + x(v) + "</value></ifl:property>"
	}
	step := func(n int, name, script, function, variant, component, sub string) string {
		return fmt.Sprintf(`<bpmn2:callActivity id="CallActivity_%d" name="%s"><bpmn2:extensionElements>%s%s%s%s%s%s%s</bpmn2:extensionElements><bpmn2:incoming>SequenceFlow_%d</bpmn2:incoming><bpmn2:outgoing>SequenceFlow_%d</bpmn2:outgoing></bpmn2:callActivity>`,
			n, x(name), prop("scriptFunction", function), prop("scriptBundleId", ""), prop("componentVersion", component), prop("activityType", "Script"),
			prop("cmdVariantUri", variant), prop("subActivityType", sub), prop("script", script), n, n+1)
	}
	groovy := "ctype::FlowstepVariant/cname::GroovyScript/version::1.1.2"
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn2:definitions xmlns:bpmn2="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" xmlns:dc="http://www.omg.org/spec/DD/20100524/DC" xmlns:di="http://www.omg.org/spec/DD/20100524/DI" xmlns:ifl="http:///com.sap.ifl.model/Ifl.xsd" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" id="Definitions_1">
<bpmn2:collaboration id="Collaboration_1" name="Default Collaboration">
<bpmn2:extensionElements>` + prop("namespaceMapping", "") + prop("allowedHeaderList", "*") + prop("log", "All events") + prop("returnExceptionToSender", "false") + prop("cmdVariantUri", "ctype::IFlowVariant/cname::IFlowConfiguration/version::1.2.4") + `</bpmn2:extensionElements>
<bpmn2:participant id="Participant_Sender" ifl:type="EndpointSender" name="Sender"><bpmn2:extensionElements>` + prop("enableBasicAuthentication", "false") + prop("ifl:type", "EndpointSender") + `</bpmn2:extensionElements></bpmn2:participant>
<bpmn2:participant id="Participant_Process" ifl:type="IntegrationProcess" name="Integration Process" processRef="Process_1"><bpmn2:extensionElements/></bpmn2:participant>
<bpmn2:messageFlow id="MessageFlow_Sender" name="HTTPS" sourceRef="Participant_Sender" targetRef="StartEvent_1"><bpmn2:extensionElements>` +
		prop("ComponentNS", "sap") + prop("ComponentSWCVId", "1.5.0") + prop("ComponentSWCVName", "external") + prop("ComponentType", "HTTPS") +
		prop("MessageProtocol", "None") + prop("MessageProtocolVersion", "1.5.0") + prop("Name", "HTTPS") + prop("TransportProtocol", "HTTPS") +
		prop("TransportProtocolVersion", "1.5.0") + prop("cmdVariantUri", "ctype::AdapterVariant/cname::sap:HTTPS/tp::HTTPS/mp::None/direction::Sender/version::1.5.0") +
		prop("componentVersion", "1.5") + prop("direction", "Sender") + prop("maximumAttachmentSize", "100") + prop("maximumBodySize", "40") +
		prop("senderAuthType", "RoleBased") + prop("urlPath", urlPath) + prop("userRole", "{{"+ScriptRoleParameter+"}}") + prop("xsrfProtection", "0") + `</bpmn2:extensionElements></bpmn2:messageFlow>
</bpmn2:collaboration>
<bpmn2:process id="Process_1" name="Integration Process">
<bpmn2:extensionElements>` + prop("transactionTimeout", "30") + prop("cmdVariantUri", "ctype::FlowElementVariant/cname::IntegrationProcess/version::1.2.1") + prop("transactionalHandling", "Not Required") + `</bpmn2:extensionElements>
<bpmn2:startEvent id="StartEvent_1" name="Start"><bpmn2:extensionElements>` + prop("componentVersion", "1.0") + prop("cmdVariantUri", "ctype::FlowstepVariant/cname::MessageStartEvent/version::1.0") + `</bpmn2:extensionElements><bpmn2:outgoing>SequenceFlow_0</bpmn2:outgoing><bpmn2:messageEventDefinition/></bpmn2:startEvent>
` + step(0, "Set case", "cpictl_prepare.groovy", "", groovy, "1.1", "GroovyScript") + `
` + step(1, "Script under test", script, function, variant, component, sub) + `
` + step(2, "Result", "cpictl_envelope.groovy", "", groovy, "1.1", "GroovyScript") + `
<bpmn2:endEvent id="EndEvent_1" name="End"><bpmn2:extensionElements>` + prop("componentVersion", "1.1") + prop("cmdVariantUri", "ctype::FlowstepVariant/cname::MessageEndEvent/version::1.1.0") + `</bpmn2:extensionElements><bpmn2:incoming>SequenceFlow_3</bpmn2:incoming><bpmn2:messageEventDefinition/></bpmn2:endEvent>
<bpmn2:sequenceFlow id="SequenceFlow_0" sourceRef="StartEvent_1" targetRef="CallActivity_0"/>
<bpmn2:sequenceFlow id="SequenceFlow_1" sourceRef="CallActivity_0" targetRef="CallActivity_1"/>
<bpmn2:sequenceFlow id="SequenceFlow_2" sourceRef="CallActivity_1" targetRef="CallActivity_2"/>
<bpmn2:sequenceFlow id="SequenceFlow_3" sourceRef="CallActivity_2" targetRef="EndEvent_1"/>
</bpmn2:process>
<bpmndi:BPMNDiagram id="BPMNDiagram_1" name="Default Collaboration Diagram"><bpmndi:BPMNPlane bpmnElement="Collaboration_1" id="BPMNPlane_1">` +
		`<bpmndi:BPMNShape bpmnElement="Participant_Process" id="BPMNShape_Participant_Process"><dc:Bounds height="160.0" width="680.0" x="220.0" y="60.0"/></bpmndi:BPMNShape>` +
		`<bpmndi:BPMNShape bpmnElement="StartEvent_1" id="BPMNShape_StartEvent_1"><dc:Bounds height="32.0" width="32.0" x="270.0" y="124.0"/></bpmndi:BPMNShape>` +
		`<bpmndi:BPMNShape bpmnElement="CallActivity_0" id="BPMNShape_CallActivity_0"><dc:Bounds height="60.0" width="100.0" x="362.0" y="110.0"/></bpmndi:BPMNShape>` +
		`<bpmndi:BPMNShape bpmnElement="CallActivity_1" id="BPMNShape_CallActivity_1"><dc:Bounds height="60.0" width="100.0" x="502.0" y="110.0"/></bpmndi:BPMNShape>` +
		`<bpmndi:BPMNShape bpmnElement="CallActivity_2" id="BPMNShape_CallActivity_2"><dc:Bounds height="60.0" width="100.0" x="642.0" y="110.0"/></bpmndi:BPMNShape>` +
		`<bpmndi:BPMNShape bpmnElement="EndEvent_1" id="BPMNShape_EndEvent_1"><dc:Bounds height="32.0" width="32.0" x="802.0" y="124.0"/></bpmndi:BPMNShape>` +
		`<bpmndi:BPMNShape bpmnElement="Participant_Sender" id="BPMNShape_Participant_Sender"><dc:Bounds height="140.0" width="100.0" x="40.0" y="70.0"/></bpmndi:BPMNShape>` +
		edge("SequenceFlow_0", "StartEvent_1", "CallActivity_0", 286, 412) + edge("SequenceFlow_1", "CallActivity_0", "CallActivity_1", 412, 552) +
		edge("SequenceFlow_2", "CallActivity_1", "CallActivity_2", 552, 692) + edge("SequenceFlow_3", "CallActivity_2", "EndEvent_1", 692, 818) +
		`<bpmndi:BPMNEdge bpmnElement="MessageFlow_Sender" id="BPMNEdge_MessageFlow_Sender" sourceElement="BPMNShape_Participant_Sender" targetElement="BPMNShape_StartEvent_1"><di:waypoint x="90.0" xsi:type="dc:Point" y="140.0"/><di:waypoint x="286.0" xsi:type="dc:Point" y="140.0"/></bpmndi:BPMNEdge>` +
		`</bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn2:definitions>
`
}

func edge(id, from, to string, x1, x2 int) string {
	return fmt.Sprintf(`<bpmndi:BPMNEdge bpmnElement="%s" id="BPMNEdge_%s" sourceElement="BPMNShape_%s" targetElement="BPMNShape_%s"><di:waypoint x="%d.0" xsi:type="dc:Point" y="140.0"/><di:waypoint x="%d.0" xsi:type="dc:Point" y="140.0"/></bpmndi:BPMNEdge>`,
		id, id, from, to, x1, x2)
}
