package httpclnt

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

type HTTPExecuter struct {
	basicUserId   string
	basicPassword string
	host          string
	scheme        string
	port          int
	httpClient    *http.Client
	AuthType      string
	showLogs      bool
	// csrf is shared by the copies WithContext returns.
	csrf *csrf
	// ctx cancels the requests of this executer (nil: never).
	ctx context.Context
	// csrfPath is where CSRF tokens are fetched (default CSRFFetchPath);
	// csrfOnDemand disables the up-front fetch for Basic Auth.
	csrfPath     string
	csrfOnDemand bool
	readRetries  int
	readBackoff  time.Duration
}

// ForEndpoint configures the executer for a runtime endpoint of an integration
// flow: CSRF tokens are fetched from path, and only after the endpoint asked for
// one (403 "X-CSRF-Token: Required"), so no GET reaches an endpoint that does
// not need a token.
// RetryReads retries GET and HEAD requests that answer 429, 502, 503 or 504
// up to n times, waiting backoff, 2*backoff, ... (or Retry-After when the
// tenant sends it, at most 30 s). For bulk reads such as parallel snapshots.
func (e *HTTPExecuter) RetryReads(n int, backoff time.Duration) *HTTPExecuter {
	e.readRetries, e.readBackoff = n, backoff
	return e
}

