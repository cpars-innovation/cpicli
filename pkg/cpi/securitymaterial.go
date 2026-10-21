package cpi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Security material beyond credentials and certificates: key pair and SSH key
// generation, certificate signing requests, certificate chains, keystore
// history, PGP keyrings, OAuth2 authorization code credentials, access
// policies and the audit log. Private keys never leave the tenant: the API
// has no call that returns one, and the client maps none.

// odataJSONDate formats t for a JSON request body (OData v2 verbose).
func odataJSONDate(t time.Time) string {
	return fmt.Sprintf("/Date(%d)/", t.UnixMilli())
}

// KeyPairRequest describes a key pair the tenant generates (self-signed
// certificate; replace it with a CA-signed chain after a CSR).
type KeyPairRequest struct {
	Alias              string    `json:"alias"`
	KeyType            string    `json:"keyType"` // RSA, DSA, EC
	KeySize            int       `json:"keySize,omitempty"`
	EllipticCurve      string    `json:"ellipticCurve,omitempty"`
	SignatureAlgorithm string    `json:"signatureAlgorithm,omitempty"`
	ValidNotAfter      time.Time `json:"validNotAfter"`
	CommonName         string    `json:"commonName"`
	OrganizationUnit   string    `json:"organizationUnit,omitempty"`
	Organization       string    `json:"organization,omitempty"`
	Locality           string    `json:"locality,omitempty"`
	State              string    `json:"state,omitempty"`
	Country            string    `json:"country,omitempty"`
	Email              string    `json:"email,omitempty"`
}

func (r KeyPairRequest) body() map[string]any {
	b := map[string]any{"Hexalias": HexAlias(r.Alias), "Alias": r.Alias, "KeyType": r.KeyType}
	if r.KeySize > 0 {
		b["KeySize"] = r.KeySize
	}
	if !r.ValidNotAfter.IsZero() {
		b["ValidNotAfter"] = odataJSONDate(r.ValidNotAfter)
	}
	for k, v := range map[string]string{"EllipticCurve": r.EllipticCurve, "SignatureAlgorithm": r.SignatureAlgorithm, "CommonName": r.CommonName,
		"OrganizationUnit": r.OrganizationUnit, "Organization": r.Organization, "Locality": r.Locality, "State": r.State, "Country": r.Country, "Email": r.Email} {
		if v != "" {
			b[k] = v
		}
	}
	return b
}

// GenerateKeyPair has the tenant generate a key pair in the system keystore
// (KeyPairGenerationRequests). update replaces an existing alias.
func (s *Security) GenerateKeyPair(r KeyPairRequest, update bool) error {
	payload, err := json.Marshal(r.body())
	if err != nil {
		return err
	}
	return s.write(http.MethodPost, fmt.Sprintf("/api/v1/KeyPairGenerationRequests?returnKeystoreEntries=false&update=%t", update),
		payload, "application/json", "Generate key pair")
}

// SSHKeyRequest describes an SSH key for the system keystore
// (SSHKeyGenerationRequests). Without SSHFile the tenant generates the key;
// with SSHFile (an OpenSSH private key file) it is imported, protected by
// Password. Neither is ever logged.
type SSHKeyRequest struct {
	KeyPairRequest
	SSHFile  string `json:"-"`
	Password string `json:"-"`
}

// GenerateSSHKey creates or imports an SSH key.
func (s *Security) GenerateSSHKey(r SSHKeyRequest, update bool) error {
	b := r.body()
	if r.SSHFile != "" {
		// the tenant reads the key file only as base64 (raw text: "cannot
		// load key"; verified on a tenant)
		b["SSHFile"] = base64.StdEncoding.EncodeToString([]byte(r.SSHFile))
	}
	if r.Password != "" {
		b["Password"] = r.Password
	}
	payload, err := json.Marshal(b)
	if err != nil {
		return err
	}
	// Sent without request-body logging: it may contain the key and password
	return s.write(http.MethodPost, fmt.Sprintf("/api/v1/SSHKeyGenerationRequests?returnKeystoreEntries=false&update=%t", update),
		payload, "application/json", "Generate SSH key")
}

// DeleteKeystoreEntry deletes an entry of the system keystore.
func (s *Security) DeleteKeystoreEntry(alias string) error {
	return s.write(http.MethodDelete, fmt.Sprintf("/api/v1/KeystoreEntries(%s)", keyLiteral(HexAlias(alias))), nil, "", "Delete keystore entry")
}

