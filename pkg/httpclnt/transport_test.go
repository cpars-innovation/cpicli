package httpclnt

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CAs a program adds to http.DefaultTransport after cpicli was loaded (a
// company CA) are trusted by executers created afterwards (#25).
func TestSecureTransportKeepsAddedCAs(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	newExe := func() *HTTPExecuter {
		return NewWithOptions(Options{Host: u.Hostname(), Scheme: "https", Port: port, BasicUserID: "u", BasicPassword: "p"})
	}

	_, err := newExe().ExecGetRequest("/", nil)
	require.Error(t, err, "the test server's CA is unknown")

	orig := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = orig })
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	tr := orig.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS10}
	http.DefaultTransport = tr

	resp, err := newExe().ExecGetRequest("/", nil)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)

	st := secureTransport().(*http.Transport)
	assert.Equal(t, uint16(tls.VersionTLS12), st.TLSClientConfig.MinVersion, "the minimum is raised")
	assert.Same(t, pool, st.TLSClientConfig.RootCAs)
	assert.Equal(t, uint16(tls.VersionTLS10), tr.TLSClientConfig.MinVersion, "the program's transport is not changed")
}
