package cpi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// In gateway mode a test message goes to the gateway, below its tenant path,
// with the user's token: not to the runtime host the tenant lists.
func TestEndpointExecuterThroughGateway(t *testing.T) {
	var path, auth string
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.RequestURI(), r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer gw.Close()
	details := &ServiceDetails{Gateway: gw.URL + "/t/dev", GatewayToken: "made-up-user-token"}
	exe, p, err := NewEndpointExecuter(details, "https://rt.example.com/http/sd/orders?x=1")
	require.NoError(t, err)
	resp, err := exe.Exec(http.MethodGet, p, http.NoBody, nil)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, "/t/dev/http/sd/orders?x=1", path)
	assert.Equal(t, "Bearer made-up-user-token", auth)
}
