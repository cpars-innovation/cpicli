# Monitoring and checks


## Summary: what is failing?

```bash
cpictl logs summary --since 1h
cpictl logs summary --since 24h --artifact 'Orders_*' --output json
```

Reads the message processing logs of the window (newest first, at most `--max-messages`, default
5000) and summarizes them:

- **per flow**: messages per status, failures, average and longest duration (most failures first);
- **per connection between flows**: messages and failures, linked by the logs' predecessor, or for
  the connections of the [content graph](graph.md) (`.cpi/graph.json`, `--graph`) by shared
  correlation ID;
- **per error fingerprint**: failures with the same cause grouped (the error text without IDs,
  timestamps and long numbers), with a sample, count, first and last occurrence and the newest
  message GUID. Error texts are read for at most `--error-samples` failures (default 50).

The MCP tool `message_summary` returns the same.

## Before uploading: drift

```bash
cpictl drift --local-dir ./content --package-id Orders
```

Per artifact: `in_sync`, `tenant_newer` (edited on the tenant: download first, or the change is
lost), `local_newer`, `diverged` (different content, same version), `not_on_tenant`, plus
`runtimeOutdated` when the deployed version is not the designtime version. Content is compared like
`update artifact` compares it (whitespace, `Origin` headers and `parameters.prop` ignored).

## Before deploying

```bash
cpictl validate --artifact-id OrderIntake          # tenant check, exit 5 if it fails
cpictl guidelines --artifact-id OrderIntake        # activated design guidelines, exit 5 on violations
```

`guidelines` needs design guidelines activated on the tenant by an administrator; otherwise
the tenant answers with an error.

## Runtime

```bash
cpictl status --artifact-ids OrderIntake,Billing   # given artifacts, including NOT_DEPLOYED
cpictl status --runtime-status ERROR               # all deployed artifacts in ERROR, with error text
cpictl endpoints --artifact-id OrderIntake         # URLs to send test messages to
```

## Test messages

```bash
# Send a file to the flow's endpoint and wait for the processing log (exit 5 if it did not complete)
cpictl send --artifact-id OrderIntake --body-file order.xml --content-type application/xml --wait 60s
echo '{"id":1}' | cpictl send --artifact-id OrderIntake --body-file - --header X-Test=1 --output json
```

