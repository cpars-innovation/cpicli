---
name: cpi-operate
description: Operate an SAP Cloud Integration tenant - find out what is failing and why, follow a message by its business key across flows, handle messages waiting in JMS queues (inspect, retry, move), check runtime errors, edit locks, expiring certificates, integration connections and tenant settings, and collect logs. Use when the user asks why messages fail, what is stuck, about the health of a tenant, about a specific order or document number, or to clean up after an incident.
---

# Operate a tenant

Monitoring and incident handling on a deployed landscape. Read first, change only what the user
agreed to, one change at a time, and say what you did. On a productive tenant every change
(retry, move, log level) needs the user's explicit go for that change.

## 1. What is failing

- `message_summary` over the window the user cares about (default the last hours): volume and
  failures per flow and per connection, grouped by an error fingerprint. Start here for "what is
  failing"; it names the flows and the most frequent errors.
- `list_runtime_artifacts` with `statuses: ["ERROR"]`: flows that do not run at all (the error
  text is included). A flow in ERROR is a deployment problem, not a message problem.
- `list_locks`: a flow someone has open in the Web UI cannot be updated; mention who holds it.
- `list_keystore` (`expiring_within`): certificates that expire soon break TLS and signatures.

## 2. One message, one business key

- The user names an order, invoice or document number: `find_messages_by_header` with the key
  header the flows write (see `.cpi/conventions.md`, Logging, e.g. `OrderNo=4711`). One call,
  tenant-wide, newest first.
- An IDoc number (from the backend's WE02/BD87): flows that follow
  [sap-adapters.md](../cpi-build/sap-adapters.md) set `SAP_ApplicationID` to DOCNUM, so
  `list_message_logs` with the application ID finds it; otherwise `find_messages_by_header` with
  the IDoc header the conventions name. `graph_search` for the IDoc type (e.g. ORDERS05) shows which
  flows handle it. An OData error from S/4HANA: the message log's error text carries the HTTP
  status and the service's message; the receiver system is in the flow's receiver channel.
- A message GUID: `get_message_log` (status, error text, `errorInformation` with the last failed
  model step, adapter attributes), `get_message_steps` (`properties: true` for step properties),
  `get_trace_tree` (the path across flows: ProcessDirect, JMS, key headers).
- Payload at a step: `get_message_attachment` / `get_message_store_entry`; still unclear and the
  user agrees: `set_log_level` TRACE for that flow (it reverts on its own), reproduce,
  `get_message_trace` / `get_trace_message`.
- Errors outside message processing (adapter connection errors, polling failures):
  `list_log_files` / `get_log_file`; `download_log_archive` saves the latest http or trace logs as
  a zip for the user or SAP support.

## 3. Messages waiting in JMS queues

- `list_jms_queues` (fill level, which flows use a queue) and `list_jms_messages` (`queue`,
  `failed_only`): messages that failed and wait for the next retry, with retry count, next retry
  and the message GUID to look up with step 2.
- Fix the cause first (flow, receiver, credential). Then, with the user's go:
  `retry_jms_messages` (confirm) runs them again now; `move_jms_messages` (confirm) parks them in
  another queue, for example an error queue. `set_jms_queue_state` stops or starts a queue's
  consumers.
- Deleting messages loses them for good. `delete_jms_messages` is offered only when the server
  was started with it; never suggest it as a shortcut, and only on the user's explicit request
  naming the messages.

## 4. Landscape and settings

- `list_integration_connections`: which systems the deployed flows connect (sender host -> flows
  -> receiver host), as the tenant computes it. Use it to find every flow that talks to a system
  that is down.
- `get_tenant_settings`: external logging (e.g. to Splunk) and archiving of message logs on or
  off, last archiving runs and failures.
- `list_data_store_entries` / `list_variables` / `list_idempotent_entries`: stuck data store
  entries, timer state, messages skipped as duplicates.
- Security material changes (`generate_key_pair`, `upload_certificate_chain`, credentials) are
  not part of incident handling: tell the user what is missing or expiring and let them decide.

## 5. Report

End with: what is failing (flows, counts, error), the cause as far as verified, what you changed
(retries, moves, log levels, with counts and message IDs), what is still open and who has to act
(the team owning a receiver, an admin for certificates or roles). Keep facts and suspicions apart.
