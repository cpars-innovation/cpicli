package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testCases = "../../pkg/testrun/testdata/tests"

// testDemo is the demo landscape with receivers that never fail and the
// test harness deployed.
func testDemo(t *testing.T) *cpitest.Tenant {
	t.Helper()
	m := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(m, "dev", time.Now()))
	for _, name := range []string{"erp", "finance", "order-api", "partner-sftp", "returns"} {
		setSystem(t, m, name, `{"match":"`+map[string]string{"erp": "erp-*.example.com", "finance": "finance-*.example.com",
			"order-api": "orders-api-*.example.com", "partner-sftp": "sftp.partner.example.com", "returns": "returns-*.example.com"}[name]+`","failRate":0}`)
	}
	m.Artifacts[ops.DefaultHarnessID] = &cpitest.Artifact{Type: "Integration", DesignVersion: "1.0.0",
		Runtime: &cpitest.Runtime{Version: "1.0.0", Status: "STARTED"}, EndpointURL: m.URL() + "/http/cpictl/test"}
	m.Inbound["/http/cpictl/test"] = &cpitest.Inbound{Artifact: ops.DefaultHarnessID, Harness: true}
	return m
}

func setSystem(t *testing.T, m *cpitest.Tenant, name, spec string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, m.URL()+"/_mock/systems/"+name, bytes.NewReader([]byte(spec)))
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

type testRunEnvelope struct {
	ExitCode int
	Result   struct {
		Passed, Failed, Errors, Skipped int
		Warnings                        []string
		Cases                           []struct {
			Flow, Case, Status, MessageGuid string
			Failures                        []struct{ Expectation, Message string }
		}
	}
}

func TestTestListOffline(t *testing.T) {
	r := runMain(t, "test", "list", "--dir", testCases, "--output", "json")
	require.Equal(t, 0, r.code, r.stderr)
	var env struct {
		Result struct {
			Cases int
			Flows []struct {
				Flow       string
				ConfigKeys []string
				Cases      []struct{ Case, Trigger string }
			}
		}
	}
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &env), r.stdout)
	assert.Equal(t, 5, env.Result.Cases)
	require.Len(t, env.Result.Flows, 3)
	assert.Equal(t, "Orders_IDoc_In", env.Result.Flows[0].Flow)
	assert.Equal(t, []string{"ORDER_API_URL"}, env.Result.Flows[0].ConfigKeys)
	assert.Equal(t, "idoc", env.Result.Flows[0].Cases[0].Trigger)
	assert.Equal(t, "process_direct", env.Result.Flows[2].Cases[0].Trigger)

	r = runMain(t, "test", "list", "--dir", testCases, "--tag", "smoke")
	require.Equal(t, 0, r.code, r.stderr)
	assert.Contains(t, r.stderr, "2 test case(s) in 1 flow(s)")

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "F", "bad.yaml"), "request:\n  bodyy: x\n")
	r = runMain(t, "test", "list", "--dir", dir)
	assert.Equal(t, 2, r.code)
	assert.Contains(t, r.stderr, filepath.Join(dir, "F", "bad.yaml")+":2: field bodyy not found")
}

func TestTestRun(t *testing.T) {
	m := testDemo(t)
	dir := t.TempDir()
	events, junit := filepath.Join(dir, "events.jsonl"), filepath.Join(dir, "report.xml")
	r := runMain(t, append([]string{"test", "run", "--dir", testCases, "--parallel", "2", "--events", events, "--junit", junit, "--output", "json"},
		basicAuth(m)...)...)
	require.Equal(t, 0, r.code, r.stdout+r.stderr)
	var env testRunEnvelope
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &env), r.stdout)
	assert.Equal(t, 4, env.Result.Passed)
	assert.Equal(t, 1, env.Result.Skipped)
	assert.Len(t, env.Result.Warnings, 1, "config.yaml of Orders_IDoc_In differs and was not applied")
	assert.Contains(t, r.stderr, "CASE")
	assert.Contains(t, r.stderr, "4 passed, 0 failed, 0 error(s), 1 skipped")

	data, err := os.ReadFile(events)
	require.NoError(t, err)
	evs := readEvents(t, string(data))
	require.Len(t, evs, 7, "start, one item per case, end")
	items := 0
	for _, ev := range evs {
		if ev["event"] == "item" {
			items++
			assert.NotEmpty(t, ev["data"].(map[string]any)["status"])
			assert.Equal(t, "test run", ev["command"])
		}
	}
	assert.Equal(t, 5, items)
	report, err := os.ReadFile(junit)
	require.NoError(t, err)
	assert.Contains(t, string(report), `<testsuite name="Orders_In" tests="3" failures="0" errors="0" skipped="1"`)
}

// Exit codes: 5 when a case fails, 7 when cases could not run next to
// passed ones, 2 for usage and the production guard.
func TestTestRunExitCodes(t *testing.T) {
	m := testDemo(t)
	r := runMain(t, append([]string{"test", "run", "--dir", testCases, "--case", "nope"}, basicAuth(m)...)...)
	assert.Equal(t, 2, r.code, "no case matches")
	r = runMain(t, append([]string{"test", "run", "--dir", testCases, "--parallel", "0"}, basicAuth(m)...)...)
	assert.Equal(t, 2, r.code)

	before := len(m.Requests())
	r = runMain(t, append([]string{"test", "run", "--dir", testCases, "--tier", "prod"}, basicAuth(m)...)...)
	assert.Equal(t, 2, r.code)
	assert.Contains(t, r.stderr, "production tenant")
	assert.Len(t, m.Requests(), before, "nothing was read or sent")

	setSystem(t, m, "erp", `{"match":"erp-*.example.com","failRate":1,"status":504,"error":"Read timed out calling {host}"}`)
	r = runMain(t, append([]string{"test", "run", "--dir", testCases, "--flow", "Orders_Route", "--flow", "Orders_IDoc_In", "--output", "json"}, basicAuth(m)...)...)
	require.Equal(t, 5, r.code, r.stderr)
	var env testRunEnvelope
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &env), r.stdout)
	assert.Equal(t, 1, env.Result.Failed)
	assert.Equal(t, 1, env.Result.Passed)
	assert.Contains(t, r.stderr, "failing step ServiceTask_0")

	setSystem(t, m, "erp", `{"match":"erp-*.example.com","failRate":0}`)

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Orders_In", "ok.yaml"), "request: {body: <Order/>}\n")
	writeFile(t, filepath.Join(dir, "NoSuchFlow", "x.yaml"), "request: {body: x}\n")
	r = runMain(t, append([]string{"test", "run", "--dir", dir}, basicAuth(m)...)...)
	assert.Equal(t, 7, r.code, r.stderr)
	assert.True(t, strings.Contains(r.stderr, "NoSuchFlow/x could not be run"), r.stderr)
}
