---
name: cpi-test
description: Set up and run tests of a deployed SAP Cloud Integration flow - write test cases with sample messages under .cpi/tests/<FlowId>/, send them with send_test_message, check HTTP status, message status, response and logs, and diagnose failures down to the failing step. Use after a deployment, when the user asks to test a flow, to add test cases, or to find out why messages fail.
---

# Test an integration flow

Tests run against a deployed flow on a **development tenant** and trigger real processing,
including calls to receivers. Check `.cpi/conventions.md` (Testing) for receivers that must
not be called and for test data rules. Never use productive data.

## How the message gets in

Look up the flow's trigger: `iflows[].triggers` in `.cpi/discovery.json` (refresh with
`discover_tenant`), or the sender adapters in the local `.iflw`. Then choose:

| Trigger | Route |
|---------|-------|
| HTTPS / SOAP / REST | `send_test_message` with `artifact_id` |
| OData sender | `send_test_message` with `artifact_id` and the OData method, entity set, key and query ([sap-adapters.md](../cpi-build/sap-adapters.md#testing)) |
| IDoc sender | `idoc_template` for the control record, add the data segments, then `send_test_message` with `artifact_id` and `idoc`; also send the same DOCNUM twice (idempotency) |
| ProcessDirect | `send_test_message` with `artifact_id` = the flow and `process_direct_address` = its address, through the test harness |
| Timer, run once | `deploy`, then `list_message_logs` (artifact_id, since = deploy time, wait_seconds) |
| Timer scheduled, SFTP, mail, JMS, ... | the test entry (below), or ask the user to provide the input and observe with `list_message_logs` |

### Test harness (ProcessDirect)

If `send_test_message` reports that the harness `CPICTL_Test_Harness` is not deployed, offer to
build it on the development tenant (ask first): HTTPS sender `/cpictl/test` -> Request Reply ->
ProcessDirect receiver with address `${header.CpictlTargetAddress}`; Allowed Header(s)
`CpictlTargetAddress` plus the test headers. Use the cpi-build loop and copy the HTTPS sender and
the ProcessDirect receiver from existing flows of the tenant (discovery: `triggers` with HTTPS,
`processDirectCalls`). Put it in a test tooling package that is never transported. Details in
the cpictl docs (docs/testing.md).

### Test entry (polling and scheduled flows)

Check the Testing section of conventions.md for the team's decision. If test entries are
allowed: move the logic into a local integration process, keep the original trigger calling it,
and add an integration process with a ProcessDirect sender `/test/<FlowId>` that calls it too.
Test it through the harness. If the conventions say test entries are removed before transport,
note it in the plan so cpi-build removes it and cpi-review checks it. Never add a test entry
without the user's agreement when the conventions do not say anything; ask and record the answer
in conventions.md.

## Test cases

One folder per flow, one YAML file per case, payloads next to it:

```
.cpi/tests/<FlowId>/
├── valid-order.yaml
├── valid-order.xml
└── missing-customer.yaml
```

```yaml
# .cpi/tests/OrderIntake/valid-order.yaml
description: Valid order is accepted and forwarded
request:
  method: POST                      # default POST
  content_type: application/xml
  headers: { X-Test-Case: valid-order }
  body_file: valid-order.xml        # or body: "<inline/>"
  endpoint: ""                      # only if the flow has several endpoints (list_service_endpoints)
  process_direct_address: ""        # ProcessDirect flows / test entries: send through the harness
expect:
  http_status: 200
  message_status: COMPLETED         # COMPLETED, FAILED, ESCALATED, ... ("" = do not wait)
  response_contains: ["<Status>OK</Status>"]
  error_contains: []                # for negative cases: text expected in the error
  custom_headers: { OrderId: "4711" }   # custom header properties expected in the message log
```

Write cases from the plan's test case list (`.cpi/plans/<FlowId>.md`): the happy path, each
validation rule, each receiver error the flow handles. Use small, synthetic payloads; ask the
user for real-world samples with anonymised data if the mapping needs them.

## Running

First the static checks, once per test run, on the version that is deployed:

- `validate_artifact` (the tenant's check of the model, as *Check* in the Web UI). FAILED means
  the flow is not ready: report it and fix it with the cpi-build loop before sending anything.
- `check_guidelines` (the tenant's activated design guidelines). List every violation in the
  report; they do not stop the run, but each one must be fixed or justified before the review.
- `get_runtime_status`: the flow is STARTED and the runtime version equals the version you test.

Then for each case:

1. `send_test_message` with artifact_id, body, content_type, headers and
   `wait_seconds: 60` (more for slow receivers). The result has `httpStatus`, `response`,
   `messageGuid` and, after the wait, `log` (status, error text, custom headers, attachments).
2. Compare with `expect`. A case passes only if every expectation holds.
3. On a mismatch:
   - `get_message_log` with the GUID: for a failed message `errorInformation` names the last
     model step that failed (`lastErrorModelStepId`) and the adapter attributes show what the
     receiver answered.
   - `get_message_steps` with the message GUID: the first failing step and its `modelStepId`
     (the id of the element in the .iflw); `properties: true` adds each step's properties.
   - `get_message_log` / `get_message_attachment` / `get_message_store_entry` for the payload
     at the logged points.
   - Still unclear what happens between the steps: `set_log_level` TRACE, send the case
     again within 10 minutes, `get_message_trace` with the message GUID and
     `get_trace_message` for payload and headers before and after the failing step.
     Set the level back (`INFO`) afterwards if the conventions ask for it.
   - Decide whether the flow or the test is wrong. Fix the flow with the cpi-build loop,
     never weaken an expectation just to make the test pass. Ask the user when the expected
     behaviour is unclear.

Asynchronous chains (JMS, ProcessDirect to further flows): the case's `messageGuid` covers the
first flow only. Follow the message with `get_trace_tree` (message_guid, key_headers), or find
every message with the same business key with `find_messages_by_header` (for example
`OrderNo=4711`, one call, no scan). A message that waits in a JMS queue after a failure shows in
`list_jms_messages` (queue, `failed_only`); `retry_jms_messages` (confirm) runs it again after the
fix. Ask before retrying on a shared tenant.

Report the static checks (validation status, guideline violations) and a table: case, result,
HTTP status, message status, message GUID, reason.

## Errors that are not the flow's fault

- `auth` on send_test_message: the runtime credentials lack the role ESBMessaging.send or are
  missing (`CPICTL_RUNTIME_OAUTH_CLIENTID/SECRET`, a service key of plan integration-flow). Ask
  the user; do not retry.
- "has no endpoint": the flow is not deployed or has no HTTP-based sender. Timer or polling
  flows (SFTP, mail, JMS) are tested by placing input where the sender reads it; ask the user
  how and use `list_message_logs` with `since` and `wait_seconds` to observe the run.
