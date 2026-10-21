package cpi

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// Security reads and writes security material (credentials, keystore).
// Secrets are only ever sent, never returned: the API does not return
// passwords, secrets or secure parameter values, and this client does not map
// them into results either.
type Security struct {
	exe *httpclnt.HTTPExecuter
}

// NewSecurity returns a Security Content client.
func NewSecurity(exe *httpclnt.HTTPExecuter) *Security {
	return &Security{exe: exe}
}

// keyLiteral is an OData key literal that is safe in a URL path.
func keyLiteral(s string) string {
	return url.PathEscape(odataString(s))
}

// HexAlias encodes a keystore alias as the API expects (hex of the UTF-8 bytes).
func HexAlias(alias string) string {
	return strings.ToUpper(hex.EncodeToString([]byte(alias)))
}

// Deployment is the deployment state of a security artifact.
type Deployment struct {
	Status     string    `json:"status,omitempty"`
	DeployedBy string    `json:"deployedBy,omitempty"`
	DeployedOn time.Time `json:"deployedOn"`
}

type descriptorData struct {
	Type       string `json:"Type"`
	DeployedBy string `json:"DeployedBy"`
	DeployedOn string `json:"DeployedOn"`
	Status     string `json:"Status"`
}

func (d *descriptorData) deployment() Deployment {
	if d == nil {
		return Deployment{}
	}
	on, _ := ParseODataTime(d.DeployedOn)
	return Deployment{Status: d.Status, DeployedBy: d.DeployedBy, DeployedOn: on}
}

// UserCredential is a deployed user credential (without password).
type UserCredential struct {
	Name        string `json:"name"`
	Kind        string `json:"kind,omitempty"` // default, successfactors, openconnectors
	Description string `json:"description,omitempty"`
	User        string `json:"user,omitempty"`
	CompanyID   string `json:"companyId,omitempty"`
	Deployment
}

// OAuth2Credential is a deployed OAuth2 client credential (without secret).
type OAuth2Credential struct {
	Name                 string `json:"name"`
	Description          string `json:"description,omitempty"`
	TokenServiceURL      string `json:"tokenServiceUrl,omitempty"`
	ClientID             string `json:"clientId,omitempty"`
	ClientAuthentication string `json:"clientAuthentication,omitempty"`
	Scope                string `json:"scope,omitempty"`
	Resource             string `json:"resource,omitempty"`
	Audience             string `json:"audience,omitempty"`
	Deployment
}

// SecureParameter is a deployed secure parameter (without value).
type SecureParameter struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Deployment
}

// UserCredentials lists the deployed user credentials.
func (s *Security) UserCredentials() ([]UserCredential, error) {
	var rows []struct {
		Name, Kind, Description, User, CompanyId string
		SecurityArtifactDescriptor               *descriptorData
	}
	if err := getResults(s.exe, "/api/v1/UserCredentials", "Get user credentials", &rows); err != nil {
		return nil, err
	}
	out := make([]UserCredential, 0, len(rows))
	for _, r := range rows {
		out = append(out, UserCredential{Name: r.Name, Kind: r.Kind, Description: r.Description, User: r.User, CompanyID: r.CompanyId,
			Deployment: r.SecurityArtifactDescriptor.deployment()})
	}
	return out, nil
}

// OAuth2Credentials lists the deployed OAuth2 client credentials.
func (s *Security) OAuth2Credentials() ([]OAuth2Credential, error) {
	var rows []struct {
		Name, Description, TokenServiceUrl, ClientId, ClientAuthentication, Scope, Resource, Audience string
		SecurityArtifactDescriptor                                                                    *descriptorData
	}
	if err := getResults(s.exe, "/api/v1/OAuth2ClientCredentials", "Get OAuth2 client credentials", &rows); err != nil {
		return nil, err
	}
	out := make([]OAuth2Credential, 0, len(rows))
	for _, r := range rows {
		out = append(out, OAuth2Credential{Name: r.Name, Description: r.Description, TokenServiceURL: r.TokenServiceUrl, ClientID: r.ClientId,
			ClientAuthentication: r.ClientAuthentication, Scope: r.Scope, Resource: r.Resource, Audience: r.Audience,
			Deployment: r.SecurityArtifactDescriptor.deployment()})
	}
	return out, nil
}

