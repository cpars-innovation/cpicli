package httpclnt

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// rotatingSource hands out a new token each time its current one has expired,
// like oauth2's own refresh behaviour.
type rotatingSource struct {
	mu   sync.Mutex
	n    int
	life time.Duration
}

func (s *rotatingSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return &oauth2.Token{AccessToken: tokenName(s.n), TokenType: "Bearer", Expiry: time.Now().Add(s.life)}, nil
}

func tokenName(n int) string { return "tok-" + string(rune('0'+n)) }

func TestBearerTokenSourceIsSentAndRefreshed(t *testing.T) {
	var got []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Header.Get("Authorization"))
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	host, port := GetHostPort(srv.URL)

	src := &rotatingSource{life: 40 * time.Millisecond}
	exe := NewWithOptions(Options{Host: host, Scheme: "http", Port: port, BearerTokenSource: src})
	assert.Equal(t, "BEARER", exe.AuthType)
	assert.Empty(t, exe.bearer, "the transport sets the header; Exec must not also set it")

	// first request uses the first token
	_, err := exe.Exec(http.MethodGet, "/x", nil, nil)
	require.NoError(t, err)
	// let the token expire, then a second request must carry a refreshed one
	time.Sleep(60 * time.Millisecond)
	_, err = exe.Exec(http.MethodGet, "/x", nil, nil)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, got, 2)
	assert.Equal(t, "Bearer "+tokenName(1), got[0])
	assert.Equal(t, "Bearer "+tokenName(2), got[1], "an expired token must be refreshed, not resent")
}

// A static BearerToken keeps working (the env-var path, no refresh).
func TestStaticBearerTokenStillWorks(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
	}))
	t.Cleanup(srv.Close)
	host, port := GetHostPort(srv.URL)
	exe := NewWithOptions(Options{Host: host, Scheme: "http", Port: port, BearerToken: "static-abc"})
	_, err := exe.Exec(http.MethodGet, "/x", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "Bearer static-abc", got)
}