func retryable(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

func (e *HTTPExecuter) ForEndpoint(path string) *HTTPExecuter {
	e.csrfPath, e.csrfOnDemand = path, true
	return e
}

// DefaultTimeout limits one request (including the OAuth token request) when
// Options.HTTPClient is not set.
const DefaultTimeout = 30 * time.Second

// Options configure an executer (see NewWithOptions).
type Options struct {
	// Host, Scheme and Port of the tenant (e.g. "x.hana.ondemand.com",
	// "https", 443).
	Host   string
	Scheme string
	Port   int
	// OAuthHost and OAuthPath name the token endpoint (same scheme and
	// port as the tenant); with OAuthHost set, OAuth client credentials are
	// used, else Basic Auth.
	OAuthHost, OAuthPath       string
	ClientID, ClientSecret     string
	BasicUserID, BasicPassword string
	// HTTPClient is the client requests go through (its Transport, Timeout
	// and Jar); with OAuth it also fetches the tokens. Nil: a client with
	// DefaultTimeout.
	HTTPClient *http.Client
	// ShowLogs writes debug logs of requests.
	ShowLogs bool
}

// New returns an initialised HTTPExecuter instance.
func New(oauthHost string, oauthPath string, clientId string, clientSecret string, userId string, password string, host string, scheme string, port int, showLogs bool) *HTTPExecuter {
	return NewWithOptions(Options{Host: host, Scheme: scheme, Port: port, OAuthHost: oauthHost, OAuthPath: oauthPath,
		ClientID: clientId, ClientSecret: clientSecret, BasicUserID: userId, BasicPassword: password, ShowLogs: showLogs})
}

// NewWithOptions returns an executer configured by o.
func NewWithOptions(o Options) *HTTPExecuter {
	e := &HTTPExecuter{host: o.Host, scheme: o.Scheme, port: o.Port, showLogs: o.ShowLogs, csrf: &csrf{}}
	base := o.HTTPClient
	if base == nil {
		base = &http.Client{Timeout: DefaultTimeout, Transport: secureTransport}
	}
	if o.OAuthHost != "" {
		if e.showLogs {
			log.Debug().Msg("Initialising HTTP client with OAuth 2.0")
		}
		tokenURL := fmt.Sprintf("%v://%v:%d%v", o.Scheme, o.OAuthHost, o.Port, o.OAuthPath)
		if e.showLogs {
			log.Debug().Msgf("Setting up OAuth 2.0 client with token URL %v", tokenURL)
		}
		// Reference https://pkg.go.dev/golang.org/x/oauth2/clientcredentials#pkg-overview
		conf := &clientcredentials.Config{ClientID: o.ClientID, ClientSecret: o.ClientSecret, TokenURL: tokenURL}
		// the token requests go through base (and its timeout); so do the
		// API requests, which carry the token
		client := conf.Client(context.WithValue(context.Background(), oauth2.HTTPClient, base))
		client.Timeout, client.Jar = base.Timeout, base.Jar
		e.httpClient = client
		e.AuthType = "OAUTH"
	} else {
		if e.showLogs {
			log.Debug().Msg("Initialising HTTP client with Basic Authentication")
		}
		e.httpClient = base
		e.basicUserId = o.BasicUserID
		e.basicPassword = o.BasicPassword
		e.AuthType = "BASIC"
	}
	return e
}

// WithContext returns a copy of the executer whose requests are cancelled
// with ctx (also the waits between retries). The copy shares the session:
// CSRF token, cookies, OAuth token and retry settings. Use one per request
// or job: exe.WithContext(r.Context()).
func (e *HTTPExecuter) WithContext(ctx context.Context) *HTTPExecuter {
	c := *e
	c.ctx = ctx
	return &c
}

// Context is the executer's context (context.Background without one).
func (e *HTTPExecuter) Context() context.Context {
	if e.ctx == nil {
		return context.Background()
	}
	return e.ctx
}

// Exec sends a request to the tenant. Modifying requests (POST, PUT, PATCH,
// DELETE) get a CSRF token automatically, see csrf.
func (e *HTTPExecuter) Exec(method string, path string, body io.Reader, headers map[string]string) (*http.Response, error) {
	var payload []byte
	if body != nil && body != http.NoBody {
		var err error
		if payload, err = io.ReadAll(body); err != nil {
			return nil, err
		}
	}
	if !isModifying(method) {
		resp, err := e.send(method, path, payload, headers, nil)
		wait := e.readBackoff
		for attempt := 0; attempt < e.readRetries && err == nil && retryable(resp.StatusCode); attempt++ {
			delay := wait
			if s, perr := strconv.Atoi(resp.Header.Get("Retry-After")); perr == nil && s >= 0 {
				delay = min(time.Duration(s)*time.Second, 30*time.Second)
			}
			_, _ = e.ReadRespBody(resp)
			log.Debug().Msgf("%v %v answered %d, retrying in %v", method, path, resp.StatusCode, delay)
			if err := sleepCtx(e.Context(), delay); err != nil {
				return nil, err
			}
			wait *= 2
			resp, err = e.send(method, path, payload, headers, nil)
		}
		return resp, err
	}

	token, cookies, generation, err := e.csrfCurrent(e.AuthType == "BASIC" && !e.csrfOnDemand)
	if err != nil {
		return nil, err
	}
	resp, err := e.send(method, path, payload, withCSRF(headers, token), cookies)
	if err != nil || !csrfRequired(resp) {
		return resp, err
	}

	// Token missing or expired: fetch a new one and retry once
	_, _ = e.ReadRespBody(resp)
	if e.showLogs {
		log.Debug().Msgf("CSRF token required for %v %v, fetching a new token", method, path)
	}
	token, cookies, err = e.csrfRefresh(generation)
	if err != nil {
		return nil, err
	}
	return e.send(method, path, payload, withCSRF(headers, token), cookies)
}

func withCSRF(headers map[string]string, token string) map[string]string {
	if token == "" {
		return headers
	}
	h := make(map[string]string, len(headers)+1)
	for k, v := range headers {
		h[k] = v
	}
	h["X-CSRF-Token"] = token
	return h
}

func (e *HTTPExecuter) send(method string, path string, payload []byte, headers map[string]string, cookies []*http.Cookie) (*http.Response, error) {
	url := fmt.Sprintf("%v://%v:%d%v", e.scheme, e.host, e.port, path)
	if e.showLogs {
		log.Debug().Msgf("Executing HTTP request: %v %v", method, url)
	}

	var body io.Reader = http.NoBody
	if len(payload) > 0 {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(e.Context(), method, url, body)
	if err != nil {
		return nil, err
	}
	if e.basicUserId != "" {
		req.SetBasicAuth(e.basicUserId, e.basicPassword)
	}
	for k, v := range headers {
		// as given: header names are case-insensitive in HTTP, but flows
		// see them as sent (a script iterating the headers, an XSLT
		// parameter named after one)
		req.Header.Del(k)
		req.Header[k] = []string{v}
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	base := fmt.Sprintf("%v://%v:%d", e.scheme, e.host, e.port)
	return e.guarded(method, base, path, payload, headers, func() (*http.Response, error) { return e.httpClient.Do(req) })
}

// sleepCtx waits d or until ctx ends (then it returns ctx's error).
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *HTTPExecuter) ExecGetRequest(path string, headers map[string]string) (resp *http.Response, err error) {
	return e.Exec(http.MethodGet, path, http.NoBody, headers)
}

func (e *HTTPExecuter) ReadRespBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err == nil && int64(len(body)) > MaxResponseBytes {
		return nil, fmt.Errorf("response larger than %d bytes", MaxResponseBytes)
	}
	return body, err
}

// MaxResponseBytes caps a response read into memory (artifact downloads,
// payloads): a misbehaving endpoint cannot exhaust memory.
var MaxResponseBytes int64 = 1 << 30

// secureTransport is the default transport with TLS 1.2 as the minimum; it
// keeps the proxy settings of the environment.
var secureTransport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	return t
}()

// HTTPError is returned for tenant responses with an unexpected status code.
// Its message is kept identical to the historic plain error string because
// some callers still match on it.
type HTTPError struct {
	CallType   string
	StatusCode int
	Body       []byte
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%v call failed with response code = %d", e.CallType, e.StatusCode)
}

// StatusCode returns the HTTP status code carried by err (or any error it
// wraps), or 0 if err is not an HTTPError.
func StatusCode(err error) int {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode
	}
	return 0
}

// IsAuthError reports whether err is an HTTP 401/403 from the tenant.
func IsAuthError(err error) bool {
	code := StatusCode(err)
	return code == http.StatusUnauthorized || code == http.StatusForbidden
}

func (e *HTTPExecuter) LogError(resp *http.Response, callType string) (resBody []byte, err error) {
	resBody, err = e.ReadRespBody(resp)
	if err != nil {
		return
	}

	if len(resBody) != 0 && e.showLogs {
		log.Warn().Msgf("Response body = %s", Redact(string(resBody)))
	}

	return resBody, &HTTPError{CallType: callType, StatusCode: resp.StatusCode, Body: resBody}
}
