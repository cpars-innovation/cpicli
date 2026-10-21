# Security material from a vault

The vault (Azure Key Vault, AWS Secrets Manager, HashiCorp Vault, Google Secret Manager, …) is the source of
truth for passwords, client secrets and private keys; CPI keeps a copy. A pipeline reads the vault with the
vault's own tools, puts each value into an environment variable, and **one command** writes everything to CPI:

```bash
cpictl --profile prod credentials apply --file credentials.yml
```

cpictl never talks to a vault itself, so any vault works and the vault-specific part stays in your pipeline.
`credentials.yml` holds no secrets, only references (`{env: VAR}` or `{file: path}`), and belongs in Git.

## What can come from a vault

| CPI security material | `credentials.yml` section | What the vault holds | On every run |
|---|---|---|---|
| User credentials (default, SuccessFactors, OpenConnectors) | `userCredentials` | the password | written (a password cannot be read back to compare) |
| OAuth2 client credentials | `oauth2Credentials` | the client secret | written |
| Secure parameters (Neo and Cloud Foundry) | `secureParameters` | the value | written |
| Key pairs (X.509: private key + certificate chain) | `keyPairs` | PKCS#12 (`.p12`/`.pfx`) as base64, and its password | left alone when the tenant has the same certificate; a renewed one replaces it |
| SSH keys | `sshKeys` | the private key (OpenSSH or PEM) as base64, and its passphrase if encrypted | left alone when the public key is the same; a new key replaces it |
| PGP secret keys | `pgpSecretKeys` | the secret key(s) as base64 (or armored), and the passphrase | added to the tenant's secret keyring; a key ID already there is left alone |

All of these were run against a real Cloud Foundry tenant, including a second run (nothing rewritten) and a
renewed key pair (replaced).

**Not from a vault**, because they are not secret or cannot be written by API:

- **Certificates** (public): keep them in Git, `cpictl keystore import-cert`; `backup restore` restores them.
- **PGP public keys:** `cpictl pgp import-public`.
- **SSH known hosts:** public; maintained in the Web UI.
- **OAuth2 authorization code credentials:** need a user's consent in a browser (Web UI).
- **Key pairs generated on the tenant:** the private key never leaves it. Generate again and have the
  certificate signed (`keystore generate-key-pair`, `csr`, `upload-chain`), or keep such keys in the vault
  from the start and import them with `keyPairs`.
- **Entries owned by SAP:** cannot be replaced.

## credentials.yml

```yaml
userCredentials:
  - name: ERP_User
    kind: default                       # default, successfactors, openconnectors
    user: svc_erp
    password: {env: CPI_SECRET_USER_ERP_USER}
oauth2Credentials:
  - name: Graph
    tokenServiceUrl: https://login.microsoftonline.com/<tenant>/oauth2/v2.0/token
    clientId: 0d3c…
    clientSecret: {env: CPI_SECRET_OAUTH2_GRAPH}
    scope: https://graph.microsoft.com/.default
secureParameters:
  - name: Webhook_Key
    value: {env: CPI_SECRET_SECURE_WEBHOOK_KEY}
keyPairs:
  - alias: partner_signing
    pkcs12: {env: CPI_SECRET_KEYPAIR_PARTNER_SIGNING}
    password: {env: CPI_SECRET_KEYPAIR_PARTNER_SIGNING_PASSWORD}   # leave out for a PFX without one
sshKeys:
  - alias: sftp_partner
    privateKey: {env: CPI_SECRET_SSHKEY_SFTP_PARTNER}
    passphrase: {env: CPI_SECRET_SSHKEY_SFTP_PARTNER_PASSPHRASE}   # only for an encrypted key
pgpSecretKeys:
  - name: payroll                       # a label for the result
    keys: {env: CPI_SECRET_PGP_PAYROLL}
    passphrase: {env: CPI_SECRET_PGP_PAYROLL_PASSPHRASE}
```

**You don't have to write it by hand:** `cpictl backup create` writes `security/credentials.yml` for the tenant,
with every credential, key pair and PGP secret key and the variable each secret is read from
(`CPI_SECRET_<KIND>_<NAME>`). An SSH key appears under `keyPairs` (the keystore cannot tell them apart): move it
to `sshKeys`.

**Values:**
- Binary or multi-line material may be given as base64, and **should be**: cpictl decodes it itself. A single
  line can be masked in CI logs; masking works line by line.
- A `{file: …}` source may be binary (`.p12`) or text, relative to `credentials.yml`.

## What cpictl guarantees

- **Not as a command-line value:** a secret is never accepted as a flag value, so it stays out of shell history
  and process lists.
- **Not in the log:** never logged, not even with `--debug`. On a real tenant, runs at `--debug` were checked
  for every password, passphrase and key, also as base64 fragments: none.
