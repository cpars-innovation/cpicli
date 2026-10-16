# Content graph

`cpictl discover` (MCP: `discover_tenant`) writes two files:

| File | What it is | Good for |
|------|------------|----------|
| `.cpi/discovery.json` | Facts per flow plus a summary (counts, naming, shared scripts) | Deriving conventions |
| `.cpi/graph.json` | The same content as nodes and edges | Lookups: who calls whom, who uses what |

Agents query the graph with three tools instead of reading the whole discovery file. The tools
read the local file only; they never contact the tenant and work in every mode, including
`discover`.

| Question | MCP call | CLI |
|----------|----------|-----|
| Where is the billing flow / address / host? | `graph_search {query: "billing"}` | `cpictl graph search billing` |
| Which flows call `Billing`? | `graph_neighbors {node: "Billing", direction: "in", edge_types: ["sends_to"]}` | `cpictl graph neighbors Billing --direction in --edge-types sends_to` |
| What does `Orders_In` trigger downstream? | `graph_neighbors {node: "Orders_In", direction: "out", edge_types: ["sends_to"], depth: 3}` | `cpictl graph neighbors Orders_In --direction out --edge-types sends_to --depth 3` |
| Which flows use credential `SFTP_User`? | `graph_neighbors {node: "credential:SFTP_User"}` | `cpictl graph neighbors credential:SFTP_User` |
| Which flows read PD parameter `SAP_SYSTEM_001:Identity`? | `graph_neighbors {node: "pd:SAP_SYSTEM_001:Identity"}` | `cpictl graph neighbors pd:SAP_SYSTEM_001:Identity` |
| Which flows call the S/4 system? | `graph_neighbors {node: "system:s4.example.com"}` | `cpictl graph neighbors system:s4.example.com` |
| How does a message get from `Orders_In` to `Invoice_Send`? | `graph_path {from: "Orders_In", to: "Invoice_Send"}` | `cpictl graph path Orders_In Invoice_Send` |
| Which flows handle IDoc type `ORDERS05`? | `graph_search {query: "ORDERS05"}`, then `graph_neighbors {node: "idoc_type:ORDERS05"}` | `cpictl graph neighbors idoc_type:ORDERS05` |
| Which flows write sales orders in S/4? | `graph_neighbors {node: "odata_entity:s4.example.com/A_SalesOrder", edge_types: ["writes_odata"]}` | `cpictl graph neighbors odata_entity:s4.example.com/A_SalesOrder --edge-types writes_odata` |

## Nodes

IDs are `<type>:<key>`. Every tool also accepts a bare key (`Orders_In`; flows are tried first)
or a unique name.

| Type | Key | Example |
|------|-----|---------|
| `package` | package ID | `package:SalesOrders` |
| `iflow` | flow ID; attributes: package, version, runtime status, triggers, log level, IDoc types | `iflow:Orders_In` |
| `endpoint` | `<adapter>:<address>` of a sender, or of a ProcessDirect / JMS receiver | `endpoint:ProcessDirect:/billing/in`, `endpoint:HTTPS:/orders/in`, `endpoint:JMS:Q.Invoices` |
| `system` | host of a receiver URL (HTTP, SOAP, OData, ...) | `system:s4.example.com` |
| `credential` | credential name or key alias | `credential:SFTP_User` |
| `script` | a flow's script: content hash (identical copies in several flows are one node), name the file name; a script collection's script: `<collection>/<file>` with `collection`, `package` and `functions` (public functions with the first sentence of their doc comments), linked by `uses_script` from each flow that calls it (`via` is the function). Both carry what reuse needs ([script-docs.md](script-docs.md)): `reusable` (`true` for script collections), `summary`, `keywords`, `reads` and `writes` (headers and properties by name), `docStatus` (`ok`, `incomplete`, `missing`), `usages` (flows using it) and `shape` (equal for scripts that differ only in comments and formatting) | `script:3f2a9c0d1e4b`, `script:Orders_Scripts/Headers.groovy` |
| `header`, `property` | name set by a Content Modifier | `header:SAP_ApplicationID` |
| `customheader` | custom header property a script writes to the message log | `customheader:OrderId` |
| `pd` | literal Partner Directory parameter `PID:ID` (`:ID` when only the ID is literal) | `pd:SAP_SYSTEM_001:Identity` |
| `odata_entity` | `<host>/<entity set>` an OData or SuccessFactors OData receiver reads or writes; `SuccessFactors/<entity set>` for SuccessFactors without host; attributes: `version` (V2, V4), `operations` (all flows), `host`, `entitySet`. A host that cannot be resolved makes an entity of its own flow (`…@<flow>`, `unresolved`) | `odata_entity:s4.example.com/A_SalesOrder`, `odata_entity:SuccessFactors/User` |
| `idoc_type` | IDoc type (basic type or extension) a flow with an IDoc channel handles | `idoc_type:ORDERS05`, `idoc_type:ZORDERS05` |

