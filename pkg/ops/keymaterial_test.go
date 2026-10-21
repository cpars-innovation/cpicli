package ops

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

const keyMaterialYAML = `keyPairs:
  - alias: partner_signing
    pkcs12: {env: T_KP}
    password: {env: T_KP_PW}
sshKeys:
  - alias: sftp_partner
    privateKey: {env: T_SSH}
    passphrase: {env: T_SSH_PW}
pgpSecretKeys:
  - name: payroll
    keys: {env: T_PGP}
    passphrase: {env: T_PGP_PW}
`

func sshKeyFile(t *testing.T, passphrase string) []byte {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	b, err := ssh.MarshalPrivateKeyWithPassphrase(k, "test", []byte(passphrase))
	require.NoError(t, err)
	return pem.EncodeToMemory(b)
}

// Key pairs, SSH keys and PGP secret keys from a vault (environment
// variables): written once, left alone while the vault holds the same, a
// new key pair replaces the old, nothing is written when a secret is
// missing, and no secret is in the result.
func TestApplyKeyMaterial(t *testing.T) {
	m := cpitest.NewTenant(t, nil)
	dir := t.TempDir()
	file := filepath.Join(dir, "credentials.yml")
	require.NoError(t, os.WriteFile(file, []byte(keyMaterialYAML), 0o600))
	p12 := append([]byte{0x30, 0x82, 0x01, 0x02}, []byte("pkcs12-bytes")...)
	sshKey := sshKeyFile(t, "ssh-secret-pass")
	pgp := "-----BEGIN PGP PRIVATE KEY BLOCK-----\n\nlQPGBGfake\n-----END PGP PRIVATE KEY BLOCK-----\n"
	env := map[string]string{"T_KP": base64.StdEncoding.EncodeToString(p12), "T_KP_PW": "kp-secret-pass",
		"T_SSH": base64.StdEncoding.EncodeToString(sshKey), "T_SSH_PW": "ssh-secret-pass", "T_PGP": pgp, "T_PGP_PW": "pgp-secret-pass"}
	for k, v := range env {
		t.Setenv(k, v)
	}
	f, err := LoadCredentialsFile(file)
	require.NoError(t, err)

	actions := func(res *CredentialsApplyResult) map[string]string {
		out := map[string]string{}
		for _, r := range res.Results {
			out[r.Kind+"/"+r.Name] = r.Action
		}
		return out
	}
	dry, err := ApplyCredentials(m.Executer(), f, true)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"key-pair/partner_signing": "WOULD_DEPLOY", "ssh-key/sftp_partner": "WOULD_DEPLOY", "pgp-secret-key/payroll": "WOULD_DEPLOY"}, actions(dry))
	assert.Zero(t, m.KeyPairImports, "a dry run writes nothing")

	res, err := ApplyCredentials(m.Executer(), f, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"key-pair/partner_signing": "CREATED", "ssh-key/sftp_partner": "CREATED", "pgp-secret-key/payroll": "CREATED"}, actions(res))
	out, _ := json.Marshal(res)
	for _, v := range env {
		assert.NotContains(t, string(out), v)
	}
	assert.Equal(t, 1, m.KeyPairImports)

	again, err := ApplyCredentials(m.Executer(), f, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"key-pair/partner_signing": "UNCHANGED", "ssh-key/sftp_partner": "UNCHANGED", "pgp-secret-key/payroll": "UNCHANGED"}, actions(again),
		"a nightly sync does not rewrite the keystore")
	assert.Equal(t, 1, m.KeyPairImports)

	// the vault holds a renewed key pair: it replaces the old one
	t.Setenv("T_KP", base64.StdEncoding.EncodeToString(append(p12, []byte("-renewed")...)))
	rotated, err := ApplyCredentials(m.Executer(), f, false)
	require.NoError(t, err)
	assert.Equal(t, "UPDATED", actions(rotated)["key-pair/partner_signing"])

	// a wrong PKCS#12 password is the tenant's error, named per entry
	t.Setenv("T_KP_PW", "wrong")
	_, err = ApplyCredentials(m.Executer(), f, false)
	assert.ErrorContains(t, err, "1 of 3")

	// a missing secret: nothing is written
	os.Unsetenv("T_SSH_PW")
	before := m.KeyPairImports
	_, err = ApplyCredentials(m.Executer(), f, false)
	assert.ErrorContains(t, err, "T_SSH_PW is not set")
	assert.Equal(t, before, m.KeyPairImports)
}

// Sources a vault gives: base64 of binary content, PEM and armored text as
// they are, and a binary file read as it is (a last byte 0x0a stays).
func TestBinarySecret(t *testing.T) {
	bin := []byte{0x30, 0x82, 0x00, 0x0a}
	assert.Equal(t, bin, binarySecret([]byte(base64.StdEncoding.EncodeToString(bin)+"\n")))
	assert.Equal(t, bin, binarySecret(bin))
	pemText := []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----\n")
	assert.Equal(t, pemText, binarySecret(pemText))

	path := filepath.Join(t.TempDir(), "kp.p12")
	require.NoError(t, os.WriteFile(path, bin, 0o600))
	got, err := SecretSource{File: path}.resolveBytes("x")
	require.NoError(t, err)
	assert.Equal(t, bin, got)

	_, err = KeyPairSpec{Alias: "a", PKCS12: SecretSource{File: path}, Password: &SecretSource{File: path}}.resolve()
	assert.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("not a key"), 0o600))
	_, err = KeyPairSpec{Alias: "a", PKCS12: SecretSource{File: path}, Password: &SecretSource{File: path}}.resolve()
	assert.ErrorContains(t, err, "neither a .p12/.pfx file nor its base64")
	_, err = SSHKeySpec{Alias: "s", PrivateKey: SecretSource{File: path}}.resolve()
	assert.ErrorContains(t, err, "not an OpenSSH or PEM private key")
}