- **Not in the output:** never in the result or the JSON output, which list kind, name and action only.
- **All or nothing:** every secret is resolved before anything is written. A missing variable stops the run
  with exit code 2 and no change.
- **Dry run:** `--dry-run` resolves and checks everything and talks to no tenant.
- **Partial failure:** a key pair with a wrong password fails with the tenant's message ("PKCS12 verification
  error, incorrect password"). The other entries are still written, and the exit code is 7 (partial).

## Rules for the pipeline

1. **Fetch and apply in the same step.** Export the values in the step that runs cpictl; when the step ends,
   they are gone. Don't write them to `$GITHUB_ENV`, files or artifacts.
2. **Never print a value.** No `echo "$value"`, no `set -x`, no `env`/`printenv` in that step.
3. **Mask every value** anyway, as defence in depth: `::add-mask::` in GitHub Actions, `##vso[task.setsecret]`
   in Azure DevOps, masked variables in GitLab.
4. **Store multi-line values as base64** (keys, PGP blocks): masks match only single lines.
5. **Log in to the vault with OIDC / workload identity**, not a stored vault password. Read access only, and
   only to the secrets of that tier: one vault, or one path prefix, per tier.
6. **Run the sync with the tier's SAP key, not through a cpictl gateway.** It is a machine job; gateway users are
   people.

## The loop

Every example below uses the same loop: for each variable that `credentials.yml` references, fetch the value
from the vault, mask it, and export it. Only `fetch` differs per vault.

```bash
set +x
for var in $(grep -o 'CPI_SECRET_[A-Z0-9_]*' credentials.yml | sort -u); do
  value=$(fetch "$var") || continue      # a missing secret: cpictl names it and stops
  mask "$value"
  export "$var=$value"
done
cpictl --profile prod credentials apply --file credentials.yml --output json
```

## Putting material into the vault

**Do it once, from a trusted workstation**, then delete the local copies:

```bash
base64 -w0 partner_signing.p12 > ps.b64          # macOS: base64 -i partner_signing.p12 > ps.b64
base64 -w0 id_sftp_partner     > ssh.b64
gpg --export-secret-keys --armor payroll@example.com | base64 -w0 > pgp.b64
```

- **Azure Key Vault:** names allow no `_`, so use `-`.

  ```bash
  az keyvault secret set --vault-name cpi-prod --name CPI-SECRET-KEYPAIR-PARTNER-SIGNING --file ps.b64
  ```

  A **Key Vault certificate** (an imported or Key Vault-issued PFX) can be used directly: read as a secret, it
  is the base64 PFX **without a password**. Name it after the variable and leave out `password`.
- **AWS Secrets Manager:**

  ```bash
  aws secretsmanager create-secret --name cpi/prod/CPI_SECRET_KEYPAIR_PARTNER_SIGNING --secret-string file://ps.b64
  ```
- **HashiCorp Vault (KV v2):**

  ```bash
  vault kv put secret/cpi/prod/CPI_SECRET_KEYPAIR_PARTNER_SIGNING value=@ps.b64
  ```
- **Google Secret Manager:** names allow `_`.

  ```bash
  gcloud secrets create CPI_SECRET_KEYPAIR_PARTNER_SIGNING --data-file=ps.b64
  ```

## GitHub Actions + Azure Key Vault

```yaml
permissions: {id-token: write, contents: read}
jobs:
  sync:
    runs-on: ubuntu-latest
    environment: prod                       # holds the SAP key of prod and the Azure IDs
    steps:
      - uses: actions/checkout@v4
      - uses: azure/login@v2                # OIDC, no stored vault password
        with: {client-id: '${{ vars.AZURE_CLIENT_ID }}', tenant-id: '${{ vars.AZURE_TENANT_ID }}', subscription-id: '${{ vars.AZURE_SUBSCRIPTION_ID }}'}
      - name: Sync CPI security material from Key Vault
        env:
          CPICTL_TMN_HOST: ${{ vars.CPI_HOST }}
          CPICTL_OAUTH_HOST: ${{ vars.CPI_OAUTH_HOST }}
          CPICTL_OAUTH_CLIENTID: ${{ secrets.CPI_CLIENT_ID }}
          CPICTL_OAUTH_CLIENTSECRET: ${{ secrets.CPI_CLIENT_SECRET }}
        run: |
          set +x
          for var in $(grep -o 'CPI_SECRET_[A-Z0-9_]*' security/credentials.yml | sort -u); do
            value=$(az keyvault secret show --vault-name cpi-prod --name "${var//_/-}" --query value -o tsv) || continue
            echo "::add-mask::$value"
            export "$var=$value"
          done
          cpictl credentials apply --file security/credentials.yml --output json
```

