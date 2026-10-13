package testrun

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// End to end on the mock tenant: the receiver stub installed by
// InstallTestTools, the flow's receiver pointed at it by config.yaml
// (apply_config), the calls checked with stub_calls, and a stub error
// answer failing the flow.
func TestRunWithReceiverStub(t *testing.T) {
	m, newExe := demo(t)
	exe := m.Executer()
	_, err := ops.InstallTestTools(context.Background(), exe, ops.TestToolsOptions{Only: "stub", Deploy: true,
		DeployOptions: ops.Options{Interval: time.Millisecond, MaxChecks: 5}})
	require.NoError(t, err)
	before := m.Artifacts["Orders_Route"].Parameters["ERP_URL"]

	dir := t.TempDir()
	flow := filepath.Join(dir, "Orders_Route")
	write(t, flow, "config.yaml", "ERP_URL: "+m.URL()+"/http/cpictl/stub/erp/orders\n")
	write(t, flow, "posts-order.yaml", `description: The order goes to ERP
order: 1
request:
  process_direct_address: /orders/route
  content_type: application/xml
  body: "<Order><OrderNo>4500017003</OrderNo></Order>"
expect:
  message_status: COMPLETED
  stub_calls:
    - rule: erp
      method: POST
      path_contains: orders
      body_contains: ["<OrderNo>4500017003</OrderNo>"]
`)
	write(t, flow, "erp-down.yaml", `description: ERP answers 503, the flow fails
order: 2
request:
  process_direct_address: /orders/route
  content_type: application/xml
  body: "<Order><OrderNo>4500017004</OrderNo></Order>"
expect:
  message_status: FAILED
  stub_calls:
    - rule: erp
      body_contains: ["4500017004"]
`)
	cases, err := Load(dir, Filter{})
	require.NoError(t, err)
	require.Len(t, cases, 2)

	// the first case passes with the default answer; then ERP is down
	res, err := Run(context.Background(), exe, newExe, cases[:1], fast(Options{ApplyConfig: true}))
	require.NoError(t, err)
	require.Equal(t, 1, res.Passed, "%+v", res.Cases)

	require.NoError(t, ops.SetStubResponse(exe, "erp", ops.StubResponse{Status: 503, Body: "maintenance"}))
	res, err = Run(context.Background(), exe, newExe, cases[1:], fast(Options{ApplyConfig: true}))
	require.NoError(t, err)
	assert.Equal(t, 1, res.Passed, "%+v", res.Cases)
	assert.Equal(t, before, m.Artifacts["Orders_Route"].Parameters["ERP_URL"], "receiver restored")

	// a wrong expectation on the stub call fails with the calls seen
	write(t, flow, "wrong.yaml", `request:
  process_direct_address: /orders/route
  body: "<Order><OrderNo>1</OrderNo></Order>"
expect:
  message_status: FAILED
  stub_calls:
    - rule: crm
`)
	cases, err = Load(dir, Filter{Cases: []string{"wrong"}})
	require.NoError(t, err)
	res, err = Run(context.Background(), exe, newExe, cases, fast(Options{ApplyConfig: true}))
	require.NoError(t, err)
	require.Equal(t, 1, res.Failed, "%+v", res.Cases)
	assert.Equal(t, "stub_calls[0]", res.Cases[0].Failures[0].Expectation)

	// a case on the inbound flow: the receiver is in the flow it calls over
	// ProcessDirect, configured through the flows: section
	in := filepath.Join(dir, "Orders_In")
	write(t, in, "config.yaml", "flows:\n  Orders_Route:\n    ERP_URL: "+m.URL()+"/http/cpictl/stub/erp/orders\n")
	write(t, in, "end-to-end.yaml", `request:
  content_type: application/xml
  body: "<Order><OrderNo>4500017005</OrderNo></Order>"
expect:
  message_status: COMPLETED
  stub_calls:
    - rule: erp
      body_contains: ["4500017005"]
`)
	require.NoError(t, ops.ClearStubResponse(exe, "erp"))
	cases, err = Load(dir, Filter{Flows: []string{"Orders_In"}})
	require.NoError(t, err)
	require.Len(t, cases, 1)
	assert.Equal(t, map[string][]string{"Orders_Route": {"ERP_URL"}}, List(cases).Flows[0].ConfigFlows)
	res, err = Run(context.Background(), exe, newExe, cases, fast(Options{ApplyConfig: true}))
	require.NoError(t, err)
	assert.Equal(t, 1, res.Passed, "%+v", res.Cases)
	require.Len(t, res.Config, 1)
	assert.Equal(t, "Orders_Route", res.Config[0].Flow)
	assert.Equal(t, []string{"ERP_URL"}, res.Config[0].Restored)
	assert.Equal(t, before, m.Artifacts["Orders_Route"].Parameters["ERP_URL"], "downstream receiver restored")
}