## Edges

| Type | From → to |
|------|-----------|
| `contains` | package → iflow |
| `exposes` | iflow → endpoint it is started by |
| `calls` | iflow → ProcessDirect / JMS endpoint it sends to |
| `sends_to` | iflow → iflow, derived: the caller's `calls` endpoint is the other flow's `exposes` endpoint (`via` names it) |
| `calls_system` | iflow → system |
| `uses_credential` | iflow → credential |
| `uses_script` | iflow → script |
| `sets_header`, `sets_property` | iflow → header / property |
| `logs_header` | script → customheader |
| `reads_pd` | iflow → pd |
| `reads_odata` | iflow → odata_entity, operation Query or Read (`via` is the operation) |
| `writes_odata` | iflow → odata_entity, operation Create, Update, Merge, Patch, Delete, Function Import or `$batch` (`via` is the operation) |
| `receives_idoc` | iflow → idoc_type, the flow has an IDoc sender channel (`via`: where the type was found) |
| `sends_idoc` | iflow → idoc_type, the flow has an IDoc receiver channel (`via`: where the type was found) |

`exposes`, `calls`, `calls_system`, `reads_odata` and `writes_odata` carry the `adapter`.

## OData and IDoc

Discovery reads OData and IDoc channels beyond their address (`iflows[].receivers[].odata`,
`.idoc`; the same on `triggers` for senders):

- **OData** (OData V2/V4 receiver, SuccessFactors OData V2/V4, OData senders): `version`,
  `operation`, `resourcePath` and `entitySet`, `queryOptions` and their keys, `paging` and
  `pageSize`, `batch`, the `edmx` file under `edmx/` the channel was built from (named after the
  service URL), and the `host`.
- **IDoc**: `direction`, the address `path` (`/sap/bc/srt/idoc`), a literal `sapClient`, the
  `contentType`.

The IDoc adapter does not name the IDoc type, so discovery takes it from the flow's content
(`iflows[].idocTypes`, each with its `sources`): XPaths `/<TYPE>/IDOC`, `IDOCTYP` conditions and
payloads in steps and router conditions, and mappings and schemas that contain the control record
`EDI_DC40` (a name like `ORDERS05` or `ZORDERS05` in the file name, a schema's root element). A
flow with types but without an IDoc channel (e.g. a mapping flow behind ProcessDirect) gets no
IDoc edges; `graph_search` still finds it through its `idocTypes` attribute.

The graph links only OData receivers to entities, and only when the entity set and the operation
are known: a `{{parameter}}` resource path without a value or a computed operation gives no edge.

## Addresses and parameters

Addresses are often externalised (`{{Billing_Address}}`). Discovery resolves them with the
value in the flow's `parameters.prop`; the edge then says `via: "parameter Billing_Address"`.
That is the value of the downloaded design time. A value set on the tenant with *Configure*
(or `cpictl configure`) can differ, and the graph does not see it.

An address that cannot be resolved (no value, or computed at runtime with `${...}`) gets an
endpoint node of its own flow (`endpoint:ProcessDirect:{{X}}@Orders_In`, attribute
`unresolved`). Two flows are never linked just because they use the same parameter name.

## Limits

- The graph shows what the last discovery saw. Run discover again after changes; on a shared
  tenant, a discovery limited with `--package-ids` only links the flows of those packages
  (calls to other packages end at an endpoint without a flow behind it).
- Only ProcessDirect and JMS addresses link flows. A flow that calls another flow over HTTP
  shows up as `calls_system` to the tenant's own host. OData entities and IDoc types relate flows
  that work on the same data; they do not mean that one calls the other.
- Dynamic Partner Directory references (`pd:${...}`) and Groovy `getParameter` calls with
  computed arguments are not in the graph; `pd_dependencies` lists them for local content.
- `cpictl graph build` creates the graph from an existing `discovery.json`. Files from older
  cpictl versions lack receiver addresses and Partner Directory references, so discover again
  for the full graph. Without `graph.json`, the tools build the graph from `discovery.json` in
  the same folder.
