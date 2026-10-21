package ops

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeleteKeystoreEntry(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.Keystore = []cpitest.KeystoreEntry{{Alias: "partner_acme", NotAfter: time.Now().AddDate(1, 0, 0)},
		{Alias: "sap_cloud_root", Owner: "SAP", NotAfter: time.Now().AddDate(5, 0, 0)}}
	exe := mock.Executer()

	res, err := DeleteKeystoreEntry(exe, "partner_acme")
	require.NoError(t, err)
	assert.Equal(t, "partner_acme", res.Deleted.Alias)
	assert.Contains(t, mock.Requests(), "DELETE /api/v1/KeystoreEntries('"+cpi.HexAlias("partner_acme")+"')")
	assert.Len(t, mock.Keystore, 1)

	_, err = DeleteKeystoreEntry(exe, "partner_acme")
	assert.ErrorContains(t, err, "not found")
	_, err = DeleteKeystoreEntry(exe, "sap_cloud_root")
	assert.ErrorContains(t, err, "owned by SAP")
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
}

func TestGenerateKeyPairCSRAndChain(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	exe := mock.Executer()
	now := time.Now()

	_, err := GenerateKeyPair(exe, cpi.KeyPairRequest{Alias: "client"}, false, now)
	assert.ErrorContains(t, err, "common name")
	_, err = GenerateKeyPair(exe, cpi.KeyPairRequest{Alias: "client", CommonName: "x", KeyType: "RSA", KeySize: 1024}, false, now)
	assert.ErrorContains(t, err, "2048")

	res, err := GenerateKeyPair(exe, cpi.KeyPairRequest{Alias: "client", CommonName: "client.example.com", Country: "DE"}, false, now)
	require.NoError(t, err)
	assert.Equal(t, "CREATED", res.Action)
	require.NotNil(t, res.Entry)
	assert.Equal(t, "KeyPair", res.Entry.Type)
	assert.WithinDuration(t, now.AddDate(1, 0, 0), res.Entry.ValidNotAfter, time.Minute, "one year by default")
	_, err = GenerateKeyPair(exe, cpi.KeyPairRequest{Alias: "client", CommonName: "client.example.com"}, false, now)
	assert.ErrorContains(t, err, "already exists")

	csr, err := CertificateSigningRequest(exe, "client")
	require.NoError(t, err)
	assert.Contains(t, csr.PEM, "BEGIN CERTIFICATE REQUEST")
	assert.Equal(t, "CN=client.example.com", csr.Subject)

	chain, err := GetCertificateChain(exe, "client")
	require.NoError(t, err)
	require.Len(t, chain.Certificates, 1)
	assert.Equal(t, "CN=client.example.com", chain.Certificates[0].SubjectDN)

	// the CA's answer: leaf + root
	block, _ := pem.Decode([]byte(csr.PEM))
	req, err := x509.ParseCertificateRequest(block.Bytes)
	require.NoError(t, err)
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"}, NotBefore: now.Add(-time.Hour),
		NotAfter: now.AddDate(5, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	ca, _ := x509.ParseCertificate(caDER)
	leafDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{SerialNumber: big.NewInt(2), Subject: req.Subject,
		NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(2, 0, 0)}, ca, req.PublicKey, caKey)
	require.NoError(t, err)
	pemChain := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})...)

	up, err := UploadCertificateChain(exe, "client", pemChain)
	require.NoError(t, err)
	assert.Len(t, up.Certificates, 2)
	chain, err = GetCertificateChain(exe, "client")
	require.NoError(t, err)
	require.Len(t, chain.Certificates, 2)
	assert.Equal(t, "CN=Test CA", chain.Certificates[0].IssuerDN)

	_, err = UploadCertificateChain(exe, "client", []byte("-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"))
	assert.ErrorContains(t, err, "private keys are not accepted")

	// certificates have no chain
	mock.Keystore = append(mock.Keystore, cpitest.KeystoreEntry{Alias: "root", NotAfter: now.AddDate(1, 0, 0)})
	_, err = GetCertificateChain(exe, "root")
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
	_, err = UploadCertificateChain(exe, "root", pemChain)
	assert.ErrorContains(t, err, "belongs to a key pair")

	key, err := SSHPublicKey(exe, "client")
	require.NoError(t, err)
	assert.Contains(t, key, "ssh-rsa ")
}

