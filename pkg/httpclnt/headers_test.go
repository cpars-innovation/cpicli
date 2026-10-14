package httpclnt

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type captureTransport struct{ req *http.Request }

func (c *captureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	h := http.Header{}
	fetch := false
	for k, v := range r.Header { // names as sent: not canonical
		fetch = fetch || strings.EqualFold(k, "X-CSRF-Token") && strings.EqualFold(v[0], "fetch")
	}
	if fetch {
		h.Set("X-CSRF-Token", "t1") // the token fetch before a write
	} else {
		c.req = r
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: h, Request: r}, nil
}

// Header names go out as given (flows see them as sent).
func TestHeaderNamesAsGiven(t *testing.T) {
	ct := &captureTransport{}
	e := NewWithOptions(Options{Host: "tenant.example.com", Scheme: "https", Port: 443, HTTPClient: &http.Client{Transport: ct}})
	resp, err := e.Exec(http.MethodPost, "/http/x", http.NoBody, map[string]string{"SAP_ApplicationID": "4711", "Content-Type": "application/xml"})
	require.NoError(t, err)
	resp.Body.Close()
	require.NotNil(t, ct.req)
	assert.Equal(t, []string{"4711"}, ct.req.Header["SAP_ApplicationID"], "exact name")
	assert.NotContains(t, ct.req.Header, "Sap_applicationid")
	assert.Equal(t, "application/xml", ct.req.Header.Get("Content-Type"))
}
