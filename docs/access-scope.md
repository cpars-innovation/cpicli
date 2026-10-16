# Access scope

An access scope restricts one cpictl process, CLI command or MCP server, to some packages, artifacts and Partner
Directory partners, a few tenant-wide areas and a set of actions. Everything not granted is hidden or refused
(default deny). Use it when an agent session or a person should work on part of a tenant whose service key can do
everything: Cloud Integration has no package-level authorisation of its own.

Without a scope cpictl behaves as before.

```bash
cpictl mcp --scope-file scope.json
cpictl deploy --artifact-ids SD_Order_In --scope-file scope.json
cpictl packages --scope-packages 'SD_*' --scope-actions read          # without a file
```

```json
{
  "version": 1,
  "packages":  ["SD_*", "Shared_*"],
  "artifacts": ["!*_Legacy"],
  "partners":  ["SD_*"],
  "tenant":    ["variables:read"],
  "actions":   ["read", "download", "upload", "deploy", "test"]
}
```

JSON or YAML. The scope is fixed for the life of the process. It is read only from the flags (`--scope-file`, or the
`--scope-packages`, `--scope-artifacts`, `--scope-partners`, `--scope-tenant` and `--scope-actions` flags; not both),
never from a config file, so a session that can edit `cpictl.yaml` cannot widen it.

## Matching

- Patterns: exact and case-sensitive, `*` is the only wildcard, a leading `!` excludes. They are evaluated in order
  and the last match wins. A list that starts with an exclusion starts with an implicit `*`.
- Patterns may contain letters, digits, `_`, `.`, `-` and `*`. Under a scope, IDs with other characters are refused
  before any tenant call.
- `packages`: the packages whose content is visible. An empty list means none.
- `artifacts`: optional, narrows within the allowed packages (no list: all artifacts of the allowed packages).
- `partners`: Partner Directory partner IDs (PIDs).
- `tenant`: tenant-wide areas, each `<area>:read` or `<area>:write` (write includes read). The areas are `keystore`,
  `credentials`, `jms`, `variables`, `numberranges`, `datastores`, `accesspolicies`, `logfiles`, `settings`,
  `auditlog`, `idmappings`, `idempotency`, `locks` and `components`. Without a grant they are refused.

## Actions

| Action | Covers |
|--------|--------|
| `read` | lists, status, parameters, message logs without payloads |
| `download` | content: artifact files, resources, attachments, message store payloads, traces |
| `upload` | create and update artifacts and packages, value mapping pairs |
| `deploy` | deploy and undeploy |
| `configure` | externalised parameters |
| `test` | send test messages, test runs (the test tooling `CPICTL_Test*` may be read and called, never changed) |
| `log-level` | change a flow's log level (TRACE shows payloads) |
| `delete` | delete artifacts and packages |
| `pd-write` | change Partner Directory entries |

## How it is enforced

The boundary is the HTTP client, not the tools. Every request a cpictl process sends to the tenant passes one guard,
whichever command or MCP tool sends it, including tools added later:

- **Writes, deploys and deletes** outside the scope are refused before the request is sent. A `$batch` is parsed
  operation by operation and each one is checked; a batch that cannot be parsed completely is refused as a whole.
- **Lists** (packages, artifacts of a package, runtime artifacts, service endpoints, message logs, custom header
  search, Partner Directory, the Web UI runtime list) are filtered. Totals (`__count`) are removed, so they don't
  reveal hidden entries. A list URL can only be read: a write to it (also with a `$filter`) is refused.
- **`$expand`** is limited to the expansions cpictl uses (entry points, custom header log, run step properties,
  design guidelines), so an expansion cannot carry entities the filter does not see.
- **An artifact's package** is found by listing the artifacts of the allowed packages (a few calls, cached;
  refreshed once on an unknown ID). An artifact outside them is refused like one that does not exist, and the
  refusal does not name its package.
- **Message details** (errors, attachments, store entries, runs, traces) are allowed for messages of flows in the
  scope. Their child IDs are only accepted when they came with an allowed answer.
- **Test messages** go only to entry points the guard has seen for flows in the scope, and only with `test`. An
  endpoint covers its own path and paths below it, not siblings (`/sd/order` does not cover `/sd/orders`).
- **The test harness** (`CPICTL_Test_*`) forwards to a ProcessDirect address. Calls to it need `test` and a target
  address that belongs to the sender of a flow in the scope; the guard reads those addresses from the allowed flows
  (externalised parameters resolved).
- **The test tooling** is exempt only by exact name: flows starting with `CPICTL_Test_`, and the Partner Directory
  partners `CPICTL_Test` and `CPICTL_Stub`, whose parameters `test` may change (stub responses). Messages of tooling
  flows, and their children, are readable with `test`.
- **API Management**, tenant-wide artifact lists and any request the guard does not know are refused.

A refusal has exit code **9** and `errorCategory: "scope"`, and names the action and the resource:
`scope: deploy of artifact FI_Post is not granted`. With `--events`, each refusal is also a `scope.refused` line.
MCP `help` (topic `server`) shows the active scope and its SHA-256, so the caller can verify which scope a server it
started enforces.

Go: `pkg/scope` (`Parse`, `Scope.Allows`, `Scope.Hash`, `Guard`) is the same matcher the CLI uses, so other tools
(CPI Studio) decide identically.

## Limits

- A scope is a boundary only for whoever cannot reach the service key. Someone who holds the key can call SAP
  directly; for them it is a guardrail.
- Local files are not filtered: if an earlier, unscoped `discover` wrote `.cpi/graph.json`, the graph tools serve
  it. Run discover under the scope.
