# Security material

Integration flows reference credentials and keys by name. cpictl lists them, deploys
them from CI, and watches certificate expiry.

## Rules

- **Secrets are never flag values.** Every secret comes from an environment variable
  (`--password-env NAME`), a file (`--password-file path`) or stdin (`--password-stdin`),
  so it does not end up in shell history, process lists or CI logs.
- **Secrets are never returned.** `credentials list` (and the MCP tools) show names, users,
  client IDs and status only; the tenant API does not return passwords and cpictl does not
  map such fields.
- **Secrets are never logged.** Request bodies are not written to the log, not even with `--debug`.
- **The MCP server is read-only for security material.** Agents can see which credentials
  exist (`list_credentials`, `list_keystore`), but deploying a secret is done by a person or a
  pipeline with `cpictl credentials`.

## Credentials

```bash
cpictl credentials list                         # user, oauth2, secure-param (names and metadata)
cpictl credentials list --kind oauth2

ERP_PASSWORD=... cpictl credentials set-user --name ERP_User --user svc_erp --password-env ERP_PASSWORD
cpictl credentials set-oauth2 --name Graph --token-url https://login.example.com/oauth/token \
  --client-id my-app --secret-file ./graph.secret [--client-auth header] [--scope ...]
printf '%s' "$API_KEY" | cpictl credentials set-secure-param --name ApiKey --value-stdin

cpictl credentials delete --kind user --name ERP_User --confirm
```

`set-*` create the credential or update it if it exists (`action`: `CREATED` / `UPDATED`);
`--dry-run` validates the input and resolves the secret without writing.

### Declarative: credentials apply

```yaml
# credentials.yml (commit this; it contains no secrets)
userCredentials:
  - name: ERP_User
    user: svc_erp
    kind: default            # default, successfactors, openconnectors
    password: {env: ERP_PASSWORD}
oauth2Credentials:
  - name: Graph
    tokenServiceUrl: https://login.example.com/oauth/token
    clientId: my-app
    clientSecret: {file: secrets/graph.txt}   # relative to this file
    clientAuthentication: body                # or header
secureParameters:
  - name: ApiKey
    value: {env: API_KEY}
```

```bash
cpictl credentials apply --file credentials.yml --dry-run
cpictl credentials apply --file credentials.yml --output json
```

All secrets are resolved before anything is written; a missing variable or file stops the
run with exit code 2 and no changes. Inline values (`password: plain`) and unknown keys are
rejected. If some credentials fail to deploy the exit code is 7 and the result lists each one.

Secure parameters work on Neo and Cloud Foundry (verified on a Cloud Foundry tenant).

