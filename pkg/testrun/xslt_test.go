package testrun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/cpars-innovation/cpicli/pkg/testtools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFakeSaxon stands in for Saxon: it reads -s:, -o: and name=value
// arguments and writes <out p="params">input</out>, or fails when the input
// contains FAIL.
func TestFakeSaxon(t *testing.T) {
	if os.Getenv("CPICTL_FAKE_SAXON") == "" {
		return
	}
	var in, out string
	var params []string
	for _, a := range os.Args {
		switch {
		case strings.HasPrefix(a, "-s:"):
			in = a[3:]
		case strings.HasPrefix(a, "-o:"):
			out = a[3:]
		case strings.Contains(a, "=") && !strings.HasPrefix(a, "-"):
			params = append(params, a)
		}
	}
	data, _ := os.ReadFile(in)
	if strings.Contains(string(data), "FAIL") {
		os.Stderr.WriteString("Error at xsl:value-of on line 3: XPTY0004: A sequence of more than one item\n")
		os.Exit(2)
	}
	_ = os.WriteFile(out, []byte(`<?xml version="1.0" encoding="UTF-8"?>`+"\n"+`<out p="`+strings.Join(params, ",")+`">`+string(data)+"</out>"), 0o644)
	os.Exit(0)
}

func fakeSaxon(t *testing.T) []string {
	t.Setenv("CPICTL_FAKE_SAXON", "1")
	return []string{os.Args[0], "-test.run=TestFakeSaxon", "--"}
}

func xsltProject(t *testing.T) (tests, pd string) {
	t.Helper()
	dir := t.TempDir()
	tests, pd = filepath.Join(dir, ".cpi", "tests"), filepath.Join(dir, "partner-directory")
	write(t, filepath.Join(pd, "SAP_ERP", "Binary"), "OrderMap.xsl", "<xsl:stylesheet/>")
	c := filepath.Join(tests, "pd", "SAP_ERP", "OrderMap")
	write(t, c, "maps.yaml", `description: maps an order
tags: [smoke]
input: "<o>1</o>"
headers: {SAP_Sender: ERP}
properties: {Mode: test}
expect:
  output: |
    <out   p="Mode=test,SAP_Sender=ERP">
       <o>1</o>
    </out>
  output_contains: ["<o>1</o>"]
  output_xpath: {/out/o: "1", /out/@p: "Mode=test,SAP_Sender=ERP"}
`)
	write(t, c, "wrong.yaml", `input: "<o>2</o>"
expect:
  output: "<out p=\"\"><o>3</o></out>"
  output_not_contains: ["<o>2</o>"]
`)
	write(t, c, "fails.yaml", `input: "<o>FAIL</o>"
expect:
  error_contains: [XPTY0004]
`)
	write(t, c, "breaks.yaml", `input: "<o>FAIL</o>"
`)
	write(t, c, "later.yaml", "skip: not yet\n")
	// a flow case next to the pd folder: Load does not see pd
	write(t, filepath.Join(tests, "Orders_In"), "ok.yaml", "request: {body: x}\n")
	return tests, pd
}

func TestRunXSLT(t *testing.T) {
	tests, pd := xsltProject(t)
	flows, err := Load(tests, Filter{})
	require.NoError(t, err)
	require.Len(t, flows, 1, "pd is not a flow")

	cases, err := LoadXSLT(tests, Filter{})
	require.NoError(t, err)
	require.Len(t, cases, 5)
	assert.Equal(t, "SAP_ERP", cases[0].PID)
	assert.Equal(t, "OrderMap", cases[0].ID)
	smoke, err := LoadXSLT(tests, Filter{Tags: []string{"smoke"}, Flows: []string{"SAP_ERP/*"}})
	require.NoError(t, err)
	require.Len(t, smoke, 1)

	res, err := RunXSLT(context.Background(), cases, XSLTOptions{PDDir: pd, Command: fakeSaxon(t), Parallel: 2})
	require.NoError(t, err)
	by := map[string]CaseResult{}
	for _, c := range res.Cases {
		by[c.Case] = c
		assert.Equal(t, "pd:SAP_ERP/OrderMap", c.Flow)
	}
	assert.Equal(t, StatusPassed, by["maps"].Status, "%+v", by["maps"].Failures)
	assert.Equal(t, StatusPassed, by["fails"].Status, "%+v", by["fails"].Failures)
	assert.Equal(t, StatusSkipped, by["later"].Status)

	wrong := by["wrong"]
	require.Equal(t, StatusFailed, wrong.Status)
	require.Len(t, wrong.Failures, 2)
	assert.Equal(t, "output", wrong.Failures[0].Expectation)
	assert.Equal(t, "output_not_contains", wrong.Failures[1].Expectation)
	require.NotNil(t, wrong.Response)
	assert.Contains(t, wrong.Response.Text, "<o>2</o>")

	breaks := by["breaks"]
	require.Equal(t, StatusFailed, breaks.Status)
	assert.Equal(t, "transformation", breaks.Failures[0].Expectation)
	assert.Contains(t, breaks.Diagnostics.LastError, "XPTY0004")

	assert.Equal(t, 2, res.Passed)
	assert.Equal(t, 2, res.Failed)
	assert.Equal(t, 1, res.Skipped)
	assert.Equal(t, exitcode.DeployFailed, output.ExitCode(res.Err()))

	// no stylesheet in the Partner Directory folder: an error, not a failure
	res, err = RunXSLT(context.Background(), cases[:1], XSLTOptions{PDDir: t.TempDir(), Command: fakeSaxon(t)})
	require.NoError(t, err)
	assert.Equal(t, StatusError, res.Cases[0].Status)
	assert.Contains(t, res.Cases[0].Error, "run pd-snapshot")

	_, err = RunXSLT(context.Background(), cases, XSLTOptions{})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err), "no processor")
}

