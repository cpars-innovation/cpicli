package ops

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testIFlow = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn2:definitions xmlns:bpmn2="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:ifl="http:///com.sap.ifl.model/Ifl.xsd">
  <bpmn2:collaboration id="Collaboration_1">
    <bpmn2:extensionElements>
      <ifl:property><key>log</key><value>All events</value></ifl:property>
      <ifl:property><key>returnExceptionToSender</key><value>true</value></ifl:property>
    </bpmn2:extensionElements>
    <bpmn2:participant id="Participant_1" ifl:type="EndpointSender" name="Sender">
      <bpmn2:extensionElements>
        <ifl:property><key>cmdVariantUri</key><value>ctype::FlowstepVariant/cname::Sender</value></ifl:property>
      </bpmn2:extensionElements>
    </bpmn2:participant>
    <bpmn2:messageFlow id="MessageFlow_1" name="HTTPS">
      <bpmn2:extensionElements>
        <ifl:property><key>ComponentType</key><value>HTTPS</value></ifl:property>
        <ifl:property><key>urlPath</key><value>/orders/in</value></ifl:property>
        <ifl:property><key>direction</key><value>Sender</value></ifl:property>
        <ifl:property><key>cmdVariantUri</key><value>ctype::AdapterVariant/cname::sap:HTTPS/direction::Sender</value></ifl:property>
      </bpmn2:extensionElements>
    </bpmn2:messageFlow>
    <bpmn2:messageFlow id="MessageFlow_2" name="SFTP">
      <bpmn2:extensionElements>
        <ifl:property><key>ComponentType</key><value>SFTP</value></ifl:property>
        <ifl:property><key>direction</key><value>Receiver</value></ifl:property>
        <ifl:property><key>credentialName</key><value>SFTP_Orders</value></ifl:property>
        <ifl:property><key>cmdVariantUri</key><value>ctype::AdapterVariant/cname::sap:SFTP/direction::Receiver</value></ifl:property>
      </bpmn2:extensionElements>
    </bpmn2:messageFlow>
    <bpmn2:messageFlow id="MessageFlow_3" name="ProcessDirect">
      <bpmn2:extensionElements>
        <ifl:property><key>ComponentType</key><value>ProcessDirect</value></ifl:property>
        <ifl:property><key>direction</key><value>Receiver</value></ifl:property>
        <ifl:property><key>address</key><value>/billing/in</value></ifl:property>
        <ifl:property><key>cmdVariantUri</key><value>ctype::AdapterVariant/cname::sap:ProcessDirect/direction::Receiver</value></ifl:property>
      </bpmn2:extensionElements>
    </bpmn2:messageFlow>
  </bpmn2:collaboration>
  <bpmn2:process id="Process_1">
    <bpmn2:extensionElements>
      <ifl:property><key>cmdVariantUri</key><value>ctype::FlowElementVariant/cname::IntegrationProcess</value></ifl:property>
    </bpmn2:extensionElements>
    <bpmn2:callActivity id="CallActivity_1" name="Set headers">
      <bpmn2:extensionElements>
        <ifl:property><key>headerTable</key><value>&lt;row&gt;&lt;cell id='Name'&gt;SAP_ApplicationID&lt;/cell&gt;&lt;/row&gt;</value></ifl:property>
        <ifl:property><key>propertyTable</key><value>&lt;row&gt;&lt;cell id='Name'&gt;orderId&lt;/cell&gt;&lt;/row&gt;</value></ifl:property>
        <ifl:property><key>cmdVariantUri</key><value>ctype::FlowstepVariant/cname::Enricher/version::1.5.1</value></ifl:property>
      </bpmn2:extensionElements>
    </bpmn2:callActivity>
    <bpmn2:callActivity id="CallActivity_2" name="Log">
      <bpmn2:extensionElements>
        <ifl:property><key>script</key><value>logPayload.groovy</value></ifl:property>
        <ifl:property><key>cmdVariantUri</key><value>ctype::FlowstepVariant/cname::GroovyScript/version::1.1.2</value></ifl:property>
      </bpmn2:extensionElements>
    </bpmn2:callActivity>
    <bpmn2:subProcess id="SubProcess_1" name="Exception Subprocess">
      <bpmn2:extensionElements>
        <ifl:property><key>cmdVariantUri</key><value>ctype::FlowElementVariant/cname::ErrorEventSubProcessTemplate/version::1.1.0</value></ifl:property>
      </bpmn2:extensionElements>
      <bpmn2:callActivity id="CallActivity_3" name="Log error">
        <bpmn2:extensionElements>
          <ifl:property><key>cmdVariantUri</key><value>ctype::FlowstepVariant/cname::GroovyScript/version::1.1.2</value></ifl:property>
        </bpmn2:extensionElements>
      </bpmn2:callActivity>
    </bpmn2:subProcess>
  </bpmn2:process>
