package lint

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const collectionScript = `import com.sap.gateway.ip.core.customdev.util.Message

/**
 * Sets the order number as a custom header for monitoring.
 * Reads: header OrderNo. Writes: custom header property OrderNo.
 * Call: script collection Orders_Scripts, Headers.groovy, function setOrderHeader.
 */
def Message setOrderHeader(Message message) {
    message
}

def Message cleanHeaders(Message message) {
    message
}

private String helper(String s) { s }
`

func writeProjectFile(t *testing.T, root, name, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

// Script collections are libraries: every function says what it does, tests
// call every function, and the coverage report reaches the minimum.
func TestScriptDocAndTests(t *testing.T) {
	project := t.TempDir()
	content := filepath.Join(project, "packages")
	tFlow{pkg: "Orders", id: "Orders_Scripts", collection: true, scripts: map[string]string{
		"Headers.groovy": collectionScript,
		"Format.groovy":  "/** Formats dates. */\ndef String isoDate(String d) {\n  d\n}\n",
	}}.write(t, content)
	tFlow{pkg: "Orders", id: "Orders_In", steps: []tStep{tScript("S1", "Map", "Map.groovy"), tScript("S2", "Log", "Log.groovy")},
		scripts: map[string]string{
			"Map.groovy": "def Message processData(Message message) {\n  message\n}\n",
			"Log.groovy": "// Logs the order number.\n" + logScript,
		}}.write(t, content)

	lintWith := func(yaml string) *Result {
		t.Helper()
		writeProjectFile(t, project, ".cpi/lint.yaml", yaml)
		cfg, err := LoadConfig(filepath.Join(project, ".cpi", "lint.yaml"))
		require.NoError(t, err)
		res, err := Run(context.Background(), Options{Dir: content, Config: cfg})
		require.NoError(t, err)
		return res
	}

	// no tests, no report
	res := lintWith("")
	doc := findingsOf(res, "collection-script-doc", "Orders_Scripts")
	require.Len(t, doc, 1, "Format.groovy is documented, the private helper does not count")
	assert.Equal(t, "Headers.groovy: function without a doc comment: cleanHeaders", doc[0].Message)
	assert.Equal(t, SevWarning, doc[0].Severity)

	flowDoc := findingsOf(res, "script-doc", "Orders_In")
	require.Len(t, flowDoc, 1)
	assert.Equal(t, "script Map.groovy says nothing about what it does", flowDoc[0].Message)
	assert.Equal(t, SevInfo, flowDoc[0].Severity)

	tests := findingsOf(res, "collection-script-tests", "Orders_Scripts")
	require.Len(t, tests, 2)
	assert.Contains(t, tests[0].Message, "has no tests (tests/scripts/Orders/Orders_Scripts is empty or missing)")
	cov := findingsOf(res, "collection-script-coverage", "Orders_Scripts")
	require.Len(t, cov, 1)
	assert.Contains(t, cov[0].Message, "coverage not measured")

	// tests for one function, and a report: Headers.groovy below the minimum,
	// Format.groovy above it
	writeProjectFile(t, project, "tests/scripts/Orders/Orders_Scripts/HeadersTest.groovy",
		"class HeadersTest { void testSet() { new Headers().setOrderHeader(msg) } }\n")
	writeProjectFile(t, project, "tests/scripts/Orders/Orders_Scripts/FormatTest.groovy",
		"class FormatTest { void test() { assert new Format().isoDate ('x') } }\n")
	writeProjectFile(t, project, "build/reports/jacoco/test/jacocoTestReport.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<!DOCTYPE report PUBLIC "-//JACOCO//DTD Report 1.1//EN" "report.dtd">
<report name="scripts"><package name="">
<sourcefile name="Headers.groovy"><counter type="INSTRUCTION" missed="9" covered="1"/><counter type="LINE" missed="3" covered="2"/></sourcefile>
<sourcefile name="Format.groovy"><counter type="LINE" missed="0" covered="2"/></sourcefile>
</package></report>`)
	// the report is newer than the scripts
	future := time.Now().Add(time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(project, "build/reports/jacoco/test/jacocoTestReport.xml"), future, future))
	res = lintWith("")
	tests = findingsOf(res, "collection-script-tests", "Orders_Scripts")
	require.Len(t, tests, 1)
	assert.Equal(t, "Headers.groovy: no test calls cleanHeaders", tests[0].Message)
	cov = findingsOf(res, "collection-script-coverage", "Orders_Scripts")
	require.Len(t, cov, 1)
	assert.Equal(t, "Headers.groovy: line coverage 40% (minimum 80%)", cov[0].Message)

	// the minimum and the paths come from .cpi/lint.yaml, relative to the project
	res = lintWith("scriptTests:\n  minCoverage: 40\n")
	assert.Empty(t, findingsOf(res, "collection-script-coverage", "Orders_Scripts"))
	res = lintWith("scriptTests:\n  dir: other\n  coverageReport: missing.xml\n")
	assert.Contains(t, findingsOf(res, "collection-script-tests", "Orders_Scripts")[0].Message, "other/Orders/Orders_Scripts")
	assert.Contains(t, findingsOf(res, "collection-script-coverage", "Orders_Scripts")[0].Message, "missing.xml")

	// a script changed after the test run: its coverage is unknown
	past := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(project, "build/reports/jacoco/test/jacocoTestReport.xml"), past, past))
	res = lintWith("")
	cov = findingsOf(res, "collection-script-coverage", "Orders_Scripts")
	require.Len(t, cov, 2)
	assert.Contains(t, cov[0].Message, "changed after the coverage report was written")
}

// A token URL is an address, not a secret (SAP's pipeline flows,
// CI_ODataAPI_TokenURL); a URL that carries credentials still is.
func TestSecretURLs(t *testing.T) {
	content := t.TempDir()
	tFlow{pkg: "P", id: "Flow", params: "CI_ODataAPI_TokenURL=https\\://host/oauth/token\n" +
		"Token_Path=/oauth/token\n" +
		"WebhookToken=https\\://host/hook?token=abc123\n" +
		"Api_Url=https\\://user\\:pw@host/api\n" +
		"ClientSecret=s3cr3tvalue\n"}.write(t, content)
	res, err := Run(context.Background(), Options{Dir: content})
	require.NoError(t, err)
	var keys []string
	for _, f := range findingsOf(res, "hardcoded-secret", "Flow") {
		keys = append(keys, f.Message)
	}
	assert.ElementsMatch(t, []string{
		"the parameter WebhookToken holds a URL with credentials (user info or a secret query parameter)",
		"the parameter Api_Url holds a URL with credentials (user info or a secret query parameter)",
		"the parameter ClientSecret holds a value that looks like a secret",
	}, keys)
}

// SAP-delivered content (package vendor SAP, or com.sap. IDs without package
// details) is left out by default; ignoreVendors: [] checks it.
func TestIgnoreVendors(t *testing.T) {
	content := t.TempDir()
	secret := "Password=hunter22\n"
	tFlow{pkg: "SAPPkg", id: "Std_Flow", params: secret}.write(t, content)
	writeProjectFile(t, content, "SAPPkg/SAPPkg.json", `{"d":{"Id":"SAPPkg","Name":"SAP content","Vendor":"SAP"}}`)
	tFlow{pkg: "Loose", id: "com.sap.integration.cloud.pipeline.step02", params: secret}.write(t, content)
	tFlow{pkg: "Mine", id: "My_Flow", params: secret}.write(t, content)
	writeProjectFile(t, content, "Mine/Mine.json", `{"d":{"Id":"Mine","Name":"Mine","Vendor":"ACME"}}`)

	res, err := Run(context.Background(), Options{Dir: content})
	require.NoError(t, err)
	assert.Equal(t, 2, res.SkippedVendor)
	assert.Equal(t, 1, res.Checked)
	require.Len(t, findingsOf(res, "hardcoded-secret", "My_Flow"), 1)
	assert.Equal(t, "ACME", findingsOf(res, "hardcoded-secret", "My_Flow")[0].Vendor)
	assert.Empty(t, findingsOf(res, "hardcoded-secret", "Std_Flow"))

	cfg := DefaultConfig()
	cfg.IgnoreVendors = nil
	res, err = Run(context.Background(), Options{Dir: content, Config: cfg})
	require.NoError(t, err)
	assert.Zero(t, res.SkippedVendor)
	assert.Equal(t, "SAP", findingsOf(res, "hardcoded-secret", "Std_Flow")[0].Vendor)
	assert.Equal(t, "SAP", findingsOf(res, "hardcoded-secret", "com.sap.integration.cloud.pipeline.step02")[0].Vendor)
}

func TestIgnoreVendorsConfig(t *testing.T) {
	dir := t.TempDir()
	writeProjectFile(t, dir, ".cpi/lint.yaml", "ignoreVendors: []\n")
	cfg, err := LoadConfig(filepath.Join(dir, ".cpi", "lint.yaml"))
	require.NoError(t, err)
	assert.Empty(t, cfg.IgnoreVendors)
	writeProjectFile(t, dir, ".cpi/lint.yaml", "rules: {}\n")
	cfg, err = LoadConfig(filepath.Join(dir, ".cpi", "lint.yaml"))
	require.NoError(t, err)
	assert.Equal(t, []string{"SAP"}, cfg.IgnoreVendors)
}

func TestUnsupportedScriptClass(t *testing.T) {
	a := &lintArtifact{Scripts: map[string][]byte{
		"src/main/resources/script/stub.groovy": []byte("def ms = 100\nThread.sleep(ms)\n"),
		"src/main/resources/script/ok.groovy":   []byte("// ThreadLocal and a Thread in a comment line\ndef threads = 1\nmessage.setHeader('ThreadId', '1')\n"),
		"src/main/resources/script/new.groovy":  []byte("def t = new Thread({ -> })\n"),
	}}
	f := ruleUnsupportedScriptClass(nil, a)
	require.Len(t, f, 2, "%v", f)
	assert.Contains(t, f[0].Message, "new.groovy line 1")
	assert.Contains(t, f[1].Message, "stub.groovy line 2")
}

// Comments of script collections have to be good enough to reuse the
// script: no placeholders, every header and property named, keywords.
func TestCollectionScriptDocQuality(t *testing.T) {
	project := t.TempDir()
	content := filepath.Join(project, "packages")
	good := `/**
 * Formats the SAP date in header SAP_Date as ISO 8601.
 *
 * @reads    header SAP_Date
 * @writes   property isoDate
 * @keywords date, ISO 8601
 */
def Message formatSapDate(Message message) {
    message.setProperty("isoDate", message.getHeader("SAP_Date", String))
}
`
	poor := `/** TODO */
def Message formatDate(Message message) {
    message.setProperty("isoDate", message.getHeader("SAP_Date", String))
}
`
	tFlow{pkg: "Shared", id: "Common_Scripts", collection: true, scripts: map[string]string{"Good.groovy": good, "Poor.groovy": poor}}.write(t, content)
	tFlow{pkg: "Orders", id: "Orders_In", steps: []tStep{tScript("S1", "Map", "Map.groovy")},
		scripts: map[string]string{"Map.groovy": "// Script.\ndef Message processData(Message message) {\n  message\n}\n"}}.write(t, content)

	res, err := Run(context.Background(), Options{Dir: content})
	require.NoError(t, err)

	quality := findingsOf(res, "collection-script-doc-quality", "Common_Scripts")
	require.Len(t, quality, 1, "Good.groovy is complete")
	assert.Equal(t, `Poor.groovy: formatDate: the description "TODO" says nothing; the comments do not name header SAP_Date, property isoDate (use @reads / @writes)`, quality[0].Message)
	assert.Equal(t, SevWarning, quality[0].Severity)

	keywords := findingsOf(res, "collection-script-keywords", "Common_Scripts")
	require.Len(t, keywords, 1)
	assert.Contains(t, keywords[0].Message, "Poor.groovy")
	assert.Equal(t, SevInfo, keywords[0].Severity)

	flowDoc := findingsOf(res, "script-doc", "Orders_In")
	require.Len(t, flowDoc, 1)
	assert.Equal(t, `script Map.groovy: the description "Script." says nothing about what it does`, flowDoc[0].Message)
}
