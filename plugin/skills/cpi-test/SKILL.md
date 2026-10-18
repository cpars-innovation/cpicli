---
name: cpi-test
description: Set up and run tests of a deployed SAP Cloud Integration flow - write test cases with sample messages under .cpi/tests/<FlowId>/, run them with run_tests (HTTP status, message status, response, custom headers, steps, receiver stub calls), and diagnose failures down to the failing step. Use after a deployment, when the user asks to test a flow, to add test cases, or to find out why messages fail.
inputs:
  - id: flowId
    label: Flow ID
    kind: text
    required: true
  - id: tier
    label: Development tenant
    kind: select
    source: tiers
  - id: installTools
    label: Install the test tooling if missing
    kind: boolean
    default: false
---

# Test an integration flow

**Inputs:** flow ID (`flowId`, required); development tenant (`tier`); install the test tooling if missing (`installTools`, default false). The same list is in the front matter (`inputs`) for tools
such as CPI Studio, which ask for them in one form. Use the values the user or the calling tool
already gave, find the rest yourself where you can, and ask only for what is still missing, in
one question.

Tests run against a deployed flow on a **development tenant** and trigger real processing,
including calls to receivers. Check `.cpi/conventions.md` (Testing) for receivers that must
not be called and for test data rules. Never use productive data.

## How the message gets in

Look up the flow's trigger: `iflows[].triggers` in `.cpi/discovery.json` (refresh with
`discover_tenant`), or the sender adapters in the local `.iflw`. Then choose:

