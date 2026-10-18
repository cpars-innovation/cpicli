# cpictl

**Build, deploy and operate SAP Cloud Integration (CPI) content from the command line,
CI/CD pipelines and AI agents.**

```bash
cpictl update artifact --artifact-id OrderIntake --package-id Orders --dir-artifact ./OrderIntake
cpictl deploy --artifact-ids OrderIntake
cpictl status --artifact-ids OrderIntake --output json
```

- **CLI and MCP server in one binary.** `cpictl mcp` gives AI agents the same operations,
  with the same safety rules, as the command line.
- **Machine-readable by design.** `--output json` returns one result document on stdout,
  logs go to stderr, and the exit code says *why* something failed.
- **Deployments you can trust.** `deploy` follows the tenant's build/deploy task and waits
  for the *new* runtime artifact. A redeploy is never reported as done while the previous
  version is still running.
- **Fast pipelines for large tenants.** Incremental, parallel snapshots; the orchestrator
  compares with the snapshot instead of downloading every artifact again; `--plan` shows what
  would be uploaded and deployed, and every artifact is deployed at most once per run.
- **Safe defaults.** Partner Directory full sync never deletes a partner's parameters when its
  local files cannot be read; destructive MCP tools need explicit confirmation; secrets are
  never taken from flags, never returned and never logged.

## Contents

