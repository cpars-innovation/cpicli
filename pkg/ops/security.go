package ops

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"gopkg.in/yaml.v3"
)

// SecretSource says where a secret is read from. Exactly one field must be
// set. Secrets are never taken from command line values or config files
// directly, so they do not end up in shell history, process lists or Git.
type SecretSource struct {
	Env   string    `yaml:"env,omitempty"`  // name of an environment variable
	File  string    `yaml:"file,omitempty"` // path of a file (trailing newline removed)
	Stdin io.Reader `yaml:"-"`              // read everything (trailing newline removed)
}

// Resolve returns the secret.
func (s SecretSource) Resolve(what string) (string, error) {
	set := 0
	for _, b := range []bool{s.Env != "", s.File != "", s.Stdin != nil} {
		if b {
			set++
		}
	}
	if set != 1 {
		return "", output.Usagef("%s: give exactly one secret source (environment variable, file or stdin)", what)
	}
	var value string
	switch {
	case s.Env != "":
		v, ok := os.LookupEnv(s.Env)
		if !ok {
			return "", output.Usagef("%s: environment variable %s is not set", what, s.Env)
		}
		value = v
	case s.File != "":
		b, err := os.ReadFile(s.File)
		if err != nil {
			return "", output.Usagef("%s: %v", what, err)
		}
		value = strings.TrimRight(string(b), "\r\n")
	default:
		b, err := io.ReadAll(s.Stdin)
		if err != nil {
			return "", err
		}
		value = strings.TrimRight(string(b), "\r\n")
	}
	if value == "" {
		return "", output.Usagef("%s: secret is empty", what)
	}
	return value, nil
}

// UserCredentialSpec describes a user credential to deploy.
type UserCredentialSpec struct {
	Name        string       `yaml:"name"`
	Kind        string       `yaml:"kind,omitempty"` // default (default), successfactors, openconnectors
	Description string       `yaml:"description,omitempty"`
	User        string       `yaml:"user"`
	CompanyID   string       `yaml:"companyId,omitempty"`
	Password    SecretSource `yaml:"password"`
}

// OAuth2CredentialSpec describes an OAuth2 client credential to deploy.
type OAuth2CredentialSpec struct {
	Name                 string       `yaml:"name"`
	Description          string       `yaml:"description,omitempty"`
	TokenServiceURL      string       `yaml:"tokenServiceUrl"`
	ClientID             string       `yaml:"clientId"`
	ClientSecret         SecretSource `yaml:"clientSecret"`
	ClientAuthentication string       `yaml:"clientAuthentication,omitempty"` // body (default) or header
	Scope                string       `yaml:"scope,omitempty"`
	ScopeContentType     string       `yaml:"scopeContentType,omitempty"` // urlencoded (default) or json
	Resource             string       `yaml:"resource,omitempty"`
	Audience             string       `yaml:"audience,omitempty"`
}

// SecureParameterSpec describes a secure parameter to deploy (Neo and Cloud
// Foundry; verified on a Cloud Foundry tenant).
type SecureParameterSpec struct {
	Name        string       `yaml:"name"`
	Description string       `yaml:"description,omitempty"`
	Value       SecretSource `yaml:"value"`
}

// CredentialResult is the outcome for one credential.
type CredentialResult struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Action string `json:"action"` // CREATED, UPDATED, DELETED, WOULD_DEPLOY, FAILED
	Error  string `json:"error,omitempty"`
	Err    error  `json:"-"`
}

func specKind(spec any) string {
	switch spec.(type) {
	case UserCredentialSpec:
		return cpi.KindUser
	case OAuth2CredentialSpec:
		return cpi.KindOAuth2
	case SecureParameterSpec:
		return cpi.KindSecureParam
	}
	return ""
}

