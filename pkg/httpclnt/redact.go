package httpclnt

import "regexp"

// reSecret finds the values of secret fields in JSON, form and header text:
// passwords, client secrets, tokens, secure parameters, private keys.
var reSecret = regexp.MustCompile(`(?i)("(?:[a-z_]*password|clientsecret|client_secret|securereparam|secureparam|access_token|refresh_token|id_token|token|privatekey|private_key)"\s*:\s*")[^"]*(")` +
	`|((?:password|client_secret|access_token|refresh_token|token)=)[^&\s"]*` +
	`|((?:authorization|x-csrf-token)\s*:\s*)[^\r\n"]+` +
	`|(-----BEGIN [A-Z ]*PRIVATE KEY(?: BLOCK)?-----)[\s\S]*?(-----END [A-Z ]*PRIVATE KEY(?: BLOCK)?-----)`)

// Redact replaces secret values in s with "***" so that logs and error
// details never carry them.
func Redact(s string) string {
	return reSecret.ReplaceAllString(s, "${1}${3}${4}${5}***${2}${6}")
}
