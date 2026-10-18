package scripttest

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/cpars-innovation/cpicli/pkg/testrun"
	"github.com/cpars-innovation/cpicli/pkg/testtools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func str(s string) *string { return &s }

// The fixtures of the local runner run online: one flow per script and
// function, deployed, called, deleted. The mock plays the scripts.
func TestRunCasesOnline(t *testing.T) {
	m := cpitest.NewTenant(t, map[string]*cpitest.Artifact{})
	m.Live = true
	newExe := func(endpoint string) (*httpclnt.HTTPExecuter, string, error) {
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, "", err
		}
		return m.Executer().ForEndpoint(u.EscapedPath()), u.EscapedPath(), nil
	}
	var calls []cpitest.ScriptRun
	m.ScriptRunner = func(r cpitest.ScriptRun) cpitest.ScriptRunResult {
		calls = append(calls, r)
		no := strings.TrimSpace(r.Headers["OrderNo"])
		if no == "" {
			return cpitest.ScriptRunResult{Error: "java.lang.IllegalStateException: OrderNo missing@ line 9 in Headers.groovy"}
		}
		return cpitest.ScriptRunResult{Body: "<Order><No>" + no + "</No></Order>",
			Headers: map[string]*string{"OrderNo": str(no)}, Properties: map[string]*string{"x": str(no)}}
	}
	project := filepath.Join("testdata", "cases")
	cases, err := testrun.LoadScriptCases(filepath.Join(project, ".cpi", "tests"), testrun.Filter{})
	require.NoError(t, err)

	opts := OnlineOptions{Content: filepath.Join(project, "packages"), Exe: m.Executer(), NewExe: newExe,
		Deploy: ops.Options{Interval: time.Millisecond, MaxChecks: 20}}
	res, err := RunCasesOnline(context.Background(), cases, opts)
	require.NoError(t, err)
	status := map[string]string{}
	for _, c := range res.Cases {
		status[c.Flow+"/"+c.Case] = string(c.Status) + " " + c.SkipReason + c.Error
	}
	assert.Equal(t, map[string]string{
		"script:Orders_In/Headers.groovy/missing":   "passed ",
		"script:Orders_In/Headers.groovy/trims":     "skipped uses pd values: local only",
		"script:Orders_In/Headers.groovy/wrong":     "failed ",
		"script:Orders_In/udf.groovy/format-date":   "skipped call.args: runs locally only (a script step passes the message)",
		"script:Orders_In/udf.groovy/upper-all":     "skipped call.args: runs locally only (a script step passes the message)",
		"collection:Orders_Scripts/pads":            "skipped call.args: runs locally only (a script step passes the message)",
		"collection:Orders_Scripts/v2-no-getheader": "failed ",
	}, status, "the runtime accepts getHeader in version 2 (here: the mock's script)")
	assert.Equal(t, 1, res.Passed)
	assert.Equal(t, 2, res.Failed)
	assert.Equal(t, 4, res.Skipped)
	require.NotEmpty(t, calls)
	assert.Equal(t, " 4711 ", calls[len(calls)-1].Headers["OrderNo"], "the case's headers reach the script with their names")

	// nothing is left on the tenant
	for id, a := range m.Artifacts {
		if strings.HasPrefix(id, testtools.ScriptIDPrefix) {
			assert.Empty(t, a.DesignVersion, "%s deleted", id)
		}
	}

	// --keep leaves the flows; a second run replaces them
	opts.Keep = true
	_, err = RunCasesOnline(context.Background(), cases, opts)
	require.NoError(t, err)
	_, err = RunCasesOnline(context.Background(), cases, opts)
	require.NoError(t, err)
	kept := 0
	for id, a := range m.Artifacts {
		if strings.HasPrefix(id, testtools.ScriptIDPrefix) && a.DesignVersion != "" {
			kept++
		}
	}
	assert.Equal(t, 2, kept, "Headers.groovy/processData and Format.groovy/wrongApi")

	opts.Tier = "prod"
	_, err = RunCasesOnline(context.Background(), cases, opts)
	assert.ErrorContains(t, err, "prod")
}

func TestThrownBy(t *testing.T) {
	class, msg := thrownBy("com.sap.it.rt.adapter.http.api.exception.HttpResponseException: An internal server error occured: java.lang.IllegalArgumentException: OrderNo missing@ line 4 in Keys.groovy.\nThe MPL ID for the failed message is : X\n")
	assert.Equal(t, "java.lang.IllegalArgumentException", class)
	assert.Equal(t, "OrderNo missing", msg)
	class, msg = thrownBy("javax.script.ScriptException: groovy.lang.MissingPropertyException: No such property: x for class: Script1")
	assert.Equal(t, "groovy.lang.MissingPropertyException", class)
	assert.Equal(t, "No such property: x for class: Script1", msg)
	class, msg = thrownBy("something odd")
	assert.Empty(t, class)
	assert.Equal(t, "something odd", msg)
}

func TestScriptFlowFiles(t *testing.T) {
	dir := t.TempDir()
	f := testtools.ScriptFlow{Owner: "Orders/In", Script: "v2/Keys.groovy", Function: "processData", Language: testtools.GroovyV2,
		Files: map[string][]byte{"v2/Keys.groovy": []byte("package script.v2"), "../lib/x.jar": []byte("jar")}, Version: "1.0.7"}
	require.NoError(t, f.WriteDir(dir))
	iflw, err := os.ReadFile(filepath.Join(dir, "src", "main", "resources", "scenarioflows", "integrationflow", f.ID()+".iflw"))
	require.NoError(t, err)
	assert.Contains(t, string(iflw), "<key>script</key><value>v2/Keys.groovy</value>")
	assert.Contains(t, string(iflw), "GroovyScript/version::2.0.0")
	assert.Contains(t, string(iflw), "<key>urlPath</key><value>"+f.Path()+"</value>")
	mf, err := os.ReadFile(filepath.Join(dir, "META-INF", "MANIFEST.MF"))
	require.NoError(t, err)
	assert.Contains(t, string(mf), "Bundle-Version: 1.0.7")
	assert.Contains(t, string(mf), "Bundle-Name: cpictl script test Orders_In v2_Keys.groovy processData", "names without '/'")
	assert.FileExists(t, filepath.Join(dir, "src", "main", "resources", "lib", "x.jar"))
	assert.FileExists(t, filepath.Join(dir, "src", "main", "resources", "script", "cpictl_prepare.groovy"))

	bad := f
	bad.Function = "x(); evil"
	assert.Error(t, bad.WriteDir(t.TempDir()))
	bad = f
	bad.Files = map[string][]byte{"../../META-INF/MANIFEST.MF": nil}
	assert.Error(t, bad.WriteDir(t.TempDir()))
	bad = f
	bad.Files = map[string][]byte{"cpictl_prepare.groovy": nil}
	assert.Error(t, bad.WriteDir(t.TempDir()))
}
