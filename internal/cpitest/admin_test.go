package cpitest_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func admin(t *testing.T, m *cpitest.Tenant, method, path string, body any, out any) int {
	t.Helper()
	var b bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&b).Encode(body))
	}
	req, err := http.NewRequest(method, m.URL()+"/_mock"+path, &b)
	require.NoError(t, err)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	if out != nil {
		require.NoError(t, json.NewDecoder(res.Body).Decode(out))
	}
	return res.StatusCode
}

func TestAdminSystemsAndRun(t *testing.T) {
	m := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(m, "dev", time.Now()))
	var systems map[string]cpitest.SystemSpec
	require.Equal(t, http.StatusOK, admin(t, m, http.MethodGet, "/systems", nil, &systems))
	require.Contains(t, systems, "finance")

	finance := systems["finance"]
	finance.FailRate, finance.Error = 1, "finance down for {key}"
	require.Equal(t, http.StatusOK, admin(t, m, http.MethodPut, "/systems/finance", finance, nil))
	var out struct {
		Runs []map[string]string `json:"runs"`
	}
	require.Equal(t, http.StatusOK, admin(t, m, http.MethodPost, "/run", map[string]any{"artifact": "Billing_Post", "key": "4711", "count": 2}, &out))
	require.Len(t, out.Runs, 2)
	assert.Equal(t, "FAILED", out.Runs[0]["status"])
	assert.Equal(t, "finance down for 4711", out.Runs[0]["error"])

	assert.Equal(t, http.StatusBadRequest, admin(t, m, http.MethodPost, "/run", map[string]any{"artifact": "Nope"}, nil))
	assert.Equal(t, http.StatusBadRequest, admin(t, m, http.MethodPut, "/systems/x", cpitest.SystemSpec{FailRate: 2}, nil))
	assert.Equal(t, http.StatusNotFound, admin(t, m, http.MethodGet, "/nothing", nil, nil))
}

func TestAdminFaultsAndLogs(t *testing.T) {
	m := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(m, "test", time.Now()))
	exe := m.Executer()

	require.Equal(t, http.StatusOK, admin(t, m, http.MethodPost, "/faults", map[string]any{"pathPrefix": "/api/v1/IntegrationPackages", "status": 503, "count": 1}, nil))
	_, err := ops.ListPackages(exe)
	require.Error(t, err, "one-shot fault")
	_, err = ops.ListPackages(exe)
	require.NoError(t, err, "the fault is used up")

	log := cpitest.MessageLog{Guid: "AGX1", Artifact: "Orders_In", Status: "ESCALATED", CorrelationID: "C-x", ErrorText: "escalated by test"}
	require.Equal(t, http.StatusOK, admin(t, m, http.MethodPost, "/messagelogs", []cpitest.MessageLog{log}, nil))
	got, err := ops.QueryMessageLogs(exe, ops.MessageLogQuery{Statuses: []string{"ESCALATED"}})
	require.NoError(t, err)
	require.Len(t, got.Logs, 1)
	assert.Equal(t, "AGX1", got.Logs[0].MessageGuid)
}

func TestAdminStateExportImport(t *testing.T) {
	src := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(src, "prod", time.Now()))
	var state json.RawMessage
	require.Equal(t, http.StatusOK, admin(t, src, http.MethodGet, "/state", nil, &state))

	dst := cpitest.NewTenant(t, nil)
	require.Equal(t, http.StatusNoContent, admin(t, dst, http.MethodPut, "/state", state, nil))
	assert.Equal(t, src.Packages, dst.Packages)
	assert.Len(t, dst.MessageLogs(), len(src.MessageLogs()))
	assert.Equal(t, src.Artifacts["Billing_Post"].Parameters, dst.Artifacts["Billing_Post"].Parameters)
	pkgs, err := ops.ListPackages(dst.Executer())
	require.NoError(t, err)
	assert.Len(t, pkgs, 3)
}

// runOnce runs a flow through the admin API and returns the new logs of that flow.
func runOnce(t *testing.T, m *cpitest.Tenant, artifact string) []cpitest.MessageLog {
	t.Helper()
	var out struct {
		Runs []map[string]string `json:"runs"`
	}
	require.Equal(t, http.StatusOK, admin(t, m, http.MethodPost, "/run", map[string]any{"artifact": artifact}, &out))
	var logs []cpitest.MessageLog
	for _, l := range m.MessageLogs() {
		for _, r := range out.Runs {
			if l.Guid == r["messageGuid"] {
				logs = append(logs, l)
			}
		}
	}
	return logs
}

func TestAdminRunCountIsBounded(t *testing.T) {
	m := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(m, "dev", time.Now()))
	before := len(m.MessageLogs())
	assert.Equal(t, http.StatusBadRequest, admin(t, m, http.MethodPost, "/run", map[string]any{"artifact": "Billing_Post", "count": 1001}, nil))
	assert.Equal(t, http.StatusBadRequest, admin(t, m, http.MethodPost, "/run", map[string]any{"artifact": "Billing_Post", "count": 100000000}, nil))
	assert.Len(t, m.MessageLogs(), before, "nothing ran")
	assert.Equal(t, http.StatusOK, admin(t, m, http.MethodPost, "/run", map[string]any{"artifact": "Billing_Post", "count": 1000}, nil))
}

