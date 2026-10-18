---
name: cpi-improve
description: Find and apply improvements across SAP Cloud Integration flows - all packages, a package or single flows. Runs lint (reuse, Partner Directory candidates, dead weight, simplification, robustness, performance, configuration, hygiene), groups the findings into proposals such as moving shared scripts into script collections, routing tables into the Partner Directory or removing unused steps, writes them to .cpi/improvements, and after approval applies them one proposal at a time (lint_fix for mechanical changes, cpi-build for the rest), then tests and reviews. Use when the user asks to clean up, refactor, harmonise or improve flows, reduce duplication, or check best practices across many flows.
inputs:
  - id: scope
    label: Scope
    kind: select
    required: true
    options: [all, packages, flows]
    default: packages
  - id: packages
    label: Packages
    kind: multiselect
    source: packages
    help: With scope packages.
  - id: flowIds
    label: Flows
    kind: text
    help: With scope flows; flow IDs, comma-separated.
---

# Improve integration flows

**Inputs:** scope (`scope`, required, all / packages / flows, default packages); packages (`packages`); flows (`flowIds`). The same list is in the front matter (`inputs`) for tools
such as CPI Studio, which ask for them in one form. Use the values the user or the calling tool
already gave, find the rest yourself where you can, and ask only for what is still missing, in
one question.

Two phases: **propose** (read only) and, after the user approves, **apply** (one proposal at a
time). Never change files or the tenant before the user has approved a proposal.

## 1. Scope and context

- Ask for the scope if it is not given: all packages, one package, or a list of flows. A first run
  on a large repository: all packages, but propose the work package by package.
- Read `.cpi/conventions.md` (house rules beat general advice) and `.cpi/lint.yaml` if it exists
  (rules switched off, thresholds, script collection names, `crossPackage`).
- The repository is the tenant layout (`packages/<pkg>/<artifact>`). Work on a clean Git tree.

## 2. Find

1. Call `lint` for the scope (`packages` / `artifacts`; the cross-flow rules always see every
   flow). For a large result, first look at the counts per rule (`byRule`), then fetch the rules
   you work on with `rules: [...]`.
2. Add context where it changes the decision:
   - `graph_neighbors` / `graph_search`: who calls a flow, which flows share a script, header or
     Partner Directory parameter (impact of a change).
   - `drift`: flows edited on the tenant must be snapshotted first, or the improvement overwrites
     those edits.
   - `check_guidelines` / `validate_artifact` for the tenant's own design guidelines.
   - `pd_dependencies` before proposing Partner Directory changes.
3. Read the files behind the top findings before proposing (a finding is a hint, not a verdict).
   [improvement-catalogue.md](improvement-catalogue.md) says per rule when to act and how.

## 3. Propose

Group findings into **proposals** by what a person would approve, not one per finding:

| Proposal | Built from |
|----------|-----------|
| One script collection per cluster of shared scripts (package or shared) | `duplicate-script`, `use-script-collection`, `similar-script`, `large-script` |
| Documentation of script collections, so they are found and reused | `collection-script-doc`, `collection-script-doc-quality`, `collection-script-keywords` |
| One shared mapping artifact per duplicated mapping | `duplicate-mapping` |
| One Partner Directory design per routing table, lookup table or flow family | `router-literals`, `lookup-table-in-script`, `deployment-copies`, `hardcoded-endpoint` |
| A dead-weight sweep per package | `unconnected-step`, `unused-*`, `noop-content-modifier`, `property-never-read`, `dead-local-process` |
| Robustness and security per flow | `no-exception-subprocess`, `exception-subprocess-message-end`, `swallowed-exception`, `hardcoded-secret`, `payload-attachment` |
| Simplifications per flow | `consecutive-content-modifiers`, `converter-roundtrip`, `trivial-script` |
| Hygiene in passing (only with another change to the flow) | `outdated-component`, `default-step-name`, `naming` |

For each: affected flows, the change, benefit, risk, effort (S/M/L), test plan, and whether
`lint_fix` can do it. Order by benefit and risk: secrets and robustness first, then reuse with many
flows, then Partner Directory designs, then dead weight, then the rest.

Write them to `.cpi/improvements/<date>-<scope>.md` from
[proposal-template.md](proposal-template.md) and present the summary table. Background for the
two big themes: [script-collections.md](script-collections.md) and
[partner-directory-patterns.md](partner-directory-patterns.md).

Ask the user which proposals to apply. Findings the user decides to keep go into `.cpi/lint.yaml`
under `ignore` (with a comment why), or into the baseline with `cpictl lint --update-baseline`.

## 4. Apply (per approved proposal)

One proposal = one branch / pull request. For each:

1. `drift` for the affected flows; stop if a flow was changed on the tenant.
2. Change:
   - mechanical (scripts into collections, unused scripts, unconnected steps, no-op content
     modifiers): `lint_fix` with `dry_run: true`, show the changes, then without dry run, scoped
     with `packages` / `artifacts` and `rules` to this proposal;
   - everything else: edit the files as in the cpi-build skill (one flow at a time).
3. `layout_iflow` for every `.iflw` the proposal changed (removed or added steps leave gaps and
   crooked lines); a proposal "tidy the diagrams" is `lint_fix` with `rules: ["layout"]`.
4. `bump_versions` with `changed: true` (when the repository uses versioning manifest).
5. `lint` again for the affected flows: the proposal's findings are gone, nothing new appeared.
6. Upload and deploy on the **development** tenant, dependencies first: script collections and
   mapping artifacts, then Partner Directory parameters (`pd_deploy`, dry run first), then the flows
   (`upload_artifacts`, `validate_artifact`, `deploy`).
7. Test every affected flow with the cpi-test skill (also flows that only use a changed script
   collection). Compare behaviour before and after: same output for the same input.
8. cpi-review for larger changes. Set the proposal's status to `done` (or `blocked` with the
   reason) in the proposal file.

Use `loop_start` / `loop_end` for the build/test loop. Never change QA or production, never delete
Partner Directory parameters, and never move or delete credentials.
