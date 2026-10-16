package mcp

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/scope"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// With a scope, MCP tools get the same guard as the CLI: out-of-scope
// content is hidden or refused with errorCategory "scope".
func TestToolsUnderScope(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"SD_Order_In": {Type: "Integration", DesignVersion: "1", Package: "SD_Orders"},
		"FI_Post":     {Type: "Integration", DesignVersion: "1", Package: "FI_Billing"},
	})
	mock.Packages = []cpitest.Package{{ID: "SD_Orders", Name: "SD", ShortText: "SD"}, {ID: "FI_Billing", Name: "FI", ShortText: "FI"}}
	sc, err := scope.Parse([]byte(`{"version":1,"packages":["SD_*"],"actions":["read"]}`))
	require.NoError(t, err)
	exe := mock.Executer()
	httpclnt.SetGuard(&scope.Guard{Scope: sc, Fetch: func(ctx context.Context, path string) ([]byte, error) {
		resp, err := exe.WithContext(ctx).Exec(http.MethodGet, path, http.NoBody, nil)
		if err != nil {
			return nil, err
		}
		return exe.ReadRespBody(resp)
	}})
	t.Cleanup(func() { httpclnt.SetGuard(nil) })

	resp := session(t, mock, t.TempDir(),
		call(1, "list_packages", map[string]any{}),
		call(2, "list_artifacts", map[string]any{"package_id": "FI_Billing"}),
		call(3, "deploy", map[string]any{"artifact_ids": []string{"SD_Order_In"}}),
	)
	res := toolResult(t, resp["1"])
	require.False(t, res.IsError, res.Content[0].Text)
	text := fmt.Sprint(res.StructuredContent.Result)
	assert.Contains(t, text, "SD_Orders")
	assert.NotContains(t, text, "FI_Billing")

	res = toolResult(t, resp["2"])
	assert.True(t, res.IsError)
	assert.Equal(t, "scope", res.StructuredContent.ErrorCategory, res.Content[0].Text)

	res = toolResult(t, resp["3"])
	assert.True(t, res.IsError)
	assert.Equal(t, "scope", res.StructuredContent.ErrorCategory, res.Content[0].Text)
	assert.Zero(t, mock.Count("POST /api/v1/DeployIntegrationDesigntimeArtifact"))
}
