package mcp

import (
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDesignTools(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(mock, "dev", time.Now()))
	mock.Live = true
	vm := map[string]any{"id": "Billing_CountryCodes", "src_agency": "ERP", "src_id": "Country", "tgt_agency": "Finance", "tgt_id": "CountryCode",
		"src_value": "DE", "tgt_value": "GER"}
	confirmed := map[string]any{"confirm": true}
	for k, v := range vm {
		confirmed[k] = v
	}
	resp := session(t, mock, t.TempDir(),
		call(1, "list_value_mappings", map[string]any{}),
		call(2, "set_value_mapping", vm),
		call(3, "set_value_mapping", confirmed),
		call(5, "delete_artifact", map[string]any{"artifact_id": "Returns_In", "confirm": false}),
		call(6, "delete_artifact", map[string]any{"artifact_id": "Returns_In", "confirm": true}),
		call(8, "delete_package", map[string]any{"package_id": "Partners", "confirm": true}),
	)
	list := toolResult(t, resp["1"]).StructuredContent
	require.True(t, list.OK, list.Error)
	assert.Len(t, list.Result.(map[string]any)["valueMappings"], 1)
	assert.Equal(t, "usage", toolResult(t, resp["2"]).StructuredContent.ErrorCategory, "set_value_mapping needs confirm")
	set := toolResult(t, resp["3"]).StructuredContent
	require.True(t, set.OK, set.Error)
	assert.Equal(t, "changed", set.Result.(map[string]any)["action"])
	// calls of one session run concurrently: read after the change
	get := toolResult(t, session(t, mock, t.TempDir(), call(4, "get_value_mapping", map[string]any{"id": "Billing_CountryCodes"}))["4"]).StructuredContent
	require.True(t, get.OK, get.Error)
	values := get.Result.(map[string]any)["schemas"].([]any)[0].(map[string]any)["values"].([]any)
	assert.Equal(t, "GER", values[0].(map[string]any)["tgtValue"])

	assert.Equal(t, "usage", toolResult(t, resp["5"]).StructuredContent.ErrorCategory, "delete needs confirm")
	deployed := toolResult(t, resp["6"]).StructuredContent
	assert.Equal(t, "usage", deployed.ErrorCategory)
	assert.Contains(t, deployed.Error, "undeploy_first")
	del := toolResult(t, session(t, mock, t.TempDir(),
		call(7, "delete_artifact", map[string]any{"artifact_id": "Returns_In", "confirm": true, "undeploy_first": true}))["7"]).StructuredContent
	require.True(t, del.OK, del.Error)
	assert.Equal(t, true, del.Result.(map[string]any)["deleted"])
	assert.Contains(t, toolResult(t, resp["8"]).StructuredContent.Error, "with_artifacts")
}
