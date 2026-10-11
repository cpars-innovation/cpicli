package ops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/iflow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// flowFiles are the files of a flow as snapshot writes them.
func cmpFlowFiles(id, version, script, params string) map[string]string {
	return map[string]string{
		"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\nBundle-SymbolicName: " + id + "; singleton:=true\nBundle-Name: " + id + "\nBundle-Version: " + version + "\nSAP-BundleType: IntegrationFlow\n",
		"src/main/resources/scenarioflows/integrationflow/" + id + ".iflw": "<bpmn2:definitions/>\n",
		"src/main/resources/script/s.groovy":                               script,
		"src/main/resources/parameters.prop":                               params,
	}
}

func writeTree(t *testing.T, root, pkg, id string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(root, pkg, id, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
}

func itemOf(res *CompareResult, id string) CompareItem {
	for _, it := range res.Items {
		if it.Artifact == id {
			return it
		}
	}
	return CompareItem{}
}

func TestCompareTrees(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	writeTree(t, a, "P", "Same", cmpFlowFiles("Same", "1.0.0", "v1\n", "Host=h\n"))
	writeTree(t, b, "P", "Same", cmpFlowFiles("Same", "1.0.0", "v1\n", "Host=h\n"))
	writeTree(t, a, "P", "Content", cmpFlowFiles("Content", "1.0.1", "line1\nline2\n", "Host=h\n"))
	writeTree(t, b, "P", "Content", cmpFlowFiles("Content", "1.0.2", "line1\nchanged\n", "Host=h\n"))
	writeTree(t, a, "P", "Version", cmpFlowFiles("Version", "1.0.1", "v1\n", "Host=h\n"))
	writeTree(t, b, "P", "Version", cmpFlowFiles("Version", "1.0.2", "v1\n", "Host=h\n"))
	writeTree(t, a, "P", "Params", cmpFlowFiles("Params", "1.0.0", "v1\n", "Host=dev\nUser=u\n"))
	writeTree(t, b, "P", "Params", cmpFlowFiles("Params", "1.0.0", "v1\n", "Host=prod\nPort=443\n"))
	writeTree(t, a, "P", "OnlyA", cmpFlowFiles("OnlyA", "1.0.0", "v1\n", ""))
	writeTree(t, b, "Other", "OnlyB", cmpFlowFiles("OnlyB", "1.0.0", "v1\n", ""))
	// a file only in B
	writeTree(t, b, "P", "Content", map[string]string{"src/main/resources/script/new.groovy": "x\n"})

	res, err := Compare(context.Background(), CompareSide{Label: "dev", Dir: a}, CompareSide{Label: "prod", Dir: b}, CompareOptions{Diff: true})
	require.NoError(t, err)
	assert.Equal(t, map[string]int{CompareSame: 1, CompareContentDiffers: 1, CompareVersionDiffers: 1, CompareParametersDiffer: 1,
		CompareOnlyA: 1, CompareOnlyB: 1}, res.Summary)

	c := itemOf(res, "Content")
	assert.Equal(t, CompareContentDiffers, c.Status)
	assert.Equal(t, []string{"content", "version"}, c.Differences, "every differing aspect, not only the status")
	assert.Equal(t, []string{"parameters"}, itemOf(res, "Params").Differences)
	assert.Empty(t, itemOf(res, "Same").Differences)
	assert.Equal(t, "1.0.1", c.VersionA)
	assert.Equal(t, "1.0.2", c.VersionB)
	require.Len(t, c.Files, 3)
	assert.Equal(t, FileChange{Path: "META-INF/MANIFEST.MF", Change: "changed", Diff: c.Files[0].Diff}, c.Files[0])
	assert.Equal(t, "src/main/resources/script/new.groovy", c.Files[1].Path)
	assert.Equal(t, "added", c.Files[1].Change)
	assert.Contains(t, c.Files[2].Diff, "-line2\n+changed\n")
	assert.Contains(t, c.Files[2].Diff, "--- dev/src/main/resources/script/s.groovy")

	p := itemOf(res, "Params")
	assert.Equal(t, []ParameterChange{{Key: "Host", Change: "differs"}, {Key: "Port", Change: "only_b"}, {Key: "User", Change: "only_a"}}, p.Parameters, "keys only by default")
	assert.Equal(t, "Other", itemOf(res, "OnlyB").Package)

	// filters and values
	res, err = Compare(context.Background(), CompareSide{Label: "dev", Dir: a}, CompareSide{Label: "prod", Dir: b},
		CompareOptions{Artifacts: []string{"Params"}, Values: true})
	require.NoError(t, err)
	require.Len(t, res.Items, 1)
	assert.Equal(t, ParameterChange{Key: "Host", Change: "differs", A: "dev", B: "prod"}, res.Items[0].Parameters[0])
	assert.Empty(t, res.Items[0].Files, "parameters.prop is compared by key, not as a file")
}

func TestCompareTenant(t *testing.T) {
	local := t.TempDir()
	// the repository has what snapshot wrote: LF, normalized parameters.prop
	writeTree(t, local, "P", "Flow", cmpFlowFiles("Flow", "1.0.3", "v1\n", "Host=h\n"))
	writeTree(t, local, "P", "Edited", cmpFlowFiles("Edited", "1.0.3", "v1\n", "Host=h\n"))
	tenantZip := func(id, script string) []byte {
		files := cmpFlowFiles(id, "1.0.3", script, "#Fri Oct 09 09:34:25 UTC 2026\r\nHost=h\r\n")
		files["META-INF/MANIFEST.MF"] = "Manifest-Version: 1.0\r\nBundle-SymbolicName: " + id + "; singleton:=true\r\nBundle-Name: " + id + "\r\nBundle-Version: 1.0.3\r\nSAP-BundleType: IntegrationFlow\r\n"
		return zipOf(t, files)
	}
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"Flow":   {Type: "Integration", DesignVersion: "1.0.3", Package: "P", Name: "Flow", Zip: tenantZip("Flow", "v1\r\n"), Runtime: &cpitest.Runtime{Version: "1.0.3", Status: "STARTED"}},
		"Edited": {Type: "Integration", DesignVersion: "1.0.3", Package: "P", Name: "Edited", Zip: tenantZip("Edited", "edited in the Web UI\r\n"), ModifiedBy: "jane.doe@example.com"},
		"Else":   {Type: "Integration", DesignVersion: "1.0.0", Package: "Unrelated", Name: "Else", Zip: tenantZip("Else", "x")},
	})
	mock.Packages = []cpitest.Package{{ID: "P", Version: "1.0.0"}, {ID: "Unrelated", Version: "1.0.0"}}

	res, err := Compare(context.Background(), CompareSide{Label: "git", Dir: local}, CompareSide{Label: "DEV", Exe: mock.Executer()},
		CompareOptions{Packages: []string{"P"}, Diff: true})
	require.NoError(t, err)
	assert.Equal(t, map[string]int{CompareSame: 1, CompareContentDiffers: 1}, res.Summary, "normalized like snapshot: CRLF and the timestamp do not count")
	f := itemOf(res, "Flow")
	require.NotNil(t, f.B)
	assert.Equal(t, CompareSideInfo{Designtime: "1.0.3", Running: "1.0.3", RuntimeStatus: "STARTED"}, *f.B)
	e := itemOf(res, "Edited")
	assert.Equal(t, "jane.doe@example.com", e.B.ModifiedBy, "who changed it on the tenant")
	require.Len(t, e.Files, 1)
	assert.Contains(t, e.Files[0].Diff, "+edited in the Web UI")
}

