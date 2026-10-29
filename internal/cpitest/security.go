package cpitest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// SecurityMaterial is the security content of the mock tenant beyond
// credentials and certificates. Secrets (client secrets, refresh tokens, SSH
// key passwords, private keys) are kept but never returned, like on a real
// tenant.
type SecurityMaterial struct {
	// KeystoreHistory are former versions of keystore entries.
	KeystoreHistory []KeystoreEntry `json:"keystoreHistory,omitempty"`
	// PGPKeys are the keys of the PGP keyrings ("pubring", "secring").
	PGPKeys []PGPKey `json:"pgpKeys,omitempty"`
	// PGPPublicKeyring is the content of the public keyring (as uploaded).
	PGPPublicKeyring []byte `json:"pgpPublicKeyring,omitempty"`
	// OAuth2AuthCodes are OAuth2 authorization code credentials.
	OAuth2AuthCodes []OAuth2AuthCode `json:"oauth2AuthCodes,omitempty"`
	// AccessPolicies are the access policies with artifact references.
	AccessPolicies []AccessPolicy `json:"accessPolicies,omitempty"`
	// AuditLogs is the audit log.
	AuditLogs []AuditEntry `json:"auditLogs,omitempty"`
	// AlternativePartners and AuthorizedUsers of the Partner Directory.
	AlternativePartners []AlternativePartner `json:"alternativePartners,omitempty"`
	AuthorizedUsers     []AuthorizedUser     `json:"authorizedUsers,omitempty"`
	// SSHKeyRequests records SSH key requests without password and key file
	// (whether they were sent).
	SSHKeyRequests []map[string]any `json:"-"`
	serial         int64
}

// PGPKey is a key of a PGP keyring.
type PGPKey struct {
	Keyring     string   `json:"keyring"` // pubring or secring
	ID          string   `json:"id"`
	KeyID       string   `json:"keyId"`
	Fingerprint string   `json:"fingerprint"`
	UserIDs     []string `json:"userIds"`
	ValidUntil  time.Time
	SubKeyIDs   []string `json:"subKeyIds,omitempty"`
}

// OAuth2AuthCode is an OAuth2 authorization code credential.
type OAuth2AuthCode struct {
	Name, Provider, TokenURL, AuthURL, ClientID, UserName string
	ClientSecret, RefreshToken                            string // never returned
	LastModified                                          time.Time
}

// AccessPolicy is an access policy of the mock tenant.
type AccessPolicy struct {
	ID          int64          `json:"id"`
	RoleName    string         `json:"roleName"`
	Description string         `json:"description"`
	References  []ArtifactRefs `json:"references"`
}

// ArtifactRefs is an artifact reference of an access policy.
type ArtifactRefs struct {
	ID                                                                         int64
	Name, Description, Type, ConditionAttribute, ConditionValue, ConditionType string
}

// AuditEntry is an audit log entry.
type AuditEntry struct {
	ChangeID, Action, UserName, ObjName, ObjType string
	Timestamp                                    time.Time
}

// AlternativePartner maps agency/scheme/id to a partner ID.
type AlternativePartner struct{ Agency, Scheme, ID, Pid string }

// AuthorizedUser authorizes a user for a partner ID.
type AuthorizedUser struct{ User, Pid string }

