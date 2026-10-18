package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Agents choose tools by their descriptions: every tool and every argument
// must be described, and the instructions must mention every tool.
func TestToolsAreDescribed(t *testing.T) {
	for _, tool := range Tools(Config{}) {
		assert.GreaterOrEqual(t, len(tool.Description), 60, "%s: description too short to choose the tool", tool.Name)
		assert.NotEmpty(t, tool.Title, tool.Name)
		props, _ := tool.InputSchema["properties"].(map[string]any)
		for name, p := range props {
			desc, _ := p.(map[string]any)["description"].(string)
			assert.NotEmpty(t, desc, "%s.%s has no description", tool.Name, name)
		}
		if tool.Deprecated == "" { // deprecated tools are not pointed to any more
			assert.Contains(t, Instructions, tool.Name, "instructions do not mention %s", tool.Name)
		}
		_, hasReadOnly := tool.Annotations["readOnlyHint"]
		assert.True(t, hasReadOnly, "%s: readOnlyHint missing", tool.Name)
	}
}

func TestCreatePackageTool(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	resp := session(t, mock, t.TempDir(), call(1, "create_package", map[string]any{"package_id": "Orders", "name": "Orders"}))
	res := toolResult(t, resp["1"])
	require.False(t, res.IsError, res.Content[0].Text)
	assert.Equal(t, "CREATED", res.StructuredContent.Result.(map[string]any)["action"])
}

func TestSendTestMessageTool(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"Orders": {Type: "Integration", DesignVersion: "1"}})
	host, port := mock.HostPort()
	mock.Artifacts["Orders"].EndpointURL = fmt.Sprintf("http://%s:%d/http/orders", host, port)
	mock.Inbound = map[string]*cpitest.Inbound{"/http/orders": {MessageGuid: "G1", Response: "done"}}
	mock.MessageLogSteps = [][]cpitest.MessageLog{{{Guid: "G1", Artifact: "Orders", Status: "COMPLETED"}}}
	newExe := func(url string) (*httpclnt.HTTPExecuter, string, error) {
		return cpi.NewEndpointExecuter(&cpi.ServiceDetails{Userid: "u", Password: "p"}, url)
	}
	cfg := Config{Exe: mock.Executer(), Root: t.TempDir(), LogPollInterval: time.Millisecond, NewEndpointExecuter: newExe}

	resp := sessionWith(t, cfg,
		call(1, "send_test_message", map[string]any{"artifact_id": "Orders", "body": "<a/>", "content_type": "application/xml", "wait_seconds": 5}),
		call(2, "send_test_message", map[string]any{"artifact_id": "Orders", "headers": map[string]string{"Cookie": "x"}}),
	)
	res := toolResult(t, resp["1"])
	require.False(t, res.IsError, res.Content[0].Text)
	result := res.StructuredContent.Result.(map[string]any)
	assert.Equal(t, "G1", result["messageGuid"])
	assert.Equal(t, "COMPLETED", result["log"].(map[string]any)["status"])

	res = toolResult(t, resp["2"])
	assert.True(t, res.IsError)
	assert.Equal(t, "usage", res.StructuredContent.ErrorCategory)
	assert.Len(t, mock.Received, 1)

	// without runtime credentials configured the tool explains itself
	resp = session(t, mock, t.TempDir(), call(1, "send_test_message", map[string]any{"artifact_id": "Orders"}))
	res = toolResult(t, resp["1"])
	assert.True(t, res.IsError)
	assert.Contains(t, res.StructuredContent.Error, "not configured")
}

