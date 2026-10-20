package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// fakeIssuer stands in for Entra: authorize redirects back with a code, token
// mints access/refresh tokens for the auth-code, refresh and device grants.
type fakeIssuer struct {
	srv       *httptest.Server
	issued    int64 // access tokens minted
	lastGrant string
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	t.Helper()
	f := &fakeIssuer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/tid/oauth2/v2.0/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
			http.Error(w, "missing PKCE challenge", http.StatusBadRequest)
			return
		}
		loc := q.Get("redirect_uri") + "?code=the-code&state=" + url.QueryEscape(q.Get("state"))
		http.Redirect(w, r, loc, http.StatusFound)
	})
	mux.HandleFunc("/tid/oauth2/v2.0/devicecode", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"device_code": "dev-code", "user_code": "ABCD-EFGH",
			"verification_uri": f.srv.URL + "/device", "expires_in": 300, "interval": 1,
		})
	})
	mux.HandleFunc("/tid/oauth2/v2.0/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		grant := r.Form.Get("grant_type")
		f.lastGrant = grant
		if grant == "authorization_code" && r.Form.Get("code_verifier") == "" {
			http.Error(w, "missing PKCE verifier", http.StatusBadRequest)
			return
		}
		n := atomic.AddInt64(&f.issued, 1)
		writeJSON(w, map[string]any{
			"access_token": fmt.Sprintf("access-%d", n), "token_type": "Bearer",
			"refresh_token": "refresh-tok", "expires_in": 3600,
		})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIssuer) config() Config {
	return Config{TenantID: "tid", ClientID: "client", Scopes: []string{"api://gw/access"}, Authority: f.srv.URL + "/"}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestLoginInteractive(t *testing.T) {
	f := newFakeIssuer(t)
	tok, err := LoginInteractive(context.Background(), f.config(), func(authURL string) error {
		// a browser would load this; the fake redirects to the loopback callback
		resp, err := http.Get(authURL)
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, "access-1", tok.AccessToken)
	assert.Equal(t, "refresh-tok", tok.RefreshToken)
	assert.Equal(t, "authorization_code", f.lastGrant)
}

func TestLoginDeviceCode(t *testing.T) {
	f := newFakeIssuer(t)
	var shown *oauth2.DeviceAuthResponse
	tok, err := LoginDeviceCode(context.Background(), f.config(), func(d *oauth2.DeviceAuthResponse) { shown = d })
	require.NoError(t, err)
	assert.Equal(t, "ABCD-EFGH", shown.UserCode)
	assert.Equal(t, "access-1", tok.AccessToken)
	assert.Equal(t, "urn:ietf:params:oauth:grant-type:device_code", f.lastGrant)
}

func TestStoreRoundTripAndPermissions(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	if _, err := s.Load("dev"); err != ErrNotLoggedIn {
		t.Fatalf("want ErrNotLoggedIn, got %v", err)
	}
	tok := &oauth2.Token{AccessToken: "a", RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}
	require.NoError(t, s.Save("dev", tok))
	got, err := s.Load("dev")
	require.NoError(t, err)
	assert.Equal(t, "a", got.AccessToken)

	if runtime.GOOS != "windows" {
		p := filepath.Join(s.Dir, "dev.json")
		info, err := os.Stat(p)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "a token file must not be world- or group-readable")
	}

	require.NoError(t, s.Delete("dev"))
	_, err = s.Load("dev")
	assert.ErrorIs(t, err, ErrNotLoggedIn)
}

func TestTokenSourceRefreshesAndPersists(t *testing.T) {
	f := newFakeIssuer(t)
	s := &Store{Dir: t.TempDir()}
	// an already-expired access token with a refresh token
	require.NoError(t, s.Save("dev", &oauth2.Token{
		AccessToken: "old", RefreshToken: "refresh-tok", Expiry: time.Now().Add(-time.Minute),
	}))

	src, err := s.TokenSource(context.Background(), f.config(), "dev")
	require.NoError(t, err)
	tok, err := src.Token()
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(tok.AccessToken, "access-"), "expired token should be refreshed")
	assert.Equal(t, "refresh_token", f.lastGrant)

	// the refreshed token must be written back for the next process
	stored, err := s.Load("dev")
	require.NoError(t, err)
	assert.Equal(t, tok.AccessToken, stored.AccessToken, "refreshed token must be persisted")
}