// SecureParameters lists the deployed secure parameters (Neo environment only).
func (s *Security) SecureParameters() ([]SecureParameter, error) {
	var rows []struct {
		Name, Description, DeployedBy, DeployedOn, Status string
	}
	if err := getResults(s.exe, "/api/v1/SecureParameters", "Get secure parameters", &rows); err != nil {
		return nil, err
	}
	out := make([]SecureParameter, 0, len(rows))
	for _, r := range rows {
		on, _ := ParseODataTime(r.DeployedOn)
		out = append(out, SecureParameter{Name: r.Name, Description: r.Description, Deployment: Deployment{Status: r.Status, DeployedBy: r.DeployedBy, DeployedOn: on}})
	}
	return out, nil
}

// Credential kinds accepted by UpsertCredential / DeleteCredential.
const (
	KindUser        = "user"
	KindOAuth2      = "oauth2"
	KindSecureParam = "secure-param"
)

var credentialCollections = map[string]string{
	KindUser:        "UserCredentials",
	KindOAuth2:      "OAuth2ClientCredentials",
	KindSecureParam: "SecureParameters",
}

// CredentialKinds lists the supported credential kinds.
var CredentialKinds = []string{KindUser, KindOAuth2, KindSecureParam}

// KindOAuth2AuthCode is the kind of OAuth2 authorization code credentials:
// listed, but created only in the Web UI (they need the user's consent in a
// browser).
const KindOAuth2AuthCode = "oauth2-authcode"

// ListCredentialKinds are the kinds `credentials list` accepts.
var ListCredentialKinds = []string{KindUser, KindOAuth2, KindOAuth2AuthCode, KindSecureParam}

func collection(kind string) (string, error) {
	c, ok := credentialCollections[kind]
	if !ok {
		return "", fmt.Errorf("invalid credential kind %q (valid: %s)", kind, strings.Join(CredentialKinds, ", "))
	}
	return c, nil
}

// CredentialExists reports whether a credential of the given kind exists.
func (s *Security) CredentialExists(kind, name string) (bool, error) {
	coll, err := collection(kind)
	if err != nil {
		return false, err
	}
	urlPath := fmt.Sprintf("/api/v1/%s(%s)", coll, keyLiteral(name))
	resp, err := s.exe.Exec(http.MethodGet, urlPath, http.NoBody, map[string]string{"Accept": "application/json"})
	if err != nil {
		return false, err
	}
	if resp.StatusCode == http.StatusNotFound { // the expected answer for a new credential: no warning
		_, _ = s.exe.ReadRespBody(resp)
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		_, err = s.exe.LogError(resp, "Get "+coll)
		return false, err
	}
	_, _ = s.exe.ReadRespBody(resp)
	return true, nil
}

// UpsertCredential creates (POST) or updates (PUT) a credential. body holds
// the OData properties of the -create/-update entity (including the secret).
// It returns true if the credential was created.
func (s *Security) UpsertCredential(kind, name string, body map[string]any) (bool, error) {
	coll, err := collection(kind)
	if err != nil {
		return false, err
	}
	exists, err := s.CredentialExists(kind, name)
	if err != nil {
		return false, err
	}
	body["Name"] = name
	payload, err := json.Marshal(body)
	if err != nil {
		return false, err
	}
	method, path := http.MethodPost, "/api/v1/"+coll
	if exists {
		method, path = http.MethodPut, fmt.Sprintf("/api/v1/%s(%s)", coll, keyLiteral(name))
	}
	// Sent without request-body logging: the payload contains the secret
	return !exists, s.write(method, path, payload, "application/json", "Deploy "+coll)
}

// DeleteCredential deletes a credential.
func (s *Security) DeleteCredential(kind, name string) error {
	coll, err := collection(kind)
	if err != nil {
		return err
	}
	return s.write(http.MethodDelete, fmt.Sprintf("/api/v1/%s(%s)", coll, keyLiteral(name)), nil, "", "Delete "+coll)
}

func (s *Security) write(method, path string, payload []byte, contentType, callType string) error {
	headers := map[string]string{"Accept": "application/json"}
	if contentType != "" {
		headers["Content-Type"] = contentType
	}
	resp, err := s.exe.Exec(method, path, strings.NewReader(string(payload)), headers)
	if err != nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNoContent:
		_, _ = s.exe.ReadRespBody(resp)
		return nil
	}
	_, err = s.exe.LogError(resp, callType)
	return err
}

// Keystores accepted by keystore calls.
var Keystores = []string{"system", "backup_admin_system", "KeyRenewal", "KeyHistory"}

