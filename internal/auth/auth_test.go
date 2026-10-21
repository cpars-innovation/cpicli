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
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
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
		// like Entra with http://localhost registered: any port, nothing else
		if !entraLoopback.MatchString(q.Get("redirect_uri")) {
			http.Error(w, "AADSTS50011: redirect URI mismatch", http.StatusBadRequest)
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

var entraLoopback = regexp.MustCompile(`^http://localhost:[0-9]+$`)

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
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		redirect := u.Query().Get("redirect_uri")
		assert.Regexp(t, entraLoopback, redirect, "must match a registered http://localhost")
		// what a browser or another local process sends first must not end
		// the sign-in
		for _, stray := range []string{"/favicon.ico", "/", "/?foo=bar", "/callback?code=x"} {
			resp, err := http.Get(redirect + stray)
			if err != nil {
				return err
			}
			resp.Body.Close()
			assert.Equal(t, http.StatusNotFound, resp.StatusCode, stray)
		}
		// a browser would load this; the fake redirects to the loopback
		resp, err := http.Get(authURL)
		if err != nil {
			return err
		}
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
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

func TestValidateAuthority(t *testing.T) {
	for a, ok := range map[string]bool{"": true, "https://login.microsoftonline.com/": true, "http://127.0.0.1:8080/": true,
		"http://localhost/": true, "http://login.example.com/": false, "ftp://x/": false, "::": false} {
		err := Config{Authority: a}.Validate()
		if ok && err != nil || !ok && err == nil {
			t.Errorf("%q: %v", a, err)
		}
	}
}

// Parallel requests share one source: refresh and persist must not race.
func TestTokenSourceParallel(t *testing.T) {
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"b","token_type":"Bearer","refresh_token":"r2","expires_in":3600}`)
	}))
	defer issuer.Close()
	s := &Store{Dir: t.TempDir()}
	tok := &oauth2.Token{AccessToken: "a", TokenType: "Bearer", RefreshToken: "r", Expiry: time.Now().Add(-time.Minute)} // expired: refresh
	if err := s.Save("p", tok); err != nil {
		t.Fatal(err)
	}
	src, err := s.TokenSource(context.Background(), Config{TenantID: "t", ClientID: "c", Authority: issuer.URL + "/"}, "p")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if _, err := src.Token(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if got, err := s.Load("p"); err != nil || got.AccessToken != "b" {
		t.Errorf("refreshed token not persisted: %v %v", got, err)
	}
}

// An expired or revoked refresh token says what to do, and stays an
// authentication error (exit code 3).
func TestTokenSourceExpiredSignIn(t *testing.T) {
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant","error_description":"AADSTS700082: The refresh token has expired"}`)
	}))
	defer issuer.Close()
	s := &Store{Dir: t.TempDir()}
	require.NoError(t, s.Save("dev", &oauth2.Token{AccessToken: "a", RefreshToken: "r", Expiry: time.Now().Add(-time.Minute)}))
	src, err := s.TokenSource(context.Background(), Config{TenantID: "t", ClientID: "c", Authority: issuer.URL + "/"}, "dev")
	require.NoError(t, err)
	_, err = src.Token()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired for profile dev: run cpictl auth login")
	var re *oauth2.RetrieveError
	assert.ErrorAs(t, err, &re)
}

// Sign-in and refresh use cpictl's client (with a timeout), not
// http.DefaultClient; a client the caller put in the context is kept.
func TestWithClient(t *testing.T) {
	c, ok := withClient(context.Background()).Value(oauth2.HTTPClient).(*http.Client)
	require.True(t, ok)
	assert.Equal(t, httpclnt.DefaultTimeout, c.Timeout)
	mine := &http.Client{}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, mine)
	assert.Same(t, mine, withClient(ctx).Value(oauth2.HTTPClient))
}

// fakeKeyring stands in for the OS keychain; fail makes it refuse, like a
// host without one.
type fakeKeyring struct {
	m    map[string]string
	fail bool
}

func (f *fakeKeyring) Get(service, user string) (string, error) {
	if f.fail {
		return "", fmt.Errorf("no keychain")
	}
	v, ok := f.m[service+"/"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (f *fakeKeyring) Set(service, user, secret string) error {
	if f.fail {
		return fmt.Errorf("no keychain")
	}
	f.m[service+"/"+user] = secret
	return nil
}

func (f *fakeKeyring) Delete(service, user string) error {
	if _, ok := f.m[service+"/"+user]; !ok {
		return keyring.ErrNotFound
	}
	delete(f.m, service+"/"+user)
	return nil
}

// The refresh token goes to the OS keychain and never into the file; an older
// file token moves there; without a keychain the file keeps it all (#42).
func TestStoreKeychain(t *testing.T) {
	kr := &fakeKeyring{m: map[string]string{}}
	s := &Store{Dir: t.TempDir(), Keyring: kr}
	tok := &oauth2.Token{AccessToken: "acc", RefreshToken: "long-lived-refresh", Expiry: time.Now().Add(time.Hour)}
	require.NoError(t, s.Save("dev", tok))
	file, err := os.ReadFile(filepath.Join(s.Dir, "dev.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(file), "long-lived-refresh", "the refresh token is not in the file")
	assert.Equal(t, "long-lived-refresh", kr.m[KeyringService+"/gateway-refresh-token:dev"])
	got, err := s.Load("dev")
	require.NoError(t, err)
	assert.Equal(t, "acc", got.AccessToken)
	assert.Equal(t, "long-lived-refresh", got.RefreshToken)

	require.NoError(t, s.Delete("dev"))
	assert.Empty(t, kr.m, "logout clears the keychain too")
	_, err = s.Load("dev")
	assert.ErrorIs(t, err, ErrNotLoggedIn)

	// a token stored before: moved on first use
	plain := &Store{Dir: s.Dir}
	require.NoError(t, plain.Save("old", tok))
	got, err = s.Load("old")
	require.NoError(t, err)
	assert.Equal(t, "long-lived-refresh", got.RefreshToken)
	file, _ = os.ReadFile(filepath.Join(s.Dir, "old.json"))
	assert.NotContains(t, string(file), "long-lived-refresh", "moved to the keychain")

	// no keychain on this host: all in the file, still readable
	none := &Store{Dir: t.TempDir(), Keyring: &fakeKeyring{m: map[string]string{}, fail: true}}
	require.NoError(t, none.Save("ci", tok))
	got, err = none.Load("ci")
	require.NoError(t, err)
	assert.Equal(t, "long-lived-refresh", got.RefreshToken)
}

// A refresh token the keychain no longer has: the access token works until
// it expires, then the error says to sign in again.
func TestStoreKeychainLost(t *testing.T) {
	kr := &fakeKeyring{m: map[string]string{}}
	s := &Store{Dir: t.TempDir(), Keyring: kr}
	require.NoError(t, s.Save("dev", &oauth2.Token{AccessToken: "acc", RefreshToken: "r", Expiry: time.Now().Add(-time.Minute)}))
	kr.m = map[string]string{}
	src, err := s.TokenSource(context.Background(), Config{TenantID: "t", ClientID: "c", Authority: "http://127.0.0.1:1/"}, "dev")
	require.NoError(t, err)
	_, err = src.Token()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "run cpictl auth login")
}
