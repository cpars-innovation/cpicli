package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/testtools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTestToolsCommands(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.Live, mock.FilterMessageLogs = true, true

	_, _, err := runCLI(t, mock, "test", "tools", "install", "--deploy", "--role", "ACME.Test", "--delay-length", "0", "--max-check-limit", "5")
	require.NoError(t, err)
	require.Len(t, mock.Packages, 1)
	assert.Equal(t, testtools.PackageID, mock.Packages[0].ID)
	for _, ta := range testtools.Artifacts() {
		a := mock.Artifacts[ta.ID]
		require.NotNil(t, a, ta.ID)
		require.NotNil(t, a.Runtime, ta.ID)
		assert.Equal(t, "STARTED", a.Runtime.Status, ta.ID)
		assert.Equal(t, ta.Version, a.Runtime.Version, ta.ID)
		assert.Equal(t, "ACME.Test", a.Parameters[ta.RoleParameter], ta.ID)
	}
	_, _, err = runCLI(t, mock, "test", "tools", "install", "--role", "not a role")
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))

	_, _, err = runCLI(t, mock, "test", "tools", "status")
	require.NoError(t, err)

	body := filepath.Join(t.TempDir(), "answer.json")
	require.NoError(t, os.WriteFile(body, []byte(`{"error":"down"}`), 0o600))
	_, _, err = runCLI(t, mock, "test", "stub", "set", "erp", "--status", "500", "--body-file", body, "--header", "X-Reason=test")
	require.NoError(t, err)
	assert.JSONEq(t, `{"status":500,"contentType":"application/json","body":"{\"error\":\"down\"}","headers":{"X-Reason":"test"}}`,
		mock.PDStrings[testtools.StubPID+"/erp"])

	_, _, err = runCLI(t, mock, "test", "stub", "set", "bad/rule")
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
	_, _, err = runCLI(t, mock, "test", "stub", "set", "erp", "--status", "700")
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))

	_, _, err = runCLI(t, mock, "test", "stub", "clear", "erp")
	require.NoError(t, err)
	assert.NotContains(t, mock.PDStrings, testtools.StubPID+"/erp")
}

// On a production tier, installing the tooling and setting stub answers are
// refused before anything is sent; --allow-production lifts it.
func TestTestToolsRefusedOnProduction(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.Live = true
	for _, args := range [][]string{
		{"test", "tools", "install", "--tier", "prod"},
		{"test", "stub", "set", "erp", "--tier", "prod"},
	} {
		before := len(mock.Requests())
		_, _, err := runCLI(t, mock, args...)
		require.Error(t, err, args)
		assert.Equal(t, exitcode.Usage, output.ExitCode(err), args)
		assert.Contains(t, err.Error(), "production tenant")
		assert.Len(t, mock.Requests(), before, "nothing was sent: %v", args)
	}
	_, _, err := runCLI(t, mock, "test", "stub", "set", "erp", "--tier", "prod", "--allow-production")
	require.NoError(t, err)
}
