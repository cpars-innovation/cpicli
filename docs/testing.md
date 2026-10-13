# Testing integration flows

`cpictl send` (MCP: `send_test_message`) sends a message to a deployed flow and waits for its
processing log. How the message gets into the flow depends on the flow's trigger.
`cpictl discover` lists the triggers of every flow (`iflows[].triggers`: adapter and address).

| Trigger | How to test | Change to the flow |
|---------|-------------|--------------------|
| HTTPS, SOAP, REST sender | Send directly to the flow's endpoint | none |
| IDoc sender | `send --idoc file.xml` to the flow's IDoc endpoint | none |
| OData V2 sender | `send --odata-method … --entity-set …` to the flow's OData service | none |
| ProcessDirect sender | Send through the **test harness** to the flow's address | none |
| Timer, "run once" | Deploying is the trigger: deploy, then wait for the log | none |
| Timer, scheduled | Trigger by redeploying with a run-once schedule on the development tenant, or a test entry | parameter or test entry |
| SFTP, FTP, mail, JMS, AMQP, Kafka (polling / queue) | Put input where the flow reads it, or a **test entry** | none, or test entry |

All tests run on a development tenant and trigger real processing, including calls to
receivers. Use synthetic data.

## Direct: HTTP-based senders

```bash
cpictl send --artifact-id OrderIntake --body-file order.xml --content-type application/xml --wait 60s
```

The runtime answers with the message GUID (`SAP_MessageProcessingLogID`), so the result has the
HTTP status, the response and the final message log.

