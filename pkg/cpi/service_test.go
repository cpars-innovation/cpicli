package cpi

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every executer retries throttled reads, never writes.
func TestInitHTTPExecuterRetriesReads(t *testing.T) {
	old := ReadBackoff
	ReadBackoff = time.Millisecond
	t.Cleanup(func() { ReadBackoff = old })

	var gets, posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CSRF-Token", "token")
		if r.URL.Path != "/x" {
			return // CSRF token fetch
		}
		n := gets.Load()
		if r.Method == http.MethodPost {
			posts.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		gets.Add(1)
		if n < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	exe := InitHTTPExecuter(&ServiceDetails{Host: srv.URL, Userid: "u", Password: "p"})
	resp, err := exe.ExecGetRequest("/x", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int32(3), gets.Load())

	resp, err = exe.Exec(http.MethodPost, "/x", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, int32(1), posts.Load(), "writes are not retried")
}

func TestParseGateway(t *testing.T) {
	for in, want := range map[string][4]any{
		"https://gw.example.com/t/dev":       {"https", "gw.example.com", 443, "/t/dev"},
		"https://gw.example.com:8443/t/dev/": {"https", "gw.example.com", 8443, "/t/dev"},
		"gw.example.com/t/dev":               {"https", "gw.example.com", 443, "/t/dev"},
		"http://127.0.0.1:8080/t/dev":        {"http", "127.0.0.1", 8080, "/t/dev"},
		"http://gw.example.com/t/dev":        {"https", "gw.example.com", 443, "/t/dev"}, // no token in clear text
		"https://gw.example.com":             {"https", "gw.example.com", 443, ""},
	} {
		s, h, p, path := ParseGateway(in)
		assert.Equal(t, want, [4]any{s, h, p, path}, in)
	}
}

func TestGatewayExecuter(t *testing.T) {
	var auth, path atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		path.Store(r.URL.Path)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	exe := InitHTTPExecuter(&ServiceDetails{Gateway: srv.URL + "/t/dev", GatewayToken: "tok",
		OauthHost: "ignored.example.com", OauthClientId: "x", OauthClientSecret: "y"})
	resp, err := exe.Exec(http.MethodPost, "/api/v1/IntegrationPackages", nil, nil)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, "Bearer tok", auth.Load(), "the user's token, no service key")
	assert.Equal(t, "/t/dev/api/v1/IntegrationPackages", path.Load())
	assert.Equal(t, "BEARER", exe.AuthType)
}