func TestLoadXSLTStrict(t *testing.T) {
	dir := t.TempDir()
	c := filepath.Join(dir, "pd", "P", "X")
	write(t, c, "a.yaml", "inptu: x\n")
	_, err := LoadXSLT(dir, Filter{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a.yaml:1: field inptu not found")

	write(t, c, "a.yaml", "headers: {A: 1}\nproperties: {A: 2}\n")
	_, err = LoadXSLT(dir, Filter{})
	assert.Contains(t, err.Error(), "A is both a header and a property")

	write(t, c, "a.yaml", "input_file: missing.xml\n")
	_, err = LoadXSLT(dir, Filter{})
	assert.Contains(t, err.Error(), "missing.xml not found")

	none, err := LoadXSLT(t.TempDir(), Filter{})
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestSameOutput(t *testing.T) {
	assert.True(t, sameOutput([]byte(`<?xml version="1.0"?><a x="1" y="2"><!-- c --><b> t </b></a>`), []byte("<a y=\"2\" x=\"1\">\n  <b>t</b>\n</a>")))
	assert.True(t, sameOutput([]byte(`<p:a xmlns:p="urn:x"/>`), []byte(`<q:a xmlns:q="urn:x"/>`)), "prefixes do not count")
	assert.False(t, sameOutput([]byte(`<a><b>1</b></a>`), []byte(`<a><b>2</b></a>`)))
	assert.False(t, sameOutput([]byte(`<a x="1"/>`), []byte(`<a x="2"/>`)))
	assert.True(t, sameOutput([]byte("plain text \n"), []byte("plain text")), "text output")
	assert.False(t, sameOutput([]byte("a"), []byte("b")))
}

// Online: the stylesheet goes to the Partner Directory, the case to the
// XSLT test flow (emulated by the mock with a fake transformer), the
// uploads are removed afterwards.
func TestRunXSLTOnline(t *testing.T) {
	defer func(n int, d time.Duration) { xsltLogTries, xsltLogInterval = n, d }(xsltLogTries, xsltLogInterval)
	xsltLogTries, xsltLogInterval = 3, time.Millisecond
	tests, pd := xsltProject(t)
	cases, err := LoadXSLT(tests, Filter{})
	require.NoError(t, err)
	m, newExe := demo(t)
	_, err = ops.InstallTestTools(context.Background(), m.Executer(), ops.TestToolsOptions{Only: testtools.ToolXSLT, Deploy: true,
		DeployOptions: ops.Options{Interval: time.Millisecond, MaxChecks: 5}})
	require.NoError(t, err)
	var seenXSL string
	m.XSLT = func(xsl, input []byte, params map[string]string) ([]byte, error) {
		seenXSL = string(xsl)
		if strings.Contains(string(input), "FAIL") {
			return nil, errors.New("XPTY0004: A sequence of more than one item")
		}
		names := []string{}
		for _, k := range []string{"Mode", "SAP_Sender"} {
			if v, ok := params[k]; ok {
				names = append(names, k+"="+v)
			}
		}
		return []byte(`<out p="` + strings.Join(names, ",") + `">` + string(input) + `</out>`), nil
	}

	res, err := RunXSLT(context.Background(), cases, XSLTOptions{PDDir: pd, Online: true, Exe: m.Executer(), NewExe: newExe})
	require.NoError(t, err)
	by := map[string]CaseResult{}
	for _, c := range res.Cases {
		by[c.Case] = c
	}
	assert.Equal(t, StatusPassed, by["maps"].Status, "%+v %s", by["maps"].Failures, by["maps"].Error)
	assert.NotEmpty(t, by["maps"].MessageGuid)
	assert.Equal(t, StatusPassed, by["fails"].Status, "%+v %s", by["fails"].Failures, by["fails"].Error)
	assert.Equal(t, StatusFailed, by["wrong"].Status)
	assert.Equal(t, StatusFailed, by["breaks"].Status)
	assert.Contains(t, by["breaks"].Diagnostics.LastError, "XPTY0004", "from the message log: the tenant's 500 only names the message")
	assert.NotEmpty(t, by["breaks"].MessageGuid, "the message ID from the 500's text")
	assert.Equal(t, "<xsl:stylesheet/>", seenXSL, "the stylesheet from the Partner Directory folder")
	assert.Empty(t, m.PDBinaries, "uploads removed")

	// Keep leaves them for inspection
	_, err = RunXSLT(context.Background(), cases[:1], XSLTOptions{PDDir: pd, Online: true, Exe: m.Executer(), NewExe: newExe, Keep: true})
	require.NoError(t, err)
	assert.Contains(t, m.PDBinaries, testtools.XSLTPID+"/"+OnlineXSLTID("SAP_ERP", "OrderMap"))

	_, err = RunXSLT(context.Background(), cases, XSLTOptions{PDDir: pd, Online: true, Exe: m.Executer(), NewExe: newExe, Tier: "prod"})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err), "refused on prod")
}

func TestOnlineXSLTID(t *testing.T) {
	assert.Equal(t, "SAP_ERP.OrderMap", OnlineXSLTID("SAP_ERP", "OrderMap"))
	assert.Equal(t, "P_1.a_b", OnlineXSLTID("P 1", "a/b"))
	long := OnlineXSLTID(strings.Repeat("P", 50), strings.Repeat("I", 50))
	assert.Len(t, long, 60)
	assert.NotEqual(t, long, OnlineXSLTID(strings.Repeat("P", 50), strings.Repeat("I", 49)+"J"))
}