func TestGenerateSSHKeyNeverReturnsThePassword(t *testing.T) {
	t.Setenv("CPICTL_TEST_SECRET", secret)
	mock := cpitest.NewTenant(t, nil)
	// a real encrypted key: the tenant (and the mock) read it, sent as base64
	res, err := GenerateSSHKey(mock.Executer(), cpi.KeyPairRequest{Alias: "sftp", CommonName: "sftp"}, sshKeyFile(t, secret),
		&SecretSource{Env: "CPICTL_TEST_SECRET"}, false, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "SSHKey", res.Entry.Type)
	require.Len(t, mock.SSHKeyRequests, 1)
	assert.Equal(t, true, mock.SSHKeyRequests[0]["Password"], "password sent")
	assert.Equal(t, true, mock.SSHKeyRequests[0]["SSHFile"], "key file sent")
	out, _ := json.Marshal(res)
	assert.NotContains(t, string(out), secret)
}

func TestKeystoreHistory(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.KeystoreHistory = []cpitest.KeystoreEntry{{Alias: "client", NotAfter: time.Now().AddDate(-1, 0, 0)}, {Alias: "other", NotAfter: time.Now()}}
	all, err := KeystoreHistory(mock.Executer(), "")
	require.NoError(t, err)
	assert.Len(t, all, 2)
	one, err := KeystoreHistory(mock.Executer(), "client")
	require.NoError(t, err)
	require.Len(t, one, 1)
	assert.Equal(t, 1, one[0].ActivationSequenceNumber)
}

