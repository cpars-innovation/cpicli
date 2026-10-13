package ops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
}

// Script collections are discovered with what their scripts say about
// themselves, and graph search finds a function by its description.
func TestDiscoverScriptCollections(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"Orders/Orders_Scripts/META-INF/MANIFEST.MF": "Manifest-Version: 1.0\nBundle-SymbolicName: Orders_Scripts\nBundle-Name: Order scripts\nBundle-Version: 1.0.2\nSAP-BundleType: ScriptCollection\n\n",
		"Orders/Orders_Scripts/src/main/resources/script/Headers.groovy": "/**\n * Sets the order number as a custom header for monitoring.\n */\n" +
			"def Message setOrderHeader(Message message) {\n  message\n}\n\nprivate def helper() {}\n",
		"Orders/Orders_In/META-INF/MANIFEST.MF": "Manifest-Version: 1.0\nBundle-SymbolicName: Orders_In\nSAP-BundleType: IntegrationFlow\n\n",
		"Orders/Orders_In/src/main/resources/scenarioflows/integrationflow/Orders_In.iflw": `<?xml version="1.0" encoding="UTF-8"?>
<bpmn2:definitions xmlns:bpmn2="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:ifl="http:///com.sap.ifl.model/Ifl.xsd">
<bpmn2:process id="Process_1"><bpmn2:callActivity id="CallActivity_1" name="Header"><bpmn2:extensionElements>
<ifl:property><key>activityType</key><value>Script</value></ifl:property>
<ifl:property><key>scriptBundleId</key><value>Orders_Scripts</value></ifl:property>
<ifl:property><key>script</key><value>Headers.groovy</value></ifl:property>
<ifl:property><key>scriptFunction</key><value>setOrderHeader</value></ifl:property>
</bpmn2:extensionElements></bpmn2:callActivity></bpmn2:process></bpmn2:definitions>`,
	})
	d, err := DiscoverDir(context.Background(), dir)
	require.NoError(t, err)
	require.Len(t, d.ScriptCollections, 1)
	c := d.ScriptCollections[0]
	assert.Equal(t, "Orders_Scripts", c.ID)
	assert.Equal(t, "Orders", c.PackageID)
	assert.Equal(t, "1.0.2", c.Version)
	require.Len(t, c.Scripts, 1)
	s := c.Scripts[0]
	assert.Equal(t, "Headers.groovy", s.Name)
	assert.Equal(t, "Sets the order number as a custom header for monitoring.", s.Summary)
	require.Len(t, s.Functions, 2)
	assert.Equal(t, "setOrderHeader", s.Functions[0].Name)

	require.Len(t, d.IFlows, 1)
	assert.Equal(t, []CollectionScriptRef{{Collection: "Orders_Scripts", Script: "Headers.groovy", Function: "setOrderHeader"}}, d.IFlows[0].CollectionScripts)

	g := BuildGraph(d)
	res, err := g.Search("custom header for monitoring", []string{NodeScript}, 0)
	require.NoError(t, err)
	require.Len(t, res.Matches, 1)
	m := res.Matches[0]
	assert.Equal(t, "script:Orders_Scripts/Headers.groovy", m.ID)
	assert.Equal(t, "setOrderHeader: Sets the order number as a custom header for monitoring.", m.Attrs["functions"], "private helpers are left out")
	assert.Equal(t, 1, m.Degree, "the flow that calls it")
}