## Azure DevOps + Azure Key Vault

```yaml
- task: AzureCLI@2                          # service connection with workload identity federation
  inputs:
    azureSubscription: cpi-prod-keyvault-reader
    scriptType: bash
    scriptLocation: inlineScript
    inlineScript: |
      set +x
      for var in $(grep -o 'CPI_SECRET_[A-Z0-9_]*' security/credentials.yml | sort -u); do
        value=$(az keyvault secret show --vault-name cpi-prod --name "${var//_/-}" --query value -o tsv) || continue
        echo "##vso[task.setsecret]$value"
        export "$var=$value"
      done
      cpictl credentials apply --file security/credentials.yml --output json
  env:
    CPICTL_TMN_HOST: $(CPI_HOST)
    CPICTL_OAUTH_HOST: $(CPI_OAUTH_HOST)
    CPICTL_OAUTH_CLIENTID: $(CPI_CLIENT_ID)
    CPICTL_OAUTH_CLIENTSECRET: $(CPI_CLIENT_SECRET)   # secret variables are mapped explicitly
```

## GitHub Actions + AWS Secrets Manager

```yaml
- uses: aws-actions/configure-aws-credentials@v4     # OIDC role, read access to cpi/prod/* only
  with: {role-to-assume: '${{ vars.AWS_ROLE_ARN }}', aws-region: eu-central-1}
- name: Sync CPI security material from Secrets Manager
  env: {CPICTL_TMN_HOST: '${{ vars.CPI_HOST }}', CPICTL_OAUTH_HOST: '${{ vars.CPI_OAUTH_HOST }}',
        CPICTL_OAUTH_CLIENTID: '${{ secrets.CPI_CLIENT_ID }}', CPICTL_OAUTH_CLIENTSECRET: '${{ secrets.CPI_CLIENT_SECRET }}'}
  run: |
    set +x
    for var in $(grep -o 'CPI_SECRET_[A-Z0-9_]*' security/credentials.yml | sort -u); do
      value=$(aws secretsmanager get-secret-value --secret-id "cpi/prod/$var" --query SecretString --output text) || continue
      echo "::add-mask::$value"
      export "$var=$value"
    done
    cpictl credentials apply --file security/credentials.yml --output json
```

## GitLab CI + HashiCorp Vault

```yaml
sync-cpi-secrets:
  image: hashicorp/vault:1.17
  id_tokens:
    VAULT_ID_TOKEN: {aud: https://vault.example.com}
  variables: {VAULT_ADDR: https://vault.example.com}
  script:
    - export VAULT_TOKEN="$(vault write -field=token auth/jwt/login role=cpi-prod-read jwt=$VAULT_ID_TOKEN)"
    - |
      set +x
      for var in $(grep -o 'CPI_SECRET_[A-Z0-9_]*' security/credentials.yml | sort -u); do
        value=$(vault kv get -field=value "secret/cpi/prod/$var") || continue
        export "$var=$value"
      done
      ./cpictl credentials apply --file security/credentials.yml --output json
```

GitLab cannot mask a value at runtime: rules 1 and 2 matter all the more here (fetch and apply in one step,
never print).

## Google Secret Manager

```bash
value=$(gcloud secrets versions access latest --secret="$var" --project cpi-prod)
```

## Check coverage nightly

```bash
cpictl credentials apply --file credentials.yml --dry-run
```

This resolves every secret and writes nothing; it talks to no tenant. It fails with exit code 2, naming the
variable, when one is missing.

**Run it nightly** after `backup create`, with the vault loop and the new backup's `credentials.yml`. A secret
or key that lives only in SAP then shows up before you need it.

## Rotation

Update the value in the vault, then run the sync.

- **A renewed key pair** (new certificate) replaces the old one under the same alias. Partners that pin the
  certificate need the new one.
- **A new SSH key** replaces the old one. Hand the partner the new public key: `cpictl keystore ssh-public-key`.
- **A new PGP key** has a new key ID: it is added next to the old one. Remove the old key in the Web UI once
  partners use the new one.
- **Passwords and client secrets** are written on every run anyway.

## Disaster recovery

`cpictl backup restore` reads the same variables:

- **With the vault loop before the restore**, credentials, key pairs, SSH keys and PGP secret keys come back
  with everything else.
- **Without it**, the restore plan lists each one as a manual step, naming the variables to set.

See [backup.md](backup.md#restore).

## Go (CPI Studio, gateway)

```go
f, err := ops.LoadCredentialsFile("credentials.yml")   // or build an ops.CredentialsFile
res, err := ops.ApplyCredentials(exe, f, dryRun)
```

A service that holds the values itself (not in the environment) gives each one as
`ops.SecretSource{Stdin: strings.NewReader(value)}`. The result never contains a secret.