Only URLs that the tenant lists for the flow (`endpoints`) are used; `--url` chooses one
when there are several. The result contains the HTTP status, the response (64 KB inline),
the message GUID (`SAP_MessageProcessingLogID`) and, with `--wait`, the message log. Exit
codes: 5 for a non-2xx answer or a message that is not COMPLETED, 3 for 401/403, 6 when the
log is not final in time. The message is processed like any other, including calls to
receivers. Credentials: [configuration.md](configuration.md#runtime-endpoints-test-messages).

For flows without an HTTP sender (ProcessDirect, timers, polling adapters) see
[testing.md](testing.md); it also covers tracing (`log-level`, `logs trace`).

## Message processing logs

```bash
cpictl logs --artifact-id OrderIntake --since 1h                    # newest first, max 200
cpictl logs --status FAILED,ESCALATED --since 1d --errors           # with error texts
cpictl logs --correlation-id <id>
cpictl logs --artifact-id OrderIntake --since 2m --wait 60s --errors # wait until final (exit 6 on timeout)
```

`--since` / `--until`: duration back from now (`30m`, `2h`, `1d`) or RFC 3339 timestamp.
Statuses: `COMPLETED`, `PROCESSING`, `RETRY`, `ESCALATED`, `FAILED`, `CANCELLED`,
`DISCARDED`, `ABANDONED`.

One message:

```bash
cpictl logs get --message-guid <guid>        # error text and the model step it failed at, custom header
                                             # properties, adapter attributes (e.g. HTTP status),
                                             # attachments, persisted messages (with their IDs)
cpictl logs steps --message-guid <guid>      # processing steps and the failing step (ModelStepId)
cpictl logs steps --message-guid <guid> --properties   # with each step's properties
cpictl logs attachment --id <attachment-id> [--out file]
cpictl logs payload --id <message-store-entry-id> [--out file]
```

In text mode `logs attachment` / `logs payload` / `resources get` write the content to stdout
(pipe or redirect it); with `--output json` it is part of the result (text, or base64 for binary,
limited by `--max-bytes`, default 64 KB).

### Searching by custom header

Custom header properties your flows write (`messageLog.addCustomHeaderProperty`, e.g. an order
number) find their messages tenant-wide in one call:

```bash
cpictl logs search --header OrderNo=4711
cpictl logs search --header OrderNo=4711 --since 2d --status FAILED --errors
cpictl logs --package-id Orders --since 1h --header OrderNo=4711 --top 5   # same search, narrowed
```

The tenant cannot filter message processing logs by a custom header (`$filter` answers 400), but
it filters `MessageProcessingLogCustomHeaderProperties` by `Name` and `Value` and expands each
match's `Log`. `--artifact-id`, `--package-id`, `--since`, `--until` and `--status` narrow the
matches. A tenant without that entity set is scanned instead: that needs `--artifact-id` or
`--package-id` and `--since` (the tenant is shared), reads at most `--top` × 10 messages (max 500),
and `scanned` and `truncated` in the result say how far it got. `--package-id` alone lists the
messages of all flows of the package.

### Following a message across flows

`cpictl send` (MCP `send_test_message`) sends a W3C `traceparent` header (unless `--no-trace` or
your own `--header traceparent=...`) and returns its `traceId`. Flows with a tracer script write
`trace-id`, `span-id` and `parent-span-id` as custom header properties; `logs tree` rebuilds the
call tree from them and points at the first failure:

```bash
cpictl logs tree --trace-id 0af7651916cd43dd8448eb211c80319c
cpictl logs tree --trace-id <id> --package-id Orders --since 30m   # scan if not found directly
```

Messages are found by ApplicationMessageId = trace ID first (set `SAP_ApplicationID` to the trace
ID in the tracer), otherwise by scanning the scope (`--max-scan`, default 200). Other property
names: `--trace-property`, `--span-property`, `--parent-property`. Tenants whose sender adapters
need business headers (e.g. `sap-client`) get them with `--header` on `send`.

### Following a message without a tracer

From any message GUID (for example a failed message from the error list), `logs tree --message`
follows its **correlation ID** across flows (ProcessDirect and JMS keep it). With
`--key-header`, the custom header properties your flows write (`messageLog.addCustomHeaderProperty`,
e.g. an order number) join runs that have **another correlation ID**: the message left the tenant
(Event Mesh, an external system) and came back. Those runs come with all their messages, also
flows that do not write the header.

```bash
cpictl logs tree --message <guid>
cpictl logs tree --message <guid> --key-header OrderNo --package-id Orders --since 2h
```

The key value is taken from any message of the run. The messages with the same value are found
with the header search above (no scope needed; a scope narrows them); a tenant without it is
scanned (scope required, `--max-scan`, default 200).

The result has the tree, `hops` (from flow, to flow, both messages, `link`) and a `pathKey` that is
the same for every message taking the same route. Links:

| Link | Meaning |
|------|---------|
| `span` | the tracer's parent span ID |
| `predecessor` | the message log names the calling message |
| `inferred` | same correlation ID, attached to the latest message that started before (the log has no predecessor) |
| `header` | joined by a key header, attached to the latest message that started before |

`inferred` and `header` links follow start times: with parallel branches the parent can be a
sibling. Store and compare paths by `pathKey` to see which routes messages take.

## Runtime data

```bash
cpictl datastore list --overdue-only                              # stores with entry counts
cpictl datastore entries --data-store Orders --artifact-id OrderIntake [--message-guid <guid>]
cpictl datastore get --data-store Orders --artifact-id OrderIntake --id <entry> --out entry.xml
cpictl datastore delete --data-store Orders --artifact-id OrderIntake --id <entry> --confirm
cpictl variables --artifact-id OrderIntake                        # flow and global variables
cpictl variables get --name lastRun --artifact-id OrderIntake
cpictl jms queues --prefix ORDERS                                 # fullest first, with their flows
cpictl jms messages --queue ORDERS_IN --failed-only               # messages waiting for a retry
cpictl jms retry --queue ORDERS_IN --id <jms-message-id> --confirm
cpictl jms move --queue ORDERS_ERR --to ORDERS_IN --all --confirm # e.g. back from an error queue
cpictl jms delete --queue ORDERS_IN --id <jms-message-id> --confirm
cpictl jms deactivate --queue ORDERS_IN                           # keep messages, stop consuming
cpictl jms broker                                                 # capacity and usage
cpictl number-ranges
cpictl log-files --type http --since 2h                           # adapter errors without a message log
cpictl log-files get --name <name> --application <app> --tail-bytes 20000
cpictl idempotent --id in/orders.csv --component SFTP             # skipped as duplicate?
cpictl id-mappings --source-id <id>
```

Data store entries and variables are business data: lists show metadata only, `get` downloads.
Global data stores and variables have an empty `--artifact-id`.

JMS messages are managed as in Manage Message Queues of the Web UI. On Cloud Foundry they are
read through `MessagingQueues('{queue}')/MessagingMessages` (the `MessagingMessages` collection
answers 404, the Neo-era `JmsMessages` / `JmsQueues` 501). An unknown queue lists no messages on
the tenant, so cpictl checks the name against `jms queues` first. Messages are selected with
`--id` (repeatable) or `--all` (`--failed-only`); `retry`, `move` and `delete` need `--confirm`.
`retry` and `move` call `RetryMessagingMessages` / `MoveMessagingMessages` with a JSON body
(`queueName`, `targetQueueName`, `jmsMessageIds`), `delete` deletes
`MessagingMessages(jmsMessageId,queueName)`, `activate` / `deactivate` call `activateQueue` /
`deactivateQueue`. The reads are verified on a Cloud Foundry tenant; the changing calls are not
yet (the function imports declare no parameters, so the body format is cpictl's reading of the
`$metadata`). The flows of a queue come from `Queues?$expand=Artifacts`; their `direction` is the
tenant's code, passed on as is.

## Resources of an iFlow

```bash
cpictl resources --artifact-id OrderIntake
cpictl resources get --artifact-id OrderIntake --name script1.groovy --type groovy
cpictl download --artifact-id OrderIntake --dir ./OrderIntake     # whole artifact, extracted
```

## API coverage

| Area | Endpoints |
|------|-----------|
| Logs | `MessageProcessingLogs` (query, single), `ErrorInformation/$value`, `ErrorInformation`, `CustomHeaderProperties`, `AdapterAttributes`, `Attachments`, `MessageProcessingLogCustomHeaderProperties?$filter=Name eq … and Value eq …&$expand=Log` |
| Payloads and steps | `MessageProcessingLogAttachments('{Id}')/$value`, `MessageProcessingLogs('{guid}')/MessageStoreEntries`, `MessageStoreEntries('{Id}')/$value`, `MessageProcessingLogs('{guid}')/Runs`, `MessageProcessingLogRuns('{Id}')/RunSteps` (`$expand=RunStepProperties`: step properties are only served this way) |
| Content | `ValidateIntegrationDesigntimeArtifact`, `ExecuteIntegrationDesigntimeArtifactsGuidelines`, `DesignGuidelineExecutionResults`, `ServiceEndpoints`, `IntegrationRuntimeArtifacts`, `Resources`, artifact `$value` |

The endpoints in the second row belong to SAP's Message Store and trace APIs and are not
part of the Message Processing Logs API specification; `logs get` treats them as optional
and reports problems in `warnings` instead of failing.

Required roles: monitoring read access for logs; reading persisted messages and
attachments can require additional roles (e.g. access to message content).
