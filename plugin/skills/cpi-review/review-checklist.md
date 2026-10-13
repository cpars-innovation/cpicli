# Review checklist for integration flows

Apply `.cpi/conventions.md` first; its "Review checklist additions" are mandatory. Then:

## Tenant checks
- `validate_artifact` PASSED.
- `check_guidelines`: every violation is fixed or justified.
- Deployed version equals the reviewed version (`get_runtime_status`), status STARTED.
- `lint`: no `component-newer-than-tenant` (a step or adapter newer than the target tenant
  offers); new steps use the current versions from `list_components`, Groovy scripts version 2.
- Nobody holds an edit lock on the flow (`list_locks`).

## Conventions
- Package, flow, parameter, credential and script names follow conventions.md.
- Shared scripts are reused (identical copy or script collection), not modified copies.

## Design
- Matches the plan; deviations are explained.
- No unreachable steps, every route of a router has a sensible end, default routes exist.
- The diagram is readable: `lint` reports no `layout` finding for the flow (otherwise
  `layout_iflow` / `cpictl iflow layout` fixes it).
- Exception subprocess present where the conventions require it; the sender gets a defined
  answer on errors; errors are not swallowed silently.
- Idempotency / duplicate handling where messages can be resent.
- Callers still fit: for a changed address, payload or header, the flows that call this one
  (`graph_neighbors`, `direction: "in"`, `edge_types: ["sends_to"]`) were checked or adapted.
- Large payloads: no needless conversions to String, streaming where possible.

## Configuration and security
- Environment-specific values are externalised; no hard-coded hosts, paths or credential
  names that differ per environment.
- No secrets, tokens or personal data in scripts, parameters, properties or logs.
- Credentials and key aliases the flow refers to exist (`list_credentials`, `list_keystore`)
  and certificates are not about to expire.
- Sender authorization (role-based / client certificate) as the conventions require.

## Logging and monitoring
- Log level as in the conventions; payload logging only where allowed and with personal data
  considered.
- Custom header properties (e.g. SAP_ApplicationID, business keys) written as the conventions say.

## Scripts and mappings
- Scripts: null checks, no `println`, exceptions with meaningful messages, no unused imports,
  no reading the whole body as String for large messages unless needed.
- Every script says what it does (comment at the top or above its function).
- Script collections: each changed or new public function has a doc comment (purpose, reads,
  writes, errors, how to call it) that matches the code, and tests in
  `tests/scripts/<package>/<collection>/` call it; lint shows no `collection-script-doc`,
  `collection-script-tests` or `collection-script-coverage` finding for it.
- Mappings: mandatory fields, value mappings instead of hard-coded code lists.

## Tests
- Test cases exist in `.cpi/tests/<FlowId>/` for the happy path and the error paths; they pass.
- A test entry (ProcessDirect `/test/<FlowId>`) is present or absent as the conventions require.
- The log level is not left on TRACE/DEBUG where the conventions require INFO.

## Report format

For each finding: severity (**blocker** / **major** / **minor** / **nit**), location (file and
element id, or tenant object), what is wrong, why it matters, suggested fix. End with a verdict:
ready / ready after fixes / not ready.
