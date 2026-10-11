package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// securityTools cover security material beyond credentials and the keystore
// listing (key pairs, CSRs, chains, PGP, access policies, audit log) and the
// partners of the Partner Directory. No tool takes or returns a secret or
// private key: SSH key import and PGP keyring upload stay CLI commands. The
// delete tools are opt-in (see optInTools) and need confirm=true.
func securityTools(cfg Config, readOnly map[string]any) []Tool {
	tenant := map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": true}
	destructive := map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": true}
	alias := str("Keystore alias")
	confirm := boolean("Must be true: only on the user's explicit request")
	pd := func() *cpi.PartnerDirectory { return cpi.NewPartnerDirectory(cfg.Exe) }
	refProps := props{
		"name":                str("Name of the artifact reference"),
		"description":         str("Description"),
		"type":                str("Artifact type, e.g. INTEGRATION_FLOW, VALUE_MAPPING, DATA_STORE"),
		"condition_attribute": str("Name or ID"),
		"condition_value":     str("Artifact name/ID, or a regular expression"),
		"condition_type":      enum("How condition_value matches", "EXACT_MATCH", "REGULAR_EXPRESSION"),
	}
	type refArgs struct {
		Name               string `json:"name"`
		Description        string `json:"description"`
		Type               string `json:"type"`
		ConditionAttribute string `json:"condition_attribute"`
		ConditionValue     string `json:"condition_value"`
		ConditionType      string `json:"condition_type"`
	}
	toRef := func(a refArgs) cpi.ArtifactReference {
		return cpi.ArtifactReference{Name: a.Name, Description: a.Description, Type: a.Type, ConditionAttribute: a.ConditionAttribute,
			ConditionValue: a.ConditionValue, ConditionType: a.ConditionType}
	}
	return []Tool{
		{
			Name: "list_keystore_history", Title: "List keystore history",
			Description: "Former versions of keystore entries (replaced by a renewal or update): alias, validity, when they were active and who replaced them.",
			InputSchema: object(props{"alias": str("Only versions of this alias")}),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Alias string `json:"alias"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				entries, err := ops.KeystoreHistory(cfg.Exe, a.Alias)
				return map[string]any{"entries": entries}, err
			},
		},
		{
			Name: "get_certificate_chain", Title: "Get the certificate chain of a key pair",
			Description: "Certificates of a key pair's chain (index 0: its own certificate): subject, issuer, validity. Shows whether a key pair is still self-signed or CA-signed.",
			InputSchema: object(props{"alias": alias}, "alias"),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Alias string `json:"alias"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				return ops.GetCertificateChain(cfg.Exe, a.Alias)
			},
		},
		{
			Name: "get_csr", Title: "Create a certificate signing request",
			Description: "PKCS#10 certificate signing request (PEM) for a key pair of the system keystore, to send to a CA. The CA's answer goes back with upload_certificate_chain. Changes nothing on the tenant.",
			InputSchema: object(props{"alias": alias}, "alias"),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Alias string `json:"alias"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				return ops.CertificateSigningRequest(cfg.Exe, a.Alias)
			},
		},
		{
			Name: "get_ssh_public_key", Title: "Get an SSH public key",
			Description: "Public key of a keystore entry in OpenSSH format, for the authorized_keys file of an SFTP server.",
			InputSchema: object(props{"alias": alias}, "alias"),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Alias string `json:"alias"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				key, err := ops.SSHPublicKey(cfg.Exe, a.Alias)
				return map[string]string{"alias": a.Alias, "publicKey": key}, err
			},
		},
		{
			Name: "generate_key_pair", Title: "Generate a key pair",
			Description: "The tenant generates a key pair (self-signed certificate) in the system keystore; the private key never leaves it. Default RSA 2048, valid one year. Then get_csr and upload_certificate_chain for a CA-signed certificate. update=true replaces an existing alias (breaks flows that trust the old certificate).",
			InputSchema: object(props{
				"alias": alias, "common_name": str("Common name (CN)"), "key_type": enum("Key type (default RSA)", ops.KeyTypes...),
				"key_size": integer("Key size in bits (default 2048)"), "signature_algorithm": str("e.g. SHA256/RSA (default: the tenant's)"),
				"valid_until": str("Validity end, RFC 3339 (default: one year)"), "organization": str("O"), "organization_unit": str("OU"),
				"locality": str("L"), "state": str("ST"), "country": str("Two-letter country code"), "email": str("E-mail"),
				"update": boolean("Replace an existing entry with this alias"),
			}, "alias", "common_name"),
			Annotations: tenant,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Alias              string `json:"alias"`
					CommonName         string `json:"common_name"`
					KeyType            string `json:"key_type"`
					KeySize            int    `json:"key_size"`
					SignatureAlgorithm string `json:"signature_algorithm"`
					ValidUntil         string `json:"valid_until"`
					Organization       string `json:"organization"`
					OrganizationUnit   string `json:"organization_unit"`
					Locality           string `json:"locality"`
					State              string `json:"state"`
					Country            string `json:"country"`
					Email              string `json:"email"`
					Update             bool   `json:"update"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				r := cpi.KeyPairRequest{Alias: a.Alias, CommonName: a.CommonName, KeyType: a.KeyType, KeySize: a.KeySize, SignatureAlgorithm: a.SignatureAlgorithm,
					Organization: a.Organization, OrganizationUnit: a.OrganizationUnit, Locality: a.Locality, State: a.State, Country: a.Country, Email: a.Email}
				if a.ValidUntil != "" {
					t, err := time.Parse(time.RFC3339, a.ValidUntil)
					if err != nil {
						return nil, output.Usagef("invalid valid_until %q (RFC 3339)", a.ValidUntil)
					}
					r.ValidNotAfter = t
				}
				return ops.GenerateKeyPair(cfg.Exe, r, a.Update, time.Now())
			},
		},
		{
			Name: "upload_certificate_chain", Title: "Upload a certificate chain",
			Description: "Replace the certificate of a key pair with the CA-signed chain (the answer to get_csr): PEM text with the key pair's certificate first, then intermediate and root. Private keys are refused.",
			InputSchema: object(props{"alias": alias, "pem": str("PEM certificates")}, "alias", "pem"),
			Annotations: tenant,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Alias string `json:"alias"`
					PEM   string `json:"pem"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				return ops.UploadCertificateChain(cfg.Exe, a.Alias, []byte(a.PEM))
			},
		},
		{
			Name: "delete_keystore_entry", Title: "Delete a keystore entry",
			Description: "Delete a certificate or key pair of the system keystore. Flows that use it fail afterwards: only on the user's explicit request, after graph_search or list_keystore showed it is unused. SAP-owned entries cannot be deleted. Requires confirm=true.",
			InputSchema: object(props{"alias": alias, "confirm": confirm}, "alias", "confirm"),
			Annotations: destructive,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Alias   string `json:"alias"`
					Confirm bool   `json:"confirm"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if !a.Confirm {
					return nil, output.Usagef("delete_keystore_entry breaks flows that use %s; call again with confirm=true only if the user asked for it", a.Alias)
				}
				return ops.DeleteKeystoreEntry(cfg.Exe, a.Alias)
			},
		},
		{
			Name: "list_pgp_keys", Title: "List PGP keys",
			Description: "PGP public and secret keyrings with their keys: key ID, user IDs, sub keys, algorithm, validity (metadata only, never key material). Use it to check the keys an encryptor or decryptor step references.",
			InputSchema: object(nil),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				if err := decode(raw, &struct{}{}); err != nil {
					return nil, err
				}
				return ops.ListPGP(cfg.Exe)
			},
		},
		{
			Name: "list_access_policies", Title: "List access policies",
			Description: "Access policies (role) with their artifact references (type, attribute, condition): which artifacts only users with a role may read or change.",
			InputSchema: object(nil),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				if err := decode(raw, &struct{}{}); err != nil {
					return nil, err
				}
				policies, err := ops.ListAccessPolicies(cfg.Exe)
				return map[string]any{"accessPolicies": policies}, err
			},
		},
		{
			Name: "create_access_policy", Title: "Create an access policy",
			Description: "Create an access policy for a role, optionally with artifact references; matched artifacts become accessible only to users with the role.",
			InputSchema: object(props{"role_name": str("Role name"), "description": str("Description"),
				"references": map[string]any{"type": "array", "description": "Artifact references", "items": object(refProps, "name", "type", "condition_attribute", "condition_value", "condition_type")},
			}, "role_name"),
			Annotations: tenant,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					RoleName    string    `json:"role_name"`
					Description string    `json:"description"`
					References  []refArgs `json:"references"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				refs := make([]cpi.ArtifactReference, 0, len(a.References))
				for _, r := range a.References {
					refs = append(refs, toRef(r))
				}
				return ops.CreateAccessPolicy(cfg.Exe, a.RoleName, a.Description, refs)
			},
		},
		{
			Name: "add_artifact_reference", Title: "Add an artifact reference",
			Description: "Add an artifact reference to an access policy (policy_id from list_access_policies).",
			InputSchema: object(func() props {
				p := props{"policy_id": str("Access policy ID")}
				for k, v := range refProps {
					p[k] = v
				}
				return p
			}(), "policy_id", "name", "type", "condition_attribute", "condition_value", "condition_type"),
			Annotations: tenant,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					PolicyID string `json:"policy_id"`
					refArgs
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				return ops.AddArtifactReference(cfg.Exe, a.PolicyID, toRef(a.refArgs))
			},
		},
		{
			Name: "delete_access_policy", Title: "Delete an access policy",
			Description: "Delete an access policy with its artifact references: the artifacts are no longer restricted to the role. Only on the user's explicit request. Requires confirm=true.",
			InputSchema: object(props{"id": str("Access policy ID"), "confirm": confirm}, "id", "confirm"),
			Annotations: destructive,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ID      string `json:"id"`
					Confirm bool   `json:"confirm"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if !a.Confirm {
					return nil, output.Usagef("delete_access_policy lifts the access restriction of policy %s; call again with confirm=true only if the user asked for it", a.ID)
				}
				return ops.DeleteAccessPolicy(cfg.Exe, a.ID)
			},
		},
		{
			Name: "delete_artifact_reference", Title: "Delete an artifact reference",
			Description: "Delete an artifact reference of an access policy: the artifacts it matched are no longer restricted. Only on the user's explicit request. Requires confirm=true.",
			InputSchema: object(props{"id": str("Artifact reference ID"), "confirm": confirm}, "id", "confirm"),
			Annotations: destructive,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ID      string `json:"id"`
					Confirm bool   `json:"confirm"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if !a.Confirm {
					return nil, output.Usagef("delete_artifact_reference lifts an access restriction; call again with confirm=true only if the user asked for it")
				}
				return ops.DeleteArtifactReference(cfg.Exe, a.ID)
			},
		},
		{
			Name: "list_audit_logs", Title: "List audit log entries",
			Description: "The tenant's audit log, newest first: who changed, deployed or deleted what and when. status missing_role means the credentials may not read it (HTTP 403): tell the user, do not retry.",
			InputSchema: object(props{
				"since": str("Entries after this time: RFC 3339 timestamp or duration back from now (30m, 2h, 1d); default 24h"),
				"top":   integer("At most this many entries (default 200)"), "user": str("Only entries of this user"),
				"object": str("Only entries whose object name contains this text"),
			}),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Since  string `json:"since"`
					Top    int    `json:"top"`
					User   string `json:"user"`
					Object string `json:"object"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if a.Since == "" {
					a.Since = "24h"
				}
				since, err := ops.ParseTimeArg(a.Since, time.Now())
				if err != nil {
					return nil, output.Usagef("invalid since %q: %v", a.Since, err)
				}
				return ops.ListAuditLogs(cfg.Exe, since, a.Top, a.User, a.Object)
			},
		},
		{
			Name: "list_pd_partners", Title: "List Partner Directory partners",
			Description: "Partner IDs of the Partner Directory with the number of string and binary parameters, binary content types, alternative partners and authorized users. Values are not read (sizes=true reads the binaries for their total size). Then get_pd_parameters for one partner.",
			InputSchema: object(props{"pid": str("Only partner IDs containing this text"), "sizes": boolean("Also report the total binary size (reads the values)")}),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Pid   string `json:"pid"`
					Sizes bool   `json:"sizes"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				partners, err := ops.ListPDPartnersWith(pd(), ops.PDPartnersOptions{Pid: a.Pid, Sizes: a.Sizes})
				return map[string]any{"partners": partners}, err
			},
		},
		{
			Name: "list_alternative_partners", Title: "List alternative partners",
			Description: "Alternative partners of the Partner Directory: agency, scheme and identifier (e.g. an EDI sender ID) mapped to a partner ID.",
			InputSchema: object(props{"pid": str("Only those of this partner ID")}),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Pid string `json:"pid"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				alts, err := ops.ListAlternativePartners(pd(), a.Pid)
				return map[string]any{"alternativePartners": alts}, err
			},
		},
		{
			Name: "create_alternative_partner", Title: "Create an alternative partner",
			Description: "Map agency, scheme and identifier (e.g. an EDI sender ID or GLN) to a partner ID, so that flows resolve incoming messages to the partner's Partner Directory parameters.",
			InputSchema: object(props{"agency": str("Agency"), "scheme": str("Scheme"), "id": str("Identifier"), "pid": str("Partner ID")}, "agency", "scheme", "id", "pid"),
			Annotations: tenant,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Agency string `json:"agency"`
					Scheme string `json:"scheme"`
					ID     string `json:"id"`
					Pid    string `json:"pid"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				return ops.CreateAlternativePartner(pd(), cpi.AlternativePartner{Agency: a.Agency, Scheme: a.Scheme, ID: a.ID, Pid: a.Pid})
			},
		},
		{
			Name: "delete_alternative_partner", Title: "Delete an alternative partner",
			Description: "Delete an alternative partner: messages identified by it no longer resolve to the partner ID. Only on the user's explicit request. Requires confirm=true.",
			InputSchema: object(props{"agency": str("Agency"), "scheme": str("Scheme"), "id": str("Identifier"), "confirm": confirm}, "agency", "scheme", "id", "confirm"),
			Annotations: destructive,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Agency  string `json:"agency"`
					Scheme  string `json:"scheme"`
					ID      string `json:"id"`
					Confirm bool   `json:"confirm"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if !a.Confirm {
					return nil, output.Usagef("delete_alternative_partner breaks partner resolution; call again with confirm=true only if the user asked for it")
				}
				return ops.DeleteAlternativePartner(pd(), a.Agency, a.Scheme, a.ID)
			},
		},
		{
			Name: "list_authorized_users", Title: "List authorized users",
			Description: "Authorized users of the Partner Directory: which user (as authenticated by a sender adapter) acts for which partner ID.",
			InputSchema: object(props{"pid": str("Only those of this partner ID")}),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Pid string `json:"pid"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				users, err := ops.ListAuthorizedUsers(pd(), a.Pid)
				return map[string]any{"authorizedUsers": users}, err
			},
		},
		{
			Name: "create_authorized_user", Title: "Create an authorized user",
			Description: "Authorize a user (as authenticated by a sender adapter, e.g. a client certificate's subject or a technical user) to act for a partner ID in the Partner Directory.",
			InputSchema: object(props{"user": str("User"), "pid": str("Partner ID")}, "user", "pid"),
			Annotations: tenant,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					User string `json:"user"`
					Pid  string `json:"pid"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				return ops.CreateAuthorizedUser(pd(), a.User, a.Pid)
			},
		},
		{
			Name: "delete_authorized_user", Title: "Delete an authorized user",
			Description: "Remove an authorized user: the user can no longer act for its partner ID. Only on the user's explicit request. Requires confirm=true.",
			InputSchema: object(props{"user": str("User"), "confirm": confirm}, "user", "confirm"),
			Annotations: destructive,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					User    string `json:"user"`
					Confirm bool   `json:"confirm"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if !a.Confirm {
					return nil, output.Usagef("delete_authorized_user removes %s's access; call again with confirm=true only if the user asked for it", a.User)
				}
				return ops.DeleteAuthorizedUser(pd(), a.User)
			},
		},
	}
}
