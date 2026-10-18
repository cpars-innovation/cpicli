# New flows from templates

A new integration flow starts as a copy of a flow that already works on your tenant, never as an
empty model. A copy brings the house conventions (exception subprocess, logging scripts,
externalised parameters, component versions the tenant supports) with it.
`cpictl iflow copy` (MCP: `copy_iflow`) makes that copy and renames everything that must be
unique, so the copy can be uploaded and deployed next to the original.

```
brief  ->  plan  ->  iflow copy  ->  edit  ->  update artifact  ->  validate  ->  deploy  ->  test
(.cpi/briefs)  (.cpi/plans)   (from a template)          (upload)
```

## 1. Templates

A template is an ordinary integration flow that the team keeps as a starting point for one
pattern. Two common setups:

- **Dedicated template flows** (recommended): a package such as `Templates` with flows
  `Template_Sync_HTTPS_OData`, `Template_Async_SFTP_IDoc`, `Template_ProcessDirect_Subflow`, ...
  They contain the house skeleton (exception subprocess, logging, parameters) and placeholder
  steps. Keep them deployable: deploy each template once after a change, so you know it works.
  Give them sender addresses nobody uses (`/template/...`), or keep them undeployed.
- **Reference flows**: good productive flows named per pattern. No extra maintenance, but they
  carry business logic you have to remove after the copy.

List them in the *Templates* section of `.cpi/conventions.md` (the `cpi-discover` skill of the
plugin proposes candidates; the team marks them `agreed`):

```markdown
| Pattern | Reference flow (package) | Why this one | Status |
|---------|--------------------------|--------------|--------|
| sync HTTPS -> OData | Template_Sync_HTTPS_OData (Templates) | house skeleton, CSRF off, retries | agreed |
```

The house logging and error handling scripts can additionally live in
`.cpi/templates/scripts/`; the agent reuses them when it needs a script that the template
does not have.

## 2. Brief

The brief collects what the requester knows before anyone designs the flow: purpose, IDs and
names (or leave them to the naming conventions), sender, receivers, messages and samples,
mapping, error behaviour, environments, test data and acceptance criteria. The `cpi-plan`
skill copies `.cpi/templates/brief.md` (created from the plugin's
[brief template](../plugin/skills/cpi-plan/brief-template.md) on first use, then yours to adapt)
to `.cpi/briefs/<name>.md`. The plan in `.cpi/plans/<FlowId>.md` then names the template, the
new ID, name, description and sender address: everything `iflow copy` needs.

## 3. Copy

```bash
# from the tenant (read only), template with one sender address
cpictl iflow copy --from Template_Sync_HTTPS_OData \
  --id SD_Orders_S4_Sync --name "SD Orders to S/4 (sync)" \
  --description "Order intake from the web shop, creates sales orders in S/4" \
  --address /sd/orders/s4 \
  --dir content/SDOrders/SD_Orders_S4_Sync

# from a local folder (offline), template with two sender addresses
cpictl iflow copy --from-dir content/Templates/Template_Async_SFTP_IDoc \
  --id FI_Invoices_In --name "FI incoming invoices" \
  --address '{{SFTP_Directory}}=/in/fi/invoices' \
  --address /test/Template_Async_SFTP_IDoc=/test/FI_Invoices_In
```

| Flag | Meaning |
|------|---------|
| `--from ID` | Source flow on the tenant (`--from-version`, default `active`). Needs a tenant connection, reads only. |
| `--from-dir DIR` | Source flow in a local folder (the layout of `download` / `sync`). Works offline. |
| `--id` | New flow ID (`Bundle-SymbolicName`). Letters, digits, `_`, `.`, `-`, at most 100 characters; follow your naming conventions. |
| `--name` | Display name (`Bundle-Name`), default the ID. |
| `--description` | Description (`metainfo.prop`). Without it, the template's description is **removed**, so the copy never claims to be the template. |
| `--version` | `Bundle-Version` of the copy, default `1.0.0`. |
| `--dir` | Target folder; must not exist or be empty. Default: `<ID>` next to `--from-dir`, or `./<ID>`. |
| `--address` | New sender address, see below. Repeatable. |
| `--keep-addresses` | Allow sender addresses to stay the same as in the source. |

### What is changed

| File | Change |
|------|--------|
| `META-INF/MANIFEST.MF` | `Bundle-SymbolicName` (a `;singleton:=true` suffix is kept), `Bundle-Name`, `Bundle-Version`. Values longer than a manifest line are wrapped at 72 bytes as the manifest format requires; all other headers stay byte for byte. |
| `metainfo.prop` | `description` (other keys kept). Non-ASCII characters are written as `\uXXXX`, as Java properties files expect. |
| `src/main/resources/scenarioflows/integrationflow/<SourceID>.iflw` | Renamed to `<ID>.iflw`. A model file with another name keeps it. |
| The model (`.iflw`) | Sender addresses given with `--address` (literal addresses). |
| `src/main/resources/parameters.prop` | Sender addresses that are a `{{parameter}}`: the parameter's value (the model keeps the parameter). `:` and `=` are escaped as CPI writes them. |
| `.project` | The project name. |