func mkLog(guid string, start time.Time) cpitest.MessageLog {
	return cpitest.MessageLog{Guid: guid, Artifact: "Orders_In", Status: "COMPLETED", Start: start, End: start.Add(time.Second)}
}

func guids(m *cpitest.Tenant) []string {
	var out []string
	for _, l := range m.MessageLogs() {
		out = append(out, l.Guid)
	}
	return out
}

func TestLogsStayNewestFirst(t *testing.T) {
	m := cpitest.NewTenant(t, nil)
	now := time.Now()
	add := func(logs ...cpitest.MessageLog) {
		require.Equal(t, http.StatusOK, admin(t, m, http.MethodPost, "/messagelogs", logs, nil))
	}
	add(mkLog("B", now.Add(-2*time.Hour)), mkLog("D", now.Add(-4*time.Hour)))
	add(mkLog("A", now.Add(-time.Hour)))                                          // newest: in front
	add(mkLog("E", now.Add(-5*time.Hour)))                                        // oldest: at the end
	add(mkLog("C2", now.Add(-3*time.Hour)), mkLog("C1", now.Add(-3*time.Hour/2))) // a batch in between, unordered
	add(mkLog("A0", now), mkLog("B2", now.Add(-2*time.Hour)))                     // a tie keeps the earlier log first
	assert.Equal(t, []string{"A0", "A", "C1", "B", "B2", "C2", "D", "E"}, guids(m))
}

func TestLogRetentionAndCap(t *testing.T) {
	m := cpitest.NewTenant(t, nil)
	now := time.Now()
	m.LogRetention = 2 * time.Hour
	require.Equal(t, http.StatusOK, admin(t, m, http.MethodPost, "/messagelogs",
		[]cpitest.MessageLog{mkLog("new", now), mkLog("old", now.Add(-3*time.Hour)), mkLog("mid", now.Add(-time.Hour))}, nil))
	assert.Equal(t, []string{"new", "mid"}, guids(m), "older than the retention: dropped")

	m.LogRetention = -1 // no age limit
	m.MaxLogs = 3
	require.Equal(t, http.StatusOK, admin(t, m, http.MethodPost, "/messagelogs",
		[]cpitest.MessageLog{mkLog("x1", now.Add(-5*time.Hour)), mkLog("x2", now.Add(-10*time.Hour)), mkLog("x3", now.Add(-time.Minute))}, nil))
	assert.Equal(t, []string{"new", "x3", "mid"}, guids(m), "beyond the cap the oldest are dropped")
}

func TestLogRetentionIsMeasuredFromTheNewestLog(t *testing.T) {
	// logs added for an earlier period are kept (the clock does not count)
	m := cpitest.NewTenant(t, nil)
	var res map[string]int
	old := time.Now().Add(-10 * 24 * time.Hour)
	require.Equal(t, http.StatusOK, admin(t, m, http.MethodPost, "/messagelogs", []cpitest.MessageLog{mkLog("old", old)}, &res))
	assert.Equal(t, map[string]int{"added": 1, "kept": 1}, res)
	assert.Equal(t, []string{"old"}, guids(m))

	// what is older than the retention behind the newest log is dropped, and reported
	m.LogRetention = 24 * time.Hour
	require.Equal(t, http.StatusOK, admin(t, m, http.MethodPost, "/messagelogs",
		[]cpitest.MessageLog{mkLog("now", time.Now()), mkLog("older", old.Add(-time.Hour))}, &res))
	assert.Equal(t, map[string]int{"added": 2, "kept": 1}, res)
	assert.Equal(t, []string{"now"}, guids(m))

	// a mock seeded in the past keeps its history when logs of that period are added
	past := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(past, "dev", time.Now().Add(-30*24*time.Hour)))
	before := len(past.MessageLogs())
	require.Positive(t, before)
	require.Equal(t, http.StatusOK, admin(t, past, http.MethodPost, "/messagelogs",
		[]cpitest.MessageLog{mkLog("then", time.Now().Add(-30*24*time.Hour+time.Minute))}, &res))
	assert.Equal(t, before+1, len(past.MessageLogs()))
}

func TestPersistentFaultWithTenantMessage(t *testing.T) {
	m := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(m, "dev", time.Now()))
	require.Equal(t, http.StatusOK, admin(t, m, http.MethodPost, "/faults",
		map[string]any{"pathPrefix": "/api/v1/LogFiles", "status": 501, "count": -1, "message": "Not implemented"}, nil))
	for range 3 { // stays until deleted
		res, err := m.Executer().ExecGetRequest("/api/v1/LogFiles", nil)
		require.NoError(t, err)
		var body struct {
			Error struct {
				Message struct{ Value string } `json:"message"`
			} `json:"error"`
		}
		require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
		_ = res.Body.Close()
		assert.Equal(t, 501, res.StatusCode)
		assert.Equal(t, "Not implemented", body.Error.Message.Value)
	}
	require.Equal(t, http.StatusNoContent, admin(t, m, http.MethodDelete, "/faults", nil, nil))
	res, err := m.Executer().ExecGetRequest("/api/v1/LogFiles", nil)
	require.NoError(t, err)
	_ = res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
}
