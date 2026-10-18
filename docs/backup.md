# Backup and disaster recovery

`cpictl backup create` writes everything needed to rebuild a Cloud Integration tenant into one
archive. The pipeline uploads it to S3 or Azure Blob Storage, and the storage's lifecycle rules
rotate it. `backup diff` compares backups with each other or with the tenant, and `backup verify`
checks an archive.

```bash
cpictl backup create --dir ./backups --schedule-folders     # nightly
cpictl backup verify backups/daily/cpi-backup_mytenant_2026-10-17T02-00-00Z.tar.gz
cpictl backup diff backups/daily/<yesterday>.tar.gz backups/daily/<today>.tar.gz
cpictl backup diff backups/daily/<today>.tar.gz              # against the tenant now (metadata only)
```

## What a backup contains

| Content | In the archive | Restorable |
|---------|----------------|------------|
| Integration packages | `packages/<pkg>/package.json` | yes; packages from Discover (`mode` READ_ONLY) are copied from Discover again |
| Artifacts (flows, script collections, message and value mappings) | `packages/<pkg>/<id>.zip`, as the tenant exports them | yes |
| Externalised parameters | `packages/<pkg>/<id>.parameters.json` | yes |
| Partner Directory (string and binary parameters) | `partner-directory/` (the pd snapshot layout) | yes |
| What is deployed, in which version and status (with `--cells` also on integration cells) | `runtime/deployed.json` | yes: deploy again what was running |
| Credentials (user, OAuth2, secure parameters) | `security/credentials.yml`: metadata and one environment variable per secret | **the secrets come from your vault** (below) |
| Keystore | `security/keystore.json` (metadata), `security/certs/*.pem` (certificates) | certificates yes; key pairs **regenerated** (below); `sap_*` entries are SAP's |
| Access policies | `security/access-policies.json` | yes |
| Number ranges | `tenant/number-ranges.json` (with the current value) | yes: restore the counter, or numbers repeat |
| Variables, data stores, JMS queues | `tenant/*.json`: **names only** | the content is runtime data and not backed up |

Two more files describe the archive:
- **`manifest.json`:** tenant, time, cpictl version, counts, warnings, and the SHA-256 of every
  file.
- **`index.json`:** what a comparison needs: versions and checksums of artifacts and parameters,
  the deployed state, credential and keystore metadata, Partner Directory checksums.

### Secrets and private keys

The tenant API never returns passwords, client secrets or private keys, so no backup can contain
them.

- **Credentials:** `security/credentials.yml` has every credential with its metadata and an
  environment variable for the secret (`CPI_SECRET_USER_ERP_USER`, `CPI_SECRET_OAUTH2_GRAPH`,
  ...). Keep the secrets in a vault (Azure Key Vault, AWS Secrets Manager, your CI's secret
  store). On restore, set the variables from the vault and run
  `cpictl credentials apply --file credentials.yml`. The backup tells you exactly which secrets
  you need; the vault is their backup.
- **Key pairs and SSH keys** (`restore: regenerate` in `keystore.json`): a private key generated
  on the tenant cannot leave it. After a disaster:
  - generate the key pair again and have it signed (`keystore generate-key-pair`, `csr`,
    `upload-chain`; the backed-up certificate shows subject and issuer);
  - or upload the original key from where you keep it;
  - partners that trust the old certificate then need the new one.
- **Certificates** (`restore: certificate`) are restored from `security/certs/`.

### Not backed up

- Message processing logs, JMS message contents, data store entries and variable values
  (runtime data).
- API Management, Trading Partner Management (B2B), Event Mesh.
- SAP's own keystore entries (`sap_*`).

## Exit codes

- **`backup create`:**
  - 0: complete.
  - 7: the archive was written, but something could not be read (the manifest's `warnings`;
    for example a role missing on the service key). Pipelines should alert on 7.
  - An area the tenant doesn't offer (for example secure parameters on Cloud Foundry) is listed
    as `unavailable` and is not a warning.
- **`backup verify`:** 5 when a checksum, a file or an artifact zip is wrong, or the backup was
  incomplete.
- **`backup diff`:** 0. With `--fail-on-change`, 5 when something changed.

The service key needs read access to design time, runtime, security material and the Partner
Directory (role collections `PI_Integration_Developer` + `PI_Administrator`, or the matching
role templates of an API service key).

## Storage and rotation

cpictl writes the archive; the pipeline uploads it with the cloud's own CLI (OIDC or managed
identity, no keys in the pipeline). `--schedule-folders` puts the archive into `daily/`,
`weekly/` (Sundays) or `monthly/` (the 1st). Lifecycle rules then keep each folder for its own
time, and nothing in the pipeline deletes backups.

### GitHub Actions to S3

