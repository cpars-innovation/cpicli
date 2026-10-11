# Component catalogue

Every step and adapter in a flow model names its variant and version in `cmdVariantUri`, for
example `ctype::AdapterVariant/cname::sap:HTTP/tp::HTTP/mp::None/direction::Receiver/version::1.22.0`.
A tenant accepts the versions its editor offers and older ones. A newer version fails the
tenant's Check and the deployment. That happens when a flow comes from a newer tenant, or when
an agent writes the XML from memory.

The component catalogue `.cpi/components.json` lists what a tenant offers. It is used by:

- `cpictl lint`: rule `component-newer-than-tenant` (error) and `outdated-component` (what the
  tenant offers, not only what other flows use);
- the MCP tool `list_components`, which the build skill uses for new steps and adapters;
- people: `cpictl components list --name HTTP`.

## Building it

| Source | Command | What it knows |
|--------|---------|---------------|
| Editor metadata of the Web UI | `cpictl components import metadata.json` | Every adapter (also custom ones from packages) and step the editor offers, with the current version: authoritative |
| Flows that exist | `cpictl components learn` (tenant) or `--dir ./content` (offline); also every `cpictl discover` | The versions in use: automatic, but only what is used |

Entries from the editor metadata win over learned ones. Importing keeps the learned entries;
learning keeps the editor entries. Only editor entries are an upper bound: a flow newer than a
learned version is not reported.

### Getting the editor metadata

The editor loads it from `https://<tenant UI host>/api/1.0/metadata?profileID=<runtime profile>`
(for example `profileID=integrationcell` on a Cloud Foundry tenant with Edge Integration Cell
support). That endpoint needs the browser's login; an API service key gets a login page, so
cpictl cannot fetch it. To save it:

1. Open an integration flow in the Web UI and press Edit.
2. In the browser's developer tools (Network), find the request to `metadata?profileID=`.
3. Save the response as a JSON file (or copy it) and run
   `cpictl components import metadata.json --tenant dev`.

Repeat after a tenant upgrade (SAP updates tenants monthly). A pipeline can keep the learned part
current without a browser: `cpictl components learn` on a schedule.

## File format

```json
{
  "schema": "cpictl.components/v1",
  "tenant": "dev",
  "generatedAt": "2026-10-11T10:00:00Z",
  "sources": ["editor", "learned"],
  "sourceDates": {"editor": "2026-10-11T10:00:00Z", "learned": "2026-10-12T02:00:00Z"},
  "adapters": [
    {"name": "HTTP", "displayName": "HTTP", "direction": "Receiver", "transportProtocol": "HTTP",
     "messageProtocol": "None",
     "variantUri": "ctype::AdapterVariant/cname::sap:HTTP/tp::HTTP/mp::None/direction::Receiver/version::1.22.0",
     "version": "1.22.0", "adapterVersion": "1.22.0", "source": "editor"}
  ],
  "steps": [
    {"name": "GroovyScript", "displayName": "Groovy Script", "flavor": "GroovyScript", "type": "Activity",
     "variantUri": "ctype::FlowstepVariant/cname::GroovyScript/version::2.0.0", "version": "2.0.0", "source": "editor"}
  ]
}
```

- One entry per variant (the URI without its version); `version` is the newest known.
- `sourceDates` is when each source was last added; `generatedAt` changes with every write. The
  editor date tells whether the metadata predates a tenant upgrade (`components list` prints its
  age).
- `custom: true` marks adapters installed from a package (for example from the Business
  Accelerator Hub); a tier without that package does not offer them.
- In the model, `componentVersion` is the major.minor of the variant's version (`1.22` for
  `1.22.0`).
- Some variants have no version (`cname::EndEvent`, `cname::TerminateEndEvent`).

## Several tiers

Each tier has its own catalogue, since tiers are upgraded at different times and can have different
custom adapters. Keep one file per tier, for example `--file .cpi/components/prod.json`, and lint
against the target tier before a transport: `cpictl lint --components .cpi/components/prod.json`.