// SigningRequest returns a certificate signing request (PKCS#10) for a key
// pair, as the tenant sends it (PEM, or base64 / raw DER).
func (s *Security) SigningRequest(alias string) ([]byte, error) {
	return getValue(s.exe, fmt.Sprintf("/api/v1/KeystoreEntries(%s)/SigningRequest/$value", keyLiteral(HexAlias(alias))), "Create certificate signing request")
}

// SSHPublicKey returns the public key of an entry in OpenSSH format.
func (s *Security) SSHPublicKey(alias string) (string, error) {
	b, err := getValue(s.exe, fmt.Sprintf("/api/v1/KeystoreEntries(%s)/Sshkey/$value", keyLiteral(HexAlias(alias))), "Get SSH public key")
	return strings.TrimSpace(string(b)), err
}

// ChainCertificate is one certificate of a key pair's chain (index 0: the
// key pair's own certificate).
type ChainCertificate struct {
	Index              int       `json:"index"`
	SubjectDN          string    `json:"subjectDN,omitempty"`
	IssuerDN           string    `json:"issuerDN,omitempty"`
	SerialNumber       string    `json:"serialNumber,omitempty"`
	KeyType            string    `json:"keyType,omitempty"`
	KeySize            int       `json:"keySize,omitempty"`
	SignatureAlgorithm string    `json:"signatureAlgorithm,omitempty"`
	ValidNotBefore     time.Time `json:"validNotBefore"`
	ValidNotAfter      time.Time `json:"validNotAfter"`
	FingerprintSha256  string    `json:"fingerprintSha256,omitempty"`
}

// ChainCertificates lists the certificate chain of a key pair (the tenant
// answers 400 for an entry that is not a key pair).
func (s *Security) ChainCertificates(alias string) ([]ChainCertificate, error) {
	var rows []struct {
		Index                                                          int
		KeySize                                                        int
		SubjectDN, IssuerDN, SerialNumber, KeyType, SignatureAlgorithm string
		ValidNotBefore, ValidNotAfter, FingerprintSha256               string
	}
	if err := getResults(s.exe, fmt.Sprintf("/api/v1/KeystoreEntries(%s)/ChainCertificates", keyLiteral(HexAlias(alias))), "Get certificate chain", &rows); err != nil {
		return nil, err
	}
	out := make([]ChainCertificate, 0, len(rows))
	for _, r := range rows {
		c := ChainCertificate{Index: r.Index, SubjectDN: r.SubjectDN, IssuerDN: r.IssuerDN, SerialNumber: r.SerialNumber, KeyType: r.KeyType,
			KeySize: r.KeySize, SignatureAlgorithm: r.SignatureAlgorithm, FingerprintSha256: r.FingerprintSha256}
		c.ValidNotBefore, _ = ParseODataTime(r.ValidNotBefore)
		c.ValidNotAfter, _ = ParseODataTime(r.ValidNotAfter)
		out = append(out, c)
	}
	return out, nil
}

// UploadCertificateChain replaces the certificate chain of a key pair (the
// CA's answer to a CSR): the DER certificates, the key pair's own first.
func (s *Security) UploadCertificateChain(alias string, ders [][]byte) error {
	urlPath := fmt.Sprintf("/api/v1/CertificateChainResources(%s)/$value?fingerprintVerified=true&returnKeystoreEntries=false", keyLiteral(HexAlias(alias)))
	payload := []byte(base64.StdEncoding.EncodeToString(bytes.Join(ders, nil)))
	return s.write(http.MethodPut, urlPath, payload, "application/pkix-cert", "Upload certificate chain")
}

// HistoryKeystoreEntry is a former version of a keystore entry (replaced by
// a renewal or update).
type HistoryKeystoreEntry struct {
	KeystoreEntry
	OriginalAlias            string     `json:"originalAlias,omitempty"`
	ActivatedBy              string     `json:"activatedBy,omitempty"`
	ActiveFrom               *time.Time `json:"activeFrom,omitempty"`
	DeactivatedBy            string     `json:"deactivatedBy,omitempty"`
	ActiveUntil              *time.Time `json:"activeUntil,omitempty"`
	ActivationSequenceNumber int        `json:"activationSequenceNumber,omitempty"`
}

