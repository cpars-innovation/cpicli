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

## Grants and deny

The top-level `packages`, `artifacts` and `actions` give the same actions on every package. When packages need
different actions, `grants` adds package-bound permissions, and `deny` removes some whatever is granted. This is the
form the cpictl gateway builds from its policy, one grant per matching rule; it works in a scope file as well.

```yaml
version: 1
grants:
  - packages: ["SD_*"]
    artifacts: ["!*_Legacy"]          # optional, as at the top level
    actions: [read, download, upload, deploy, test]
  - packages: ["FI_Shared"]
    actions: [read]
deny:
  - packages: ["HR_*"]                # every action; also hides the packages
  - packages: ["SD_*"]
    artifacts: ["*_Keys"]
    actions: [download]
```

- Each grant needs `packages` and `actions`. The top level counts as one more grant; grants add up.
- A `deny` entry names `packages`, `artifacts` or both, and optionally `actions` (none: every action). It is applied
  after all grants and cannot be overridden. An entry that names artifacts applies to those artifacts only, not to
  the package that holds them.
- Partners and tenant areas take the top-level `actions` only.
- Message contents (attachments, payloads, traces) and test messages are decided per flow: reading `FI_Shared`'s
  message logs does not allow downloading their payloads, and seeing its entry points does not allow sending to them.

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
- **An artifact's package** is found by listing the artifacts of the allowed packages (a few calls, cached for
  30 seconds, also for known IDs; refreshed at once on an unknown ID). An artifact that moves to another package
  loses the access of its old one within that time. A ProcessDirect address's flow is cached for 5 minutes (the
  rebuild reads every allowed flow). An artifact outside them is refused like one that does not exist, and the
  refusal does not name its package.
- **Redirects are not followed.** A 3xx answer comes back as it is; following it would send a request the guard
  has not checked (and, with a token transport, the token to another host).
- **Message details** (errors, attachments, store entries, runs, traces) are allowed for messages of flows in the
  scope. Their child IDs are only accepted when they came with an allowed answer.
- **Test messages** go only to entry points the guard has seen for flows in the scope, and only with `test`. An
  endpoint covers its own path and paths below it, not siblings (`/sd/order` does not cover `/sd/orders`).
- **The test harness** (`CPICTL_Test_*`) forwards to a ProcessDirect address. Calls to it need `test` and a target
  address that belongs to the sender of a flow in the scope; the guard reads those addresses from the allowed flows
  (externalised parameters resolved).
- **The test tooling** is exempt only by exact name: flows starting with `CPICTL_Test_`, and the Partner Directory
  partners `CPICTL_Test` and `CPICTL_Stub`. Teams share these partners, so their parameters (stub rules,
  stylesheets under test) are decided by their ID: it must start with an owner and a dot, where the owner is a flow
  the scope grants `test` on (stub rules, e.g. `Orders_Route.erp`) or a partner it may read (stylesheets: cpictl
  names them `<partner>.<parameter>`). With `test`, those may be read and written; others are hidden and refused,
  also the list of a tooling partner's parameters through `Partners('…')/…`.
- **Messages of the test tooling** are decided by the flow they tested. The harness, the flow under test and the
  receiver stub share one correlation ID, so a harness or stub message (and its runs, attachments and recordings) is
  readable when a flow in its correlation group, outside the tooling, is in the scope with `test`. Another team's
  test runs stay hidden. A tooling message with no such flow (an XSLT or script test run) is refused: nothing says
  whose it was.
- **API Management**, tenant-wide artifact lists and any request the guard does not know are refused.
- **Requests a server could read differently** are refused, also inside a `$batch`: dot segments (also encoded or as
  `..;`), encoded slashes, backslashes and `%`, `;` in API paths, repeated query parameters, method override headers,
  compressed request bodies and JSON bodies that repeat a key. The guard decides on the literal request; these would
  let the tenant or a proxy in between address something else.

A refusal has exit code **9** and `errorCategory: "scope"`, and names the action and the resource:
`scope: deploy of artifact FI_Post is not granted`. With `--events`, each refusal is also a `scope.refused` line.
MCP `help` (topic `server`) shows the active scope and its SHA-256, so the caller can verify which scope a server it
started enforces.

Go: `pkg/scope` (`Parse`, `Scope.Allows`, `Scope.Hash`, `Guard`, `Guard.Check`) is the same matcher the CLI uses, so other tools
(CPI Studio) decide identically.

## Limits

- A scope is a boundary only for whoever cannot reach the service key. Someone who holds the key can call SAP
  directly; for them it is a guardrail.
- Local files are not filtered: if an earlier, unscoped `discover` wrote `.cpi/graph.json`, the graph tools serve
  it. Run discover under the scope.
- Below a message and a runtime artifact only the lists cpictl reads are allowed (`ErrorInformation`, `Attachments`,
  `MessageStoreEntries`, `Runs`, `CustomHeaderProperties`, `AdapterAttributes`): the tenant navigates from there into
  contents, which are only read through their own IDs and need `download`.
- The error text of a failed XSLT test run is read from its message, which a scope refuses (no tested flow): under
  a scope, such a run reports the failure without the tenant's error text.
- With `test` (also without `read`), a scope reads the attachments, steps and traces of the test tooling's
  messages of the flows it tests: that is how a test run checks what its flow sent to the stub.
- A test message may not set `SAP_MplCorrelationId` or `SAP_MessageProcessingLogID` (the cpictl gateway drops
  them too): with a chosen correlation ID, it would join another team's test run. A team that may change its own
  flows (`upload`, `deploy`) could still set one inside a flow it calls; where teams must not see each other's
  test runs, give `upload` only to people, not to agents, and review flows for that header.
- A list with many test tooling messages costs SAP lookups (one per message without its correlation ID, one per
  correlation group, 8 at a time, cached; "no flow of the scope" for 10 seconds). At most 100 of each per answer;
  rows beyond are hidden in that answer: filter the list by flow.