```yaml
name: cpi-backup
on:
  schedule: [{cron: "17 2 * * *"}]
  workflow_dispatch: {}
permissions: {id-token: write, contents: read}
jobs:
  backup:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: curl -sSL "https://github.com/cpars-innovation/cpicli/releases/download/v${V}/cpictl_v${V}_linux_amd64.tar.gz" | tar xz --strip-components=1 "cpictl_v${V}_linux_amd64/cpictl"
        env: {V: "0.22.0"}   # pin the cpictl version
      - name: Backup
        env:
          CPICTL_TMN_HOST: ${{ vars.CPI_HOST }}
          CPICTL_OAUTH_HOST: ${{ vars.CPI_OAUTH_HOST }}
          CPICTL_OAUTH_CLIENTID: ${{ secrets.CPI_CLIENT_ID }}
          CPICTL_OAUTH_CLIENTSECRET: ${{ secrets.CPI_CLIENT_SECRET }}
        run: |
          ./cpictl backup create --dir backups --schedule-folders --cells
          ./cpictl backup verify backups/*/*.tar.gz
      - uses: aws-actions/configure-aws-credentials@v4
        with: {role-to-assume: "${{ vars.BACKUP_ROLE_ARN }}", aws-region: eu-central-1}
      - run: aws s3 cp backups/ s3://my-cpi-backups/prod/ --recursive
```

Pin the cpictl version; the release assets are named `cpictl_v<version>_<os>_<arch>.tar.gz`.

S3 lifecycle (keep 14 days, 8 weeks, 12 months):

```json
{"Rules": [
  {"ID": "daily",   "Status": "Enabled", "Filter": {"Prefix": "prod/daily/"},   "Expiration": {"Days": 14}},
  {"ID": "weekly",  "Status": "Enabled", "Filter": {"Prefix": "prod/weekly/"},  "Expiration": {"Days": 56}},
  {"ID": "monthly", "Status": "Enabled", "Filter": {"Prefix": "prod/monthly/"}, "Expiration": {"Days": 365}}
]}
```

### Azure DevOps to Blob Storage

```yaml
schedules:
  - cron: "17 2 * * *"
    branches: {include: [main]}
    always: true
steps:
  - script: |
      ./cpictl backup create --dir backups --schedule-folders --cells
      ./cpictl backup verify backups/*/*.tar.gz
    env:
      CPICTL_TMN_HOST: $(CPI_HOST)
      CPICTL_OAUTH_HOST: $(CPI_OAUTH_HOST)
      CPICTL_OAUTH_CLIENTID: $(CPI_CLIENT_ID)
      CPICTL_OAUTH_CLIENTSECRET: $(CPI_CLIENT_SECRET)
  - task: AzureCLI@2
    inputs:
      azureSubscription: cpi-backup            # service connection (workload identity)
      scriptType: bash
      scriptLocation: inlineScript
      inlineScript: az storage blob upload-batch --auth-mode login --account-name mycpibackups -d backups --destination-path prod -s backups
```

Azure lifecycle management policy:

```json
{"rules": [
  {"name": "daily",   "enabled": true, "type": "Lifecycle", "definition": {"filters": {"blobTypes": ["blockBlob"], "prefixMatch": ["backups/prod/daily/"]},   "actions": {"baseBlob": {"delete": {"daysAfterModificationGreaterThan": 14}}}}},
  {"name": "weekly",  "enabled": true, "type": "Lifecycle", "definition": {"filters": {"blobTypes": ["blockBlob"], "prefixMatch": ["backups/prod/weekly/"]},  "actions": {"baseBlob": {"delete": {"daysAfterModificationGreaterThan": 56}}}}},
  {"name": "monthly", "enabled": true, "type": "Lifecycle", "definition": {"filters": {"blobTypes": ["blockBlob"], "prefixMatch": ["backups/prod/monthly/"]}, "actions": {"baseBlob": {"delete": {"daysAfterModificationGreaterThan": 365}}}}}
]}
```

### Recommendations

- **Immutable storage:** turn on S3 Object Lock (compliance mode) or Azure immutable blob storage
  (time-based retention) for the retention period, so a compromised pipeline or account cannot
  delete or change backups. Give the pipeline write-only access (`s3:PutObject` /
  `Storage Blob Data Contributor` without delete).
- **Encryption at rest:** S3 SSE-KMS or Azure customer-managed keys. The archive contains
  Partner Directory values and parameters, so restrict who can read the bucket.
- **Copy to another region or account** (S3 replication, Azure object replication) for a
  regional outage.
- **Back up before every transport** to production too, not only nightly.
- **Monitor:** alert when the nightly job fails or exits 7, and when no backup arrived in the
  last 26 hours.
- **Drift:** `cpictl backup diff <last>.tar.gz --fail-on-change` in the morning shows what
  changed on production since the night, outside of transports.

## Still to come

`backup restore`, with `--dry-run` and a list of the manual steps, comes next. Until then a
restore uses the existing commands:
1. `update artifact` / `snapshot restore` from the unpacked zips.
2. `configure` with the parameters.
3. `pd deploy` from `partner-directory/`.
4. `credentials apply`.
5. `keystore import-cert`.
6. `deploy` of what `runtime/deployed.json` lists.

Optional client-side encryption of the archive (off by default) follows after that.
