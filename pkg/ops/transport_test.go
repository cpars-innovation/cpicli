package ops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// transportRepo: Orders uses the script collection Shared_Scripts, the
// message mapping OrderMapping, the credential SFTP_Orders, the PD
// parameter SAP_SYSTEM_001:Mapping, and calls Billing via ProcessDirect.
func transportRepo(t *testing.T) string {
	root := t.TempDir()
	orders := testIFlowFiles("Orders")
	model := strings.Replace(testIFlow, "<ifl:property><key>script</key><value>logPayload.groovy</value></ifl:property>",
		"<ifl:property><key>script</key><value>logPayload.groovy</value></ifl:property><ifl:property><key>scriptBundleId</key><value>Shared_Scripts</value></ifl:property>", 1)
	model = strings.Replace(model, "<ifl:property><key>headerTable</key>",
		"<ifl:property><key>mappingpath</key><value>p://Pkg/OrderMapping</value></ifl:property><ifl:property><key>headerTable</key>", 1)
	orders["src/main/resources/scenarioflows/integrationflow/Orders.iflw"] = model
	orders["src/main/resources/script/pd.groovy"] = `def m = service.getParameter("Mapping", "SAP_SYSTEM_001", String.class)`
	orders["src/main/resources/parameters.propdef"] = `<parameters><parameter><name>Receiver Host</name></parameter><parameter><name>Directory</name></parameter></parameters>`
	writeTree(t, root, "Pkg", "Orders", orders)

	billing := testIFlowFiles("Billing")
	billingModel := strings.Replace(testIFlow, "<key>urlPath</key><value>/orders/in</value>", "<key>address</key><value>/billing/in</value>", 1)
	billing["src/main/resources/scenarioflows/integrationflow/Billing.iflw"] = strings.Replace(strings.Replace(billingModel,
		"<ifl:property><key>ComponentType</key><value>HTTPS</value></ifl:property>",
		"<ifl:property><key>ComponentType</key><value>ProcessDirect</value></ifl:property>", 1),
		"ctype::AdapterVariant/cname::sap:HTTPS/direction::Sender", "ctype::AdapterVariant/cname::sap:ProcessDirect/direction::Sender", 1)
	writeTree(t, root, "Pkg", "Billing", billing)
	writeTree(t, root, "Pkg", "Shared_Scripts", map[string]string{
		"META-INF/MANIFEST.MF":                    "Manifest-Version: 1.0\nBundle-SymbolicName: Shared_Scripts\nBundle-Version: 1.0.0\nSAP-BundleType: ScriptCollection\n",
		"src/main/resources/script/common.groovy": "x",
	})
	writeTree(t, root, "Pkg", "OrderMapping", map[string]string{
		"META-INF/MANIFEST.MF":                  "Manifest-Version: 1.0\nBundle-SymbolicName: OrderMapping\nBundle-Version: 1.0.0\nSAP-BundleType: MessageMapping\n",
		"src/main/resources/mapping/Order.mmap": "x",
	})
	return root
}

func depOf(deps []TransportDependency, kind, id string) *TransportDependency {
	for i := range deps {
		if deps[i].Kind == kind && deps[i].ID == id {
			return &deps[i]
		}
	}
	return nil
}

func TestResolveTransport(t *testing.T) {
	root := transportRepo(t)
	set, err := ResolveTransport(context.Background(), root, []string{"Orders"}, false)
	require.NoError(t, err)
	require.Len(t, set.Artifacts, 1)
	assert.Equal(t, TransportArtifact{ID: "Orders", Type: "Integration", Package: "Pkg", Path: "Pkg/Orders", Version: "1.0.3"}, set.Artifacts[0])

	sc := depOf(set.Dependencies, DepArtifact, "Shared_Scripts")
	require.NotNil(t, sc)
	assert.Equal(t, "ScriptCollection", sc.Type)
	assert.True(t, sc.Local)
	assert.False(t, sc.InSelection)
	mm := depOf(set.Dependencies, DepArtifact, "OrderMapping")
	require.NotNil(t, mm, "%+v", set.Dependencies)
	assert.Equal(t, "MessageMapping", mm.Type)
	call := depOf(set.Dependencies, DepFlowCall, "Billing")
	require.NotNil(t, call, "%+v", set.Dependencies)
	assert.Contains(t, call.Reason, "/billing/in")
	assert.NotNil(t, depOf(set.Dependencies, DepCredential, "SFTP_Orders"))
	assert.NotNil(t, depOf(set.Dependencies, DepPD, "SAP_SYSTEM_001:Mapping"))

	set, err = ResolveTransport(context.Background(), root, []string{"Orders"}, true)
	require.NoError(t, err)
	var ids []string
	for _, a := range set.Artifacts {
		ids = append(ids, a.ID)
		assert.Equal(t, a.ID != "Orders", a.AddedAsDependency, a.ID)
	}
	assert.Equal(t, []string{"OrderMapping", "Orders", "Shared_Scripts"}, ids, "referenced artifacts added, called flows not")
	assert.True(t, depOf(set.Dependencies, DepArtifact, "Shared_Scripts").InSelection)

	_, err = ResolveTransport(context.Background(), root, []string{"Nope"}, false)
	assert.Error(t, err)
}

