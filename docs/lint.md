# Lint and improvements

`cpictl lint` checks the integration flows of a content tree (the tenant layout
`packages/<package>/<artifact>`, as `snapshot` writes it) and reports findings with a rule, a
severity and a suggestion. It reads local files only and is fast enough for every pull request on
a repository with a thousand flows. `--fix` applies the mechanical fixes. The `cpi-improve` skill
turns the findings into proposals and applies them with an agent.

```bash
cpictl lint                                             # all flows below packages/
cpictl lint --package UtilitiesBaseEDM --output json
cpictl lint --changed --since origin/main --fail-on warning     # pull request gate
cpictl lint --update-baseline                           # accept today's findings as known
cpictl lint --fix --dry-run                             # what --fix would change
cpictl lint --fix --package UtilitiesBaseEDM            # apply it
cpictl lint --list-rules
```

## Rules

| Group | Rule | Severity | Finds | Fix |
|-------|------|----------|-------|-----|
| reuse | `duplicate-script` | warning | the same script in several flows | moves it to a script collection |
| | `use-script-collection` | warning | a script equal to one in an existing collection | references the collection |
| | `similar-script` | info | scripts that differ only in comments and formatting | |
| | `duplicate-mapping` | warning | the same message mapping or XSLT in several flows | |
| | `missing-script-collection` | warning | a referenced collection not in the repository | |
| | `collection-script-doc` | warning | a public function of a script collection without a doc comment (what it does, what it reads and writes, how to call it); the first sentence is what discovery and `graph_search` show | |
| partner-directory | `router-literals` | warning | a router deciding on 4+ literal values of one header or property | |
| | `lookup-table-in-script` | info | a script with 10+ literal map entries or case branches | |
| | `deployment-copies` | info | one folder deployed under several IDs with different `configOverrides` (from the deploy config) | |
| dead-weight | `unconnected-step` | warning | a step not reachable from the start event | removes it |
| | `unused-script` | warning | a script file no step uses | deletes it |
| | `unused-resource` | info | a mapping, schema or WSDL nothing references | |
| | `unused-parameter` | warning | a `parameters.prop` key no `{{key}}` uses | |
| | `noop-content-modifier` | warning | a content modifier without headers, properties and body | removes it (`--fix-rules`) |
| | `property-never-read` | info | an exchange property set but never read in the flow | |
| simplify | `consecutive-content-modifiers` | info | content modifiers in a row | |
| | `converter-roundtrip` | warning | XML to JSON and straight back (or the reverse) | |
| | `trivial-script` | info | a script that only sets literal headers or properties | |
| | `large-script` | info | a script over 300 lines | |
| robustness | `no-exception-subprocess` | warning | an integration process without exception subprocess | |
| | `swallowed-exception` | warning | an empty `catch` block | |
| | `collection-script-tests` | warning | a public function of a script collection that no test in `scriptTests.dir/<package>/<collection>/` calls ([script tests](#script-collection-tests)) | |
| | `collection-script-coverage` | warning | a script collection script below `scriptTests.minCoverage` line coverage in the JaCoCo report, not in the report, or changed after it; no report at all | |
| | `idoc-no-idempotency` | info | a flow with an IDoc sender channel, but no Idempotent Process Call and no JMS receiver (decoupling): IDocs that SAP resends are processed twice | |
| performance | `body-as-string` | info | `getBody(String)` (whole payload in memory) | |
| | `payload-attachment` | warning | the payload written into the message processing log | |
| | `logging-in-loop` | warning | log attachments inside a loop | |
| | `println` | info | output to stdout | |
| | `odata-query-without-paging` | warning | an OData receiver with operation Query, no `$top` in the query options and paging off (a single entity by key, `Products(1)`, is fine) | |
| | `odata-single-calls-in-loop` | info | an OData receiver that creates, updates or deletes per record: its step comes after a splitter (up to the gather), or is in a local process called by a Looping Process Call or by a process call that itself runs per record; not with `$batch` | |
| | `component-newer-than-tenant` | error | a step or adapter version newer than the tenant's editor offers (needs a [component catalogue](components.md) from the editor metadata): the tenant's Check and deploy fail. Typical after copying a flow from a newer tenant | |
| configuration | `hardcoded-endpoint` | warning | a receiver address that is not a `{{parameter}}` or `${...}` (HTTP, SOAP, OData, IDoc, SFTP, ...) | |
| | `hardcoded-url-in-script` | warning | a URL in a script (namespaces excluded) | |
| | `hardcoded-secret` | error | a password, token or key assigned in a script or parameter; a URL only when it carries credentials (user info, `token=`, `sig=`, `key=` ...) | |
| | `odata-hardcoded-query` | info | literal OData query options or resource path with environment-specific values: `sap-client=<nnn>`, `CompanyCode`/`BUKRS`, `SalesOrganization`/`VKORG`, `Plant`/`WERKS`, `ControllingArea`/`KOKRS eq '<literal>'` | |
| | `idoc-sap-client-literal` | warning | an IDoc receiver address that is partly externalised but keeps a literal host or `sap-client` (a fully literal address is `hardcoded-endpoint`, reported once) | |
| | `idoc-control-record-literal` | info | the control record partners `EDI_DC40` `SNDPRN`, `RCVPRN`, `SNDPOR`, `RCVPOR` set to literals: in a content modifier body, an XSLT (literal result element) or a message mapping (target field from the `const` function) | |
| hygiene | `prefer-script-v2` | info | a Groovy script step of version 1 (Groovy 2.4, `com.sap.gateway…Message`): new and changed scripts use version 2 (Groovy 4, `package script.v2`, `com.sap.it.script.v2.api.Message`, files under `script/v2/`); moving is a rewrite, not a version change | |
| hygiene | `outdated-component` | info | a step or adapter older than the tenant offers (with a [component catalogue](components.md)), else older than the newest version used in the repository (not Groovy 1 → 2: that is `prefer-script-v2`) | |
| | `script-doc` | info | a flow's local script with no comment saying what it does (at the top or above its function) | |
| | `default-step-name` | info | "Groovy Script 1", "Content Modifier 2", ... | |
| | `layout` | info | the diagram has steps or lines that are not drawn, overlapping shapes, shapes outside their pool, lines through steps or shapes that nearly touch ([diagram layout](#diagram-layout)) | yes |
| | `naming` | warning | a flow ID not matching `naming.iflowId` | |

The OData and IDoc rules read the adapter properties the Web UI writes (`operation`,
`resourcePath`, `queryOptions`, `customQueryOptions`, `pagination`, `enableBatchProcessing`,
`address`) and the step types of splitters, gathers and process calls; see
[graph.md](graph.md#odata-and-idoc) for what discovery reads from the same channels.

All flows are read for the cross-flow rules (duplicates, versions); `--package`, `--artifact` and
`--changed` select which flows are reported. `deployment-copies` needs the deploy config
(`--deploy-config`, else `orchestrator.deployConfig`). The agent's guide per rule (when to act, how
to change it, risk) is the skill's
[improvement catalogue](../plugin/skills/cpi-improve/improvement-catalogue.md).

## Settings: `.cpi/lint.yaml`

```yaml
rules:                      # severity per rule: off, info, warning, error
  default-step-name: off
  hardcoded-endpoint: error
settings:
  duplicateMinFlows: 2      # duplicate-script, similar-script, duplicate-mapping
  routerMinValues: 4        # router-literals
  lookupMinEntries: 10      # lookup-table-in-script
  largeScriptLines: 300     # large-script
scriptCollections:
  packageCollection: "{package}_Scripts"
  crossPackage: false       # true: flows may reference a collection in another package
  sharedPackage: SharedScripts
  sharedCollection: Shared_Scripts
naming:
  iflowId: "^[A-Z][A-Za-z0-9]*(_[A-Za-z0-9]+)+$"
layout:                     # cpictl iflow layout and the layout rule's fix
  mode: tidy                # tidy (keep the order of branches) or full
  hgap: 60                  # space between columns of steps
  vgap: 40                  # space between rows
scriptTests:                # paths relative to the project root (the folder holding .cpi)
  dir: tests/scripts        # tests in the layout <package>/<collection>/
  coverageReport: build/reports/jacoco/test/jacocoTestReport.xml
  minCoverage: 80           # line coverage per collection script, percent
ignoreVendors: [SAP]        # default: SAP-delivered content is not checked; [] checks every vendor
ignore:                     # findings that stay (say why in a comment)
  - artifact: Legacy_*
    rule: "*"
```

**Vendors.** Each finding carries `vendor`: the `Vendor` of the package details that snapshot
writes (`<package>/<package>.json`), or `SAP` for an artifact ID that starts with `com.sap.` when
there are none. Content of the vendors in `ignoreVendors` (default `SAP`: packages SAP delivers,
which customers do not edit) is not reported; cross-flow rules still compare against it. The
result says how many artifacts were left out (`skippedVendor`). A copy of an SAP package that you
edit keeps vendor SAP: set `ignoreVendors: []`, or give the copy your own vendor.

`--rules` points to another file. An unknown rule or severity is an error (exit code 2).

## Script collection tests

A script collection is a library that many flows call, so its scripts are documented and tested.
The tests live outside the artifact folders (upload would send them to the tenant, and snapshot
replaces artifact folders), in the same layout as the content tree:

```
packages/Orders/Orders_Scripts/src/main/resources/script/Headers.groovy
tests/scripts/Orders/Orders_Scripts/HeadersTest.groovy
```

cpictl does not run Groovy. A small Gradle build at the project root compiles the collection
scripts in place with stubs of SAP's `Message` API, runs the tests and writes a JaCoCo report;
lint then reads the report. The build, the stubs and an example test are in the build skill's
[script-tests.md](../plugin/skills/cpi-build/script-tests.md). In CI, run the tests before lint:

```bash
gradle test            # writes build/reports/jacoco/test/jacocoTestReport.xml
cpictl lint --fail-on warning
```

`collection-script-tests` looks for a call of each public function (`name(`) in the test sources
(`.groovy`, `.java`). `collection-script-coverage` matches scripts by file name, so script names
must be unique across the collections in one build. Private functions are helpers: no doc comment
and no direct test needed.

## CI: baseline and gate

`--fail-on warning` exits with code 5 when a **new** finding has at least that severity. New means
not in the baseline (`.cpi/lint-baseline.json`, `--baseline`): `cpictl lint --update-baseline`
records today's findings, so a large existing repository can start gating pull requests without
fixing everything first. Commit the baseline; update it when findings are fixed or accepted.

```bash
cpictl lint --changed --since origin/main --fail-on warning
```

In GitHub Actions the new findings also go to the job summary.

## Fixes

`--fix` changes local files only (`--dry-run` lists the changes):

| Rule | Change |
|------|--------|
| `duplicate-script` | the script goes into the package's collection (`<package>_Scripts`, created with manifest and `metainfo.prop` when missing), or with `crossPackage: true` and flows in several packages into the shared collection; every script step that used the local file references the collection (`scriptBundleId`); the local copies are deleted |
| `use-script-collection` | the script step references the existing collection that holds the same script; the local copy is deleted |
| `unused-script` | the file is deleted |
| `unconnected-step` | the step, its sequence flows and its diagram shapes are removed |
| `noop-content-modifier` | only with `--fix-rules noop-content-modifier` (or `all`): the step is removed and its neighbours connected |
| `layout` | only with `--fix-rules layout` (or `all`): the diagram is laid out (`tidy`); models that lose steps through other fixes in the same run are laid out too |

Afterwards: review the diff, `cpictl version bump --changed`, add new script collections to the
deploy config and deploy them **before** the flows, then deploy and test the flows. Whether flows
may reference a collection in another package depends on the tenant: check it once before setting
`crossPackage: true`.

## Diagram layout

`.iflw` files keep the flow (steps, configuration, sequence flows) apart from the drawing
(`BPMNDiagram`: one shape with position and size per step, one line with waypoints per sequence
flow). The Web UI draws what the file says, so diagrams written by hand or by an agent are often
cramped. `cpictl iflow layout <path>...` (MCP `layout_iflow`) recomputes only the drawing:

- steps left to right in flow order, every step in the column after its predecessor;
- branches of a router one below the other, centred on the router, and joined again;
- right-angled lines that bend in the gap between two columns, so they cross no step;
- exception subprocesses below the main flow, inside the pool; local processes as their own
  pools below the integration process;
- senders left of the pool, level with their start event; receivers right of the pool, level
  with the step that calls them;
- standard sizes (events 32, gateways 40, steps 100 × 60; widened steps keep their width).

| Option | Meaning |
|--------|---------|
| `--mode tidy` (default) | keeps the order of steps and branches as they are drawn now |
| `--mode full` | also reorders branches to reduce crossing lines |
| `--check` | only reports problems; exit code 5 when any (for CI) |
| `--dry-run` | computes the layout, writes nothing |
| `--hgap`, `--vgap` | spacing (also `layout` in `.cpi/lint.yaml`) |

Paths are `.iflw` files or directories (an artifact, a package, the whole tree). The result is
stable: a second run changes nothing, and the rest of the file stays byte for byte. Steps and
lines missing from the diagram are added. Look at the result in the Web UI once before relying
on it in a pipeline (the Web UI shows what the file says; we have not seen it re-layout a file).

The agent skills call `layout_iflow` after every change to an `.iflw` instead of computing
coordinates.

## With an agent: the `cpi-improve` skill

The skill runs `lint` (MCP tool) for the scope you name, groups the findings into proposals (one
script collection per cluster of shared scripts, one Partner Directory design per routing table or
flow family, a dead-weight sweep per package, ...), writes them to `.cpi/improvements/<date>-<scope>.md`
and asks which to apply. Approved proposals are applied one at a time on the development tenant:
`lint_fix` for the mechanical ones, cpi-build for the rest, then `bump_versions`, upload
(dependencies first), deploy, cpi-test for every affected flow and cpi-review.

```text
> Check the package UtilitiesBaseEDM for improvements.
> Apply P1 and P3.
```

MCP tools: `lint` (read, local files) and `lint_fix` (local files, `dry_run`), see [mcp.md](mcp.md).