func TestGitTree(t *testing.T) {
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q", "-b", "main")
	writeTree(t, repo, "packages/P", "Flow", cmpFlowFiles("Flow", "1.0.0", "v1\n", ""))
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "v1")
	writeTree(t, repo, "packages/P", "Flow", cmpFlowFiles("Flow", "1.0.1", "v2\n", ""))

	old := t.TempDir()
	require.NoError(t, GitTree(context.Background(), repo, "HEAD", "packages", old))
	res, err := Compare(context.Background(), CompareSide{Label: "HEAD", Dir: old}, CompareSide{Label: "work", Dir: filepath.Join(repo, "packages")}, CompareOptions{})
	require.NoError(t, err)
	assert.Equal(t, CompareContentDiffers, itemOf(res, "Flow").Status)
	assert.Equal(t, "1.0.0", itemOf(res, "Flow").VersionA)

	err = GitTree(context.Background(), repo, "no-such-ref", "", t.TempDir())
	assert.Error(t, err)
}

func TestCompareModel(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "cpitest", "landscapes", "demo", "packages", "Orders", "Orders_Route",
		"src", "main", "resources", "scenarioflows", "integrationflow", "Orders_Route.iflw"))
	require.NoError(t, err)
	model := string(data)
	const iflw = "src/main/resources/scenarioflows/integrationflow/Flow.iflw"
	a, b := t.TempDir(), t.TempDir()
	filesA := cmpFlowFiles("Flow", "1.0.0", "v1\n", "")
	filesA[iflw] = model
	filesB := cmpFlowFiles("Flow", "1.0.0", "v2\n", "")
	filesB[iflw] = strings.Replace(model, "<value>/orders/route</value>", "<value>/orders/route/v2</value>", 1)
	writeTree(t, a, "P", "Flow", filesA)
	writeTree(t, b, "P", "Flow", filesB)
	writeTree(t, a, "P", "Broken", cmpFlowFiles("Broken", "1.0.0", "v1\n", ""))
	brokenB := cmpFlowFiles("Broken", "1.0.0", "v1\n", "")
	brokenB["src/main/resources/scenarioflows/integrationflow/Broken.iflw"] = "not xml <"
	writeTree(t, b, "P", "Broken", brokenB)

	res, err := Compare(context.Background(), CompareSide{Label: "a", Dir: a}, CompareSide{Label: "b", Dir: b}, CompareOptions{Model: true})
	require.NoError(t, err)
	f := itemOf(res, "Flow")
	require.Len(t, f.Files, 2)
	assert.Equal(t, FileChange{Path: iflw, Change: "changed", Model: &iflow.ModelDiff{Elements: []iflow.ElementChange{
		{ID: "MessageFlow_Sender", Type: "messageFlow", Kind: "ProcessDirect", Name: "ProcessDirect", Change: iflow.ChangeChanged,
			Properties: []iflow.PropertyChange{{Key: "address", Change: iflow.ChangeChanged, A: "/orders/route", B: "/orders/route/v2"}}},
	}}}, f.Files[0], "the model diff instead of a line diff")
	assert.Equal(t, FileChange{Path: "src/main/resources/script/s.groovy", Change: "changed"}, f.Files[1], "no model for other files")
	broken := itemOf(res, "Broken")
	require.Len(t, broken.Files, 1)
	assert.Nil(t, broken.Files[0].Model)
	assert.NotEmpty(t, broken.Files[0].ModelError)

	// default: no model diff
	res, err = Compare(context.Background(), CompareSide{Label: "a", Dir: a}, CompareSide{Label: "b", Dir: b}, CompareOptions{Diff: true})
	require.NoError(t, err)
	assert.Nil(t, itemOf(res, "Flow").Files[0].Model)
	assert.Contains(t, itemOf(res, "Flow").Files[0].Diff, "/orders/route/v2")
}