func TestCheckTransport(t *testing.T) {
	root := transportRepo(t)
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"Orders":       {Type: "Integration", DesignVersion: "Active", Package: "Pkg", Name: "Orders"}, // draft on the target
		"OrderMapping": {Type: "MessageMapping", DesignVersion: "1.0.0", Package: "Other", Name: "OrderMapping"},
		"Billing":      {Type: "Integration", DesignVersion: "1.0.0", Package: "Pkg", Name: "Billing", Runtime: &cpitest.Runtime{Version: "1.0.0", Status: "STARTED"}},
	})
	mock.Packages = []cpitest.Package{{ID: "Pkg", Version: "1.0.0"}, {ID: "Other", Version: "1.0.0"}}
	mock.Credentials = map[string]map[string]map[string]any{"UserCredentials": {"Other_Cred": {"Name": "Other_Cred"}}}
	mock.SecureParametersUnavailable = true
	mock.PDStrings = map[string]string{"SAP_SYSTEM_001/Mapping": "x"}

	cfg := &models.ConfigureConfig{Packages: []models.ConfigurePackage{{ID: "Pkg", Artifacts: []models.ConfigureArtifact{
		{ID: "Orders", Parameters: []models.ConfigurationParameter{{Key: "Directory", Value: "/prod/in"}}}}}}}
	res, err := CheckTransport(context.Background(), mock.Executer(), root, []string{"Orders"}, TransportCheckOptions{Configure: cfg})
	require.NoError(t, err)
	byCheck := func(check, contains string) TransportCheck {
		for _, c := range res.Checks {
			if c.Check == check && strings.Contains(c.Message, contains) {
				return c
			}
		}
		t.Fatalf("no %s check with %q in %+v", check, contains, res.Checks)
		return TransportCheck{}
	}
	assert.Equal(t, CheckFail, byCheck("draft", "draft").Status)
	assert.Equal(t, CheckFail, byCheck("dependency", "Shared_Scripts").Status, "neither on the target nor in the transport")
	assert.Equal(t, CheckPass, byCheck("dependency", "OrderMapping").Status, "found in another package")
	assert.Equal(t, CheckPass, byCheck("flow_call", "Billing").Status)
	assert.Equal(t, CheckFail, byCheck("credential", "SFTP_Orders").Status)
	assert.Equal(t, CheckPass, byCheck("pd", "SAP_SYSTEM_001:Mapping").Status)
	p := byCheck("parameters", "Receiver Host")
	assert.Equal(t, CheckWarn, p.Status, "the dev value would travel")
	assert.NotContains(t, p.Message, "Directory")
	assert.Equal(t, 0, mock.Artifacts["Orders"].Uploads, "read only")

	// with the dependencies the script collection travels too
	res, err = CheckTransport(context.Background(), mock.Executer(), root, []string{"Orders"}, TransportCheckOptions{WithDeps: true})
	require.NoError(t, err)
	assert.Equal(t, CheckPass, byCheckIn(t, res, "dependency", "Shared_Scripts").Status)
	assert.Equal(t, CheckWarn, byCheckIn(t, res, "parameters", "source tier's value travels").Status, "nothing configured on the target")
}

func byCheckIn(t *testing.T, res *TransportCheckResult, check, contains string) TransportCheck {
	t.Helper()
	for _, c := range res.Checks {
		if c.Check == check && strings.Contains(c.Message, contains) {
			return c
		}
	}
	t.Fatalf("no %s check with %q", check, contains)
	return TransportCheck{}
}

func TestCopyArtifacts(t *testing.T) {
	from := transportRepo(t)
	to := t.TempDir()
	writeTree(t, to, "Pkg", "Orders", map[string]string{"stale.txt": "gone after the copy"})
	res, err := CopyArtifacts(context.Background(), from, to, []string{"Orders"}, true, true)
	require.NoError(t, err)
	assert.True(t, res.DryRun)
	assert.FileExists(t, filepath.Join(to, "Pkg", "Orders", "stale.txt"), "dry run")

	res, err = CopyArtifacts(context.Background(), from, to, []string{"Orders"}, true, false)
	require.NoError(t, err)
	actions := map[string]string{}
	for _, a := range res.Artifacts {
		actions[a.ID] = a.Action
	}
	assert.Equal(t, map[string]string{"Orders": "updated", "Shared_Scripts": "created", "OrderMapping": "created"}, actions)
	assert.NoFileExists(t, filepath.Join(to, "Pkg", "Orders", "stale.txt"), "the folder is replaced exactly")
	_, err = os.Stat(filepath.Join(to, "Pkg", "Billing"))
	assert.True(t, os.IsNotExist(err), "called flows are not copied")

	res, err = CopyArtifacts(context.Background(), from, to, []string{"Orders"}, false, false)
	require.NoError(t, err)
	assert.Equal(t, "unchanged", res.Artifacts[0].Action)
}

