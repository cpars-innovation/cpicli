package cpitest_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Like a real tenant, the key and SSH key generation requests refuse the
// update query option: 400 for key pairs, 500 "jschKeyPair is null" for
// SSH keys (both seen on a real tenant).
func TestKeyGenerationRefusesUpdateOption(t *testing.T) {
	m := cpitest.NewTenant(t, nil)
	body := `{"Alias":"client","Hexalias":"636C69656E74","CommonName":"client"}`
	for _, update := range []string{"true", "false"} {
		resp := post(t, m.URL()+"/api/v1/KeyPairGenerationRequests?returnKeystoreEntries=false&update="+update, "application/json", body, nil)
		b, _ := io.ReadAll(resp.Body)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Contains(t, string(b), "invalid query option(s) [update]")

		resp = post(t, m.URL()+"/api/v1/SSHKeyGenerationRequests?returnKeystoreEntries=false&update="+update, "application/json", body, nil)
		b, _ = io.ReadAll(resp.Body)
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		assert.Contains(t, string(b), "jschKeyPair is null")
	}
	assert.Empty(t, m.Keystore)

	resp := post(t, m.URL()+"/api/v1/KeyPairGenerationRequests?returnKeystoreEntries=false", "application/json", body, nil)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Len(t, m.Keystore, 1)
}

// Like a real tenant, the keystore stores and lists aliases in lower case
// and finds an entry by its alias in any case.
func TestKeystoreAliasesAreLowerCase(t *testing.T) {
	m := cpitest.NewTenant(t, nil)
	resp := post(t, m.URL()+"/api/v1/KeyPairGenerationRequests?returnKeystoreEntries=false", "application/json",
		`{"Alias":"MyKey","Hexalias":"4D794B6579","CommonName":"client"}`, nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.Len(t, m.Keystore, 1)
	assert.Equal(t, "mykey", m.Keystore[0].Alias)

	// the same alias in another case exists already
	resp = post(t, m.URL()+"/api/v1/KeyPairGenerationRequests?returnKeystoreEntries=false", "application/json",
		`{"Alias":"MYKEY","Hexalias":"4D594B4559","CommonName":"client"}`, nil)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Len(t, m.Keystore, 1)
}
