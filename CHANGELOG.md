# Changelog

All notable changes to cpictl. Coming from FlashPipe? See
[docs/migrating-from-flashpipe.md](docs/migrating-from-flashpipe.md).

## Unreleased

## 0.32.0

- **Security material from a vault: one command for every type** ([vault.md](docs/vault.md)).
  `cpictl credentials apply` now also imports **key pairs** (PKCS#12 / PFX), **SSH keys** (OpenSSH or PEM) and
  **PGP secret keys**, next to user credentials, OAuth2 client credentials and secure parameters. A pipeline
  reads the vault, exports the values, and runs the command; cpictl itself talks to no vault, so any vault works.
  - Values may be base64 (vault-friendly, maskable in CI logs) or files. A PFX without a password (as Azure Key
    Vault exports a certificate) works without `password`.
  - Key pairs, SSH keys and PGP keys the tenant already has are left alone (same certificate fingerprint,
    same public key, same key ID), so a nightly sync does not rewrite the keystore; a renewed key pair or a new
    SSH key replaces the old one. Credentials are written each time.
  - Never logged, not even with `--debug`, never in results. All secrets are resolved before anything is
    written; `--dry-run` talks to no tenant. All of it was run against a real Cloud Foundry tenant, including
    repeat runs, a renewed key pair and a scan of the `--debug` output for every secret.
  - `docs/vault.md`: what can and cannot come from a vault, the pipeline rules, and examples for Azure Key Vault
    (GitHub Actions, Azure DevOps), AWS Secrets Manager, HashiCorp Vault (GitLab) and Google Secret Manager.
  - `backup create` lists key pairs and PGP secret keys in `credentials.yml` too
    (`CPI_SECRET_KEYPAIR_<ALIAS>` + `_PASSWORD`, `CPI_SECRET_PGP_<KEYID>` + `_PASSPHRASE`), and `backup restore`
    imports them when the variables are set; otherwise a manual step names the variables.
- **Fixed: `keystore generate-ssh-key --ssh-file`** sent the key as text; the tenant reads it only as base64
  ("cannot load key"). Importing an SSH key works now.
- **Fixed: secure parameters are not Neo-only.** They work on Cloud Foundry too; the docs and help said
  otherwise.
- **Go:** `ops.KeyPairSpec`, `ops.SSHKeySpec`, `ops.PGPSecretKeySpec` in `ops.CredentialsFile`;
  `cpi.Security.ImportKeyPair`, `PreviewKeyPair`, `AddPgpSecretKeys`; `cpi.KeystoreEntry.FingerprintSha256`.

**What to do with this release:**
- **Pipelines / platform team:**
  - Put the vault sync from `docs/vault.md` into the pipeline of each tier.
  - Run `credentials apply --dry-run` nightly after `backup create`, so a secret that exists only in SAP shows
    up early.
  - Keep key pairs you import (instead of generating them on the tenant) in the vault, so a restore can bring
    them back.
- **CPI Studio:**
  - Nothing breaks: pin v0.32.0.
  - When Studio creates credentials or keys, call `ops.ApplyCredentials` with
    `ops.SecretSource{Stdin: strings.NewReader(value)}` per value. Do not log, store or echo the value, and never
    let an agent see it.
  - Agents add entries to `credentials.yml` (`{env: CPI_SECRET_...}`) and ask a person to put the value into the
    vault.
- **cpictl gateway:**
  - Nothing to change.
  - The vault sync is a pipeline job with the tier's SAP key, not a gateway call; users of the gateway need no
    `credentials:write` / `keystore:write` for it.
- **Agents (MCP, skills):** the server instructions and the cpi-build skill now say how to request security
  material: add the entry to `credentials.yml` and ask the user to put the value into the vault. Values never
  pass through MCP.

## 0.31.0

- **MCP offers only the tools an access scope can allow** (#40): with `--scope-file` or in gateway mode (your scope
  from the gateway's `/whoami`, read at start), a tool whose action the scope grants on no package (no `deploy`
  anywhere: no `deploy`) or a tenant area it does not grant is not offered; `help` names the reason. Tools the
  scope allows on some packages stay, and each call is still decided per package.
- **Gateway sign-in: the refresh token is in the OS keychain** (#42): macOS Keychain, Windows Credential Manager,
  Linux Secret Service. The access token (about an hour) stays in the `0600` file. Without a keychain (CI,
  headless Linux) the file holds it all, as before; `CPICTL_TOKEN_STORE=file` forces that. A token stored by
  0.30 moves to the keychain on first use; `auth logout` clears both. New dependency: `github.com/zalando/go-keyring`.
- **`cpictl changed-since`** / MCP `changed_since` / Go `ops.DesigntimeChangedSince` (#29): has designtime content
  changed since a time, and what (newest first, with who changed it)? SAP has no single call for it, so the
  artifacts of all packages are listed in parallel (3 s for a small real tenant). The result's `fingerprint`, passed
  back with `--fingerprint`, also shows deletions, which no change time can.
- **Access scope: test tooling messages decided by the flow they tested** (#48). With `test` on any package, the
  messages of the shared test harness and receiver stub (and their attachments and recordings) were readable, also
  those of other teams' tests. Now a tooling message is readable only when the flow it tested is in the scope with
  `test`, found through the correlation ID the harness, the flow and the stub share (verified on a tenant).
  The messages of XSLT and script test runs, which test no flow, are refused under a scope (the runs themselves
  work). **Who is affected:** only scopes with
  `test` (also through a cpictl gateway); a test run sees its own messages as before.
- **Access scope: stub rules and stylesheets under test belong to their owner** (#48). The test tooling's
  Partner Directory partners (`CPICTL_Stub`, `CPICTL_Test`) are shared by teams. Under a scope, a parameter there
  is readable and writable only when its ID starts with a flow the scope grants `test` on, or a partner it may
  read, and a dot: name stub rules `<flowId>.<name>` (`Orders_Route.erp`). Stylesheets under test are already
  named `<partner>.<parameter>`. Others are hidden and refused, with a message that says how to name them.
  **Who is affected:** scopes with `test` whose stub rules are not named that way. Without a scope nothing
  changes; the docs and the cpi-test skill now use the naming everywhere.
- **`secret-scan` / `secretscan.Text` finds HTTP authorization values in text** (#47): `Authorization: Bearer …`,
  `"authorization": "Bearer …"`, `authorization=Basic …` and `<Authorization>…</Authorization>`, in binary Partner
  Directory parameters and files. Basic only when it decodes to `user:password`; same fingerprint as
  `secretscan.Value` for the same value.

## 0.30.1

- **`cpictl auth status`** shows which SAP key the gateway uses for you per tenant (`credential`,
  when the gateway sends it): SAP's own audit names that key, not you.

## 0.30.0

- **`cpictl auth` for gateway mode** ([configuration.md](docs/configuration.md#gateway-mode)):
  - `cpictl auth login`: sign in to the cpictl gateway with Entra ID in the browser (or
    `--device-code` on a headless host). The token is kept per profile and refreshed silently, so a
    long-running `cpictl mcp` keeps working past the first token's hour.
  - `cpictl auth logout`: clears the stored token of the profile.
  - `cpictl auth status` (short: `cpictl whoami`): who the gateway takes you for, roles, token
    expiry, and your scope per SAP tenant (#43).
  - `CPICTL_GATEWAY_TOKEN` still works without signing in. A project `cpictl.yaml` may not set
    `login`. Profiles that talk to SAP directly are not affected.
  - Sign-in fits the Entra registration (#46): the redirect URI is `http://localhost:<port>`, which
    matches a registered `http://localhost` (before, `127.0.0.1/callback` would have been refused with
    AADSTS50011). An expired or revoked sign-in says "run cpictl auth login" instead of Entra's
    `invalid_grant`. Token requests use cpictl's HTTP client with its timeout, so a hung token
    endpoint cannot hang `cpictl mcp`. The Entra app registration is documented.
- **Gateway refusals are reported as such** (#40): a request the cpictl gateway refuses reads
  "refused by the cpictl gateway: …" (it no longer says "the tenant says") and exits with code 9
  (scope), not 3 (authentication). `tenant-settings` / MCP `get_tenant_settings` fail when no part
  could be read instead of answering ok with three warnings. **Go:** `ops.GetTenantSettings` now
  returns `(*TenantSettingsResult, error)`; `httpclnt.IsGatewayRefusal(err)` tells a gateway
  refusal from a tenant error.
- **Docs:** secrets from a vault in the pipeline (Azure Key Vault, AWS Secrets Manager), masking,
  and a nightly coverage check with `credentials apply --dry-run`
  ([backup.md](docs/backup.md#secrets-from-a-vault-pipeline)).

- **`cpictl backup restore`** ([backup.md](docs/backup.md#restore)): puts a backup back into a
  tenant, the same one or a new one.
  - **Restored:** packages, artifacts (as backed up), externalised parameters, the Partner
    Directory, credentials whose secret is set in its environment variable, certificates, access
    policies, number ranges (never lowered), and the deployments that were running.
  - **Manual steps,** listed as a checklist: secrets not given, key pairs and SSH keys, standard
    packages from Discover, integration cells.
  - **Dry run:** `--dry-run` writes nothing; `--plan-file` saves the plan, or the report of a real
    restore, as Markdown (JSON for a `.json` name).
  - **Safe and repeatable:** writing needs `--confirm`; a damaged archive is refused; nothing is
    deleted; a second run changes nothing. `--only` and `--packages` restore part of a backup.
  - Tested on a real tenant: a deleted flow, a changed Partner Directory value and a deleted number
    range came back, and the flow was deployed again.

## 0.29.0

**Who is affected:** only users of an access scope (`--scope-file`, `--scope-*`, MCP servers started
with a scope). Without a scope nothing changes. With one: downloading message contents and sending
test messages now need the action on that flow's package (a runtime flow whose designtime artifact
no longer exists is refused for them), and requests below a message or runtime artifact other than
the lists cpictl reads are refused. cpictl's own commands and MCP tools work as before.

- **Access scope: `grants` and `deny`** ([access-scope.md](docs/access-scope.md#grants-and-deny)). A scope can give
  different actions on different packages (deploy on `SD_*`, only read on `FI_Shared`) and deny packages or artifacts
  whatever is granted. Message contents and test messages are now decided on the message's flow, not only on whether
  the action is granted at all. This is the form the cpictl gateway builds per user from its policy.
- **Access scope: requests a server could read differently from the guard are refused**, also inside a `$batch`:
  dot segments (also encoded, or as `..;`), encoded slashes, backslashes and `%`, empty path segments, `;` in API
  paths, repeated query parameters (also differing in case), method override headers (`X-HTTP-Method`), compressed
  request bodies, JSON bodies that repeat a key, designtime types other than the four cpictl uses, and artifact
  versions that are not plain IDs. cpictl itself never sends these; under a scope they could address another
  package than the one checked.
- **Fixed: under a scope, message contents were reachable through a message's own path** (for example
  `MessageProcessingLogs('…')/Attachments('…')/$value`), checked only as `read`. Below a message and a runtime
  artifact only the lists cpictl reads are allowed now, and every path naming `$value` or `TraceMessages` needs
  `download`.
- `pkg/scope`: `Guard.Check` decides a request like `Before` and reports what it touches (`Access`) and whether the
  answer must pass through `After` (`ReadsAnswer`), for proxies that audit requests and stream content.
- **Gateway mode** ([configuration.md](docs/configuration.md#gateway-mode)): `gateway: https://gw.example.com/t/dev`
  in a profile (or `--gateway`) sends every API call to a cpictl gateway with the user's token from
  `CPICTL_GATEWAY_TOKEN` instead of to the tenant with a service key; the gateway decides per user which packages
  are visible. SAP credentials in the same configuration are refused, and so is `gateway` in a project
  `cpictl.yaml`. `cpictl login` and test messages through the gateway follow.
- `pkg/httpclnt`: `Options.BearerToken` and `Options.BasePath`; `pkg/cpi`: `ServiceDetails.Gateway`,
  `ParseGateway`.

## 0.28.0

- **Security material audit.** Passwords, tokens and keys belong in the secure store and the
  keystore; new checks find them elsewhere ([lint.md](docs/lint.md#security-material)):
  - New lint rules (group `security`) follow values read from the SecureStoreService
    (credentials, secure parameters, OAuth2 tokens), the KeystoreService and token endpoint
    responses through scripts and content modifiers:
    - `secret-target-name` (error): stored only in the headers and properties named in
      `.cpi/lint.yaml` `security` (default headers `Authorization`, `Proxy-Authorization`,
      `X-API-Key`, `APIKey`; properties `secret*`).
    - `secret-logged` (error): never in the message processing log, a logger, `println` or an
      exception text.
    - `secret-header-sent` (error): a secret header is not passed on by a JMS, ProcessDirect,
      AMQP or Kafka receiver or by a wildcard in a receiver's request headers.
    - `secret-header-inbound` (warning): Allowed Header(s) of the flow do not accept a secret
      header from senders.
    - `secret-in-body` (warning): security material in the message body.
    - `undeclared-auth-header` (warning): a header that reads like an authentication header
      (`X-Partner-Token`, `Ocp-Apim-Subscription-Key`) is not declared yet.
  - Projects declare their own authentication headers and secret properties in `.cpi/lint.yaml`
    `security`; the names add to the defaults.
  - The skills follow it: cpi-discover finds the project's authentication headers and writes
    them into `.cpi/lint.yaml` and conventions.md, cpi-build keeps credentials in them only,
    the review checklist and the cpi-reviewer agent treat `secret-*` findings as blockers.
  - `hardcoded-secret` also checks configure files (`repository.configure`) and recognises
    private keys, JSON web tokens, provider access keys and `Basic`/`Bearer` values whatever the
    parameter's name.
  - New command `cpictl secret-scan` and MCP tool `scan_secrets`: the tenant's Partner Directory
    (string and binary parameters, files inside ZIPs) and the configuration parameters of every
    integration flow; `--pd-dir` scans a pd snapshot folder. Findings carry the kind, the reason
    and a fingerprint, never the value. Exits with code 5 when it finds something.
- **Fixed: `pd deploy --full-sync --dry-run` did not say what it would delete.** It now lists the
  parameters (`deleted` in the result) and deletes nothing, as before.

## 0.27.0

- **Removed: the seven MCP tool names deprecated in 0.25.0** (announced in 0.26.0):
  `get_message_steps`, `get_message_attachment`, `get_message_store_entry`, `get_trace_message`,
  `get_runtime_status`, `upload_artifact`, `get_variable`.
  - A call to an old name fails with `errorCategory: usage` naming the replacement (table under
    0.25.0).
  - A `--tools` / `--disable-tools` filter that names one of them is refused at start, naming the
    replacement.
  - The CLI's old command names (`pd-deploy`, `log-level`, ...) stay.
- **Fixed: MCP tool descriptions still pointed agents at the deprecated names** ("next:
  get_message_steps", "before upload_artifact"); they name the replacements now.
  `upload_artifacts` carries the full description of the former `upload_artifact` (draft skip,
  directory layout, next steps).

## 0.26.0

- **Announcement: the seven MCP tool names deprecated in 0.25.0 are removed in 0.27.0**
  (`get_message_steps`, `get_message_attachment`, `get_message_store_entry`, `get_trace_message`,
  `get_runtime_status`, `upload_artifact`, `get_variable`).
  - Switch to the replacements listed under 0.25.0 before upgrading to 0.27.0.
  - In 0.27.0 a call to an old name fails with `errorCategory: usage` naming the replacement.
  - The old CLI names (`pd-deploy`, `log-level`, ...) stay.
- **CI runs the test suite on Windows too;** a failure there fails the run.

- **Fixed (macOS and Windows): MCP tools refused new files below the server root** ("outside the
  server root") when the root's path goes through a symbolic link (macOS `/var` -> `/private/var`)
  or an 8.3 short name (Windows). Examples: `discover_tenant`'s `.cpi/`, a new `download_artifact`
  directory. Error texts with Windows paths are made root-relative too.
- **Hardened:** a dangling symbolic link below the MCP server root is refused (writing through it
  would create its target, which may be outside the root).
- **Fixed:** with a zero status interval, deploy and undeploy could reuse the previous runtime
  list on a coarse clock (Windows) and time out early. They now read it fresh every time.
- **Windows:** the repository checks out with LF line endings (`.gitattributes` `eol=lf`), so
  golden files, front matter and schemas compare the same on every OS.

## 0.25.0

- **CLI: grouped names for four commands (from the review in #37).**

  | Now | Before (still works) |
  |---|---|
  | `pd snapshot` | `pd-snapshot` |
  | `pd deploy` | `pd-deploy` |
  | `logs level` | `log-level` |
  | `logs files` | `log-files` |

  - The old names keep working exactly as before and are no longer listed. Nothing is printed
    for them, so stderr stays JSON lines with `--output json`.
  - Flags, config keys (`pd-deploy.*` in `cpictl.yaml`), environment variables and output are
    unchanged. The envelope's `command` is the name you used.
  - No pipeline needs to change. The docs use the new names.

- **Smaller MCP tool list (from the review in #37).**
  - **Seven tools are now covered by others** of the same kind (read or tenant change) and no
    longer listed. They still work when called, and the result's `notice` names the
    replacement:

    | Deprecated | Use instead |
    |---|---|
    | `get_message_steps` | `get_message_log` with `include_steps` (error and failing step in one call) |
    | `get_message_attachment`, `get_message_store_entry` | `get_message_content` with `attachment_id` or `store_entry_id` |
    | `get_trace_message` | `get_message_trace` with `trace_id` |
    | `get_runtime_status` | `list_runtime_artifacts` with `artifact_ids` |
    | `upload_artifact` | `upload_artifacts` (one or several) |
    | `get_variable` | `list_variables` with `name` |
  - **Payload redaction** (`--redact-payloads`) covers the replacements.
  - **With `--toolset`, the instructions are only those of the toolsets** (about half the size).
  - **The example configurations for Codex, Cursor, OpenCode and Gemini CLI use `--toolset`**:
    `build,test` for development, `inspect,monitor` read-only. That's about 12–17k tokens instead
    of about 28k on every prompt. Claude Code loads tool definitions on demand itself.
  - Skills, the reviewer agent and the docs use the new forms.
- **Windows:**
  - In the MCP server, a rooted path without a drive (`\x`) and a drive-relative one (`D:x`) are
    refused instead of being mapped below the root.
  - The test suite is portable (home directory, symbolic links, file modes). CI runs it on
    Windows too, as a report for now.

## 0.24.0

- **Script cases on the tenant: `cpictl test scripts cases --online` (#13).** The same case files
  as the local run, on the tenant's runtime ([docs/testing.md](docs/testing.md#script-cases-on-the-tenant)).
  - Per script and function a test flow is generated in the test tooling package, deployed,
    called once per case and deleted (`--keep` keeps it).
  - Groovy v1 and v2 and JavaScript, verified on a real tenant. Cases that need local-only values
    (`call.args`, `pd`, `secure`, ...) are skipped with the reason.
  - Refused on production tiers. MCP: `run_script_cases_online`. Go: `scripttest.RunCasesOnline`,
    `testtools.ScriptFlow`.
- **Corrected:** our docs said a version 2 script's `getHeader(name, Class)` fails on the runtime. It
  doesn't: today's runtime accepts it (verified on a tenant). It is still not part of the v2 API,
  and the stand-ins still refuse it.

- **JSON schemas of all results (#32):** `schemas/` has a JSON Schema (2020-12) for the
  `--output json` envelope and for the `result` of every command, generated from the Go types the
  commands return. `schemas/index.json` lists them, and releases attach
  `cpictl_<version>_schemas.tar.gz`.
  - Tests check real outputs against the schemas.
  - A breaking change to a result (a removed field, a changed type, a field no longer always
    present) needs a new `schemaVersion`.

- **Skills list their inputs in the front matter (`inputs:`, #35)** in the format CPI Studio uses to
  ask for them in one form ([docs/plugin.md](docs/plugin.md#skill-inputs)). Each skill's text
  names the same inputs, and the agent asks only for the missing ones.

- **Error groups of `logs summary` / `message_summary` show when they happened (#27):**
  - `occurrences` lists the time and message GUID per failure, and `buckets` counts them per time
    bucket (`--buckets`, default 24).
  - `step` names the failing model step from the tenant's error information.
  - The step is part of the fingerprint now, so the same text at two steps makes two groups.
    Fingerprints of errors with a known step change.

## 0.23.0

- **Partner Directory binaries can be inspected: `pd get --inspect` (#30).**
  - Shows the type the bytes really are, with a mismatch flag when it is not the declared type.
  - Decompresses gz and zlib, and lists zip entries (`--zip-entry` reads and inspects one).
  - Parses certificates: subject, issuer, validity and days left, key, SANs and fingerprints.
  - Bounded against zip bombs, standard library only.
  - Go: `ops.InspectPDBinary` and `ops.GetPDParametersWith`. MCP: `get_pd_parameters` with
    `inspect` and `zip_entry`.
- **Lint findings carry the line of their element** (`line`, 1-based, in `file`; #31).
- **Component catalogue as a Go API (#28):**
  - `ops.ImportComponents(old, data, tenant)` is what `components import` does: it keeps the
    learned entries and their date.
  - `Catalog.Find(uri)` returns the entry for a `cmdVariantUri` with cpictl's variant matching.
- **Mock tenant: Partner Directory parameters carry `CreatedBy`, `CreatedTime`, `LastModifiedBy`
  and `LastModifiedTime` like the real tenant,** and `$select` returns only the selected fields
  (#34).
- **`cpi-discover` skill:** the naming table of `.cpi/conventions.md` starts with the anchored
  regex of each rule, and the skill writes the flow ID rule into `.cpi/lint.yaml` (#36).

### Fixed

- **Custom CAs added to `http.DefaultTransport` after start are used (#25).** cpictl cloned the
  default transport once, when the package was loaded; now it clones it per client. To set your own
  CAs or proxy explicitly, pass `httpclnt.Options.HTTPClient`.

## 0.22.0

- **Backups for disaster recovery: `cpictl backup create | verify | diff` ([docs/backup.md](docs/backup.md)).**
  One archive per run holds:
  - every package and artifact (zip), the externalised parameters and the Partner Directory;
  - what is deployed (`--cells` for integration cells), number ranges and access policies;
  - credentials as metadata, with `credentials.yml` naming one environment variable per secret
    (secrets are not readable from the tenant);
  - keystore metadata and certificates (key pairs are marked "regenerate");
  - the names of variables, data stores and JMS queues.

  `manifest.json` has a checksum per file, `index.json` the data for quick comparisons.
  - `--schedule-folders` writes into `daily/`, `weekly/` and `monthly/` for storage lifecycle rules.
  - `backup verify` checks checksums and artifact zips. `backup diff` compares two backups, or a
    backup with the tenant (metadata only).
  - Exit code 7 when something could not be read.
  - docs/backup.md has GitHub Actions to S3 and Azure DevOps to Blob Storage examples, lifecycle
    rules and immutability.

- **Deployments are watched from one runtime list, with one timeout ([docs/deployments.md](docs/deployments.md)).**
  deploy, configure, orchestrator and `deploy --pending` read the runtime list once per interval for
  all deployments in progress (before: one request per artifact and check).
  - configure, orchestrator and `deploy --pending` no longer go package by package. Script collections
    and mappings are deployed first, then all flows. Up to `parallelDeployments` are triggered at a
    time, and all are watched together.
  - **New: `--deploy-timeout` / `deploy.timeout` / `configure.deployTimeout` / `orchestrator.deployTimeout`
    / `CPICTL_DEPLOY_TIMEOUT`** (default 10m): how long to wait for all deployments of the run together.
    `--max-check-limit` / `maxCheckLimit` and `--deploy-retries` / `deployRetries` are deprecated. When set
    without a timeout, the timeout is checks × interval (the same total as before).
  - Logs show state changes and a closing summary (`finished in 2m13s: 40 deployed, 1 failed`)
    instead of every check.
  - `undeploy` and `delete` read the status from the list too.
- **`cpictl runtimes watch`**: every deployment, status change and undeployment on the tenant as JSON
  lines, from one request per interval, from any source (Web UI included). Errors carry the tenant's
  text, and `--cells` also covers integration cells. With `--webhook` each event is POSTed (signed with
  `CPICTL_WEBHOOK_SECRET`). Go: `ops.WatchRuntime`, `ops.DiffRuntime`.

## 0.21.0

- **Access scope ([docs/access-scope.md](docs/access-scope.md)).** `--scope-file scope.json`, or the
  `--scope-packages`, `--scope-artifacts`, `--scope-partners`, `--scope-tenant` and `--scope-actions` flags, on any
  command and on `cpictl mcp`. It restricts the process to some packages, artifacts and Partner Directory partners,
  explicit tenant-wide areas and a set of actions (read, download, upload, deploy, configure, test, log-level,
  delete, pd-write). Everything else is hidden or refused (default deny).
  - It is enforced in the HTTP client for every request, whatever command or tool sends it: writes are refused before
    they are sent, every `$batch` operation is checked, and lists are filtered.
  - Refusals have exit code 9 and `errorCategory: "scope"`, and appear as `scope.refused` events; MCP `help` shows the
    scope and its SHA-256.
  - Go: `pkg/scope`, the same matcher for CPI Studio.
  - Without a scope nothing changes.
  - Before release, a security review of the guard found six ways around it (harness calls to any ProcessDirect
    flow, a `$batch` form the check did not parse, writes to filtered list URLs, sibling endpoint paths, tooling
    names matched by prefix, `$expand` carrying hidden entities). All are closed and tested; configure and
    orchestrated commands whose items were refused by the scope now also end with exit code 9.

- **Scripts are documented for reuse ([docs/script-docs.md](docs/script-docs.md)).** A comment template
  for Groovy scripts: a first sentence with the words someone would search for, then `@reads`, `@writes`,
  `@body`, `@throws`, `@keywords`, `@example`.
  - discover and the graph read the tags. Script nodes now carry `reusable` (`true` for scripts of
    script collections), `keywords`, `reads` and `writes` (headers and properties the code uses by name),
    `docStatus` (`ok`, `incomplete`, `missing`), `usages` (flows using the script) and `shape` (equal for
    scripts that differ only in comments and formatting, to find near-duplicates).
  - `graph_search` with several words finds nodes that contain all of them (`"format date"`).
  - New lint rules `collection-script-doc-quality` (warning: placeholder first sentence, headers or
    properties the code uses that the comments do not name) and `collection-script-keywords` (info);
    `script-doc` also reports placeholder first sentences.
  - `cpictl script-doc <files>` checks single scripts offline (`--fail` for CI); `--hook` runs it as a
    Claude Code hook, which the cpi plugin now installs: after the agent writes a Groovy script it gets
    the missing parts back right away.
  - The skills search the graph for reusable scripts before writing one and use the template.
- **Graph: adapter and step versions, mappings.** Every adapter and step a flow uses is a `component` node
  (`component:adapter/HTTP/Receiver`, "OData receiver (OData V2)") with the versions in use, the newest and
  how many flows are behind it; the flow's version is on the `uses_component` edge. With the editor's
  component catalogue (`cpictl components import`) the node also says what the tenant offers and how many
  flows are outdated. Mapping files (by content hash, so copies are one node), referenced message mapping
  artifacts and value mappings that scripts look up are nodes too (`mapping`, `value_mapping`).
  See [docs/graph.md](docs/graph.md#versions-of-adapters-and-steps).

Security hardening (from a security review of the codebase):
- **IDs are escaped in every SAP API URL.** Package, artifact, runtime, Partner Directory, configuration and API
  Management keys were put into URLs as they came. An ID like `X')/IntegrationPackages('Y` changed which entity the
  tenant addressed. Ordinary IDs (letters, digits, `_.-`) produce the same URLs as before.
- **Git revisions from tool arguments are checked.** A revision starting with `-` is refused before git runs
  (`git diff --output=FILE` would have written a file; `lint --changed-since`, `compare`/`transport_check` `git:`).
- **Unzip is limited and safe by construction:** at most 1 GiB uncompressed and 100,000 entries; permissions are no
  longer taken from the archive (no setuid, no world-writable files); symlink entries are refused; a symlinked
  directory inside the destination cannot redirect files outside it.
- **The MCP server refuses requests larger than 16 MiB** with a parse error and keeps the session running, instead
  of growing memory without bound.
- **A config file every user may write is refused**, because it decides which hosts the credentials go to
  (`chmod o-w`). Group-writable files are still accepted.
- **HTTP:** TLS 1.2 minimum; responses are capped at 1 GiB in memory; logged tenant error bodies have passwords,
  client secrets, tokens, `Authorization` headers and private keys replaced by `***`.
- **CI:** `govulncheck`; GitHub Actions are pinned by commit SHA.

## 0.20.0

- **Integration cells ([#24](https://github.com/cpars-innovation/cpicli/issues/24)).** A flow bound
  to an integration cell deploys there, but the public API lists only the Cloud Integration runtime,
  so `deploy` ran into a timeout (`task status "SUCCESS", runtime status "NOT_DEPLOYED"`). It now
  looks the flow up on the tenant's other runtimes (the Web UI's runtime list, which the tenant
  answers for the API key) and reports `DEPLOYED` with `runtimeLocation`, or `FAILED`. When the
  flow is nowhere, the timeout says the task succeeded and names the cell as the likely place.
  New `cpictl runtimes` (MCP `list_runtimes`) lists the runtimes and what is deployed on each.
  The mock tenant has cell-bound flows (`Artifact.RuntimeLocation`, `Runtime.Location`) and both
  Web UI endpoints. Go: `ops.ListRuntimes`, `cpi.Runtime.Locations`, `cpi.Runtime.LocationArtifacts`,
  `ops.Result.RuntimeLocation`.
- New lint rule `incomplete-script-step` (error, fixed by default; reported by CPI Studio on
  [#20](https://github.com/cpars-innovation/cpicli/issues/20)). A script step without
  `activityType`, `subActivityType` or `componentVersion` passes Check and deploys, but the runtime
  fails it (`"mappingClass" is null`). Go: `iflow.ScriptStepGaps`, `iflow.CompleteScriptSteps`.
- `unsupported-script-class` is now info, not warning. Studio deployed a minimal `Thread.sleep` on an
  integration cell without a refusal, so "a cell refuses Thread" no longer holds as stated. The stub
  (1.4.0) sleeps with `TimeUnit.MILLISECONDS.sleep`, which a cell accepts.
- Script pattern rules (`println`, `body-as-string`, `unsupported-script-class`, ...) no longer match
  inside comments.

## 0.19.0

- **Test tools run on a tenant again (harness 1.2.0, stub 1.3.0, XSLT flow 1.1.0;
  [#20](https://github.com/cpars-innovation/cpicli/issues/20)).** The flows cpictl shipped had no
  `<bpmn2:incoming>`/`<bpmn2:outgoing>` references. A tenant deploys such a flow, but its runtime
  builds the route from them: the process never ran, and the message completed with the body it
  came in with. That is why the harness echoed the body without calling the ProcessDirect address,
  which we had put down to two runtimes. Run `cpictl test tools install --deploy`. A send through an
  older harness that finds no message of the flow under test now names that cause. The mock tenant's
  demo content had the same defect and is fixed; the mock now runs no step of such a flow either.
- New lint rule `missing-flow-reference` (error, fixed by default by `lint --fix`): elements that do
  not list their sequence flows. Flows from SAP's editor always have them; generated or hand-written
  models may not.
- `send` and test runs wait up to two minutes for the endpoint of a flow that is `STARTED` but not
  yet listed in `ServiceEndpoints`. On the tenant we tried, the endpoint appeared about 80 s after the
  deploy reported success. The mock tenant can simulate this (`EndpointDelay`,
  `Artifact.EndpointHidden`).
- Message log attachments are read from a real tenant: `PayloadSize` comes as a string there (`"33"`),
  which failed the attachment list with a warning.
- `test xslt --online` verified on a tenant (the XSLT test flow had never run there before 1.1.0,
  see above). A failed message's answer (HTTP 500) has no ID headers there, only `The MPL ID for the
  failed message is : <ID>`: `send` and test runs take the message ID from that text, and online
  XSLT cases read the error (Saxon's message) from the message log. The tenant's engine does not
  report the text of `xsl:message terminate="yes"` (`Processing terminated by xsl:message at line
  N`): see docs/testing.md. The mock answers the same way.
- `credentials set-*` no longer logs the tenant's 404 as a warning when it creates a credential.

- Verified on a second real tenant: `test tools install` without `--package` now really adopts the
  package where tools already are (the flag defaulted to `CPICTLTesting`, which made the 0.18.0
  fix refuse instead, [#21](https://github.com/cpars-innovation/cpicli/issues/21)).
- A refused upload or deploy trigger (5xx) adds the tenant's reason to the error (`... response
  code = 500; the tenant says: invalid header field (line 11)`, `"Failed to deploy Value Mapping id:
  X"`), also for `update artifact`; runtime error information with several issues is one line
  (`... | Source element is not assigned | Target element is not assigned`). The mock answers with
  the texts the tenant returned ([#22](https://github.com/cpars-innovation/cpicli/issues/22)).
- `delete artifact` without `--type` (MCP `delete_artifact` without `artifact_type`) no longer takes
  a value mapping for a message mapping: a tenant's `MessageMappingDesigntimeArtifacts(Id=…)`
  answers for an artifact of any type, so that type is tried last; asking for a message mapping
  that is another type is refused. The mock answers the same way.

## 0.18.0

- Message logs: a detail the key may not read (403/401 on message store entries, attachments,
  custom headers, adapter attributes or the error text) is a warning, no longer a failure of the
  whole read. `send --wait` and test runs work with a key without `esbmessagestorage.read`
  ([#19](https://github.com/cpars-innovation/cpicli/issues/19)).
- `send --process-direct`: when the harness answers without message IDs, its own log is found by
  time; when the flow under test then never logs, the error says that the harness completed but
  did not reach the ProcessDirect address, and what to check
  ([#20](https://github.com/cpars-innovation/cpicli/issues/20)).
- `test tools install` decides the package before uploading anything: tools already installed
  elsewhere make the others join them (a note says so), a different `--package` or tools spread over
  several packages are refused up front, so a failed install leaves the tenant as it was
  ([#21](https://github.com/cpars-innovation/cpicli/issues/21)).
- Mock tenant refuses what real tenants refused: a manifest header without a space after the colon
  (upload 500 `invalid header field (line N)`), a value mapping with an empty value (deploy 500), a
  message mapping without an assigned target (deploy ERROR `Target element is not assigned`)
  ([#22](https://github.com/cpars-innovation/cpicli/issues/22)).
- Test cases: `expect.response_headers` (name case-insensitive, value exact, `null`: absent);
  `SentMessage.ResponseHeaders` holds the answer's headers without cookies
  ([#23](https://github.com/cpars-innovation/cpicli/issues/23)).
- Deploy: a build and deploy task that ends `FAIL` while the runtime runs the new version `STARTED`
  is reported as deployed with a warning naming both (`DeployResult.Warnings`); a deployment that
  never reaches the runtime mentions integration cell refusals. Lint `unsupported-script-class`:
  scripts using `Thread`, which an integration cell refuses at deploy
  ([#24](https://github.com/cpars-innovation/cpicli/issues/24)).

- Script cases ([#13](https://github.com/cpars-innovation/cpicli/issues/13)): YAML cases that call one
  script function with a message and check body, headers, properties (`null`: absent), return
  value, exception, message log and duration, in the format shared with CPI Studio
  (`docs/script-case.schema.json`): `.cpi/tests/<FlowId>/scripts/<script file>/<case>.yaml` and
  `.cpi/tests/collections/<CollectionId>/<case>.yaml`. `cpictl test scripts cases` (MCP
  `run_script_cases`; Go `testrun.LoadScriptCases`, `scripttest.RunCases`) runs them locally on the
  stand-ins through the new Gradle task `cpictlScriptCases` of `test scripts init`'s `build.gradle`
  (an older one needs `init --force`). `pd`, `value_mappings`, `secure` and `keystore` set what SAP's
  services answer; they are local only, a later run on the tenant skips such cases.
  `collections` is reserved below `.cpi/tests` (not a flow's test folder). `call.args` also works
  for a flow's scripts (custom functions of message mappings): `MappingContext` and `Output`
  parameters are supplied by the runner, and with `Output` `returns` is the list of added values.
- A runtime `DeployedOn` without a time zone (`2026-10-15T12:54:54.223`, seen on a trial tenant) is
  read as UTC instead of being ignored with a warning on every status check
  ([#18](https://github.com/cpars-innovation/cpicli/issues/18)).

- Script tests: the stand-ins `cpictl test scripts init` writes (`tests/stubs`) now cover SAP's script
  API as CPI Studio's library does: `Message` v1 and v2 (body conversion, attachments, SOAP
  headers), `Exchange`, the message log, `ITApiFactory` with `SecureStoreService`,
  `KeystoreService`, `PartnerDirectoryService`, `ValueMappingApi`, the data store and number ranges,
  SOAP fault classes, `MappingContext` / `Output`. Tests set the values the services answer with
  (`cpictl.standin.CaseContext.builder()`, see `tests/stubs/README.md`). **Change:** `Message` is an
  interface now, as at SAP: tests use `new MessageV1()` / `new MessageV2()`, and the message log is
  `messageLogFactory` plus `CaseContext.log`. `build.gradle` gains Camel attachments,
  `javax.activation`, slf4j-api and Bouncy Castle; the stand-ins need JDK 17+. The data store
  classes, `ValueMappingApi.getMappedValues` and `MappingContext` / `Output` are not verified against
  SAP's javadoc (marked in the README). The version 2 `Message` keeps the 0.17.0 rule: no
  `getHeader(...)`, version 2 scripts read `getHeaders().get(name)`. Existing projects keep their
  files unless `init --force` ([#14](https://github.com/cpars-innovation/cpicli/issues/14)).

## 0.17.0

- Lint compares the flows with the pipeline files, offline: `configure-unknown-parameter` (a
  configure file sets a key the flow lacks: configure fails), `configure-missing-parameter` (a
  configure file covering the package has no value for a parameter) and `flow-without-deployment`
  (a flow in a package folder the deploy config covers, without its own entry). Files from
  `repository.configure` / `repository.deployConfig` in `.cpi/lint.yaml`, or `--configure` /
  `--deploy-config` (also for the MCP `lint` tool through `.cpi/lint.yaml`).
- Lint `header-empty-vs-absent`: a flow sets a header to an empty value and calls, through
  ProcessDirect, a flow that tests it against null (an empty header is not null).
- `cpictl test scripts init` also writes the Groovy script 2.x stand-in
  (`com.sap.it.script.v2.api.Message`) without the version 1 `getHeader` methods, so tests of
  `script/v2/` scripts fail on calls the 2.x API lacks.
- `cpi-plan`: takes a decision log or architecture proposal as input, asks open decisions in
  rounds (each question with a recommended answer, dependent ones later), and labels every API,
  service and event with its source (tenant, SAP documentation, user, new; otherwise
  "unverified"). `cpi-build` must not invent APIs either. docs/plugin.md lists optional
  third-party research MCP servers (SAP documentation, API Hub, Road Map, Notes).
- `send_test_message`: the description now says that URLs below a listed endpoint and `path` work
  (supported since 0.12.0).

- `cpictl mcp --branch-policy <file>` (optional, off by default): tools that change the tenant
  are refused (errorCategory `branch_readonly`) while a repository under `--root` is on a
  read-only branch (`readonlyBranches` patterns, a detached root checkout, a tier outside
  `writableEnvironments`). Checked on every call, so a checkout during the session counts;
  `help {"topic": "server"}` shows the verdict. See docs/mcp.md, Branch policy.
- `iflow layout` / `layout_iflow`: each receiver sits above the step that calls it (below when
  steps are in the way) with a straight line, the pools moved apart to make room; the column
  right of the pool is only the fallback.
- Discovery leaves out cpictl's test tooling flows (`skippedTestTooling`), describes each
  exception subprocess (`exceptionSubprocesses`: steps, end events, local process calls;
  summary `emptyExceptionSubprocess`, `exceptionSubprocessEnds`) and counts script steps by
  collection or local script (`scriptSteps`, `collectionScriptCalls`). `cpi-discover` uses them
  and treats a uniform log level as a likely default.

- Lint rules from field feedback: `exception-subprocess-message-end` (an exception subprocess
  that swallows the error: Message End with nothing that raises it again),
  `dead-local-process` (a local process nobody calls, with the receivers only it uses),
  `inline-local-process` (a local process called once that is a straight line of steps) and
  `duplicate-header-case` (`OrderNo` and `orderNo`: one header in Camel). Checked against SAP's
  Pipeline flows without findings.
- A refused upload (4xx) says what the tenant answered and, when the artifact is open in the
  Web UI, who holds the edit lock since when and how to remove it (`upload_artifact`,
  `update artifact`, the orchestrator).
- `cpictl mcp --opt-in <tool>` (`CPICTL_OPT_IN`) offers an opt-in tool (`unlock_artifact`, the
  delete tools) without limiting the other tools as `--tools` does. `help` lists every toolset
  with the members the server does not offer and why (`notOffered`).
- Skills: `cpi-build` has `editing-pitfalls.md` (review the whole flow, absent vs empty, model
  details that break the editor) and `http-receiver.md` (address and query handling, dynamic
  method, CSRF, 4xx diagnosis); `cpi-review` and `cpi-operate` adopt the matching checks
  (TRACE only after deploy, report times in UTC and local time, local / tenant / not run kept
  apart).

## 0.16.0

- `cpictl test xslt --online` (Go `XSLTOptions.Online`): XSLT cases run on the tenant's XSLT
  engine through the new test tooling flow `CPICTL_Test_XSLT` (HTTPS `/cpictl/xslt`, XSLT mapping
  with the stylesheet from the Partner Directory, PID `CPICTL_Test`); the stylesheet is uploaded
  for the run and removed afterwards (`--keep-uploads`). `cpictl test tools install` installs the
  three tools (`--only xslt`) ([#13](https://github.com/cpars-innovation/cpicli/issues/13)).
- HTTP requests send header names exactly as given (Go canonicalised them, e.g.
  `SAP_ApplicationID` went out as `Sap_applicationid`): scripts and stylesheets see test message
  headers with their real names.

- XSLT mappings in the Partner Directory: `cpictl test xslt` (Go `testrun.LoadXSLT`,
  `testrun.RunXSLT`) runs cases from `.cpi/tests/pd/<PID>/<ID>/<case>.yaml` with Saxon
  (`--saxon-jar`, `CPICTL_SAXON_JAR` or `--command`): headers and properties as `xsl:param`,
  expectations `output` (compared as XML), `output_contains`, `output_not_contains`,
  `output_xpath`, `error_contains`. Results and JUnit like test runs. Schema
  `docs/xslt-case.schema.json`. Checked with Saxon-HE 9.9
  ([#12](https://github.com/cpars-innovation/cpicli/issues/12)).

- Test runs: a failed case (or an error after the message was sent) carries the first 8 KB of
  the endpoint's answer (`CaseResult.Response`, `ResponseContentType`); the MCP server redacts
  it with `--redact-payloads` ([#15](https://github.com/cpars-innovation/cpicli/issues/15)).
- `cpictl test scripts init` (Go `scripttest.Init`) writes `build.gradle` and the SAP script API
  stand-ins into the project, so `cpictl test scripts` runs without copying them from the skill
  ([#14](https://github.com/cpars-innovation/cpicli/issues/14)).

## 0.15.0

- `cpictl test scripts` (Go `pkg/scripttest`): runs the Gradle build of the script collection
  tests and reports every test and every collection function with its line coverage (JaCoCo, per
  method), the test files and methods that call it and its status (passed, failed, skipped,
  untested). `--no-run` reads the reports of an earlier run
  ([#11](https://github.com/cpars-innovation/cpicli/issues/11)).
- Test run diagnostics have the message's path through the flow (`Diagnostics.Path`): the model
  steps that ran in order, the failing one marked; steps not listed were not reached
  ([#11](https://github.com/cpars-innovation/cpicli/issues/11)).

- Receiver stub 1.2.0: conditional answers per rule (`when`: `pathContains`, `header`,
  `bodyContains`, each entry overriding the answer fields it sets) and delays (`delayMs`, up to
  60 s). The recorded call names the chosen answer (`StubCall.Matched`) and carries the stub's
  processing time (`StubCall.DurationMs`). CLI `test stub set --delay --when-file`, MCP
  `set_stub_response` `delay_ms`, `when`. Run `cpictl test tools install --deploy` to update the
  stub ([#11](https://github.com/cpars-innovation/cpicli/issues/11)).
- Test runs report their phases (`testrun.Options.Phase`, `PhaseEvent`): apply, deploy and
  restore of the configuration, and per case send, wait_log, stub_calls and diagnose. A restore
  that failed can be retried in the same process (`Result.RetryRestore`). `SendTestMessage`
  reports once when the endpoint answered and the wait for the log begins
  ([#11](https://github.com/cpars-innovation/cpicli/issues/11)).
- The test case schema has a `title` and `examples` for every field (form editors such as CPI
  Studio's) ([#11](https://github.com/cpars-innovation/cpicli/issues/11)).

- Test messages through the harness: when no deployed flow consumes the ProcessDirect address,
  `send` / `send_test_message` / test runs fail at once with that reason instead of waiting the
  whole timeout for a flow log that cannot appear. The mock tenant's "no consumer" error lists the
  deployed ProcessDirect senders; a regression test runs cases through the harness that
  `InstallTestTools` installs ([#10](https://github.com/cpars-innovation/cpicli/issues/10)).

- Upload compares `metainfo.prop` (and other `.prop` files) by their entries as Java reads them:
  the tenant rewrites the description with `:` escaped and a comment line, so an unchanged
  artifact was reported as changed and, with versioning manifest, refused ("content differs but
  Bundle-Version equals the tenant's"). This made `cpictl test tools install` fail on a second run
  ([#7](https://github.com/cpars-innovation/cpicli/issues/7)). The mock tenant now stores
  `metainfo.prop` like a tenant.
- An artifact that exists in another package: the error names that package ("X already exists in
  package P, not in Q") instead of "not found in package Q". A deployment that never reaches the
  runtime says so in its timeout error (check that the tenant's runtime is up).
- Lint `hardcoded-secret`: an address parameter (`*URL`, `*Uri`, `*Endpoint`, `*Path`, `*Host`) or a
  URL value is not a secret unless the URL carries credentials (user info, `token=`, `sig=`,
  `key=`, ...); such a URL is now reported under any parameter name
  ([#8](https://github.com/cpars-innovation/cpicli/issues/8)).
- Lint leaves SAP-delivered content out by default: findings carry the package `vendor` (from the
  snapshot's `<package>.json`, `SAP` for `com.sap.` IDs without one); `ignoreVendors` in
  `.cpi/lint.yaml` (default `[SAP]`, `[]` checks all) and `Result.SkippedVendor`
  ([#9](https://github.com/cpars-innovation/cpicli/issues/9)).

## 0.14.0

- Test tooling: the user role of the harness's and the stub's HTTPS senders is externalised
  (`CPICTL_Harness_Role`, `CPICTL_Stub_Role`, default `ESBMessaging.send`). `cpictl test tools
  install --role <role>` (MCP `install_test_tools` `role`, Go `TestToolsOptions.Role`) sets it
  before the deploy, so on a shared tenant only clients with that role can reach ProcessDirect
  flows through the harness; without `--role` the configured role stays. `test tools status`
  (`TestToolStatus.Role`) shows it. The tooling's version is now 1.1.0: run `cpictl test tools
  install --deploy` to update ([#6](https://github.com/cpars-innovation/cpicli/issues/6)).

## 0.13.0

- Test runs: `config.yaml` can set the parameters of other flows (`flows:`), for receivers in the
  flows a case's flow calls (ProcessDirect, JMS); a case that expects a failed message
  (`message_status: FAILED`, ...) no longer needs `http_status`. End-to-end test of runner, stub
  and configuration on the mock tenant. With `--redact-payloads` the MCP server also masks the
  response and stub-call excerpts that `run_tests` failures quote.

- Test tooling ships with cpictl: `cpictl test tools install [--package CPICTLTesting] [--deploy]
  [--only harness|stub]` (MCP `install_test_tools`) uploads the test harness `CPICTL_Test_Harness`
  (HTTPS `/cpictl/test` -> ProcessDirect `${header.CpictlTargetAddress}`) and the new receiver stub
  `CPICTL_Test_Stub` (HTTPS `/cpictl/stub/*`) into their package; `test tools status` shows
  versions, deployment and the stub URL. The stub records every request as the message log
  attachment `cpictl-stub-request` with the custom headers `CpictlStubRule` and `CpictlStubPath`, and
  answers per rule from the Partner Directory (`CPICTL_Stub/<rule>`): `cpictl test stub set <rule>
  --status --body-file --content-type --header` / `test stub clear <rule>` (MCP
  `set_stub_response`). Go API: `ops.StubCalls`, `ops.SetStubResponse`, `ops.ClearStubResponse`.
  Install and `stub set` are refused on a production tier unless `--allow-production`. The mock
  tenant emulates both flows (dynamic `${header.*}` addresses, receivers pointing at the mock's
  own `/http/...` URLs). See [docs/testing.md](docs/testing.md#test-tooling-harness-and-receiver-stub).
- Test runs. Test cases in `.cpi/tests/<FlowId>/<case>.yaml` (the cpi-test skill's format, now
  checked strictly with file:line errors; `endpoint` is accepted as the old name of `url`) run
  with `cpictl test run` (MCP `run_tests`) and are listed with `cpictl test list` (MCP
  `list_tests`, local). Each case sends its message like `send` (HTTP, `path`, ProcessDirect via
  the harness, `idoc` / `idoc_file`, `odata`), waits for the log (`wait`, default 60s) and checks
  `http_status`, `message_status`, `response_contains`, `response_not_contains`,
  `response_xpath`, `response_json`, `error_contains`, `custom_headers`, `steps_include`,
  `max_duration` and `stub_calls` (calls recorded by the receiver stub `CPICTL_Test_Stub`). New
  keys `description`, `tags`, `skip`, `order`, `flow`. A failed case lists each expectation
  (expected, actual) and diagnostics: message GUID, last error, `errorInformation`, failing step
  and the root of the message path. `--parallel` (cases with `stub_calls` run alone), `--junit`,
  `--events` (one item per case). `--apply-config` (MCP `apply_config`) sets the flow's
  `config.yaml` parameter values, redeploys, runs its cases and restores the previous values and
  redeploys again, also on failure or cancellation (keys reported, never values); without it a
  differing `config.yaml` is a warning. Exit codes: 0 all passed, 5 a case failed, 7 passed and
  unrunnable cases, else the cause. Refused on a production tier unless `--allow-production`.
  `run_tests` counts in MCP build loops like `send_test_message` (each distinct failure once).
  Schema: [docs/test-case.schema.json](docs/test-case.schema.json); docs:
  [testing.md](docs/testing.md#test-runs). Go: `pkg/testrun` (`Load`, `Run`, `WriteJUnit`,
  `List`, `XPath`, `JSONPath`), `ops.StubCall` / `ops.StubCalls`.
- Mock tenant: an inbound endpoint with `Harness` forwards to the ProcessDirect address in
  `CpictlTargetAddress` like the test harness flow.
- Plugin (0.3.2): the cpi-test skill writes cases (new keys, `config.yaml`, `stub_calls`) and runs
  them with `run_tests` instead of sending each message by hand.

- Production guard: a `tier` setting (profile key, `--tier`, `CPICTL_TIER`). On `prod`
  (`production`, `prd`), `send` and MCP `send_test_message` are refused unless `--allow-production`
  (for MCP: when the server is started with it; no tool argument lifts it). A project
  `cpictl.yaml` from the repository may set neither `tier` nor `allow-production`.

## 0.12.0

- `send` / `send_test_message`: `--path` (MCP `path`) appends a sub-path and query to the flow's
  endpoint, and `--url` may be a URL below a listed endpoint, for flows that route on the path
  (a SOAP sender at `/out/` reached as `/cxf/out/test`, HTTPS addresses ending in `/*`). Other
  hosts, other flows' paths and `..` are still refused. The mock tenant routes such sub-paths too.

- OData and IDoc in discovery, the graph and lint. Discovery records per OData channel (OData V2/V4,
  SuccessFactors OData, OData senders) the version, operation, resource path and entity set, query
  options, paging, `$batch`, the EDMX under `edmx/` and the host (`triggers[].odata`,
  `receivers[].odata`), per IDoc channel the direction, address path, literal `sap-client` and
  content type (`.idoc`), and per flow the IDoc types it handles (`idocTypes`, from XPaths, `IDOCTYP`
  conditions and payloads, and mappings and schemas with the control record), each with where it
  was found. The graph has the node types `odata_entity` (`<host>/<entity set>`) and `idoc_type`
  and the edges `reads_odata`, `writes_odata`, `receives_idoc` and `sends_idoc`
  ([graph.md](docs/graph.md#odata-and-idoc)). New lint rules: `odata-query-without-paging`,
  `odata-single-calls-in-loop`, `odata-hardcoded-query`, `idoc-sap-client-literal`,
  `idoc-no-idempotency` and `idoc-control-record-literal`. `hardcoded-endpoint` now also checks
  OData receivers (`HCIOData`). Go: `ops.ODataChannelOf`, `ops.IDocChannelOf`,
  `ops.ODataOperationKind`.
- Plugin (0.3.1): the discover skill and the conventions template cover OData and IDoc; the
  improvement catalogue has the new rules.
- IDoc and OData test messages. `cpictl send --idoc FILE` (MCP `send_test_message` `idoc`) sends
  IDoc XML to the flow's IDoc sender (checks the EDI_DC40 control record, reports DOCNUM and
  IDOCTYP); `--odata-method`, `--entity-set`, `--key`, `--query` (MCP `odata_method`,
  `entity_set`, `key`, `query`) build an OData V2 request against the flow's OData sender.
  `cpictl idoc template` (MCP `idoc_template`, local) writes an IDoc XML skeleton to fill. Go:
  `pkg/idoc`, `ops.TestMessage.IDoc` / `.OData`.
- Mock tenant: IDoc senders at `/cxf/<address>` (plain or SOAP-wrapped IDoc XML, IDoc headers,
  ApplicationMessageId from DOCNUM), OData V2 senders at `/gw/odata/<namespace>/<service>;v=<n>`
  (`$metadata` from the flow's EDMX, entity sets run the flow), IDoc receivers answered by the
  landscape's systems; `--credentials strict` covers them. Paths, headers and answers SAP
  documents but that were not verified on a tenant are marked as assumptions in
  [docs/mock-tenant.md](docs/mock-tenant.md#idoc-and-odata-senders). The demo landscape has two
  new flows, `Orders_IDoc_In` (ORDERS05 IDocs, with traffic) and `Orders_OData_Service`.

## 0.11.0

- Script collections as documented, tested libraries. New lint rules: `collection-script-doc` (a
  public function of a script collection without a doc comment), `collection-script-tests` (a
  function no test calls; tests in `tests/scripts/<package>/<collection>/`, outside the artifact
  folders), `collection-script-coverage` (line coverage per script from a JaCoCo report, default
  minimum 80 %, also when the report is missing or older than the script) and `script-doc` (info:
  a flow script that says nothing about what it does). Settings: `scriptTests` in `.cpi/lint.yaml`.
- Discovery reads script collections (`scriptCollections`: each script's summary and functions
  with their doc comments) and which collection functions flows call (`collectionScripts`);
  `graph_search` finds collection scripts by what their comments say. Go: `ops.ParseScriptDoc`.
- Plugin (0.3.0): the build skill looks for an existing collection function before writing one,
  documents and tests what it adds to a collection ([script-tests.md](plugin/skills/cpi-build/script-tests.md):
  Gradle build, `Message` stubs, example test, verified with Groovy 4 and JaCoCo); improve, discover
  and the review checklist follow.

## 0.10.0

- Plugin (0.2.1): the reviewer reads .iflw changes with `compare` `model: true` instead of the
  line diff.

- `compare --model` (MCP `compare` with `model: true`, Go `ops.CompareOptions.Model`): a semantic
  diff of each changed .iflw per element and per property instead of an unreadable line diff of
  one-line XML (`files[].model`): steps, events, gateways, participants and adapters (address,
  authentication, versions, ...) added / removed / changed with their properties, renames,
  sequence flows (source, target, condition), the flow's own properties; shapes that only moved
  are marked `moved`; sensitive values are masked. The text output prints one line per change,
  e.g. `~ adapter MessageFlow_Sender (ProcessDirect): address: /a -> /b`. Go:
  `iflow.DiffModels`, `iflow.DiffModelsWith` (`IgnoreGeometry`), `ModelDiff.Summary`;
  `iflow.Parse` also returns the participants and the collaboration's properties.

- Progress events (`--events <file>` or `--events -`): JSON lines `start`, one `item` per artifact
  (`snapshot`, `orchestrator`, `deploy`, `undeploy`, `configure`) and `end` with exit code and
  category, for progress displays without parsing log lines. Go: `ops.OrchestratorOptions.Progress`
  (and `snapshot.Options.Progress`).

- `orchestrator --plan` (and `ops.PlanOrchestration`) no longer fills the update counters and
  artifact statuses (`packagesUpdated`, `artifactsChanged`, `successfulArtifactUpdates`, …): a plan
  writes nothing, its outcome is in `plan[]`. Failures still count.
- `matrix`: every row has `cells`, the tiers in promotion order (also tiers without the artifact,
  `present: false`); `tiers` stays as the map. `compare`: every item has `differences`, all
  differing aspects (`content`, `version`, `parameters`), not only the one `status` names.

- Diagram geometry for flow viewers: `iflow.ReadDiagram(data)` returns the shape bounds and line
  waypoints of an .iflw by BPMN element ID, the pools (participants) and lanes, the flow nodes
  with type, name and component, the sequence flows and the message flows with their adapter;
  elements without a shape or line are listed as undrawn. New command
  `cpictl iflow diagram <path> --output json`.

- MCP instructions per toolset: `help {"topic": "instructions", "toolsets": ["build", …]}`
  returns the server instructions for those toolsets only (the general part, mode and loop policy,
  plus the sections of their tools); without `toolsets` the active ones (after `--mode`, `--tools`,
  `--toolset`; with `--dynamic-toolsets` the enabled ones). The instructions are now built from
  fragments keyed by toolset, separated by blank lines.
- MCP progress: a `tools/call` with `_meta.progressToken` gets `notifications/progress` while
  `deploy`/`undeploy` poll the runtime status ("artifact X: STARTING, check 3/30"),
  `discover_tenant` runs its phases, `check_guidelines` polls and `send_test_message` waits.
  For Go callers: `ops.Progress`/`ops.ProgressFunc`, and the optional `Progress` field of
  `ops.Options`, `ops.DiscoverOptions` and `ops.TestMessage`; `ops.CheckGuidelinesWith` with
  `ops.GuidelineOptions` (`CheckGuidelines` is unchanged).
- MCP resources: `resources/list` and `resources/read` (capability `resources`) offer the cpi
  skills and their files (`cpictl://skills/<name>[/<file>]`), the workflows
  (`cpictl://workflows`) and the loop journals under `--root` (`cpictl://loops/<id>`,
  `text/markdown`). Unknown URIs are JSON-RPC errors; loop URIs cannot leave the root.
- Fix (MCP): a `notifications/cancelled` sent right after its `tools/call` could arrive before the
  call was registered and be ignored; calls are now registered before they start.

- Component catalogue: `sourceDates` records when the editor metadata was imported and when
  versions were last learned (`generatedAt` changes with every write); `components list` prints
  the editor metadata's age.

- Cancellation and injectable HTTP client for Go callers: `exe.WithContext(ctx)` returns a copy
  of an executer whose requests (and retry waits) end with ctx and that shares the session (CSRF
  token, cookies, OAuth token). `httpclnt.NewWithOptions(httpclnt.Options{…, HTTPClient})` and
  `cpi.ServiceDetails.HTTPClient` set the client (transport, proxy, timeout). The OAuth token
  request had no timeout; it now uses the client's (default 30 s). `pkg/ops` functions that take
  a context cancel their tenant calls with it, and the CLI's requests end on Ctrl+C.

- Typed errors for Go callers: `ops.ErrorCategory(err)` and `ops.ExitCode(err)` with exported
  constants (`ops.CategoryUsage`, …, `ops.ExitUsage`, …); the usage, partial and failed errors
  implement `ExitCode() int`, so `errors.As` with an `ExitCode` interface works everywhere.
- The CLI's JSON envelope has `errorCategory` (as MCP tool results) and `schemaVersion` (1).
- `doctor`: every check has `category` (`auth` or `tenant_http` when it is not ok) and
  `httpStatus`.

- Fix: `MessagePathFor` (`logs tree --message`, `get_trace_tree message_guid`) panicked for an
  unknown message GUID; it is a usage error now.
- Fix: data race on the batch boundary counter when batches were built concurrently.
- Fix: "does not exist" checks for packages, designtime artifacts, API proxies and products
  compared error texts; they use the HTTP status now.
- Fix: `config-generate` creates the output file's directory.

- MCP loop policy: `cpictl mcp --loop-required` refuses tools that change the tenant or trigger
  processing while no loop is open (`errorCategory: "loop_required"`, exit code 2);
  `--loop-max-iterations`, `--loop-max-deploys`, `--loop-max-same-errors` and `--loop-max-minutes`
  cap the limits `loop_start` accepts (a higher value is a usage error naming the cap; omitted
  limits get the default lowered to the cap). Also as `cpictl.yaml` keys and `CPICTL_LOOP_*`.
- MCP loops: `deploy` with `dry_run: true` no longer counts against `max_deploys`; only failures
  that belong to the loop (message after `loop_start`, or of an artifact uploaded, deployed or
  sent to in the loop) count toward `same_error_limit`, so reading older failures while
  diagnosing no longer stops the loop.
- MCP masking in one place for every tool result: values of sensitive names (authorization,
  cookie, password, secret, token, credential, API key) are masked in all name/value lists
  (custom header properties, adapter attributes, step properties, trace headers and properties,
  `get_trace_tree` keys, the `find_messages_by_header` filter); `--mask-pattern` adds name
  patterns; `--redact-payloads` replaces message bodies (test responses, attachments, message
  store entries, trace payloads, data store entries, variables) with a placeholder, size and
  sha256.
- MCP results use root-relative, slash-separated paths (also in error texts).
- MCP `help`: every tool has a `class` (`read`, `local`, `write`, `deploy`, `process`, `log`,
  `destructive`) next to its `effect`; toolsets carry each tool's effect and class
  (`toolEffects`) and an `other` toolset lists tools in no toolset. `help {"topic": "server"}`
  reports the effective `readOnly`, the toolsets, the loop policy and the masking options.
- MCP: a call to a tool the mode or filters removed returns an error result (`isError: true`,
  `errorCategory: "usage"`, the reason) instead of a JSON-RPC error.

- Mock tenant: `validate` checks component versions against the runtime profile
  (`integrationcell`) like a tenant: a channel with version 1.0 of an adapter that has a newer one
  (or without `componentVersion`, which the tenant reads as 1.0) fails with `This component HTTP
  with version 1.0 is not supported in integrationcell profile.`, a variant URI the tenant does
  not know with `Enter adapter details for channel`, and adapters or steps newer than the tenant
  offers fail too. The versions come from a built-in component catalogue (SAP's editor metadata of
  an integrationcell tenant); `landscape.yaml` can bring its own (`components:`, also per tier).
- Mock tenant: `--credentials strict` checks credentials like a tenant with two service keys: the
  API user or client (`--api-user`, `--api-password`, `--api-client-id`, `--api-client-secret`)
  on `/api/v1`, the runtime client (`--runtime-client-id`, `--runtime-client-secret`, default
  `mock-rt`) on `/http/...` and `/cxf/...`; the other key gets 403, wrong credentials 401, and the
  token endpoint issues a token per client. The default `--credentials any` accepts any
  credentials as before. The start output prints `CPICTL_RUNTIME_OAUTH_*` for `cpictl send`.
- Mock tenant: uploads set `ModifiedAt` and `ModifiedBy` (the API user or OAuth client of the
  request) on every save, also outside the live mode and for value mappings; the artifact itself
  reports `ModifiedBy` too.
- Mock tenant: the demo's `Billing_Post` script writes an `Invoice` attachment, so failed
  postings have a trace and an attachment (scripts calling `addAttachmentAsString` get one);
  data stores and log files are empty lists with `--seed empty` too (were 404).

- Go API (docs/go-api.md): logic that was only reachable through the CLI is public.
  - Deploy configs and orchestrator plans: `ops.LoadDeployConfig`, `ops.PlanOrchestration`
    (the code of `orchestrator --plan`), `ops.NewOrchestrator` for full runs,
    `ops.PlanDeployments`, `lint.DeploymentCopiesOf`; the deploy and configure file models
    moved to `pkg/models`.
  - `pkg/versioning`, `pkg/manifest` and `pkg/pdrepo` (local Partner Directory files, now
    `pdrepo.New`) moved out of `internal`; `ops.PDSnapshot` is the code of `pd-snapshot`.
  - `ops.NewLogLevelReverter` (`Schedule`, `RunDue`, `RevertAll`), used by the MCP server.
  - `ops.ListArtifacts` reports `ModifiedBy` and `ModifiedAt`.

- Go API: the snapshot is public as `pkg/snapshot`: `snapshot.Take(ctx, exe, dir, Options)`
  with a progress callback per artifact, `Result` (items, counts, changed `Paths`, state),
  `State`/`ArtifactState`/`LoadState`, `Commit`. `cpictl snapshot` runs it (one implementation).
  See [docs/go-api.md](docs/go-api.md).
- Go API: content hashing and normalised diffs are public as `pkg/content` (`UploadHash`,
  `ContentHash`, `TreeHash`, `NormalizeTree`, `NormalizeProperties`, `TreeDiff`, `UnifiedDiff`,
  ...), moved from `internal/file`; the hashes are unchanged (a test pins them), so existing
  snapshot states stay valid.
- Go API: `ops.UploadRequest` has `Baseline` (a snapshot state: compare without a download, as
  the orchestrator does) and `KeepRuntime` (never undeploy: a content change with the running
  version sets `Redeploy`).
- `snapshot`: an artifact in draft that is skipped (`--draft-handling SKIP`) is reported with
  status `draft` (was `unchanged` with a note), counted as such and listed in the job summary; the
  state file keeps it with `draft: true`.
- `snapshot`: `--dir-git-repo` may be a folder inside a Git working tree (the repository is found
  above it); only changes below it are committed, and a commit is refused when the index holds
  staged changes outside it. `--git-commit-changed-only` commits only what the snapshot wrote or
  removed instead of every change below `--dir-git-repo`.
- `snapshot`, `snapshot restore`: downloads go to a temporary folder of the run, created in
  `--dir-work` (default now the system's temporary directory instead of `/tmp` itself) and removed
  after the run; `--keep-work-dir` keeps it.
- `KeepRuntime` of an upload (orchestrator) also covers changed configured parameters: reported
  as to be deployed again instead of undeploying.

## 0.9.1

- Plugin (0.2.0): new skill `cpi-operate` (what is failing, follow a business key with
  `find_messages_by_header`, JMS queues: inspect, retry, move; locks, certificates, connections,
  tenant settings, log archives). `cpi-build` checks edit locks before changing a flow, takes new
  component versions from `list_components` and changes value mappings; `cpi-test` uses the
  error information, step properties, business-key search and JMS queues for asynchronous
  chains; `cpi-discover` reads integration connections and the component catalogue; the reviewer
  runs lint (`component-newer-than-tenant` is a blocker) and checks locks.

## 0.9.0

Many new tenant APIs (so an agent can do what a Web UI user can). Every read was checked on a
real Cloud Foundry tenant; most write calls follow the API metadata and are tested against the
mock tenant only, notably: value mapping `set`, JMS retry/move/delete, keystore key generation,
CSR, chain upload and delete, PGP import, access policies, alternative partners and authorized
users, delete artifact/package, external logging and archiving switches.

- `cpictl connections`: the integration connections the tenant computes from its deployed flows
  (sender host -> flows -> receiver host; `--refresh` recomputes). MCP
  `list_integration_connections`.
- `cpictl log-files archive --type http|trace --out x.zip`: the latest log files as one zip. MCP
  `download_log_archive`.
- `cpictl tenant-settings`: external logging and archiving on/off and the last archiving runs;
  `tenant-settings external-logging --on|--off --confirm`, `tenant-settings archiving --on
  --confirm`. MCP `get_tenant_settings` (read only).
- `cpictl components learn` (tenant) records custom adapters deployed on the tenant with their
  version (`deployedAdapters`). The API has no list of SAP's own adapter versions; those come
  from the editor metadata (`components import`).
- Mock tenant: integration connections (derived from the running flows; it requires the
  `InvalidateCache` header like a tenant), log file archives, external logging and archiving
  switches.

- Component catalogue (`.cpi/components.json`, [docs/components.md](docs/components.md)): the
  adapter and step variants (`cmdVariantUri`) and versions the tenant offers.
  `cpictl components import <editor metadata>` reads the Web UI's editor metadata (what the
  browser loads from `/api/1.0/metadata`; it needs the browser login); `cpictl components learn`
  and every `cpictl discover` add the versions existing flows use. `cpictl components list`;
  MCP `list_components` (`build`, `improve`), which the build skill uses for new steps and
  adapters. Lint: new rule `component-newer-than-tenant` (error: Check and deploy would fail),
  and `outdated-component` reports what the tenant offers.
- `cpictl delete artifact --artifact-id X [--type ...] --confirm` and `cpictl delete package
  --package-id X --confirm`: delete designtime content as the Web UI does. A deployed artifact is
  refused unless `--undeploy-first` (undeploys, waits, then deletes); a package that still has
  artifacts is refused unless `--with-artifacts`. Without `--type` the type is found on the tenant.
  MCP: `delete_artifact`, `delete_package` (destructive, `confirm: true`, **opt-in**: offered only
  when `--tools` names them exactly, in no toolset).
- `cpictl valmap list|get|set`: value mappings (`ValueMappingDesigntimeArtifacts`, `ValMapSchema`,
  `ValMaps`, `DefaultValMaps`); `set` adds a value pair to an existing schema or changes the target
  value of a source value (function import `UpsertValMaps`), then deploy the value mapping. MCP:
  `list_value_mappings`, `get_value_mapping` (`inspect`, `build`), `set_value_mapping` (`build`,
  `confirm: true`). Reads verified on a real tenant; the `UpsertValMaps` call follows the API
  metadata and is not yet verified on a tenant. Deleting single value pairs is not offered.
- `deploy` accepts any 2xx answer of the deploy call and takes only a plain-text answer as the task
  ID (the API metadata declares the designtime entity as the answer for message and value
  mappings). Tests cover deploying all four designtime types against the mock tenant.
- Mock tenant: deleting artifacts and packages, value mappings (demo: `Billing_CountryCodes`), and
  a deploy with the wrong artifact type answers 404.
- JMS message management, as Manage Message Queues in the Web UI: `cpictl jms messages --queue Q
  [--failed-only]`, `jms retry|move|delete --queue Q (--id ID ... | --all [--failed-only]) --confirm`
  (`move --to T`), `jms activate|deactivate --queue Q`; `jms queues` shows the flows using a queue
  (`Queues?$expand=Artifacts`). Messages are read through `MessagingQueues('Q')/MessagingMessages`
  (verified on a Cloud Foundry tenant; an unknown queue name is a usage error, the tenant would
  list nothing). Retry and move post `RetryMessagingMessages` / `MoveMessagingMessages` with a JSON
  body, delete removes `MessagingMessages(jmsMessageId,queueName)`: these calls are not verified on
  a tenant yet. MCP: `list_jms_messages` (`monitor`), `retry_jms_messages`, `move_jms_messages`
  (`confirm: true`), `set_jms_queue_state` (also in mode `operate`) and `delete_jms_messages`
  (`confirm: true`, **opt-in** like `unlock_artifact`). The mock tenant keeps the message of a
  failed JMS consumer in its queue (demo: `Orders_Billing`).
- Messages by custom header value without a scan: `cpictl logs search --header OrderNo=4711
  [--since ...]`, MCP `find_messages_by_header`, through
  `MessageProcessingLogCustomHeaderProperties` with `$filter` on `Name`/`Value` and `$expand=Log`
  (one call, tenant-wide; flow, package, time and status narrow it). `logs --header`,
  `list_message_logs custom_header` and `logs tree --message --key-header` (`get_trace_tree
  key_headers`) use it too and need no scope any more; tenants without the entity set fall back
  to the scoped scan. The mock serves the entity set.
- Message details: `logs get` / `get_message_log` add `errorInformation` (error type and
  `lastErrorModelStepId`, the `.iflw` element where the message failed) and print adapter
  attributes; `logs steps --properties` / `get_message_steps properties` add each step's
  properties (`RunSteps?$expand=RunStepProperties`, the only way the tenant serves them).
- Security material beyond credentials and certificates (verified read-only on a real tenant
  where it had data; write payloads follow the API's metadata and are not yet verified on a tenant):
  - `cpictl keystore generate-key-pair`, `generate-ssh-key` (generated, or `--ssh-file` import with
    the password from env/file/stdin), `csr` (PKCS#10 PEM of a key pair), `upload-chain` (the CA's
    answer), `chain`, `ssh-public-key` (OpenSSH format), `history` (`HistoryKeystoreEntries`) and
    `delete --confirm` (SAP-owned entries refused). The API offers no restore of history or backup
    entries.
  - `cpictl pgp list` (public and secret keys with user IDs and sub keys, never key material),
    `pgp export-public`, `pgp import-public --confirm` (replaces the public keyring).
  - `credentials list` includes OAuth2 authorization code credentials (`--kind oauth2-authcode`;
    no client secret or refresh token).
  - `cpictl access-policies list|create|delete|add-reference|delete-reference`.
  - `cpictl audit-log --since 24h [--user] [--object]`; a 403 (no role, as on trial tenants) is a
    `missing_role` result, and `cpictl doctor` probes the audit log.
- Partner Directory: `cpictl pd list` / `ops.ListPDPartners` (per partner: string and binary
  counts, content types, alternative partners, authorized users; values are not downloaded,
  `--sizes` adds the binary bytes), `pd alternative-partners list|create|delete` and
  `pd authorized-users list|create|delete`. `ops.PDContentTypes` lists SAP's binary content types
  with extensions and MIME types; `pd-deploy --keys` now maps `.txt`, `.zip`, `.gz`, `.zlib` and
  `.crt` files too.
- MCP: `list_keystore_history`, `get_certificate_chain`, `get_csr`, `get_ssh_public_key`,
  `generate_key_pair`, `upload_certificate_chain`, `list_pgp_keys`, `list_access_policies`,
  `create_access_policy`, `add_artifact_reference`, `list_audit_logs`, `list_pd_partners`,
  `list_alternative_partners`, `create_alternative_partner`, `list_authorized_users`,
  `create_authorized_user`. The delete tools (`delete_keystore_entry`, `delete_access_policy`,
  `delete_artifact_reference`, `delete_alternative_partner`, `delete_authorized_user`) need
  `confirm: true` and are **opt-in** like `unlock_artifact`. The `security` and
  `partner-directory` toolsets include the new tools. The mock tenant serves all of them.

- `cpictl locks list` / `locks unlock --artifact-id X --confirm`: edit locks of artifacts (who holds
  one, since when) and removing one, as Unlock in the Web UI (the holder's unsaved changes are
  lost). Uses `/api/v1/IntegrationDesigntimeLocks`; verified on a real tenant. MCP: `list_locks`
  (`inspect`, `build`) and `unlock_artifact`, which needs `confirm: true` and is **opt-in**: no mode
  offers it, only `--tools` naming it exactly. The mock tenant has locks (landscape tier `locks`;
  demo dev: `Billing_Post`).

- Mock tenant: the demo flows now pass a real tenant's Check (adapter and step versions and
  properties as SAP's own content has them; `Billing_Post` uses a version 2 Groovy script;
  `Partner_Notify` sends to SFTP from its end event). The mock's `validate` runs a structural check
  and answers in SAP's format (`Check execution result: Failed` plus the JSON findings).
- SFTP receivers name their credential in `credential_name`: discover, the graph, `transport check`
  and the mock now read it (before only `credentialName`).

- Fix: `tenant:<profile>` sides (`compare`, `matrix`, `transport check --target/--source`) with
  OAuth sent the token request to the token host's root instead of `/oauth/token`
  ("oauth2: cannot parse json"). Found on a real tenant.

- Groovy script step version 2 (Groovy 4, `com.sap.it.script.v2.api`, scripts under `script/v2/`):
  lint resolves `v2/<name>.groovy` references, so v2 scripts are no longer reported as unused
  (and `lint --fix` no longer deletes them); v2 scripts stay out of the script collection fixes;
  new rule `prefer-script-v2` (info) for version 1 steps; the build skill writes new scripts as
  version 2.
- Fix: the flow model counted nested parts with an id (a router's `conditionExpression`, a loop's
  `standardLoopCharacteristics` / `loopCondition`) as steps; lint reported them as unconnected and
  `lint --fix` removed them, breaking routers and loops. Only direct children of a process are
  steps now.
- `hardcoded-secret` no longer reports parameters that name a credential or key (`…_REF`,
  `…_ALIAS`, `…_NAME`, `…CREDENTIAL`, `…_ID`) when the value is an identifier.

- Fix: `update artifact` (and everything reading `MANIFEST.MF`) failed with "EOF" on a manifest
  exactly as a tenant exports it (no closing blank line), e.g. a flow from `iflow copy --from`.
  Found on a real tenant.
- Errors no longer print a Go stack trace (only with `--debug`).

- Fix: `log-files` sent `$orderby`, which the tenant refuses on that collection (HTTP 501 on Cloud
  Foundry); it now lists without it and sorts newest first itself. Verified on a real tenant.
- Packages created without `ShortText` get the package name as short text (with a warning): the
  tenant refuses a package without one (HTTP 400).
- Mock tenant, closer to a real tenant (findings of CPI Studio, verified on a real tenant):
  packages carry `ShortText` and `Description` and creating one without `ShortText` fails with the
  tenant's 400; an upload whose `parameters.propdef` lacks `<constraint/>` fails with the tenant's
  500; the demo propdefs have the shape the Web UI writes; credentials, keystore, data stores and
  log files refuse `$top` (and log files `$orderby`) with the tenant's answers.
- Mock admin API: a fault can stay until deleted (`"count": -1`) and carry the tenant's error
  message (`"message"`), so an API area can answer like a tenant that does not offer it.

- Fix: `doctor` probed some API areas with requests the commands never send (`$top=1` on
  credentials, data stores and log files; the keystore without `keystoreName`), so tenants that
  reject those options showed HTTP 400 / 501 for areas that work. The probes now send the
  commands' own requests, show the tenant's error message, and report 501 as "not offered by the
  tenant". `doctor` also recognises a profile as configuration (it reported "no config file or
  profile" while using one).

## 0.8.0

### Mock tenant

- `cpictl mock-tenant --seed-dir <dir>`: load your own landscape (`landscape.yaml` plus content in
  the `snapshot` layout) with tiers, receiver systems and message traffic, instead of the demo.
  See [docs/mock-tenant.md](docs/mock-tenant.md#landscapes---seed-dir).
- The mock runs flows: one message log per flow, ProcessDirect and JMS hand-over, receivers
  answered by the landscape's systems (latency, failures), a receiver with a missing credential
  fails. `--live-traffic <speed>` keeps generating the landscape's traffic in real time (at most
  one message per millisecond and rule; a negative `trafficScale` is refused).
- Admin API `/_mock/...` (systems, faults, runs, message logs, state export and import) to change
  a running mock. It needs `Authorization: Bearer <token>`: `--admin-token`, or a random token
  printed at start as `CPICTL_MOCK_ADMIN_TOKEN`. `/_mock/run` accepts `count` up to 1000. The
  state export contains stored secrets and artifact content: do not load real customer secrets.
- Message logs are bounded: `--log-retention` (default `168h`, measured back from the newest log,
  not the clock) and at most 100 000 logs, oldest dropped first; new logs are merged into the list
  instead of re-sorting it. `/_mock/messagelogs` reports how many it kept.
- Two flows on one address: the second deployment ends in `ERROR` with `the address /orders/in is
  already used by Orders_In` in its runtime error information, as on a real tenant; undeploying the
  first flow frees the address.
- `pkg/mocktenant`: the mock as a Go library for tests of tools built on cpicli (`Start`, `Serve`,
  `SeedDemo`, `SeedDir`, `SeedLandscape`, `MessageLogs`, `Tenant.AdminToken`). Experimental: the
  API may change in minor versions until cpicli 1.0.

### Fixes

- Demo landscape: message logs no longer lie in the future (runs that would end after the start
  are left out), and the test and prod tiers no longer share one failure pattern.
- Paths from flags, config and environment variables were rejected when they contained the
  tenant user ID or OAuth client ID (e.g. user `cpi` blocked `.cpi/`, `mock` blocked
  `mock-landscapes/`), and every path when a secret was set to an empty value. Only the password
  and the client secret are checked now, and only when at least 8 characters long.

## 0.7.0

### MCP: tools per task

- `cpictl mcp --toolset inspect|build|test|monitor|promote|improve|partner-directory|security`:
  offer only the tools of the session's task (combined with `--tools`, the mode still applies).
  See [docs/mcp.md](docs/mcp.md#toolsets).
- `cpictl mcp --dynamic-toolsets`: start with `help`, `doctor`, `list_toolsets` and
  `enable_toolset`; the agent enables the toolsets it needs and the server sends
  `notifications/tools/list_changed`. Off by default, for clients without list-changed support.
  See [docs/mcp.md](docs/mcp.md#dynamic-toolsets).
- MCP `help` lists the toolsets (`help {"topic": "toolsets"}`) with the tools the server offers,
  for clients that select tools per step themselves.

### Message paths

- `cpictl logs tree --message <guid>` (MCP `get_trace_tree` with `message_guid`): the path of any
  message across flows by its correlation ID; `--key-header` (custom header properties the flows
  write) joins runs with another correlation ID, e.g. after Event Mesh or an external system.
  Trees now have `hops` (with how each was linked: span, predecessor, inferred, header) and a
  `pathKey` per route. See [docs/monitoring.md](docs/monitoring.md#following-a-message-without-a-tracer).

### Mock tenant

- `cpictl mock-tenant`: an in-memory CPI tenant for local development, demos and tests, with a
  demo landscape per tier (`--tier dev|test|prod`: flows linked by ProcessDirect and JMS, real
  models with diagrams, a day of messages with failures and the `OrderNo` custom header, tiers
  that differ like real ones), live deploys and test messages, Basic Auth or OAuth, optional TLS
  with a generated CA. See [docs/mock-tenant.md](docs/mock-tenant.md).

### Fixes

- `snapshot` without `--dir-git-repo` (and no `snapshot.dirGitRepo`) wrote the packages
  relative to the file system root; it now stops with exit code 2. Relative `--dir-git-repo` and
  `--dir-artifacts` (`.` and `packages`) are accepted.

## 0.6.0

- `cpictl compare <A> <B>` (MCP `compare`): two tiers (`tenant:<profile>`), Git refs
  (`git:<ref>[:<path>]`) or content trees per artifact: `same`, `content_differs`,
  `version_differs`, `parameters_differ`, `only_a`, `only_b`; changed files (`--diff`: unified
  diffs), `parameters.prop` keys (`--show-values`), designtime / running version, draft and who
  changed it last on the tenant; `--fail-on-diff` for CI. Tenants are normalized like `snapshot`,
  so a snapshot equals its tenant. See [docs/compare.md](docs/compare.md).
- `cpictl logs summary` (MCP `message_summary`): message volume and failures per flow, per
  connection between flows (predecessor links, or graph connections by correlation ID) and per
  error fingerprint over a time window. See [docs/monitoring.md](docs/monitoring.md).
- Error fingerprints are a public function (`ops.ErrorFingerprint`), shared by the loop limits and
  the summary.
- `cpictl matrix <tier>...`: version matrix of Git and each tier (designtime, draft, running
  version and status, last changed by) with the tiers each artifact is `behind` on.
- `transport check` answers "will it run on the target?":
  - compares each flow's configuration with the source tier (`--source tenant:<profile>`, else
    `parameters.prop`): parameters without a value on the target fail, values that would travel
    from the source and identical environment-specific values (URLs, hosts) warn;
  - resolves credentials and key aliases named by parameters (`{{SFTP Credential}}`) with the
    target's value and checks them;
  - warns about key aliases expiring within `--expiry-days` (default 30), fails on expired ones;
  - `--allow-missing credential,keystore,pd,parameters,dependency` (MCP `allow_missing`) reports
    gaps that someone else will close as warnings instead of failures.
- `cpictl transport deps|check|copy` (MCP `transport_check`): what artifacts need (script
  collections and mappings they reference, called flows, credentials, Partner Directory
  parameters; `--with-deps` adds the referenced artifacts), pre-checks against the target tenant
  (draft, changes outside the pipeline, dependencies, called flows, credentials, PD parameters,
  parameters without a value in the target's configure file; exit 5 on a failed check), and
  copying artifact folders between content trees or Git refs. See [docs/transport.md](docs/transport.md).
- The package artifact list reads `ModifiedBy` (else `CreatedBy`) where the tenant reports it.

## 0.5.0

- `cpictl iflow layout <path>...` (MCP `layout_iflow`): lays out the diagram of `.iflw` files:
  flow order left to right, router branches one below the other, right-angled lines that cross no
  step, exception subprocesses below the main flow, senders and receivers next to their steps.
  Only the diagram changes; a second run changes nothing. `--mode tidy` (keeps the order of
  branches, default) or `full`, `--check` (exit 5 on problems), `--dry-run`, spacing in
  `.cpi/lint.yaml` (`layout`). See [docs/lint.md](docs/lint.md#diagram-layout).
- Lint rule `layout` (missing shapes or lines, overlaps, shapes outside their pool, lines through
  steps, cramped shapes); `--fix-rules layout` lays out the flagged diagrams.
- The `cpi-build`, `cpi-improve` and `cpi-review` skills use `layout_iflow` instead of placing
  shapes by hand.
- `lint --fix` no longer rewrites `'` as `&apos;` in the models it edits.
- Opt-in end-to-end test on a development tenant (`go test -tags e2e`, `CPICTL_E2E=1`): plan,
  upload and deploy, two snapshots without diff, comparison with the snapshot state, a change
  deployed once, layout accepted. See [docs/e2e.md](docs/e2e.md).
- Lint and the flow model moved to `pkg/lint` and `pkg/iflow` (Go API; the CLI is unchanged).

- **Drafts no longer fail the orchestrator.** An artifact in draft on the tenant (someone edits
  it in the Web UI) is skipped: not uploaded, not deployed, one warning per ID, and the run goes
  on (exit 0 when drafts are the only problem). See [docs/orchestrator.md](docs/orchestrator.md#drafts).
  - New status `skipped-draft` in `--output json` (`artifacts[].status`, `counts.skippedDraft`,
    `stats.skippedDrafts`), in the summary and in the job summary (own section: ID, package,
    tenant designtime version, running version, repository `Bundle-Version`).
  - `--plan` predicts it (`upload: skipped-draft  no deploy`) instead of reporting a failure.
  - Per deployed ID: a draft on a deployment copy or prefix variant skips only that ID.
  - Artifacts deployed without an upload (`--deploy-only`, `sync: false`) are checked too.
  - `orchestrator.draftHandling: SKIP|ERROR` (`--draft-handling`, `CPICTL_DRAFT_HANDLING`,
    `--fail-on-draft`), default `SKIP`; `ERROR` keeps the old behaviour (exit 5).
  - MCP `upload_artifact` / `upload_artifacts` return `SKIPPED` with `skipped: "draft"` and the
    reason; `deploy` (and `deploy --pending`) skip drafts with `skipped: "draft"`, and
    `deploy --pending` keeps them in the pending file for the next run.
- `snapshot --draft-handling ADD` writes a numeric `Bundle-Version` for drafts (the repository's
  when higher, else the last saved or running version, else `1.0.0`) instead of
  `Bundle-Version: Active`, and marks them: `new (draft)` / `changed (draft)`, JSON `draft`, state
  file `draft`. A `Bundle-Version: Active` left by v0.4.0 is replaced.
- `snapshot` normalizes `metainfo.prop` like `parameters.prop` (no timestamp comment, sorted keys),
  so an unchanged artifact gives no diff. The first snapshot rewrites it once. `download` too.
- `snapshot` no longer writes parameters the tenant keeps but `parameters.propdef` no longer
  declares (e.g. after a rename); they are reported per artifact as `orphanParameters`.
  `--keep-orphan-parameters` writes them.
- `snapshot`: packages outside `--ids-include` / in `--ids-exclude` give one info line with the
  count instead of one warning per package (listed with `--debug`).
- `orchestrator` result: `artifacts` (status per deployed ID: `created`, `updated`, `unchanged`,
  `failed`, `skipped-draft`, `not-uploaded`, with the deployment result) and `counts`.
- `orchestrator --artifact-filter` also takes the artifact folder (`artifactDir`, as change
  detection passes it) or the folder's source ID: both select every ID deployed from that folder.
  Before, only `artifactId` matched, so a folder name selected nothing for deployment copies.

- `lint`: checks the flows of a content tree (local files only) with 30 rules in 8 groups and
  reports rule, severity and suggestion. See [docs/lint.md](docs/lint.md).
  - reuse: scripts and mappings in several flows (-> script collection, global or per package),
    missing script collections; Partner Directory candidates: routers on many literal values,
    lookup tables in scripts, flows deployed several times with different `configOverrides`.
  - dead weight (unconnected steps, unused scripts, resources, parameters, no-op content
    modifiers, properties nobody reads), simplification, robustness, performance,
    configuration (fixed endpoints, URLs and secrets in scripts), hygiene.
  - `.cpi/lint.yaml` for severities, thresholds and script collection names; a baseline
    (`--update-baseline`) so `--fail-on` fails only on new findings; `--changed --since`.
  - `--fix` (`--dry-run`): moves scripts into script collections, deletes unused scripts,
    removes unconnected steps (`--fix-rules all`: also no-op content modifiers).
- MCP tools `lint` (read) and `lint_fix` (local files).
- Skill `cpi-improve`: lint, write proposals to `.cpi/improvements/`, apply one at a time.

## 0.4.0

- `snapshot` for a repository with one copy of every artifact (`packages/` in the tenant layout,
  edited by developers, deployed by the orchestrator). See [docs/snapshot.md](docs/snapshot.md).
  - Per artifact: `new`, `changed`, `unchanged`, `deleted`, `local-modified`, `local-only`,
    `derived`, in the text output, `--output json` (`counts`, `artifacts`) and the job summary.
  - `--dry-run` writes no files and no state; `--fail-on-local-modified` exits 5 (CI).
  - **Local edits are no longer overwritten**: an artifact whose files differ from what the last
    snapshot wrote is skipped as `local-modified` (`--overwrite-local` forces it). The state has a
    hash of every local file (`filesHash`).
  - A written artifact folder is replaced by the tenant's content, so files deleted on the
    tenant are deleted locally. Artifacts and packages deleted on the tenant are reported;
    `--prune` removes them (never local edits, never local-only folders).
  - Deployment copies are not written: IDs deployed from another `artifactDir`, and
    `deploymentPrefix` variants of artifacts and packages, read from `--deploy-config` (else
    `orchestrator.deployConfig`). A copy edited on the tenant is reported as a warning.
    `--include-derived` turns this off. For artifacts deployed with `configOverrides`, the
    repository's values of those keys are kept.
  - Stable output: `parameters.prop` without the timestamp comment and with sorted keys, LF line
    endings for text files, binaries byte for byte; an unchanged snapshot gives no diff, the
    state file included.
- `download` / MCP `download_artifact` normalize `parameters.prop` the same way.

## 0.3.0

- OpenCode: MCP setup (`opencode.json`) and `cpictl skills install --agent opencode`
  (`.opencode/skills`, `--user`: `~/.config/opencode/skills`). Cursor setup expanded (global
  file, `${env:...}`, tool approval). Examples with profiles and with environment variables in
  [docs/examples/agents](docs/examples/agents); see [docs/agents.md](docs/agents.md).

- Defaults unified: deployment status every 10 s, up to 30 checks (5 minutes) for `deploy`
  (before 30 s x 10), `orchestrator` and `configure` (before 15 s x 5, which timed out slow
  deployments after 75 s); 5 deployments at the same time per package (before 3). All defaults
  in one table: [docs/configuration.md](docs/configuration.md#defaults).
- Job summary: `orchestrator`, `configure`, `deploy`, `undeploy` and `snapshot` append a markdown
  summary (counts, plan, deployments with errors first) to `$GITHUB_STEP_SUMMARY`, or to
  `--summary FILE`; `--summary off` disables it. See [docs/ci.md](docs/ci.md#job-summary).
- `cpictl doctor` (MCP: `doctor`): checks configuration, connection and authentication, and per
  API area whether the credentials can use it (403: missing role, 404: not offered). Read only.
- MCP: `upload_artifacts` uploads several artifacts in one call (8 at a time); `get_parameters`
  takes `artifact_ids`. `list_packages` and `list_artifacts` results are reused for
  `--cache-ttl` seconds (default 60, `cached: true`; `refresh: true` re-reads); every tool that
  changes the tenant clears the cache.
- Parallel: `orchestrator --parallel` (default 8) uploads and compares artifacts at the same time
  across all packages (packages are still created first); `configure --parallel` (8) reads the
  parameters of all artifacts at the same time and writes the changes one after another;
  `configure pull --parallel` (8) reads packages and artifacts in parallel; `drift --parallel`
  (8, before: 4).
- Deploy once per pipeline: `orchestrator --defer-deploy` and `configure --defer-deploy` add what
  needs a deployment (and why) to `.cpi/pending-deploy.json`; `cpictl deploy --pending` deploys
  each artifact once, with force when any step needs it, and keeps only failures in the file.
  `deploy --plan` (also with `--artifact-ids`) predicts without triggering. See
  [docs/ci.md](docs/ci.md#pipeline-snapshot-update-configure-deploy-once).
- `orchestrator`: a content change with the running version is no longer undeployed during the
  upload; phase 2 deploys it with force, so the runtime keeps running in between.
- Plan mode: `orchestrator --plan` reports per artifact whether it would be created, updated or
  left unchanged and whether it would be deployed, and why; nothing is written. `configure
  --plan` (= `--dry-run`) now also says which artifacts would be deployed and why (`plan` in the
  result). MCP: `upload_artifact` and `deploy` take `dry_run`.
- `orchestrator`: compares existing artifacts with the snapshot state (`.cpi/snapshot-state.json`,
  written by `snapshot` earlier in the pipeline) instead of downloading each one again. Artifacts
  the state does not cover, or that changed on the tenant since the snapshot, are downloaded as
  before. `--snapshot-state` picks the file (`off`: always download), `--verify-download`
  downloads anyway. New statistics: `comparedWithSnapshot`, `downloadedForComparison`,
  `artifactsChanged`, `artifactsUnchanged`. `snapshot` records the tenant host and a content
  hash per artifact for that.
- Every command and the MCP server retry tenant reads (GET) answered with 429 or 502-504 up to
  three times with backoff (before: `snapshot` only). `--read-retries` (`CPICTL_READ_RETRIES`)
  sets the count, `0` turns it off. Writes are never retried.
- `cpictl stats`: local usage statistics. Every command and MCP tool call appends name, source,
  exit code, duration and time to `~/.cpictl/stats.jsonl` (no arguments, hosts or names; nothing
  is sent anywhere; compacted at 1 MiB; `CPICTL_STATS=off` disables it).
- MCP: every tool result has `durationMs`; the server log has one line per finished call.

## 0.2.4

- Every command logs the time it took at the end (`⏱ snapshot finished in 4m12s`, JSON log field
  `durationMs`); the `--output json` envelope has a new field `durationMs`.
- `snapshot`: `--parallel` (default now 8) limits the artifacts downloaded at the same time across
  all packages instead of the packages: the artifacts of a large package are downloaded in
  parallel too, so it no longer runs alone at the end. A failing artifact does not stop the rest
  of its package. The result reports `artifactsDownloaded`, `artifactsSkipped` and `seconds`.

## 0.2.3

- `snapshot`: `--incremental` skips the download of artifacts whose designtime version,
  `ModifiedAt`, configured parameters (SHA-256) and local copy (SHA-256) are unchanged since the
  last snapshot (state in `.cpi/snapshot-state.json`, committed with the snapshot; artifacts
  without `ModifiedAt` are always downloaded). Packages run `--parallel` (default 4); a failing
  package no longer aborts the snapshot (exit code 7, the rest is written and committed).
  Throttled or gateway-failed reads (429, 502-504) are retried with backoff. See
  [docs/snapshot.md](docs/snapshot.md).

## 0.2.2

- Versioning for promotion (`dev -> test -> prod`, same content = same version), see
  [docs/versioning.md](docs/versioning.md):
  - `--versioning manifest|keep|tenant-bump` (env `CPICTL_VERSIONING`) on `update artifact`,
    `sync`, `snapshot restore`, `orchestrator`, `configure`, `deploy` and `mcp`; `versioning` per
    package or artifact in deployment and configure files for exceptions. `manifest`: upload sets
    the designtime version to `Bundle-Version` of the repository (SaveAsVersion when the tenant
    did not take it), deploy refuses lower versions (no timestamp exception). `keep`: tenant
    versions, no downgrade guard. `tenant-bump`: max(designtime, runtime)+1 on content changes.
    Upload and deploy log the version per artifact and why (`versionReason`, `versioning`).
  - `cpictl version bump [--changed] [--level] [--package] [--artifact] [--dry-run]` and MCP
    `bump_versions`: raise `Bundle-Version` of artifacts changed since the commit that last set
    it; no tenant access.
  - Export (`sync --target git`, `snapshot`) keeps the repository's `Bundle-Version` (or takes a
    higher designtime version) instead of the download's (usually 1.0.0); `download` writes the
    designtime version. Content comparisons ignore `Bundle-Version`.
  - `manifest` details: content that differs from the tenant's while `Bundle-Version` equals the
    tenant's designtime or running version is refused before anything is written (bump it); the
    version set with SaveAsVersion is read back; a lower target version is attempted with a
    warning and a refusal is reported with the version to exceed; the orchestrator deploys only
    the directory's `Bundle-Version` (rule `guard` otherwise).
  - Rules in upload and deploy results and logs: `manifest`, `bump`, `keep`, `tenant`, `guard`
    (plus `version`, `modified after deployment`, `allowDowngrade` without a mode), with a reason.
- Orchestrator multi-deploy: rewriting `Bundle-SymbolicName` for a final artifact ID keeps its
  attributes (`; singleton:=true` was dropped) and removes continuation lines of the old
  `Bundle-Name`; values are wrapped at 72 bytes.
- MCP `upload_artifact` returns the result (version, rule) together with an error.

- Deploy (`deploy`, `configure`, `orchestrator`, MCP `deploy`): a designtime version older than
  the running one is deployed when the designtime artifact was changed after the running version
  was deployed (designtime `ModifiedAt` later than runtime `DeployedOn`; e.g. the runtime came
  from a manual deployment of an older build with a bumped version). Otherwise, or when a
  timestamp is missing, it is still refused. Every result names the deciding rule
  (`version`, `modified after deployment`, `allowDowngrade`) on its log line and in `rule`.
- `configure`: `allowDowngrade: true|false` per artifact and per package in the configure file
  (artifact wins, then package, then `--allow-downgrade` / `configure.allowDowngrade` /
  `deploy.allowDowngrade`); before, configure could not allow a downgrade at all. The
  modification time is read before parameters are written, so a parameter change never makes
  older content look new. The refusal names the `allowDowngrade` key.
- `resources` / MCP `list_resources`: `ResourceSize` sent as a string (`"1234"`) or with
  decimals no longer fails (`cannot unmarshal string into ... ResourceSize`).
- Partner Directory (`pd diff`, `pd get`, `pd-snapshot`, `pd-deploy`, MCP `pd_diff`,
  `get_pd_parameters`): all pages are read. The tenant returns at most 30 binary parameters per
  page; listings stopped after the first page, so further binaries showed as missing or
  `create`, and snapshots could miss them. Paging now follows `__next` or `$skip` until
  `__count` is reached.

## 0.2.1

- `configure`: artifacts without `parameters` (script collections, mappings, flows that only need
  a deployment) no longer fail with 404 (`Integration design time artifact not found`): their
  parameters are not read, and with `deploy: true` they are deployed unless the runtime already
  runs the designtime version. Artifacts whose parameters are unchanged are now also deployed
  when the designtime version is newer than the runtime (they were skipped). `parameters` on a
  type other than Integration is reported as an error before anything is written; `config_diff`
  skips artifacts without parameters.
- Skills: cpi-test starts every run with the static checks (`validate_artifact`,
  `check_guidelines`, runtime version) and reports them; cpi-build runs `check_guidelines`
  after the validation.
- Package IDs: only letters and digits are accepted (`packages create`, MCP `create_package`);
  the tenant refuses `_`, `-` and `.`. Examples and the cpi-discover skill no longer suggest them.

## 0.2.0

- MCP tool `help`: what the server offers in its mode (available and disabled tools), workflows
  (task -> skill, tools, docs), the cpi skills with their instructions and reference files, and
  the CLI commands; `topic` for details of one tool, skill, skill file or command. Lets clients
  without skill support use the skills.
- `cpictl skills list|show|install`: the skills are built into the binary; `install` copies them
  into `.agents/skills`, `.cursor/skills`, `.gemini/skills` or `.claude/skills` (repository or
  `--user`) without a checkout of this repository.
- `docs/mcp.md` is checked to mention every MCP tool.
- `cpictl iflow copy` (MCP `copy_iflow`): copy an integration flow from the tenant or a local
  folder under a new ID, name, description and version; renames the model and `.project`,
  keeps `;singleton:=true`, wraps manifest lines correctly, and changes every sender address
  (in the model or in `parameters.prop`), refusing to keep one unless asked. See
  [docs/new-flows.md](docs/new-flows.md). The `cpi-build` skill uses it for new flows.
- MCP `upload_artifact` without `name` uses `Bundle-Name` of the manifest, as the CLI does
  (it used the ID).
- Plugin: brief template for new flows (`.cpi/templates/brief.md`, filled in as
  `.cpi/briefs/<name>.md`; cpi-plan asks only for what is missing), a *Templates* section in
  the conventions (one reference flow per pattern, proposed by cpi-discover, agreed by the
  team) and house scripts in `.cpi/templates/scripts/`; cpi-build copies from them.
- Content graph: `discover` (MCP `discover_tenant`) also writes `.cpi/graph.json`: flows,
  packages, endpoints, receiver systems, credentials, scripts, headers, properties and Partner
  Directory parameters as nodes; `sends_to` links flows through matching ProcessDirect and JMS
  addresses (`{{parameter}}` addresses resolved with `parameters.prop`). Query it with
  `cpictl graph search|neighbors|path|build` and the MCP tools `graph_search`, `graph_neighbors`,
  `graph_path` (read a local file only). Discovery now also records receiver addresses,
  the values of address parameters and literal Partner Directory references per flow.
  See [docs/graph.md](docs/graph.md).
- Codex, Cursor and Gemini CLI: MCP configuration examples (`docs/examples/agents`),
  `scripts/install-skills.sh` to copy the skills into `.agents/skills`, `.cursor/skills` or
  `.gemini/skills`, an `AGENTS.md` template, and [docs/agents.md](docs/agents.md).
  `cpi-review` reviews by itself when there is no `cpi-reviewer` agent.

## 0.1.0

First release.

- CLI for SAP Cloud Integration: inspect (`packages`, `artifacts`, `status`), designtime
  (`update artifact`, `update package`), runtime (`deploy`, `undeploy`), parameters
  (`params`, `configure`), multi-package deployments (`orchestrator`, `config-generate`),
  Git (`sync`, `snapshot`), API Management and Partner Directory commands.
- `--output json` result documents and a stable exit code contract.
- Deployments confirmed via the BuildAndDeployStatus task and a fresh runtime artifact,
  with one structured result per artifact (including designtime and runtime version).
- `drift` (CLI and MCP): local artifacts vs designtime and runtime (in_sync, tenant_newer,
  local_newer, diverged, not_on_tenant). Content comparison no longer needs an external `diff`
  program (it failed on Windows).
- Runtime data: data stores (list, entries, get, delete with confirm), variables, JMS queues and
  broker, number ranges, log files (tail), idempotent repository, ID mapper (CLI and MCP).
- `set_log_level` reverts to INFO after `revert_after_minutes` and on server shutdown; trace tools
  report a missing role (403) as `status: "missing_role"` instead of an auth error.
- Build loops: `loop_start` / `loop_status` / `loop_end` with server-enforced limits
  (iterations, repeated errors, wall clock, deploys); exit code 8 / `stopped`. `cpictl mcp --mode
  discover|operate|develop|full`.
- Tracing across flows: `send` / `send_test_message` send a W3C `traceparent` and return the
  `traceId`; `logs tree` / `get_trace_tree` build the call tree and the first failure;
  `logs --header name=value` / `custom_header` and `--package-id` search by custom header
  (scoped, capped client-side scans). Message logs include `applicationMessageType`,
  `packageName` and `predecessorMessageGuid`.
- `configure` writes only changed parameters and redeploys only changed artifacts (`--force`,
  `--dry-run` diff, `--offline`); MCP `config_diff`. `pd deps` / `pd_dependencies` show which
  flows read which Partner Directory parameters.
- Partner Directory: `pd get` / `get_pd_parameters`, `pd diff` / `pd_diff`, and
  `pd-deploy --keys PID:ID` / `pd_deploy keys` for single-parameter changes.
- Downgrade guard: a designtime version older than the running one is not deployed unless
  `--allow-downgrade` / `allow_downgrade`.
- Message processing logs: `logs` (query, `--wait` for final status, error texts),
  `logs get` (error text, custom headers, adapter attributes, attachments, persisted messages),
  `logs steps` (failing step), `logs attachment`, `logs payload`.
- `validate` and `guidelines` (tenant check and design guidelines), `endpoints`,
  `resources`, `download`, `status --runtime-status ERROR`.
- Security material: `credentials` (list, set-user, set-oauth2, set-secure-param, declarative
  `apply`, delete) with secrets from env/file/stdin only; `keystore` (list with expiry check,
  export-cert, import-cert).
- `send` (test messages to a flow's endpoint or, through a test harness flow, to a
  ProcessDirect address; waits for the processing log), `log-level` (e.g. TRACE),
  `logs trace` / `logs trace-message` (payload and headers per step), `discover`
  (inventory of existing flows for conventions), `packages create`.
- CSRF tokens handled once per session with transparent refresh and retry.
- `cpictl mcp --read-only / --tools / --disable-tools`: limit the tools per server.
- MCP server (`cpictl mcp`) with tools for listing, status, message logs, parameters,
  upload, validation, deploy, undeploy, Partner Directory deploy and read-only security
  material.
- Claude Code plugin `cpi` (marketplace in this repository): skills cpi-discover, cpi-plan,
  cpi-build, cpi-test, cpi-review and the read-only cpi-reviewer agent; conventions live in
  each content repository under `.cpi/`.
- Profiles: `~/.cpictl/<name>.yaml`, `cpictl profile list|use|current`, `--profile`,
  `CPICTL_PROFILE`; every tenant command logs the profile and host it uses.
- Project file: `cpictl.yaml` in the repository (no secrets; home credentials are never sent
  to hosts it sets), merged over `$HOME/cpictl.yaml`.
- `CPICTL_CONFIG` selects the config file; a warning when a config file with secrets is
  readable by others.
- Settings via flags, `CPICTL_*` environment variables and `$HOME/cpictl.yaml`.
