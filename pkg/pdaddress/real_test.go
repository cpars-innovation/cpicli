package pdaddress_test

import (
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/pdaddress"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The demo flows (which pass a tenant's Check) are read as they are.
func TestDemoArchives(t *testing.T) {
	m := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(m, "dev", time.Now()))
	n := 0
	for id, a := range m.Artifacts {
		if a.Type != "Integration" || len(a.Zip) == 0 {
			continue
		}
		addrs, err := pdaddress.FromArchive(a.Zip)
		require.NoError(t, err, id)
		if id == "Billing_Post" {
			assert.Equal(t, []string{"/billing/post"}, addrs)
		}
		n++
	}
	assert.Positive(t, n)
}
