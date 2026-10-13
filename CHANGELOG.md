# Changelog

All notable changes to cpictl. Coming from FlashPipe? See
[docs/migrating-from-flashpipe.md](docs/migrating-from-flashpipe.md).

## Unreleased

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