Flows that route on the path below their address (a SOAP sender at `/out/` reads the rest as
`CamelHttpPath`; an HTTPS sender with an address ending in `/*`) are reached with `--path`
(MCP `path`): it is appended to the flow's endpoint. `--url` may also be a URL below a listed
endpoint. Anything else (another host, another flow's path, `..`) is refused: a test message
only ever goes to the flow under test.

```bash
cpictl send --artifact-id UtilitiesBase_GenericSOAP_To_AEM --path test --body-file routing-test.xml \
  --content-type text/xml --wait 60s      # POST .../cxf/out/test
```

## IDoc and OData senders

**IDoc**: `--idoc FILE` (MCP `idoc`: the XML text) POSTs IDoc XML to the flow's IDoc sender
endpoint (`/cxf/<address>`) with `Content-Type: application/x-sap.idoc`. cpictl checks that the
XML has an `EDI_DC40` control record (a SOAP envelope around it is fine) and reports DOCNUM,
IDOCTYP, MESTYP, SNDPRN and RCVPRN of each IDoc (`idocs` in the result). Start from a skeleton:

```bash
cpictl idoc template --type ORDERS05 --mestyp ORDERS --sndprn S4HCLNT100 --rcvprn CPI --out orders.xml
# add the data segments of ORDERS05 (from WE60 or a real IDoc), then
cpictl send --artifact-id Orders_IDoc_In --idoc orders.xml --wait 60s
```

The skeleton (MCP `idoc_template`) holds the control record with the standard fields and a
commented placeholder; it invents no segments. Use a new `--docnum` per message when the flow
or the receiver detects duplicates. On the tenant the ApplicationMessageId is what the flow sets;
the mock uses the DOCNUM.

**OData V2**: `--odata-method GET|POST|PUT|PATCH|DELETE` (default GET), `--entity-set`, `--key`
(as written in the parentheses: `"'4711'"`, `4711`, `OrderNo='4711',Item=10`) and `--query`
(`"$filter=Status eq 'OPEN'&$top=10"`) build the request against the flow's OData service root
(`.../gw/odata/<namespace>/<service>;v=<version>`), with `Accept: application/json` and, for a
body, `Content-Type: application/json`. `--entity-set '$metadata'` reads the EDMX; no entity set
reads the service document. MCP: `odata_method`, `entity_set`, `key`, `query`.

```bash
cpictl send --artifact-id Orders_OData_Service --entity-set SalesOrders --query '$top=5'
cpictl send --artifact-id Orders_OData_Service --odata-method POST --entity-set SalesOrders --body '{"OrderNo":"4711"}' --wait 60s
```

Both find the endpoint in `ServiceEndpoints` like any other send: `--idoc` takes the flow's
`/cxf/` entry points, the OData flags the entries with protocol `ODATA` or a `/gw/odata/` URL;
with several, choose one with `--url`. Neither goes through the test harness. How a real tenant
lists IDoc and OData entry points was not verified; the mock tenant lists them as described in
[mock-tenant.md](mock-tenant.md#idoc-and-odata-senders) and runs both demo flows.

## Test harness: ProcessDirect senders

A flow started by ProcessDirect can only be called by other flows on the same tenant. The test
harness is one small flow on the development tenant that forwards an HTTP request to any
ProcessDirect address:

```
HTTPS sender (/cpictl/test) -> Request Reply -> ProcessDirect receiver, address ${header.CpictlTargetAddress}
```

```bash
cpictl send --artifact-id Billing --process-direct /billing/in --body-file invoice.xml --wait 60s
```

cpictl sends the address in the header `CpictlTargetAddress` to the harness
(`--harness`, default `CPICTL_Test_Harness`). The flow behind the address runs with the
harness message's correlation ID; with `--wait` cpictl finds that flow's message log by
correlation ID and `--artifact-id`, and reports its status.

Setting up the harness (once per development tenant; the cpi-test skill of the Claude Code
plugin builds it by copying the components from existing flows of the tenant):

- Integration flow ID `CPICTL_Test_Harness`, in a package for test tooling. Do not transport it.
- HTTPS sender: address `/cpictl/test`, user role `ESBMessaging.send`, CSRF protection
  off (or on: cpictl handles the token).
- Request Reply step with a ProcessDirect receiver whose address is
  `${header.CpictlTargetAddress}` (dynamic address). Verify on your tenant that the ProcessDirect
  receiver accepts the dynamic address.
- Runtime configuration, *Allowed Header(s)*: `CpictlTargetAddress` and the headers your test
  cases send (or `*` on the development tenant).

## Test entry: polling and scheduled flows

For flows that poll (SFTP, mail, JMS, ...) or run on a schedule, the flow can get an additional
entry for tests:

- Move the business logic into a **local integration process**.
- The existing integration process (SFTP sender, timer, ...) only calls it (Process Call).
- A second integration process with a **ProcessDirect sender** `/test/<FlowId>` calls the same
  local process. Test it through the harness:
  `cpictl send --artifact-id <FlowId> --process-direct /test/<FlowId> --body-file input.csv --wait 60s`.

Keep it or drop it: a ProcessDirect address is only reachable from flows of the same tenant,
so a permanent test entry exposes nothing and keeps regression tests possible after every
change. Teams whose guidelines forbid test code in productive content remove it before
transport (and the review checks that). Record the decision in the Testing section of
`.cpi/conventions.md`; the build and review skills follow it.

Alternatives: provide the real input (upload a file to the SFTP directory, put a message on the
queue) and observe the run with
`cpictl logs --artifact-id <FlowId> --since 2m --wait 120s`; or, for content you cannot change,
deploy a copy of the flow with an HTTPS sender (it drifts from the original; use it only as a
last resort).

## Timers

A timer set to *Run once* starts the flow on every deployment:

```bash
cpictl deploy --artifact-ids Cleanup --compare-versions=false
cpictl logs --artifact-id Cleanup --since 2m --wait 120s --errors
```

For scheduled timers, check whether the schedule is externalised (`cpictl params get`): set it to
run once on the development tenant, deploy, test, and set it back.

## Tracing

With log level TRACE the tenant records payload and headers at every step:

```bash
cpictl log-level --artifact-id Billing --level TRACE      # active for 10 minutes
cpictl send --artifact-id Billing --process-direct /billing/in --body-file invoice.xml --wait 60s
cpictl logs trace --message-guid <guid>                    # traced steps with trace IDs
cpictl logs trace-message --id <trace-id>                  # payload, headers, exchange properties
```

Over MCP, `set_log_level` is time-boxed: the server sets the flow back to INFO after
`revert_after_minutes` (default 10 for TRACE and DEBUG; 0 = never) on the next tool call, and when
it stops. (The tenant itself also ends TRACE after 10 minutes; DEBUG stays until changed.) If the
service key lacks the role to read message content, the trace tools answer `ok` with
`status: "missing_role"` instead of failing, so an agent can continue and report it.

The log level is set through the Web UI's operations command
`/Operations/com.sap.it.op.tmn.commands.dashboard.webui.IntegrationComponentSetMplLogLevelCommand`
(there is no OData API for it). `--node-type` (default `IFLMAP`) and `--runtime-location-id`
(default `cloudintegration`) select the runtime. Traces contain business data; values of headers
and properties whose names look sensitive are masked in the output.
