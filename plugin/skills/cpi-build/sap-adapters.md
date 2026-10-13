# OData and IDoc

Most SAP integrations read or write OData services (S/4HANA, SuccessFactors, C4C) or exchange IDocs
with an SAP backend. This page is what an agent needs to build, test and operate such flows.
Items marked *(check)* come from SAP documentation and experience, not from a verified tenant: check
them on the development tenant before relying on them.

## Find what exists first

- `discover_tenant` / `discovery.json` records for OData channels the version, operation, resource
  path (entity set), query options and paging, and for IDoc channels the address and the IDoc types
  the flow handles (from its mappings, schemas and conditions).
- The graph has `odata_entity` and `idoc_type` nodes: `graph_search` "ORDERS05" or "A_SalesOrder"
  lists the flows that receive, send, read or write them (`receives_idoc`, `sends_idoc`,
  `reads_odata`, `writes_odata`). Reuse an existing flow, mapping or script collection before
  building a new one.

## OData receiver (calling S/4HANA, SuccessFactors, ...)

Adapter `HCIOData` (OData V2 or V4) or `SuccessFactors` (OData V2). Properties in the model:

| Property | Meaning |
|----------|---------|
| `address` | service root URL: externalise it (`{{S4_OData_Address}}`), never a literal host |
| `operation` | `Query(GET)`, `Create(POST)`, `Update(PUT)`, `Merge(MERGE)`/`Patch`, `Delete(DELETE)`, `Function Import`, `$batch` |
| `resourcePath` | entity set (e.g. `A_SalesOrder`), with key for single entities |
| `queryOptions` | `$select`, `$filter`, `$expand`, `$top` ... |
| `pagination`, `odatapagesize` (SuccessFactors) | process large results in pages |

- **Queries**: always `$select` the fields you need; page large results (`pagination` on, or
  `$top`/`$skip` in a looping process call). lint: `odata-query-without-paging`.
- **Writes**: the adapter fetches the CSRF token itself when CSRF protection is on (the default
  for V2) *(check)*. Many records: one `$batch` request instead of one call per record in a loop
  (lint `odata-single-calls-in-loop`).
- **Client and environment**: `sap-client` and other system-specific values go into parameters,
  not into `queryOptions` or the address (lint `odata-hardcoded-query`).
- **EDMX**: the adapter and message mappings use the service's metadata. Get it from the service
  (`<service root>/$metadata`, ask the user or the backend team; cpictl does not call backend
  systems) and store it under `src/main/resources/edmx/`.
- Errors: an OData error comes back as an exception with the HTTP status and the service's error
  body; handle it in the exception subprocess (log the status and message, decide retry vs. fail).

## OData sender (CPI exposes an OData service)

Adapter `ODataSender` (OData V2). The flow implements the service: the endpoint is
`/gw/odata/<namespace>/<service>;v=<version>/` *(check: read the real URL with
`list_service_endpoints` after deploying)*. The service model (EDMX) is part of the artifact.

## IDoc from the backend into CPI (IDoc sender)

Adapter `IDOC`, message protocol `IDoc SOAP`. The endpoint is `/cxf/<address>` on the runtime host
*(check with `list_service_endpoints`)*.

- Backend side (basis team): an HTTP destination (SM59, type G) to the CPI runtime host and path
  `/cxf/<address>`, an XML HTTP or SOAP port (WE21) and the partner profile (WE20) *(check with
  your basis team: depends on the release)*.
- In the flow, read the control record `EDI_DC40` (DOCNUM, IDOCTYP, MESTYP, SNDPRN, RCVPRN).
  Set `SAP_ApplicationID` to the IDoc number (content modifier header, XPath
  `//EDI_DC40/DOCNUM`) so that monitoring finds a message by IDoc number
  (`find_messages_by_header`, MPL `ApplicationMessageId`). The conventions may name more custom
  header properties (IDoc type, partner).
- Backends resend IDocs: make processing idempotent on the IDoc number (Idempotent Process Call
  with DOCNUM as message ID) and decouple long or failure-prone processing with JMS (lint
  `idoc-no-idempotency`).
- Several IDocs in one call: split them (IDoc splitter / General Splitter on `IDOC`).

## IDoc from CPI to the backend (IDoc receiver)

Adapter `IDOC`, message protocol `IDoc SOAP`. Real properties:

| Property | Value |
|----------|-------|
| `address` | `https://<host>:<port>/sap/bc/srt/idoc?sap-client=<client>`: externalise host and client (`{{S4_IDoc_Address}}`; lint `idoc-sap-client-literal`) |
| `IDocContentType` | `application/x-sap.idoc` (plain IDoc XML) |
| `authentication`, `credentialName` | Basic (a user credential) or client certificate |
| `cleanupHeaders`, `allowChunking`, `requestTimeout` | as in the conventions |

- The backend's IDoc SOAP service (`/sap/bc/srt/idoc`) must be active (transaction SRTIDOC)
  *(check with your basis team)*.
- The control record decides the partner: take SNDPRN/RCVPRN/SNDPOR/RCVPOR from parameters or the
  Partner Directory, not literals in mappings (lint `idoc-control-record-literal`).
- The response carries the IDoc number the backend assigned *(check)*: log it as a custom header
  property for monitoring.

## Schemas and mappings

- IDoc XSD: from the backend (WE60, XML schema of the IDoc type including extensions) or the SAP
  Business Accelerator Hub. Store it under `src/main/resources/xsd/` with the type in the name
  (`ORDERS05.xsd`, `ZORDERS05.xsd` for an extension); discovery reads IDoc types from these names.
- OData: the EDMX under `edmx/`; message mappings use it as source or target.
- Keep shared mappings (the same IDoc type to the same target) in one mapping artifact, not copies
  (lint `duplicate-mapping`).

## Testing

- IDoc sender flows: `cpictl idoc template --type ORDERS05 --mestyp ORDERS --sndprn … --rcvprn …`
  (MCP `idoc_template`) writes a skeleton with the control record; add the data segments from a real
  IDoc (WE19/WE02 in the backend, or a trace), then `send_test_message` with `idoc` (CLI
  `cpictl send --idoc file.xml`). Test a duplicate (same DOCNUM twice) and a wrong partner.
- OData sender flows: `send_test_message` with `odata_method`, `entity_set`, `key` and `query` (CLI
  `--odata-method`, `--entity-set`, `--key`, `--query`).
- OData and IDoc receivers in tests: on the mock tenant, receivers (IDoc included) answer as the landscape's
  systems say (latency, failures); on a real development tenant, point the receiver parameters at a test system
  or a stub, never at production.
- The mock tenant's demo landscape has `Orders_IDoc_In` (IDoc sender) and `Orders_OData_Service`
  (OData sender) to try this without a backend.
