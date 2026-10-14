# Lessons from real flows

Rules that customers learnt the hard way while building and refactoring flows with these tools.
Read this before changing an existing flow.

## Review the whole flow you touch

An existing flow is not a reference for what is right. When you change a flow, look at all of it,
not only the part the task names, and fix what is wrong instead of keeping it because it was there:

- local integration processes, receivers or steps no start event reaches (a local process without a
  call step is dead, and so is the receiver only it calls);
- local processes that are a linear chain called once: inline them into the main process (move
  their steps and sequence flows, drop their start/end events, call step and pool, then
  `layout_iflow`). Keep a local process only for a loop, for reuse by several call steps, or to hide
  a real sub-flow;
- Content Modifier rows and exchange properties nothing reads (properties do not cross
  ProcessDirect: the called flow never sees them);
- the same header twice under names that differ only in case (`SAP_CorrelationID` and
  `SAP_CorrelationId`);
- parameters, resources and tests nothing references; generic step names (`Content Modifier 1`,
  `Router 1`, `name="null"`);
- error handling that hides failures: an exception subprocess that ends in a **Message End** event
  marks the message COMPLETED and answers the sender with success. End it in an **Error End**
  event, or remove an empty one and let `returnExceptionToSender` answer the sender.

Ask the user before a change that alters what callers or downstream flows see (error responses,
validation, routing, headers another flow reads); fix the rest and report it. "Smallest change"
applies to the XML serialisation (no reformatting, stable IDs), not to the structure. `lint` finds
most of these (`dead-local-process`, `inline-local-process`, `exception-subprocess-message-end`,
`duplicate-header-case`, `property-never-read`, `unused-*`, `default-step-name`).

## Headers between flows: absent is not empty

When a script or step replaces others that set headers for another flow, keep each header exactly
as it was: absent, empty or set. Called flows often test `!= null`; an empty string where the old
flow left a header absent makes them take another branch (a real case: `partnerId is empty` for
every unmapped message after a refactor). Read the consuming script (`graph_neighbors`,
`edge_types: ["sends_to"]`) before deciding, and test the unmapped case end to end.

## Model details that break the editor or the runtime

- Table-valued properties (Content Modifier `headerTable` / `propertyTable`, Write Variables
  `variable`) are `<row><cell>…</cell></row>` markup, escaped inside `<value>`. A missing `<row>`
  wrapper or an unescaped `&` in a cell makes the Web UI fail with an internal server error when the
  flow is opened. Copy the row shape from an existing step.
- Every `bpmn2:startEvent` has an `outgoing` sequence flow whose `sourceRef` is that event.
- Quote numeric literals in Camel Simple conditions: `${property.count} &gt; '0'`.
- Merge branches by letting several sequence flows end in the same step; do not add a join
  gateway. Routers keep a default route.
- A sender or receiver participant's `name`, the `system` property of its message flow and the
  `attribute_category` of its parameters in `parameters.propdef` belong together: rename all three
  at once, without spaces (`BusinessPartnerLookup`, not `Receiver5`).
- Name every node for what it does, including start and end events and router routes
  ("Look up business partner by ID"); keep the `id`s unchanged when renaming.
- A flow's description is the plain-text `description=` line of `metainfo.prop` (`\n` for line
  breaks). For a reusable flow, describe the headers and properties it requires and reads.
- When a flow will not open in the Web UI and `validate_artifact` does not say why, bisect:
  upload stripped-down copies of the same flow (steps removed in halves) until the culprit shows.

## Groovy script version 2

- The version 2 `Message` (`com.sap.it.script.v2.api.Message`) is not the version 1 API. Read
  headers with `message.getHeaders().get('Name')` and convert explicitly
  (`?.toString()?.trim()`); do not call the version 1 `getHeader(name, Class)`.
- The file starts with `package script.v2` (dots, not `script/v2`), lives in `script/v2/`, and the
  step references it as `v2/<File>.groovy` with `componentVersion` `2.0` and
  `GroovyScript/version::2.0.0`. A `v2/...` reference with the version 1 step (`1.1.2`) is invalid.
- In Spock or JUnit tests, do not put `$` in a feature name: Groovy reads it as a GString.

## HTTP receivers

See [http-receiver.md](http-receiver.md).
