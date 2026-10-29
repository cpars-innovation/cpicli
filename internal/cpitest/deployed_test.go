package cpitest

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A deployment records the addresses the flow listens on: a later change of
// the designtime artifact takes effect only with the next deployment, as on
// a tenant (cpicli#65; the harness of the mock hid it).
func TestProcessDirectFollowsTheDeployment(t *testing.T) {
	m := NewTenant(t, nil)
	m.Live = true
	require.NoError(t, SeedDemo(m, "dev", time.Now()))
	a := m.Artifacts["Billing_Post"]
	require.NotNil(t, a.Runtime)
	a.Runtime.Senders = deployedSenders(a) // as a deployment records them
	x := m.newExecutor("LIVE")
	id, _ := x.flowBySender("ProcessDirect", "/billing/post")
	require.Equal(t, "Billing_Post", id)

	// the designtime artifact moves to another address, not deployed
	a.applyContent(tenantSerialised(rewriteZip(t, a.Zip, map[string]string{"/billing/post": "/billing/new"}, "")))
	id, _ = x.flowBySender("ProcessDirect", "/billing/post")
	assert.Equal(t, "Billing_Post", id, "the running content still listens on the old address")
	id, _ = x.flowBySender("ProcessDirect", "/billing/new")
	assert.Empty(t, id, "the new address only after a deployment")

	a.Runtime.Senders = deployedSenders(a) // deployed
	id, _ = x.flowBySender("ProcessDirect", "/billing/new")
	assert.Equal(t, "Billing_Post", id)
}
