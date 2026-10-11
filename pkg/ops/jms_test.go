package ops

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func jmsMock(t *testing.T) *cpitest.Tenant {
	mock := cpitest.NewTenant(t, nil)
	created := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	mock.Queues = map[string]*cpitest.JMSQueue{
		"Orders_Billing": {Name: "Orders_Billing", Messages: []cpitest.JMSMessage{
			{ID: "ID:1", Failed: true, MplID: "M1", CreatedAt: created, RetryCount: 3, NextRetry: created.Add(time.Hour)},
			{ID: "ID:2", CreatedAt: created},
			{ID: "ID:3", Failed: true, MplID: "M3", CreatedAt: created},
		}},
		"Orders_Billing_Error": {Name: "Orders_Billing_Error", Messages: []cpitest.JMSMessage{}},
	}
	return mock
}

func TestListQueueMessages(t *testing.T) {
	mock := jmsMock(t)
	exe := mock.Executer()

	res, err := ListQueueMessages(exe, "Orders_Billing", false, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, res.Total)
	assert.Equal(t, 2, res.Failed)
	m := res.Messages[0]
	assert.Equal(t, "ID:1", m.ID)
	assert.Equal(t, "Orders_Billing", m.Queue)
	assert.True(t, m.Failed)
	assert.Equal(t, "M1", m.MessageGuid)
	assert.Equal(t, int64(3), m.RetryCount)
	require.NotNil(t, m.CreatedAt)
	assert.Equal(t, time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC), m.CreatedAt.UTC())
	require.NotNil(t, m.NextRetry)
	assert.Contains(t, mock.Requests(), "GET /api/v1/MessagingQueues('Orders_Billing')/MessagingMessages")

	res, err = ListQueueMessages(exe, "Orders_Billing", true, 1)
	require.NoError(t, err)
	assert.Equal(t, 2, res.Total)
	assert.True(t, res.Truncated)
	assert.Len(t, res.Messages, 1)

	_, err = ListQueueMessages(exe, "Orders_Bill", false, 0)
	assert.Equal(t, exitcode.Usage, output.ExitCode(err), "an unknown queue lists nothing on the tenant: a typo is a usage error")
}

func TestRetryMoveDeleteQueueMessages(t *testing.T) {
	mock := jmsMock(t)
	exe := mock.Executer()

	res, err := RetryQueueMessages(exe, JMSSelection{Queue: "Orders_Billing", All: true, FailedOnly: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"ID:1", "ID:3"}, res.MessageIDs)
	assert.Equal(t, "2 message(s) processed", res.Response)
	assert.Equal(t, 4, mock.Queues["Orders_Billing"].Messages[0].RetryCount)

	_, err = RetryQueueMessages(exe, JMSSelection{Queue: "Orders_Billing", IDs: []string{"ID:9"}})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err), "unknown message")
	_, err = RetryQueueMessages(exe, JMSSelection{Queue: "Orders_Billing"})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err), "ids or all")
	_, err = RetryQueueMessages(exe, JMSSelection{Queue: "Orders_Billing", IDs: []string{"ID:1"}, All: true})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err), "not both")

	res, err = MoveQueueMessages(exe, JMSSelection{Queue: "Orders_Billing", IDs: []string{"ID:3"}}, "Orders_Billing_Error")
	require.NoError(t, err)
	assert.Equal(t, "Orders_Billing_Error", res.TargetQueue)
	assert.Len(t, mock.Queues["Orders_Billing"].Messages, 2)
	assert.Equal(t, "ID:3", mock.Queues["Orders_Billing_Error"].Messages[0].ID)
	_, err = MoveQueueMessages(exe, JMSSelection{Queue: "Orders_Billing", IDs: []string{"ID:1"}}, "Nowhere")
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
	_, err = MoveQueueMessages(exe, JMSSelection{Queue: "Orders_Billing", IDs: []string{"ID:1"}}, "Orders_Billing")
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))

	res, err = DeleteQueueMessages(exe, JMSSelection{Queue: "Orders_Billing", IDs: []string{"ID:2"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"ID:2"}, res.MessageIDs)
	assert.Contains(t, mock.Requests(), "DELETE /api/v1/MessagingMessages(jmsMessageId='ID:2',queueName='Orders_Billing')")
	assert.Len(t, mock.Queues["Orders_Billing"].Messages, 1)
}

// The request bodies of the function imports (format not confirmed on a
// tenant: keep it in one place).
func TestJMSRequestBodies(t *testing.T) {
	b, _ := json.Marshal(cpi.QueueMessagesBody{QueueName: "Q", JmsMessageIDs: []string{"ID:1"}})
	assert.JSONEq(t, `{"queueName":"Q","jmsMessageIds":["ID:1"]}`, string(b))
	b, _ = json.Marshal(cpi.MoveMessagesBody{QueueName: "Q", TargetQueueName: "T", JmsMessageIDs: []string{"ID:1"}})
	assert.JSONEq(t, `{"queueName":"Q","targetQueueName":"T","jmsMessageIds":["ID:1"]}`, string(b))
}

func TestSetQueueState(t *testing.T) {
	mock := jmsMock(t)
	res, err := SetQueueState(mock.Executer(), "Orders_Billing", false)
	require.NoError(t, err)
	assert.False(t, res.Active)
	assert.True(t, mock.Queues["Orders_Billing"].Inactive)
	queues, err := ListQueues(mock.Executer(), "Orders_Billing")
	require.NoError(t, err)
	assert.Equal(t, "Orders_Billing", queues[0].Name, "fullest first")
	assert.False(t, queues[0].Active)

	_, err = SetQueueState(mock.Executer(), "Orders_Billing", true)
	require.NoError(t, err)
	assert.False(t, mock.Queues["Orders_Billing"].Inactive)
	assert.Contains(t, mock.Requests(), "POST /api/v1/activateQueue")
}

func TestQueueFlows(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.Raw = map[string]any{
		"/api/v1/MessagingQueues": []map[string]any{{"queueName": "Orders_Billing", "numberOfMessages": "1", "active": true}},
		"/api/v1/Queues": []map[string]any{{"Name": "Orders_Billing", "Artifacts": map[string]any{"results": []map[string]any{
			{"Name": "Billing_In", "Id": "Billing_In", "Direction": "0"}}}}},
	}
	queues, err := ListQueues(mock.Executer(), "")
	require.NoError(t, err)
	assert.Equal(t, []cpi.QueueFlow{{Name: "Billing_In", ID: "Billing_In"}}, queues[0].Flows)
	assert.Equal(t, "$expand=Artifacts", mock.RawQueries["/api/v1/Queues"])
}

// The demo: failed postings leave Billing_In's message in its queue.
func TestDemoQueueHasRetries(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(mock, "dev", time.Now()))
	queues, err := ListQueues(mock.Executer(), "")
	require.NoError(t, err)
	require.Len(t, queues, 1)
	assert.Equal(t, "Orders_Billing", queues[0].Name)
	assert.Positive(t, queues[0].Messages)
	res, err := ListQueueMessages(mock.Executer(), "Orders_Billing", true, 0)
	require.NoError(t, err)
	require.NotEmpty(t, res.Messages)
	d, err := GetMessageLog(mock.Executer(), res.Messages[0].MessageGuid, 0)
	require.NoError(t, err)
	assert.Equal(t, "RETRY", d.Status)
	assert.Equal(t, "Billing_In", d.ArtifactID)
}
