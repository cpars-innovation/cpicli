package httpclnt

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testExe(t *testing.T, h http.Handler, o Options) *HTTPExecuter {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	host, port := GetHostPort(srv.URL)
	o.Host, o.Scheme, o.Port = host, "http", port
	if o.OAuthHost != "" {
		o.OAuthHost = host
	} else {
		o.BasicUserID, o.BasicPassword = "u", "p"
	}
	return NewWithOptions(o)
}

// A cancelled context ends a request that hangs.
func TestWithContextCancelsRequest(t *testing.T) {
	release := make(chan struct{})
	exe := testExe(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}), Options{})
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := exe.WithContext(ctx).ExecGetRequest("/x", nil)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "%v", err)
	assert.Less(t, time.Since(started), 5*time.Second)
	assert.Equal(t, context.Background(), exe.Context(), "the original executer is unchanged")
}

// The wait between read retries ends with the context too.
func TestWithContextCancelsRetryWait(t *testing.T) {
	exe := testExe(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusServiceUnavailable)
	}), Options{}).RetryReads(3, time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := exe.WithContext(ctx).ExecGetRequest("/x", nil)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "%v", err)
	assert.Less(t, time.Since(started), 5*time.Second)
}

// Copies share the CSRF session: one fetch for all of them.
func TestWithContextSharesCSRF(t *testing.T) {
	exe := testExe(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-CSRF-Token") == "Fetch" {
			w.Header().Set("X-CSRF-Token", "t1")
			return
		}
		if r.Header.Get("X-CSRF-Token") != "t1" {
			w.Header().Set("X-CSRF-Token", "Required")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}), Options{})
	for range 3 {
		resp, err := exe.WithContext(context.Background()).Exec(http.MethodPost, "/x", nil, nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusCreated, resp.StatusCode)
	}
	assert.Equal(t, 1, exe.CSRFFetches())
}

type countingTransport struct{ n atomic.Int32 }

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return http.DefaultTransport.RoundTrip(r)
}

// An injected client carries the requests, also the OAuth token request,
// and its timeout applies to the token request.
func TestInjectedHTTPClient(t *testing.T) {
	tr := &countingTransport{}
	exe := testExe(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"a","token_type":"bearer","expires_in":3600}`))
			return
		}
		assert.Equal(t, "Bearer a", r.Header.Get("Authorization"))
	}), Options{OAuthHost: "x", OAuthPath: "/oauth/token", ClientID: "c", ClientSecret: "s", HTTPClient: &http.Client{Transport: tr}})
	_, err := exe.ExecGetRequest("/x", nil)
	require.NoError(t, err)
	assert.Equal(t, int32(2), tr.n.Load(), "token and API request through the injected transport")

	hang := make(chan struct{})
	defer close(hang)
	slow := testExe(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-hang }),
		Options{OAuthHost: "x", OAuthPath: "/oauth/token", ClientID: "c", ClientSecret: "s", HTTPClient: &http.Client{Timeout: 100 * time.Millisecond}})
	started := time.Now()
	_, err = slow.ExecGetRequest("/x", nil)
	assert.Error(t, err)
	assert.Less(t, time.Since(started), 5*time.Second, "the token request has a timeout")
}
