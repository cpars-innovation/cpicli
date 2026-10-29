package cpitest_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Without pkg/mocktenant/sshkeys, the mock does not read SSH keys and says
// so (cpicli#62); with it, pkg/ops' key material tests cover the reading.
func TestSSHKeyUploadNeedsSSHKeys(t *testing.T) {
	require.Nil(t, cpitest.SSHPublicKey, "this test binary must not import pkg/mocktenant/sshkeys")
	m := cpitest.NewTenant(t, nil)
	resp := post(t, m.URL()+"/api/v1/SSHKeyGenerationRequests", "application/json",
		`{"Alias":"sftp","Hexalias":"73667470","CommonName":"sftp","SSHFile":"bWFkZS11cC1rZXk="}`, nil)
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusNotImplemented, resp.StatusCode, string(body))
	assert.Contains(t, string(body), "pkg/mocktenant/sshkeys")
}