func credentialBody(kind string, spec any) (string, map[string]any, error) {
	switch s := spec.(type) {
	case UserCredentialSpec:
		if s.Name == "" || s.User == "" {
			return "", nil, output.Usagef("user credential: name and user are required")
		}
		if s.Kind == "" {
			s.Kind = "default"
		}
		if !slices.Contains([]string{"default", "successfactors", "openconnectors"}, s.Kind) {
			return "", nil, output.Usagef("user credential %s: invalid kind %q (default, successfactors, openconnectors)", s.Name, s.Kind)
		}
		password, err := s.Password.Resolve("user credential " + s.Name + " password")
		if err != nil {
			return "", nil, err
		}
		return s.Name, map[string]any{"Kind": s.Kind, "Description": s.Description, "User": s.User, "Password": password, "CompanyId": s.CompanyID}, nil
	case OAuth2CredentialSpec:
		if s.Name == "" || s.TokenServiceURL == "" || s.ClientID == "" {
			return "", nil, output.Usagef("OAuth2 credential: name, tokenServiceUrl and clientId are required")
		}
		if !strings.HasPrefix(s.TokenServiceURL, "https://") {
			return "", nil, output.Usagef("OAuth2 credential %s: tokenServiceUrl must use https", s.Name)
		}
		if s.ClientAuthentication == "" {
			s.ClientAuthentication = "body"
		}
		if s.ScopeContentType == "" {
			s.ScopeContentType = "urlencoded"
		}
		if !slices.Contains([]string{"body", "header"}, s.ClientAuthentication) || !slices.Contains([]string{"urlencoded", "json"}, s.ScopeContentType) {
			return "", nil, output.Usagef("OAuth2 credential %s: clientAuthentication must be body or header, scopeContentType urlencoded or json", s.Name)
		}
		secret, err := s.ClientSecret.Resolve("OAuth2 credential " + s.Name + " client secret")
		if err != nil {
			return "", nil, err
		}
		return s.Name, map[string]any{"Description": s.Description, "TokenServiceUrl": s.TokenServiceURL, "ClientId": s.ClientID,
			"ClientSecret": secret, "ClientAuthentication": s.ClientAuthentication, "Scope": s.Scope,
			"ScopeContentType": s.ScopeContentType, "Resource": s.Resource, "Audience": s.Audience}, nil
	case SecureParameterSpec:
		if s.Name == "" {
			return "", nil, output.Usagef("secure parameter: name is required")
		}
		value, err := s.Value.Resolve("secure parameter " + s.Name + " value")
		if err != nil {
			return "", nil, err
		}
		return s.Name, map[string]any{"Description": s.Description, "SecureParam": value}, nil
	}
	return "", nil, fmt.Errorf("unsupported credential spec %T", spec)
}

// DeployCredential creates or updates one credential. spec is a
// UserCredentialSpec, OAuth2CredentialSpec or SecureParameterSpec. With
// dryRun the secret is resolved (so a missing source is reported) but
// nothing is written.
func DeployCredential(exe *httpclnt.HTTPExecuter, spec any, dryRun bool) (*CredentialResult, error) {
	kind := specKind(spec)
	name, body, err := credentialBody(kind, spec)
	if err != nil {
		return nil, err
	}
	res := &CredentialResult{Kind: kind, Name: name}
	if dryRun {
		res.Action = "WOULD_DEPLOY"
		return res, nil
	}
	created, err := cpi.NewSecurity(exe).UpsertCredential(kind, name, body)
	if err != nil {
		res.Action, res.Err, res.Error = "FAILED", err, err.Error()
		return res, err
	}
	res.Action = "UPDATED"
	if created {
		res.Action = "CREATED"
	}
	return res, nil
}

// DeleteCredential deletes a credential (kind: user, oauth2, secure-param).
func DeleteCredential(exe *httpclnt.HTTPExecuter, kind, name string) (*CredentialResult, error) {
	if !slices.Contains(cpi.CredentialKinds, kind) || name == "" {
		return nil, output.Usagef("kind (%s) and name are required", strings.Join(cpi.CredentialKinds, ", "))
	}
	sec := cpi.NewSecurity(exe)
	exists, err := sec.CredentialExists(kind, name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, output.Usagef("%s credential %s does not exist", kind, name)
	}
	if err := sec.DeleteCredential(kind, name); err != nil {
		return nil, err
	}
	return &CredentialResult{Kind: kind, Name: name, Action: "DELETED"}, nil
}

// CredentialsFile is the format of `credentials apply`. Secrets are given as
// sources only, e.g. `password: {env: ERP_PASSWORD}`.
type CredentialsFile struct {
	UserCredentials   []UserCredentialSpec   `yaml:"userCredentials,omitempty"`
	OAuth2Credentials []OAuth2CredentialSpec `yaml:"oauth2Credentials,omitempty"`
	SecureParameters  []SecureParameterSpec  `yaml:"secureParameters,omitempty"`
	KeyPairs          []KeyPairSpec          `yaml:"keyPairs,omitempty"`
	SSHKeys           []SSHKeySpec           `yaml:"sshKeys,omitempty"`
	PGPSecretKeys     []PGPSecretKeySpec     `yaml:"pgpSecretKeys,omitempty"`
}