func TestPGP(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.PGPKeys = []cpitest.PGPKey{{Keyring: "pubring", ID: "1", KeyID: "AB12", UserIDs: []string{"Partner <pgp@partner.example>"}, SubKeyIDs: []string{"CD34"}},
		{Keyring: "secring", ID: "2", KeyID: "EF56", UserIDs: []string{"Us <pgp@us.example>"}}}
	exe := mock.Executer()
	o, err := ListPGP(exe)
	require.NoError(t, err)
	assert.Len(t, o.Keyrings, 2)
	require.Len(t, o.PublicKeys, 1)
	assert.Equal(t, "Partner <pgp@partner.example>", o.PublicKeys[0].PrimaryUserID)
	assert.Equal(t, []string{"Partner <pgp@partner.example>"}, o.PublicKeys[0].UserIDs)
	require.Len(t, o.PublicKeys[0].SubKeys, 1)
	assert.Equal(t, "CD34", o.PublicKeys[0].SubKeys[0].KeyID)
	require.Len(t, o.SecretKeys, 1)

	data, err := ExportPGPPublicKeyring(exe)
	require.NoError(t, err)
	assert.Empty(t, data, "204 for an empty keyring")
	_, err = ImportPGPPublicKeyring(exe, []byte("-----BEGIN PGP PRIVATE KEY BLOCK-----"))
	assert.ErrorContains(t, err, "secret key")
	res, err := ImportPGPPublicKeyring(exe, []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----\nx\n"))
	require.NoError(t, err)
	assert.Equal(t, 39, res.Bytes)
	data, err = ExportPGPPublicKeyring(exe)
	require.NoError(t, err)
	assert.Contains(t, string(data), "PUBLIC KEY BLOCK")
}

func TestOAuth2AuthorizationCodesInCredentials(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.OAuth2AuthCodes = []cpitest.OAuth2AuthCode{{Name: "Graph_Delegated", Provider: "Microsoft 365", ClientID: "app", ClientSecret: secret,
		RefreshToken: "refresh-" + secret, LastModified: time.Now()}}
	all, err := ListCredentials(mock.Executer(), "")
	require.NoError(t, err)
	require.Len(t, all.OAuth2AuthorizationCodes, 1)
	c := all.OAuth2AuthorizationCodes[0]
	assert.Equal(t, "Graph_Delegated", c.Name)
	assert.True(t, c.RefreshTokenExists)
	require.NotNil(t, c.LastModifiedTime)
	out, _ := json.Marshal(all)
	assert.NotContains(t, string(out), secret)

	only, err := ListCredentials(mock.Executer(), cpi.KindOAuth2AuthCode)
	require.NoError(t, err)
	assert.Len(t, only.OAuth2AuthorizationCodes, 1)
	assert.Empty(t, only.UserCredentials)
}

func TestAccessPolicies(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	exe := mock.Executer()
	ref := cpi.ArtifactReference{Name: "Orders", Type: "INTEGRATION_FLOW", ConditionAttribute: "Name", ConditionValue: "Orders_.*", ConditionType: "REGULAR_EXPRESSION"}

	_, err := CreateAccessPolicy(exe, "", "", nil)
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
	_, err = CreateAccessPolicy(exe, "OrdersTeam", "", []cpi.ArtifactReference{{Name: "x"}})
	assert.ErrorContains(t, err, "condition")

	created, err := CreateAccessPolicy(exe, "OrdersTeam", "Orders flows", []cpi.ArtifactReference{ref})
	require.NoError(t, err)
	require.NotEmpty(t, created.ID)

	policies, err := ListAccessPolicies(exe)
	require.NoError(t, err)
	require.Len(t, policies, 1)
	assert.Equal(t, "OrdersTeam", policies[0].RoleName)
	require.Len(t, policies[0].ArtifactReferences, 1)
	assert.Equal(t, "Orders_.*", policies[0].ArtifactReferences[0].ConditionValue)

	ref.Name, ref.ConditionValue, ref.ConditionType = "Returns", "Returns_In", "EXACT_MATCH"
	added, err := AddArtifactReference(exe, created.ID, ref)
	require.NoError(t, err)
	assert.Contains(t, mock.Requests(), "POST /api/v1/AccessPolicies("+created.ID+")/ArtifactReferences")
	_, err = DeleteArtifactReference(exe, added.ID)
	require.NoError(t, err)
	_, err = DeleteArtifactReference(exe, added.ID)
	assert.ErrorContains(t, err, "not found")

	del, err := DeleteAccessPolicy(exe, created.ID)
	require.NoError(t, err)
	assert.Contains(t, del.Detail, "1 artifact reference")
	_, err = DeleteAccessPolicy(exe, created.ID)
	assert.ErrorContains(t, err, "not found")
	assert.Empty(t, mock.AccessPolicies)
}

func TestAuditLogs(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	now := time.Now().UTC().Truncate(time.Second)
	mock.AuditLogs = []cpitest.AuditEntry{
		{ChangeID: "1", Action: "Deploy", UserName: "dev@example.com", ObjName: "Orders_In", Timestamp: now.Add(-48 * time.Hour)},
		{ChangeID: "2", Action: "Update", UserName: "dev@example.com", ObjName: "Orders_In", Timestamp: now.Add(-time.Hour)},
		{ChangeID: "3", Action: "Delete", UserName: "ops@example.com", ObjName: "ERP_User", Timestamp: now.Add(-time.Minute)},
	}
	exe := mock.Executer()
	res, err := ListAuditLogs(exe, now.Add(-24*time.Hour), 0, "", "")
	require.NoError(t, err)
	require.Len(t, res.Entries, 2)
	assert.Equal(t, "3", res.Entries[0].ChangeID, "newest first")
	assert.Equal(t, now.Add(-time.Minute), res.Entries[0].Timestamp)

	res, err = ListAuditLogs(exe, time.Time{}, 0, "dev@example.com", "orders")
	require.NoError(t, err)
	assert.Len(t, res.Entries, 2)

	mock.ForbidPaths = []string{"/api/v1/AuditLogs"}
	res, err = ListAuditLogs(exe, time.Time{}, 10, "", "")
	require.NoError(t, err, "a missing role is a result, not an error")
	assert.Equal(t, TraceMissingRole, res.Status)
	assert.Contains(t, res.Hint, "403")
	assert.Empty(t, res.Entries)
}