| Trigger | Route |
|---------|-------|
| HTTPS / SOAP / REST | a case with `request.body`; a flow that routes on the path below its address (SOAP `/cxf/out/<route>`, HTTPS `/x/*`): add `path` (e.g. `"test"`) |
| OData sender | a case with `request.odata` {method, entity_set, key, query} ([sap-adapters.md](../cpi-build/sap-adapters.md#testing)) |
| IDoc sender | `idoc_template` for the control record, add the data segments, then a case with `request.idoc_file`; also a case with the same DOCNUM twice (idempotency) |
| ProcessDirect | a case with `request.process_direct_address` = the flow's address, through the test harness |
| Timer, run once | `deploy`, then `list_message_logs` (artifact_id, since = deploy time, wait_seconds) |
| Timer scheduled, SFTP, mail, JMS, ... | the test entry (below), or ask the user to provide the input and observe with `list_message_logs` |

### Test harness (ProcessDirect)

If `run_tests` (or `send_test_message`) reports that the harness `CPICTL_Test_Harness` is not deployed, offer to
install it on the development tenant (ask first): `install_test_tools` with `deploy: true`
(CLI: `cpictl test tools install --deploy`). It installs the harness that ships with cpictl
(HTTPS sender `/cpictl/test` -> Request Reply -> ProcessDirect receiver
`${header.CpictlTargetAddress}`) and the receiver stub into the test tooling package
`CPICTLTesting`, which is never transported. Do not build the harness by hand. It is refused on
a production tier. On a shared tenant, ask which user role should protect the tooling and pass it
as `role` (the runtime credentials must have it); without `role` the configured one stays.

### Receiver stub (simulated receivers)

To test what a flow sends to a receiver, or how it handles a receiver's answer or error, without
calling the real system: install the tooling (above), point the receiver's address (an
externalised parameter: `set_parameters`, then `deploy`) at the stub URL from `cpictl test tools
status`, `<runtime>/http/cpictl/stub/<rule>/...`, and set the answer per rule with
`set_stub_response` (status, content_type, body, headers; `clear` for the default 200 `{}`).
Each request is recorded in the stub's message log (`CPICTL_Test_Stub`): attachment
`cpictl-stub-request` (method, path, query, headers without sensitive ones, body) and custom
headers `CpictlStubRule` / `CpictlStubPath`; read it with `list_message_logs` (artifact
`CPICTL_Test_Stub`, since) and `get_message_attachment`. In test cases, put the stub URL into the flow's
`config.yaml` and check the calls with `stub_calls` (`run_tests` with `apply_config` sets and
restores the addresses). By hand: set the receiver address back after the test unless the
conventions keep it on the development tenant.

### Test entry (polling and scheduled flows)

Check the Testing section of conventions.md for the team's decision. If test entries are
allowed: move the logic into a local integration process, keep the original trigger calling it,
and add an integration process with a ProcessDirect sender `/test/<FlowId>` that calls it too.
Test it through the harness. If the conventions say test entries are removed before transport,
note it in the plan so cpi-build removes it and cpi-review checks it. Never add a test entry
without the user's agreement when the conventions do not say anything; ask and record the answer
in conventions.md.

## Test cases

Write every test as a case file and run them with `run_tests`; do not send case messages by hand
with `send_test_message` (keep that for exploring a single message). One folder per flow, one
YAML file per case, payloads next to it:

```
.cpi/tests/<FlowId>/
├── valid-order.yaml
├── valid-order.xml
├── missing-customer.yaml
└── config.yaml            # optional: parameter values for the test (apply_config)
```

```yaml
# .cpi/tests/OrderIntake/valid-order.yaml
description: Valid order is accepted and forwarded
tags: [smoke]
request:
  method: POST                      # default POST
  content_type: application/xml
  headers: { X-Test-Case: valid-order }
  body_file: valid-order.xml        # or body: "<inline/>"
  # url: ...                        # only if the flow has several endpoints (list_service_endpoints)
  # path: test                      # flows that route on the path below their address
  # process_direct_address: /orders/in   # ProcessDirect flows / test entries: through the harness
  # idoc_file: orders.xml           # IDoc sender (idoc_template writes a skeleton)
  # odata: { method: POST, entity_set: SalesOrders }   # OData sender
  wait: 60s                         # how long to wait for the message log
expect:
  http_status: 200                  # default: any 2xx (unless error_contains or a failed message_status)
  message_status: COMPLETED         # COMPLETED, FAILED, ESCALATED, ... (omit: not checked)
  response_contains: ["<Status>OK</Status>"]
  response_not_contains: ["<Error>"]
  response_xpath: { /Order/Status: OK }          # /a/b, //name, [@attr='v'], /@attr
  response_json: { order.status: OK, order.items.0.qty: 2 }
  error_contains: []                # negative cases: text expected in the error
  response_headers: { X-Order-Status: accepted }   # headers of the answer (null: must be absent)
  custom_headers: { OrderId: "4711" }   # custom header properties expected in the message log
  steps_include: [Map Order]        # step names or model step IDs the message passed
  max_duration: 10s
```

Other keys: `skip: <reason>`, `order: <n>` (sorts the cases of a flow), `flow:` (default: the
folder name). Unknown keys are errors (`list_tests` reports them with file:line), so check the
cases with `list_tests` after writing them. The full schema is docs/test-case.schema.json of
cpictl.

Write cases from the plan's test case list (`.cpi/plans/<FlowId>.md`): the happy path, each
validation rule, each receiver error the flow handles. A negative case sets `http_status` (e.g.
500) and/or `message_status: FAILED` and `error_contains`. Use small, synthetic payloads; ask the
user for real-world samples with anonymised data if the mapping needs them.

### Receivers during tests

Receivers that must not be called (conventions.md, Testing) or that should be checked point at
the receiver stub `CPICTL_Test_Stub` during the run: put the receiver parameters in
`.cpi/tests/<FlowId>/config.yaml` (e.g. `ERP_URL: https://<runtime host>/http/cpictl/stub/erp`;
receivers of the flows it calls go under `flows: {Orders_Route: {ERP_URL: ...}}`)
and run with `apply_config: true`; the run sets them, redeploys, runs the flow's cases and
restores the previous values (also when a case fails). Assert what the flow sent:

```yaml
expect:
  stub_calls:
    - { rule: erp, method: POST, body_contains: ["<OrderNo>4711</OrderNo>"], count: 1 }
```

Cases with `stub_calls` run alone. Ask before `apply_config` on a shared tenant: the flow is
redeployed twice.

### Script cases

To test one script function without a tenant, write a script case:
`.cpi/tests/<FlowId>/scripts/<script file>/<case>.yaml` (a flow's script step) or
`.cpi/tests/collections/<CollectionId>/<case>.yaml` (a collection function; `call.function`,
`call.args`). Give `input` (body, headers, properties), the service answers the script reads (`pd`,
`value_mappings`, `secure`, `keystore`: test values only) and `expect` (body, headers and properties
with `null` for absent, `returns`, `throws`, `log`). Run them with `run_script_cases` (needs the
project set up with `cpictl test scripts init`). Format: docs/script-case.schema.json and
docs/testing.md (Script cases). Cover the absent and empty header cases separately.

### XSLT mappings in the Partner Directory

For an XSLT mapping stored as a Partner Directory binary (`<PID>/<ID>`), write cases as
`.cpi/tests/pd/<PID>/<ID>/<case>.yaml` (`input`, `headers` / `properties` as `xsl:param`,
`expect.output` / `output_xpath` / `error_contains`; docs/xslt-case.schema.json) and run them
with `cpictl test xslt --saxon-jar <jar>` (Java and a Saxon-HE jar needed; ask the user for the
jar path). Nothing goes to the tenant.

## Running

First the static checks, once per test run, on the version that is deployed:

- `validate_artifact` (the tenant's check of the model, as *Check* in the Web UI). FAILED means
  the flow is not ready: report it and fix it with the cpi-build loop before sending anything.
- `check_guidelines` (the tenant's activated design guidelines). List every violation in the
  report; they do not stop the run, but each one must be fixed or justified before the review.
- `get_runtime_status`: the flow is STARTED and the runtime version equals the version you test.

Then `run_tests` with `flow` (and `case` or `tags` to narrow it; `parallel` for many cases). The
result has `passed`, `failed`, `errors`, `skipped` and per case `status`, `httpStatus`,
`messageStatus`, `messageGuid` and, for a failed case, `failures` (expectation, expected, actual)
and `diagnostics`: `lastError`, `errorInformation.lastErrorModelStepId`, `failedStep`
(`modelStepId` = the id of the element in the .iflw) and `traceRoot` (the message's path across
flows). A case with status `error` could not run (unknown flow, credentials, harness missing):
fix that first.

On a failed case:

- Read `diagnostics` first; they usually name the failing step and the receiver's answer.
- `get_message_log` / `get_message_attachment` / `get_message_store_entry` with the message GUID
  for the payload at the logged points; `get_message_steps` with `properties: true` for each
  step's properties.
- Still unclear what happens between the steps: `set_log_level` TRACE, run the case again
  (`run_tests` with `case`) within 10 minutes, `get_message_trace` with the message GUID and
  `get_trace_message` for payload and headers before and after the failing step. Set the level
  back (`INFO`) afterwards if the conventions ask for it.
- Decide whether the flow or the test is wrong. Fix the flow with the cpi-build loop, then run
  the cases again; never weaken an expectation just to make the test pass. Ask the user when the
  expected behaviour is unclear.

Asynchronous chains (JMS, ProcessDirect to further flows): the case's `messageGuid` covers the
first flow only; `diagnostics.traceRoot` shows the messages with the same correlation ID. Follow
the message further with `get_trace_tree` (message_guid, key_headers), or find every message with
the same business key with `find_messages_by_header` (for example `OrderNo=4711`, one call, no
scan). A message that waits in a JMS queue after a failure shows in `list_jms_messages` (queue,
`failed_only`); `retry_jms_messages` (confirm) runs it again after the fix. Ask before retrying
on a shared tenant.

Report the static checks (validation status, guideline violations) and a table from the
`run_tests` result: case, result, HTTP status, message status, message GUID, reason (the
failures). The same run from a terminal or CI: `cpictl test run --flow <FlowId> --junit
report.xml`.

## Errors that are not the flow's fault

- `auth` on send_test_message: the runtime credentials lack the role ESBMessaging.send or are
  missing (`CPICTL_RUNTIME_OAUTH_CLIENTID/SECRET`, a service key of plan integration-flow). Ask
  the user; do not retry.
- "has no endpoint": the flow is not deployed or has no HTTP-based sender. Timer or polling
  flows (SFTP, mail, JMS) are tested by placing input where the sender reads it; ask the user
  how and use `list_message_logs` with `since` and `wait_seconds` to observe the run.