// LoadCredentialsFile parses a credentials file strictly (unknown keys, e.g.
// an inline "password: secret", are rejected). Relative secret files are
// resolved against the directory of the credentials file.
func LoadCredentialsFile(path string) (*CredentialsFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, output.Usagef("%v", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var f CredentialsFile
	if err := dec.Decode(&f); err != nil && err != io.EOF {
		return nil, output.Usagef("invalid credentials file %s: %v (secrets must be given as {env: VAR} or {file: path})", path, err)
	}
	base := filepath.Dir(path)
	fix := func(s *SecretSource) {
		if s.File != "" && !filepath.IsAbs(s.File) {
			s.File = filepath.Join(base, s.File)
		}
	}
	for i := range f.UserCredentials {
		fix(&f.UserCredentials[i].Password)
	}
	for i := range f.OAuth2Credentials {
		fix(&f.OAuth2Credentials[i].ClientSecret)
	}
	for i := range f.SecureParameters {
		fix(&f.SecureParameters[i].Value)
	}
	for i := range f.KeyPairs {
		fix(&f.KeyPairs[i].PKCS12)
		if f.KeyPairs[i].Password != nil {
			fix(f.KeyPairs[i].Password)
		}
	}
	for i := range f.SSHKeys {
		fix(&f.SSHKeys[i].PrivateKey)
		if f.SSHKeys[i].Passphrase != nil {
			fix(f.SSHKeys[i].Passphrase)
		}
	}
	for i := range f.PGPSecretKeys {
		fix(&f.PGPSecretKeys[i].Keys)
		fix(&f.PGPSecretKeys[i].Passphrase)
	}
	return &f, nil
}

// CredentialsApplyResult is the outcome of ApplyCredentials.
type CredentialsApplyResult struct {
	DryRun  bool               `json:"dryRun"`
	Results []CredentialResult `json:"results"`
}

// ApplyCredentials deploys every credential of f. All secrets are resolved
// first; if any is missing nothing is written.
func ApplyCredentials(exe *httpclnt.HTTPExecuter, f *CredentialsFile, dryRun bool) (*CredentialsApplyResult, error) {
	var specs []any
	for _, s := range f.UserCredentials {
		specs = append(specs, s)
	}
	for _, s := range f.OAuth2Credentials {
		specs = append(specs, s)
	}
	for _, s := range f.SecureParameters {
		specs = append(specs, s)
	}
	for _, s := range f.KeyPairs {
		specs = append(specs, s)
	}
	for _, s := range f.SSHKeys {
		specs = append(specs, s)
	}
	for _, s := range f.PGPSecretKeys {
		specs = append(specs, s)
	}
	if len(specs) == 0 {
		return nil, output.Usagef("the credentials file contains no credentials")
	}
	// validate and resolve all secrets before writing anything
	seen := map[string]bool{}
	resolved := make([]any, len(specs))
	for i, spec := range specs {
		var kind, name string
		if km, ok := spec.(keyMaterialSpec); ok {
			r, err := km.resolve()
			if err != nil {
				return nil, err
			}
			resolved[i], kind, name = r, r.kind, r.name
		} else {
			kind = specKind(spec)
			n, _, err := credentialBody(kind, spec)
			if err != nil {
				return nil, err
			}
			resolved[i], name = spec, n
		}
		if seen[kind+"/"+name] {
			return nil, output.Usagef("%s %s is listed twice", kind, name)
		}
		seen[kind+"/"+name] = true
	}

	res := &CredentialsApplyResult{DryRun: dryRun, Results: []CredentialResult{}}
	failed := 0
	for _, spec := range resolved {
		var r *CredentialResult
		var err error
		if km, ok := spec.(*resolvedKeyMaterial); ok {
			r, err = km.deploy(exe, dryRun)
		} else {
			r, err = DeployCredential(exe, spec, dryRun)
		}
		if r != nil {
			res.Results = append(res.Results, *r)
		}
		if err != nil {
			failed++
		}
	}
	switch {
	case failed == 0:
		return res, nil
	case failed < len(specs):
		return res, output.Partial(fmt.Errorf("%d of %d credential(s) failed", failed, len(specs)))
	default:
		return res, firstCredentialError(res.Results)
	}
}

func firstCredentialError(results []CredentialResult) error {
	for _, r := range results {
		if r.Err != nil {
			return fmt.Errorf("all credentials failed, first: %s %s: %w", r.Kind, r.Name, r.Err)
		}
	}
	return fmt.Errorf("all credentials failed")
}

// SecurityOverview lists credentials (names and metadata only, never secrets).
type SecurityOverview struct {
	UserCredentials   []cpi.UserCredential   `json:"userCredentials"`
	OAuth2Credentials []cpi.OAuth2Credential `json:"oauth2Credentials"`
	// OAuth2AuthorizationCodes are OAuth2 authorization code credentials
	// (without client secret and refresh token).
	OAuth2AuthorizationCodes []cpi.OAuth2AuthorizationCode `json:"oauth2AuthorizationCodes"`
	SecureParameters         []cpi.SecureParameter         `json:"secureParameters"`
	Warnings                 []string                      `json:"warnings,omitempty"`
}

// ListCredentials returns all credentials (kind "" = all kinds). Secure
// parameters only exist in the Neo environment; elsewhere their lookup is
// reported as a warning.
func ListCredentials(exe *httpclnt.HTTPExecuter, kind string) (*SecurityOverview, error) {
	if kind != "" && !slices.Contains(cpi.ListCredentialKinds, kind) {
		return nil, output.Usagef("invalid kind %q (valid: %s)", kind, strings.Join(cpi.ListCredentialKinds, ", "))
	}
	sec := cpi.NewSecurity(exe)
	o := &SecurityOverview{UserCredentials: []cpi.UserCredential{}, OAuth2Credentials: []cpi.OAuth2Credential{},
		OAuth2AuthorizationCodes: []cpi.OAuth2AuthorizationCode{}, SecureParameters: []cpi.SecureParameter{}}
	var err error
	if kind == "" || kind == cpi.KindUser {
		if o.UserCredentials, err = sec.UserCredentials(); err != nil {
			return nil, err
		}
	}
	if kind == "" || kind == cpi.KindOAuth2 {
		if o.OAuth2Credentials, err = sec.OAuth2Credentials(); err != nil {
			return nil, err
		}
	}
	if kind == "" || kind == cpi.KindOAuth2AuthCode {
		codes, err := sec.OAuth2AuthorizationCodes()
		switch {
		case err == nil:
			o.OAuth2AuthorizationCodes = codes
		case kind == cpi.KindOAuth2AuthCode || httpclnt.IsAuthError(err):
			return nil, err
		default:
			o.Warnings = append(o.Warnings, fmt.Sprintf("OAuth2 authorization code credentials not available: %v", err))
		}
	}
	if kind == "" || kind == cpi.KindSecureParam {
		params, err := sec.SecureParameters()
		switch {
		case err == nil:
			o.SecureParameters = params
		case kind == cpi.KindSecureParam || httpclnt.IsAuthError(err):
			return nil, err
		default:
			o.Warnings = append(o.Warnings, fmt.Sprintf("secure parameters not available (Neo environment only): %v", err))
		}
	}
	return o, nil
}

// KeystoreEntryStatus is a keystore entry with its remaining validity.
type KeystoreEntryStatus struct {
	cpi.KeystoreEntry
	DaysLeft     int  `json:"daysLeft"`
	Expired      bool `json:"expired,omitempty"`
	ExpiringSoon bool `json:"expiringSoon,omitempty"`
}

// KeystoreReport is the result of ListKeystore.
type KeystoreReport struct {
	Keystore       string                `json:"keystore"`
	ExpiringWithin string                `json:"expiringWithin,omitempty"`
	Entries        []KeystoreEntryStatus `json:"entries"`
	Expired        int                   `json:"expired"`
	ExpiringSoon   int                   `json:"expiringSoon"`
}

// ExpiryError reports expired or soon expiring certificates (exit code 5).
type ExpiryError struct{ Msg string }

func (e *ExpiryError) Error() string { return e.Msg }
func (e *ExpiryError) ExitCode() int { return exitcode.DeployFailed }

// ListKeystore lists keystore entries with their remaining validity. Entries
// that expire within `within` are flagged; with failOnExpiry an expired or
// expiring entry yields an *ExpiryError together with the report.
func ListKeystore(exe *httpclnt.HTTPExecuter, keystore string, within time.Duration, failOnExpiry bool, now time.Time) (*KeystoreReport, error) {
	if keystore == "" {
		keystore = "system"
	}
	if !slices.Contains(cpi.Keystores, keystore) {
		return nil, output.Usagef("invalid keystore %q (valid: %s)", keystore, strings.Join(cpi.Keystores, ", "))
	}
	entries, err := cpi.NewSecurity(exe).KeystoreEntries(keystore)
	if err != nil {
		return nil, err
	}
	r := &KeystoreReport{Keystore: keystore, Entries: make([]KeystoreEntryStatus, 0, len(entries))}
	if within > 0 {
		r.ExpiringWithin = within.String()
	}
	for _, e := range entries {
		s := KeystoreEntryStatus{KeystoreEntry: e}
		if !e.ValidNotAfter.IsZero() {
			left := e.ValidNotAfter.Sub(now)
			s.DaysLeft = int(math.Floor(left.Hours() / 24))
			s.Expired = left < 0
			s.ExpiringSoon = !s.Expired && within > 0 && left <= within
		}
		if s.Expired {
			r.Expired++
		}
		if s.ExpiringSoon {
			r.ExpiringSoon++
		}
		r.Entries = append(r.Entries, s)
	}
	slices.SortFunc(r.Entries, func(a, b KeystoreEntryStatus) int { return a.ValidNotAfter.Compare(b.ValidNotAfter) })
	if failOnExpiry && r.Expired+r.ExpiringSoon > 0 {
		return r, &ExpiryError{Msg: fmt.Sprintf("%d expired and %d expiring keystore entr(ies) in %s", r.Expired, r.ExpiringSoon, keystore)}
	}
	return r, nil
}

// CertificateInfo describes a certificate.
type CertificateInfo struct {
	Alias             string    `json:"alias"`
	Subject           string    `json:"subject"`
	Issuer            string    `json:"issuer"`
	SerialNumber      string    `json:"serialNumber"`
	NotBefore         time.Time `json:"notBefore"`
	NotAfter          time.Time `json:"notAfter"`
	FingerprintSHA256 string    `json:"fingerprintSha256"`
	PEM               string    `json:"pem,omitempty"`
}

func certificateInfo(alias string, cert *x509.Certificate) CertificateInfo {
	sum := sha256.Sum256(cert.Raw)
	return CertificateInfo{Alias: alias, Subject: cert.Subject.String(), Issuer: cert.Issuer.String(),
		SerialNumber: cert.SerialNumber.Text(16), NotBefore: cert.NotBefore.UTC(), NotAfter: cert.NotAfter.UTC(),
		FingerprintSHA256: strings.ToUpper(hex.EncodeToString(sum[:]))}
}

// ParseCertificate accepts a PEM or DER certificate.
func ParseCertificate(data []byte) (*x509.Certificate, error) {
	if block, _ := pem.Decode(data); block != nil {
		if block.Type != "CERTIFICATE" {
			return nil, output.Usagef("PEM block is %q, expected CERTIFICATE (private keys are not accepted)", block.Type)
		}
		data = block.Bytes
	}
	cert, err := x509.ParseCertificate(data)
	if err != nil {
		return nil, output.Usagef("not a valid X.509 certificate: %v", err)
	}
	return cert, nil
}

// ExportCertificate returns the certificate of a keystore entry as PEM.
func ExportCertificate(exe *httpclnt.HTTPExecuter, alias, keystore string) (*CertificateInfo, error) {
	if alias == "" {
		return nil, output.Usagef("alias is required")
	}
	der, err := cpi.NewSecurity(exe).ExportCertificate(alias, keystore)
	if err != nil {
		if httpclnt.StatusCode(err) == 404 {
			return nil, output.Usagef("keystore entry %s not found", alias)
		}
		return nil, err
	}
	cert, err := ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("tenant returned an unreadable certificate for %s: %w", alias, err)
	}
	info := certificateInfo(alias, cert)
	info.PEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
	return &info, nil
}

// ImportCertificate imports a PEM or DER certificate under alias. An existing
// alias is only replaced with update.
func ImportCertificate(exe *httpclnt.HTTPExecuter, alias string, data []byte, update bool) (*CertificateInfo, error) {
	if alias == "" {
		return nil, output.Usagef("alias is required")
	}
	cert, err := ParseCertificate(data)
	if err != nil {
		return nil, err
	}
	sec := cpi.NewSecurity(exe)
	if !update {
		entries, err := sec.KeystoreEntries("system")
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.Alias == alias {
				return nil, output.Usagef("keystore entry %s already exists (use update to replace it)", alias)
			}
		}
	}
	if err := sec.ImportCertificate(alias, cert.Raw, update); err != nil {
		return nil, err
	}
	info := certificateInfo(alias, cert)
	return &info, nil
}
