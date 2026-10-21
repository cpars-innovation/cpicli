package secretscan

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValue(t *testing.T) {
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	for _, c := range []struct{ name, value, kind string }{
		{"ReceiverPassword", "Welcome1", KindNamed},
		{"ClientSecret", "s3cr3tvalue", KindNamed},
		{"API_Key", "abc123def456", KindNamed},
		{"Anything", basic, KindCredentials},
		{"Anything", "Bearer abcdefghijklmnop", KindToken},
		{"Anything", jwt, KindToken},
		{"Anything", "AKIAIOSFODNN7EXAMPLE", KindToken},
		{"Anything", "-----BEGIN RSA PRIVATE KEY-----\nMIIE...", KindPrivateKey},
		{"Webhook", "https://host/hook?token=abc", KindURL},
		{"Api_Url", "https://user:pw@host/api", KindURL},
	} {
		h := Value(c.name, c.value)
		if assert.NotNil(t, h, c.name+"="+c.value) {
			assert.Equal(t, c.kind, h.Kind, c.name)
			assert.Len(t, h.Fingerprint, 12)
			assert.NotContains(t, h.Reason, c.value)
		}
	}
	for _, c := range [][2]string{
		{"CredentialName", "Finance_Basic"},      // a reference
		{"PasswordAlias", "Finance_Basic"},       // a reference
		{"TokenURL", "https://host/oauth/token"}, // an address
		{"Token_Path", "/oauth/token"},
		{"Password", "{{PASSWORD}}"}, // placeholder
		{"Password", ""},
		{"Password", "********"},
		{"Region", "eu10"},
		{"Anything", "Basic notbase64!!"},
		{"TokenLifetime", "3600"},
	} {
		assert.Nil(t, Value(c[0], c[1]), c[0]+"="+c[1])
	}
}

func TestText(t *testing.T) {
	hits := Text(`<cfg><user>u</user><password>Pa55word!</password>
api_key = "abcd1234efgh5678"
"client_secret": "${secret}"
<ns:accessToken>tok_1234567</ns:accessToken>
-----BEGIN OPENSSH PRIVATE KEY-----
</cfg>`)
	var kinds []string
	for _, h := range hits {
		kinds = append(kinds, h.Kind+": "+h.Reason)
	}
	assert.ElementsMatch(t, []string{
		"private-key: a private key (BEGIN OPENSSH PRIVATE KEY)",
		"credentials: a value assigned to api_key",
		"credentials: a value assigned to password",
		"credentials: a value assigned to ns:accesstoken",
	}, kinds)
	assert.Empty(t, Text("<cfg><password>${property.pwd}</password></cfg>\nplain text"))
}

// HTTP authorization values inside text (#47): after an Authorization key
// or header only, Basic only when it decodes to user:password.
func TestTextAuthorization(t *testing.T) {
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("erp:Welcome1"))
	for _, text := range []string{
		`{"authorization": "Bearer abcdefgh12345678"}`,
		"GET / HTTP/1.1\nAuthorization: Bearer abcdefgh12345678\n",
		"authorization=Bearer abcdefgh12345678",
		"Proxy-Authorization: " + basic,
		"<Authorization>Bearer abcdefgh12345678</Authorization>",
	} {
		hits := Text(text)
		require.Len(t, hits, 1, text)
		assert.Contains(t, hits[0].Reason, "authorization value", text)
	}
	// the same fingerprint as the whole value through Value
	assert.Equal(t, Value("h", "Bearer abcdefgh12345678").Fingerprint, Text("Authorization: Bearer abcdefgh12345678")[0].Fingerprint)
	for _, text := range []string{
		"Authorization: Basic bm90LWEtcGFpcg==", // decodes, but no user:password
		"Authorization: Bearer ${property.token}",
		"Authorization: Bearer xxxxxxxxxx",
		"we use basic authentication; the bearer mechanism is described elsewhere",
	} {
		assert.Empty(t, Text(text), text)
	}
}

func TestFingerprintStable(t *testing.T) {
	assert.Equal(t, Fingerprint("x"), Fingerprint("x"))
	assert.NotEqual(t, Fingerprint("x"), Fingerprint("y"))
}
