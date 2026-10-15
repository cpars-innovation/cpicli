package ops

import (
	"context"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetLogLevel(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"Oauth_PoC": {Type: "Integration", DesignVersion: "1"}})

	res, err := SetLogLevel(mock.Executer(), LogLevelRequest{ArtifactID: "Oauth_PoC", Level: "trace"})
	require.NoError(t, err)
	assert.Equal(t, "TRACE", res.Level)
	require.NotNil(t, res.Until)
	assert.WithinDuration(t, time.Now().Add(TraceDuration), *res.Until, time.Minute)
	assert.Equal(t, map[string]string{"artifactSymbolicName": "Oauth_PoC", "mplLogLevel": "TRACE", "nodeType": "IFLMAP", "runtimeLocationId": "cloudintegration"},
		mock.LogLevels["Oauth_PoC"])

	res, err = SetLogLevel(mock.Executer(), LogLevelRequest{ArtifactID: "Oauth_PoC", Level: "INFO"})
	require.NoError(t, err)
	assert.Nil(t, res.Until)

	_, err = SetLogLevel(mock.Executer(), LogLevelRequest{ArtifactID: "Oauth_PoC", Level: "VERBOSE"})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
	_, err = SetLogLevel(mock.Executer(), LogLevelRequest{ArtifactID: "Unknown", Level: "INFO"})
	assert.Equal(t, exitcode.TenantHTTP, output.ExitCode(err))
}

func TestMessageTrace(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.MessageLogSteps = [][]cpitest.MessageLog{{{Guid: "G1", Artifact: "A", Status: "FAILED", Steps: []cpitest.Step{
		{StepID: "s0", ModelStepID: "StartEvent_1", Activity: "Start"},
		{StepID: "s1", ModelStepID: "CallActivity_2", Activity: "Mapping", Status: "FAILED", Traces: []cpitest.Trace{{
			ID: "42", Payload: "<in/>", Headers: map[string]string{"Authorization": "Bearer x", "OrderId": "7"},
			ExchangeProperties: map[string]string{"SAP_MessageProcessingLogID": "G1", "clientSecret": "s"},
		}}},
	}}}}

	tr, err := GetMessageTrace(mock.Executer(), "G1", "")
	require.NoError(t, err)
	require.Len(t, tr.Steps, 1)
	assert.Equal(t, "CallActivity_2", tr.Steps[0].ModelStepID)
	assert.Equal(t, "42", tr.Steps[0].Traces[0].TraceID)
	assert.EqualValues(t, 5, tr.Steps[0].Traces[0].PayloadSize)
	assert.Empty(t, tr.Hint)

	tr, err = GetMessageTrace(mock.Executer(), "G1", "StartEvent_1")
	require.NoError(t, err)
	assert.Empty(t, tr.Steps)
	assert.Contains(t, tr.Hint, "TRACE")

	d, err := GetTraceMessage(mock.Executer(), "42", 0)
	require.NoError(t, err)
	assert.Equal(t, "<in/>", d.Payload.Text)
	assert.Contains(t, d.Headers, nameValue("Authorization", "***"))
	assert.Contains(t, d.Headers, nameValue("OrderId", "7"))
	assert.Contains(t, d.ExchangeProperties, nameValue("clientSecret", "***"))

	_, err = GetTraceMessage(mock.Executer(), "42) or (1", 0)
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
}

