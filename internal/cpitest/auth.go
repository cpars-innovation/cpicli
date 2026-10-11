package cpitest

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Client is an OAuth client of a service key (client ID and secret); a
// tenant also accepts it as Basic Auth user and password.
type Client struct {
	ID, Secret string
}

// Auth makes the mock check credentials like a Cloud Foundry tenant with two
// service keys: the API key (plan api) manages content through /api/v1 and
// cannot call flows; the runtime key (plan integration-flow, role
// ESBMessaging.send) calls the flows' endpoints (/http/..., /cxf/...) and
// cannot manage content. The OAuth token endpoint issues a token per client,
// so the mock tells them apart.
type Auth struct {
	// APIUser and APIPassword are the API's Basic Auth user.
	APIUser, APIPassword string
	// APIClient is the API key's OAuth client.
	APIClient Client
	// Runtime is the runtime key's OAuth client.
	Runtime Client
}

// Credential kinds.
const (
	kindAPI     = "api"
	kindRuntime = "runtime"
)

// issuedToken is an access token the mock issued.
type issuedToken struct {
	client, kind string
}

func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// clientKind is the kind of key a client or user belongs to ("" when the
// credentials are wrong). Without Auth every client counts as the API key.
func (a *Auth) clientKind(id, secret string) string {
	switch {
	case a == nil:
		return kindAPI
	case id != "" && equal(id, a.Runtime.ID) && equal(secret, a.Runtime.Secret):
		return kindRuntime
	case id != "" && equal(id, a.APIClient.ID) && equal(secret, a.APIClient.Secret):
		return kindAPI
	case id != "" && equal(id, a.APIUser) && equal(secret, a.APIPassword):
		return kindAPI
	}
	return ""
}

// handleToken serves client credential tokens at /oauth/token: one token per
// request, remembered with its client. With Auth only its clients get one
// (XSUAA's 401 otherwise).
func (m *Tenant) handleToken(w http.ResponseWriter, r *http.Request) {
	id, secret, ok := r.BasicAuth()
	if ok { // RFC 6749 2.3.1: form-encoded in the header (client IDs like sb-x|it!b1)
		if v, err := url.QueryUnescape(id); err == nil {
			id = v
		}
		if v, err := url.QueryUnescape(secret); err == nil {
			secret = v
		}
	} else {
		_ = r.ParseForm()
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	kind := m.Auth.clientKind(id, secret)
	if kind == "" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("WWW-Authenticate", `Basic realm="UAA/client"`)
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, map[string]string{"error": "unauthorized", "error_description": "Bad credentials"})
		return
	}
	if m.tokens == nil {
		m.tokens = map[string]issuedToken{}
	}
	m.tokenSerial++
	token := fmt.Sprintf("mock-%s-token-%d", kind, m.tokenSerial)
	m.tokens[token] = issuedToken{client: id, kind: kind}
	writeJSON(w, map[string]any{"access_token": token, "token_type": "bearer", "expires_in": 3600, "scope": "uaa.resource", "jti": token})
}

// caller identifies the credentials of a request: the user or client and
// the kind of key ("" when the credentials are not valid). Without Auth any
// credentials are valid (API).
func (m *Tenant) caller(r *http.Request) (name, kind string) {
	h := r.Header.Get("Authorization")
	if token, ok := strings.CutPrefix(h, "Bearer "); ok {
		if t, ok := m.tokens[token]; ok {
			return t.client, t.kind
		}
		if m.Auth == nil {
			return "", kindAPI
		}
		return "", ""
	}
	user, password, ok := r.BasicAuth()
	if !ok {
		if m.Auth == nil {
			return "", kindAPI
		}
		return "", ""
	}
	return user, m.Auth.clientKind(user, password)
}

// modifiedBy is the user a change is recorded for: the Basic Auth user or
// the OAuth client of the request.
func (m *Tenant) modifiedBy(r *http.Request) string {
	if name, _ := m.caller(r); name != "" {
		return name
	}
	return "mock-user"
}

// authorize checks the credentials of a request with Auth set: runtime
// endpoints need the runtime key, everything else the API key. It writes the
// tenant's answer and returns false when the request is refused: 401 for
// missing or wrong credentials, 403 for the other key (authenticated, but
// without the role).
func (m *Tenant) authorize(w http.ResponseWriter, r *http.Request, runtime bool) bool {
	if m.Auth == nil {
		return true
	}
	_, kind := m.caller(r)
	want := kindAPI
	if runtime {
		want = kindRuntime
	}
	switch kind {
	case want:
		return true
	case "":
		if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			w.Header().Set("WWW-Authenticate", `Bearer realm="cpi", error="invalid_token"`)
		} else {
			w.Header().Set("WWW-Authenticate", `Basic realm="cpi"`)
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Unauthorized"))
	default:
		w.WriteHeader(http.StatusForbidden)
		if runtime {
			_, _ = w.Write([]byte("Forbidden: the credentials have no role to call integration flows (ESBMessaging.send); use the runtime key (plan integration-flow)"))
		} else {
			_, _ = w.Write([]byte("Forbidden: the credentials have no role for the API; use the API key (plan api)"))
		}
	}
	return false
}
