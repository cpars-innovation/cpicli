---
name: cpi-discover
description: Discover how an SAP Cloud Integration tenant builds its integration flows (naming, adapters, error handling, logging, scripts, parameters) and write the repository's conventions file .cpi/conventions.md. Use when a repository has no .cpi/conventions.md yet, when the user asks to analyse, onboard or learn a tenant, or to refresh the conventions after the tenant changed.
allowed-tools: Read Write Edit Glob Grep mcp__plugin_cpi_cpi__discover_tenant mcp__plugin_cpi_cpi__list_packages mcp__plugin_cpi_cpi__list_artifacts mcp__plugin_cpi_cpi__list_resources mcp__plugin_cpi_cpi__get_resource mcp__plugin_cpi_cpi__get_parameters mcp__plugin_cpi_cpi__list_credentials mcp__plugin_cpi_cpi__list_runtime_artifacts mcp__plugin_cpi_cpi__graph_search mcp__plugin_cpi_cpi__graph_neighbors mcp__plugin_cpi_cpi__graph_path mcp__plugin_cpi_cpi__list_components mcp__plugin_cpi_cpi__list_integration_connections mcp__plugin_cpi_cpi__lint
inputs:
  - id: packages
    label: Representative packages
    kind: multiselect
    source: packages
    help: Packages whose flows show the house style. Empty means all, except SAP standard content.
---

# Discover a tenant's conventions

**Inputs:** representative packages (`packages`). The same list is in the front matter (`inputs`) for tools
such as CPI Studio, which ask for them in one form. Use the values the user or the calling tool
already gave, find the rest yourself where you can, and ask only for what is still missing, in
one question.

Every tenant has its own house style. The other cpi skills (plan, build, test, review) follow
`.cpi/conventions.md` in the repository. This skill writes the first version of that file from
the flows that already exist. The user owns the file afterwards: they edit and extend it, and
you never overwrite their changes.

## 1. Check what is there

- If `.cpi/conventions.md` exists, this is a **refresh**: keep every section the user wrote or
  changed, only add new findings and mark contradictions (`> Discovery 2026-…: 14 of 20 new flows
  use X instead`). Ask before changing an existing rule.
- Ask the user which packages are representative if the tenant is large (more than ~50 flows) or
  contains SAP standard content that should not be copied (packages from the Discover catalogue,
  read-only packages). Standard content is a poor source for house conventions.

## 2. Collect the facts

Call `discover_tenant` (writes `.cpi/discovery.json`, `.cpi/graph.json` and the component
catalogue `.cpi/components.json`, returns the summary). Use `package_ids` for the representative packages. If the repository already contains the content (e.g. from
`cpictl sync` or `snapshot`), `local_dir` analyses it offline.

The summary contains counts, not rules. Then **look at examples** to understand the why, about
5-8 flows chosen across packages and adapters:

- `list_resources` + `get_resource` for scripts that appear in several flows
  (`summary.sharedScripts`) and for the most common script names: they are usually the
  logging and error handling framework.