// Readiness of the target: configuration compared with the source tier,
// credentials named by parameters, expiring keys, accepted gaps.
func TestCheckTransportReadiness(t *testing.T) {
	root := t.TempDir()
	files := testIFlowFiles("Orders")
	model := strings.Replace(testIFlow, "<key>credentialName</key><value>SFTP_Orders</value>", "<key>credentialName</key><value>{{SFTP Credential}}</value>", 1)
	model = strings.Replace(model, "<ifl:property><key>ComponentType</key><value>SFTP</value></ifl:property>",
		"<ifl:property><key>ComponentType</key><value>SFTP</value></ifl:property><ifl:property><key>privateKeyAlias</key><value>orders_key</value></ifl:property>", 1)
	files["src/main/resources/scenarioflows/integrationflow/Orders.iflw"] = model
	files["src/main/resources/parameters.prop"] = "Receiver\\ Host=sftp.dev.example.com\nDirectory=/in\nSFTP\\ Credential=SFTP_Dev\n"
	files["src/main/resources/parameters.propdef"] = `<parameters><parameter><name>Receiver Host</name></parameter><parameter><name>Directory</name></parameter><parameter><name>SFTP Credential</name></parameter></parameters>`
	writeTree(t, root, "Pkg", "Orders", files)

	dev := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"Orders": {Type: "Integration", DesignVersion: "1.0.3", Package: "Pkg", Name: "Orders",
			Parameters: map[string]string{"Receiver Host": "sftp.dev.example.com", "Directory": "/in", "SFTP Credential": "SFTP_Dev"}},
	})
	dev.Packages = []cpitest.Package{{ID: "Pkg", Version: "1.0.0"}}
	prod := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"Orders": {Type: "Integration", DesignVersion: "1.0.2", Package: "Pkg", Name: "Orders",
			Parameters: map[string]string{"Receiver Host": "sftp.dev.example.com", "SFTP Credential": "SFTP_Prod"}},
	})
	prod.Packages = []cpitest.Package{{ID: "Pkg", Version: "1.0.0"}}
	prod.SecureParametersUnavailable = true
	prod.Credentials = map[string]map[string]map[string]any{"UserCredentials": {"SFTP_Prod": {"Name": "SFTP_Prod"}}}
	prod.Keystore = []cpitest.KeystoreEntry{{Alias: "orders_key", NotAfter: time.Now().AddDate(0, 0, 10)}}

	res, err := CheckTransport(context.Background(), prod.Executer(), root, []string{"Orders"}, TransportCheckOptions{SourceExe: dev.Executer()})
	require.NoError(t, err)
	assert.Equal(t, CheckWarn, byCheckIn(t, res, "parameters", "looks environment-specific").Status)
	assert.Contains(t, byCheckIn(t, res, "parameters", "looks environment-specific").Message, "Receiver Host")
	assert.Contains(t, byCheckIn(t, res, "parameters", "travels").Message, "Directory")
	assert.Equal(t, CheckPass, byCheckIn(t, res, "credential", "parameter SFTP Credential = SFTP_Prod").Status, "resolved with the target's value")
	assert.Equal(t, CheckWarn, byCheckIn(t, res, "keystore", "orders_key expires").Status)
	assert.Zero(t, res.Summary[CheckFail])

	// the credential is not on the target: a failure, unless accepted
	prod.Credentials = map[string]map[string]map[string]any{"UserCredentials": {}}
	res, err = CheckTransport(context.Background(), prod.Executer(), root, []string{"Orders"}, TransportCheckOptions{SourceExe: dev.Executer()})
	require.NoError(t, err)
	assert.Equal(t, CheckFail, byCheckIn(t, res, "credential", "SFTP_Prod").Status)
	res, err = CheckTransport(context.Background(), prod.Executer(), root, []string{"Orders"},
		TransportCheckOptions{SourceExe: dev.Executer(), AllowMissing: []string{"credential"}})
	require.NoError(t, err)
	c := byCheckIn(t, res, "credential", "SFTP_Prod")
	assert.Equal(t, CheckWarn, c.Status)
	assert.Contains(t, c.Message, "provided separately")
	assert.Zero(t, res.Summary[CheckFail])

	_, err = CheckTransport(context.Background(), prod.Executer(), root, []string{"Orders"}, TransportCheckOptions{AllowMissing: []string{"everything"}})
	assert.Error(t, err)
}
