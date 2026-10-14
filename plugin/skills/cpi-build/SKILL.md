---
name: cpi-build
description: Build or change an SAP Cloud Integration flow and get it deployed - edit the flow's files locally (.iflw model, scripts, mappings, parameters), upload, validate, deploy and fix until the runtime starts it, following .cpi/conventions.md and the plan in .cpi/plans. Use when the user asks to implement, change, fix or deploy an integration flow.
---

# Build an integration flow

Work on local files in the repository and use the cpi MCP tools to move them to the tenant.
Read [iflow-structure.md](iflow-structure.md) before editing a model for the first time, and
[editing-pitfalls.md](editing-pitfalls.md) before changing an existing flow: review the whole flow
you touch, keep headers absent/empty/set exactly as before, and the model details that break the
editor. HTTP receivers: [http-receiver.md](http-receiver.md).

## Before you start

1. Read `.cpi/conventions.md` and the plan `.cpi/plans/<FlowId>.md` if it exists. A change
   without a plan is fine for small fixes; for a new flow, run cpi-plan first.
2. Make sure you work on a **development tenant** (ask if unclear). Never deploy to a
   productive tenant from this skill.
3. Run `drift` on the content folder: `tenant_newer` or `diverged` means someone changed the
   flow on the tenant; download it and merge before you upload, or their change is lost.
   Changing the address, payload or headers of an existing flow? `graph_neighbors` (node the
   flow, `direction: "in"`, `edge_types: ["sends_to"]`) lists the flows that call it; tell the
   user before you break them.
   `list_locks` with the flow's ID: if someone holds the edit lock, they have the flow open in
   the Web UI and an upload either fails or their unsaved work is lost. Tell the user who holds
   it since when and wait. Never remove a lock unless the user asks for exactly that
   (`unlock_artifact` exists only when the server was started with it).
4. Get the files:
   - existing flow: `download_artifact` into the repository's content folder (convention, or
     `content/<PackageId>/<FlowId>`), unless the files are already there and up to date;
   - new flow: `copy_iflow` from the reference flow named in the plan (else the one for the
     pattern in the Templates section of conventions.md, else the closest flow in
     discovery.json) with the plan's ID, name, description and new sender address(es); target
     `content/<PackageId>/<FlowId>` or the folder the conventions name. Then check the files in
     `remaining` and adapt receivers, steps and parameters. Never rename files by hand and do
     not write a model from scratch. Upload without `name`: it comes from the manifest.
   - scripts: start from `.cpi/templates/scripts/` and the reference flow's scripts; write a
     new script only for logic that none of them covers. New scripts use the Groovy script step
     version 2 (Groovy 4, `package script.v2`, `com.sap.it.script.v2.api.Message`, files under
     `script/v2/`); see iflow-structure.md. Before writing a function, search the script
     collections for one that does it (`graph_search` with `types: ["script"]` and words of the
     purpose: collection scripts carry their functions and doc comments) and call it from the
     step instead of copying it.
   - every script says what it does: a comment at the top or above its function. A function you
     add to or change in a **script collection** gets a doc comment (purpose, reads, writes,
     errors, how to call it) and a test in `tests/scripts/<package>/<collection>/`; see
     [script-tests.md](script-tests.md). lint checks both (`collection-script-doc`,
     `collection-script-tests`, `collection-script-coverage`).
   - OData or IDoc channels (S/4HANA, SuccessFactors, backend IDocs): read
     [sap-adapters.md](sap-adapters.md) first: adapter properties, paging, CSRF, `$batch`, EDMX and
     IDoc schemas, the control record, `SAP_ApplicationID` = IDoc number, idempotency.

## The loop

Open it with `loop_start` (goal from the plan) before the first change on the tenant. The
server then enforces the limits (iterations, repeated errors, time, deploys): when a tool answers
`errorCategory: "stopped"`, stop changing things, call `loop_end` with the outcome and give the
user the summary (`.cpi/loops/<loop_id>.md`). Call `loop_end` as well when the flow works.

1. Edit the local files. Keep changes minimal and consistent with the conventions.
   After changing an `.iflw` (steps or sequence flows added, removed or rewired), call
   `layout_iflow` on it: the server lays out the diagram (flow order, branches, no overlaps,
   right-angled lines). Do not compute or edit coordinates yourself; a new element only needs a
   `BPMNShape` / `BPMNEdge` (without bounds is fine), `layout_iflow` places it.
2. New package? `create_package` (never changes an existing one).
3. `upload_artifact` (action UNCHANGED means the tenant already has exactly these files).
4. `validate_artifact`. On FAILED read the details, fix, go back to 1. Then `check_guidelines`:
   fix violations the conventions care about now; list the others for the review.
5. `deploy`. On FAILED the result's `error` is the runtime error (for example a missing
   credential, an unknown key alias, a script compile error, a port or path conflict).
   Fix the files and go back to 1. Missing security material is not yours to create:
   tell the user which credential or certificate to deploy with `cpictl credentials` /
   `cpictl keystore` and stop.
6. Parameters only: `set_parameters` then `deploy` (no upload needed). Value mapping entries:
   `get_value_mapping`, then `set_value_mapping` (confirm) and `deploy` the value mapping.
7. When the flow is DEPLOYED, continue with the cpi-test skill.

Stop and ask after three failed attempts at the same error, or when the fix needs a decision
the plan does not cover.

## Rules

- Never invent component XML. Copy an element of the same type from a flow of this tenant
  (discovery.json lists which flows use which component) and adapt its properties.
- Component versions: `list_components` (name, kind, direction) gives the `cmdVariantUri` and
  version the tenant offers for every adapter and step. Use those for new elements and never a
  newer version: the tenant's Check and deploy fail (`lint` reports it as
  `component-newer-than-tenant`). No catalogue yet? Run `discover_tenant` (it learns the versions
  in use) and ask the user to import the editor metadata (`cpictl components import`, see
  docs/components.md) for the exact list.
- Externalise everything that differs between environments (hosts, paths, credential names,
  directories) as `{{Parameter}}`, with keys named as in the conventions.
- Do not put secrets, tokens or productive data into files, parameters or test messages.
- Keep `Bundle-SymbolicName` in META-INF/MANIFEST.MF equal to the flow ID.
- Report what you changed (files, parameters, deployed version) at the end, and say what you
  checked locally, what on the tenant, and what you did not run.