- [Install](#install)
- [Configuration and profiles](#configuration-and-profiles)
- [Everyday workflow](#everyday-workflow)
- [Commands](#commands)
- [Output and exit codes](#output-and-exit-codes)
- [AI agents (MCP)](#ai-agents-mcp)
- [Documentation](#documentation)
- [Development](#development)

## Install

Download a binary for your platform from the
[releases](https://github.com/cpars-innovation/cpicli/releases), or install with Go 1.26 or later:

```bash
go install github.com/cpars-innovation/cpicli/cmd/cpictl@latest   # or @v0.1.0
cpictl --version
cpictl doctor          # after the configuration below: connection, authentication, roles
```

The binary lands in `$(go env GOPATH)/bin` (usually `~/go/bin`), which must be on your `PATH`.
While the repository is private, tell Go to fetch it directly with your Git credentials:
`go env -w GOPRIVATE=github.com/cpars-innovation/*`.

From source:

```bash
git clone https://github.com/cpars-innovation/cpicli.git
cd cpicli
make build                 # -> bin/cpictl
./bin/cpictl --version
```

`make build-all` cross-compiles for Windows, Linux (amd64, arm64) and macOS.

## Configuration and profiles

### 1. Get the credentials

cpictl talks to the Cloud Integration API with an OAuth client. In the SAP BTP cockpit, create
a service instance of *SAP Process Integration Runtime*, plan **api**, and a service key
([step by step](docs/configuration.md#creating-an-oauth-client-in-sap-btp)). For test messages
(`cpictl send`, MCP `send_test_message`) create a second key, plan **integration-flow**, with the
role `ESBMessaging.send`.

| Service key field | Setting | Note |
|-------------------|---------|------|
| `url` | `tmn-host` | host only, without `https://` |
| `tokenurl` | `oauth-host` | host only, without `/oauth/token` |
| `clientid` / `clientsecret` | `oauth-clientid` / `oauth-clientsecret` | |
| integration-flow key: `clientid` / `clientsecret` | `runtime-oauth-clientid` / `runtime-oauth-clientsecret` | only for test messages |

### 2. Put each tenant into a profile

A profile is one YAML file per tenant in `~/.cpictl/`, named after the tenant. It holds the
connection and the credentials, so it is personal: readable only by you, never committed.

```bash
mkdir -p ~/.cpictl && chmod 700 ~/.cpictl
cp docs/examples/profiles/dev.yaml ~/.cpictl/dev.yaml     # then fill in the values
cp docs/examples/profiles/qa.yaml  ~/.cpictl/qa.yaml
chmod 600 ~/.cpictl/*.yaml
```

```yaml
# ~/.cpictl/dev.yaml
tmn-host: mytenant-dev.it-cpi018.cfapps.eu10-003.hana.ondemand.com
oauth-host: mytenant-dev.authentication.eu10.hana.ondemand.com
oauth-clientid: "sb-...!b1234|it!b5678"
oauth-clientsecret: "..."
runtime-oauth-clientid: "sb-...|it-rt-mytenant-dev!b5678"   # optional, for test messages
runtime-oauth-clientsecret: "..."
deploy:                                                      # optional command defaults
  timeout: 10m
```

### 3. Switch between tenants

| You want | Do |
|----------|----|
| Make a tenant the default (all shells, until changed) | `cpictl profile use dev` |
| Run one command against another tenant | `cpictl --profile qa status --runtime-status ERROR` |
| Use a tenant in this terminal only | `export CPICTL_PROFILE=qa` |
| See which profiles exist and which is active | `cpictl profile list` (`*` = active) |
| See what the next command will use | `cpictl profile current` |
| Go back to `~/cpictl.yaml` | `cpictl profile use -` |

Every command that talks to a tenant first logs `Profile dev (<host>)`, so you always see where
it goes. `profile use` stores the name in `~/.cpictl/current`.

Give production profiles `tier: prod`. Then `send`, `test run`, MCP `send_test_message` and `run_tests` refuse
to trigger processing there unless `--allow-production` is given (for the MCP server: when it is
started with it). The repository's `cpictl.yaml` cannot set `tier` or `allow-production`.

### How cpictl finds its settings

For each setting the first source that has it wins:

1. a command-line flag (`--deploy-timeout 15m`)
2. an environment variable: `CPICTL_` + the flag name in upper case (`CPICTL_DEPLOY_TIMEOUT=15m`)
3. the repository's `cpictl.yaml` (see below)
4. the active profile, or `~/cpictl.yaml` when no profile is active
5. the built-in default

Which profile is active: `--profile`, else `CPICTL_PROFILE`, else the one from `profile use`.
`--config <file>` (or `CPICTL_CONFIG`) reads only that file: no profile, no repository file.
Command sections (`deploy:`, `configure:`, ...) are merged key by key. `--debug` logs which files
were read.

### Settings for one repository

A `cpictl.yaml` in the root of an integration content repository is read whenever you run
cpictl in that repository (or a subfolder) and is meant to be committed. Use it for the
repository's defaults; [example](docs/examples/project-cpictl.yaml):

```yaml
# <repository>/cpictl.yaml
orchestrator:
  packagesDir: ./packages
configure:
  configPath: ./config/dev.yml
pd-deploy:
  resources-path: ./partner-directory
```

Two rules protect your credentials, because the file comes with the repository:

- It may not contain secrets (`oauth-clientsecret`, `tmn-password`, runtime secrets): cpictl
  stops with exit code 2. Secrets belong in a profile or in environment variables.
- If it sets `tmn-host` or `oauth-host`, they must match the profile's. Otherwise cpictl stops
  instead of sending the profile's credentials to a host the repository chose. Leave the hosts
  out when the repository is used with several tenants.

### CI pipelines

No files needed: set `CPICTL_TMN_HOST`, `CPICTL_OAUTH_HOST`, `CPICTL_OAUTH_CLIENTID` and
`CPICTL_OAUTH_CLIENTSECRET` from the CI secret store ([docs/ci.md](docs/ci.md)).

### When something does not work

| Message | Cause and fix |
|---------|---------------|
| `no tenant configured` | No profile active and no `~/cpictl.yaml`: `cpictl profile use <name>` |
| `profile "x" not found (...); available: ...` | Typo, or the file is not `~/.cpictl/x.yaml` |
| `<repo>/cpictl.yaml contains oauth-clientsecret` | Move the file to a profile: `mv cpictl.yaml ~/.cpictl/<name>.yaml && cpictl profile use <name>` |
| `<repo>/cpictl.yaml sets tmn-host to ... but the credentials ... come from ...` | The repository points at another tenant than your profile: switch profile or remove the host from the repository file |
| `... can be read by other users; run: chmod 600 ...` | A file with secrets is readable by others |
| exit code 3 (`auth`) | Wrong client ID / secret, or the key lacks roles |

All settings: [docs/configuration.md](docs/configuration.md).

## Everyday workflow

```bash
# Look around
cpictl packages
cpictl artifacts --package-id Orders

# Get an iFlow into a local folder, change it, upload it (only if the content differs), check and deploy
cpictl download --artifact-id OrderIntake --dir ./OrderIntake
cpictl update artifact --artifact-id OrderIntake --package-id Orders --dir-artifact ./OrderIntake
cpictl validate --artifact-id OrderIntake
cpictl deploy --artifact-ids OrderIntake

# Did it start? If not, the tenant's error message is in the output
cpictl status --artifact-ids OrderIntake

# Send a test message, wait for the processing log, find the failing step
cpictl send --artifact-id OrderIntake --body-file order.xml --content-type application/xml --wait 60s
cpictl logs steps --message-guid <guid>

# Start a new flow from a template: new ID, name and sender address (see docs/new-flows.md)
cpictl iflow copy --from Template_Sync_HTTPS --id OrderStatus --name "Order status" --address /orders/status --dir ./OrderStatus

# Change externalised parameters and activate them
cpictl params set --artifact-id OrderIntake --param ReceiverHost=orders.example.com
cpictl deploy --artifact-ids OrderIntake --compare-versions=false

# Take it off the runtime again
cpictl undeploy --artifact-ids OrderIntake
```

For many packages at once, use [`orchestrator`](docs/orchestrator.md) (update + deploy from a
directory tree) or [`configure`](docs/configure.md) (parameters from YAML per environment).

### In a pipeline

One copy of every artifact in the tenant layout (`packages/`, tracked in Git): developers edit
it, `snapshot` brings tenant changes into it as a reviewable diff, `orchestrator` deploys from it.

```bash
cpictl snapshot --dir-git-repo packages --git-skip-commit --dry-run   # new / changed / deleted / local-modified / derived
cpictl snapshot --dir-git-repo packages --git-skip-commit             # write it; local edits are not overwritten
cpictl orchestrator -d packages -c deployments --plan                 # what would be uploaded and deployed, and why
cpictl orchestrator -d packages -c deployments --defer-deploy         # uploads only what changed; never writes into packages/
cpictl configure -c config/dev.yml --defer-deploy                     # writes only changed parameters
cpictl deploy --pending                                               # deploys each artifact once
```

Snapshot output is stable (two runs, empty `git status`), deployment copies of the deploy config
are not written, and reads, uploads and parameter reads run 8 at a time. Details:
[docs/snapshot.md](docs/snapshot.md), [docs/ci.md](docs/ci.md#pipeline-snapshot-update-configure-deploy-once).

## Commands

| Area | Commands |
|------|----------|
| Inspect | `packages`, `artifacts`, `status`, `endpoints`, `resources`, `discover`, `graph`, `drift`, `compare` (tiers, Git refs, trees) |
| Runtime data | `datastore`, `variables`, `jms`, `number-ranges`, `log-files`, `idempotent`, `id-mappings` |
| Testing and monitoring | `send`, `log-level`, `logs`, `logs get`, `logs steps`, `logs trace`, `logs trace-message`, `logs attachment`, `logs payload` |
| Quality | `validate`, `guidelines`, `lint` (reuse, Partner Directory candidates, dead weight, best practices; `--fix`) |
| Designtime | `download`, `iflow copy`, `iflow layout` (tidy the diagram), `packages create`, `update artifact`, `update package`, `version bump` |
| Runtime | `deploy`, `undeploy` |
| Parameters | `params get`, `params set`, `configure`, `configure pull` |
| Many packages | `orchestrator`, `config-generate` |
| Tiers | `matrix` (versions per tier), `compare`, `transport` (deps, check, copy) |
| Development | `mock-tenant` (in-memory tenant with a demo landscape per tier) |
| Git | `sync`, `snapshot`, `snapshot restore` |
| API Management | `sync apiproxy`, `sync apiproduct` |
| Partner Directory | `pd-snapshot`, `pd-deploy` |
| Security | `credentials` (list, set-user, set-oauth2, set-secure-param, apply, delete), `keystore` (list, export-cert, import-cert) |
| AI agents | `mcp`, `skills` (list, show, install) |
| Setup and local | `doctor` (connection and roles), `profile`, `stats` (usage statistics, kept on this machine only) |

All commands and flags: [docs/commands.md](docs/commands.md) (generated from the CLI).

## Output and exit codes

By default cpictl writes human-readable logs to stderr and nothing to stdout (except
commands that download content, such as `resources get`, which print the content).
With `--output json` stdout receives exactly one JSON document and stderr receives JSON log lines:

```json
{
  "command": "deploy",
  "ok": false,
  "exitCode": 7,
  "errorCategory": "partial",
  "error": "1 of 2 artifact(s) failed - Billing: FAILED (designtime artifact Billing does not exist)",
  "schemaVersion": 1,
  "durationMs": 48210,
  "result": {
    "results": [
      {"id": "OrderIntake", "type": "Integration", "taskId": "...", "status": "DEPLOYED", "version": "1.0.2"},
      {"id": "Billing", "type": "Integration", "status": "FAILED", "error": "designtime artifact Billing does not exist"}
    ]
  }
}
```

`errorCategory` names the exit code as MCP tool results do (`usage`, `auth`, `tenant_http`,
`failed`, `timeout`, `partial`, `stopped`, `error`). `schemaVersion` changes only when a field is
removed or changes meaning. Go code calling `pkg/ops` gets the same classes from
`ops.ErrorCategory(err)` and `ops.ExitCode(err)`.

**JSON schemas** of the envelope and of every command's `result` are in
[`schemas/`](schemas/) (JSON Schema 2020-12). Each release attaches them as
`cpictl_<version>_schemas.tar.gz`.
- `schemas/index.json` maps each command to its file (`pd get` is `pd_get.json`), and
  `envelope.json` describes the envelope.
- The schemas are generated from the Go types the commands return, and tests check real outputs
  against them.
- A field that may be missing is not in `required`, and a field that may be `null` says so.
- A removed field, a changed type, or a field that is no longer always present raises
  `schemaVersion`. New fields don't.

Progress for a display (Studio, a CI step) comes as events with `--events <file>` (or `--events -`
for stderr, next to the log lines): one JSON object per line with `schemaVersion`, `seq`, `time`,
`command`, `event` and `data`. `start` opens the stream, `item` reports one artifact as soon as
it is handled (`snapshot`: the snapshot item with `status` and `action`; `orchestrator`: the
artifact with `status` and later `deploy`; `deploy`/`undeploy`/`configure`: the deployment
result), and `end` carries `exitCode`, `errorCategory` (empty for success) and `durationMs`. The result document
stays the final word; events never change it or the exit code.

```text
{"schemaVersion":1,"seq":1,"time":"…","command":"snapshot","event":"start"}
{"schemaVersion":1,"seq":2,"time":"…","command":"snapshot","event":"item","data":{"package":"Billing","artifact":"Billing_In","status":"new","action":"write",…}}
{"schemaVersion":1,"seq":9,"time":"…","command":"snapshot","event":"end","data":{"exitCode":0,"errorCategory":"","durationMs":5123}}
```

Every command ends with the time it took, on stderr (`⏱ deploy failed in 48.2s`, JSON log field
`durationMs`) and in the envelope (`durationMs`). `--help` and `--version` print none.

Tenant reads (GET) answered with `429`, `502`, `503` or `504` are retried up to three times with
backoff 2s, 4s, 8s (`Retry-After` is honoured, at most 30 s); `--read-retries 0` turns it off.
Writes are never retried.

### Checking the setup

`cpictl doctor` checks the configuration (config file or profile, host, authentication, runtime
credentials, git, snapshot state and pending deployments in `.cpi`) and then the tenant: the
connection, and with one small read per API area which areas the credentials can use
(designtime, runtime, message logs, security material, keystore, Partner Directory, data stores,
log files). `403` means the OAuth client or user lacks the role for that area, `404` that the
tenant does not offer the API. Nothing is changed; exit code 3 for authentication problems, 4
without a connection. MCP: the `doctor` tool.

### Usage statistics

Every command and MCP tool call appends one line to `~/.cpictl/stats.jsonl`: name, source
(`cli`/`mcp`), exit code, duration and time. No arguments, hosts, artifact names or results are
recorded, and nothing is sent anywhere. The file is compacted to its newest half at 1 MiB, so it
stays small. `cpictl stats` (`--since 7d`, `--source mcp`) shows runs, failures and median /
p95 / max duration per command; `cpictl stats --reset` deletes the file; `CPICTL_STATS=off`
stops recording (e.g. on CI runners).

Artifact statuses: `DEPLOYED`, `SKIPPED` (same version already running), `UNDEPLOYED`,
`NOT_DEPLOYED`, `FAILED`, `TIMEOUT`.

| Exit code | Meaning | Typical reaction |
|-----------|---------|------------------|
| 0 | OK | |
| 1 | Unexpected internal error | report a bug |
| 2 | Usage or configuration error | fix flags or config |
| 3 | Authentication failed (401/403, OAuth token) | check credentials and roles |
| 4 | Tenant HTTP error or tenant unreachable | retry, check host |
| 5 | Deployment, validation or test message failed on the tenant | fix the artifact |
| 6 | Timeout | check `status`, raise `--deploy-timeout` ([deployments.md](docs/deployments.md)) |
| 7 | Partial failure | inspect the per-item results |
| 8 | Loop stopped (MCP only): a build loop limit was reached | stop, report the loop summary |

## AI agents (MCP)

`cpictl mcp` is a [Model Context Protocol](https://modelcontextprotocol.io) server on stdio. It
uses the same profiles as the CLI, so the MCP configuration only names the profile. Commit this
as `.mcp.json` in the root of the integration content repository
([example](docs/examples/profiles.mcp.json)):

```json
{
  "mcpServers": {
    "cpi-dev": {
      "command": "cpictl",
      "args": ["mcp", "--root", ".", "--profile", "${CPICTL_DEV_PROFILE:-dev}", "--mode", "develop"]
    },
    "cpi-qa": {
      "command": "cpictl",
      "args": ["mcp", "--root", ".", "--profile", "${CPICTL_QA_PROFILE:-qa}", "--read-only"]
    }
  }
}
```

- No credentials in the file: each developer has their own `~/.cpictl/dev.yaml`; whoever named
  the profile differently sets `CPICTL_DEV_PROFILE`.
- A server keeps its profile while it runs; `cpictl profile use` does not affect it.
- `--mode`: `discover` (read-only), `operate` (read + `set_log_level`), `develop` (all tools, no
  Partner Directory full sync), `full` (no restrictions). Details: [docs/mcp.md](docs/mcp.md#modes).
- `--root .` confines local paths of tool calls to the repository. Use the full path of `cpictl`
  if Claude Code does not find it on the `PATH`.
- Without profiles, pass the `CPICTL_*` variables in `env` instead
  ([example](docs/examples/claude-code.mcp.json)); Cursor, OpenCode, Codex, Gemini CLI:
  [docs/agents.md](docs/agents.md); other clients: [docs/examples/mcp.json](docs/examples/mcp.json). Ad hoc in Claude Code:
  `claude mcp add cpi -- cpictl mcp --root . --profile dev`.

Limit what agents may do per server with `--read-only`, `--tools list_*,get_*` or
`--disable-tools undeploy,pd_deploy` ([details](docs/mcp.md#limiting-tools)).
Full example with a read-only QA tenant: [docs/examples/claude-code.mcp.json](docs/examples/claude-code.mcp.json);
for Cursor and OpenCode see [below](#codex-cursor-opencode-gemini-cli-and-other-agents), for
other clients (Claude Desktop, ...): [docs/examples/mcp.json](docs/examples/mcp.json).
The runtime credentials are only needed for `send_test_message`
([configuration.md](docs/configuration.md#runtime-endpoints-test-messages)).

Tools cover the whole loop: `doctor`, `create_package`, `download_artifact`, `drift`,
`upload_artifact` / `upload_artifacts` (`dry_run`), `validate_artifact`, `check_guidelines`, `lint` / `lint_fix`, `layout_iflow`, `compare`, `transport_check`,
`deploy` (`dry_run`: what would be deployed and why), `send_test_message`, `get_runtime_status`,
`list_message_logs`, `get_message_log`, `get_message_steps`, `get_message_attachment`,
`get_message_store_entry`, parameters, resources, `discover_tenant`, `graph_search`, `graph_neighbors`,
`graph_path`, `list_credentials`,
`list_keystore`, key pairs and CSRs, PGP keys, access policies, the audit log, Partner Directory
partners, `undeploy` and `pd_deploy`. Secrets and private keys never pass through the agent.
Every result carries `ok`, an `errorCategory` matching the exit codes, and the structured result.
See [docs/mcp.md](docs/mcp.md).

### What can it do?

Ask the agent: "what can you do with the cpi server?" The MCP tool `help` answers from the
server itself: the tools available in its mode, the skills and their instructions, the CLI
commands, and which tools and skill fit tasks such as "create a new flow from a template" or
"find who calls this flow" ([details](docs/mcp.md#finding-your-way)). Without an agent:
`cpictl --help`, `cpictl skills list`, [docs/commands.md](docs/commands.md).

### Claude Code plugin

The repository is also a Claude Code plugin marketplace. The plugin `cpi` adds the MCP server,
skills to discover your tenant's conventions, to plan, build, test and review flows and to improve
existing ones (`cpi-improve`: script collections, Partner Directory, dead steps), to operate a
tenant (`cpi-operate`: failed messages, business keys, JMS queues, locks, certificates), and a
read-only reviewer agent:

```text
/plugin marketplace add cpars-innovation/cpicli
/plugin install cpi@cpicli
```

Then run the `cpi-discover` skill once per repository. See [docs/plugin.md](docs/plugin.md).

### Codex, Cursor, OpenCode, Gemini CLI and other agents

The MCP server works in any MCP client, and the skills are plain `SKILL.md` folders. Configure the
server with a profile and copy the skills into the content repository:

| Agent | MCP configuration | Example | Skills |
|-------|-------------------|---------|--------|
| Cursor | `.cursor/mcp.json` (or `~/.cursor/mcp.json`) | [cursor-mcp.json](docs/examples/agents/cursor-mcp.json), [with env](docs/examples/agents/cursor-mcp-env.json) | `cpictl skills install --agent cursor` |
| OpenCode | `opencode.json` (or `~/.config/opencode/opencode.json`) | [opencode.json](docs/examples/agents/opencode.json), [with env](docs/examples/agents/opencode-env.json) | `cpictl skills install --agent opencode` |
| Codex | `.codex/config.toml` | [codex-config.toml](docs/examples/agents/codex-config.toml) | `cpictl skills install --agent codex` |
| Gemini CLI | `.gemini/settings.json` | [gemini-settings.json](docs/examples/agents/gemini-settings.json) | `cpictl skills install --agent gemini` |

Cursor (`.cursor/mcp.json`):

```json
{
  "mcpServers": {
    "cpi-dev": {
      "type": "stdio",
      "command": "cpictl",
      "args": ["mcp", "--root", "${workspaceFolder}", "--profile", "dev", "--mode", "develop"]
    }
  }
}
```

OpenCode (`opencode.json` in the repository root):

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "cpi-dev": {
      "type": "local",
      "command": ["cpictl", "mcp", "--root", ".", "--profile", "dev", "--mode", "develop"],
      "enabled": true,
      "timeout": 15000
    }
  }
}
```

Then ask the agent to *"use the doctor tool of cpi-dev"* to check the connection. All four read
`AGENTS.md` ([example](docs/examples/agents/AGENTS.md)) for project rules. Setup per agent (read-only
QA server, environment variables instead of profiles, tool approval) and what differs from the
Claude Code plugin: [docs/agents.md](docs/agents.md).

## Documentation

| | |
|---|---|
| [Configuration](docs/configuration.md) | Connection, config file, environment variables, OAuth client, [all defaults](docs/configuration.md#defaults) |
| [Command reference](docs/commands.md) | All commands and flags |
| [MCP server](docs/mcp.md) | Agent setup, tools, result format, safety |
| [Claude Code plugin](docs/plugin.md) | Skills, reviewer agent, tenant conventions |
| [Other agents](docs/agents.md) | Cursor, OpenCode, Codex, Gemini CLI: MCP setup, skills, AGENTS.md |
| [Orchestrator](docs/orchestrator.md) | Update + deploy many packages: `--plan`, comparison with the snapshot, `--defer-deploy`, `--parallel`, `config-generate` |
| [Snapshot](docs/snapshot.md) | Tenant into the repository: `--dry-run`, local edits protected, deployment copies skipped, stable output, `--prune`, `--incremental`, `--parallel` |
| [Versioning](docs/versioning.md) | Versions in the repository (`Bundle-Version`), `--versioning manifest\|keep\|tenant-bump`, `version bump --changed` |
| [New flows from templates](docs/new-flows.md) | Templates, briefs, `iflow copy` (what it renames, sender addresses), upload and deploy |
| [Transports](docs/transport.md) | Moving artifacts between tiers: dependencies, pre-checks against the target, copy between trees |
| [Compare](docs/compare.md) | Two tiers, Git refs or content trees per artifact: content, version, parameters, file diffs |
| [Mock tenant](docs/mock-tenant.md) | An in-memory CPI tenant with a demo landscape per tier, for local development, demos and tests |
| [Content graph](docs/graph.md) | `.cpi/graph.json`: which flows call which, shared credentials, scripts, PD parameters |
| [Testing](docs/testing.md) | Test messages by trigger type, test harness, test entries, tracing |
| [Lint and improvements](docs/lint.md) | `lint` rules, `.cpi/lint.yaml`, baseline and CI gate, `--fix` (script collections), [diagram layout](docs/lint.md#diagram-layout) (`iflow layout`), the `cpi-improve` skill |
| [Script comments](docs/script-docs.md) | Comment template for Groovy scripts, how discover, the graph and lint use it, `script-doc` and the agent hook |
| [Backup and disaster recovery](docs/backup.md) | `backup create`, `verify`, `diff`: what is in a backup, secrets and keys, pipelines to S3 and Azure, rotation |
| [Deployments](docs/deployments.md) | How deploy, configure and orchestrator wait (one runtime list, one timeout), migrating pipeline settings, `runtimes watch` events and webhooks |
| [Monitoring and checks](docs/monitoring.md) | Message logs, steps, attachments, payloads, validation, guidelines |
| [Configure](docs/configure.md) | Parameters from YAML (`configure`, `configure pull`) |
| [Security material](docs/security.md) | Credentials, `credentials apply`, keystore and certificate expiry |
| [Partner Directory](docs/partner-directory.md) | `pd-snapshot`, `pd-deploy`, full sync |
| [CI/CD](docs/ci.md) | GitHub Actions, Azure Pipelines, the snapshot -> update -> configure -> deploy-once pipeline, job summary, exit codes |
| [Examples](docs/examples) | Ready-to-copy configuration files |
| [End-to-end test](docs/e2e.md) | Opt-in test of the pipeline on a development tenant, manual checks (drafts, layout) |
| [Coming from FlashPipe](docs/migrating-from-flashpipe.md) | What changed and how to migrate |
| [Go API](docs/go-api.md) | Using cpictl as a Go library: packages, operations, snapshot, content hashing |

## Development

```bash
go test ./...               # offline against an in-memory mock tenant, a few seconds
go test -race ./...
make cover                  # cross-package coverage report
make test-integration       # writes to a REAL tenant, needs CPICTL_* variables
```

```
cmd/cpictl         main package
internal/cmd       CLI commands, output contract
internal/mcp       MCP server and tools
pkg/ops            operations with structured results (shared by CLI and MCP; docs/go-api.md)
pkg/models         deploy config and configure file models
pkg/snapshot       tenant -> folder snapshot, its state, commit
pkg/content        content hashing, normalisation and diffs
pkg/cpi            SAP Cloud Integration OData API client
pkg/httpclnt       HTTP client: OAuth / Basic Auth, CSRF, typed errors
internal/cpitest   mock tenant for tests
```

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache License 2.0, see [LICENSE](LICENSE). cpictl contains code originally developed as
[FlashPipe](https://github.com/engswee/flashpipe) by Eng Swee Yeoh; see [NOTICE](NOTICE).
Third-party licenses: [licenses/](licenses).
