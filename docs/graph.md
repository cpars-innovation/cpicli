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

## Nodes

IDs are `<type>:<key>`. Every tool also accepts a bare key (`Orders_In`; flows are tried first)
or a unique name.

| Type | Key | Example |
|------|-----|---------|
| `package` | package ID | `package:SalesOrders` |
| `iflow` | flow ID; attributes: package, version, runtime status, triggers, log level | `iflow:Orders_In` |
| `endpoint` | `<adapter>:<address>` of a sender, or of a ProcessDirect / JMS receiver | `endpoint:ProcessDirect:/billing/in`, `endpoint:HTTPS:/orders/in`, `endpoint:JMS:Q.Invoices` |
| `system` | host of a receiver URL (HTTP, SOAP, OData, ...) | `system:s4.example.com` |
| `credential` | credential name or key alias | `credential:SFTP_User` |
| `script` | a flow's script: content hash (identical copies in several flows are one node), name the file name, `summary` from its comment; a script collection's script: `<collection>/<file>` with `collection`, `summary` and `functions` (public functions with the first sentence of their doc comments), linked by `uses_script` from each flow that calls it (`via` is the function) | `script:3f2a9c0d1e4b`, `script:Orders_Scripts/Headers.groovy` |
| `header`, `property` | name set by a Content Modifier | `header:SAP_ApplicationID` |
| `customheader` | custom header property a script writes to the message log | `customheader:OrderId` |
| `pd` | literal Partner Directory parameter `PID:ID` (`:ID` when only the ID is literal) | `pd:SAP_SYSTEM_001:Identity` |

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

`exposes`, `calls` and `calls_system` carry the `adapter`.

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
  shows up as `calls_system` to the tenant's own host.
- Dynamic Partner Directory references (`pd:${...}`) and Groovy `getParameter` calls with
  computed arguments are not in the graph; `pd_dependencies` lists them for local content.
- `cpictl graph build` creates the graph from an existing `discovery.json`. Files from older
  cpictl versions lack receiver addresses and Partner Directory references, so discover again
  for the full graph. Without `graph.json`, the tools build the graph from `discovery.json` in
  the same folder.
