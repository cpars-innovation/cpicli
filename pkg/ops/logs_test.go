package ops

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mplMock(t *testing.T, steps ...[]cpitest.MessageLog) *cpitest.Tenant {
	m := cpitest.NewTenant(t, nil)
	m.MessageLogSteps = steps
	return m
}

func TestQueryMessageLogs(t *testing.T) {
	start := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	m := mplMock(t, []cpitest.MessageLog{
		{Guid: "g2", Artifact: "A", Status: "FAILED", Start: start, End: start.Add(1500 * time.Millisecond), ErrorText: "com.sap.it.rt.adapter.http.api.exception.HttpResponseException: 500"},
		{Guid: "g1", Artifact: "A", Status: "COMPLETED", Start: start, End: start.Add(time.Second)},
	})
	res, err := QueryMessageLogs(m.Executer(), MessageLogQuery{ArtifactID: "A", Since: start, Top: 10, IncludeErrors: true, MaxErrorBytes: 20})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Total)
	require.Len(t, res.Logs, 2)
	assert.Equal(t, "FAILED", res.Logs[0].Status)
	assert.Equal(t, int64(1500), res.Logs[0].DurationMs)
	assert.Equal(t, "com.sap.it.rt.adapte", res.Logs[0].ErrorText)
	assert.True(t, res.Logs[0].ErrorTruncated)
	assert.Empty(t, res.Logs[1].ErrorText, "no error lookup for completed messages")

	q, err := url.ParseQuery(m.LastMessageLogQuery)
	require.NoError(t, err)
	assert.Equal(t, "IntegrationFlowName eq 'A' and LogEnd ge datetime'2026-10-04T10:00:00.000'", q.Get("$filter"))
	assert.Equal(t, "LogEnd desc", q.Get("$orderby"))
	assert.Equal(t, "10", q.Get("$top"))
	assert.Equal(t, 1, m.Count("GET /api/v1/MessageProcessingLogs('g2')/ErrorInformation"))
}

func TestQueryMessageLogsValidation(t *testing.T) {
	m := mplMock(t)
	_, err := QueryMessageLogs(m.Executer(), MessageLogQuery{Statuses: []string{"BROKEN"}})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
	_, err = QueryMessageLogs(m.Executer(), MessageLogQuery{Top: MaxMessageLogs + 1})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
	assert.Empty(t, m.Requests())
}

func TestWaitForMessageLogs(t *testing.T) {
	now := time.Now()
	t.Run("returns once all messages are final", func(t *testing.T) {
		m := mplMock(t,
			nil, // nothing yet
			[]cpitest.MessageLog{{Guid: "g1", Artifact: "A", Status: "PROCESSING", Start: now}},
			[]cpitest.MessageLog{{Guid: "g1", Artifact: "A", Status: "FAILED", Start: now, End: now, ErrorText: "boom"}},
		)
		res, err := WaitForMessageLogs(context.Background(), m.Executer(), MessageLogQuery{ArtifactID: "A", IncludeErrors: true}, time.Second, time.Millisecond)
		require.NoError(t, err)
		require.Len(t, res.Logs, 1)
		assert.Equal(t, "FAILED", res.Logs[0].Status)
		assert.Equal(t, "boom", res.Logs[0].ErrorText)
		queries := 0
		for _, r := range m.Requests() {
			if r == "GET /api/v1/MessageProcessingLogs" {
				queries++
			}
		}
		assert.Equal(t, 3, queries, "polled until the message was final")
	})

	t.Run("times out while still processing", func(t *testing.T) {
		m := mplMock(t, []cpitest.MessageLog{{Guid: "g1", Artifact: "A", Status: "PROCESSING", Start: now}})
		res, err := WaitForMessageLogs(context.Background(), m.Executer(), MessageLogQuery{ArtifactID: "A"}, 20*time.Millisecond, 5*time.Millisecond)
		assert.Equal(t, exitcode.Timeout, output.ExitCode(err))
		require.NotNil(t, res)
		assert.Len(t, res.Logs, 1)
	})
}

