package scripttest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func write(t *testing.T, root, name, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

// project writes a project with one script collection, its tests and the
// reports of a test run.
func project(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	coll := "packages/Orders/Orders_Scripts/"
	write(t, dir, coll+"META-INF/MANIFEST.MF", "Manifest-Version: 1.0\nBundle-SymbolicName: Orders_Scripts\nBundle-Version: 1.0.0\nSAP-BundleType: ScriptCollection\n")
	write(t, dir, coll+"src/main/resources/script/Headers.groovy", `import com.sap.gateway.ip.core.customdev.util.Message

/** Sets the order number as custom header. */
def Message setOrderHeader(Message message) {
    message
}

/** Removes internal headers. */
def Message cleanHeaders(Message message) {
    message
}

/** Never called by a test. */
def String untested(String s) { s }

private String helper(String s) { s }
`)
	write(t, dir, "tests/scripts/Orders/Orders_Scripts/HeadersTest.groovy", "class HeadersTest { void a() { new Headers().setOrderHeader(m) } }\n")
	write(t, dir, "tests/scripts/Orders/Orders_Scripts/CleanTest.groovy", "class CleanTest { void b() { new Headers().cleanHeaders (m) } }\n")
	write(t, dir, "build/test-results/test/TEST-HeadersTest.xml", `<?xml version="1.0" encoding="UTF-8"?>
<testsuite name="HeadersTest" tests="2" failures="0" errors="0">
  <testcase name="setsOrderHeader()" classname="HeadersTest" time="0.012"/>
  <testcase name="skipped()" classname="HeadersTest" time="0"><skipped/></testcase>
</testsuite>`)
	write(t, dir, "build/test-results/test/TEST-CleanTest.xml", `<?xml version="1.0" encoding="UTF-8"?>
<testsuite name="CleanTest" tests="1" failures="1" errors="0">
  <testcase name="removesInternal()" classname="CleanTest" time="0.5">
    <failure message="expected: &lt;null&gt; but was: &lt;x&gt;" type="org.opentest4j.AssertionFailedError">org.opentest4j.AssertionFailedError: expected
	at CleanTest.b(CleanTest.groovy:3)</failure>
  </testcase>
</testsuite>`)
	write(t, dir, "build/reports/jacoco/test/jacocoTestReport.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<!DOCTYPE report PUBLIC "-//JACOCO//DTD Report 1.1//EN" "report.dtd">
<report name="scripts"><package name="">
<class name="Headers" sourcefilename="Headers.groovy">
  <method name="setOrderHeader" desc="(LMessage;)LMessage;" line="4"><counter type="LINE" missed="0" covered="2"/></method>
  <method name="cleanHeaders" desc="(LMessage;)LMessage;" line="9"><counter type="LINE" missed="1" covered="1"/></method>
  <method name="untested" desc="(Ljava/lang/String;)Ljava/lang/String;" line="13"><counter type="LINE" missed="1" covered="0"/></method>
</class>
<sourcefile name="Headers.groovy"><counter type="LINE" missed="2" covered="3"/></sourcefile>
</package></report>`)
	return dir
}

func TestReadReports(t *testing.T) {
	dir := project(t)
	res, err := Run(context.Background(), Options{Project: dir, NoRun: true})
	require.NoError(t, err)
	assert.Empty(t, res.Command)
	assert.Equal(t, 1, res.Passed)
	assert.Equal(t, 1, res.Failed)
	assert.Equal(t, 1, res.Skipped)
	require.Len(t, res.Tests, 3)
	assert.Equal(t, TestCase{Class: "CleanTest", Name: "removesInternal()", Status: StatusFailed, DurationMs: 500,
		Message: "expected: <null> but was: <x>"}, res.Tests[0])

	require.Len(t, res.Scripts, 1)
	s := res.Scripts[0]
	assert.Equal(t, "Orders", s.Package)
	assert.Equal(t, "Orders_Scripts", s.Collection)
	assert.Equal(t, "Orders/Orders_Scripts/src/main/resources/script/Headers.groovy", s.File)
	require.NotNil(t, s.LineCoverage)
	assert.InDelta(t, 60, *s.LineCoverage, 0.01)
	require.Len(t, s.Functions, 3, "the private helper is not listed")

	set, clean, untested := s.Functions[0], s.Functions[1], s.Functions[2]
	assert.Equal(t, "setOrderHeader", set.Name)
	assert.Equal(t, "Sets the order number as custom header.", set.Summary)
	assert.Equal(t, []string{"HeadersTest.groovy"}, set.TestFiles)
	assert.Equal(t, StatusPassed, set.Status)
	assert.InDelta(t, 100, *set.LineCoverage, 0.01)

	assert.Equal(t, []string{"CleanTest.groovy"}, clean.TestFiles)
	assert.Equal(t, StatusFailed, clean.Status)
	assert.InDelta(t, 50, *clean.LineCoverage, 0.01)

	assert.Equal(t, StatusUntested, untested.Status)
	assert.Empty(t, untested.TestFiles)
	assert.InDelta(t, 0, *untested.LineCoverage, 0.01)

	assert.Equal(t, exitcode.DeployFailed, output.ExitCode(res.Err()))
}

func TestMissingReports(t *testing.T) {
	dir := t.TempDir()
	_, err := Run(context.Background(), Options{Project: dir, NoRun: true})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err), "no content tree")
	require.NoError(t, os.Mkdir(filepath.Join(dir, "packages"), 0o755))
	res, err := Run(context.Background(), Options{Project: dir, NoRun: true})
	require.NoError(t, err)
	assert.Empty(t, res.Tests)
	assert.Empty(t, res.Scripts)
	assert.Len(t, res.Warnings, 2, "no JUnit reports, no coverage report")
	assert.NoError(t, res.Err())
}

// TestHelperProcess stands in for Gradle: it exits with GO_HELPER_EXIT.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_HELPER_EXIT") == "" {
		return
	}
	os.Stdout.WriteString("> Task :test\n")
	if os.Getenv("GO_HELPER_EXIT") == "1" {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestRunCommand(t *testing.T) {
	dir := project(t)
	helper := []string{os.Args[0], "-test.run=TestHelperProcess"}

	t.Setenv("GO_HELPER_EXIT", "1") // tests failed: the build fails, the reports are read
	var out testWriter
	res, err := Run(context.Background(), Options{Project: dir, Command: helper, Output: &out})
	require.NoError(t, err)
	assert.Equal(t, 1, res.ExitCode)
	assert.Contains(t, res.Command, "-test.run=TestHelperProcess")
	assert.Equal(t, 1, res.Failed)
	assert.Contains(t, string(out), "> Task :test")

	t.Setenv("GO_HELPER_EXIT", "0")
	require.NoError(t, os.RemoveAll(filepath.Join(dir, "build/test-results/test/TEST-CleanTest.xml")))
	res, err = Run(context.Background(), Options{Project: dir, Command: helper})
	require.NoError(t, err)
	assert.Zero(t, res.ExitCode)
	assert.NoError(t, res.Err())

	_, err = Run(context.Background(), Options{Project: dir, Command: []string{filepath.Join(dir, "no-such-gradle")}})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
}

type testWriter []byte

func (w *testWriter) Write(p []byte) (int, error) { *w = append(*w, p...); return len(p), nil }

// A function's status comes from the test methods that call it, not from
// every test of the class.
func TestStatusPerTestMethod(t *testing.T) {
	dir := project(t)
	write(t, dir, "tests/scripts/Orders/Orders_Scripts/HeadersTest.groovy", `class HeadersTest {
    @Test
    void setsOrderHeader() {
        new Headers().setOrderHeader(m)
    }

    @Test
    void cleans() {
        new Headers().cleanHeaders(m)
    }

    def "untested stays untested"() {
        expect: true
    }
}
`)
	write(t, dir, "build/test-results/test/TEST-HeadersTest.xml", `<testsuite name="HeadersTest">
  <testcase name="setsOrderHeader()" classname="HeadersTest" time="0.01"/>
  <testcase name="cleans()" classname="HeadersTest" time="0.01"><failure message="boom"/></testcase>
  <testcase name="untested stays untested" classname="HeadersTest" time="0.01"/>
</testsuite>`)
	require.NoError(t, os.Remove(filepath.Join(dir, "tests/scripts/Orders/Orders_Scripts/CleanTest.groovy")))
	require.NoError(t, os.Remove(filepath.Join(dir, "build/test-results/test/TEST-CleanTest.xml")))
	res, err := Run(context.Background(), Options{Project: dir, NoRun: true})
	require.NoError(t, err)
	fns := res.Scripts[0].Functions
	assert.Equal(t, []string{"HeadersTest.setsOrderHeader"}, fns[0].Tests)
	assert.Equal(t, StatusPassed, fns[0].Status, "the failing test of the class does not call it")
	assert.Equal(t, []string{"HeadersTest.cleans"}, fns[1].Tests)
	assert.Equal(t, StatusFailed, fns[1].Status)
	assert.Equal(t, StatusUntested, fns[2].Status)

	names := []string{}
	for _, m := range testMethods(`class X {
    @Test
    void a() { }
    def "spock feature"() { }
    public void b() throws Exception { }
}`) {
		names = append(names, m.name)
	}
	assert.Equal(t, []string{"a", "spock feature", "b"}, names)
}
