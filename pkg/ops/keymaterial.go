package ops

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"golang.org/x/crypto/ssh"
)

// Kinds of key material in a credentials file (CredentialResult.Kind).
const (
	KindKeyPair      = "key-pair"
	KindSSHKey       = "ssh-key"
	KindPGPSecretKey = "pgp-secret-key"
)

// KeyPairSpec is a key pair (private key with its certificate chain) to
// import into the system keystore, as PKCS#12 (.p12 / .pfx). Vaults hold it
// as base64 text (e.g. an Azure Key Vault certificate read as a secret); a
// file may be binary or base64. Password is left out for a PKCS#12 without
// one (Azure Key Vault exports it so; the tenant takes an empty password).
type KeyPairSpec struct {
	Alias    string        `yaml:"alias"`
	PKCS12   SecretSource  `yaml:"pkcs12"`
	Password *SecretSource `yaml:"password,omitempty"`
}

// SSHKeySpec is an SSH private key (OpenSSH or PEM) to import into the
// system keystore. The tenant wraps it in a certificate named CommonName
// (default: the alias).
type SSHKeySpec struct {
	Alias      string        `yaml:"alias"`
	CommonName string        `yaml:"commonName,omitempty"`
	PrivateKey SecretSource  `yaml:"privateKey"`
	Passphrase *SecretSource `yaml:"passphrase,omitempty"` // only for an encrypted key
}

// PGPSecretKeySpec is one or more OpenPGP secret keys (ASCII-armored,
// binary, or base64 of either) to add to the tenant's secret keyring, with
// their passphrase. Name labels the entry in results.
type PGPSecretKeySpec struct {
	Name       string       `yaml:"name"`
	Keys       SecretSource `yaml:"keys"`
	Passphrase SecretSource `yaml:"passphrase"`
}

// keyMaterialSpec is a key material spec of a credentials file.
type keyMaterialSpec interface {
	resolve() (*resolvedKeyMaterial, error)
}

// resolvedKeyMaterial holds the secrets read from their sources, ready to
// deploy. It is never logged or returned.
type resolvedKeyMaterial struct {
	kind, name string
	data       []byte
	secret     string
	commonName string
}

func (s KeyPairSpec) resolve() (*resolvedKeyMaterial, error) {
	if s.Alias == "" {
		return nil, output.Usagef("key pair: alias is required")
	}
	raw, err := s.PKCS12.resolveBytes("key pair " + s.Alias + " PKCS#12")
	if err != nil {
		return nil, err
	}
	data := binarySecret(raw)
	// PKCS#12 is DER: a SEQUENCE (0x30)
	if len(data) < 4 || data[0] != 0x30 {
		return nil, output.Usagef("key pair %s: the PKCS#12 source is neither a .p12/.pfx file nor its base64", s.Alias)
	}
	pw := ""
	if s.Password != nil {
		if pw, err = s.Password.Resolve("key pair " + s.Alias + " password"); err != nil {
			return nil, err
		}
	}
	return &resolvedKeyMaterial{kind: KindKeyPair, name: s.Alias, data: data, secret: pw}, nil
}

func (s SSHKeySpec) resolve() (*resolvedKeyMaterial, error) {
	if s.Alias == "" {
		return nil, output.Usagef("SSH key: alias is required")
	}
	raw, err := s.PrivateKey.resolveBytes("SSH key " + s.Alias + " private key")
	if err != nil {
		return nil, err
	}
	key := binarySecret(raw)
	if !bytes.Contains(key, []byte("PRIVATE KEY")) {
		return nil, output.Usagef("SSH key %s: the private key source is not an OpenSSH or PEM private key (or its base64)", s.Alias)
	}
	if !bytes.HasSuffix(key, []byte("\n")) {
		key = append(key, '\n')
	}
	m := &resolvedKeyMaterial{kind: KindSSHKey, name: s.Alias, data: key, commonName: s.CommonName}
	if m.commonName == "" {
		m.commonName = s.Alias
	}
	if s.Passphrase != nil {
		if m.secret, err = s.Passphrase.Resolve("SSH key " + s.Alias + " passphrase"); err != nil {
			return nil, err
		}
	}
	if _, err := sshPublicKey(key, m.secret); err != nil {
		return nil, output.Usagef("SSH key %s: %v", s.Alias, err)
	}
	return m, nil
}

func (s PGPSecretKeySpec) resolve() (*resolvedKeyMaterial, error) {
	if s.Name == "" {
		return nil, output.Usagef("PGP secret key: name is required")
	}
	raw, err := s.Keys.resolveBytes("PGP secret key " + s.Name)
	if err != nil {
		return nil, err
	}
	data := binarySecret(raw)
	if bytes.Contains(data, []byte("-----BEGIN PGP PUBLIC KEY BLOCK")) && !bytes.Contains(data, []byte("PRIVATE KEY BLOCK")) {
		return nil, output.Usagef("PGP secret key %s: the source holds public keys only (use cpictl pgp import-public for those)", s.Name)
	}
	pass, err := s.Passphrase.Resolve("PGP secret key " + s.Name + " passphrase")
	if err != nil {
		return nil, err
	}
	return &resolvedKeyMaterial{kind: KindPGPSecretKey, name: s.Name, data: data, secret: pass}, nil
}