`credentials apply` also imports **key pairs** (PKCS#12), **SSH keys** and **PGP secret keys**, and leaves those
the tenant already has alone: it is the one command to sync security material from a vault. The full format, what
can and cannot come from a vault, and pipeline examples for Azure Key Vault, AWS Secrets Manager, HashiCorp Vault
and Google Secret Manager are in [vault.md](vault.md).

## Keystore

```bash
cpictl keystore list                                   # tenant keystore, sorted by expiry
cpictl keystore list --expiring-within 30d             # flag entries expiring within 30 days
cpictl keystore list --expiring-within 30d --fail-on-expiry   # exit 5 if any: use in a nightly job
cpictl keystore list --keystore KeyRenewal

cpictl keystore export-cert --alias sap_cloudintegrationcertificate > tenant.pem
cpictl keystore import-cert --alias partner_acme --file acme.pem            # PEM or DER
cpictl keystore import-cert --alias partner_acme --file acme-2027.pem --update
```

`import-cert` validates the file as an X.509 certificate before uploading, rejects private
keys and refuses to replace an existing alias without `--update`.

### Key pairs, CSRs and SSH keys

```bash
# the tenant generates the key; the private key never leaves it (RSA 2048, one year by default)
cpictl keystore generate-key-pair --alias client_cert --cn cpi-client.example.com --org "Example AG" --country DE
cpictl keystore csr --alias client_cert --out client_cert.csr       # PKCS#10 for the CA
cpictl keystore upload-chain --alias client_cert --file signed-chain.pem   # the CA's answer, own certificate first
cpictl keystore chain --alias client_cert                           # self-signed or CA-signed?

cpictl keystore generate-ssh-key --alias sftp_partner --cn sftp_partner
SSH_PW=... cpictl keystore generate-ssh-key --alias sftp_partner --cn sftp_partner --ssh-file id_rsa --password-env SSH_PW
cpictl keystore ssh-public-key --alias sftp_partner >> authorized_keys   # OpenSSH format

cpictl keystore history                                             # former versions of entries
cpictl keystore delete --alias partner_acme_old --confirm
```

- `generate-key-pair` and `generate-ssh-key` refuse an existing alias without `--update`;
  SAP-owned entries (`sap_*`, owner SAP) can be neither replaced nor deleted.
- `csr` and `upload-chain` need a key pair (or SSH key); `upload-chain` accepts PEM with several
  certificates or one DER certificate and rejects private keys.
- `generate-ssh-key --ssh-file` imports an existing OpenSSH private key; its password comes from
  `--password-env`, `--password-file` or `--password-stdin` and is never logged.
- `history` lists `HistoryKeystoreEntries` (versions replaced by renewals or updates). The API has
  no call to reactivate a history version or to restore the backup keystore
  (`keystore list --keystore backup_admin_system` shows it); use the Web UI for that.

MCP: `list_keystore_history`, `get_certificate_chain`, `get_csr`, `get_ssh_public_key`,
`generate_key_pair`, `upload_certificate_chain`, `delete_keystore_entry` (confirm, opt-in). SSH key
import stays a CLI command: it needs a private key and a password.

## OAuth2 authorization code credentials

`credentials list` includes OAuth2 authorization code credentials (`--kind oauth2-authcode`):
name, provider, client ID, user, whether a refresh token exists. Client secret and refresh token
are never shown. They are created in the Web UI (the user's consent needs a browser).

## PGP keyrings

```bash
cpictl pgp list                                  # public and secret keys: key ID, user IDs, sub keys, validity
cpictl pgp export-public --out pubring.gpg
cpictl pgp import-public --file pubring-new.gpg --confirm   # replaces the public keyring
```

`list` shows metadata only, never key material. `import-public` replaces the whole public
keyring: export it first and merge locally. Secret keys are refused. MCP: `list_pgp_keys`.

## Access policies

```bash
cpictl access-policies list
cpictl access-policies create --role OrdersTeam --policy-description "Orders flows" \
  --name Orders --type INTEGRATION_FLOW --attribute Name --value 'Orders_.*' --condition REGULAR_EXPRESSION
cpictl access-policies add-reference --policy-id 1001 --name Returns --value Returns_In
cpictl access-policies delete-reference --id 1002 --confirm
cpictl access-policies delete --id 1001 --confirm
```

An artifact matched by a policy's artifact reference is accessible only to users with the
policy's role. Deleting a policy or a reference lifts the restriction. MCP:
`list_access_policies`, `create_access_policy`, `add_artifact_reference`; `delete_access_policy`
and `delete_artifact_reference` (confirm, opt-in).

## Audit log

```bash
cpictl audit-log --since 24h
cpictl audit-log --since 7d --user dev@example.com --object Orders
```

Newest first, at most `--top` entries (default 200). Without a role that may read the audit log
the tenant answers 403: the result has `status: missing_role` and a hint (exit code 0), and
`cpictl doctor` lists the `audit log` area as forbidden (as on trial tenants). MCP:
`list_audit_logs`.

## Roles

Reading credentials and the keystore and changing them need the corresponding security
roles of the API client (credentials read/write, keystore read/write); missing roles show up
as exit code 3.
