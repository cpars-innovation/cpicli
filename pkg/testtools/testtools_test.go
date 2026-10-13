package testtools_test

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/iflow"
	"github.com/cpars-innovation/cpicli/pkg/testtools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArtifacts(t *testing.T) {
	arts := testtools.Artifacts()
	require.Len(t, arts, 2)
	assert.Equal(t, testtools.Artifact{ID: testtools.HarnessID, Tool: testtools.ToolHarness, Name: "cpictl test harness", Version: "1.1.0", RoleParameter: testtools.HarnessRoleParameter}, arts[0])
	assert.Equal(t, testtools.Artifact{ID: testtools.StubID, Tool: testtools.ToolStub, Name: "cpictl receiver stub", Version: "1.2.0", RoleParameter: testtools.StubRoleParameter}, arts[1])

	only, err := testtools.Select("stub")
	require.NoError(t, err)
	assert.Equal(t, arts[1:], only)
	_, err = testtools.Select("both")
	assert.Error(t, err)
}

// The embedded artifacts are archives with a manifest whose symbolic name is
// the ID, and they pass the mock tenant's Check (component versions of an
// integrationcell tenant, required channel attributes, scripts).
func TestEmbeddedArtifactsAreValid(t *testing.T) {
	m := cpitest.NewTenant(t, map[string]*cpitest.Artifact{})
	m.Live = true
	m.Packages = []cpitest.Package{{ID: testtools.PackageID, Name: testtools.PackageName, ShortText: testtools.PackageShortText}}
	for _, a := range testtools.Artifacts() {
		data, err := testtools.Zip(a.ID)
		require.NoError(t, err)
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		require.NoError(t, err)
		files := map[string]string{}
		for _, f := range zr.File {
			rc, err := f.Open()
			require.NoError(t, err)
			b, _ := io.ReadAll(rc)
			_ = rc.Close()
			files[f.Name] = string(b)
		}
		require.Contains(t, files, "META-INF/MANIFEST.MF", a.ID)
		assert.Contains(t, files["META-INF/MANIFEST.MF"], "Bundle-SymbolicName: "+a.ID+"; singleton:=true")
		model := files["src/main/resources/scenarioflows/integrationflow/"+a.ID+".iflw"]
		require.NotEmpty(t, model, a.ID)
		parsed, err := iflow.Parse([]byte(model))
		require.NoError(t, err, a.ID)
		assert.NotEmpty(t, parsed.Adapters, a.ID)
		// the sender's role is externalised, with the default value and a
		// propdef entry (the Web UI's Configure dialog)
		assert.Contains(t, model, "<key>userRole</key><value>{{"+a.RoleParameter+"}}</value>", a.ID)
		assert.Contains(t, files["src/main/resources/parameters.prop"], a.RoleParameter+"="+testtools.DefaultRole, a.ID)
		assert.Contains(t, files["src/main/resources/parameters.propdef"], "<name>"+a.RoleParameter+"</name>", a.ID)
		assert.Contains(t, files["src/main/resources/parameters.propdef"], `param_key="`+a.RoleParameter+`"`, a.ID)

		dir := t.TempDir()
		require.NoError(t, testtools.WriteDir(a.ID, dir))
		require.NoError(t, cpi.NewIntegration(m.Executer()).Create(a.ID, a.Name, testtools.PackageID, dir), a.ID)
		text, err := cpi.NewContent(m.Executer()).Validate(a.ID, "active")
		require.NoError(t, err)
		assert.Equal(t, "Check execution result: Passed", text, a.ID)
	}
}

func TestWriteDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, testtools.WriteDir(testtools.StubID, dir))
	script, err := os.ReadFile(filepath.Join(dir, "src/main/resources/script/stub.groovy"))
	require.NoError(t, err)
	for _, want := range []string{testtools.StubAttachment, testtools.StubPID, testtools.StubRuleHeader, testtools.StubPathHeader,
		"com.sap.gateway.ip.core.customdev.util.Message", "PartnerDirectoryService", "CamelHttpResponseCode"} {
		assert.True(t, strings.Contains(string(script), want), want)
	}
	assert.Error(t, testtools.WriteDir("Nope", t.TempDir()))
}