// HistoryKeystoreEntries lists the keystore history (no query options: the
// tenant rejects $top and keystoreName here).
func (s *Security) HistoryKeystoreEntries() ([]HistoryKeystoreEntry, error) {
	var rows []struct {
		Hexalias, Alias, Type, Owner, KeyType, SubjectDN, IssuerDN, SerialNumber string
		KeySize, ActivationSequenceNumber                                        int
		ValidNotBefore, ValidNotAfter, LastModifiedTime                          string
		OriginalAlias, ActivatedBy, ActiveFrom, DeactivatedBy, ActiveUntil       string
	}
	if err := getResults(s.exe, "/api/v1/HistoryKeystoreEntries", "Get keystore history", &rows); err != nil {
		return nil, err
	}
	out := make([]HistoryKeystoreEntry, 0, len(rows))
	for _, r := range rows {
		e := HistoryKeystoreEntry{KeystoreEntry: KeystoreEntry{Alias: r.Alias, HexAlias: r.Hexalias, Type: r.Type, Owner: r.Owner, KeyType: r.KeyType,
			KeySize: r.KeySize, SubjectDN: r.SubjectDN, IssuerDN: r.IssuerDN, SerialNumber: r.SerialNumber},
			OriginalAlias: r.OriginalAlias, ActivatedBy: r.ActivatedBy, DeactivatedBy: r.DeactivatedBy, ActivationSequenceNumber: r.ActivationSequenceNumber}
		e.ValidNotBefore, _ = ParseODataTime(r.ValidNotBefore)
		e.ValidNotAfter, _ = ParseODataTime(r.ValidNotAfter)
		e.LastModified, _ = ParseODataTime(r.LastModifiedTime)
		e.ActiveFrom, e.ActiveUntil = optTime(r.ActiveFrom), optTime(r.ActiveUntil)
		out = append(out, e)
	}
	return out, nil
}

// PgpKeyring is a PGP keyring (public: "pubring", secret: "secring").
type PgpKeyring struct {
	Name             string     `json:"name"`
	Kind             string     `json:"kind"` // public or secret
	Status           string     `json:"status,omitempty"`
	LastModifiedBy   string     `json:"lastModifiedBy,omitempty"`
	LastModifiedTime *time.Time `json:"lastModifiedTime,omitempty"`
}

// PgpKeyrings lists the public and secret keyrings.
func (s *Security) PgpKeyrings() ([]PgpKeyring, error) {
	out := []PgpKeyring{}
	for _, kind := range []string{"public", "secret"} {
		var rows []struct{ Name, LastModifiedBy, LastModifiedTime, AggregatedStatus string }
		coll := "PgpPublicKeyrings"
		if kind == "secret" {
			coll = "PgpSecretKeyrings"
		}
		if err := getResults(s.exe, "/api/v1/"+coll, "Get PGP keyrings", &rows); err != nil {
			return nil, err
		}
		for _, r := range rows {
			out = append(out, PgpKeyring{Name: r.Name, Kind: kind, Status: r.AggregatedStatus, LastModifiedBy: r.LastModifiedBy, LastModifiedTime: optTime(r.LastModifiedTime)})
		}
	}
	return out, nil
}

// PgpKey is a key of a PGP keyring (metadata only).
type PgpKey struct {
	ID            string     `json:"id"`
	KeyID         string     `json:"keyId"`
	Fingerprint   string     `json:"fingerprint,omitempty"`
	Type          string     `json:"type,omitempty"`
	Algorithm     string     `json:"algorithm,omitempty"`
	KeyLength     int        `json:"keyLength,omitempty"`
	Usage         string     `json:"usage,omitempty"` // KeyFlagsLabel
	Validity      string     `json:"validity,omitempty"`
	ValidUntil    *time.Time `json:"validUntil,omitempty"`
	CreatedTime   *time.Time `json:"createdTime,omitempty"`
	PrimaryUserID string     `json:"primaryUserId,omitempty"`
	UserIDs       []string   `json:"userIds,omitempty"`
	SubKeys       []PgpKey   `json:"subKeys,omitempty"`
	Error         string     `json:"error,omitempty"`
}

