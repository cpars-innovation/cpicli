package ops

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const traceA = "0af7651916cd43dd8448eb211c80319c"

// chain A -> B -> C (C failed), plus noise from another trace
func traceLogs(appID bool) []cpitest.MessageLog {
	base := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	span := func(trace, s, parent string) map[string]string {
		h := map[string]string{"trace-id": trace, "span-id": s}
		if parent != "" {
			h["parent-span-id"] = parent
		}
		return h
	}
	id := func(s string) string {
		if appID {
			return s
		}
		return ""
	}
	return []cpitest.MessageLog{
		{Guid: "C", Artifact: "Flow_C", Status: "FAILED", ErrorText: "mapping failed", Start: base.Add(2 * time.Second), End: base.Add(3 * time.Second),
			Headers: span(traceA, "cccccccccccccccc", "bbbbbbbbbbbbbbbb"), ApplicationID: id(traceA)},
		{Guid: "B", Artifact: "Flow_B", Status: "COMPLETED", Start: base.Add(time.Second), End: base.Add(4 * time.Second),
			Headers: span(traceA, "bbbbbbbbbbbbbbbb", "aaaaaaaaaaaaaaaa"), ApplicationID: id(traceA)},
		{Guid: "A", Artifact: "Flow_A", Status: "COMPLETED", Start: base, End: base.Add(5 * time.Second),
			Headers: span(traceA, "aaaaaaaaaaaaaaaa", ""), ApplicationID: id(traceA)},
		{Guid: "X", Artifact: "Flow_A", Status: "COMPLETED", Start: base, End: base,
			Headers: span("ffffffffffffffffffffffffffffffff", "dddddddddddddddd", "")},
	}
}

func headerCalls(m *cpitest.Tenant) int {
	n := 0
	for _, r := range m.Requests() {
		if strings.HasSuffix(r, "/CustomHeaderProperties") {
			n++
		}
	}
	return n
}

func TestTraceTreeByApplicationMessageID(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.FilterMessageLogs = true
	mock.MessageLogSteps = [][]cpitest.MessageLog{traceLogs(true)}

	tree, err := TraceTreeFor(context.Background(), mock.Executer(), TraceTreeQuery{TraceID: strings.ToUpper(traceA)})
	require.NoError(t, err)
	assert.Equal(t, "application_message_id", tree.Source)
	assert.Equal(t, 3, tree.Messages)
	require.Len(t, tree.Roots, 1)
	a := tree.Roots[0]
	require.Equal(t, "A", a.MessageGuid)
	require.Len(t, a.Children, 1)
	require.Len(t, a.Children[0].Children, 1)
	assert.Equal(t, "C", a.Children[0].Children[0].MessageGuid, "depth 3")
	require.NotNil(t, tree.FirstFailure)
	assert.Equal(t, "C", tree.FirstFailure.MessageGuid)
	assert.Equal(t, "mapping failed", tree.FirstFailure.ErrorText)
	assert.Equal(t, 3, headerCalls(mock), "headers only for the 3 matches, no scan")
	assert.Zero(t, tree.Scanned)
}

func TestTraceTreeScanFallback(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.FilterMessageLogs = true
	mock.MessageLogSteps = [][]cpitest.MessageLog{traceLogs(false)}
	scope := ScanScope{ArtifactIDs: []string{"Flow_A", "Flow_B", "Flow_C"}, Since: time.Now().Add(-time.Hour)}

	tree, err := TraceTreeFor(context.Background(), mock.Executer(), TraceTreeQuery{TraceID: traceA, Scope: scope})
	require.NoError(t, err)
	assert.Equal(t, "scan", tree.Source)
	assert.Equal(t, 3, tree.Messages)
	assert.Equal(t, 4, tree.Scanned)
	assert.Equal(t, "C", tree.FirstFailure.MessageGuid)

	// the cap stops the scan
	before := headerCalls(mock)
	tree, err = TraceTreeFor(context.Background(), mock.Executer(), TraceTreeQuery{TraceID: traceA, Scope: scope, MaxScan: 2})
	require.NoError(t, err)
	assert.Equal(t, 2, tree.Scanned)
	assert.True(t, tree.Truncated)
	assert.Equal(t, 2, headerCalls(mock)-before)

	_, err = TraceTreeFor(context.Background(), mock.Executer(), TraceTreeQuery{TraceID: traceA})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err), "no match by ApplicationMessageId and no scope")
	_, err = TraceTreeFor(context.Background(), mock.Executer(), TraceTreeQuery{TraceID: "xyz", Scope: scope})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
}

