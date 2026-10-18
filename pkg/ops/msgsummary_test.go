package ops

import (
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorFingerprint(t *testing.T) {
	a := ErrorFingerprint("Orders", "", "Message AFq478Bblxi4wCjBcDb_G0vAGGZG failed at 2026-10-05T08:00:01.123Z, order 1234567")
	b := ErrorFingerprint("Orders", "", "Message AGr123Bblxi4wCjBcDb_G0vAZZZZ failed at 2026-10-05T09:12:44Z, order 7654321")
	assert.Equal(t, a, b)
	assert.NotEqual(t, a, ErrorFingerprint("Billing", "", "Message AGr123Bblxi4wCjBcDb_G0vAZZZZ failed at 2026-10-05T09:12:44Z, order 7654321"))
}

func TestSummarizeMessages(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	at := func(min int) time.Time { return now.Add(time.Duration(-min) * time.Minute) }
	logs := []cpitest.MessageLog{
		{Guid: "m1", Artifact: "Orders", Status: "COMPLETED", CorrelationID: "c1", Start: at(30), End: at(30).Add(2 * time.Second)},
		{Guid: "m2", Artifact: "Billing", Status: "COMPLETED", CorrelationID: "c1", Predecessor: "m1", Start: at(30), End: at(30).Add(time.Second)},
		{Guid: "m3", Artifact: "Orders", Status: "COMPLETED", CorrelationID: "c2", Start: at(20), End: at(20).Add(4 * time.Second)},
		{Guid: "m4", Artifact: "Billing", Status: "FAILED", CorrelationID: "c2", Predecessor: "m3", Start: at(20), End: at(20).Add(time.Second),
			ErrorText: "Connection refused to billing.example.com at 2026-10-05T08:00:01Z, order 1234567"},
		{Guid: "m5", Artifact: "Billing", Status: "FAILED", CorrelationID: "c3", Start: at(10), End: at(10).Add(time.Second),
			ErrorText: "Connection refused to billing.example.com at 2026-10-05T09:00:01Z, order 7654321"},
		{Guid: "m8", Artifact: "Billing", Status: "FAILED", CorrelationID: "c5", Start: at(8), End: at(8).Add(time.Second),
			ErrorText: "Connection refused to billing.example.com at 2026-10-05T09:02:01Z, order 7654322",
			Steps:     []cpitest.Step{{StepID: "s1", ModelStepID: "CallActivity_7", Status: "FAILED", Error: "refused"}}},
		{Guid: "m6", Artifact: "Stock", Status: "COMPLETED", CorrelationID: "c4", Start: at(5), End: at(5).Add(time.Second)},
		{Guid: "m7", Artifact: "Invoices", Status: "COMPLETED", CorrelationID: "c4", Start: at(5), End: at(5).Add(time.Second)},
	}
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{})
	mock.MessageLogSteps = [][]cpitest.MessageLog{logs}
	mock.FilterMessageLogs = true
	g := &Graph{Edges: []GraphEdge{{From: "iflow:Stock", To: "iflow:Invoices", Type: EdgeSendsTo, Via: "endpoint:processdirect:/invoices"}}}

	s, err := SummarizeMessages(mock.Executer(), MessageSummaryQuery{Since: at(60), Until: now, Graph: g})
	require.NoError(t, err)
	assert.Equal(t, 8, s.Scanned)
	assert.False(t, s.Truncated)
	require.NotEmpty(t, s.Flows)
	assert.Equal(t, "Billing", s.Flows[0].Artifact, "most failures first")
	assert.Equal(t, 3, s.Flows[0].Failed)
	assert.Equal(t, map[string]int{"COMPLETED": 1, "FAILED": 3}, s.Flows[0].ByStatus)
	var orders FlowSummary
	for _, f := range s.Flows {
		if f.Artifact == "Orders" {
			orders = f
		}
	}
	assert.Equal(t, int64(3000), orders.AvgDurationMs)
	assert.Equal(t, int64(4000), orders.MaxDurationMs)

	assert.Equal(t, []EdgeSummary{
		{From: "Orders", To: "Billing", Messages: 2, Failed: 1, Source: "predecessor"},
		{From: "Stock", To: "Invoices", Messages: 1, Source: "correlation", Via: "processdirect:/invoices"},
	}, s.Edges)

	require.Len(t, s.Errors, 2, "the same text at another step is another group")
	e := s.Errors[0]
	assert.Equal(t, 2, e.Count)
	assert.Equal(t, "Billing", e.Artifact)
	assert.Empty(t, e.Step)
	assert.Contains(t, e.Sample, "Connection refused")
	assert.Equal(t, "m5", e.MessageGuid, "the newest")
	assert.Equal(t, []ErrorOccurrence{{at(10), "m5"}, {at(20), "m4"}}, e.Occurrences)
	assert.Equal(t, int64(150), s.BucketSeconds, "60 minutes in 24 buckets")
	require.Len(t, e.Buckets, 24)
	assert.Equal(t, 1, e.Buckets[16], "40 minutes after since")
	assert.Equal(t, 1, e.Buckets[20], "50 minutes after since")
	assert.Equal(t, 2, sum(e.Buckets))
	assert.Equal(t, "CallActivity_7", s.Errors[1].Step)
	assert.Equal(t, ErrorFingerprint("Billing", "CallActivity_7", "Connection refused to billing.example.com at 2026-01-01T00:00:00Z, order 9999999"), s.Errors[1].Fingerprint)

	s, err = SummarizeMessages(mock.Executer(), MessageSummaryQuery{Since: at(60), Until: now, Artifacts: []string{"Orders"}, ErrorSamples: 1})
	require.NoError(t, err)
	assert.Equal(t, 2, s.Scanned)

	_, err = SummarizeMessages(mock.Executer(), MessageSummaryQuery{Since: now, Until: at(5)})
	assert.Error(t, err)
}

func sum(xs []int) int {
	n := 0
	for _, x := range xs {
		n += x
	}
	return n
}
