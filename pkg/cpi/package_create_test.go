package cpi_test

import (
	"strings"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreatePackageWithoutShortText(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.NoCSRF = true
	ip := cpi.NewIntegrationPackage(mock.Executer())

	var data cpi.PackageSingleData
	data.Root.Id, data.Root.Name = "ZZ_Orders", "Orders"
	require.NoError(t, ip.Create(&data), "the missing short text is filled in")
	require.Len(t, mock.Packages, 1)
	assert.Equal(t, "Orders", mock.Packages[0].ShortText)
}

func TestMockRejectsEmptyShortText(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.NoCSRF = true
	resp, err := mock.Executer().Exec("POST", "/api/v1/IntegrationPackages",
		strings.NewReader(`{"d":{"Id":"X","Name":"X","ShortText":""}}`), map[string]string{"Content-Type": "application/json"})
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode)
}
