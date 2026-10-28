# MCP server

`cpictl mcp` runs a [Model Context Protocol](https://modelcontextprotocol.io) server on
stdin/stdout so that AI agents can work with a CPI tenant: list content, upload an iFlow,
deploy it, read the runtime error, fix, repeat.

- Transport: stdio, JSON-RPC 2.0, one message per line.
  Protocol versions `2025-06-18`, `2025-03-26`, `2024-11-05`.
- stdout carries only protocol messages. Logs are JSON lines on stderr.
- Tenant settings are the same as for the CLI (flags, `CPICTL_*` environment variables,
  `cpictl.yaml`), see [configuration.md](configuration.md). One server = one tenant.

Restrict a server to part of the tenant with an access scope: `cpictl mcp --scope-file scope.json`, see
[access-scope.md](access-scope.md). Tools the scope can never allow are not offered: a tool whose action the scope
grants on no package (no `deploy` anywhere: no `deploy`, `undeploy`), or a tenant area it does not grant. Tools
it allows on some packages stay, and each call is decided by the scope. In gateway mode the server reads your
scope on the profile's SAP tenant from the gateway (`cpictl auth status`) once at start and filters the same way;
restart the server after a policy change. `help` lists the removed tools with the reason.

## Setup

### Claude Code

```bash
claude mcp add cpi \
  -e CPICTL_TMN_HOST=mytenant.it-cpi018.cfapps.eu10-003.hana.ondemand.com \
  -e CPICTL_OAUTH_HOST=mytenant.authentication.eu10.hana.ondemand.com \
  -e CPICTL_OAUTH_CLIENTID=... \
  -e CPICTL_OAUTH_CLIENTSECRET=... \
  -- /path/to/bin/cpictl mcp --root /path/to/integration-repo
```

### Cursor, OpenCode, Codex, Gemini CLI

Each has its own configuration file; ready-to-copy examples (with profiles or with environment
variables) and the skill setup are in [agents.md](agents.md):
[Cursor](agents.md#cursor) (`.cursor/mcp.json`), [OpenCode](agents.md#opencode) (`opencode.json`),
[Codex](agents.md#codex), [Gemini CLI](agents.md#gemini-cli).

### Other clients (Claude Desktop, ...)

See [examples/mcp.json](examples/mcp.json):

```json
{
  "mcpServers": {
    "cpi-dev": {
      "command": "/path/to/bin/cpictl",
      "args": ["mcp", "--root", "/path/to/integration-repo"],
      "env": {
        "CPICTL_TMN_HOST": "mytenant.it-cpi018.cfapps.eu10-003.hana.ondemand.com",
        "CPICTL_OAUTH_HOST": "mytenant.authentication.eu10.hana.ondemand.com",
        "CPICTL_OAUTH_CLIENTID": "...",
        "CPICTL_OAUTH_CLIENTSECRET": "..."
      }
    }
  }
}
```

### Project file for Claude Code (`.mcp.json`)

To share the server setup with the team, commit a `.mcp.json` to the root of the integration
content repository, see [examples/claude-code.mcp.json](examples/claude-code.mcp.json). It
holds no secrets: Claude Code expands `${VAR}` (and `${VAR:-default}`) from each developer's
environment, so everyone exports their own `CPI_DEV_*` / `CPI_QA_*` variables (shell profile,
direnv, a secret manager):

```json
{
  "mcpServers": {
    "cpi-dev": {
      "command": "${CPICTL_BIN:-cpictl}",
      "args": ["mcp", "--root", "."],
      "env": {
        "CPICTL_TMN_HOST": "${CPI_DEV_TMN_HOST}",
        "CPICTL_OAUTH_HOST": "${CPI_DEV_OAUTH_HOST}",
        "CPICTL_OAUTH_CLIENTID": "${CPI_DEV_OAUTH_CLIENTID}",
        "CPICTL_OAUTH_CLIENTSECRET": "${CPI_DEV_OAUTH_CLIENTSECRET}",
        "CPICTL_RUNTIME_OAUTH_CLIENTID": "${CPI_DEV_RUNTIME_OAUTH_CLIENTID}",
        "CPICTL_RUNTIME_OAUTH_CLIENTSECRET": "${CPI_DEV_RUNTIME_OAUTH_CLIENTSECRET}"
      }
    }
  }
}
```

With [profiles](configuration.md#profiles-switching-tenants) the entries are even shorter:
`"args": ["mcp", "--root", ".", "--profile", "dev"]`, no `env` needed.

The example also has a read-only `cpi-qa` server; Claude Code asks each developer once to approve
project MCP servers. `--root .` is the repository root (the server's working directory).
If you use the [Claude Code plugin](plugin.md), it already starts a server named `cpi`
from the plain `CPICTL_*` variables; use a project file for additional tenants.

Use one server entry per tenant (e.g. `cpi-dev`, `cpi-qa`). Point agents at a development
tenant and give other tenants a read-only server ([Limiting tools](#limiting-tools)).

For `send_test_message` add the runtime credentials (`CPICTL_RUNTIME_OAUTH_CLIENTID`,
`CPICTL_RUNTIME_OAUTH_CLIENTSECRET`), see
[configuration.md](configuration.md#runtime-endpoints-test-messages).

For Claude Code there is also a plugin with skills and a reviewer agent on top of this
server: [plugin.md](plugin.md).

### Server flags

| Flag | Default | Description |
|------|---------|-------------|
| `--root` | `.` | Local paths in tool arguments are resolved against this directory and may not leave it (symlinks are resolved) |
| `--poll-interval` | `10` | Default seconds between deploy/undeploy status checks |
| `--max-checks` | `30` | Default maximum number of status checks per artifact |
| `--cache-ttl` | `60` | Seconds `list_packages` / `list_artifacts` results are reused (`0`: no cache). Every tool that changes the tenant clears the cache; results from the cache have `cached: true` |
| `--full-results` | `false` | Results as the CLI prints them: no lean fields, empty values and columns, see [Results](#results) |
| `--verbose-results` | `false` | Every result in the envelope `{ok, exitCode, durationMs, result}` |
| `--structured-content` | `false` | Also send each result as `structuredContent` (verbose envelope) |
| `--all-tools` | `false` | List every tool from the start instead of the core tools plus the rest on demand, see [Tools on demand](#tools-on-demand) |
| `--read-only` | `false` | Offer only tools that do not change the tenant or trigger processing |
| `--tools` | all | Offer only these tools: names or patterns, e.g. `list_*,get_*,validate_artifact` |
| `--disable-tools` | none | Do not offer these tools (names or patterns); wins over `--tools` |
| `--opt-in` | none | Also offer these opt-in tools (exact names), without limiting the others |
| `--versioning` | not set | Versioning mode of `upload_artifacts` and `deploy` (`manifest`, `keep`, `tenant-bump`), see [versioning.md](versioning.md) |
| `--branch-policy` | not set | Policy file: refuse tenant tools (errorCategory `branch_readonly`) while a repository is on a read-only branch, see [Branch policy](#branch-policy) |
| `--loop-required` | `false` | Refuse tenant tools (errorCategory `loop_required`) while no loop is open, see [Build loops](#build-loops) |
| `--loop-max-iterations`, `--loop-max-deploys`, `--loop-max-same-errors`, `--loop-max-minutes` | `0` (no cap) | Highest `max_iterations`, `max_deploys`, `same_error_limit`, `wall_clock_minutes` that `loop_start` accepts; they also lower the defaults |
| `--mask-pattern` | none | Also mask the values of names matching this regular expression (case-insensitive, repeatable), see [Masking](#masking) |
| `--redact-payloads` | `false` | Replace message bodies in results with a placeholder, size and sha256, see [Masking](#masking) |

Every flag can also be set in `cpictl.yaml` (`loop-required: true`, `loop-max-deploys: 5`,
`mask-pattern: ["^X-Customer", "iban"]`) or as an environment variable (`CPICTL_LOOP_REQUIRED=true`,
`CPICTL_LOOP_MAX_DEPLOYS=5`), like the other flags.

### Modes

`--mode` (or `CPICTL_MODE`) is a preset on top of the other limits; together the most
restrictive wins:

| Mode | Tools |
|------|-------|
| `discover` | read-only (same as `--read-only`) |
| `operate` | read tools, local files, `set_log_level`, `retry_jms_messages`, `move_jms_messages`, `set_jms_queue_state`: monitoring and diagnosis |
| `develop` | all tools; `pd_deploy` refuses `full_sync`, `undeploy` needs `confirm` |
| `full` | all tools without restrictions, including `pd_deploy` with `full_sync` (same as no `--mode`) |

`undeploy`, `delete_data_store_entry`, `unlock_artifact`, `set_value_mapping`, `retry_jms_messages`,
`move_jms_messages` and every delete tool (`delete_artifact`, `delete_package`,
`delete_jms_messages`, `delete_keystore_entry`, `delete_access_policy`, `delete_artifact_reference`,
`delete_alternative_partner`, `delete_authorized_user`) always need `confirm: true`; that is part
of the tool, not of a mode.

`unlock_artifact` and these delete tools (all but `delete_data_store_entry`) are **opt-in**: no mode
offers them (not even `full`), only `--opt-in` or `--tools` naming them exactly. `--opt-in
delete_artifact` adds the tool and keeps all others; `--tools 'list_*,unlock_artifact'` also limits
the server to the listed tools. A pattern such as `unlock_*`, `delete_*` or `*` does not count.
`--disable-tools`, `--read-only` and the mode still apply to an opted-in tool.
Removing a lock discards the unsaved changes of whoever holds it; a deleted artifact or package
cannot be restored; deleted JMS messages are never processed; deleting a keystore entry breaks the
flows that use it; deleting an access policy lifts its restriction.

The mode is added to the server instructions.

### Toolsets

The server has about 110 tools. An agent chooses better among the tools of its task than among all
of them, and every tool description costs context in every call. `--toolset` (or
`CPICTL_TOOLSET`) offers only the tools of one or more tasks; it combines with `--tools`, and the
mode still applies on top (the more restrictive wins). `doctor` and `help` are always there.

| Toolset | For | Tools |
|---------|-----|-------|
| `inspect` | understanding content | list and read packages, artifacts, resources, parameters, runtime status, endpoints, graph, discovery, drift, compare |
| `build` | changing flows on DEV | read tools, `download_artifact`, `copy_iflow`, `layout_iflow`, `create_package`, `upload_artifacts`, `validate_artifact`, `check_guidelines`, `set_parameters`, `deploy`, `bump_versions`, `lint`, loop tools |
| `test` | testing and diagnosing | `list_tests`, `run_tests`, `send_test_message`, `idoc_template`, message logs, `find_messages_by_header`, steps, attachments, store entries, trace tree, `set_log_level`, traced messages, `install_test_tools`, `set_stub_response`, loop tools |
| `monitor` | operations | `message_summary`, message logs and traces, `find_messages_by_header`, runtime status, log files, data stores, variables, JMS queues and messages (`list_jms_messages`, `retry_jms_messages`, `move_jms_messages`, `set_jms_queue_state`), number ranges, idempotency, ID mappings, keystore, `list_audit_logs` |
| `promote` | moving between tiers | `compare`, `transport_check`, `drift`, `config_diff`, parameters, credentials, keystore, Partner Directory dependencies and diff, runtime status |
| `improve` | quality | `lint`, `lint_fix`, `layout_iflow`, graph, `drift`, `compare`, `bump_versions` |
| `partner-directory` | Partner Directory | `list_pd_partners`, `get_pd_parameters`, `pd_diff`, `pd_dependencies`, `pd_deploy`, `config_diff`, alternative partners and authorized users (list, create), `scan_secrets` |
| `security` | security material | `list_credentials`, `list_keystore`, `list_keystore_history`, `get_certificate_chain`, `get_csr`, `get_ssh_public_key`, `generate_key_pair`, `upload_certificate_chain`, `list_pgp_keys`, access policies (list, create, add reference), `list_audit_logs`, `scan_secrets`, `lint` |

`undeploy`, `delete_data_store_entry` and the opt-in tools (`unlock_artifact`, the delete tools
above) are in no toolset: offer them explicitly with `--tools`. `list_locks` is in `inspect` and
`build`; `list_value_mappings` and `get_value_mapping` are in `inspect` and `build`,
`set_value_mapping` in `build`; `list_components` in `build` and `improve`.

```json
"cpi-dev":  { "command": "cpictl", "args": ["mcp", "--root", ".", "--mode", "develop", "--toolset", "build,test"] },
"cpi-prod": { "command": "cpictl", "args": ["mcp", "--root", ".", "--mode", "discover", "--toolset", "monitor,promote"] }
```

### Tools on demand

By default the server lists only the **core tools**: `help`, `doctor`, `list_toolsets`,
`enable_toolset`, `list_packages`, `list_artifacts`, `list_runtime_artifacts`, `message_summary`,
`list_message_logs`, `get_message_log` and `get_trace_tree`. That is about 3,400 tokens per turn
instead of about 26,000 for all 98 tools. Every other tool, including those that change the tenant or
send messages, comes with its toolset: the agent calls `enable_toolset {"toolsets": ["build"]}`, the
server lists the tools and sends `notifications/tools/list_changed`, and the result carries the
toolset's instructions (for `build`: drift before upload, loops, versioning). The mode, filters and
access scope apply as always, and each tool keeps its own approval in the client.

- **Claude Code** (`clientInfo.name` `claude-code`) gets every tool: it loads tool schemas only
  when the model needs one, so a short list would only add a round trip (measured on the mock
  tenant: 5 turns on demand, 3 with every tool to send a test message). Its instructions are the
  general part and an index of the toolsets; it reads a task's part with
  `help {"topic": "instructions", "toolsets": ["build"]}` (about 750 tokens per turn instead of
  2,800).
- **Clients that do not reload the tool list** on `tools/list_changed`: start the server with
  `--all-tools` (or `CPICTL_ALL_TOOLS=true`, `all-tools: true` in `cpictl.yaml`). The symptom is
  that `enable_toolset` reports `addedTools` but the agent cannot call them.
- `--toolset`, `--tools` and `--dynamic-toolsets` choose the tools instead: with one of them the
  server lists what it names.
- The listing is guidance, not an access limit: a tool the server offers can be called before its
  toolset is enabled. Limit access with `--mode`, `--tools`, `--read-only` or an access scope; tools
  they remove are refused on call.

### Dynamic toolsets

With `--dynamic-toolsets` (or `CPICTL_DYNAMIC_TOOLSETS=true`) the server starts with only `help`,
`doctor`, `list_toolsets` and `enable_toolset`. The agent enables the toolsets of its task
(`enable_toolset {"toolsets": ["build", "test"]}`); the server then lists their tools and sends
`notifications/tools/list_changed`, so the client fetches the new list. `--toolset` names the
toolsets enabled at the start. Toolsets are not disabled again during a session.

- Only the tools the server offers after mode and filters can be enabled: on a `--read-only` server,
  enabling `build` adds `lint` but not `deploy`.
- Tools in no toolset (`undeploy`, `delete_data_store_entry`, the opt-in tools, if the server offers them) are in the
  toolset `other`.
- A call to a tool the server offers but has not listed yet still works; the listing is guidance,
  the filters are the limit.
- The server instructions name the toolsets and what they are for, so the agent can enable one
  without calling `list_toolsets` first. They hold the general part and the parts of the toolsets
  enabled at the start; `enable_toolset` returns the parts of the toolsets it enables
  (`instructions`). This keeps the start at about 1,200 tokens instead of 3,700.

A client with its own agent loop can also select tools itself: start the server normally (all
tools listed with their schemas), read the toolsets from `help {"topic": "toolsets"}` (only the
tools the server offers) and pass the model only the tools of the enabled toolsets per step.

Use it with clients that handle `tools/list_changed` (Claude Code does). For clients that do not,
start the server without it: all tools, or a fixed `--toolset`. Clients that load tool schemas on
demand (Claude Code does this when there are many MCP tools) gain little from it; it helps most with
clients that put every tool into every request.

```json
"cpi-dev": { "command": "cpictl", "args": ["mcp", "--root", ".", "--mode", "develop", "--dynamic-toolsets", "--toolset", "inspect"] }
```

### Limiting tools

The server enforces the limits itself, for every MCP client: disabled tools are not listed in
`tools/list`, calls to them are rejected, and the server instructions tell the agent which tools
are unavailable. A call to a disabled tool returns a normal tool result with `isError: true`,
`errorCategory: "usage"` and an error that names the reason (`not offered in mode discover
(read-only)`, `the server is read-only (--read-only) …`, `disabled by --disable-tools`, `not
selected by --tools / --toolset`, `opt-in: offered only when --opt-in or --tools names it exactly …`); only a
name that is no cpictl tool at all is a JSON-RPC error (-32602). A pattern that matches no tool stops the server with exit code 2, so a typo never
leaves a tool enabled by accident.

| Tool class | Tools | `--read-only` |
|------------|-------|---------------|
| read | list_\*, get_\*, `validate_artifact`, `check_guidelines`, `pd_diff`, `pd_dependencies`, `config_diff`, `drift`, `compare`, `transport_check`, `lint`, `doctor`, `graph_*`, `loop_status`, runtime data tools (`list_tests` reads local case files only) | kept |
| local files (inside `--root`) | `download_artifact`, `copy_iflow`, `bump_versions`, `lint_fix`, `layout_iflow`, `idoc_template`, `discover_tenant`, `loop_start`, `loop_end` | kept |
| tenant changes / processing (classes below) | `create_package`, `upload_artifacts`, `set_parameters`, `deploy`, `undeploy`, `pd_deploy`, `send_test_message`, `run_tests`, `set_log_level`, `delete_data_store_entry`, `unlock_artifact`, `set_value_mapping`, `delete_artifact`, `delete_package`, `retry_jms_messages`, `move_jms_messages`, `delete_jms_messages`, `set_jms_queue_state`, `generate_key_pair`, `upload_certificate_chain`, `delete_keystore_entry`, `create_access_policy`, `add_artifact_reference`, `delete_access_policy`, `delete_artifact_reference`, `create_alternative_partner`, `delete_alternative_partner`, `create_authorized_user`, `delete_authorized_user` | removed |

For approvals, `help` also reports a finer **class** per tool (the effect `read` / `local` /
`tenant` stays the filter criterion):

| Class | Tools |
|-------|-------|
| `read` | every read tool |
| `local` | every local-files tool |
| `write` | designtime content and configuration: `create_package`, `upload_artifacts`, `set_parameters`, `set_value_mapping`, `generate_key_pair`, `upload_certificate_chain`, `create_access_policy`, `add_artifact_reference`, `create_alternative_partner`, `create_authorized_user` |
| `deploy` | `deploy`, `undeploy`, `pd_deploy` |
| `process` | triggers processing: `send_test_message`, `run_tests`, `retry_jms_messages`, `move_jms_messages`, `set_jms_queue_state` |
| `log` | `set_log_level` |
| `destructive` | every `delete_*` tool and `unlock_artifact` |

```json
"cpi-qa":   { "command": "cpictl", "args": ["mcp", "--root", ".", "--read-only"] },
"cpi-dev":  { "command": "cpictl", "args": ["mcp", "--root", ".", "--disable-tools", "undeploy,pd_deploy"] },
"cpi-logs": { "command": "cpictl", "args": ["mcp", "--tools", "list_message_logs,get_message_*,list_runtime_artifacts"] }
```

The settings can also come from the environment (`CPICTL_READ_ONLY=true`,
`CPICTL_TOOLS`, `CPICTL_DISABLE_TOOLS`, comma-separated) or `cpictl.yaml`. Server-side limits
complement the client's own permission rules (in Claude Code, `permissions.deny` entries such
as `mcp__cpi-dev__undeploy` in `.claude/settings.json`), which still prompt or block per call.
The strongest limit is the tenant's: give the OAuth client of a QA or production server only
read roles.

### Branch policy

Optional and off by default. With `--branch-policy <file>` the server refuses the tools that
change the tenant (errorCategory `branch_readonly`, exit code usage) while a repository is on a
protected branch, so an agent changes the tenant from a feature branch, never from `main`. Read
and local tools keep working. The branches are read on every such call (`git rev-parse`), so a
checkout during the session counts.

```yaml
# .cpi/branch-policy.yaml (JSON works too)
default: readwrite            # readonly: always read-only
readonlyBranches: [main, "release/*"]   # '*' matches any characters, also '/'
repos: [".", integration-test]          # relative to --root; default ["."]
writableEnvironments: [dev]             # tiers (--tier) that may be changed at all
```

- The root checkout (`.`) on a detached HEAD, or whose branch cannot be read (no git), is
  read-only. Other repos on a detached HEAD (submodules at their pinned commit) and missing ones
  are not judged.
- `writableEnvironments` is checked only when the server has a `--tier`; a server for another tier
  is read-only.
- The server instructions and `help {"topic": "server"}` (`branchPolicy`) say whether tenant
  changes are allowed now and why not. File edits by the agent are the client's business (for
  example a Claude Code `PreToolUse` hook).

## Tools

**Removed tools.** Seven tools were covered by another tool of the same kind (read or tenant
change) in v0.25.0 and removed in v0.27.0. A call to an old name fails with `errorCategory`
`usage` and names the replacement:

| Removed | Use instead |
|---|---|
| `get_message_steps` | `get_message_log` with `include_steps` |
| `get_message_attachment`, `get_message_store_entry` | `get_message_content` with `attachment_id` or `store_entry_id` |
| `get_trace_message` | `get_message_trace` with `trace_id` |
| `get_runtime_status` | `list_runtime_artifacts` with `artifact_ids` |
| `upload_artifact` | `upload_artifacts` (one or several) |
| `get_variable` | `list_variables` with `name` |

| Tool | Changes | Purpose |
|------|---------|---------|
| `help` | | What this server offers: tools after mode and filters, the cpi skills (with their instructions), the CLI commands, and which tools and skill fit common tasks. `topic` for one tool, skill, skill file or command; `topic: "instructions"` with `toolsets` for the server instructions of those toolsets. Always available. See [Finding your way](#finding-your-way) |
| `doctor` | | Connection and roles: per API area ok / forbidden (403, missing role) / missing (404), and which tools need it. Same as `cpictl doctor` |
| `list_packages` | | All integration packages (cached for `--cache-ttl`; `refresh: true` re-reads) |
| `create_package` | designtime | Create a package if it does not exist (`CREATED` / `EXISTS`, never changes one) |
| `list_artifacts` | | Designtime artifacts of a package (all four types; cached like `list_packages`) |
| `changed_since` | `since`, `fingerprint`, `packages` | Whether designtime artifacts changed after a time, and which, newest first (all packages listed in parallel). The `fingerprint` of an earlier call also shows deletions |
| `list_resources` | | Scripts, mappings, schemas, ... of an integration flow |
| `get_resource` | | Content of one resource (text inline, binary base64) |
| `download_artifact` | local files | Extract an artifact into a directory inside `--root` (empty unless `overwrite`) |
| `copy_iflow` | local files | Copy a flow (tenant or local) under a new ID, name, description and sender addresses; refuses unchanged sender addresses unless `keep_addresses`. See [new-flows.md](new-flows.md) |
| `bump_versions` | local files | Raise `Bundle-Version` of changed artifacts (`changed: true`: since the Git commit that last set it) before a pull request. See [versioning.md](versioning.md) |
| `upload_artifacts` | designtime | Create or update one or several artifacts from local directories, 8 at a time; per artifact `CREATED`, `UPDATED` or `UNCHANGED`; `dry_run`: only compare and report the action and version |
| `validate_artifact` | | Tenant check of an integration flow (like *Check* in the Web UI); `PASSED` / `FAILED` with details |
| `check_guidelines` | | Run the activated design guidelines and wait; violations with violated components |
| `get_parameters` | | Externalised parameters of an integration flow; `artifact_ids` for several flows in one call (8 at a time) |
| `set_parameters` | designtime | Change parameters; only changed values are written, unknown keys fail first; `dry_run` |
| `deploy` | runtime | Deploy and wait (`artifact_type`: Integration, MessageMapping, ScriptCollection, ValueMapping); per artifact `DEPLOYED`, `SKIPPED`, `FAILED` (tenant error), `TIMEOUT`, with `designtimeVersion` / `runtimeVersion`. Refuses a designtime version older than the running one unless the designtime artifact was changed after that deployment or `allow_downgrade`; `rule` says which rule decided. `dry_run`: per artifact `deploy` true/false and the reason, nothing triggered |
| `send_test_message` | **triggers processing** | Send a message to the flow's endpoint (or via the test harness to a ProcessDirect address); HTTP status, response, message GUID; `wait_seconds` returns the final message log. `idoc`: IDoc XML to the flow's IDoc sender (must hold an EDI_DC40 control record; `idocs` in the result lists DOCNUM, IDOCTYP, MESTYP, SNDPRN, RCVPRN). `odata_method`, `entity_set`, `key`, `query`: an OData V2 request to the flow's OData sender service. See [testing.md](testing.md) |
| `list_tests` | | Test cases in `.cpi/tests/<FlowId>/<case>.yaml` per flow: trigger, tags, skip reasons, the keys of each flow's `config.yaml`; strict (unknown keys and invalid YAML are `usage` with file:line). `dir` (default `.cpi/tests`, inside `--root`), `flow`, `tags`. Local |
| `run_tests` | **triggers processing** | Run the test cases against the deployed flows and check every expectation: `passed`, `failed`, `errors`, `skipped`, per case the failures (expected, actual) and, for a failed case, diagnostics (message GUID, last error, failing step, `errorInformation`, the message path's root). `flow`, `case`, `tags`, `parallel` (1-8; cases with `stub_calls` run alone), `apply_config` (set the flow's `config.yaml`, redeploy, run, restore and redeploy; keys reported, never values). `errorCategory` `failed` when a case failed. Refused on a production tier unless the server was started with `--allow-production`. See [testing.md](testing.md#test-runs) |
| `run_script_cases` | local | Run script cases on the SAP script API stand-ins (no tenant): `.cpi/tests/<FlowId>/scripts/<script>/<case>.yaml` and `.cpi/tests/collections/<CollectionId>/<case>.yaml`, format [script-case.schema.json](script-case.schema.json) shared with CPI Studio. `dir`, `content`, `flow`, `case`, `tags`. Needs the Gradle build of `cpictl test scripts init` (task `cpictlScriptCases`), a JDK and Gradle. A case in error with `errorCategory` `unsupported` uses a part of SAP's API the stand-ins lack. See [testing.md](testing.md#script-cases) |
| `run_script_cases_online` | tenant (deploy) | The same cases on the tenant's runtime: per script and function a test flow is generated in the test tooling package, deployed, called once per case and deleted (`keep` keeps it). Refused on production tiers. Cases with `call.args`, `pd`, `value_mappings`, `secure`, `keystore`, `log` or `returns` are skipped (local only). `dir`, `content`, `flow`, `case`, `tags`, `keep`. See [testing.md](testing.md#script-cases-on-the-tenant) |
| `install_test_tools` | runtime | Upload the test tooling that ships with cpictl (test harness `CPICTL_Test_Harness`, receiver stub `CPICTL_Test_Stub`) into its package (`package_id`, default `CPICTLTesting`, created when missing); `only` harness or stub; `role` sets the user role a client needs to call them (default: keep the configured one, `ESBMessaging.send` on a first install); `deploy` deploys and waits. Refused on a production tier unless the server runs with `--allow-production`. See [testing.md](testing.md#test-tooling-harness-and-receiver-stub) |
| `set_stub_response` | designtime | The receiver stub's answer for a rule (`status`, `content_type`, `body`, `headers`), stored as Partner Directory string parameter `CPICTL_Stub/<rule>`; `clear` removes it (the stub then answers 200 `{}`). Refused on a production tier like `install_test_tools` |
| `idoc_template` | local files | IDoc XML skeleton for `send_test_message idoc`: EDI_DC40 with the standard fields (`idoc_type`, `message_type`, partners, ports, `docnum`) and a placeholder for the data segments, which you fill in; `path` also writes it inside `--root` |
| `list_runtime_artifacts` | | All deployed artifacts, filter by status (e.g. `ERROR`); `artifact_ids`: the state of those artifacts, deployed or not, with the tenant's error |
| `list_service_endpoints` | | Callable URLs of deployed integration flows (where to send test messages) |
| `list_message_logs` | | Message processing logs by artifact or package, status, time, IDs; error texts; `wait_seconds` for final status; `custom_header {name, value}` (as `find_messages_by_header`) |
| `find_messages_by_header` | | Messages whose custom header property has a value (e.g. an order number), tenant-wide in one call; narrowed by flow, package, time, status. Tenants without the search are scanned (`scanned`/`truncated`) |
| `message_summary` | | Volume and failures over a window: per flow (count per status, failures, durations), per connection between flows (predecessor links, or graph connections by correlation ID), failures grouped by error fingerprint with sample, count, first / last and newest GUID |
| `get_trace_tree` | | Path across flows from a trace ID or a message GUID (correlation ID, optionally joined by key custom headers): tree, hops, `pathKey`, `firstFailure` |
| `get_message_log` | | One message: error text, `errorInformation` (`lastErrorModelStepId`), custom header properties, adapter attributes, attachments, persisted messages; `include_steps` adds the processing steps and the first failing step (`step_properties` each step's properties) |
| `get_message_content` | | Content of a log attachment (`attachment_id`), of a message a Persist step wrote (`store_entry_id`), or the stored response of a `send_test_message` (`response_of`); text inline, binary base64, see [Results](#results) |
| `get_content` | local | Stored content by reference (`ref` from a result): a part from `offset`, or the whole into `save_to` |
| `set_log_level` | runtime setting | NONE / INFO / DEBUG / TRACE for a deployed flow; the server sets it back to INFO after `revert_after_minutes` (default 10 for TRACE and DEBUG) and on shutdown; result `revertsAt` |
| `get_message_trace` | | Traced steps of a message with trace IDs; with `trace_id` the payload, headers and exchange properties at that step (sensitive values masked); `status: "missing_role"` (ok) when the key may not read message content |
| `list_data_stores`, `list_data_store_entries`, `get_data_store_entry` | | Data stores, their entries (metadata) and one entry's content |
| `delete_data_store_entry` | tenant, **destructive** | Delete one entry; requires `confirm: true` |
| `list_locks` | | Artifacts open for editing in the Web UI: artifact, package, who holds the lock, since when |
| `list_runtimes` | `artifacts`, `runtime` | The tenant's runtimes (Cloud Integration, integration cells) and what is deployed on each. The public API lists only Cloud Integration: a flow bound to an integration cell looks undeployed everywhere else |
| `list_integration_connections` | | Sender host -> flows -> receiver host, as the tenant computes it from the deployed flows (`refresh` recomputes) |
| `get_tenant_settings` | | External logging and archiving on/off, last archiving runs |
| `scan_secrets` | | Passwords, tokens and keys in the Partner Directory (strings, binaries, ZIP entries) and in flow configuration parameters; `only`, `packages`. Never returns a value: kind, reason, fingerprint |
| `download_log_archive` | local file | Zip of the latest `http` or `trace` log files, below the server root |
| `list_components` | | Adapter and step variants (cmdVariantUri, version) the tenant offers, from `.cpi/components.json` ([components.md](components.md)); filter by name, kind, direction |
| `unlock_artifact` | tenant, **destructive**, opt-in | Remove an artifact's edit lock (the holder's unsaved changes are lost); requires `confirm: true` and `--opt-in unlock_artifact` |
| `list_value_mappings` | | Value mapping artifacts: ID, version, package |
| `get_value_mapping` | | Schemas of a value mapping (source agency/identifier -> target agency/identifier) with value pairs and default values; filter by agency or identifier |
| `set_value_mapping` | designtime | Add a value pair to an existing schema or change the target value of a source value (`added`, `changed` with `previous`, `unchanged`); deploy with `artifact_type: ValueMapping` to use it; requires `confirm: true` |
| `delete_artifact` | designtime, **destructive**, opt-in | Delete a designtime artifact of any type (type found on the tenant unless `artifact_type`); a deployed one is refused unless `undeploy_first`; requires `confirm: true` and `--opt-in delete_artifact` |
| `delete_package` | designtime, **destructive**, opt-in | Delete a package; refused while it has artifacts unless `with_artifacts`, and while they are deployed unless `undeploy_first`; requires `confirm: true` and `--opt-in delete_package` |
| `list_variables` | | Global and flow variables; `name` reads one variable's value |
| `list_jms_queues`, `get_jms_broker` | | Queues with message counts and the flows that use them; broker capacity and usage |
| `list_jms_messages` | | Messages of a queue: JMS message ID, failed, retry count, next retry, message GUID of the last attempt |
| `retry_jms_messages` | tenant | Process messages of a queue again now (ids or `all`); requires `confirm: true` |
| `move_jms_messages` | tenant | Move messages to another queue (e.g. error queue back to the processing queue); requires `confirm: true` |
| `delete_jms_messages` | tenant, **destructive**, opt-in | Delete messages from a queue; requires `confirm: true` and `--opt-in delete_jms_messages` |
| `set_jms_queue_state` | tenant | Deactivate a queue (messages are kept, not consumed) or activate it |
| `list_number_ranges` | | Number range objects and current values |
| `list_log_files`, `get_log_file` | | System / HTTP log files and the end of one (adapter errors without a message log) |
| `list_idempotent_entries`, `list_id_mappings` | | Entries ignored as duplicates; ID mapper entries |
| `list_credentials` | | User credentials, OAuth2 client credentials, OAuth2 authorization code credentials, secure parameters: names and metadata, never secrets or refresh tokens |
| `list_keystore` | | Keystore entries with validity and days left; `expiring_within_days` flags soon-expiring ones |
| `list_keystore_history` | | Former versions of keystore entries (replaced by renewals or updates) |
| `get_certificate_chain` | | Certificate chain of a key pair (self-signed or CA-signed?) |
| `get_csr` | | Certificate signing request (PKCS#10, PEM) of a key pair, for a CA |
| `get_ssh_public_key` | | Public key of an entry in OpenSSH format (for an SFTP server's `authorized_keys`) |
| `generate_key_pair` | keystore | The tenant generates a key pair (self-signed; RSA 2048, one year by default); `update` replaces an alias |
| `upload_certificate_chain` | keystore | Replace a key pair's certificate with the CA-signed chain (PEM; private keys refused) |
| `delete_keystore_entry` | keystore, **destructive**, opt-in | Delete an entry of the system keystore (not SAP-owned ones); requires `confirm: true` and `--opt-in delete_keystore_entry` |
| `list_pgp_keys` | | PGP public and secret keys: key ID, user IDs, sub keys, validity (never key material) |
| `list_access_policies` | | Access policies (role) with their artifact references |
| `create_access_policy`, `add_artifact_reference` | access policies | Create a policy for a role (optionally with references); add a reference (type, attribute, `EXACT_MATCH` / `REGULAR_EXPRESSION`) |
| `delete_access_policy`, `delete_artifact_reference` | access policies, **destructive**, opt-in | Delete a policy (with its references) or one reference; requires `confirm: true` and `--opt-in` naming the tool |
| `list_audit_logs` | | Audit log, newest first (`since`, `top`, `user`, `object`); `status: "missing_role"` (ok) when the key may not read it (HTTP 403) |
| `lint` | | Local flows: findings per rule (reuse, Partner Directory candidates, dead weight, simplify, robustness, performance, configuration, hygiene) with suggestions; `.cpi/lint.yaml`, baseline. See [lint.md](lint.md) |
| `lint_fix` | local files | Apply the mechanical fixes: scripts into script collections, unused scripts deleted, unconnected steps removed; `dry_run` |
| `layout_iflow` | local files | Lay out the diagram of `.iflw` files: flow order left to right, branches one below the other, no overlaps, right-angled lines; only the diagram changes. `mode` tidy / full, `check`, `dry_run`. See [lint.md](lint.md#diagram-layout) |
| `compare` | | Two sides per artifact (`tenant`, a content tree, `git:<ref>[:<path>]`): same / only_a / only_b / content_differs / version_differs / parameters_differ, files with `diff`, per element and per property diff of changed .iflw models with `model` (`files[].model`: steps, adapters, sequence flows, flow properties, moved shapes; sensitive values masked), parameter keys (values with `show_values`), designtime / running version, draft, last changed by. See [compare.md](compare.md) |
| `transport_check` | | Before moving artifacts to this tenant: their dependencies (script collections, mappings, called flows, credentials, Partner Directory) and pre-checks (draft, outside changes with `target_dir`, dependencies present, credentials, PD parameters, parameter values in the target's `configure` file), pass / warn / fail / skip. See [transport.md](transport.md) |
| `drift` | | Local artifacts vs tenant: in_sync / tenant_newer / local_newer / diverged / not_on_tenant, runtimeOutdated |
| `discover_tenant` | local file | Inventory of packages and flows (adapters, steps, error handling, scripts, naming) to `.cpi/discovery.json`, and the content graph to `.cpi/graph.json`; `local_dir` for a local repository |
| `graph_search` | | Find flows, endpoints, systems, credentials, scripts, headers, Partner Directory parameters, OData entities and IDoc types in `.cpi/graph.json` (local file). See [graph.md](graph.md) |
| `graph_neighbors` | | What a node is connected to: callers and callees of a flow (`sends_to`), users of a credential, script, header or PD parameter, readers and writers of an OData entity (`reads_odata`, `writes_odata`), flows receiving or sending an IDoc type (`receives_idoc`, `sends_idoc`); `direction`, `edge_types`, `depth` |
| `graph_path` | | Shortest connection between two nodes (default: how messages move, through ProcessDirect/JMS addresses) |
| `undeploy` | runtime, **destructive** | Remove from runtime and wait; requires `confirm: true` |
| `list_pd_partners` | | Partner IDs with the number of string and binary parameters, binary content types, alternative partners, authorized users; values are not read (`sizes: true` reads the binaries for their total size) |
| `list_alternative_partners`, `list_authorized_users` | | Alternative partners (agency / scheme / ID -> PID) and authorized users (user -> PID) |
| `create_alternative_partner`, `create_authorized_user` | Partner Directory | Add an alternative partner or an authorized user |
| `delete_alternative_partner`, `delete_authorized_user` | Partner Directory, **destructive**, opt-in | Remove one; requires `confirm: true` and `--opt-in` naming the tool |
| `get_pd_parameters` | | Partner Directory parameters of one PID (binaries: content type, size, sha256; content with `include_content`) |
| `pd_diff` | | Local Partner Directory files vs tenant: create / update / unchanged / remote_only per parameter |
| `pd_dependencies` | | Which local flows reference which Partner Directory parameters; dynamic references; unknown PIDs |
| `config_diff` | | Configure YAML vs tenant parameters: update / unchanged / unknown_key |
| `pd_deploy` | Partner Directory, **destructive with full_sync** | Upload Partner Directory parameters; dry run unless `dry_run: false`; `keys: ["PID:ID"]` changes only those parameters |

### Results

A tool call returns the result alone as JSON text. An error returns
`{"errorCategory", "error", "exitCode"}` (and `result` when there is a partial one) with MCP's
`isError` set. Before it is sent, the result goes through one view step. The CLI's `--json` output,
the Go API and the loop ledger see the result before it, unchanged.

- **Payloads** (message bodies, attachments, persisted messages, trace payloads, data store
  entries, variables, resources) are read whole and stored in the content store
  (`<root>/.cpi/cache/content`, kept out of Git, entries removed after 7 days or beyond 500 MB).
  Above the inline limit the result shows `{ref, size, cut, text | base64}`: the first bytes, and
  in `cut` how to get the rest. `max_bytes` sets the inline limit (default 16 KB, 64 KB for
  `get_resource` and `get_pd_parameters`; at most 1 MB).
- **`get_content {ref, offset, max_bytes, save_to}`** reads stored content by reference: a part
  from `offset` (the result says `next`), or the whole content into a file. The store survives a
  restart of the server.
- **`save_to`** on every tool with `max_bytes` writes the payload to a file under `--root` and
  returns `{size, savedTo, sha256}` instead of the text. A result with several contents writes
  `<name>-2.<ext>`, ... `get_message_content {"response_of": "<messageGuid>"}` returns or saves
  the response of a `send_test_message` without sending it again.
- **`--redact-payloads`:** nothing is stored; `save_to`, `get_content` and `response_of` are
  refused.
- **Lean results** (`detail: "full"` for every field):
  - `list_message_logs`, `find_messages_by_header`: the triage fields of each row;
  - `get_trace_tree`: the error text once (`firstFailure`), the link kind on each node instead of
    a `hops` list;
  - `get_message_log` with steps: the failed step by reference, step errors once;
  - `message_summary`: no sampled occurrences or time buckets, and one error group for a failure
    a flow passes on to the flow it calls (same error text, linked by an edge): reported where it
    failed, with `flows`.
- **Compact:** empty values (`null`, `""`, `[]`, `{}`) are left out; a list of 5 or more objects is
  written as `{"columns": [...], "rows": [[...], ...]}` (a missing value is `null`).
- **`--full-results`** turns lean and compact off for every call (payloads are still stored and
  shown by reference). **`--verbose-results`** returns every result in the envelope
  `{ok, exitCode, durationMs, result}`. **`--structured-content`** also sends that envelope as
  `structuredContent` (models read the text; MCP ties `structuredContent` to an output schema).

**Short descriptions:** `tools/list` sends a summary for the tools with a long description (what
the tool does, what it needs, its side effects, the next step), shorter descriptions for the
parameters of the largest schemas, and none for a parameter whose description only repeats its
name. `help {"topic": "<tool>"}` returns the full description and schema; the server instructions
say so.

A golden test (`TestTokenBudgets`) runs calls against the demo tenant and fails when one grows more
than 10% over its budget (`internal/cmd/testdata/token-budgets.json`).

Every tool has a JSON schema with `additionalProperties: false`: a misspelt argument is an
error, never silently ignored. `tools/list` returns the schemas and MCP annotations
(`readOnlyHint`, `destructiveHint`).

`deploy` always deploys by default (`compare_versions: false`), unlike the CLI. After
`set_parameters` the version does not change, so skipping by version would leave the old
configuration running.

## Finding your way

Agents (and you, through the agent) can ask the server what it can do. `help` is added after
the mode and tool filters, so it reports what is really available on this server:

| Call | Returns |
|------|---------|
| `help` | A short index (about 2,000 tokens): mode, read-only, scope, tool count, the tools per toolset, the workflows (task: tools, skill, tools not offered), the skills with their description, and the topics. The full sections are topics |
| `help {"topic": "server"}` | The server's capabilities in one call: mode, effective `readOnly` (no available tool changes the tenant, whether by `--read-only`, the mode or the filters), disabled tools, every toolset with its available tools, each tool's `effect` and `class` (`toolEffects`) and the members the server does not offer with the reason (`notOffered`, e.g. `opt-in: …` or `not offered in mode discover`), an `other` toolset for available tools in no toolset, the loop policy (`loop.required`, `loop.caps`) and the masking options |
| `help {"topic": "copy_iflow"}` | Description, input schema, effect and class of a tool, or why it is not available here |
| `help {"topic": "cpi-test"}` | The skill's instructions (`SKILL.md`) and its reference files |
| `help {"topic": "cpi-plan/brief-template.md"}` | One reference file of a skill |
| `help {"topic": "iflow copy"}` | Usage, description and flags of a CLI command |
| `help {"topic": "workflows"}` | The workflows in full (task, skill, tools, unavailable tools, docs); likewise `tools` (each with title, `effect`, `class`), `toolsets` (with `notOffered`), `skills` (with files), `agents`, `cli` |
| `help {"topic": "instructions", "toolsets": ["build", "test"]}` | The server instructions for these toolsets only: the general part (help, result format, mode, loop policy) plus the sections of the tools in these toolsets (`other`: tools in no toolset, such as `undeploy`). Without `toolsets`: the active ones (the toolsets with tools after mode and filters; with `--dynamic-toolsets` the enabled ones). `toolsetsSource` says which (`requested`, `active`) |

Typical questions it answers: "what can you do on this tenant?", "which tool finds the flows that
call X?", "how do I test a ProcessDirect flow?". On the command line the same information is in
`cpictl --help`, `cpictl <command> --help`, [commands.md](commands.md) and `cpictl skills list`.

A client that enables only some toolsets (its own tool selection, or `--dynamic-toolsets`) can
replace the server instructions from `initialize`, which describe all tools, with
`help {"topic": "instructions", "toolsets": [...]}` for the enabled toolsets.

### Resources

The server also offers MCP resources (`resources/list`, `resources/read`; the `resources`
capability in `initialize`), for clients that attach documents to the context instead of calling
`help`:

| URI | mimeType | Content |
|-----|----------|---------|
| `cpictl://skills/<name>` | `text/markdown` | A skill's `SKILL.md` (as `help {"topic": "<name>"}`) |
| `cpictl://skills/<name>/<file>` | by extension | A reference file of a skill |
| `cpictl://workflows` | `application/json` | The workflows (task -> skill, tools, docs), as `help {"topic": "workflows"}` |
| `cpictl://loops/<loop id>` | `text/markdown` | The journal `loop_end` wrote to `.cpi/loops/<loop id>.md` under `--root` |

Loop journals are listed as they exist under `--root` when `resources/list` is called. Reads stay
inside `--root`: a loop URI must be a plain loop ID (no `/`, `..` or encoded separators) and is
an invalid-params error otherwise. Unknown URIs are a JSON-RPC error (`-32002`, resource not
found). There are no prompts (`prompts/list` is not implemented) and no resource subscriptions.

### Progress and cancellation

When `tools/call` carries `_meta.progressToken`, long tools send `notifications/progress`
(`progressToken`, `progress`, `total` when known, `message`) while they run, and none after the
result:

| Tool | Progress |
|------|----------|
| `deploy`, `undeploy` | every status check: `artifact Orders: STARTING, check 3/30`; `total` is `max_checks` times the number of artifacts (an upper bound: an artifact that is done early stops counting) |
| `discover_tenant` | each phase (packages, runtime artifacts, artifacts per package) and each analysed flow (`analysed integration flow X (4/20)`); `total` once the flows are known |
| `check_guidelines` | every poll of the execution status; `total` is the highest number of polls within `timeout_seconds` |
| `send_test_message` with `wait_seconds` | every poll for the message processing log |
| `run_tests` | each case when it is final: `Orders_In/valid-order: passed`; `total` is the number of cases |

Without a token nothing is sent. `notifications/cancelled` with the request's ID cancels the
running call (tenant requests and waits end); the call then returns an error result.

## Result format

Every tool call returns the same structure, both as `structuredContent` and as JSON text:

```json
{
  "ok": false,
  "errorCategory": "failed",
  "exitCode": 5,
  "error": "1 of 1 artifact(s) failed - MyIFlow: FAILED (artifact MyIFlow deployment unsuccessful, ended with status ERROR. Error message = ...)",
  "durationMs": 61234,
  "result": {
    "results": [
      {"id": "MyIFlow", "type": "Integration", "taskId": "...", "status": "FAILED", "version": "1.0.3", "error": "..."}
    ]
  }
}
```

`errorCategory` mirrors the CLI exit codes and tells the agent what to do next:

| Category | Exit code | Meaning for the agent |
|----------|-----------|-----------------------|
| `usage` | 2 | Arguments are wrong: fix the call (also returned when `confirm` is missing) |
| `auth` | 3 | Credentials or roles: stop and ask the user |
| `tenant_http` | 4 | Tenant error or unreachable: retry later |
| `failed` | 5 | The tenant rejected the content: read `error`, fix the artifact |
| `timeout` | 6 | Not finished in time: check `list_runtime_artifacts` with `artifact_ids` |
| `partial` | 7 | Some items failed: look at the per-item results |
| `stopped` | 8 | A loop limit was reached: tenant changes are refused; call `loop_end` and report |
| `loop_required` | 2 | The server runs with `--loop-required` and no loop is open: call `loop_start` first |
| `branch_readonly` | 2 | The server runs with `--branch-policy` and a repository is on a read-only branch: switch branches or ask the user |

Tool failures are returned as tool results with `isError: true`, not as JSON-RPC errors,
so the model sees them; so are calls to tools the mode or filters removed (`usage`). JSON-RPC
errors are only used for protocol problems (a name that is no tool, malformed request).

Local paths in results (and in error texts) are relative to `--root`, slash-separated
(`download_artifact`'s directory, `lint` and `layout_iflow` files, `discover_tenant` output,
`download_log_archive` file, `loop_end` summary): absolute paths inside the root are rewritten
for every tool, so the server's own directory never reaches the model.

### Masking

Every tool result passes one post-processor in the server before it is sent, so new tools are
covered without their own code:

- **Name/value lists**: in every object with a string `name` and `value` (custom header
  properties and adapter attributes of `get_message_log` and `send_test_message`'s log, step
  properties of `get_message_log include_steps`, headers and exchange properties of `get_message_trace trace_id`, …)
  the value is replaced by `***` when the name contains
  `authorization`, `cookie`, `password`, `passwd`, `secret`, `token`, `credential`, `apikey` or
  `api-key` (case-insensitive), or matches a `--mask-pattern`.
- **Name/value maps** under `keys`, `headers` or `properties` (the key headers of
  `get_trace_tree`): the same rule by key.
- **Header search**: the `custom header <name> = "<value>"` filter text of
  `find_messages_by_header` gets `"***"` as value when the name is masked.
- **Payloads** (only with `--redact-payloads`): the bodies returned by `send_test_message`
  (response), `get_message_content`, `get_message_trace` with `trace_id`
  (payload), `get_data_store_entry` and `list_variables` with `name` become `{"size": …, "text": "[redacted]",
  "sha256": "<hex>", "redacted": true}`; `size` is the full size, `sha256` is computed over the
  bytes the tool returned (the first `max_bytes` when `truncated`). Resources, scripts and
  Partner Directory values are content, not payloads, and stay readable.

Not masked: error texts (`errorText`; the tenant's message may quote payload fragments), message
IDs, artifact names, the business keys of `list_message_logs` (`applicationMessageId`,
`correlationId`) and configuration parameters (`get_parameters` lists `key`/`value`: secure
parameters name credentials, they do not hold secrets). Masking applies to results only; the loop
ledger and the CLI see the original values.

## Typical agent loop

1. `download_artifact` once to get the iFlow into the local repository (inside `--root`),
   or start from files that are already there. New package: `create_package`.
2. Edit the files; `layout_iflow` after changing an `.iflw` (the server lays out the diagram, the
   agent does not place shapes); `upload_artifacts`; `validate_artifact` (and `check_guidelines`).
3. `deploy`; on `failed`, read `error` (the tenant's runtime error), fix and go back to 2.
4. `run_tests` (the cases in `.cpi/tests/<FlowId>/`, written by the agent from the plan's test
   list): every expectation checked, failures with expected and actual values and diagnostics.
   `send_test_message` with `wait_seconds` (e.g. 60) explores a single message: HTTP status,
   response and the final message log in one call. (For flows without an HTTP sender: trigger them otherwise and use
   `list_message_logs` with `since` and `wait_seconds`.)
5. For a `FAILED` message the log contains `errorText`.
6. `get_message_log` with `include_steps: true` shows the error, the failing step (`modelStepId`,
   as in the `.iflw` BPMN), custom headers, attachments and persisted messages in one call;
   `get_message_content` downloads an attachment or persisted message. Fix and go back to 2.
7. `set_parameters` + `deploy` to change externalised configuration.

Message logs, attachments and persisted messages contain business data. Error texts are
truncated (4 KB in lists, 16 KB in `get_message_log`, flagged with `errorTruncated`) to keep
tool results small. Use a development tenant with test data for agents.

## Build loops

For autonomous build/test/fix loops the server enforces limits the model cannot override:

1. `loop_start {goal, max_iterations=5, same_error_limit=2, wall_clock_minutes=60, max_deploys=15}`
   returns a `loop_id` (one open loop per server).
2. While it is open every tenant-changing call is counted. `max_deploys` counts `deploy` and
   `pd_deploy` calls that are not dry runs (`deploy` with `dry_run: true` and `pd_deploy` without
   `dry_run: false` are free). An **iteration** is such a deploy after a failed test result
   (`send_test_message`, `run_tests` with a failed case, `get_trace_tree` with `firstFailure`,
   `list_message_logs` with a FAILED or ESCALATED message). A `run_tests` call counts each
   distinct failure of its cases once, like a `send_test_message` per failure; its flows count as
   sent to in the loop. The redeployments of `apply_config` are not counted as deploys. Failures are fingerprinted (artifact, step, error text without IDs,
   timestamps and long numbers). Only failures that **belong to the loop** count: the message
   started or ended after `loop_start`, or its artifact was uploaded, deployed or sent to in the
   loop (dry runs touch nothing). Reading older failures of other flows while diagnosing does
   not stop the loop.
3. When a limit is reached (iterations, the same fingerprint `same_error_limit` times, wall clock,
   deploys), every tenant-changing tool returns `ok: false`, `errorCategory: "stopped"`, exit code
   8. Read tools keep working.
4. `loop_status` shows the counters; `loop_end {loop_id, outcome}` always works and writes
   `.cpi/loops/<loop_id>.md` (goal, outcome, counters, every call). Each call is also appended to
   `.cpi/loops/<loop_id>.jsonl` as it happens.

Without server flags the loop is the agent's choice and its limits are its arguments (within the
schema ranges). The server's **loop policy** takes that out of the model's hands:

- `--loop-required`: every tenant tool (effect `tenant`: write, deploy, process, log,
  destructive) is refused while no loop is open, with `ok: false`, `errorCategory:
  "loop_required"`, exit code 2 and an error that says to call `loop_start`. Nothing reaches the
  tenant. Read and local tools work without a loop; after `loop_end` tenant tools are refused
  again.
- `--loop-max-iterations`, `--loop-max-deploys`, `--loop-max-same-errors`,
  `--loop-max-minutes`: caps for `max_iterations`, `max_deploys`, `same_error_limit`,
  `wall_clock_minutes`. A higher value in `loop_start` is refused (`usage`, e.g. `max_deploys 20
  exceeds this server's cap of 5 (--loop-max-deploys); use at most 5`); an omitted limit gets the
  default lowered to the cap. The `loop_start` input schema shows the caps as `maximum`, the server
  instructions and `help {"topic": "server"}` (`loop`) state them.

```json
"cpi-dev": { "command": "cpictl", "args": ["mcp", "--root", ".", "--mode", "develop",
             "--loop-required", "--loop-max-deploys", "10", "--loop-max-minutes", "45"] }
```

## Safety

- `undeploy` refuses to run without `confirm: true`.
- `delete_artifact` and `delete_package` are offered only when `--tools` names them, need
  `confirm: true`, and refuse deployed artifacts unless `undeploy_first`.
- `pd_deploy` is a dry run unless `dry_run: false`; full sync never deletes the parameters
  of a partner whose local files could not be read.
- Local paths cannot escape `--root`.
- Long-running calls are cancelled on `notifications/cancelled` and when the server stops.
- Credentials are never part of tool results or logs. No tool takes or returns a secret or a
  private key: credentials, SSH key import and PGP keyrings are CLI commands (`cpictl
  credentials`, `keystore generate-ssh-key`, `pgp import-public`, see [security.md](security.md)).
  `generate_key_pair` has the tenant create the key; only certificates, CSRs and public keys pass
  through the model.
