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
receivers. Use synthetic data. Write the tests as cases and run them with `cpictl test run`
(MCP `run_tests`), see [Test runs](#test-runs); `cpictl send` is for a single message by hand.

## Test runs

Test cases are YAML files, one per case, in `.cpi/tests/<FlowId>/<case>.yaml`; `cpictl test run`
(MCP `run_tests`) sends each case's message, waits for the message processing log and checks every
expectation. `cpictl test list` (MCP `list_tests`) shows the cases without sending anything. The
schema is [test-case.schema.json](test-case.schema.json) (add
`# yaml-language-server: $schema=...` at the top of a case for editor completion).

```
.cpi/tests/Orders_In/
├── valid-order.yaml
├── valid-order.xml        # body_file, relative to the case
├── missing-customer.yaml
└── config.yaml            # optional: parameter values for --apply-config
```

```yaml
# .cpi/tests/Orders_In/valid-order.yaml
description: Valid order is accepted and passed on
tags: [smoke]
order: 1                       # optional: sorts the cases of a flow (then by file name)
# flow: Orders_In              # default: the folder name
# skip: partner not ready      # skips the case, with the reason
request:
  method: POST                 # default POST
  content_type: application/xml
  headers: { X-Test-Case: valid-order }
  body_file: valid-order.xml   # or body: "<Order/>"
  # url: ...                   # one of several endpoints (old name: endpoint)
  # path: test                 # sub-path below the flow's endpoint
  # process_direct_address: /orders/route   # through the test harness
  # idoc_file: orders.xml      # or idoc: "<ORDERS05>..."; to the flow's IDoc sender
  # odata: { method: GET, entity_set: SalesOrders, key: "'4711'", query: "$top=1" }
  wait: 60s                    # how long to wait for the message log (default 60s)
expect:
  http_status: 200
  message_status: COMPLETED
  response_contains: ["<Status>OK</Status>"]
  response_not_contains: ["<Error>"]
  response_xpath: { /Order/Status: OK, "//Item[@pos='1']/Qty": "2" }
  response_json: { order.no: "4711", order.items.0.qty: 2 }
  custom_headers: { OrderNo: "4500017001" }
  steps_include: [Map Order, CallActivity_2]
  max_duration: 5s
```

Keys are checked strictly: an unknown key, a wrong type or invalid YAML is a usage error with
`file:line`. Older cases written for the cpi-test skill (`endpoint`, the list forms) load
unchanged; `response_contains`, `response_not_contains`, `error_contains`, `steps_include` and
`body_contains` accept a single string too.

| Expectation | Holds when |
|-------------|------------|
| `http_status` | the endpoint answered with this status. Without `http_status` (and without `error_contains`) the status must be 2xx, unless `message_status` expects a failure (FAILED, ESCALATED, ...) |
| `message_status` | the message's final status is this one (any case); the case waits for the log |
| `response_contains` / `response_not_contains` | the response contains / does not contain each text |
| `response_xpath` | the first match of each path has this text (see below) |
| `response_json` | the value at each dotted path (`a.b.0.c`: keys and array indexes) equals the expected one: numbers by value (`9.5` = `9.50`, `2` = `"2"`), objects and arrays as JSON |
| `error_contains` | each text is in the error: the log's error text, the response of a failed call or the send error (negative cases) |
| `response_headers` | the answer has these headers (names case-insensitive, values exact); `null`: the header must be absent |
| `custom_headers` | the message log has these custom header properties with these values |
| `steps_include` | each name is a step's activity name or model step ID (`.iflw` element ID) in the message's steps |
| `max_duration` | the message took at most this long (log start to end; without a log, the HTTP call) |
| `stub_calls` | the receiver stub recorded the expected calls (below) |

The case waits for the processing log (up to `wait`) when an expectation needs it:
`message_status`, `custom_headers`, `steps_include` or `error_contains`.

**XPath subset** (`response_xpath`, via the etree library): absolute paths (`/a/b/c`), descendants
(`//name`), `*`, `.`, `..`, positions (`[1]`, `[last()]`), filters (`[@attr]`, `[@attr='v']`,
`[child]`, `[child='v']`); a final `/@attr` returns the attribute, `/text()` the text. Names
match in any namespace unless written with the document's prefix (`ns0:Order`). The text of an
element includes the text of its children, trimmed. No functions or axes beyond these.

### Running

```bash
cpictl test list
cpictl test run                                    # every case under .cpi/tests
cpictl test run --flow Orders_In --case valid-order
cpictl test run --tag smoke --parallel 4 --junit report.xml --events -
```

Each case runs like `cpictl send` (same runtime credentials, harness and endpoint rules). A failed
case lists every expectation that did not hold (expected and actual value) and diagnostics: the
message GUID and status, the last error, `errorInformation` (the model step that failed last),
the first failing step and the root of the message's path across flows (as `get_trace_tree` by
message GUID). The text output ends with a table (case, result, HTTP status, message status, time,
message GUID); `--output json` puts the whole result in the envelope; `--events` writes one item
event per case when it is final; `--junit FILE` writes JUnit XML (a test suite per flow, a test
case per case, `failure` with the messages, `error` for cases that could not run, `skipped`).

