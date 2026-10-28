package httpclnt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// No executer follows a redirect: a followed redirect skips the access
// scope's check of the request it leads to, and a refreshing token
// transport would send its token to the redirect's host (security review
// of 0.35.1, findings 1 and 2).
func TestNoRedirectsInAnyAuthMode(t *testing.T) {
	var elsewhere atomic.Int32
	var leaked atomic.Value
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere.Add(1)
		leaked.Store(r.Header.Get("Authorization"))
	}))
	defer other.Close()
	var sameHost atomic.Int32
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"made-up-token","token_type":"bearer","expires_in":3600}`))
		case "/to-other":
			http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
		case "/to-same":
			http.Redirect(w, r, "/api/v1/IntegrationPackages('FI_Billing')", http.StatusFound)
		default:
			sameHost.Add(1)
		}
	}))
	defer src.Close()
	u, _ := url.Parse(src.URL)
	port, _ := strconv.Atoi(u.Port())
	base := Options{Host: u.Hostname(), Scheme: "http", Port: port}

	modes := map[string]Options{}
	basic := base
	basic.BasicUserID, basic.BasicPassword = "made-up-user", "made-up-password"
	modes["basic"] = basic
	oauth := base
	oauth.OAuthHost, oauth.OAuthPath, oauth.ClientID, oauth.ClientSecret = u.Hostname(), "/token", "made-up-client", "made-up-secret"
	modes["oauth"] = oauth
	bearer := base
	bearer.BearerTokenSource = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "made-up-gateway-token"})
	modes["bearer source"] = bearer
	static := base
	static.BearerToken = "made-up-static-token"
	modes["bearer"] = static
	own := basic
	own.HTTPClient = &http.Client{} // a caller's client that would follow redirects
	modes["caller's client"] = own

	for name, o := range modes {
		e := NewWithOptions(o).WithContext(context.Background())
		for _, path := range []string{"/to-other", "/to-same"} {
			resp, err := e.ExecGetRequest(path, nil)
			require.NoError(t, err, name)
			resp.Body.Close()
			assert.Equal(t, http.StatusFound, resp.StatusCode, "%s %s: the redirect is returned, not followed", name, path)
		}
	}
	assert.Zero(t, elsewhere.Load(), "no request reached the redirect's host")
	assert.Nil(t, leaked.Load(), "no token was sent there")
	assert.Zero(t, sameHost.Load(), "a same-host redirect is not followed either (it would skip the guard)")
	assert.Nil(t, own.HTTPClient.CheckRedirect, "the caller's client is not changed")
}