func TestGetMessageLog(t *testing.T) {
	now := time.Now()
	m := mplMock(t, []cpitest.MessageLog{{Guid: "g1", Artifact: "A", Status: "FAILED", Start: now, End: now,
		ErrorText: "Mapping failed", Headers: map[string]string{"OrderId": "4711"}, Attachments: map[string]string{"payload.xml": "<order/>"}, StoreEntries: map[string]string{"store-1": "<persisted/>"},
		Steps: []cpitest.Step{{StepID: "s1", ModelStepID: "CallActivity_1", Activity: "Script", Status: "COMPLETED"}, {StepID: "s2", ModelStepID: "MessageMapping_2", Activity: "Mapping", Status: "FAILED", Error: "Mapping failed"}}}})
	d, err := GetMessageLog(m.Executer(), "g1", 0)
	require.NoError(t, err)
	assert.Equal(t, "Mapping failed", d.ErrorText)
	assert.Equal(t, "4711", d.CustomHeaderProperties[0].Value)
	assert.Equal(t, "payload.xml", d.Attachments[0].Name)
	assert.Equal(t, "HTTPS", d.AdapterAttributes[0].Adapter)
	require.NotNil(t, d.ErrorInformation)
	assert.Equal(t, "MessageMapping_2", d.ErrorInformation.LastErrorModelStepID, "where it failed, from MessageProcessingLogs('g1')/ErrorInformation")

	_, err = GetMessageLog(m.Executer(), "nope", 0)
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
}

func TestMessageLogDetailsAndContent(t *testing.T) {
	now := time.Now()
	m := mplMock(t, []cpitest.MessageLog{{Guid: "g1", Artifact: "A", Status: "FAILED", Start: now, End: now,
		Attachments: map[string]string{"payload.xml": "<order/>"}, StoreEntries: map[string]string{"store-1": "<persisted/>"},
		Steps: []cpitest.Step{{StepID: "s1", ModelStepID: "CallActivity_1", Activity: "Script", Status: "COMPLETED"},
			{StepID: "s2", ModelStepID: "MessageMapping_2", Activity: "Mapping", Status: "FAILED", Error: "Mapping failed"}}}})

	d, err := GetMessageLog(m.Executer(), "g1", 0)
	require.NoError(t, err)
	require.Len(t, d.MessageStoreEntries, 1)
	assert.Empty(t, d.Warnings)

	att, err := GetMessageAttachment(m.Executer(), d.Attachments[0].ID, 0)
	require.NoError(t, err)
	assert.Equal(t, "<order/>", att.Text)

	entry, err := GetMessageStoreEntry(m.Executer(), "store-1", 0)
	require.NoError(t, err)
	assert.Equal(t, "<persisted/>", entry.Text)

	_, err = GetMessageAttachment(m.Executer(), "nope", 0)
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))

	steps, err := GetMessageSteps(m.Executer(), "g1")
	require.NoError(t, err)
	require.Len(t, steps.Runs, 1)
	assert.Len(t, steps.Runs[0].Steps, 2)
	require.NotNil(t, steps.FailedStep)
	assert.Equal(t, "MessageMapping_2", steps.FailedStep.ModelStepID)
}

func TestNewContent(t *testing.T) {
	c := NewContent([]byte("héllo"), 2) // cut inside the 2-byte é
	assert.Equal(t, "h", c.Text)
	assert.True(t, c.Truncated)
	assert.Equal(t, 6, c.Size)

	bin := NewContent([]byte{0xff, 0x00, 0xfe}, 0)
	assert.Empty(t, bin.Text)
	assert.Equal(t, "/wD+", bin.Base64)

	assert.False(t, NewContent([]byte("abc"), -1).Truncated)
}

func TestMessageStepProperties(t *testing.T) {
	now := time.Now()
	m := mplMock(t, []cpitest.MessageLog{{Guid: "g1", Artifact: "A", Status: "COMPLETED", Start: now, End: now,
		Steps: []cpitest.Step{{StepID: "s1", ModelStepID: "CallActivity_1", Activity: "Request Reply", Status: "COMPLETED",
			Properties: map[string]string{"Address": "https://erp.example.com/orders"}}}}})

	plain, err := GetMessageSteps(m.Executer(), "g1")
	require.NoError(t, err)
	assert.Nil(t, plain.Runs[0].Steps[0].Properties)

	res, err := GetMessageStepsWith(m.Executer(), "g1", true)
	require.NoError(t, err)
	assert.Equal(t, []cpi.NameValue{{Name: "Address", Value: "https://erp.example.com/orders"}}, res.Runs[0].Steps[0].Properties)
	assert.Contains(t, m.Requests(), "GET /api/v1/MessageProcessingLogRuns('run-g1')/RunSteps")
}
