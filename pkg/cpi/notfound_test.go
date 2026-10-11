package cpi_test

import (
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A 404 means "does not exist", detected by status code (not by comparing
// error texts, which broke when the message format changed).
func TestNotFoundIsNotAnError(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	_, _, exists, err := cpi.NewIntegrationPackage(mock.Executer()).Get("NoSuchPackage")
	require.NoError(t, err)
	assert.False(t, exists)

	_, _, exists, err = cpi.NewDesigntimeArtifact("Integration", mock.Executer()).Get("NoSuchFlow", "active")
	require.NoError(t, err)
	assert.False(t, exists)
}
