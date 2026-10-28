package httpclnt

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRedact(t *testing.T) {
	for in, want := range map[string]string{
		`{"Password":"s3cret","User":"u"}`:                                     `{"Password":"***","User":"u"}`,
		`{"ClientSecret": "abc$def", "ClientId":"id"}`:                         `{"ClientSecret": "***", "ClientId":"id"}`,
		`{"access_token":"eyJ.x.y","token_type":"bearer"}`:                     `{"access_token":"***","token_type":"bearer"}`,
		`grant_type=client_credentials&client_secret=xyz&a=b`:                  `grant_type=client_credentials&client_secret=***&a=b`,
		"Authorization: Bearer eyJabc\nX: y":                                   "Authorization: ***\nX: y",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIE\n-----END RSA PRIVATE KEY-----": "-----BEGIN RSA PRIVATE KEY-----***-----END RSA PRIVATE KEY-----",
		`{"error":{"message":"not found"}}`:                                    `{"error":{"message":"not found"}}`,
	} {
		assert.Equal(t, want, Redact(in), in)
	}
}

// Every private key armor is masked: PEM (PKCS#8, RSA, EC, OpenSSH) and
// OpenPGP (security review of 0.35.1, second report, finding 5).
func TestRedactPrivateKeyBlocks(t *testing.T) {
	for _, kind := range []string{"PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY", "OPENSSH PRIVATE KEY", "ENCRYPTED PRIVATE KEY", "PGP PRIVATE KEY BLOCK"} {
		in := "error: -----BEGIN " + kind + "-----\nmade-up-key-material\n-----END " + kind + "-----"
		out := Redact(in)
		assert.NotContains(t, out, "made-up-key-material", kind)
		assert.Contains(t, out, "-----BEGIN "+kind+"-----***-----END "+kind+"-----", kind)
	}
}
