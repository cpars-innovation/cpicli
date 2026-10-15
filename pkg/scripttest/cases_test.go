package scripttest

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/cpars-innovation/cpicli/pkg/testrun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// casesProject writes a project from the template plus the case fixtures.
func casesProject(t *testing.T) (string, []testrun.ScriptCase) {
	t.Helper()
	dir := t.TempDir()
	_, err := Init(dir, false)
	require.NoError(t, err)
	copyTree(t, "testdata/cases", dir)
	cases, err := testrun.LoadScriptCases(filepath.Join(dir, ".cpi", "tests"), testrun.Filter{})
	require.NoError(t, err)
	require.Len(t, cases, 7)
	return dir, cases
}

// The plan resolves each case's script (a flow script, the collection
// script defining the function), its version and the service answers; the
// runner's results become case results.
func TestRunCasesPlanAndResults(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake runner is a shell script")
	}
	dir, cases := casesProject(t)
	// a fake runner: copies the plan's case ids into canned results
	fake := `plan=""; res=""; for a in "$@"; do case "$a" in -PcpictlPlan=*) plan="${a#-PcpictlPlan=}";; -PcpictlResults=*) res="${a#-PcpictlResults=}";; esac; done
cp "$plan" "$(dirname "$res")/seen-plan.json"
cat > "$res" <<'EOF'
{"cases":[{"id":"c5","returns":"00004711","hasReturns":true,"durationMs":3},
 {"id":"c3","returns":"2026/10/15","hasReturns":true,"durationMs":1},
 {"id":"c4","returns":["A","__SUPPRESS__","B"],"hasReturns":true,"durationMs":1},
 {"id":"c6","throws":{"class":"groovy.lang.MissingMethodException","message":"No signature of method: getHeader"},"durationMs":1},
 {"id":"c0","throws":{"class":"java.lang.IllegalStateException","message":"OrderNo missing"},"durationMs":2},
 {"id":"c1","body":"<Order><No>4711</No></Order>","headers":{"OrderNo":"4711","Temp":null},"properties":{"PID":"PID1","Target":"ERP-DEV"},"log":{"customHeaders":{"OrderNo":["4711"]},"properties":{}},"durationMs":5},
 {"id":"c2","body":null,"headers":{"OrderNo":"4711"},"properties":{},"durationMs":1}]}
EOF`
	res, err := RunCases(context.Background(), cases, CaseOptions{Project: dir, Command: []string{"sh", "-c", fake, "sh"}})
	require.NoError(t, err)
	assert.Equal(t, 6, res.Passed, "%+v", res.Cases)
	assert.Equal(t, 1, res.Failed)
	byName := map[string]testrun.CaseResult{}
	for _, c := range res.Cases {
		byName[c.Case] = c
	}
	assert.Equal(t, testrun.StatusFailed, byName["wrong"].Status)
	assert.Equal(t, "script:Orders_In/Headers.groovy", byName["trims"].Flow)
	assert.Equal(t, "collection:Orders_Scripts", byName["pads"].Flow)

	data, err := os.ReadFile(filepath.Join(dir, "build", "cpictl", "seen-plan.json"))
	require.NoError(t, err)
	var plan struct{ Cases []planCase }
	require.NoError(t, json.Unmarshal(data, &plan))
	byFn := map[string]planCase{}
	for _, p := range plan.Cases {
		byFn[p.ID] = p
	}
	pads := byFn["c5"]
	assert.Equal(t, "formatNo", pads.Function)
	assert.True(t, pads.HasArgs)
	assert.Equal(t, 2, pads.Version, "package script.v2")
	assert.Equal(t, "Format.groovy", filepath.Base(pads.Script))
	trims := byFn["c1"]
	assert.Equal(t, 1, trims.Version)
	assert.Equal(t, "ERP-DEV", trims.PD["PID1/Target"])
	assert.False(t, trims.HasArgs)
	assert.True(t, byFn["c3"].HasArgs, "a flow script's mapping function takes arguments")
	assert.Equal(t, "udf.groovy", filepath.Base(byFn["c3"].Script))
}

func TestRunCasesRefusesAnOldBuild(t *testing.T) {
	dir, cases := casesProject(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "build.gradle"), []byte("plugins { id 'groovy' }\n"), 0o644))
	_, err := RunCases(context.Background(), cases, CaseOptions{Project: dir})
	assert.ErrorContains(t, err, "cpictl test scripts init")
}

func TestScriptDefining(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.groovy"), []byte("def formatNo(String no) {\n}\nString other(x) { }\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.groovy"), []byte("static String other(String y) throws Exception {\n}\n"), 0o644))
	p, err := scriptDefining(dir, "formatNo")
	require.NoError(t, err)
	assert.Equal(t, "a.groovy", filepath.Base(p))
	_, err = scriptDefining(dir, "other")
	assert.ErrorContains(t, err, "defined in several scripts (a.groovy, b.groovy)")
	_, err = scriptDefining(dir, "none")
	assert.ErrorContains(t, err, "no script defines none")
}

// TestRunCasesGradle runs the fixtures on the real stand-ins (needs JDK 17+,
// Gradle and Maven Central, like TestStandInsBuild).
func TestRunCasesGradle(t *testing.T) {
	if os.Getenv("CPICTL_GRADLE_TESTS") == "" {
		t.Skip("set CPICTL_GRADLE_TESTS=1 to run the script cases on the stand-ins (needs JDK 17+, gradle, network)")
	}
	if _, err := exec.LookPath("gradle"); err != nil {
		t.Skip("gradle not on PATH")
	}
	dir, cases := casesProject(t)
	res, err := RunCases(context.Background(), cases, CaseOptions{Project: dir, Command: []string{"gradle", "-q", "--no-daemon", casesTask}, Output: testingWriter{t}})
	require.NoError(t, err)
	for _, c := range res.Cases {
		t.Logf("%s %s: %s %s %+v", c.Flow, c.Case, c.Status, c.Error, c.Failures)
	}
	assert.Equal(t, 6, res.Passed)
	assert.Equal(t, 1, res.Failed, "wrong.yaml fails on purpose")
	assert.Zero(t, res.Errors)
}
