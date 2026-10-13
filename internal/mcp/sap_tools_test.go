package mcp

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// idoc_template writes a skeleton (local); send_test_message sends it to the
// demo's IDoc flow and an OData request to its OData service.
func TestIDocAndODataTools(t *testing.T) {
	assert.Equal(t, EffectLocal, EffectOf("idoc_template"))
	assert.Equal(t, ClassLocal, ClassOf("idoc_template"))
	assert.Contains(t, Toolsets["test"], "idoc_template")

	mock := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(mock, "dev", time.Now()))
	newExe := func(endpoint string) (*httpclnt.HTTPExecuter, string, error) {
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, "", err
		}
		p := u.EscapedPath()
		if u.RawQuery != "" {
			p += "?" + u.RawQuery
		}
		return mock.Executer().ForEndpoint(u.EscapedPath()), p, nil
	}
	root := t.TempDir()
	cfg := Config{Exe: mock.Executer(), Root: root, LogPollInterval: time.Millisecond, NewEndpointExecuter: newExe}
	resp := sessionWith(t, cfg,
		call(1, "idoc_template", map[string]any{"idoc_type": "ORDERS05", "message_type": "ORDERS", "sndprn": "S4HCLNT100", "docnum": "0000000000000007", "path": "tests/orders.xml"}),
		call(2, "idoc_template", map[string]any{"idoc_type": "ORDERS05", "message_type": "ORDERS", "path": "../outside.xml"}),
		call(3, "send_test_message", map[string]any{"artifact_id": "Orders_OData_Service", "entity_set": "SalesOrders", "query": "$top=1"}),
		call(4, "send_test_message", map[string]any{"artifact_id": "Orders_OData_Service", "entity_set": "SalesOrders", "method": "GET"}),
	)
	res := toolResult(t, resp["1"])
	require.False(t, res.IsError, res.Content[0].Text)
	text := res.StructuredContent.Result.(map[string]any)["idoc"].(string)
	assert.Contains(t, text, "<DOCNUM>0000000000000007</DOCNUM>")
	written, err := os.ReadFile(filepath.Join(root, "tests", "orders.xml"))
	require.NoError(t, err)
	assert.Equal(t, text, string(written))
	assert.True(t, toolResult(t, resp["2"]).IsError, "outside the root")

	res = toolResult(t, resp["3"])
	require.False(t, res.IsError, res.Content[0].Text)
	assert.Equal(t, float64(200), res.StructuredContent.Result.(map[string]any)["httpStatus"])
	assert.Equal(t, "/gw/odata/Demo/Orders;v=1/SalesOrders", mock.Received[len(mock.Received)-1].Path)
	assert.True(t, toolResult(t, resp["4"]).IsError, "method with OData")

	resp = sessionWith(t, cfg, call(1, "send_test_message", map[string]any{"artifact_id": "Orders_IDoc_In", "idoc": text, "wait_seconds": 5}))
	res = toolResult(t, resp["1"])
	require.False(t, res.IsError, res.Content[0].Text)
	result := res.StructuredContent.Result.(map[string]any)
	idocs := result["idocs"].([]any)
	require.Len(t, idocs, 1)
	assert.Equal(t, "0000000000000007", idocs[0].(map[string]any)["DOCNUM"])
	assert.Equal(t, "0000000000000007", result["log"].(map[string]any)["applicationMessageId"])
}
