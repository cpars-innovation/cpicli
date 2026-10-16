package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scopeTenant: SD_Orders (SD_Order_In, SD_Order_Legacy) and FI_Billing (FI_Post).
func scopeTenant(t *testing.T) *cpitest.Tenant {
	t.Helper()
	started := func() *cpitest.Runtime { return &cpitest.Runtime{Version: "1", Status: "STARTED"} }
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"SD_Order_In": {Type: "Integration", DesignVersion: "2", Package: "SD_Orders", Runtime: started(),
			AfterDeploy: []*cpitest.Runtime{{Version: "2", Status: "STARTED"}}},
		"SD_Order_Legacy": {Type: "Integration", DesignVersion: "1", Package: "SD_Orders", Runtime: started()},
		"FI_Post":         {Type: "Integration", DesignVersion: "1", Package: "FI_Billing", Runtime: started()},
	})
	mock.Packages = []cpitest.Package{{ID: "SD_Orders", Name: "SD", ShortText: "SD"}, {ID: "FI_Billing", Name: "FI", ShortText: "FI"}}
	mock.MessageLogSteps = [][]cpitest.MessageLog{{
		{Guid: "M_SD", Artifact: "SD_Order_In", Package: "SD_Orders", Status: "COMPLETED"},
		{Guid: "M_FI", Artifact: "FI_Post", Package: "FI_Billing", Status: "COMPLETED"},
	}}
	t.Cleanup(func() { httpclnt.SetGuard(nil) })
	return mock
}

// runScoped runs a command against mock with the JSON output and returns its
// result document and exit code.
func runScoped(t *testing.T, mock *cpitest.Tenant, args ...string) (map[string]any, int) {
	t.Helper()
	res := runMain(t, append(append(args, "--output", "json"), basicAuth(mock)...)...)
	var doc map[string]any
	_ = json.Unmarshal([]byte(res.stdout), &doc)
	if doc == nil {
		doc = map[string]any{"stderr": res.stderr}
	}
	return doc, res.code
}

var sdScope = []string{"--scope-packages", "SD_*", "--scope-artifacts", "!*_Legacy", "--scope-actions", "read,deploy"}

func TestScopeFiltersLists(t *testing.T) {
	mock := scopeTenant(t)
	doc, code := runScoped(t, mock, append([]string{"packages"}, sdScope...)...)
	require.Equal(t, exitcode.OK, code, doc)
	text := fmt.Sprint(doc["result"])
	assert.Contains(t, text, "SD_Orders")
	assert.NotContains(t, text, "FI_Billing")

	doc, code = runScoped(t, mock, append([]string{"artifacts", "--package-id", "SD_Orders"}, sdScope...)...)
	require.Equal(t, exitcode.OK, code, doc)
	text = fmt.Sprint(doc["result"])
	assert.Contains(t, text, "SD_Order_In")
	assert.NotContains(t, text, "SD_Order_Legacy", "excluded by the artifact pattern")

	doc, code = runScoped(t, mock, append([]string{"logs"}, sdScope...)...)
	require.Equal(t, exitcode.OK, code, doc)
	text = fmt.Sprint(doc["result"])
	assert.Contains(t, text, "M_SD")
	assert.NotContains(t, text, "M_FI")
}

func TestScopeRefusesWritesBeforeAnyTenantCall(t *testing.T) {
	mock := scopeTenant(t)
	doc, code := runScoped(t, mock, append([]string{"deploy", "--artifact-ids", "FI_Post", "--delay-length", "0", "--max-check-limit", "2"}, sdScope...)...)
	assert.Equal(t, exitcode.Scope, code, doc)
	assert.Equal(t, "scope", doc["errorCategory"])
	assert.Contains(t, fmt.Sprint(doc), "FI_Post")
	assert.NotContains(t, fmt.Sprint(doc), "FI_Billing", "the package of an out-of-scope artifact is not revealed")
	assert.Zero(t, mock.Count("POST /api/v1/DeployIntegrationDesigntimeArtifact"))

	// in scope: deploys
	_, code = runScoped(t, mock, append([]string{"deploy", "--artifact-ids", "SD_Order_In", "--delay-length", "0", "--max-check-limit", "2"}, sdScope...)...)
	assert.Equal(t, exitcode.OK, code)
	assert.Equal(t, 1, mock.Count("POST /api/v1/DeployIntegrationDesigntimeArtifact"))

	// an action the scope lacks
	doc, code = runScoped(t, mock, "undeploy", "--artifact-ids", "SD_Order_In", "--delay-length", "0", "--max-check-limit", "2", "--scope-packages", "SD_*", "--scope-actions", "read")
	assert.Equal(t, exitcode.Scope, code, doc)
	assert.Contains(t, fmt.Sprint(doc["error"]), "deploy of artifact SD_Order_In")
}

func TestScopeTenantAreasNeedAGrant(t *testing.T) {
	mock := scopeTenant(t)
	doc, code := runScoped(t, mock, append([]string{"keystore", "list"}, sdScope...)...)
	assert.Equal(t, exitcode.Scope, code, doc)
	assert.Contains(t, fmt.Sprint(doc["error"]), "tenant-wide keystore")
}

func TestScopeFileAndFlagsAreExclusive(t *testing.T) {
	mock := scopeTenant(t)
	p := filepath.Join(t.TempDir(), "s.json")
	require.NoError(t, os.WriteFile(p, []byte(`{"version":1,"packages":["SD_*"],"actions":["read"]}`), 0o600))
	_, code := runScoped(t, mock, "packages", "--scope-file", p, "--scope-actions", "read")
	assert.Equal(t, exitcode.Usage, code)
	doc, code := runScoped(t, mock, "packages", "--scope-file", p)
	assert.Equal(t, exitcode.OK, code, doc)
	assert.False(t, strings.Contains(fmt.Sprint(doc["result"]), "FI_Billing"))
}

// configure writes parameters through a $batch: under a scope every
// operation of the batch is checked, and an in-scope batch goes through.
func TestScopeConfigureBatch(t *testing.T) {
	mock := configureMock(t)
	mock.Artifacts["A"].Package = "Orders"
	mock.Packages = []cpitest.Package{{ID: "Orders", Name: "Orders", ShortText: "O"}}
	t.Cleanup(func() { httpclnt.SetGuard(nil) })
	file := writeConfigureFile(t, "          - {key: Host, value: new}\n")
	args := append([]string{"configure", "--config-path", file, "--deploy-delay", "1", "--output", "json"}, basicAuth(mock)...)

	r := runMain(t, append(args, "--scope-packages", "Orders", "--scope-actions", "read,configure,deploy")...)
	require.Equal(t, 0, r.code, r.stdout+r.stderr)
	assert.Equal(t, 1, mock.Count("POST /api/v1/$batch"))

	file = writeConfigureFile(t, "          - {key: Host, value: newer}\n")
	r = runMain(t, "configure", "--config-path", file, "--deploy-delay", "1", "--output", "json", "--tmn-host", basicAuth(mock)[1],
		"--tmn-userid", "user", "--tmn-password", "secret", "--scope-packages", "Other", "--scope-actions", "read,configure,deploy")
	assert.Equal(t, exitcode.Scope, r.code, r.stdout)
	assert.Equal(t, 1, mock.Count("POST /api/v1/$batch"), "no batch sent out of scope")
}