### What is not changed

Receivers (URLs, ProcessDirect calls, credentials), steps, scripts, mappings, other parameters
and the names of integration processes are copied unchanged: they are what the template is for,
and you adapt them in the next step. The result lists every file that still contains the source
ID (`remaining`), typically process names, scripts that log the flow name, or parameter values.
Check each one.

### Sender addresses

Every sender adapter that has an address must get a new one, because two deployed flows cannot
share it:

| Sender | Address (property) | Same address twice |
|--------|--------------------|--------------------|
| HTTPS, SOAP, OData, IDoc, REST | path (`urlPath`, `address`) | deployment of the second flow fails |
| ProcessDirect | `address` | deployment of the second flow fails |
| SFTP, FTP | directory (`path`, `directory`) | both flows poll the directory and take each other's files |
| JMS | queue (`QueueName_inbound`) | both flows consume the same queue |
| Timer | none | nothing to change |

- One sender address: `--address NEW`.
- Several: `--address OLD=NEW` for each, `OLD` exactly as in the source: the literal path, or the
  parameter reference such as `{{Inbound_Path}}`. A value containing `=` is always read as
  `OLD=NEW`, so a mistyped `OLD` is an error, never a new address.
- A `{{parameter}}` address is changed in `parameters.prop`, so the copy stays parameterised.
  Addresses that mix text and parameters (`/orders/{{Env}}`) are replaced in the model with the
  value you give.
- `--keep-addresses` copies an address unchanged, for example when the copy replaces the
  original and the original is undeployed first. Without it, a missing address is an error and
  nothing is written.

### Result

```json
{
  "from": "tenant:Template_Sync_HTTPS_OData",
  "sourceId": "Template_Sync_HTTPS_OData",
  "id": "SD_Orders_S4_Sync",
  "name": "SD Orders to S/4 (sync)",
  "version": "1.0.0",
  "dir": "content/SDOrders/SD_Orders_S4_Sync",
  "files": 12,
  "changes": [
    "MANIFEST.MF: Bundle-SymbolicName Template_Sync_HTTPS_OData -> SD_Orders_S4_Sync, ...",
    "model Template_Sync_HTTPS_OData.iflw -> SD_Orders_S4_Sync.iflw",
    "HTTPS sender address /template/sync -> /sd/orders/s4 (model)",
    "metainfo.prop: description set",
    ".project: name Template_Sync_HTTPS_OData -> SD_Orders_S4_Sync"
  ],
  "addresses": [{"adapter": "HTTPS", "old": "/template/sync", "new": "/sd/orders/s4", "where": "model"}],
  "remaining": ["src/main/resources/script/logging.groovy (1)"]
}
```

With `--output json` this is the `result` of the usual envelope. Errors: exit code 2 (usage) for
an invalid ID, a target folder that is not empty, an unchanged or unknown sender address, a
source that is not an integration flow; 3/4 for tenant errors with `--from`. On any error the
target folder is left as it was (removed again if `iflow copy` created it).

## 4. Edit, upload, deploy

1. Check the `remaining` files and adapt the copy: receivers, mapping, scripts, parameter values
   for the development tenant, the test entry (`/test/<FlowId>`) if your conventions use one.
2. Create the package if it is new: `cpictl packages create --package-id SDOrders`.
3. Upload: `cpictl update artifact --artifact-id SD_Orders_S4_Sync --package-id SDOrders
   --dir-artifact content/SDOrders/SD_Orders_S4_Sync`. The display name comes from
   `Bundle-Name` (MCP `upload_artifacts` does the same when `name` is not given).
4. `cpictl validate --artifact-id SD_Orders_S4_Sync`, then
   `cpictl deploy --artifact-ids SD_Orders_S4_Sync`. A deployment error such as a missing
   credential is in the output.
5. Test it ([testing.md](testing.md)).

## With an agent

The MCP tool `copy_iflow` takes the same options (`from_artifact_id` or `from_dir`,
`target_dir`, `id`, `name`, `description`, `version`, `addresses`, `keep_addresses`); paths are
relative to the server root and must stay inside it. It writes local files only, so it is
available in every mode (`discover` included). The `cpi-build` skill uses it for every new flow: the plan names
the template and the new ID, name and address, the skill copies, adapts, uploads and deploys.

```text
> Build SD_Orders_S4_Sync from the plan.
```

## Limits

- Only sender addresses are changed. A receiver that points to the template's own resources
  (for example a ProcessDirect call to a template sub-flow) is copied as is.
- References to script collections, message mappings or value mappings outside the flow are
  kept; the copy uses the same shared artifacts as the template.
- The display name and description are what `iflow copy` writes; renaming integration
  processes or steps inside the model is up to you.
- Tested against models and archives as `download` writes them; check the first copies of your
  own templates in the Web UI before relying on it.