- `get_parameters` of a few flows: how are parameters named and grouped?
- `list_credentials`: how are credentials named (compare with `summary.credentialRefs`)?
- **Authentication headers and secret properties.** Which headers carry credentials to receivers
  besides `Authorization` (an API's own `X-Partner-Token`, `Ocp-Apim-Subscription-Key`, ...), and
  in which properties do scripts keep values read from the secure store? Run `lint` with
  `rules: ["undeclared-auth-header", "secret-target-name"]` on the content (`local_dir`, or the
  repository): it names headers that read like authentication headers and every header or property
  a script puts a credential into. Confirm them in the scripts (`get_resource`), then declare them
  in `.cpi/lint.yaml` (they add to the defaults; ask the user when unsure whether a header is
  secret):

  ```yaml
  security:
    secretHeaders: [X-Partner-Token]
    secretProperties: ["secret*", "PartnerToken"]
  ```

  and name them in conventions.md (Configuration and security). From then on lint keeps those
  values out of logs, JMS queues, other flows and wildcard receivers.
- Script collections are in `discovery.json` (`scriptCollections`): each script with its summary
  and functions (from their doc comments), and `iflows[].collectionScripts` says which flows call
  which function. `graph_search` with `types: ["script"]` finds them by purpose; script nodes
  carry `reusable`, `docStatus`, `keywords`, `reads`, `writes`, `usages` and `shape`. Note
  collections whose scripts are not `docStatus: ok`: the conventions should name them as reuse
  candidates that need documenting (lint `collection-script-doc`, `collection-script-doc-quality`),
  and flow scripts with `usages` > 1 or a shared `shape` as candidates for a collection. Put the
  comment template ([script-docs.md](../cpi-build/script-docs.md)) into the conventions'
  script section.
- The graph shows how flows work together: `graph_search` with `types: ["endpoint"]` lists the
  ProcessDirect and JMS addresses (naming of internal interfaces), `graph_neighbors` on a
  common script or header shows which flows share it, `sends_to` edges show whether flows are
  split into inbound / processing / outbound parts.
- SAP landscapes are OData- and IDoc-centric. `discovery.json` describes OData channels
  (`receivers[].odata`: version, operation, entity set, query options, paging, EDMX, host) and IDoc
  channels (`idoc`: direction, path, sap-client) and lists the IDoc types each flow handles
  (`idocTypes`, with where they were found). In the graph, `graph_search` with `types:
  ["odata_entity"]` or `["idoc_type"]` lists them; `graph_neighbors` on an entity or IDoc type
  shows which flows read, write, receive or send it. Describe how the tenant does it: paging,
  `$batch`, externalised clients and hosts (`{{S4_OData_Address}}`), which header carries the IDoc
  number (`SAP_ApplicationID`), idempotency and JMS decoupling for inbound IDocs, where EDMX and
  IDoc XSDs live (lint `odata-query-without-paging`, `idoc-no-idempotency`,
  `idoc-sap-client-literal`). Background: [sap-adapters.md](../cpi-build/sap-adapters.md).
- `list_integration_connections`: the tenant's own view of which systems the deployed flows
  connect (sender host -> flows -> receiver host); compare it with the graph's `calls` edges and
  use the host names when you describe the landscape.
- `list_components` shows the adapter and step versions the flows use. If its `sources` lack
  `editor`, tell the user that `cpictl components import` with the Web UI's editor metadata
  (docs/components.md) gives the exact versions the tenant offers, and that new flows are built
  with those.
- If the repository has downloaded flows, read one `.iflw` of a typical flow to see how the
  exception subprocess and the logging steps are built.
- cpictl's own test tooling (`CPICTL_Test_*` flows) is left out (`skippedTestTooling`); leave out
  other test, demo and template flows the same way when you count.
- Scripts: `summary.scriptSteps` says how many script steps call a script collection and how many
  carry their own script; `summary.collectionScriptCalls` which collection scripts are called most.
  `scriptNames` only counts local file names (`script1.groovy` says nothing): describe the shared
  collections as the convention when most steps call them.
- Error handling: `iflows[].exceptionSubprocesses` (steps, end events, local process calls) and
  `summary.emptyExceptionSubprocess` / `exceptionSubprocessEnds`. An exception subprocess with no
  step, or one ending in a Message End without calling something that raises the error again,
  swallows errors: report it as a finding (lint `exception-subprocess-message-end`), never as the
  house convention.
- Log level: when every flow has the same `logLevel` (often `All events`), it is probably the
  editor's or the download's default, not a decision. Write it as observed and ask.

## 3. Write `.cpi/conventions.md`

Start from [conventions-template.md](conventions-template.md). For every rule:

- State it as an instruction ("Package IDs: `<Domain><System>`, e.g. `SDS4`"; package IDs allow letters and digits only).
- In the **Naming** table, end each Convention cell that has a regular pattern with its anchored
  regular expression in a code span, e.g. "Domain, source, target, object:
  `^[A-Z]{2}_[A-Za-z0-9]+_[A-Za-z0-9]+_[A-Za-z0-9]+$`".
  Tools (CPI Studio's creation wizard, reviews) read the first `^…$` span of the cell and check
  new IDs and names against it, so it must match every example in the row and the flows the rule
  counts: test it against the IDs in discovery.json before you write it. Use only `^ $ [] () | ? * + {}`
  and `\d`; no lookarounds. No regex for a row without a regular pattern (write "unknown"), and none
  for an **observed** rule.
  Put the Integration flow ID regex of a **rule** also into `.cpi/lint.yaml` (`naming: {iflowId: '<regex>'}`),
  so `lint` reports flows that don't follow it.
- Give the evidence: how many flows follow it and one or two example IDs.
- Mark the strength: **rule** (≥ 80 % of the flows), **common** (≥ 50 %), **observed**
  (less, or contradicting variants). Never promote a minority pattern to a rule.
- Write "unknown" instead of guessing. Leave an `Open questions` entry for the user instead.

Keep it short and concrete (one to three screens). The plan, build and review skills read
it in every task.

Fill the **Templates** section with proposals (status `proposed`): per common pattern (sender
and receiver adapter combination in `summary.triggers` / the graph), the flow that best follows
the conventions you found: exception subprocess as usual, parameters externalised, shared
scripts used, `runtimeStatus` STARTED, no errors in discovery. Prefer flows the team names as
good examples. Never propose SAP standard content.

Also create, if missing:

- `.cpi/README.md`: one paragraph on what the folder is (see the template's footer).
- `.cpi/tests/` (empty, used by cpi-test).
- `.cpi/templates/scripts/`: after the user agreed, the shared logging and error handling
  scripts (`summary.sharedScripts`, read with `get_resource`), one file each, with a comment
  line naming the flows they were taken from. New flows copy them instead of writing new ones.

Do not commit discovery.json if the user does not want tenant inventory in Git; ask once and add
it to `.gitignore` when they say so.

## 4. Hand over

Show the user the proposed templates, the rules marked **observed** and the open questions, and ask them to confirm or
correct them. Apply their answers to the file. Tell them that they can add rules at any time
(for example "always set SAP_ApplicationID") and that the other skills will follow them.
