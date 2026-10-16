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