type pgpKeyRow struct {
	Id, KeyId, Fingerprint, Type, Algorithm, KeyFlagsLabel, ValidityState, ValidUntil, CreatedTime, PrimaryUserId, ErrorInformation string
	KeyLength                                                                                                                       int
	UserIds                                                                                                                         *struct {
		Results []struct{ UserId string } `json:"results"`
	}
	SubKeys *struct {
		Results []pgpKeyRow `json:"results"`
	}
}

func (r pgpKeyRow) key() PgpKey {
	k := PgpKey{ID: r.Id, KeyID: r.KeyId, Fingerprint: r.Fingerprint, Type: r.Type, Algorithm: r.Algorithm, KeyLength: r.KeyLength, Usage: r.KeyFlagsLabel,
		Validity: r.ValidityState, ValidUntil: optTime(r.ValidUntil), CreatedTime: optTime(r.CreatedTime), PrimaryUserID: r.PrimaryUserId, Error: r.ErrorInformation}
	if r.UserIds != nil {
		for _, u := range r.UserIds.Results {
			k.UserIDs = append(k.UserIDs, u.UserId)
		}
	}
	if r.SubKeys != nil {
		for _, sk := range r.SubKeys.Results {
			k.SubKeys = append(k.SubKeys, sk.key())
		}
	}
	return k
}

// PgpKeys lists the keys of a keyring ("pubring" or "secring") with their
// user IDs and sub keys.
func (s *Security) PgpKeys(keyring string) ([]PgpKey, error) {
	var rows []pgpKeyRow
	if err := getResults(s.exe, "/api/v1/PgpKeyEntries?keyringName="+url.QueryEscape(keyring)+"&$expand=UserIds,SubKeys", "Get PGP keys", &rows); err != nil {
		return nil, err
	}
	out := make([]PgpKey, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.key())
	}
	return out, nil
}

// PgpPublicKeyring downloads the public keyring (empty when it has no keys:
// the tenant answers 204).
func (s *Security) PgpPublicKeyring(name string) ([]byte, error) {
	resp, err := s.exe.Exec(http.MethodGet, fmt.Sprintf("/api/v1/PgpKeyringPublicResources(%s)/$value", keyLiteral(name)), http.NoBody, nil)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return s.exe.ReadRespBody(resp)
	case http.StatusNoContent:
		_ = resp.Body.Close()
		return []byte{}, nil
	}
	_, err = s.exe.LogError(resp, "Download PGP public keyring")
	return nil, err
}

// UploadPgpPublicKeyring replaces the public keyring with data (binary or
// ASCII-armored OpenPGP public keys).
func (s *Security) UploadPgpPublicKeyring(name string, data []byte) error {
	return s.write(http.MethodPut, fmt.Sprintf("/api/v1/PgpKeyringPublicResources(%s)/$value", keyLiteral(name)), data, "application/octet-stream", "Upload PGP public keyring")
}

// OAuth2AuthorizationCode is an OAuth2 authorization code credential
// (without client secret and refresh token).
type OAuth2AuthorizationCode struct {
	Name                 string     `json:"name"`
	Type                 string     `json:"type,omitempty"`
	Description          string     `json:"description,omitempty"`
	Provider             string     `json:"provider,omitempty"`
	TokenURL             string     `json:"tokenUrl,omitempty"`
	AuthURL              string     `json:"authUrl,omitempty"`
	ClientID             string     `json:"clientId,omitempty"`
	ClientAuthentication string     `json:"clientAuthentication,omitempty"`
	Scope                string     `json:"scope,omitempty"`
	UserName             string     `json:"userName,omitempty"`
	RefreshTokenExists   bool       `json:"refreshTokenExists"`
	RefreshTokenCreated  *time.Time `json:"refreshTokenCreated,omitempty"`
	LastModifiedBy       string     `json:"lastModifiedBy,omitempty"`
	LastModifiedTime     *time.Time `json:"lastModifiedTime,omitempty"`
}

