package ops

import (
	"errors"
	"fmt"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
)

// Errors of real calls classify without string matching.
func TestErrorCategory(t *testing.T) {
	assert.Equal(t, "", ErrorCategory(nil))
	assert.Equal(t, ExitOK, ExitCode(nil))

	mock := cpitest.NewTenant(t, nil)
	_, err := Unlock(mock.Executer(), "")
	assert.Equal(t, CategoryUsage, ErrorCategory(err))
	assert.Equal(t, ExitUsage, ExitCode(fmt.Errorf("wrapped: %w", err)), "wrapping keeps the class")

	mock.StatusOverride = 401
	_, err = ListLocks(mock.Executer(), "", "")
	assert.Equal(t, CategoryAuth, ErrorCategory(err))

	mock.StatusOverride = 500
	_, err = ListLocks(mock.Executer(), "", "")
	assert.Equal(t, CategoryTenantHTTP, ErrorCategory(err))
	assert.Equal(t, ExitTenantHTTP, ExitCode(err))

	mock.StatusOverride = 0
	mock.Artifacts = map[string]*cpitest.Artifact{"Bad": {Type: "Integration", DesignVersion: "1", ValidationResult: "Check execution result: Failed"}}
	_, err = ValidateArtifact(mock.Executer(), "Bad", "active")
	assert.Equal(t, CategoryFailed, ErrorCategory(err))

	assert.Equal(t, CategoryTimeout, ErrorCategory(&TimeoutError{Msg: "x"}))
	assert.Equal(t, CategoryError, ErrorCategory(errors.New("anything else")))
}
