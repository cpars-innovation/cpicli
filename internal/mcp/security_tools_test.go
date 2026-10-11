package mcp

import (
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecurityAndPartnerTools(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.Keystore = []cpitest.KeystoreEntry{{Alias: "old_partner", NotAfter: time.Now().AddDate(0, 1, 0)}}
	mock.PDStrings = map[string]string{"ACME/url": "https://acme"}
	mock.ForbidPaths = []string{"/api/v1/AuditLogs"}
	resp := session(t, mock, t.TempDir(),
		call(1, "list_pd_partners", map[string]any{}),
		call(2, "delete_keystore_entry", map[string]any{"alias": "old_partner", "confirm": false}),
		call(3, "delete_keystore_entry", map[string]any{"alias": "old_partner", "confirm": true}),
		call(4, "list_audit_logs", map[string]any{"since": "2h"}),
		call(5, "generate_key_pair", map[string]any{"alias": "client", "common_name": "client.example.com"}),
		call(7, "create_access_policy", map[string]any{"role_name": "OrdersTeam", "references": []map[string]any{{"name": "Orders", "type": "INTEGRATION_FLOW",
			"condition_attribute": "Name", "condition_value": "Orders_.*", "condition_type": "REGULAR_EXPRESSION"}}}),
	)
	partners := toolResult(t, resp["1"]).StructuredContent
	require.True(t, partners.OK, partners.Error)
	assert.Len(t, partners.Result.(map[string]any)["partners"], 1)
	assert.Equal(t, "usage", toolResult(t, resp["2"]).StructuredContent.ErrorCategory)
	assert.True(t, toolResult(t, resp["3"]).StructuredContent.OK)
	assert.Equal(t, 1, countRequests(mock, "DELETE /api/v1/KeystoreEntries"), "deleted once, only with confirm")
	audit := toolResult(t, resp["4"]).StructuredContent
	require.True(t, audit.OK, "403 is a result")
	assert.Equal(t, "missing_role", audit.Result.(map[string]any)["status"])
	assert.True(t, toolResult(t, resp["5"]).StructuredContent.OK)
	later := session(t, mock, t.TempDir(), call(6, "get_csr", map[string]any{"alias": "client"})) // calls of a session run in parallel
	csr := toolResult(t, later["6"]).StructuredContent
	require.True(t, csr.OK, csr.Error)
	assert.Contains(t, csr.Result.(map[string]any)["pem"], "CERTIFICATE REQUEST")
	assert.True(t, toolResult(t, resp["7"]).StructuredContent.OK)
	require.Len(t, mock.AccessPolicies, 1)
	assert.Len(t, mock.AccessPolicies[0].References, 1)
}

func TestSecurityDeleteToolsAreOptIn(t *testing.T) {
	all := Tools(Config{})
	kept, _, err := ApplyFilters(all, "full", ToolFilter{Allow: []string{"delete_*", "*"}})
	require.NoError(t, err)
	for _, name := range optInTools {
		assert.NotContains(t, toolNames(kept), name)
	}
	kept, _, err = ApplyFilters(all, "develop", ToolFilter{Allow: []string{"list_access_policies", "delete_access_policy"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"list_access_policies", "delete_access_policy"}, toolNames(kept))
}

func countRequests(mock *cpitest.Tenant, prefix string) int {
	n := 0
	for _, r := range mock.Requests() {
		if len(r) >= len(prefix) && r[:len(prefix)] == prefix {
			n++
		}
	}
	return n
}