// OAuth2AuthorizationCodes lists the OAuth2 authorization code credentials.
func (s *Security) OAuth2AuthorizationCodes() ([]OAuth2AuthorizationCode, error) {
	var rows []struct {
		Name, Type, Description, Provider, TokenUrl, AuthUrl, ClientId, ClientAuthentication, Scope, UserName, LastModifiedBy string
		RefreshTokenExisting                                                                                                  bool
		RefreshTokenCreationTime, LastModifiedTime                                                                            flexInt
	}
	if err := getResults(s.exe, "/api/v1/OAuth2AuthorizationCodes", "Get OAuth2 authorization code credentials", &rows); err != nil {
		return nil, err
	}
	out := make([]OAuth2AuthorizationCode, 0, len(rows))
	for _, r := range rows {
		out = append(out, OAuth2AuthorizationCode{Name: r.Name, Type: r.Type, Description: r.Description, Provider: r.Provider, TokenURL: r.TokenUrl,
			AuthURL: r.AuthUrl, ClientID: r.ClientId, ClientAuthentication: r.ClientAuthentication, Scope: r.Scope, UserName: r.UserName,
			RefreshTokenExists: r.RefreshTokenExisting, RefreshTokenCreated: epochMillis(r.RefreshTokenCreationTime),
			LastModifiedBy: r.LastModifiedBy, LastModifiedTime: epochMillis(r.LastModifiedTime)})
	}
	return out, nil
}

// epochMillis reads an Edm.Int64 timestamp (0 = none).
func epochMillis(ms flexInt) *time.Time {
	if ms <= 0 {
		return nil
	}
	t := time.UnixMilli(int64(ms)).UTC()
	return &t
}

// ArtifactReference restricts an access policy to artifacts (by name or ID,
// exact match or regular expression).
type ArtifactReference struct {
	ID                 string `json:"id,omitempty"`
	Name               string `json:"name"`
	Description        string `json:"description,omitempty"`
	Type               string `json:"type"`               // e.g. INTEGRATION_FLOW, VALUE_MAPPING, DATA_STORE
	ConditionAttribute string `json:"conditionAttribute"` // e.g. Name, ID
	ConditionValue     string `json:"conditionValue"`
	ConditionType      string `json:"conditionType"` // e.g. EXACT_MATCH, REGULAR_EXPRESSION
}

func (r ArtifactReference) body() map[string]any {
	b := map[string]any{"Name": r.Name, "Type": r.Type, "ConditionAttribute": r.ConditionAttribute, "ConditionValue": r.ConditionValue,
		"ConditionType": r.ConditionType}
	if r.Description != "" {
		b["Description"] = r.Description
	}
	return b
}

// AccessPolicyDetail is an access policy with its artifact references.
type AccessPolicyDetail struct {
	AccessPolicy
	ArtifactReferences []ArtifactReference `json:"artifactReferences"`
}

type artifactReferenceRow struct {
	Id                                                                         flexInt
	Name, Description, Type, ConditionAttribute, ConditionValue, ConditionType string
}

func (r artifactReferenceRow) ref() ArtifactReference {
	return ArtifactReference{ID: fmt.Sprint(r.Id), Name: r.Name, Description: r.Description, Type: r.Type, ConditionAttribute: r.ConditionAttribute,
		ConditionValue: r.ConditionValue, ConditionType: r.ConditionType}
}