// deploy writes the key material, unless the tenant already has the same:
// a key pair with the same certificate fingerprint, an SSH key with the same
// public key, PGP keys the tenant reports unchanged. A sync from a vault
// that runs every night therefore does not rewrite the keystore (and its
// history) each time.
func (m *resolvedKeyMaterial) deploy(exe *httpclnt.HTTPExecuter, dryRun bool) (*CredentialResult, error) {
	res := &CredentialResult{Kind: m.kind, Name: m.name}
	if dryRun {
		res.Action = "WOULD_DEPLOY"
		return res, nil
	}
	fail := func(err error) (*CredentialResult, error) {
		res.Action, res.Err, res.Error = "FAILED", err, err.Error()
		return res, err
	}
	sec := cpi.NewSecurity(exe)
	switch m.kind {
	case KindKeyPair, KindSSHKey:
		existing, err := systemEntry(sec, m.name)
		if err != nil {
			return fail(err)
		}
		if existing != nil && strings.EqualFold(existing.Owner, "SAP") {
			return fail(fmt.Errorf("keystore entry %s is owned by SAP and cannot be replaced", m.name))
		}
		if existing != nil {
			same, err := m.sameAs(sec, existing)
			if err != nil {
				return fail(err)
			}
			if same {
				res.Action = "UNCHANGED"
				return res, nil
			}
		}
		if m.kind == KindKeyPair {
			err = sec.ImportKeyPair(m.name, m.data, m.secret, existing != nil)
		} else {
			req := cpi.SSHKeyRequest{KeyPairRequest: cpi.KeyPairRequest{Alias: m.name, KeyType: "RSA", CommonName: m.commonName},
				SSHFile: string(m.data), Password: m.secret}
			err = sec.GenerateSSHKey(req, existing != nil)
		}
		if err != nil {
			return fail(err)
		}
		res.Action = map[bool]string{true: "UPDATED", false: "CREATED"}[existing != nil]
	case KindPGPSecretKey:
		statuses, err := sec.AddPgpSecretKeys(m.data, m.secret)
		if err != nil {
			return fail(err)
		}
		// the tenant adds new keys and leaves a key ID it has ("not
		// imported: an entry with the same keyId already exists"): a key
		// cannot change under its ID
		res.Action = "UNCHANGED"
		for _, st := range statuses {
			switch {
			case strings.EqualFold(st.Status, "added"):
				res.Action = "CREATED"
			case strings.Contains(strings.ToLower(st.Details), "already exists"):
			default:
				return fail(fmt.Errorf("PGP key %s %s: %s", st.KeyID, st.Status, st.Details))
			}
		}
		if len(statuses) == 0 {
			res.Action = "UPDATED"
		}
	}
	return res, nil
}

// sameAs reports whether an existing keystore entry already holds this key.
func (m *resolvedKeyMaterial) sameAs(sec *cpi.Security, existing *cpi.KeystoreEntry) (bool, error) {
	if m.kind == KindKeyPair {
		// the tenant reads the PKCS#12 without importing it (409 with the
		// certificate): no PKCS#12 parser here, any encryption works
		p, err := sec.PreviewKeyPair(m.name, m.data, m.secret, true)
		if err != nil {
			return false, err
		}
		return existing.FingerprintSha256 != "" && normFingerprint(p.FingerprintSha256) == normFingerprint(existing.FingerprintSha256), nil
	}
	want, err := sshPublicKey(m.data, m.secret)
	if err != nil {
		return false, err
	}
	have, err := sec.SSHPublicKey(m.name)
	if err != nil {
		return false, nil // not an SSH key (yet): replace it
	}
	return sshKeyField(have) == sshKeyField(want), nil
}

func normFingerprint(f string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(f, ":", ""), " ", ""))
}

// sshKeyField is the type and key of an authorized_keys line, without its
// comment.
func sshKeyField(line string) string {
	f := strings.Fields(line)
	if len(f) < 2 {
		return line
	}
	return f[0] + " " + f[1]
}

// sshPublicKey returns the public key of an SSH private key in
// authorized_keys format.
func sshPublicKey(key []byte, passphrase string) (string, error) {
	var signer ssh.Signer
	var err error
	if passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(passphrase))
	} else {
		signer, err = ssh.ParsePrivateKey(key)
	}
	if err != nil {
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) {
			return "", fmt.Errorf("the private key is encrypted: give its passphrase")
		}
		return "", fmt.Errorf("the private key cannot be read: %v", err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), nil
}

// binarySecret returns binary content given as base64 text decoded (vaults
// and environment variables hold text); PEM, armored and binary content is
// returned as it is.
func binarySecret(raw []byte) []byte {
	t := strings.TrimSpace(string(raw))
	if t == "" || strings.HasPrefix(t, "-----BEGIN") {
		return raw
	}
	compact := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, t)
	if dec, err := base64.StdEncoding.DecodeString(compact); err == nil && len(dec) > 0 {
		return dec
	}
	return raw
}

// resolveBytes reads a secret like Resolve, but a file as it is (binary
// content keeps its last bytes).
func (s SecretSource) resolveBytes(what string) ([]byte, error) {
	if s.File != "" && s.Env == "" && s.Stdin == nil {
		b, err := os.ReadFile(s.File)
		if err != nil {
			return nil, output.Usagef("%s: %v", what, err)
		}
		if len(bytes.TrimSpace(b)) == 0 {
			return nil, output.Usagef("%s: secret is empty", what)
		}
		return b, nil
	}
	if s.Stdin != nil && s.Env == "" && s.File == "" {
		b, err := io.ReadAll(s.Stdin)
		if err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(b)) == 0 {
			return nil, output.Usagef("%s: secret is empty", what)
		}
		return b, nil
	}
	v, err := s.Resolve(what)
	return []byte(v), err
}
