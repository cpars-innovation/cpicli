// Package secretscan recognises security material in places it does not
// belong: Partner Directory parameters, externalised configuration
// parameters, configure files. Security material belongs in the tenant's
// secure store (user credentials, secure parameters, OAuth2 credentials) and
// keystore, where flows read it by name.
//
// A finding never carries the value: only the reason and a short
// fingerprint, so reports can be shared and compared across runs.
package secretscan

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"regexp"
	"strings"
)

// Kinds of security material.
const (
	KindPrivateKey  = "private-key"
	KindToken       = "token"
	KindCredentials = "credentials"
	KindURL         = "url-with-credentials"
	KindNamed       = "named-like-a-secret"
)

// Hit is security material found in a value.
type Hit struct {
	Kind string `json:"kind"`
	// Reason says what was found, without the value.
	Reason string `json:"reason"`
	// Fingerprint is the first 12 hex digits of the value's SHA-256: the
	// same value has the same fingerprint, the value cannot be read from it.
	Fingerprint string `json:"fingerprint"`
}

var (
	rePrivateKey = regexp.MustCompile(`-----BEGIN ((RSA|EC|DSA|OPENSSH|ENCRYPTED|PGP) )?PRIVATE KEY( BLOCK)?-----`)
	reJWT        = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)
	reAuthValue  = regexp.MustCompile(`(?i)^\s*(basic|bearer)\s+([A-Za-z0-9+/=._~-]{8,})\s*$`)
	// token formats of common providers (prefixes they document)
	reKnownToken = regexp.MustCompile(`\b(AKIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{40,}|xox[abprs]-[A-Za-z0-9-]{10,}|AIza[0-9A-Za-z_-]{35}|sk_live_[0-9A-Za-z]{20,}|glpat-[0-9A-Za-z_-]{20})\b`)
	// key = "value" pairs in text (properties, XML attributes, JSON)
	reAssignment = regexp.MustCompile(`(?i)["']?\b(password|passwd|pwd|secret|client_?secret|api_?key|access_?token|refresh_?token|private_?key)\b["']?\s*[=:]\s*["']?([^"'\s,;<>{}]{4,})`)
	// <password>value</password> in XML
	reXMLElement = regexp.MustCompile(`(?i)<((?:\w+:)?(?:password|passwd|pwd|secret|client_?secret|api_?key|access_?token|refresh_?token|private_?key))>\s*([^<\s]{4,})\s*</`)
	// names of parameters that hold security material
	reSecretName = regexp.MustCompile(`(?i)(password|passwd|pwd|secret|api_?key|apikey|token|private_?key|client_?secret|passphrase)`)
	// names that refer to security material by name: credential aliases,
	// key aliases, token URLs
	reReferenceName = regexp.MustCompile(`(?i)(_ref|_alias|alias|_name|name|credential|credentials|_id|url|uri|endpoint|path|host|address|type|header|expiry|expires|lifetime|validity|length|method|mode)$`)
	rePlaceholder   = regexp.MustCompile(`^(\{\{.*\}\}|\$\{.*\}|<.*>|\*+|x+|-|n/?a|none|null|true|false|changeme|dummy|tbd)$`)
)

// secretQueryParams are query parameters that carry a secret.
var secretQueryParams = map[string]bool{"token": true, "access_token": true, "signature": true, "sig": true, "key": true,
	"apikey": true, "api_key": true, "password": true, "pwd": true, "secret": true, "client_secret": true, "code": true}

// Fingerprint is the short hash of a value.
func Fingerprint(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])[:12]
}

// URLSecret reports whether v is an absolute URL and whether it carries a
// secret: a password in the user info or a secret query parameter.
func URLSecret(v string) (isURL, carries bool) {
	u, err := url.Parse(strings.TrimSpace(v))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false, false
	}
	if _, ok := u.User.Password(); ok {
		return true, true
	}
	for k := range u.Query() {
		if secretQueryParams[strings.ToLower(k)] {
			return true, true
		}
	}
	return true, false
}

// ReferenceName reports whether a parameter name refers to security
// material by name (CredentialName, KeyAlias, TokenURL) rather than holding
// it.
func ReferenceName(name string) bool { return reReferenceName.MatchString(strings.TrimSpace(name)) }

// SecretName reports whether a parameter name says it holds security
// material (Password, ApiKey, ClientSecret) and does not only refer to it.
func SecretName(name string) bool { return reSecretName.MatchString(name) && !ReferenceName(name) }

// Value checks a named value (a parameter): its content, then its name.
func Value(name, value string) *Hit {
	v := strings.TrimSpace(value)
	if v == "" || rePlaceholder.MatchString(strings.ToLower(v)) {
		return nil
	}
	if h := content(v); h != nil {
		return h
	}
	if isURL, carries := URLSecret(v); isURL {
		if carries {
			return hit(KindURL, "a URL with credentials (user info or a secret query parameter)", v)
		}
		return nil
	}
	if !SecretName(name) {
		return nil
	}
	return hit(KindNamed, "the name says it holds a password, token or key and the value is not a reference", v)
}

// Text checks free text (a binary parameter's content, a file): only
// formats that are security material whatever their name, and key = value
// pairs that assign one.
func Text(text string) []Hit {
	var out []Hit
	seen := map[string]bool{}
	add := func(h *Hit) {
		if h != nil && !seen[h.Fingerprint] {
			seen[h.Fingerprint] = true
			out = append(out, *h)
		}
	}
	if m := rePrivateKey.FindString(text); m != "" {
		add(hit(KindPrivateKey, "a private key ("+strings.Trim(m, "- ")+")", m+text))
	}
	for _, m := range reJWT.FindAllString(text, -1) {
		add(hit(KindToken, "a JSON web token", m))
	}
	for _, m := range reKnownToken.FindAllString(text, -1) {
		add(hit(KindToken, "an access key of a known provider", m))
	}
	for _, m := range append(reAssignment.FindAllStringSubmatch(text, -1), reXMLElement.FindAllStringSubmatch(text, -1)...) {
		v := strings.TrimSpace(m[2])
		if rePlaceholder.MatchString(strings.ToLower(v)) || strings.HasPrefix(v, "${") || strings.HasPrefix(v, "{{") {
			continue
		}
		add(hit(KindCredentials, "a value assigned to "+strings.ToLower(m[1]), v))
	}
	return out
}

// content recognises a value that is security material whatever its name.
func content(v string) *Hit {
	if rePrivateKey.MatchString(v) {
		return hit(KindPrivateKey, "a private key", v)
	}
	if reJWT.MatchString(v) {
		return hit(KindToken, "a JSON web token", v)
	}
	if reKnownToken.MatchString(v) {
		return hit(KindToken, "an access key of a known provider", v)
	}
	if m := reAuthValue.FindStringSubmatch(v); m != nil {
		if strings.EqualFold(m[1], "basic") {
			if dec, err := base64.StdEncoding.DecodeString(m[2]); err != nil || !strings.Contains(string(dec), ":") {
				return nil
			}
			return hit(KindCredentials, "an HTTP Basic authorization value (user and password)", v)
		}
		return hit(KindToken, "an HTTP Bearer authorization value", v)
	}
	return nil
}

func hit(kind, reason, v string) *Hit {
	return &Hit{Kind: kind, Reason: reason, Fingerprint: Fingerprint(v)}
}