// AccessPoliciesWithReferences lists the access policies with their
// artifact references.
func (s *Security) AccessPoliciesWithReferences() ([]AccessPolicyDetail, error) {
	var rows []struct {
		Id                    flexInt
		RoleName, Description string
		ArtifactReferences    *struct {
			Results []artifactReferenceRow `json:"results"`
		}
	}
	if err := getResults(s.exe, "/api/v1/AccessPolicies?$expand=ArtifactReferences", "Get access policies", &rows); err != nil {
		return nil, err
	}
	out := make([]AccessPolicyDetail, 0, len(rows))
	for _, r := range rows {
		p := AccessPolicyDetail{AccessPolicy: AccessPolicy{ID: fmt.Sprint(r.Id), RoleName: r.RoleName, Description: r.Description}, ArtifactReferences: []ArtifactReference{}}
		if r.ArtifactReferences != nil {
			for _, a := range r.ArtifactReferences.Results {
				p.ArtifactReferences = append(p.ArtifactReferences, a.ref())
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// int64Key checks a numeric (Edm.Int64) key.
func int64Key(id string) (string, error) {
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		return "", fmt.Errorf("invalid ID %q (a number)", id)
	}
	return id, nil
}

// CreateAccessPolicy creates an access policy (role name as in the
// subaccount's role collections) with optional artifact references, and
// returns its ID.
func (s *Security) CreateAccessPolicy(roleName, description string, refs []ArtifactReference) (string, error) {
	b := map[string]any{"RoleName": roleName}
	if description != "" {
		b["Description"] = description
	}
	if len(refs) > 0 {
		list := make([]map[string]any, 0, len(refs))
		for _, r := range refs {
			list = append(list, r.body())
		}
		b["ArtifactReferences"] = list
	}
	var created struct {
		Id flexInt
	}
	if err := s.create("/api/v1/AccessPolicies", b, "Create access policy", &created); err != nil {
		return "", err
	}
	return fmt.Sprint(created.Id), nil
}

// DeleteAccessPolicy deletes an access policy (with its artifact references).
func (s *Security) DeleteAccessPolicy(id string) error {
	key, err := int64Key(id)
	if err != nil {
		return err
	}
	return s.write(http.MethodDelete, "/api/v1/AccessPolicies("+key+")", nil, "", "Delete access policy")
}

// CreateArtifactReference adds an artifact reference to an access policy
// and returns its ID.
func (s *Security) CreateArtifactReference(policyID string, r ArtifactReference) (string, error) {
	key, err := int64Key(policyID)
	if err != nil {
		return "", err
	}
	var created struct {
		Id flexInt
	}
	if err := s.create("/api/v1/AccessPolicies("+key+")/ArtifactReferences", r.body(), "Create artifact reference", &created); err != nil {
		return "", err
	}
	return fmt.Sprint(created.Id), nil
}

// DeleteArtifactReference deletes an artifact reference.
func (s *Security) DeleteArtifactReference(id string) error {
	key, err := int64Key(id)
	if err != nil {
		return err
	}
	return s.write(http.MethodDelete, "/api/v1/ArtifactReferences("+key+")", nil, "", "Delete artifact reference")
}

// create POSTs an entity and decodes the created entity ({"d":{...}}) into v.
func (s *Security) create(path string, body map[string]any, callType string, v any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := s.exe.Exec(http.MethodPost, path, bytes.NewReader(payload), map[string]string{"Accept": "application/json", "Content-Type": "application/json"})
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		_, err = s.exe.LogError(resp, callType)
		return err
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return err
	}
	var env struct {
		D json.RawMessage `json:"d"`
	}
	if json.Unmarshal(data, &env) == nil && len(env.D) > 0 {
		_ = json.Unmarshal(env.D, v)
	}
	return nil
}

// AuditLog is an entry of the tenant's audit log (who changed what).
type AuditLog struct {
	ChangeID   string    `json:"changeId"`
	Action     string    `json:"action,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
	UserName   string    `json:"userName,omitempty"`
	UserType   string    `json:"userType,omitempty"`
	ObjectName string    `json:"objectName,omitempty"`
	ObjectType string    `json:"objectType,omitempty"`
	Source     string    `json:"source,omitempty"`
	NodeType   string    `json:"nodeType,omitempty"`
}

// AuditLogs reads audit log entries since a point in time, newest first, at
// most top (0: the tenant's default page).
func (s *Security) AuditLogs(since time.Time, top int) ([]AuditLog, error) {
	q := url.Values{}
	if !since.IsZero() {
		q.Set("$filter", "Timestamp ge "+odataDateTime(since))
	}
	q.Set("$orderby", "Timestamp desc")
	if top > 0 {
		q.Set("$top", strconv.Itoa(top))
	}
	var rows []struct {
		ChangeId, Action, Timestamp, UserName, UserType, ObjName, ObjType, Source, NodeType string
	}
	if err := getResults(s.exe, "/api/v1/AuditLogs?"+strings.ReplaceAll(q.Encode(), "+", "%20"), "Get audit logs", &rows); err != nil {
		return nil, err
	}
	out := make([]AuditLog, 0, len(rows))
	for _, r := range rows {
		ts, _ := ParseODataTime(r.Timestamp)
		out = append(out, AuditLog{ChangeID: r.ChangeId, Action: r.Action, Timestamp: ts, UserName: r.UserName, UserType: r.UserType,
			ObjectName: r.ObjName, ObjectType: r.ObjType, Source: r.Source, NodeType: r.NodeType})
	}
	return out, nil
}

// KeyPairPreview is what the tenant reads from a PKCS#12 key pair before it
// imports it: its certificate.
type KeyPairPreview struct {
	SubjectDN         string
	FingerprintSha256 string
	ValidNotAfter     time.Time
}

func keyPairPayload(alias string, pkcs12 []byte, password string) ([]byte, error) {
	return json.Marshal(map[string]any{"Hexalias": HexAlias(alias), "Alias": alias,
		"Resource": base64.StdEncoding.EncodeToString(pkcs12), "Password": password})
}

// PreviewKeyPair has the tenant read a PKCS#12 key pair without importing
// it: without fingerprintVerified the tenant answers 409 with the parsed
// certificate (verified on a tenant), or 400 for a wrong password or an
// unreadable file. Nothing is written. exists must say whether the alias is
// in the keystore (the tenant checks it first).
func (s *Security) PreviewKeyPair(alias string, pkcs12 []byte, password string, exists bool) (*KeyPairPreview, error) {
	payload, err := keyPairPayload(alias, pkcs12, password)
	if err != nil {
		return nil, err
	}
	// sent without request-body logging: it holds the private key and password
	resp, err := s.exe.Exec(http.MethodPost, fmt.Sprintf("/api/v1/KeyPairResources?returnKeystoreEntries=false&update=%t", exists),
		bytes.NewReader(payload), map[string]string{"Accept": "application/json", "Content-Type": "application/json"})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusConflict {
		_, err := s.exe.LogError(resp, "Read key pair")
		return nil, err
	}
	body, err := s.exe.ReadRespBody(resp)
	if err != nil {
		return nil, err
	}
	var v struct {
		D struct{ SubjectDN, FingerprintSha256, ValidNotAfter string } `json:"d"`
	}
	if err := json.Unmarshal(body, &v); err != nil || v.D.FingerprintSha256 == "" {
		return nil, fmt.Errorf("the tenant's answer to the key pair is not readable")
	}
	p := &KeyPairPreview{SubjectDN: v.D.SubjectDN, FingerprintSha256: v.D.FingerprintSha256}
	p.ValidNotAfter, _ = ParseODataTime(v.D.ValidNotAfter)
	return p, nil
}

// ImportKeyPair imports a PKCS#12 key pair (private key and certificate
// chain) into the system keystore; update replaces an existing entry.
func (s *Security) ImportKeyPair(alias string, pkcs12 []byte, password string, update bool) error {
	payload, err := keyPairPayload(alias, pkcs12, password)
	if err != nil {
		return err
	}
	// sent without request-body logging: it holds the private key and password
	return s.write(http.MethodPost, fmt.Sprintf("/api/v1/KeyPairResources?fingerprintVerified=true&returnKeystoreEntries=false&update=%t", update),
		payload, "application/json", "Import key pair")
}

// PgpKeyStatus is the tenant's answer for one key of an uploaded keyring.
type PgpKeyStatus struct {
	KeyID   string `json:"keyId"`
	UserID  string `json:"userId,omitempty"`
	Status  string `json:"status"` // added, or "not imported" with Details
	Details string `json:"details,omitempty"`
}

// AddPgpSecretKeys uploads OpenPGP secret keys (binary or ASCII-armored) with
// their passphrase. The tenant adds them to its secret keyring and keeps the
// keys already there (verified on a tenant).
func (s *Security) AddPgpSecretKeys(data []byte, passphrase string) ([]PgpKeyStatus, error) {
	// neither the keys nor the passphrase header are logged
	resp, err := s.exe.Exec(http.MethodPut, "/api/v1/PgpKeyringSecretResources('secring')/$value", bytes.NewReader(data),
		map[string]string{"Accept": "application/json", "Content-Type": "application/octet-stream", "Passphrase": passphrase})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		_, err := s.exe.LogError(resp, "Upload PGP secret keys")
		return nil, err
	}
	body, err := s.exe.ReadRespBody(resp)
	if err != nil {
		return nil, err
	}
	var v struct {
		D struct {
			Results []struct{ KeyId, PrimaryUserId, Status, StatusDetails string } `json:"results"`
		} `json:"d"`
	}
	_ = json.Unmarshal(body, &v)
	out := make([]PgpKeyStatus, 0, len(v.D.Results))
	for _, r := range v.D.Results {
		out = append(out, PgpKeyStatus{KeyID: r.KeyId, UserID: r.PrimaryUserId, Status: r.Status, Details: r.StatusDetails})
	}
	return out, nil
}