var (
	reKeystoreEntry  = regexp.MustCompile(`^/api/v1/KeystoreEntries\('([0-9A-Fa-f]+)'\)(/ChainCertificates|/SigningRequest/\$value|/Sshkey/\$value)?$`)
	reChainUpload    = regexp.MustCompile(`^/api/v1/CertificateChainResources\('([0-9A-Fa-f]+)'\)/\$value$`)
	rePGPPublicValue = regexp.MustCompile(`^/api/v1/PgpKeyringPublicResources\('([^']+)'\)/\$value$`)
	rePolicy         = regexp.MustCompile(`^/api/v1/AccessPolicies\(([0-9]+)L?\)(/ArtifactReferences)?$`)
	reArtifactRef    = regexp.MustCompile(`^/api/v1/ArtifactReferences\(([0-9]+)L?\)$`)
	reAltPartner     = regexp.MustCompile(`^/api/v1/AlternativePartners\(Hexagency='([0-9A-Fa-f]*)',Hexscheme='([0-9A-Fa-f]*)',Hexid='([0-9A-Fa-f]*)'\)$`)
	reAuthUser       = regexp.MustCompile(`^/api/v1/AuthorizedUsers\('(.*)'\)$`)
	reAuditSince     = regexp.MustCompile(`Timestamp ge datetime'([^']+)'`)
)

func fromHex(s string) string {
	b, _ := hex.DecodeString(s)
	return string(b)
}

func (m *Tenant) keystoreIndex(alias string) int {
	return slices.IndexFunc(m.Keystore, func(e KeystoreEntry) bool { return e.Alias == alias })
}

func (m *Tenant) nextID() int64 {
	m.serial++
	return 1000 + m.serial
}

// keyPairEntry creates a key pair entry with a self-signed certificate.
func keyPairEntry(alias, cn string, notAfter time.Time) (KeystoreEntry, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return KeystoreEntry{}, err
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	return KeystoreEntry{Alias: alias, NotAfter: notAfter, DER: der, Type: "KeyPair", key: key}, err
}

func keystoreRow(e KeystoreEntry) map[string]any {
	row := map[string]any{"Hexalias": strings.ToUpper(hex.EncodeToString([]byte(e.Alias))), "Alias": e.Alias,
		"Type": cmpOr(e.Type, "Certificate"), "Owner": e.Owner, "KeyType": "RSA", "KeySize": 2048, "ValidNotAfter": odataDate(e.NotAfter), "SubjectDN": "CN=" + e.Alias}
	if cert, err := x509.ParseCertificate(e.DER); err == nil {
		row["SubjectDN"], row["IssuerDN"] = cert.Subject.String(), cert.Issuer.String()
	}
	if e.Fingerprint != "" {
		row["FingerprintSha256"] = e.Fingerprint
	}
	return row
}