func TestQueryMessageLogsByHeader(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.FilterMessageLogs = true
	var logs []cpitest.MessageLog
	for i := range 30 {
		logs = append(logs, cpitest.MessageLog{Guid: fmt.Sprintf("M%02d", i), Artifact: "Orders", Status: "COMPLETED",
			Headers: map[string]string{"OrderId": fmt.Sprint(i % 3)}})
	}
	mock.MessageLogSteps = [][]cpitest.MessageLog{logs}
	scope := ScanScope{ArtifactIDs: []string{"Orders"}, Since: time.Now().Add(-time.Hour)}

	list, err := QueryMessageLogsByHeader(context.Background(), mock.Executer(), scope, CustomHeaderFilter{Name: "OrderId", Value: "1"}, 2, false)
	require.NoError(t, err)
	assert.Len(t, list.Logs, 2)
	assert.Equal(t, 20, list.Scanned, "top*10")
	assert.True(t, list.Truncated)
	for _, l := range list.Logs {
		assert.Equal(t, "Orders", l.ArtifactID)
	}

	_, err = QueryMessageLogsByHeader(context.Background(), mock.Executer(), ScanScope{ArtifactIDs: []string{"Orders"}}, CustomHeaderFilter{Name: "OrderId"}, 2, false)
	assert.Equal(t, exitcode.Usage, output.ExitCode(err), "since is required")

	list, err = QueryMessageLogsByHeader(context.Background(), mock.Executer(), scope, CustomHeaderFilter{Name: "OrderId", Value: "2"}, 100, false)
	require.NoError(t, err)
	assert.Equal(t, 30, list.Scanned)
	assert.Len(t, list.Logs, 10)
	assert.False(t, list.Truncated)
}

func TestSendTraceparent(t *testing.T) {
	mock, _, newExe := endpointMock(t, &cpitest.Inbound{})
	sent, err := SendTestMessage(context.Background(), mock.Executer(), newExe, TestMessage{ArtifactID: "Orders"})
	require.NoError(t, err)
	assert.Regexp(t, `^[0-9a-f]{32}$`, sent.TraceID)
	assert.Equal(t, sent.TraceID, TraceIDOf(mock.Received[0].Header.Get("traceparent")))

	given := "00-" + traceA + "-b7ad6b7169203331-01"
	sent, err = SendTestMessage(context.Background(), mock.Executer(), newExe, TestMessage{ArtifactID: "Orders", Headers: map[string]string{"traceparent": given}})
	require.NoError(t, err)
	assert.Equal(t, traceA, sent.TraceID)
	assert.Equal(t, given, mock.Received[1].Header.Get("traceparent"), "kept unchanged")

	sent, err = SendTestMessage(context.Background(), mock.Executer(), newExe, TestMessage{ArtifactID: "Orders", NoTrace: true})
	require.NoError(t, err)
	assert.Empty(t, sent.TraceID)
	assert.Empty(t, mock.Received[2].Header.Get("traceparent"))
}

// Orders_In -> Orders_Route (ProcessDirect, predecessor) and Orders_Audit
// (same correlation ID, no predecessor); later the order comes back from
// outside: Billing_In (new correlation ID, same OrderNo header) ->
// Billing_Post (no header, failed).
func pathLogs() []cpitest.MessageLog {
	t0 := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	order := map[string]string{"OrderNo": "4711"}
	return []cpitest.MessageLog{
		{Guid: "O1", Artifact: "Orders_In", Status: "COMPLETED", CorrelationID: "C1", Start: at(0), End: at(3), Headers: order},
		{Guid: "O2", Artifact: "Orders_Route", Status: "COMPLETED", CorrelationID: "C1", Predecessor: "O1", Start: at(1), End: at(2)},
		{Guid: "O3", Artifact: "Orders_Audit", Status: "COMPLETED", CorrelationID: "C1", Start: at(2), End: at(2)},
		{Guid: "B1", Artifact: "Billing_In", Status: "COMPLETED", CorrelationID: "C2", Start: at(10), End: at(12), Headers: order},
		{Guid: "B2", Artifact: "Billing_Post", Status: "FAILED", ErrorText: "posting failed", CorrelationID: "C2", Predecessor: "B1", Start: at(11), End: at(11)},
		{Guid: "N1", Artifact: "Billing_In", Status: "COMPLETED", CorrelationID: "C3", Start: at(5), End: at(5), Headers: map[string]string{"OrderNo": "9999"}},
	}
}

func hopList(tree *TraceTree) []string {
	out := []string{}
	for _, h := range tree.Hops {
		out = append(out, h.From+">"+h.To+" "+h.Link)
	}
	return out
}

