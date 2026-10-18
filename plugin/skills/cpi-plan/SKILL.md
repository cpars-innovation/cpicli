---
name: cpi-plan
description: Plan a new or changed SAP Cloud Integration flow before building it - clarify the interface (sender, receiver, protocols, payloads, mapping, error handling, volumes), decide the package, IDs and steps according to .cpi/conventions.md, and write the design to .cpi/plans/<FlowId>.md. Use when the user describes an integration requirement or asks for a design, an estimate or "how would we build ...".
inputs:
  - id: requirement
    label: What should the flow do?
    kind: text
    required: true
    help: Sender, receiver, trigger and the business purpose, or the path of a brief in .cpi/briefs.
  - id: package
    label: Package
    kind: select
    source: packages
    help: Leave empty to let the plan propose one from the conventions.
  - id: flowId
    label: Flow ID
    kind: text
    checks: [artifact-id-free, naming]
    help: Leave empty to let the plan propose one from the naming conventions.
---

# Plan an integration flow

**Inputs:** what should the flow do (`requirement`, required); package (`package`); flow ID (`flowId`). The same list is in the front matter (`inputs`) for tools
such as CPI Studio, which ask for them in one form. Use the values the user or the calling tool
already gave, find the rest yourself where you can, and ask only for what is still missing, in
one question.

The goal is a design that the cpi-build skill can implement without guessing, and that the
user has agreed to. Do not touch the tenant in this phase except for read-only lookups.

## 1. Read the context

- The brief, if there is one: `.cpi/briefs/<name>.md` or a file the user names. For a new flow
  without a brief, offer one: copy `.cpi/templates/brief.md` to `.cpi/briefs/<short-title>.md`
  and let the user fill it in. If the team has no `.cpi/templates/brief.md` yet, create it first
  from [brief-template.md](brief-template.md) (the team adapts its copy). Small changes and quick
  questions need no brief.
- A decision log or an architecture proposal, if the user has one (from a design workshop or an
  architecture review): its decisions are settled, do not ask them again; take the chosen option,
  carry its source labels into the plan (see Provenance) and turn its open items into Open points.
  When it contradicts conventions.md or the tenant, say so instead of picking one silently.
- `.cpi/conventions.md` (if missing, suggest running the cpi-discover skill first; without it,
  say which choices are your assumptions).
- The reference flow for the pattern: the **Templates** section of conventions.md. Use an
  `agreed` one; a `proposed` one only after telling the user.
- Similar existing flows: `.cpi/discovery.json` lists adapters and steps per flow. Find flows
  with the same sender/receiver adapters and reuse their structure. Name them in the plan.
  `graph_search` finds flows, addresses and systems quickly (e.g. the receiver host, a
  ProcessDirect address); `graph_neighbors` shows which flows already call a system or share a
  credential.
- Changing an existing flow: `graph_neighbors` with `direction: "in"`, `edge_types:
  ["sends_to"]` lists the flows that call it; a changed address or payload affects them. Name
  them in the plan.
- Existing packages (`list_packages`) and credentials (`list_credentials`) that the flow can use.

## 2. Clarify

Find facts yourself (conventions, discovery, graph, tenant reads, the brief); ask the user only for
decisions and for what no tool can tell. With a brief, ask only for its empty fields marked (*) and
for contradictions. IDs, names and descriptions you propose from the naming conventions; the user
confirms them.

Work the open decisions as a tree, in rounds: a decision often depends on another (the error
response depends on sync or async, the retry design on whether the receiver is idempotent).
Each round, ask every question whose prerequisites are already answered, numbered, each with
your recommended answer and why; hold back the ones that depend on an answer still open. After the
answers, the next round. Stop when nothing is open or the user wants to stop; record what is still
open under Open points. Typical decisions:

- Trigger: who sends (system, protocol, sync/async), or a timer / polling?
- Receiver(s): systems, protocols, authentication (which credential or certificate exists?).
- Payloads: formats, sample messages or schemas (ask for files), mapping rules.
- Behaviour on errors: what the sender must get back, retries, alerting.
- Volumes and sizes, ordering, idempotency (duplicates).
- Which receivers must not be called from tests.

### Provenance

Never invent an API, OData service, entity set, event, IDoc type, BAdI, SAP Note or system name.
Each one in the plan says where it comes from:

- **tenant**: an existing flow, adapter or credential (name the flow or the tool);
- **SAP documentation**: found in this session through a documentation or API catalogue tool
  (name it and the page or API);
- **user**: a document, schema or sample the user gave;
- **new**: proposed by this plan (a new flow, address, parameter or custom service).

Memory can tell you where to look; it is not a source. If nothing confirms a name, write
"unverified" next to it and add an Open point.

## 3. Write `.cpi/plans/<FlowId>.md`

```markdown
# <Flow name> (<FlowId>)
Package: <PackageId> (exists | new)    Status: draft | agreed
## Purpose
## Interface
Sender: <adapter, address/path, auth>      Receiver(s): <adapter, address, credential>
## Message flow
1. <step> - <component type, e.g. Content Modifier / Groovy script / Message Mapping> - why
## Error handling and logging      (follow conventions.md; name the deviations)
## Externalised parameters        (key, meaning, value per environment if known)
## Security material needed       (credentials/certificates to create with `cpictl credentials`)
## Test cases                     (input -> expected HTTP status, message status, output)
## Reference flows                (existing flows this design copies from)
## Open points
```

APIs, services, events and IDoc types in the plan carry their source (tenant, SAP documentation,
user, new) as in Provenance above.

- IDs and names must follow conventions.md; spell them out, the builder will not invent them.
- Prefer components and scripts the tenant already uses over new ones. Every deviation from
  a convention needs a reason in the plan.
- List test cases for the happy path and at least one error case per receiver and per
  validation rule. The cpi-test skill turns them into executable cases.

## 4. Agree

Present the plan in a few lines (interface, steps, open points) and ask for confirmation.
Set `Status: agreed` only after the user said so. Building starts with the cpi-build skill.