func TestDiscoverTenantTool(t *testing.T) {
	root := t.TempDir()
	flow := filepath.Join(root, "content", "Pkg", "Flow_A")
	files := map[string]string{
		"META-INF/MANIFEST.MF": "Bundle-SymbolicName: Flow_A\nSAP-BundleType: IntegrationFlow\n",
		"src/main/resources/scenarioflows/integrationflow/Flow_A.iflw": `<definitions><collaboration><extensionElements><property><key>log</key><value>Info</value></property></extensionElements></collaboration></definitions>`,
	}
	for name, content := range files {
		p := filepath.Join(flow, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	mock := cpitest.NewTenant(t, nil)
	resp := session(t, mock, root,
		call(1, "discover_tenant", map[string]any{"local_dir": "content"}),
		call(2, "discover_tenant", map[string]any{"local_dir": "content", "output_file": "../outside.json"}),
	)
	res := toolResult(t, resp["1"])
	require.False(t, res.IsError, res.Content[0].Text)
	summary := res.StructuredContent.Result.(map[string]any)["summary"].(map[string]any)
	assert.EqualValues(t, 1, summary["iflows"])

	data, err := os.ReadFile(filepath.Join(root, ".cpi", "discovery.json"))
	require.NoError(t, err)
	var d map[string]any
	require.NoError(t, json.Unmarshal(data, &d))
	assert.Equal(t, "content", d["source"])

	res = toolResult(t, resp["2"])
	assert.True(t, res.IsError)
	assert.True(t, strings.Contains(res.StructuredContent.Error, "outside"))
	assert.Empty(t, mock.Requests(), "local discovery does not contact the tenant")

	// the graph is written next to discovery.json and can be queried
	require.FileExists(t, filepath.Join(root, ".cpi", "graph.json"))
	resp = session(t, mock, root,
		call(1, "graph_search", map[string]any{"query": "flow_a"}),
		call(2, "graph_neighbors", map[string]any{"node": "Flow_A", "direction": "in"}),
		call(3, "graph_path", map[string]any{"from": "package:Pkg", "to": "Flow_A", "edge_types": []string{"contains"}}),
		call(4, "graph_search", map[string]any{"file": "../graph.json"}),
		call(5, "graph_neighbors", map[string]any{"node": "Flow_A", "edge_types": []string{"links"}}),
	)
	res = toolResult(t, resp["1"])
	require.False(t, res.IsError, res.Content[0].Text)
	matches := res.StructuredContent.Result.(map[string]any)["matches"].([]any)
	require.NotEmpty(t, matches)
	assert.Equal(t, "iflow:Flow_A", matches[0].(map[string]any)["id"])
	res = toolResult(t, resp["2"])
	require.False(t, res.IsError, res.Content[0].Text)
	edges := res.StructuredContent.Result.(map[string]any)["edges"].([]any)
	require.Len(t, edges, 1)
	assert.Equal(t, "package:Pkg", edges[0].(map[string]any)["from"])
	res = toolResult(t, resp["3"])
	require.False(t, res.IsError, res.Content[0].Text)
	assert.Equal(t, true, res.StructuredContent.Result.(map[string]any)["found"])
	for _, id := range []string{"4", "5"} {
		res = toolResult(t, resp[id])
		assert.True(t, res.IsError, id)
		assert.Equal(t, "usage", res.StructuredContent.ErrorCategory, id)
	}
}

const copyTemplateModel = `<bpmn2:definitions xmlns:bpmn2="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:ifl="http:///com.sap.ifl.model/Ifl.xsd"><bpmn2:collaboration>
<bpmn2:messageFlow id="MessageFlow_1"><bpmn2:extensionElements>
<ifl:property><key>ComponentType</key><value>HTTPS</value></ifl:property>
<ifl:property><key>urlPath</key><value>/tpl/in</value></ifl:property>
<ifl:property><key>direction</key><value>Sender</value></ifl:property>
</bpmn2:extensionElements></bpmn2:messageFlow></bpmn2:collaboration></bpmn2:definitions>`

func TestCopyIFlowTool(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"META-INF/MANIFEST.MF": "Bundle-SymbolicName: Tpl; singleton:=true\nBundle-Name: Template\nBundle-Version: 2.1.0\nSAP-BundleType: IntegrationFlow\n",
		"src/main/resources/scenarioflows/integrationflow/Tpl.iflw": copyTemplateModel,
	}
	for name, content := range files {
		p := filepath.Join(root, "content", "Templates", "Tpl", filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	mock := cpitest.NewTenant(t, nil)
	resp := session(t, mock, root,
		call(1, "copy_iflow", map[string]any{"from_dir": "content/Templates/Tpl", "target_dir": "content/Sales/Orders", "id": "Orders",
			"name": "Orders", "addresses": []string{"/orders"}}),
		call(2, "copy_iflow", map[string]any{"from_dir": "content/Templates/Tpl", "target_dir": "../outside", "id": "X", "addresses": []string{"/x"}}),
		call(3, "copy_iflow", map[string]any{"from_dir": "content/Templates/Tpl", "target_dir": "content/Sales/NoAddress", "id": "NoAddress"}),
	)
	res := toolResult(t, resp["1"])
	require.False(t, res.IsError, res.Content[0].Text)
	result := res.StructuredContent.Result.(map[string]any)
	assert.Equal(t, "content/Sales/Orders", result["dir"])
	assert.Equal(t, "content/Templates/Tpl", result["from"])
	mf, err := os.ReadFile(filepath.Join(root, "content", "Sales", "Orders", "META-INF", "MANIFEST.MF"))
	require.NoError(t, err)
	assert.Contains(t, string(mf), "Bundle-SymbolicName: Orders; singleton:=true")
	assert.FileExists(t, filepath.Join(root, "content/Sales/Orders/src/main/resources/scenarioflows/integrationflow/Orders.iflw"))

	for _, id := range []string{"2", "3"} {
		res = toolResult(t, resp[id])
		assert.True(t, res.IsError, id)
		assert.Equal(t, "usage", res.StructuredContent.ErrorCategory, id)
	}
	assert.NoDirExists(t, filepath.Join(root, "content", "Sales", "NoAddress"))
	assert.Empty(t, mock.Requests(), "a local copy does not contact the tenant")
}