// TestMessagePath runs against a tenant without the custom header entity
// set: key headers are found by scanning the scope.
func TestMessagePath(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.FilterMessageLogs = true
	mock.NoCustomHeaderSearch = true
	mock.MessageLogSteps = [][]cpitest.MessageLog{pathLogs()}
	ctx := context.Background()

	tree, err := MessagePathFor(ctx, mock.Executer(), MessagePathQuery{MessageGuid: "O2"})
	require.NoError(t, err)
	assert.Equal(t, "correlation_id", tree.Source)
	assert.Equal(t, "C1", tree.CorrelationID)
	assert.Equal(t, 3, tree.Messages)
	assert.Equal(t, []string{"Orders_In>Orders_Route predecessor", "Orders_Route>Orders_Audit inferred"}, hopList(tree))
	require.Len(t, tree.Roots, 1)
	assert.Nil(t, tree.FirstFailure)
	fromStart, err := MessagePathFor(ctx, mock.Executer(), MessagePathQuery{MessageGuid: "O1"})
	require.NoError(t, err)
	assert.Equal(t, tree.PathKey, fromStart.PathKey, "same route, same key")

	// joined by the key header across correlation IDs
	scope := ScanScope{ArtifactIDs: []string{"Orders_In", "Billing_In"}, Since: time.Now().Add(-time.Hour)}
	tree, err = MessagePathFor(ctx, mock.Executer(), MessagePathQuery{MessageGuid: "O2", KeyHeaders: []string{"OrderNo"}, Scope: scope})
	require.NoError(t, err)
	assert.Equal(t, "correlation_id+header", tree.Source)
	assert.Equal(t, map[string]string{"OrderNo": "4711"}, tree.Keys)
	assert.Equal(t, 5, tree.Messages, "Billing_Post comes with its correlation ID; N1 has another order")
	assert.Equal(t, []string{"Orders_In>Orders_Route predecessor", "Orders_Route>Orders_Audit inferred",
		"Orders_Audit>Billing_In header", "Billing_In>Billing_Post predecessor"}, hopList(tree))
	require.NotNil(t, tree.FirstFailure)
	assert.Equal(t, "B2", tree.FirstFailure.MessageGuid)
	assert.NotEqual(t, fromStart.PathKey, tree.PathKey)

	_, err = MessagePathFor(ctx, mock.Executer(), MessagePathQuery{MessageGuid: "O2", KeyHeaders: []string{"OrderNo"}})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err), "key headers need a scope")
	_, err = MessagePathFor(ctx, mock.Executer(), MessagePathQuery{})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
}

func TestTraceTreeHops(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.FilterMessageLogs = true
	mock.MessageLogSteps = [][]cpitest.MessageLog{traceLogs(true)}
	tree, err := TraceTreeFor(context.Background(), mock.Executer(), TraceTreeQuery{TraceID: traceA})
	require.NoError(t, err)
	assert.Equal(t, []string{"Flow_A>Flow_B span", "Flow_B>Flow_C span"}, hopList(tree))
	assert.NotEmpty(t, tree.PathKey)
}

func TestMessagePathFromTheReturningRun(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.FilterMessageLogs = true
	mock.NoCustomHeaderSearch = true
	mock.MessageLogSteps = [][]cpitest.MessageLog{pathLogs()}
	scope := ScanScope{ArtifactIDs: []string{"Orders_In", "Billing_In"}, Since: time.Now().Add(-time.Hour)}
	tree, err := MessagePathFor(context.Background(), mock.Executer(), MessagePathQuery{MessageGuid: "B2", KeyHeaders: []string{"OrderNo"}, Scope: scope})
	require.NoError(t, err)
	assert.Equal(t, []string{"Orders_In>Orders_Route predecessor", "Orders_Route>Orders_Audit inferred",
		"Orders_Audit>Billing_In header", "Billing_In>Billing_Post predecessor"}, hopList(tree), "header only where the correlation ID changes")
}

// Key headers are looked up through MessageProcessingLogCustomHeaderProperties
// when the tenant has it: no scope, no scan.
func TestMessagePathByHeaderSearch(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.FilterMessageLogs = true
	mock.MessageLogSteps = [][]cpitest.MessageLog{pathLogs()}
	tree, err := MessagePathFor(context.Background(), mock.Executer(), MessagePathQuery{MessageGuid: "O2", KeyHeaders: []string{"OrderNo"}})
	require.NoError(t, err)
	assert.Equal(t, "correlation_id+header", tree.Source)
	assert.Equal(t, 5, tree.Messages)
	assert.Zero(t, tree.Scanned, "nothing scanned")
	assert.Equal(t, []string{"Orders_In>Orders_Route predecessor", "Orders_Route>Orders_Audit inferred",
		"Orders_Audit>Billing_In header", "Billing_In>Billing_Post predecessor"}, hopList(tree))
	assert.Equal(t, 1, mock.Count("GET /api/v1/MessageProcessingLogCustomHeaderProperties"))
	assert.Contains(t, mock.LastMessageLogQuery, "C2", "the rest of the returning run by its correlation ID")

	// a scope still narrows: Billing_In is outside it
	tree, err = MessagePathFor(context.Background(), mock.Executer(), MessagePathQuery{MessageGuid: "O2", KeyHeaders: []string{"OrderNo"},
		Scope: ScanScope{ArtifactIDs: []string{"Orders_In"}}})
	require.NoError(t, err)
	assert.Equal(t, 3, tree.Messages)
}
