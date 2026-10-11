package ops

import (
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListAndUnlock(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(mock, "dev", time.Now()))
	exe := mock.Executer()

	locks, err := ListLocks(exe, "", "")
	require.NoError(t, err)
	require.Len(t, locks, 1)
	l := locks[0]
	assert.Equal(t, "Billing_Post", l.ArtifactID)
	assert.Equal(t, "Billing", l.PackageID)
	assert.Equal(t, "dev.user@customer.example", l.CreatedBy)
	assert.Equal(t, "INTEGRATION_FLOW", l.ArtifactType)
	require.NotNil(t, l.CreatedAt)
	assert.WithinDuration(t, time.Now().Add(-25*time.Minute), *l.CreatedAt, time.Minute)

	locks, err = ListLocks(exe, "", "Orders")
	require.NoError(t, err)
	assert.Empty(t, locks)

	res, err := Unlock(exe, "Billing_Post")
	require.NoError(t, err)
	require.Len(t, res.Removed, 1)
	assert.Contains(t, mock.Requests(), "DELETE /api/v1/IntegrationDesigntimeLocks('"+l.ResourceID+"')")

	_, err = Unlock(exe, "Billing_Post")
	assert.ErrorContains(t, err, "Billing_Post is not locked")
	_, err = Unlock(exe, "")
	assert.Error(t, err)
}