Cases run one at a time; `--parallel N` (at most 32; MCP `parallel`, at most 8) runs N at once. Cases with
`stub_calls` always run alone. A case's status is `passed`, `failed` (an expectation did not hold),
`error` (it could not run: unknown flow, invalid request, credentials, tenant errors) or
`skipped`. On a profile with `tier: prod` a test run is refused unless `--allow-production` (MCP:
unless the server was started with it).

| Exit code | When |
|-----------|------|
| 0 | every case passed or was skipped |
| 5 (`failed`) | a case failed, or `--apply-config` could not restore a configuration |
| 7 (`partial`) | some cases passed, others could not run, none failed |
| 2, 3, 4, ... | no case could run: the most specific cause (auth 3, tenant HTTP 4, usage 2, failed 5, timeout 6) |
| 2 (`usage`) | invalid case files, no matching case, `--parallel` out of range, production guard |

### Test configuration (`config.yaml`)

A flow's receivers usually point at real systems. `config.yaml` next to its cases holds
externalised parameter values for the test, for example a receiver pointing at the receiver stub:

```yaml
# .cpi/tests/Orders_Route/config.yaml
ERP_URL: https://<runtime host>/http/cpictl/stub/erp
```

The receiver is often in another flow than the one the case sends to (an inbound flow calls a
routing flow over ProcessDirect or JMS, which calls the backend). `flows:` sets the parameters of
those flows for the run as well; they are applied after the flow's own values and restored in
reverse order:

```yaml
# .cpi/tests/Orders_In/config.yaml
flows:
  Orders_Route:
    ERP_URL: https://<runtime host>/http/cpictl/stub/erp
```

With `--apply-config` (MCP `apply_config: true`) the run, per flow with a `config.yaml`: reads the
current values, sets the differing test values, redeploys the flow and waits until it is started,
runs the flow's cases, then sets the previous values back and redeploys again. The restore also
runs when the cases fail or the run is cancelled (with its own context and a 15 minute limit). It
refuses a flow that is not deployed, is in draft, or whose designtime version differs from the
running one (a redeployment would deploy another version), and a key the flow does not have; the
flow's cases are then errors and nothing is changed. The result's `config` lists per flow the keys
applied and restored and the redeployments; values are never reported. Without `--apply-config` a
`config.yaml` whose values differ from the tenant is a warning (keys only).

### Receiver stub (`stub_calls`)

The receiver stub flow `CPICTL_Test_Stub` (HTTPS sender `/cpictl/stub/*`) records every request
it receives and answers by rule (the path segment after `/cpictl/stub/`). Point receivers at it
with `config.yaml`, then assert the calls the flow made:

```yaml
expect:
  stub_calls:
    - rule: erp                    # the segment after /cpictl/stub/
      method: POST
      path_contains: orders
      body_contains: ["<OrderNo>4711</OrderNo>"]
      header: { Content-Type: application/xml }
      count: 1                     # default 1; 0: no such call
```

The runner reads the calls recorded since the case started (5 seconds earlier for clock skew,
but not before the previous stub case ended) and polls for up to 10 seconds until every matcher
has its count. A call matches when every field that is set matches (method and header names in
any case); exactly `count` calls must match. Cases with `stub_calls` run alone, so the calls of
other cases do not interfere.

### Go

