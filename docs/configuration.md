# Configuration

## Quick setup

Pick one of these, depending on how many tenants you work with:

| Situation | Setup | Example |
|-----------|-------|---------|
| One tenant | `$HOME/cpictl.yaml` with the connection (chmod 600) | [examples/cpictl.yaml](examples/cpictl.yaml) |
| Several tenants, switching often | one profile per tenant in `~/.cpictl/`, `cpictl profile use <name>` | [examples/profiles/](examples/profiles) |
| CI pipelines | `CPICTL_*` environment variables from the CI secret store | [ci.md](ci.md) |
| Defaults for one repository | `cpictl.yaml` in the repository root, committed, no secrets | [examples/project-cpictl.yaml](examples/project-cpictl.yaml) |

Profiles step by step:

```bash
mkdir -p ~/.cpictl && chmod 700 ~/.cpictl
cp docs/examples/profiles/dev.yaml ~/.cpictl/dev.yaml     # fill in host, client ID and secret
cp docs/examples/profiles/qa.yaml  ~/.cpictl/qa.yaml
chmod 600 ~/.cpictl/*.yaml

cpictl profile list            # * marks the active profile
cpictl profile use dev         # default from now on
cpictl packages                # logs "Profile dev (<host>)" first
cpictl --profile qa status --runtime-status ERROR   # one command against QA
```

