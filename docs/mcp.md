# MCP server

`cpictl mcp` runs a [Model Context Protocol](https://modelcontextprotocol.io) server on
stdin/stdout so that AI agents can work with a CPI tenant: list content, upload an iFlow,
deploy it, read the runtime error, fix, repeat.

- Transport: stdio, JSON-RPC 2.0, one message per line.
  Protocol versions `2025-06-18`, `2025-03-26`, `2024-11-05`.
- stdout carries only protocol messages. Logs are JSON lines on stderr.
- Tenant settings are the same as for the CLI (flags, `CPICTL_*` environment variables,
  `cpictl.yaml`), see [configuration.md](configuration.md). One server = one tenant.

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
| `--read-only` | `false` | Offer only tools that do not change the tenant or trigger processing |
| `--tools` | all | Offer only these tools: names or patterns, e.g. `list_*,get_*,validate_artifact` |
| `--disable-tools` | none | Do not offer these tools (names or patterns); wins over `--tools` |
| `--versioning` | not set | Versioning mode of `upload_artifact` and `deploy` (`manifest`, `keep`, `tenant-bump`), see [versioning.md](versioning.md) |

### Modes

`--mode` (or `CPICTL_MODE`) is a preset on top of the other limits; together the most
restrictive wins:

| Mode | Tools |
|------|-------|
| `discover` | read-only (same as `--read-only`) |
| `operate` | read tools, local files and `set_log_level`: monitoring and diagnosis |
| `develop` | all tools; `pd_deploy` refuses `full_sync`, `undeploy` needs `confirm` |
| `full` | all tools without restrictions, including `pd_deploy` with `full_sync` (same as no `--mode`) |

`undeploy`, `delete_data_store_entry` and `unlock_artifact` always need `confirm: true`; that is
part of the tool, not of a mode.

`unlock_artifact` is **opt-in**: no mode offers it (not even `full`), only `--tools` naming it
exactly (`--tools 'list_*,unlock_artifact'`; a pattern such as `unlock_*` or `*` does not count).
Removing a lock discards the unsaved changes of whoever holds it.

The mode is added to the server instructions.

### Toolsets

The server has about 65 tools. An agent chooses better among the tools of its task than among all
of them, and every tool description costs context in every call. `--toolset` (or
`CPICTL_TOOLSET`) offers only the tools of one or more tasks; it combines with `--tools`, and the
mode still applies on top (the more restrictive wins). `doctor` and `help` are always there.

| Toolset | For | Tools |
|---------|-----|-------|
| `inspect` | understanding content | list and read packages, artifacts, resources, parameters, runtime status, endpoints, graph, discovery, drift, compare |
| `build` | changing flows on DEV | read tools, `download_artifact`, `copy_iflow`, `layout_iflow`, `create_package`, `upload_artifact(s)`, `validate_artifact`, `check_guidelines`, `set_parameters`, `deploy`, `bump_versions`, `lint`, loop tools |
| `test` | testing and diagnosing | `send_test_message`, message logs, steps, attachments, store entries, trace tree, `set_log_level`, traced messages, loop tools |
| `monitor` | operations | `message_summary`, message logs and traces, runtime status, log files, data stores, variables, JMS, number ranges, idempotency, ID mappings, keystore |
| `promote` | moving between tiers | `compare`, `transport_check`, `drift`, `config_diff`, parameters, credentials, keystore, Partner Directory dependencies and diff, runtime status |
| `improve` | quality | `lint`, `lint_fix`, `layout_iflow`, graph, `drift`, `compare`, `bump_versions` |
| `partner-directory` | Partner Directory | `get_pd_parameters`, `pd_diff`, `pd_dependencies`, `pd_deploy`, `config_diff` |
| `security` | security material (read) | `list_credentials`, `list_keystore` |

`undeploy`, `delete_data_store_entry` and `unlock_artifact` are in no toolset: offer them explicitly
with `--tools`. `list_locks` is in `inspect` and `build`.

```json
"cpi-dev":  { "command": "cpictl", "args": ["mcp", "--root", ".", "--mode", "develop", "--toolset", "build,test"] },
"cpi-prod": { "command": "cpictl", "args": ["mcp", "--root", ".", "--mode", "discover", "--toolset", "monitor,promote"] }
```

### Dynamic toolsets

With `--dynamic-toolsets` (or `CPICTL_DYNAMIC_TOOLSETS=true`) the server starts with only `help`,
`doctor`, `list_toolsets` and `enable_toolset`. The agent enables the toolsets of its task
(`enable_toolset {"toolsets": ["build", "test"]}`); the server then lists their tools and sends
`notifications/tools/list_changed`, so the client fetches the new list. `--toolset` names the
toolsets enabled at the start. Toolsets are not disabled again during a session.

- Only the tools the server offers after mode and filters can be enabled: on a `--read-only` server,
  enabling `build` adds `lint` but not `deploy`.
- Tools in no toolset (`undeploy`, `delete_data_store_entry`, if the server offers them) are in the
  toolset `other`.
- A call to a tool the server offers but has not listed yet still works; the listing is guidance,
  the filters are the limit.
- The server instructions name the toolsets and what they are for, so the agent can enable one
  without calling `list_toolsets` first.

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
are unavailable. A pattern that matches no tool stops the server with exit code 2, so a typo never
leaves a tool enabled by accident.

| Tool class | Tools | `--read-only` |
|------------|-------|---------------|
| read | list_\*, get_\*, `validate_artifact`, `check_guidelines`, `pd_diff`, `pd_dependencies`, `config_diff`, `drift`, `compare`, `transport_check`, `lint`, `doctor`, `graph_*`, `loop_status`, runtime data tools | kept |
| local files (inside `--root`) | `download_artifact`, `copy_iflow`, `bump_versions`, `lint_fix`, `layout_iflow`, `discover_tenant`, `loop_start`, `loop_end` | kept |
| tenant changes / processing | `create_package`, `upload_artifact`, `upload_artifacts`, `set_parameters`, `deploy`, `undeploy`, `pd_deploy`, `send_test_message`, `set_log_level`, `delete_data_store_entry`, `unlock_artifact` | removed |

```json
"cpi-qa":   { "command": "cpictl", "args": ["mcp", "--root", ".", "--read-only"] },
"cpi-dev":  { "command": "cpictl", "args": ["mcp", "--root", ".", "--disable-tools", "undeploy,pd_deploy"] },
"cpi-logs": { "command": "cpictl", "args": ["mcp", "--tools", "list_message_logs,get_message_*,get_runtime_status"] }
```

The settings can also come from the environment (`CPICTL_READ_ONLY=true`,
`CPICTL_TOOLS`, `CPICTL_DISABLE_TOOLS`, comma-separated) or `cpictl.yaml`. Server-side limits
complement the client's own permission rules (in Claude Code, `permissions.deny` entries such
as `mcp__cpi-dev__undeploy` in `.claude/settings.json`), which still prompt or block per call.
The strongest limit is the tenant's: give the OAuth client of a QA or production server only
read roles.