The runner is `pkg/testrun` (`Load`, `Run`, `WriteJUnit`), see [go-api.md](go-api.md#test-runs-pkgtestrun).

- `Options.Progress` receives each case result when it is final; `Options.Phase` receives the
  phases as they start and end (`PhaseEvent{Flow, Case, Phase, State, Error, At}`): per flow
  `apply_config`, `deploy`, `restore_config`; per case `send` (until the endpoint answered),
  `wait_log`, `stub_calls`, `diagnose`. States are `started`, `done`, `failed`. Parameter values
  are never part of an event.
- A configuration that could not be restored is in `Result.Config[].RestoreError` (exit code 5);
  `Result.RetryRestore(ctx)` tries again in the same process (the previous values are kept in
  memory only, never in the result).
- The case schema `docs/test-case.schema.json` has a `title` and `examples` for every field, for
  editors that build a form from it.

## XSLT mappings in the Partner Directory

XSLT mappings stored as Partner Directory binary parameters (used with a dynamic `pd:` resource)
are tested offline with Saxon. A case is `.cpi/tests/pd/<PID>/<ID>/<case>.yaml` and tests the
parameter `<PID>/<ID>`; its stylesheet is the file `pd-snapshot` wrote
(`partner-directory/<PID>/Binary/<ID>.xsl`), or the case's `xslt`. Schema:
[xslt-case.schema.json](xslt-case.schema.json).

```yaml
description: Maps an order with two items
input: "<Order><No>4711</No><Item><Qty>2</Qty></Item><Item><Qty>3</Qty></Item></Order>"
headers: {SAP_Sender: ERP_PRD}      # stylesheet parameters (xsl:param), as in Cloud Integration
properties: {Mode: test}
expect:
  output: '<SalesOrder mode="test" sender="ERP_PRD"><Number>4711</Number><Items>5</Items></SalesOrder>'
  output_xpath: {/SalesOrder/Items: "5"}
```

```bash
cpictl test xslt --saxon-jar ~/lib/saxon-he-12.5.jar    # or CPICTL_SAXON_JAR, or --command saxon
cpictl test xslt --flow 'SAP_ERP/*' --junit xslt.xml
```

- `output` (or `output_file`) is compared as XML: attribute order, whitespace between elements,
  comments, the XML declaration and namespace prefixes do not count. Non-XML output is compared
  as trimmed text.
- `output_contains`, `output_not_contains`, `output_xpath` (the path subset of `response_xpath`),
  and `error_contains` for a transformation that must fail (Saxon's error text).
- Results are `testrun` results (`CaseResult` with `Flow` = `pd:<PID>/<ID>`, the output as
  `Response` for failed cases), so JUnit and Studio's views treat them like flow cases. Exit code
  5 when a case failed. Go: `testrun.LoadXSLT`, `testrun.RunXSLT`.
- Saxon runs on this machine (Java required); nothing is sent to a tenant. Use the Saxon version
  closest to your tenant's XSLT engine.
- `--online` runs the same cases on the tenant's XSLT engine: the stylesheet is uploaded as the
  Partner Directory binary `CPICTL_Test/<PID>.<ID>` (removed afterwards unless `--keep-uploads`)
  and each case goes to the XSLT test flow `CPICTL_Test_XSLT` of the test tooling (`cpictl test
  tools install --deploy`; role parameter `CPICTL_XSLT_Role`). The flow only reads PID
  `CPICTL_Test`. Headers and properties travel as one JSON header (`CpictlXsltParams`) and are
  set with their exact names, since HTTP loses the case of header names and `xsl:param` names
  are case-sensitive. Refused on a production tier unless `--allow-production`. Go:
  `XSLTOptions{Online: true, Exe, NewExe, Tier, AllowProduction, Keep}`. *(The flow passes a
  real tenant's Check; its run on a deployed tenant is still to be verified.)*

## Script cases

A script case calls one function of a script with a message and checks what comes out, without a
tenant. The format is shared with CPI Studio
([script-case.schema.json](script-case.schema.json)):

```
.cpi/tests/<FlowId>/scripts/<script file>/<case>.yaml   a script step of a flow
.cpi/tests/collections/<CollectionId>/<case>.yaml        a function of a script collection
```

```yaml
# .cpi/tests/Orders_In/scripts/Headers.groovy/trims.yaml
description: trims the order number and reads the target from the Partner Directory
input:
  headers: {OrderNo: " 4711 "}
  properties: {PID: PID1}
pd: {PID1/Target: ERP-DEV}          # what PartnerDirectoryService answers (test values only)
expect:
  headers: {OrderNo: "4711", Temp: null}   # null: must be absent
  properties: {Target: ERP-DEV}
  body_xpath: {/Order/No: "4711"}
  log: {OrderNo: "4711"}            # custom header properties the script logged
```

```yaml
# .cpi/tests/collections/Orders_Scripts/pads.yaml
call: {function: formatNo, args: [" 4711", 8]}   # with args the function gets them, not the message
expect: {returns: "00004711"}
```

- Online, a failed transformation's error is the tenant's: it answers HTTP 500 naming only the
  message, and cpictl reads the reason from the message log. The tenant's XSLT engine does not
  report the text of `<xsl:message terminate="yes">`, only `Processing terminated by xsl:message
  at line N`: an `error_contains` that names your message's text passes with Saxon locally, but not
  online. Expect the common part, or keep such cases local (`--case`).
- `call.function` defaults to `processData`; a collection case finds the one script of the
  collection that defines it.
- `call.args`: the function gets these arguments instead of the message: a collection function, or
  a custom function of a flow's message mapping (a script of the flow such as `udf.groovy`).
  `MappingContext` and `Output` parameters are supplied by the runner (the context reads the case's
  headers and properties); with `Output`, `returns` is the list of values the function added, with
  `__SUPPRESS__` and `__CONTEXT_CHANGE__` for `addSuppress` and `addContextChange`:

  ```yaml
  # .cpi/tests/Orders_In/scripts/udf.groovy/upper-all.yaml
  call: {function: upperAll, args: [["a", "", "b"]]}   # void upperAll(String[] v, Output out, MappingContext ctx)
  expect: {returns: ["A", "__SUPPRESS__", "B"]}
  ```
- `input`: `body` or `body_file`, `headers`, `properties`.
- `pd` (`<pid>/<param>` -> value or `file:<path>`), `value_mappings`, `secure` and `keystore` set what
  SAP's services answer. They are test values: never a real credential or key.
- `expect`: `body_equals_file` (XML and JSON compared structurally), `body_contains`,
  `body_not_contains`, `body_xpath`, `body_json`, `headers` and `properties` (`null`: absent; header
  names are case-insensitive, property names are not), `returns`, `throws` (`class`,
  `message_contains`), `log`, `max_duration`.

Run them with `cpictl test scripts cases` (MCP `run_script_cases`). They run on the SAP script API
stand-ins of `cpictl test scripts init`, through the Gradle task `cpictlScriptCases` of its
`build.gradle` (a JDK 17+ and Gradle needed; an older `build.gradle` needs `init --force`). Version 2
scripts (`package script.v2`) get the version 2 message, which has no `getHeader` (not part of the
version 2 API; the tenant's runtime still accepts it today, so such a case passes online). A case in error
with `errorCategory` `unsupported` uses a part of SAP's API the stand-ins do not implement: test
that part on the tenant. JavaScript scripts are not run locally.

### Script cases on the tenant

`cpictl test scripts cases --online` (MCP `run_script_cases_online`) runs the same case files on the
tenant's runtime, with SAP's real classes, Camel and the tenant's keystore.

```bash
cpictl test scripts cases --online --flow Orders_In
cpictl test scripts cases --online --keep        # leave the test flows deployed for a faster rerun
```

- **How:** cpictl generates one flow per script and function, `CPICTL_Test_Script_<hash>`, in the
  test tooling package. The flow is: HTTPS `/cpictl/script/<hash>` -> set the case's headers and
  properties -> the script under test -> the result as JSON.
  - The flow holds a copy of the owner's `script/` and `lib/` files.
  - cpictl deploys the flows (about a minute), sends each case and deletes the flows afterwards.
  - Nothing runs that isn't in your repository: no script text is sent with the message.
- **Same expectations as locally:** body, headers and properties after the call, and `throws`
  (class and message from the message log).
  - The result lists the headers and properties the case set or the script added or changed. What
    the message held before, and what the runtime writes at every step, is left out.
- **Skipped online (local only):**
  - cases with `call.args` (a script step passes the message);
  - `pd`, `value_mappings`, `secure`, `keystore` (test values are never written to a tenant);
  - `log` and `returns` expectations.

  `max_duration` is not checked (the time includes the HTTP round trip).
- **Languages:** Groovy version 1 and 2 (`script/v2/`), and JavaScript, verified on a tenant.
- **Guard:** refused on production tiers (`--tier`, `--allow-production`). The runtime client
  needs the flows' role (`--role`, default `ESBMessaging.send`); runtime credentials as for
  `send` (`--runtime-oauth-clientid`, ...).
- **Local and online can differ, and that's the point.** For example, a version 2 script calling
  `getHeader(name, Class)` fails locally (the v2 API has no such method) but runs on today's runtime.

## Script collection tests

`cpictl test scripts init` writes the Gradle build and the stand-ins of SAP's script API into the
project (`tests/stubs`: `Message` v1 and v2, the message log, `ITApiFactory` with secure store,
keystore, Partner Directory, value mapping and data store, SOAP faults, mapping context); existing
files are kept unless `--force`. Tests set the values the services answer with
(`CaseContext.builder()`); [tests/stubs/README.md](../pkg/scripttest/template/tests/stubs/README.md)
describes the classes, the test-facing API, and which signatures are not verified against SAP's
javadoc (data store, `ValueMappingApi.getMappedValues`, `MappingContext` / `Output`). Needs JDK 17+.

`cpictl test scripts` runs the Gradle build of the script collection tests (the build skill's
[script-tests.md](../plugin/skills/cpi-build/script-tests.md): `./gradlew test`, or `gradle test`
without a wrapper; `--command` for another one) and reads its JUnit and JaCoCo reports:

```bash
cpictl test scripts                       # run and report
cpictl test scripts --no-run --output json  # only read the reports of an earlier run
```

The result lists every test (class, name, status, duration, failure message) and every public
function of every collection script with its line coverage, the test files and test methods that
call it, and a status: `passed`, `failed` (a test that calls it failed), `skipped` (its tests did
not run) or `untested` (no test calls it). A test method calls a function when its body contains
the call; a function called only from a helper or setup method counts every test of the class.
The paths are lint's (`scriptTests` in `.cpi/lint.yaml`). Exit code 5 when a test failed. Go:
`pkg/scripttest` (`Run`, `Options`, `Result`). Nothing is sent to the tenant. Scripts inside
flows (not in a collection) are not covered yet.

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

The harness ships with cpictl: install it once per development tenant with
`cpictl test tools install --deploy` (MCP `install_test_tools`), see below. Harness versions
before 1.2.0 (cpictl 0.18.0 and older) do not call the address on a tenant: their model lacked the
sequence flow references SAP's runtime needs, so the harness answered with the body it got. A send
that finds no message of the flow under test says so; reinstall the tools.

A flow deployed a moment ago is `STARTED` before the tenant lists its endpoint (about a minute on
the tenants we tried): `send` and test runs wait up to two minutes for the endpoint of a started
flow instead of failing.

## Test tooling: harness and receiver stub

cpictl embeds two small flows for testing and installs them on a development tenant:

```bash
cpictl test tools install --deploy        # package CPICTLTesting, created when missing
cpictl test tools status                  # installed, deployed, version, endpoints, stub URL
```

| Flow | Model | Used for |
|------|-------|----------|
| `CPICTL_Test_Harness` | HTTPS sender `/cpictl/test` -> Request Reply -> ProcessDirect receiver `${header.CpictlTargetAddress}` | `send --process-direct`, test runs of ProcessDirect flows |
| `CPICTL_Test_Stub` | HTTPS sender `/cpictl/stub/*` -> Groovy script | a receiver for the flow under test: records each request and answers per rule |
| `CPICTL_Test_XSLT` | HTTPS sender `/cpictl/xslt` -> Groovy script -> XSLT mapping from the Partner Directory (PID `CPICTL_Test`) | `test xslt --online`: XSLT cases on the tenant's engine |

- `install` creates the package (`--package`, default `CPICTLTesting`: package IDs take letters
  and digits only) and creates or updates both flows (`--only harness|stub` for one). The
  versions are the ones this cpictl ships (versioning manifest); a running flow keeps running
  until it is deployed again: `--deploy` deploys and waits. Never transport the package.
- Both HTTPS senders have CSRF protection off and require a user role, externalised as the
  parameters `CPICTL_Harness_Role` and `CPICTL_Stub_Role` (default `ESBMessaging.send`). *Allowed
  Header(s)*: the harness passes `CpictlTargetAddress`, `traceparent` and `X-Test-*`; the stub
  takes every header (`*`) so that it can record what the flow under test sent.
- **Role.** The harness calls any ProcessDirect address it is given, so with the default role
  every client that may send messages to the tenant can reach every ProcessDirect flow through it.
  On a shared development tenant, give the tooling a role of its own:

  ```bash
  cpictl test tools install --role ACME.CpictlTest --deploy
  ```

  `--role` (MCP `role`) sets both parameters after the upload and before the deploy; without it
  the configured role stays, and `test tools status` shows it. The role must exist on the tenant
  (Monitor > Manage Security > User Roles), and the runtime credentials cpictl sends with (the
  `runtime` service key, plan **integration-flow**) must come from a service instance that has it
  (instance parameter `"roles": ["ACME.CpictlTest"]`). Clients without the role get HTTP 403.
- On a tenant whose tier is prod, `test tools install` and `test stub set` (MCP
  `install_test_tools`, `set_stub_response`) are refused unless `--allow-production`.

### Receiver stub

Point a receiver of the flow under test (for example the HTTP receiver's address parameter) at
the stub URL from `test tools status`, with a **rule** as the first path segment:
`https://<runtime>/http/cpictl/stub/<rule>/<anything>`.

For every request the stub's script

- records method, the path after `/cpictl/stub/`, query, headers (without `Authorization`,
  cookies, tokens, passwords, secrets and other sensitive names, and without `Camel*` and `SAP_*`
  internal headers) and body (the first 64 KiB) as the message processing log attachment
  `cpictl-stub-request` (JSON);
- sets the custom header properties `CpictlStubRule` (the rule) and `CpictlStubPath` (the path),
  so the runs are found by header (`cpictl logs`, `find_messages_by_header`);
- answers with the response configured for the rule in the Partner Directory: PID `CPICTL_Stub`,
  string parameter ID = rule, value
  `{"status":200,"contentType":"application/json","body":"...","headers":{}}`. Without one it
  answers 200 with `{}`. It sets `CamelHttpResponseCode` and `Content-Type`; the stub's message
  is COMPLETED whatever status it answers.

```bash
cpictl test stub set erp --status 500 --body '{"error":"posting period closed"}'
cpictl test stub set orders --body-file order-response.json --content-type application/json --header X-Request-Id=42
cpictl test stub clear erp                # back to 200 {}
```

**Conditional answers and delays.** A rule can answer differently per request: `when` lists
entries with conditions (`pathContains`, `header` name -> value, `bodyContains`; all set ones must
hold, at least one is required) and the answer fields that differ (`status`, `contentType`,
`body`, `headers`, `delayMs`); the first matching entry answers, else the rule's own answer.
`delayMs` (0-60000) delays the answer, e.g. to test a receiver's timeout.

```json
[
  {"name": "bad-order", "bodyContains": "<OrderType>XX</OrderType>", "status": 422, "body": "{\"error\":\"invalid type\"}"},
  {"name": "slow", "header": {"X-Test-Mode": "slow"}, "delayMs": 30000}
]
```

```bash
cpictl test stub set erp --body '{"status":"ok"}' --when-file erp-when.json
cpictl test stub set erp --delay 30000
```

Each recorded call says which answer was chosen (`matched`: the entry's name, `when[<i>]`, or
`default`; empty when the rule has no answer configured) and its processing time (`durationMs`).

Rules are letters, digits, `_`, `.` and `-`. MCP: `set_stub_response` (rule, status,
content_type, body, headers, delay_ms, when; `clear`). Go: `ops.SetStubResponse`, `ops.ClearStubResponse`, and
`ops.StubCalls(ctx, exe, since)` returns the recorded requests since a time, oldest first (the
stub's message logs of any status, each with its attachment and message GUID).

The script is a Groovy script step of version 1 (`com.sap.gateway.ip.core.customdev.util.Message`):
the Partner Directory API it reads the answers with (`ITApiFactory.getService(PartnerDirectoryService.class, null)`,
`getParameter(rule, "CPICTL_Stub", String.class)`) is documented for that script API; for script
API version 2 it was not verified. The path is taken from `CamelHttpUri` (or `CamelHttpUrl`,
`CamelHttpPath`) after `/cpictl/stub/`.

Not yet verified on a real tenant: the *Allowed Header(s)* value format (`|`-separated, with `*`
wildcards), the ProcessDirect receiver's dynamic address, which of the `CamelHttp*` headers the
HTTPS sender sets for a `/*` address, and the Partner Directory script API call. Check
`cpictl test tools status` and one request to the stub after the first install.

The [mock tenant](mock-tenant.md#test-tooling) emulates both flows, so test runs work offline.

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
