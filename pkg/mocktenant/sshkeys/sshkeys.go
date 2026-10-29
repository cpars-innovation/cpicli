// Package sshkeys lets the mock tenant read uploaded SSH keys
// (SSHKeyGenerationRequests with an SSHFile). Import it for its side effect
// where the mock must accept SSH keys; without it, the mock tenant does not
// link golang.org/x/crypto/ssh.
package sshkeys

import (
	"strings"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"golang.org/x/crypto/ssh"
)

func init() {
	cpitest.SSHPublicKey = func(key []byte, passphrase string) (string, error) {
		var signer ssh.Signer
		var err error
		if passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(key)
		}
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), nil
	}
}