</bpmn2:definitions>`

const testScript = `import com.sap.gateway.ip.core.customdev.util.Message
def Message processData(Message message) {
    def log = messageLogFactory.getMessageLog(message)
    log.addCustomHeaderProperty("OrderId", "1")
    log.addAttachmentAsString("payload", message.getBody(String), "text/plain")
    return message
}
`

func testIFlowFiles(id string) map[string]string {
	return map[string]string{
		"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\nBundle-SymbolicName: " + id + "; singleton:=true\nBundle-Name: " + id + " Flow\nBundle-Version: 1.0.3\nSAP-BundleType: IntegrationFlow\n",
		"src/main/resources/scenarioflows/integrationflow/" + id + ".iflw": testIFlow,
		"src/main/resources/script/logPayload.groovy":                      testScript,
		"src/main/resources/mapping/Order.mmap":                            "x",
		"src/main/resources/parameters.prop":                               "#comment\nReceiver\\ Host=sftp.example.com\nDirectory=/in\n",
	}
}

func TestAnalyzeIFlow(t *testing.T) {
	fsys := fstest.MapFS{}
	for name, content := range testIFlowFiles("Orders_In") {
		fsys[name] = &fstest.MapFile{Data: []byte(content)}
	}
	f, err := AnalyzeIFlow(fsys)
	require.NoError(t, err)
	assert.Equal(t, "Orders_In", f.ID)
	assert.Equal(t, "Orders_In Flow", f.Name)
	assert.Equal(t, "1.0.3", f.Version)
	assert.Equal(t, []string{"HTTPS"}, f.SenderAdapters)
	assert.Equal(t, []string{"ProcessDirect", "SFTP"}, f.ReceiverAdapters)
	assert.Equal(t, []Trigger{{Adapter: "HTTPS", Address: "/orders/in"}}, f.Triggers)
	assert.Equal(t, []string{"/billing/in"}, f.ProcessDirectCalls)
	assert.Equal(t, map[string]int{"Enricher": 1, "GroovyScript": 2, "ErrorEventSubProcessTemplate": 1}, f.Steps)
	assert.True(t, f.ExceptionSubprocess)
	assert.Equal(t, "All events", f.LogLevel)
	assert.Equal(t, "true", f.ReturnExceptionToSender)
	assert.Equal(t, []string{"Directory", "Receiver Host"}, f.Parameters)
	assert.Equal(t, []string{"SFTP_Orders"}, f.CredentialRefs)

	// SAP's SFTP receiver names the credential in credential_name.
	files := testIFlowFiles("Orders_In")
	for name, content := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(strings.ReplaceAll(content, "<key>credentialName</key>", "<key>credential_name</key>"))}
	}
	f, err = AnalyzeIFlow(fsys)
	require.NoError(t, err)
	assert.Equal(t, []string{"SFTP_Orders"}, f.CredentialRefs)
	assert.Equal(t, []string{"SAP_ApplicationID"}, f.HeadersSet)
	assert.Equal(t, []string{"orderId"}, f.PropertiesSet)
	assert.Equal(t, map[string]int{"script": 1, "mapping": 1}, f.Resources)
	require.Len(t, f.Scripts, 1)
	s := f.Scripts[0]
	assert.Equal(t, "groovy", s.Language)
	assert.Equal(t, 7, s.Lines)
	assert.Equal(t, []string{"OrderId"}, s.CustomHeaders)
	assert.True(t, s.LogsAttachments)
	assert.Len(t, s.Hash, 12)
}

func zipFiles(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestDiscoverTenant(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"Orders_In":  {Type: "Integration", DesignVersion: "1.0.3", Package: "SalesOrders", Name: "Orders In", Zip: zipFiles(t, testIFlowFiles("Orders_In")), Runtime: &cpitest.Runtime{Version: "1.0.3", Status: "STARTED"}},
		"Orders_Out": {Type: "Integration", DesignVersion: "1", Package: "SalesOrders", Zip: zipFiles(t, testIFlowFiles("Orders_Out"))},
		"Broken":     {Type: "Integration", DesignVersion: "1", Package: "SalesOrders"},
		"OrderMap":   {Type: "MessageMapping", DesignVersion: "1", Package: "SalesOrders"},
		"Other":      {Type: "Integration", DesignVersion: "1", Package: "Other", Zip: zipFiles(t, testIFlowFiles("Other"))},
	})
	mock.Packages = []cpitest.Package{{ID: "SalesOrders", Name: "Sales Orders"}, {ID: "Other"}}

	d, err := DiscoverTenant(context.Background(), mock.Executer(), "tenant", DiscoverOptions{PackageIDs: []string{"SalesOrders", "Missing"}})
	require.NoError(t, err)
	require.Len(t, d.Packages, 1)
	assert.Equal(t, map[string]int{"Integration": 3, "MessageMapping": 1}, d.Packages[0].Artifacts)
	require.Len(t, d.IFlows, 3)
	byID := map[string]IFlowFacts{}
	for _, f := range d.IFlows {
		byID[f.ID] = f
	}
	assert.Equal(t, "STARTED", byID["Orders_In"].RuntimeStatus)
	assert.Equal(t, "SalesOrders", byID["Orders_In"].PackageID)
	assert.NotEmpty(t, byID["Broken"].Error)
	assert.Contains(t, d.Errors, "package Missing not found")

	s := d.Summary
	assert.Equal(t, 3, s.IFlows)
	assert.Equal(t, []Count{{"HTTPS", 2}}, s.SenderAdapters)
	assert.Equal(t, 2, s.WithExceptionSubprocess)
	assert.Equal(t, []Count{{"SFTP_Orders", 2}}, s.CredentialRefs)
	assert.Equal(t, []Count{{"OrderId", 2}}, s.CustomHeaderProperties)
	require.Len(t, s.SharedScripts, 1)
	assert.Equal(t, []string{"Orders_In", "Orders_Out"}, s.SharedScripts[0].IFlows)
	assert.Equal(t, []Count{{"Orders", 2}}, s.IFlowNaming.Prefixes[:1])
	assert.Equal(t, 2, s.IFlowNaming.Separators["underscore"])
	// read only: no modifying request reached the tenant
	for _, r := range mock.Requests() {
		assert.Regexp(t, `^GET `, r)
	}
}

func TestDiscoverDir(t *testing.T) {
	root := t.TempDir()
	for _, id := range []string{"Orders_In", "Orders_Out"} {
		for name, content := range testIFlowFiles(id) {
			p := filepath.Join(root, "SalesOrders", id, filepath.FromSlash(name))
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
			require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
		}
	}
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git", "x", "META-INF"), 0o755))

	d, err := DiscoverDir(context.Background(), root)
	require.NoError(t, err)
	require.Len(t, d.IFlows, 2)
	assert.Equal(t, "SalesOrders/Orders_In", d.IFlows[0].Path)
	assert.Equal(t, "SalesOrders", d.IFlows[0].PackageID)
	assert.Equal(t, []DiscoveredPackage{{ID: "SalesOrders", Artifacts: map[string]int{"Integration": 2}}}, d.Packages)
	assert.Empty(t, d.Errors)

	_, err = DiscoverDir(context.Background(), filepath.Join(root, "missing"))
	require.Error(t, err)
}