## Tools

| Tool | Changes | Purpose |
|------|---------|---------|
| `help` | | What this server offers: tools after mode and filters, the cpi skills (with their instructions), the CLI commands, and which tools and skill fit common tasks. `topic` for one tool, skill, skill file or command. Always available. See [Finding your way](#finding-your-way) |
| `doctor` | | Connection and roles: per API area ok / forbidden (403, missing role) / missing (404), and which tools need it. Same as `cpictl doctor` |
| `list_packages` | | All integration packages (cached for `--cache-ttl`; `refresh: true` re-reads) |
| `create_package` | designtime | Create a package if it does not exist (`CREATED` / `EXISTS`, never changes one) |
| `list_artifacts` | | Designtime artifacts of a package (all four types; cached like `list_packages`) |
| `list_resources` | | Scripts, mappings, schemas, ... of an integration flow |
| `get_resource` | | Content of one resource (text inline, binary base64) |
| `download_artifact` | local files | Extract an artifact into a directory inside `--root` (empty unless `overwrite`) |
| `copy_iflow` | local files | Copy a flow (tenant or local) under a new ID, name, description and sender addresses; refuses unchanged sender addresses unless `keep_addresses`. See [new-flows.md](new-flows.md) |
| `bump_versions` | local files | Raise `Bundle-Version` of changed artifacts (`changed: true`: since the Git commit that last set it) before a pull request. See [versioning.md](versioning.md) |
| `upload_artifact` | designtime | Create or update an artifact from a local directory; `CREATED`, `UPDATED` or `UNCHANGED`. `dry_run`: only compare and report the action and version |
| `upload_artifacts` | designtime | `upload_artifact` for several artifacts in one call, 8 at a time; one result per artifact; `dry_run` |
| `validate_artifact` | | Tenant check of an integration flow (like *Check* in the Web UI); `PASSED` / `FAILED` with details |
| `check_guidelines` | | Run the activated design guidelines and wait; violations with violated components |
| `get_parameters` | | Externalised parameters of an integration flow; `artifact_ids` for several flows in one call (8 at a time) |
| `set_parameters` | designtime | Change parameters; only changed values are written, unknown keys fail first; `dry_run` |
| `deploy` | runtime | Deploy and wait; per artifact `DEPLOYED`, `SKIPPED`, `FAILED` (tenant error), `TIMEOUT`, with `designtimeVersion` / `runtimeVersion`. Refuses a designtime version older than the running one unless the designtime artifact was changed after that deployment or `allow_downgrade`; `rule` says which rule decided. `dry_run`: per artifact `deploy` true/false and the reason, nothing triggered |
| `send_test_message` | **triggers processing** | Send a message to the flow's endpoint (or via the test harness to a ProcessDirect address); HTTP status, response, message GUID; `wait_seconds` returns the final message log. See [testing.md](testing.md) |
| `get_runtime_status` | | Runtime status, version, deployment time and error of given artifacts |
| `list_runtime_artifacts` | | All deployed artifacts, filter by status (e.g. `ERROR`) |
| `list_service_endpoints` | | Callable URLs of deployed integration flows (where to send test messages) |
| `list_message_logs` | | Message processing logs by artifact or package, status, time, IDs; error texts; `wait_seconds` for final status; `custom_header {name, value}` (client-side scan, see `scanned`/`truncated`) |
| `message_summary` | | Volume and failures over a window: per flow (count per status, failures, durations), per connection between flows (predecessor links, or graph connections by correlation ID), failures grouped by error fingerprint with sample, count, first / last and newest GUID |
| `get_trace_tree` | | Path across flows from a trace ID or a message GUID (correlation ID, optionally joined by key custom headers): tree, hops, `pathKey`, `firstFailure` |
| `get_message_log` | | One message: error text, custom header properties, adapter attributes, attachments, persisted messages |
| `get_message_steps` | | Processing steps of a message and the first failing step (`modelStepId`) |
| `get_message_attachment` | | Content of a log attachment |
| `get_message_store_entry` | | Payload persisted by a Persist step |
| `set_log_level` | runtime setting | NONE / INFO / DEBUG / TRACE for a deployed flow; the server sets it back to INFO after `revert_after_minutes` (default 10 for TRACE and DEBUG) and on shutdown; result `revertsAt` |
| `get_message_trace` | | Traced steps of a message with trace IDs; `status: "missing_role"` (ok) when the key may not read message content |
| `get_trace_message` | | Payload, headers, exchange properties at one traced step (sensitive values masked) |
| `list_data_stores`, `list_data_store_entries`, `get_data_store_entry` | | Data stores, their entries (metadata) and one entry's content |
| `delete_data_store_entry` | tenant, **destructive** | Delete one entry; requires `confirm: true` |
| `list_locks` | | Artifacts open for editing in the Web UI: artifact, package, who holds the lock, since when |
| `unlock_artifact` | tenant, **destructive**, opt-in | Remove an artifact's edit lock (the holder's unsaved changes are lost); requires `confirm: true` and `--tools unlock_artifact` |
| `list_variables`, `get_variable` | | Global and flow variables, and a value |
| `list_jms_queues`, `get_jms_broker` | | Queues with message counts; broker capacity and usage |
| `list_number_ranges` | | Number range objects and current values |
| `list_log_files`, `get_log_file` | | System / HTTP log files and the end of one (adapter errors without a message log) |
| `list_idempotent_entries`, `list_id_mappings` | | Entries ignored as duplicates; ID mapper entries |
| `list_credentials` | | User credentials, OAuth2 client credentials, secure parameters: names and metadata, never secrets |
| `list_keystore` | | Keystore entries with validity and days left; `expiring_within_days` flags soon-expiring ones |
| `lint` | | Local flows: findings per rule (reuse, Partner Directory candidates, dead weight, simplify, robustness, performance, configuration, hygiene) with suggestions; `.cpi/lint.yaml`, baseline. See [lint.md](lint.md) |
| `lint_fix` | local files | Apply the mechanical fixes: scripts into script collections, unused scripts deleted, unconnected steps removed; `dry_run` |
| `layout_iflow` | local files | Lay out the diagram of `.iflw` files: flow order left to right, branches one below the other, no overlaps, right-angled lines; only the diagram changes. `mode` tidy / full, `check`, `dry_run`. See [lint.md](lint.md#diagram-layout) |
| `compare` | | Two sides per artifact (`tenant`, a content tree, `git:<ref>[:<path>]`): same / only_a / only_b / content_differs / version_differs / parameters_differ, files with `diff`, parameter keys (values with `show_values`), designtime / running version, draft, last changed by. See [compare.md](compare.md) |
| `transport_check` | | Before moving artifacts to this tenant: their dependencies (script collections, mappings, called flows, credentials, Partner Directory) and pre-checks (draft, outside changes with `target_dir`, dependencies present, credentials, PD parameters, parameter values in the target's `configure` file), pass / warn / fail / skip. See [transport.md](transport.md) |
| `drift` | | Local artifacts vs tenant: in_sync / tenant_newer / local_newer / diverged / not_on_tenant, runtimeOutdated |
| `discover_tenant` | local file | Inventory of packages and flows (adapters, steps, error handling, scripts, naming) to `.cpi/discovery.json`, and the content graph to `.cpi/graph.json`; `local_dir` for a local repository |
| `graph_search` | | Find flows, endpoints, systems, credentials, scripts, headers, Partner Directory parameters in `.cpi/graph.json` (local file). See [graph.md](graph.md) |
| `graph_neighbors` | | What a node is connected to: callers and callees of a flow (`sends_to`), users of a credential, script, header or PD parameter; `direction`, `edge_types`, `depth` |
| `graph_path` | | Shortest connection between two nodes (default: how messages move, through ProcessDirect/JMS addresses) |
| `undeploy` | runtime, **destructive** | Remove from runtime and wait; requires `confirm: true` |
| `get_pd_parameters` | | Partner Directory parameters of one PID (binaries: content type, size, sha256; content with `include_content`) |
| `pd_diff` | | Local Partner Directory files vs tenant: create / update / unchanged / remote_only per parameter |
| `pd_dependencies` | | Which local flows reference which Partner Directory parameters; dynamic references; unknown PIDs |
| `config_diff` | | Configure YAML vs tenant parameters: update / unchanged / unknown_key |
| `pd_deploy` | Partner Directory, **destructive with full_sync** | Upload Partner Directory parameters; dry run unless `dry_run: false`; `keys: ["PID:ID"]` changes only those parameters |

Content tools (`get_resource`, `get_message_attachment`, `get_message_store_entry`) return
`{size, text | base64, truncated}`; `max_bytes` (default 64 KB, max 1 MB) limits the size.

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
| `help` | Mode, available and disabled tools, workflows (task -> skill, tools, docs), skills, CLI commands |
| `help {"topic": "copy_iflow"}` | Description and input schema of a tool, or why it is not available here |
| `help {"topic": "cpi-test"}` | The skill's instructions (`SKILL.md`) and its reference files |
| `help {"topic": "cpi-plan/brief-template.md"}` | One reference file of a skill |
| `help {"topic": "iflow copy"}` | Usage, description and flags of a CLI command |
| `help {"topic": "workflows"}` | Only the task overview (also `tools`, `skills`, `cli`) |

Typical questions it answers: "what can you do on this tenant?", "which tool finds the flows that
call X?", "how do I test a ProcessDirect flow?". On the command line the same information is in
`cpictl --help`, `cpictl <command> --help`, [commands.md](commands.md) and `cpictl skills list`.

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
| `timeout` | 6 | Not finished in time: check `get_runtime_status` |
| `partial` | 7 | Some items failed: look at the per-item results |
| `stopped` | 8 | A loop limit was reached: tenant changes are refused; call `loop_end` and report |

Tool failures are returned as tool results with `isError: true`, not as JSON-RPC errors,
so the model sees them. JSON-RPC errors are only used for protocol problems (unknown tool,
malformed request).

## Typical agent loop

1. `download_artifact` once to get the iFlow into the local repository (inside `--root`),
   or start from files that are already there. New package: `create_package`.
2. Edit the files; `layout_iflow` after changing an `.iflw` (the server lays out the diagram, the
   agent does not place shapes); `upload_artifact`; `validate_artifact` (and `check_guidelines`).
3. `deploy`; on `failed`, read `error` (the tenant's runtime error), fix and go back to 2.
4. `send_test_message` with `wait_seconds` (e.g. 60): HTTP status, response and the final
   message log in one call. (For flows without an HTTP sender: trigger them otherwise and use
   `list_message_logs` with `since` and `wait_seconds`.)
5. For a `FAILED` message the log contains `errorText`.
6. `get_message_steps` shows the failing step (`modelStepId`, as in the `.iflw` BPMN);
   `get_message_log` lists custom headers, attachments and persisted messages, which
   `get_message_attachment` / `get_message_store_entry` download. Fix and go back to 2.
7. `set_parameters` + `deploy` to change externalised configuration.

Message logs, attachments and persisted messages contain business data. Error texts are
truncated (4 KB in lists, 16 KB in `get_message_log`, flagged with `errorTruncated`) to keep
tool results small. Use a development tenant with test data for agents.

## Build loops

For autonomous build/test/fix loops the server enforces limits the model cannot override:

1. `loop_start {goal, max_iterations=5, same_error_limit=2, wall_clock_minutes=60, max_deploys=15}`
   returns a `loop_id` (one open loop per server).
2. While it is open every tenant-changing call is counted. An **iteration** is a `deploy` (or a
   `pd_deploy` that is not a dry run) after a failed test result (`send_test_message`,
   `get_trace_tree` with `firstFailure`, `list_message_logs` with a FAILED message). Failures are
   fingerprinted (artifact, step, error text without IDs, timestamps and long numbers).
3. When a limit is reached (iterations, the same fingerprint `same_error_limit` times, wall clock,
   deploys), every tenant-changing tool returns `ok: false`, `errorCategory: "stopped"`, exit code
   8. Read tools keep working.
4. `loop_status` shows the counters; `loop_end {loop_id, outcome}` always works and writes
   `.cpi/loops/<loop_id>.md` (goal, outcome, counters, every call). Each call is also appended to
   `.cpi/loops/<loop_id>.jsonl` as it happens.

## Safety

- `undeploy` refuses to run without `confirm: true`.
- `pd_deploy` is a dry run unless `dry_run: false`; full sync never deletes the parameters
  of a partner whose local files could not be read.
- Local paths cannot escape `--root`.
- Long-running calls are cancelled on `notifications/cancelled` and when the server stops.
- Credentials are never part of tool results or logs. Security material is read-only: there
  is no tool that creates or changes credentials or keys, so secrets never pass through the
  model. Deploy them with `cpictl credentials` ([security.md](security.md)).