The values come from the service keys in SAP BTP: `url` → `tmn-host`, `tokenurl` → `oauth-host`
(host only), `clientid` / `clientsecret`. See
[Creating an OAuth client](#creating-an-oauth-client-in-sap-btp).

## Where settings come from

For every flag, the first of these that is set wins:

1. Command line flag, e.g. `--tmn-host`
2. Environment variable: `CPICTL_` + flag name in upper case with `-` replaced by `_`,
   e.g. `CPICTL_TMN_HOST`, `CPICTL_ARTIFACT_IDS`, `CPICTL_OUTPUT`
3. Top-level key in the config file with the flag name, e.g. `tmn-host: ...`
4. Command section in the config file, e.g. `deploy.artifactIds` (the key is shown in each
   flag's help text as `(config: ...)`)
5. The flag's default

Config files, first match wins:

1. `--config <file>`: this file only.
2. A **profile** ([Profiles](#profiles-switching-tenants)): `--profile <name>`, `CPICTL_PROFILE`,
   or the one chosen with `cpictl profile use`.
3. `CPICTL_CONFIG=<file>`: this file only.
4. Otherwise `$HOME/cpictl.yaml` (your personal settings and credentials).

A profile and `$HOME/cpictl.yaml` are overlaid by the
  **project file**: `cpictl.yaml` in the current directory or a parent directory, up to the
  repository root (the directory with `.git`). See [Project file](#project-file).
A missing default file is fine; a file given with `--config` that cannot be parsed is a
usage error (exit code 2).

> **Secrets:** you may keep `oauth-clientsecret`, `tmn-password` and the runtime secrets in a
> personal config file (like `~/.netrc` or `~/.aws/credentials`): make it readable only by you
> (`chmod 600`; cpictl warns otherwise) and never commit it. In CI and shared setups use
> environment variables instead. The file is read as plain YAML: `${VAR}` is **not** expanded.

### Project file

A `cpictl.yaml` in a content repository sets that repository's tenant and command defaults
for everyone who works in it (CLI and `cpictl mcp`, which runs in the repository):

```yaml
# <repo>/cpictl.yaml - committed, no secrets
tmn-host: mytenant-dev.it-cpi018.cfapps.eu10-003.hana.ondemand.com
oauth-host: mytenant-dev.authentication.eu10.hana.ondemand.com
oauth-clientid: sb-xxxxxxxx!b1234|it!b5678
deploy:
  timeout: 15m
orchestrator:
  packagesDir: ./packages
```

Rules, because the file comes with the repository:

- It may not contain secrets (`tmn-password`, `oauth-clientsecret`, `runtime-oauth-clientsecret`,
  `runtime-password`): exit code 2. Provide them with environment variables
  (`CPICTL_OAUTH_CLIENTSECRET`, for example from a git-ignored `.envrc` with direnv) or a
  personal file (`CPICTL_CONFIG`).
- Credentials from `$HOME/cpictl.yaml` are only sent to the hosts in that file. If the project
  file sets a different `tmn-host`, `oauth-host` or `runtime-oauth-host` while the credentials
  come from the home file, cpictl stops with exit code 2 instead of sending them elsewhere.
  Credentials from the environment or `--config` are used as given.

`--debug` logs which config files were read.

### Profiles (switching tenants)

One file per tenant in `~/.cpictl/`, with the connection and credentials:

```bash
mkdir -p ~/.cpictl && chmod 700 ~/.cpictl
cat > ~/.cpictl/dev.yaml <<'YAML'
tmn-host: mytenant-dev.it-cpi018.cfapps.eu10-003.hana.ondemand.com
oauth-host: mytenant-dev.authentication.eu10.hana.ondemand.com
oauth-clientid: sb-...
oauth-clientsecret: ...
runtime-oauth-clientid: sb-...        # for cpictl send
runtime-oauth-clientsecret: ...
YAML
chmod 600 ~/.cpictl/*.yaml            # same for qa.yaml, prod.yaml, ...
```

Switch:

```bash
cpictl profile list                   # * marks the active profile
cpictl profile use qa                 # default for every following command (all shells)
cpictl --profile dev deploy --artifact-ids OrderIntake   # one command elsewhere
export CPICTL_PROFILE=dev             # this shell only, overrides 'profile use'
cpictl profile current
cpictl profile use -                  # back to $HOME/cpictl.yaml
```

Every command against a tenant logs `Profile <name> (<host>)` first, so you always see where
it goes. A repository's `cpictl.yaml` is still overlaid; if its hosts differ from the
profile's, cpictl stops instead of sending the profile's credentials there.

For MCP, start one server per profile, e.g. `"args": ["mcp", "--root", ".", "--profile", "dev"]`
and `["mcp", "--root", ".", "--profile", "qa", "--read-only"]`. The server keeps its profile
for its whole lifetime; `cpictl profile use` does not change a running server.

Flags that are marked as required (for example `sync --package-id`) must come from the
command line, the environment or a top-level key; a command section key is read too late
for cobra's required-flag check.

## Tenant connection

| Flag | Environment variable | Description |
|------|----------------------|-------------|
| `--tmn-host` | `CPICTL_TMN_HOST` | Tenant management host of Cloud Integration (or API portal host for APIM), without `https://` |
| `--oauth-host` | `CPICTL_OAUTH_HOST` | Token server host, e.g. `<subdomain>.authentication.eu10.hana.ondemand.com` |
| `--oauth-clientid` | `CPICTL_OAUTH_CLIENTID` | OAuth client ID |
| `--oauth-clientsecret` | `CPICTL_OAUTH_CLIENTSECRET` | OAuth client secret |
| `--oauth-path` | `CPICTL_OAUTH_PATH` | Token path, default `/oauth/token` |
| `--tmn-userid` | `CPICTL_TMN_USERID` | Basic Auth user (alternative to OAuth) |
| `--tmn-password` | `CPICTL_TMN_PASSWORD` | Basic Auth password |

Either the three OAuth values or the Basic Auth pair are required, except for
`config-generate`, which works offline. The host may also be given
as `https://host` or `host:port`. Plain `http://` is only honoured for `localhost` /
`127.0.0.1` (used by the offline tests); for any other host https is used.

### Runtime endpoints (test messages)

`send` and the MCP tool `send_test_message` call the flow's own endpoint, which normally needs
a different service key than the API: *SAP Process Integration Runtime*, plan
**integration-flow**, with the role `ESBMessaging.send`.

| Flag | Environment variable | Description |
|------|----------------------|-------------|
| `--runtime-oauth-clientid` | `CPICTL_RUNTIME_OAUTH_CLIENTID` | OAuth client ID of the integration-flow key |
| `--runtime-oauth-clientsecret` | `CPICTL_RUNTIME_OAUTH_CLIENTSECRET` | Its secret |
| `--runtime-oauth-host` | `CPICTL_RUNTIME_OAUTH_HOST` | Token server host, default `--oauth-host` |
| `--runtime-userid` / `--runtime-password` | `CPICTL_RUNTIME_USERID` / `_PASSWORD` | Basic Auth instead of OAuth |

Without these settings the API credentials are used (works when they also carry
`ESBMessaging.send`, e.g. with Basic Auth). Missing roles are exit code 3.

### Gateway mode

With a [cpictl gateway](https://github.com/cpars-innovation/cpictl-gateway) in front of the tenant, cpictl holds
no SAP key: it sends every API call to the gateway with your own token, and the gateway decides per user which
packages you see and may change.

```yaml
# ~/.cpictl/dev.yaml
gateway: https://gw.example.com/t/dev      # the gateway and the SAP tenant name
```

Sign in once with Entra ID and cpictl keeps and refreshes the token for you:

```yaml
# ~/.cpictl/dev.yaml
gateway: https://gw.example.com/t/dev      # the gateway and the SAP tenant name
login:
  tenantId: <entra directory id>           # the gateway's Entra tenant
  clientId: <public-client app>            # the app registration cpictl signs in as
  scope: api://<gateway app>/access        # the gateway's API scope (string or list)
```

```bash
cpictl --profile dev auth login            # opens a browser (or: --device-code)
cpictl --profile dev auth status           # who the gateway takes you for (also: cpictl whoami)
cpictl --profile dev packages              # uses the stored token, refreshed silently
cpictl --profile dev auth logout           # clears the stored token
```

**Entra app registration for cpictl** (once per organisation, by an Entra admin): a public-client app with the
platform **Mobile and desktop applications** and the redirect URI `http://localhost` (Entra accepts any port on
it; cpictl listens on a free one). Give it the delegated permission on the gateway API's scope. Leave **Allow
public client flows** off unless you need `--device-code` on headless hosts: device code sign-in is a common
phishing path, and with it off only the browser sign-in works.

The token is stored per profile under `$HOME/.cpictl/tokens/<profile>.json` (mode `0600`; on Windows the mode
sets no ACL, so the file is protected by the user profile's permissions until an OS-keychain store follows) and refreshed with the refresh token, so a long-running `cpictl mcp` session does
not stop working after an hour. The file holds the refresh token, the long-lived credential: treat it like a
password, and run `cpictl auth logout` on shared machines. `login.authority` (default: Entra's public cloud) must use https. Without a `login` block,
or before the first `cpictl auth login`, cpictl falls back to the `CPICTL_GATEWAY_TOKEN` environment variable:

```bash
export CPICTL_GATEWAY_TOKEN=...            # your access token for the gateway
cpictl --profile dev packages
```

| Flag | Environment variable | Description |
|------|----------------------|-------------|
| `--gateway` | `CPICTL_GATEWAY` | Gateway URL including `/t/<SAP tenant>` |
| — | `CPICTL_GATEWAY_TOKEN` | Your token when not signed in (never a flag or a config file) |

- `--gateway` excludes `--oauth-*` and `--tmn-userid`: in gateway mode the gateway holds the key.
- A project `cpictl.yaml` must not set `gateway` or `login` (they choose where your token is sent and where you
  sign in): set them in the profile or `$HOME/cpictl.yaml`.
- `cpictl auth status` (short: `cpictl whoami`) shows who the gateway takes you for, your roles, when
  your token expires, and your scope per SAP tenant.
- `cpictl auth` is only for gateway mode. Profiles that talk to SAP directly keep their service key in
  the profile and need no sign-in.
- Not yet in gateway mode: an OS-keychain token store (the `0600` file is the fallback), and test
  messages (`send`), which still use the runtime settings above.

## Global flags

| Flag | Description |
|------|-------------|
| `--config` | Config file (default `$HOME/cpictl.yaml`) |
| `--profile` | Profile `~/.cpictl/<name>.yaml` ([profiles](#profiles-switching-tenants)) |
| `--output text\|json` | Result format, see [README](../README.md#output-and-exit-codes) |
| `--read-retries` | Retries of tenant reads answered with 429 or 502-504 (default 3, `0`: none); writes are never retried |
| `--summary` | Job summary file for `orchestrator`, `configure`, `deploy`, `undeploy`, `snapshot` (default `$GITHUB_STEP_SUMMARY` when set, `off`: none), see [ci.md](ci.md#job-summary) |
| `--debug` | Debug logging: request URLs and response errors; request bodies are never logged (they can contain secrets) |

Every command ends with the time it took (`⏱ ... in 12.3s`); `cpictl doctor` checks the whole
setup, `cpictl stats` shows local usage statistics.

## Config file example

A complete annotated file is in [examples/cpictl.yaml](examples/cpictl.yaml). Short version:

```yaml
tmn-host: mytenant.it-cpi018.cfapps.eu10-003.hana.ondemand.com
oauth-host: mytenant.authentication.eu10.hana.ondemand.com
# oauth-clientid / oauth-clientsecret: via CPICTL_OAUTH_CLIENTID / CPICTL_OAUTH_CLIENTSECRET

deploy:
  artifactType: Integration
  timeout: 20m           # slow deployments: wait up to 20 minutes for all of them

orchestrator:
  packagesDir: ./packages
  deployConfig: ./001-deploy-config.yml

pd-deploy:
  resources-path: ./partner-directory
  full-sync: false
```

Sections used by the commands: `deploy`, `undeploy`, `configure` (and `configure.pull`),
`orchestrator`, `update.artifact`, `update.package`, `sync`, `sync.apiproxy`,
`sync.apiproduct`, `snapshot`, `restore`, `pd snapshot`, `pd deploy`.

## Defaults

The defaults are chosen for tenants with a few hundred packages and artifacts; set a value only
when you need something else.

| Setting | Default | Where |
|---------|---------|-------|
| Deployment status checks | every 10 s from one runtime list for all deployments; deploy, configure and orchestrator wait at most 10 minutes for all of them together ([deployments.md](deployments.md)); a failed deployment ends at its next check | `deploy` (`delayLength`, `timeout`), `orchestrator` / `configure` (`deployDelaySeconds`, `deployTimeout`); `undeploy` and MCP: 30 checks per artifact (`maxCheckLimit`, `--poll-interval`, `--max-checks`) |
| Deployments triggered at a time | 5, then all watched together; script collections and mappings first, then flows, across packages | `orchestrator`, `configure`, `deploy --pending` (`parallelDeployments`) |
| Tenant reads and uploads at the same time | 8, across all packages | `snapshot`, `orchestrator`, `configure`, `configure pull`, `drift` (`parallel`) |
| Retries of throttled reads (429, 502-504) | 3, backoff 2 s, 4 s, 8 s (`Retry-After` honoured); writes never | all commands and MCP (`read-retries`) |
| Parameters per `$batch` request | 90 | `configure` (`batchSize`) |
| Orchestrator comparison | with the snapshot state when one of this tenant is found, else download | `orchestrator` (`snapshotState`, `verifyDownload`) |
| Artifacts in draft on the tenant | skipped and reported, not uploaded, not deployed (exit 0); `ERROR`: the run fails (exit 5) | `orchestrator` (`draftHandling`: `SKIP`, `ERROR`), MCP `upload_artifacts` / `deploy` (always skipped) |
| Snapshot and local edits | artifacts edited locally since the last snapshot are skipped (`local-modified`); deleted artifacts are reported, not removed; deployment copies of the deploy config are not written | `snapshot` (`overwriteLocal`, `prune`, `includeDerived`) |
| Snapshot of drafts and orphan parameters | drafts skipped (`ADD`: written with a numeric version); `parameters.prop` keys missing from `parameters.propdef` reported, not written | `snapshot` (`draftHandling`, `keepOrphanParameters`) |
| Incremental snapshot | off (on once `ModifiedAt` is verified on your tenant, see [snapshot.md](snapshot.md)) | `snapshot` (`incremental`) |
| MCP list cache | 60 s | `mcp --cache-ttl` |
| Job summary | `$GITHUB_STEP_SUMMARY` when set | `summary` |
| Usage statistics | on, local only (`~/.cpictl/stats.jsonl`, at most ~1 MiB) | `CPICTL_STATS=off` |

[examples/cpictl.yaml](examples/cpictl.yaml) lists them all with their config keys.

## Creating an OAuth client in SAP BTP

cpictl uses the OAuth 2.0 client credentials flow against the Cloud Integration API.

1. In the BTP cockpit, open the subaccount and the Cloud Foundry space that holds Integration Suite.
2. **Services → Instances and Subscriptions → Create**:
   - Service: *SAP Process Integration Runtime*, plan: **api**
   - Grant type: **Client Credentials**
   - Roles: the role collection/roles needed for designtime read/edit and deployment
     (for example `WorkspacePackagesRead`, `WorkspacePackagesEdit`, `WorkspaceArtifactsDeploy`,
     `MonitoringDataRead`) plus Partner Directory configuration roles for `pd-*` commands.
     Role names differ between tenant generations; check the role list offered for the plan.
3. Open the instance and **Create** a service key.
4. From the key:
   - `url` → host only → `tmn-host`
   - `tokenurl` → host only → `oauth-host` (the path is `/oauth/token`, the default of `--oauth-path`)
   - `clientid` → `CPICTL_OAUTH_CLIENTID`
   - `clientsecret` → `CPICTL_OAUTH_CLIENTSECRET`

For API Management (`sync apiproxy`, `sync apiproduct`) create a key for the *API Management,
API portal* service (plan `apiportal-apiaccess`) instead and use its host values.

Missing roles show up as exit code 3 (HTTP 403).
