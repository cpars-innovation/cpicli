// Package auth signs a user in to Entra ID for a cpictl gateway and keeps the
// resulting tokens, so cpictl (CLI and MCP) talks to the gateway as that user
// without a CPI service key. Two flows: an interactive one (Authorization Code
// + PKCE, a localhost callback) and a device-code one for headless and SSH.
// Tokens are kept by Store and refreshed silently with the refresh token.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"time"

	"golang.org/x/oauth2"
)

// Config says where and as whom to sign a user in.
type Config struct {
	TenantID string   // Entra directory (tenant) ID
	ClientID string   // the public-client app registration cpictl signs in as
	Scopes   []string // resource scopes, e.g. ["api://<gateway-app>/access"]
	// Authority is the sign-in base URL; empty means the Entra public cloud.
	// Set it to point at another cloud or, in tests, at a fake issuer.
	Authority string
}

// PublicCloud is the default Entra authority.
const PublicCloud = "https://login.microsoftonline.com/"

// Validate refuses an authority that is not https (plain http only on
// loopback, for tests): sign-in codes and tokens must not travel unencrypted.
func (c Config) Validate() error {
	if c.Authority == "" {
		return nil
	}
	u, err := url.Parse(c.Authority)
	if err != nil || u.Host == "" {
		return fmt.Errorf("login.authority %q is not a URL", c.Authority)
	}
	if u.Scheme == "https" {
		return nil
	}
	if ip := net.ParseIP(u.Hostname()); u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())) {
		return nil
	}
	return fmt.Errorf("login.authority %q must use https", c.Authority)
}

func (c Config) endpoint() oauth2.Endpoint {
	base := c.Authority
	if base == "" {
		base = PublicCloud
	}
	a := base + c.TenantID + "/oauth2/v2.0/"
	return oauth2.Endpoint{AuthURL: a + "authorize", TokenURL: a + "token", DeviceAuthURL: a + "devicecode"}
}

// oauth2Config always asks for openid and offline_access (the latter is what
// makes Entra return a refresh token) on top of the resource scopes.
func (c Config) oauth2Config(redirect string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:    c.ClientID,
		Endpoint:    c.endpoint(),
		RedirectURL: redirect,
		Scopes:      append([]string{"openid", "offline_access"}, c.Scopes...),
	}
}

// LoginInteractive runs Authorization Code + PKCE with a loopback redirect.
// visit is called with the sign-in URL once the local listener is up; a
// command opens the browser and prints the URL there, a test drives it. It
// returns when Entra redirects back, the context is cancelled, or visit fails.
func LoginInteractive(ctx context.Context, c Config, visit func(authURL string) error) (*oauth2.Token, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("opening a loopback listener for the sign-in redirect: %w", err)
	}
	defer ln.Close()

	conf := c.oauth2Config(fmt.Sprintf("http://%s/callback", ln.Addr().String()))
	verifier := oauth2.GenerateVerifier()
	state, err := randString()
	if err != nil {
		return nil, err
	}
	authURL := conf.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.S256ChallengeOption(verifier))

	type result struct {
		code string
		err  error
	}
	ch := make(chan result, 1)
	// one answer only, and only on /callback: a browser's favicon request or
	// another local process must neither block nor decide the sign-in
	send := func(res result) {
		select {
		case ch <- res:
		default:
		}
	}
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		switch {
		case q.Get("error") != "":
			msg := q.Get("error")
			if d := q.Get("error_description"); d != "" {
				msg += ": " + d
			}
			send(result{err: fmt.Errorf("sign-in refused: %s", msg)})
			http.Error(w, "Sign-in failed. You can close this tab.", http.StatusBadRequest)
		case q.Get("state") != state:
			send(result{err: fmt.Errorf("sign-in state did not match: possible cross-site request")})
			http.Error(w, "Sign-in failed. You can close this tab.", http.StatusBadRequest)
		default:
			send(result{code: q.Get("code")})
			fmt.Fprintln(w, "Signed in to cpictl. You can close this tab and return to the terminal.")
		}
	})}
	go srv.Serve(ln)
	defer srv.Close()

	if err := visit(authURL); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if res.err != nil {
			return nil, res.err
		}
		tok, err := conf.Exchange(ctx, res.code, oauth2.VerifierOption(verifier))
		if err != nil {
			return nil, fmt.Errorf("exchanging the sign-in code for a token: %w", err)
		}
		return tok, nil
	}
}

// LoginDeviceCode runs the device-authorization flow for headless and SSH: the
// user opens a URL on another device and types a code. show is called with the
// verification URL and code to display; the call then blocks until the user
// finishes, the code expires, or the context is cancelled.
func LoginDeviceCode(ctx context.Context, c Config, show func(*oauth2.DeviceAuthResponse)) (*oauth2.Token, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	conf := c.oauth2Config("")
	da, err := conf.DeviceAuth(ctx)
	if err != nil {
		return nil, fmt.Errorf("starting device sign-in: %w", err)
	}
	show(da)
	tok, err := conf.DeviceAccessToken(ctx, da)
	if err != nil {
		return nil, fmt.Errorf("waiting for device sign-in: %w", err)
	}
	return tok, nil
}

// Browse opens url in the user's default browser. A command calls it for the
// interactive flow and prints the URL too, so sign-in still works when no
// browser can be opened.
func Browse(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

func randString() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