// KeystoreEntry is a certificate or key pair of a keystore.
type KeystoreEntry struct {
	Alias          string    `json:"alias"`
	HexAlias       string    `json:"hexAlias"`
	Type           string    `json:"type,omitempty"` // Certificate, KeyPair, SSHKey, ...
	Owner          string    `json:"owner,omitempty"`
	KeyType        string    `json:"keyType,omitempty"`
	KeySize        int       `json:"keySize,omitempty"`
	SubjectDN      string    `json:"subjectDN,omitempty"`
	IssuerDN       string    `json:"issuerDN,omitempty"`
	SerialNumber   string    `json:"serialNumber,omitempty"`
	ValidNotBefore time.Time `json:"validNotBefore"`
	ValidNotAfter  time.Time `json:"validNotAfter"`
	LastModified   time.Time `json:"lastModified"`
	// FingerprintSha256 is the certificate's SHA-256 fingerprint as the
	// tenant writes it (AA:BB:...).
	FingerprintSha256 string `json:"fingerprintSha256,omitempty"`
}

// KeystoreEntries lists the entries of a keystore ("" = system).
func (s *Security) KeystoreEntries(keystore string) ([]KeystoreEntry, error) {
	if keystore == "" {
		keystore = "system"
	}
	if !slices.Contains(Keystores, keystore) {
		return nil, fmt.Errorf("invalid keystore %q (valid: %s)", keystore, strings.Join(Keystores, ", "))
	}
	var rows []struct {
		Hexalias, Alias, Type, Owner, KeyType, SubjectDN, IssuerDN, SerialNumber, FingerprintSha256 string
		KeySize                                                                                     int
		ValidNotBefore, ValidNotAfter, LastModifiedTime                                             string
	}
	if err := getResults(s.exe, "/api/v1/KeystoreEntries?keystoreName="+url.QueryEscape(keystore), "Get keystore entries", &rows); err != nil {
		return nil, err
	}
	out := make([]KeystoreEntry, 0, len(rows))
	for _, r := range rows {
		e := KeystoreEntry{Alias: r.Alias, HexAlias: r.Hexalias, Type: r.Type, Owner: r.Owner, KeyType: r.KeyType, KeySize: r.KeySize,
			SubjectDN: r.SubjectDN, IssuerDN: r.IssuerDN, SerialNumber: r.SerialNumber, FingerprintSha256: r.FingerprintSha256}
		e.ValidNotBefore, _ = ParseODataTime(r.ValidNotBefore)
		e.ValidNotAfter, _ = ParseODataTime(r.ValidNotAfter)
		e.LastModified, _ = ParseODataTime(r.LastModifiedTime)
		if e.HexAlias == "" {
			e.HexAlias = HexAlias(e.Alias)
		}
		out = append(out, e)
	}
	return out, nil
}

// ExportCertificate returns the DER certificate of a keystore entry (for a
// key pair: the certificate with the public key).
func (s *Security) ExportCertificate(alias, keystore string) ([]byte, error) {
	if keystore == "" {
		keystore = "system"
	}
	urlPath := fmt.Sprintf("/api/v1/KeystoreEntries(%s)/Certificate/$value?keystoreName=%s", keyLiteral(HexAlias(alias)), url.QueryEscape(keystore))
	body, err := getValue(s.exe, urlPath, "Export certificate")
	if err != nil {
		return nil, err
	}
	// Accept raw DER as well as base64-encoded DER
	trimmed := strings.TrimSpace(string(body))
	if der, err := base64.StdEncoding.DecodeString(trimmed); err == nil && len(trimmed) > 0 && !strings.HasPrefix(trimmed, "-----") {
		return der, nil
	}
	return body, nil
}

// ImportCertificate imports a DER certificate under alias into the tenant
// keystore. update must be set to replace an existing entry.
func (s *Security) ImportCertificate(alias string, der []byte, update bool) error {
	urlPath := fmt.Sprintf("/api/v1/CertificateResources(%s)/$value?fingerprintVerified=true&returnKeystoreEntries=false&update=%t",
		keyLiteral(HexAlias(alias)), update)
	payload := []byte(base64.StdEncoding.EncodeToString(der))
	return s.write(http.MethodPut, urlPath, payload, "application/pkix-cert", "Import certificate")
}

// AccessPolicy is an access policy (read-only overview).
type AccessPolicy struct {
	ID          string `json:"id"`
	RoleName    string `json:"roleName"`
	Description string `json:"description,omitempty"`
}

// AccessPolicies lists the access policies.
func (s *Security) AccessPolicies() ([]AccessPolicy, error) {
	var rows []struct {
		Id          any `json:"Id"`
		RoleName    string
		Description string
	}
	if err := getResults(s.exe, "/api/v1/AccessPolicies", "Get access policies", &rows); err != nil {
		return nil, err
	}
	out := make([]AccessPolicy, 0, len(rows))
	for _, r := range rows {
		out = append(out, AccessPolicy{ID: fmt.Sprint(r.Id), RoleName: r.RoleName, Description: r.Description})
	}
	return out, nil
}
