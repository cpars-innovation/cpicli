package ops

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchMessagesByHeader(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.FilterMessageLogs = true
	mock.MessageLogSteps = [][]cpitest.MessageLog{pathLogs()}
	ctx := context.Background()
	exe := mock.Executer()

	res, err := SearchMessagesByHeader(ctx, exe, HeaderSearchQuery{Header: CustomHeaderFilter{Name: "OrderNo", Value: "4711"}, IncludeErrors: true})
	require.NoError(t, err)
	require.Equal(t, 2, res.Total)
	assert.Equal(t, "B1", res.Logs[0].MessageGuid, "newest first")
	assert.Equal(t, "O1", res.Logs[1].MessageGuid)
	assert.Equal(t, "Orders_In", res.Logs[1].ArtifactID)
	assert.Zero(t, res.Scanned, "found by the tenant, nothing scanned")
	assert.Equal(t, `custom header OrderNo = "4711"`, res.Filter)
	assert.Equal(t, []string{"GET /api/v1/MessageProcessingLogCustomHeaderProperties"}, mock.Requests())

	// narrowing: flow, status, time, top
	res, err = SearchMessagesByHeader(ctx, exe, HeaderSearchQuery{Header: CustomHeaderFilter{Name: "OrderNo", Value: "4711"}, ArtifactIDs: []string{"Orders_In"}})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Total)
	res, err = SearchMessagesByHeader(ctx, exe, HeaderSearchQuery{Header: CustomHeaderFilter{Name: "OrderNo", Value: "4711"}, Statuses: []string{"FAILED"}})
	require.NoError(t, err)
	assert.Zero(t, res.Total)
	res, err = SearchMessagesByHeader(ctx, exe, HeaderSearchQuery{Header: CustomHeaderFilter{Name: "OrderNo", Value: "4711"},
		Since: time.Date(2026, 10, 5, 8, 0, 5, 0, time.UTC)})
	require.NoError(t, err)
	assert.Equal(t, []string{"B1"}, guids(res.Logs))
	res, err = SearchMessagesByHeader(ctx, exe, HeaderSearchQuery{Header: CustomHeaderFilter{Name: "OrderNo", Value: "4711"}, Top: 1})
	require.NoError(t, err)
	assert.True(t, res.Truncated)
	assert.Len(t, res.Logs, 1)

	_, err = SearchMessagesByHeader(ctx, exe, HeaderSearchQuery{})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
}

func guids(logs []MessageLog) []string {
	out := []string{}
	for _, l := range logs {
		out = append(out, l.MessageGuid)
	}
	return out
}

// Without MessageProcessingLogCustomHeaderProperties the search scans the
// scope, and needs one.
func TestSearchMessagesByHeaderFallback(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.FilterMessageLogs = true
	mock.NoCustomHeaderSearch = true
	mock.MessageLogSteps = [][]cpitest.MessageLog{pathLogs()}
	ctx := context.Background()

	_, err := SearchMessagesByHeader(ctx, mock.Executer(), HeaderSearchQuery{Header: CustomHeaderFilter{Name: "OrderNo", Value: "4711"}})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))

	res, err := SearchMessagesByHeader(ctx, mock.Executer(), HeaderSearchQuery{Header: CustomHeaderFilter{Name: "OrderNo", Value: "4711"},
		ArtifactIDs: []string{"Billing_In"}, Since: time.Now().Add(-time.Hour)})
	require.NoError(t, err)
	assert.Equal(t, []string{"B1"}, guids(res.Logs))
	assert.Positive(t, res.Scanned)
	assert.Contains(t, res.Filter, "client-side")
}

// The mock serves the entity set like the tenant: $filter on Name and Value
// (eq), $expand=Log; other filters are refused.
func TestMockCustomHeaderEntitySet(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.MessageLogSteps = [][]cpitest.MessageLog{pathLogs()}
	exe := mock.Executer()
	for _, tc := range []struct {
		filter string
		status int
		rows   int
	}{
		{"Name eq 'OrderNo'", 200, 3},
		{"Value eq '9999' and Name eq 'OrderNo'", 200, 1},
		{"substringof('47',Value)", 400, 0},
	} {
		resp, err := exe.ExecGetRequest("/api/v1/MessageProcessingLogCustomHeaderProperties?$filter="+url.QueryEscape(tc.filter)+"&$expand=Log", nil)
		require.NoError(t, err, tc.filter)
		assert.Equal(t, tc.status, resp.StatusCode, tc.filter)
		var data struct {
			D struct {
				Results []struct {
					Name string
					Log  struct{ MessageGuid string }
				} `json:"results"`
			} `json:"d"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&data)
		_ = resp.Body.Close()
		assert.Len(t, data.D.Results, tc.rows, tc.filter)
		for _, r := range data.D.Results {
			assert.NotEmpty(t, r.Log.MessageGuid, "expanded Log")
		}
	}
}