func TestSendThroughHarness(t *testing.T) {
	mock, _, newExe := endpointMock(t, &cpitest.Inbound{MessageGuid: "H1"})
	// the harness is the flow with the HTTP endpoint
	mock.Artifacts[DefaultHarnessID] = mock.Artifacts["Orders"]
	delete(mock.Artifacts, "Orders")
	mock.MessageLogSteps = [][]cpitest.MessageLog{{
		{Guid: "T1", Artifact: "Billing", Status: "COMPLETED", CorrelationID: "C1"},
		{Guid: "H1", Artifact: DefaultHarnessID, Status: "COMPLETED", CorrelationID: "C1"},
	}}
	sent, err := SendTestMessage(context.Background(), mock.Executer(), newExe, TestMessage{
		ArtifactID: "Billing", ProcessDirectAddress: "/billing/in", Body: []byte("x"), Wait: time.Second, PollInterval: time.Millisecond,
	})
	require.NoError(t, err)
	assert.Equal(t, "H1", sent.HarnessMessageGuid)
	assert.Equal(t, "T1", sent.MessageGuid)
	assert.Equal(t, "COMPLETED", sent.Log.Status)
	assert.Equal(t, "/billing/in", mock.Received[0].Header.Get(HarnessAddressHeader))
	assert.Contains(t, mock.LastMessageLogQuery, "CorrelationId")

	_, err = SendTestMessage(context.Background(), mock.Executer(), newExe, TestMessage{ArtifactID: "Billing", ProcessDirectAddress: "/x", Harness: "Missing"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "test harness Missing")
	_, err = SendTestMessage(context.Background(), mock.Executer(), newExe, TestMessage{ArtifactID: "Billing", ProcessDirectAddress: "billing"})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
}

// The harness answers without message IDs (#20): its own log is found by
// time; when the flow under test never logs, the error says that the harness
// did not reach the address.
func TestSendThroughHarnessWithoutIDs(t *testing.T) {
	mock, _, newExe := endpointMock(t, &cpitest.Inbound{})
	mock.FilterMessageLogs = true // filter by artifact, correlation ID and time like a tenant
	mock.Artifacts[DefaultHarnessID] = mock.Artifacts["Orders"]
	delete(mock.Artifacts, "Orders")
	now := time.Now()
	harness := cpitest.MessageLog{Guid: "H1", Artifact: DefaultHarnessID, Status: "COMPLETED", CorrelationID: "C1", Start: now, End: now}
	mock.MessageLogSteps = [][]cpitest.MessageLog{{harness, {Guid: "T1", Artifact: "Billing", Status: "COMPLETED", CorrelationID: "C1", Start: now, End: now}}}
	sent, err := SendTestMessage(context.Background(), mock.Executer(), newExe, TestMessage{
		ArtifactID: "Billing", ProcessDirectAddress: "/billing/in", Body: []byte("x"), Wait: time.Second, PollInterval: time.Millisecond,
	})
	require.NoError(t, err)
	assert.Equal(t, "H1", sent.HarnessMessageGuid, "found by time")
	assert.Equal(t, "T1", sent.MessageGuid)

	mock.MessageLogSteps = [][]cpitest.MessageLog{{harness}}
	_, err = SendTestMessage(context.Background(), mock.Executer(), newExe, TestMessage{
		ArtifactID: "Billing", ProcessDirectAddress: "/billing/in", Body: []byte("x"), Wait: 50 * time.Millisecond, PollInterval: time.Millisecond,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the test harness completed (message H1) but Billing has no message with its correlation ID C1")
	assert.Contains(t, err.Error(), "did not reach ProcessDirect address /billing/in")
	assert.Contains(t, err.Error(), "Check that Billing is deployed and STARTED", "no harness runtime known")

	// a harness from before 1.2.0 skipped its process on a tenant: the cause
	mock.Artifacts[DefaultHarnessID].Runtime = &cpitest.Runtime{Version: "1.1.0", Status: "STARTED"}
	_, err = SendTestMessage(context.Background(), mock.Executer(), newExe, TestMessage{
		ArtifactID: "Billing", ProcessDirectAddress: "/billing/in", Body: []byte("x"), Wait: 50 * time.Millisecond, PollInterval: time.Millisecond,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "The deployed harness CPICTL_Test_Harness is version 1.1.0: harness versions before 1.2.0 skip their process")
	assert.Contains(t, err.Error(), "cpictl test tools install --deploy")
}

func nameValue(n, v string) cpi.NameValue { return cpi.NameValue{Name: n, Value: v} }

func TestTraceMissingRole(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.ForbidTraces = true
	mock.MessageLogSteps = [][]cpitest.MessageLog{{{Guid: "G1", Artifact: "A", Status: "FAILED", Steps: []cpitest.Step{{StepID: "s0", ModelStepID: "CallActivity_1"}}}}}

	tr, err := GetMessageTrace(mock.Executer(), "G1", "")
	require.NoError(t, err, "a missing role is not an error the agent has to stop for")
	assert.Equal(t, TraceMissingRole, tr.Status)
	assert.Contains(t, tr.Hint, "role")

	d, err := GetTraceMessage(mock.Executer(), "42", 0)
	require.NoError(t, err)
	assert.Equal(t, TraceMissingRole, d.Status)
}