// handleSecurity serves the security material and Partner Directory
// entities of SecurityMaterial; false if the request is not one of them.
func (m *Tenant) handleSecurity(w http.ResponseWriter, r *http.Request) bool {
	path, q := r.URL.Path, r.URL.Query()
	get, post, put, del := r.Method == http.MethodGet, r.Method == http.MethodPost, r.Method == http.MethodPut, r.Method == http.MethodDelete
	switch {
	case reKeystoreEntry.MatchString(path):
		mm := reKeystoreEntry.FindStringSubmatch(path)
		i := m.keystoreIndex(fromHex(mm[1]))
		if i < 0 {
			notFound(w)
			return true
		}
		e := m.Keystore[i]
		switch {
		case del && mm[2] == "":
			if e.Owner == "SAP" {
				odataError(w, http.StatusForbidden, "Forbidden", "The alias \""+e.Alias+"\" is reserved for SAP.")
				return true
			}
			m.Keystore = slices.Delete(m.Keystore, i, i+1)
			w.WriteHeader(http.StatusOK)
		case get && mm[2] == "":
			writeJSON(w, map[string]any{"d": keystoreRow(e)})
		case get && mm[2] == "/ChainCertificates":
			if e.Type != "KeyPair" {
				odataError(w, http.StatusBadRequest, "Bad Request", "Alias \""+e.Alias+"\" does not represent a Key Pair")
				return true
			}
			chain := e.Chain
			if len(chain) == 0 {
				chain = [][]byte{e.DER}
			}
			rows := []map[string]any{}
			for n, der := range chain {
				row := map[string]any{"Hexalias": mm[1], "Alias": e.Alias, "Index": n}
				if c, err := x509.ParseCertificate(der); err == nil {
					row["SubjectDN"], row["IssuerDN"], row["ValidNotAfter"] = c.Subject.String(), c.Issuer.String(), odataDate(c.NotAfter)
				}
				rows = append(rows, row)
			}
			writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})
		case get && mm[2] == "/SigningRequest/$value":
			if e.key == nil {
				odataError(w, http.StatusBadRequest, "Bad Request", "Alias \""+e.Alias+"\" does not represent a Key Pair")
				return true
			}
			cn := e.Alias
			if c, err := x509.ParseCertificate(e.DER); err == nil {
				cn = c.Subject.CommonName
			}
			csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: cn}}, e.key)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return true
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString(csr)))
		case get && mm[2] == "/Sshkey/$value":
			w.Header().Set("Content-Type", "application/octet-stream")
			if e.SSHPublic != "" {
				_, _ = w.Write([]byte(e.SSHPublic + " mock\n"))
				return true
			}
			_, _ = w.Write([]byte("ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ mock-" + e.Alias + "\n"))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}

	case put && reChainUpload.MatchString(path):
		i := m.keystoreIndex(fromHex(reChainUpload.FindStringSubmatch(path)[1]))
		body, _ := io.ReadAll(r.Body)
		der, err := base64.StdEncoding.DecodeString(string(body))
		if i < 0 {
			notFound(w)
			return true
		}
		certs, perr := x509.ParseCertificates(der)
		if err != nil || perr != nil || len(certs) == 0 || m.Keystore[i].Type != "KeyPair" {
			odataError(w, http.StatusBadRequest, "Bad Request", "invalid certificate chain")
			return true
		}
		m.Keystore[i].Chain = nil
		for _, c := range certs {
			m.Keystore[i].Chain = append(m.Keystore[i].Chain, c.Raw)
		}
		m.Keystore[i].DER, m.Keystore[i].NotAfter = certs[0].Raw, certs[0].NotAfter
		w.WriteHeader(http.StatusNoContent)

	case post && (path == "/api/v1/KeyPairGenerationRequests" || path == "/api/v1/SSHKeyGenerationRequests"):
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return true
		}
		alias, _ := body["Alias"].(string)
		cn, _ := body["CommonName"].(string)
		if alias == "" || cn == "" || body["Hexalias"] != strings.ToUpper(hex.EncodeToString([]byte(alias))) {
			odataError(w, http.StatusBadRequest, "Bad Request", "Alias, Hexalias and CommonName are required")
			return true
		}
		notAfter := time.Now().AddDate(1, 0, 0)
		if v, ok := body["ValidNotAfter"].(string); ok {
			if ms, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(v, "/Date("), ")/"), 10, 64); err == nil {
				notAfter = time.UnixMilli(ms)
			}
		}
		i := m.keystoreIndex(alias)
		if i >= 0 && q.Get("update") != "true" {
			odataError(w, http.StatusConflict, "Conflict", "Entry with alias "+alias+" already exists")
			return true
		}
		e, err := keyPairEntry(alias, cn, notAfter)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		}
		if path == "/api/v1/SSHKeyGenerationRequests" {
			e.Type = "SSHKey"
			if f, _ := body["SSHFile"].(string); f != "" {
				// like the tenant: the key file only as base64
				key, err := base64.StdEncoding.DecodeString(f)
				if SSHPublicKey == nil {
					odataError(w, http.StatusNotImplemented, "Not Implemented",
						"this mock tenant reads no SSH keys: import github.com/cpars-innovation/cpicli/pkg/mocktenant/sshkeys")
					return true
				}
				var pub string
				if err == nil {
					pw, _ := body["Password"].(string)
					pub, err = SSHPublicKey(key, pw)
				}
				if err != nil {
					odataError(w, http.StatusInternalServerError, "Internal Server Error", "SSHKeyConversionException: cannot load key: ")
					return true
				}
				e.SSHPublic = pub
			}
			rec := map[string]any{}
			for k, v := range body {
				if k == "Password" || k == "SSHFile" {
					v = v != ""
				}
				rec[k] = v
			}
			m.SSHKeyRequests = append(m.SSHKeyRequests, rec)
		}
		if i >= 0 {
			m.Keystore[i] = e
		} else {
			m.Keystore = append(m.Keystore, e)
		}
		w.WriteHeader(http.StatusCreated)

	case post && path == "/api/v1/KeyPairResources":
		// like the tenant: without fingerprintVerified a preview (409 with
		// the certificate), nothing written
		var body struct{ Hexalias, Alias, Resource, Password string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Alias == "" {
			w.WriteHeader(http.StatusBadRequest)
			return true
		}
		p12, err := base64.StdEncoding.DecodeString(body.Resource)
		if err != nil || body.Password == "" || body.Password == "wrong" {
			odataError(w, http.StatusBadRequest, "Bad Request", "Password is invalid: PKCS12 verification error, incorrect password")
			return true
		}
		i := m.keystoreIndex(body.Alias)
		switch {
		case i >= 0 && q.Get("update") != "true":
			odataError(w, http.StatusBadRequest, "Bad Request", "Entry with alias \""+body.Alias+"\" already exists in keystore \"system\"")
			return true
		case i < 0 && q.Get("update") == "true":
			odataError(w, http.StatusBadRequest, "Bad Request", "Entry with alias \""+body.Alias+"\" does not exist in keystore \"system\"")
			return true
		}
		sum := sha256.Sum256(p12)
		var parts []string
		for _, b := range sum {
			parts = append(parts, fmt.Sprintf("%02X", b))
		}
		fp := strings.Join(parts, ":")
		if q.Get("fingerprintVerified") != "true" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"d": map[string]any{"Alias": body.Alias, "Type": "Certificate", "Status": "notImported:",
				"SubjectDN": "CN=" + body.Alias, "FingerprintSha256": fp}})
			return true
		}
		e := KeystoreEntry{Alias: body.Alias, Type: "KeyPair", NotAfter: time.Now().AddDate(1, 0, 0), Fingerprint: fp}
		if i >= 0 {
			m.Keystore[i] = e
		} else {
			m.Keystore = append(m.Keystore, e)
		}
		m.KeyPairImports++
		w.WriteHeader(http.StatusNoContent)

	case put && path == "/api/v1/PgpKeyringSecretResources('secring')/$value":
		// like the tenant: keys are added, a key ID already there is left
		pass := r.Header.Get("Passphrase")
		data, _ := io.ReadAll(r.Body)
		if pass == "" {
			odataError(w, http.StatusBadRequest, "Bad Request", "Passphrase Request Header missing or empty.")
			return true
		}
		sum := sha256.Sum256(data)
		id := strings.ToUpper(hex.EncodeToString(sum[:8]))
		status, details := "added", ""
		if slices.ContainsFunc(m.PGPKeys, func(k PGPKey) bool { return k.Keyring == "secring" && k.KeyID == id }) {
			status, details = "not imported", "An entry with the same keyId already exists"
		} else {
			m.PGPKeys = append(m.PGPKeys, PGPKey{Keyring: "secring", ID: id, KeyID: id, ValidUntil: time.Now().AddDate(1, 0, 0)})
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": []map[string]any{{"KeyId": id, "Status": status, "StatusDetails": details}}}})

	case get && path == "/api/v1/HistoryKeystoreEntries":
		if len(q) > 0 {
			odataError(w, http.StatusBadRequest, "Bad Request", "Request URI contains the invalid query option(s)")
			return true
		}
		rows := []map[string]any{}
		for n, e := range m.KeystoreHistory {
			row := keystoreRow(e)
			row["OriginalAlias"], row["ActivationSequenceNumber"], row["LastModifiedTime"] = e.Alias, n+1, odataDate(e.NotAfter.AddDate(-1, 0, 0))
			rows = append(rows, row)
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})

	case get && (path == "/api/v1/PgpPublicKeyrings" || path == "/api/v1/PgpSecretKeyrings"):
		name := "pubring"
		if path == "/api/v1/PgpSecretKeyrings" {
			name = "secring"
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": []map[string]any{{"Name": name, "AggregatedStatus": "DEPLOYED", "Size": -1}}}})

	case get && path == "/api/v1/PgpKeyEntries":
		ring := q.Get("keyringName")
		if ring != "pubring" && ring != "secring" {
			odataError(w, http.StatusInternalServerError, "Internal Server Error", "Cannot derive type from keyringName "+ring)
			return true
		}
		rows := []map[string]any{}
		for _, k := range m.PGPKeys {
			if k.Keyring != ring {
				continue
			}
			row := map[string]any{"Id": k.ID, "KeyId": k.KeyID, "Fingerprint": k.Fingerprint, "Type": "Public", "Algorithm": "RSA", "KeyLength": 3072,
				"ValidityState": "Valid", "ValidUntil": odataDate(k.ValidUntil)}
			if len(k.UserIDs) > 0 {
				row["PrimaryUserId"] = k.UserIDs[0]
			}
			if strings.Contains(q.Get("$expand"), "UserIds") {
				ids := []map[string]any{}
				for _, u := range k.UserIDs {
					ids = append(ids, map[string]any{"UserId": u})
				}
				subs := []map[string]any{}
				for _, s := range k.SubKeyIDs {
					subs = append(subs, map[string]any{"Id": s, "KeyId": s, "Algorithm": "RSA", "KeyFlagsLabel": "Encrypt"})
				}
				row["UserIds"], row["SubKeys"] = map[string]any{"results": ids}, map[string]any{"results": subs}
			}
			rows = append(rows, row)
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})

	case rePGPPublicValue.MatchString(path) && (get || put):
		if rePGPPublicValue.FindStringSubmatch(path)[1] != "pubring" {
			notFound(w)
			return true
		}
		if put {
			m.PGPPublicKeyring, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusNoContent)
			return true
		}
		if len(m.PGPPublicKeyring) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return true
		}
		_, _ = w.Write(m.PGPPublicKeyring)

	case get && path == "/api/v1/OAuth2AuthorizationCodes":
		rows := []map[string]any{}
		for _, c := range m.OAuth2AuthCodes {
			rows = append(rows, map[string]any{"Name": c.Name, "Type": "OAuth2AuthorizationCode", "Provider": c.Provider, "TokenUrl": c.TokenURL,
				"AuthUrl": c.AuthURL, "ClientId": c.ClientID, "ClientSecret": nil, "RefreshToken": nil, "UserName": c.UserName,
				"RefreshTokenExisting": c.RefreshToken != "", "LastModifiedTime": strconv.FormatInt(c.LastModified.UnixMilli(), 10), "IsEdge": false})
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})

	case path == "/api/v1/AccessPolicies" && get:
		rows := []map[string]any{}
		for _, p := range m.AccessPolicies {
			row := map[string]any{"Id": strconv.FormatInt(p.ID, 10), "RoleName": p.RoleName, "Description": p.Description}
			if strings.Contains(q.Get("$expand"), "ArtifactReferences") {
				refs := []map[string]any{}
				for _, a := range p.References {
					refs = append(refs, refRow(a))
				}
				row["ArtifactReferences"] = map[string]any{"results": refs}
			}
			rows = append(rows, row)
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})

	case path == "/api/v1/AccessPolicies" && post:
		var body struct {
			RoleName, Description string
			ArtifactReferences    []ArtifactRefs
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RoleName == "" {
			odataError(w, http.StatusBadRequest, "Bad Request", "RoleName is required")
			return true
		}
		p := AccessPolicy{ID: m.nextID(), RoleName: body.RoleName, Description: body.Description}
		for _, a := range body.ArtifactReferences {
			a.ID = m.nextID()
			p.References = append(p.References, a)
		}
		m.AccessPolicies = append(m.AccessPolicies, p)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"d": map[string]any{"Id": strconv.FormatInt(p.ID, 10), "RoleName": p.RoleName}})

	case rePolicy.MatchString(path):
		mm := rePolicy.FindStringSubmatch(path)
		id, _ := strconv.ParseInt(mm[1], 10, 64)
		i := slices.IndexFunc(m.AccessPolicies, func(p AccessPolicy) bool { return p.ID == id })
		if i < 0 {
			notFound(w)
			return true
		}
		switch {
		case del && mm[2] == "":
			m.AccessPolicies = slices.Delete(m.AccessPolicies, i, i+1)
			w.WriteHeader(http.StatusNoContent)
		case post && mm[2] != "":
			var a ArtifactRefs
			if err := json.NewDecoder(r.Body).Decode(&a); err != nil || a.Name == "" || a.Type == "" {
				odataError(w, http.StatusBadRequest, "Bad Request", "Name and Type are required")
				return true
			}
			a.ID = m.nextID()
			m.AccessPolicies[i].References = append(m.AccessPolicies[i].References, a)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"d": refRow(a)})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}

	case del && reArtifactRef.MatchString(path):
		id, _ := strconv.ParseInt(reArtifactRef.FindStringSubmatch(path)[1], 10, 64)
		for i, p := range m.AccessPolicies {
			if n := slices.IndexFunc(p.References, func(a ArtifactRefs) bool { return a.ID == id }); n >= 0 {
				m.AccessPolicies[i].References = slices.Delete(p.References, n, n+1)
				w.WriteHeader(http.StatusNoContent)
				return true
			}
		}
		notFound(w)

	case get && path == "/api/v1/AuditLogs":
		var since time.Time
		if mm := reAuditSince.FindStringSubmatch(q.Get("$filter")); mm != nil {
			since, _ = time.Parse("2006-01-02T15:04:05.000", mm[1])
		}
		entries := slices.Clone(m.AuditLogs)
		slices.SortFunc(entries, func(a, b AuditEntry) int { return b.Timestamp.Compare(a.Timestamp) })
		rows := []map[string]any{}
		for _, e := range entries {
			if e.Timestamp.Before(since) {
				continue
			}
			rows = append(rows, map[string]any{"ChangeId": e.ChangeID, "Action": e.Action, "Timestamp": odataDate(e.Timestamp), "UserName": e.UserName,
				"ObjName": e.ObjName, "ObjType": e.ObjType, "UserType": "User", "Source": "API"})
		}
		if top, _ := strconv.Atoi(q.Get("$top")); top > 0 && top < len(rows) {
			rows = rows[:top]
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})

	case get && path == "/api/v1/Partners":
		pids := map[string]bool{}
		for _, k := range append(sortedKeys(m.PDStrings), sortedKeys(m.PDBinaries)...) {
			pid, _, _ := strings.Cut(k, "/")
			pids[pid] = true
		}
		for _, a := range m.AlternativePartners {
			pids[a.Pid] = true
		}
		for _, u := range m.AuthorizedUsers {
			pids[u.Pid] = true
		}
		rows := []map[string]any{}
		for _, pid := range sortedKeys(pids) {
			rows = append(rows, map[string]any{"Pid": pid})
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})

	case path == "/api/v1/AlternativePartners" && get:
		rows := []map[string]any{}
		for _, a := range m.AlternativePartners {
			rows = append(rows, map[string]any{"Agency": a.Agency, "Scheme": a.Scheme, "Id": a.ID, "Pid": a.Pid,
				"Hexagency": hexUpper(a.Agency), "Hexscheme": hexUpper(a.Scheme), "Hexid": hexUpper(a.ID)})
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})

	case path == "/api/v1/AlternativePartners" && post:
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["Pid"] == "" || body["Hexid"] != hexUpper(body["Id"]) {
			odataError(w, http.StatusBadRequest, "Bad Request", "invalid alternative partner")
			return true
		}
		a := AlternativePartner{Agency: body["Agency"], Scheme: body["Scheme"], ID: body["Id"], Pid: body["Pid"]}
		if slices.ContainsFunc(m.AlternativePartners, func(x AlternativePartner) bool { return x.Agency == a.Agency && x.Scheme == a.Scheme && x.ID == a.ID }) {
			w.WriteHeader(http.StatusConflict)
			return true
		}
		m.AlternativePartners = append(m.AlternativePartners, a)
		w.WriteHeader(http.StatusCreated)

	case del && reAltPartner.MatchString(path):
		mm := reAltPartner.FindStringSubmatch(path)
		i := slices.IndexFunc(m.AlternativePartners, func(x AlternativePartner) bool {
			return x.Agency == fromHex(mm[1]) && x.Scheme == fromHex(mm[2]) && x.ID == fromHex(mm[3])
		})
		if i < 0 {
			notFound(w)
			return true
		}
		m.AlternativePartners = slices.Delete(m.AlternativePartners, i, i+1)
		w.WriteHeader(http.StatusNoContent)

	case path == "/api/v1/AuthorizedUsers" && get:
		rows := []map[string]any{}
		for _, u := range m.AuthorizedUsers {
			rows = append(rows, map[string]any{"User": u.User, "Pid": u.Pid})
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})

	case path == "/api/v1/AuthorizedUsers" && post:
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["User"] == "" || body["Pid"] == "" {
			odataError(w, http.StatusBadRequest, "Bad Request", "User and Pid are required")
			return true
		}
		if slices.ContainsFunc(m.AuthorizedUsers, func(u AuthorizedUser) bool { return u.User == body["User"] }) {
			w.WriteHeader(http.StatusConflict)
			return true
		}
		m.AuthorizedUsers = append(m.AuthorizedUsers, AuthorizedUser{User: body["User"], Pid: body["Pid"]})
		w.WriteHeader(http.StatusCreated)

	case del && reAuthUser.MatchString(path):
		user := strings.ReplaceAll(reAuthUser.FindStringSubmatch(path)[1], "''", "'")
		i := slices.IndexFunc(m.AuthorizedUsers, func(u AuthorizedUser) bool { return u.User == user })
		if i < 0 {
			notFound(w)
			return true
		}
		m.AuthorizedUsers = slices.Delete(m.AuthorizedUsers, i, i+1)
		w.WriteHeader(http.StatusNoContent)

	default:
		return false
	}
	return true
}

func hexUpper(s string) string { return strings.ToUpper(hex.EncodeToString([]byte(s))) }

func refRow(a ArtifactRefs) map[string]any {
	return map[string]any{"Id": strconv.FormatInt(a.ID, 10), "Name": a.Name, "Description": a.Description, "Type": a.Type,
		"ConditionAttribute": a.ConditionAttribute, "ConditionValue": a.ConditionValue, "ConditionType": a.ConditionType}
}

// AddKeyPair adds a key pair (self-signed certificate) to the keystore.
func (m *Tenant) AddKeyPair(alias, commonName string, notAfter time.Time) error {
	e, err := keyPairEntry(alias, commonName, notAfter)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Keystore = append(m.Keystore, e)
	return nil
}

// SSHPublicKey reads an uploaded SSH private key (OpenSSH or PEM, optionally
// with a passphrase) and returns its public key in authorized_keys form.
// It is set by importing pkg/mocktenant/sshkeys, so that programs embedding
// the mock without SSH keys do not link golang.org/x/crypto/ssh (cpicli#62).
// Unset, an SSH key upload is answered with 501.
var SSHPublicKey func(key []byte, passphrase string) (string, error)
