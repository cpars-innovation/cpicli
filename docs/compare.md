# Compare tiers, Git refs and content trees

`cpictl compare <A> <B>` compares two sides per artifact. A side is:

| Side | Meaning |
|------|---------|
| `<directory>` | a content tree (`<package>/<artifact>`, as `snapshot` writes it) |
| `git:<ref>[:<path>]` | a branch, tag or commit of the repository (`--repo`, default `.`), optionally only a sub-folder |
| `tenant` | the configured tenant (`--profile`, config file, environment) |
| `tenant:<profile>` | the tenant of a [profile](configuration.md#profiles-switching-tenants) (`~/.cpictl/<profile>.yaml`) |

```bash
cpictl compare tenant:test tenant:prod --package Orders     # what would a transport change?
cpictl compare packages tenant --diff                       # repository vs tenant (drift)
cpictl compare git:release/2026-10:packages git:main:packages
cpictl compare tenant:dev tenant:test --fail-on-diff         # CI gate, exit code 5 on differences
cpictl compare tenant:test tenant:prod --artifact Orders_Route --model   # per step and property
```

Tenants are only read. Their artifacts are downloaded and normalized exactly as `snapshot` writes
them (LF line endings, `parameters.prop` and `metainfo.prop` without timestamps), so a snapshot and
its tenant compare as `same`.

Per artifact (matched by artifact ID, across package folders):

| Status | Meaning |
|--------|---------|
| `same` | same content, version and parameters |
| `content_differs` | the content differs as an upload compares it (without `Bundle-Version` and `parameters.prop`) |
| `version_differs` | same content, different version |
| `parameters_differ` | same content and version, different `parameters.prop` |
| `only_a`, `only_b` | on one side only |

- **Files**: added (only in B), removed (only in A) or changed; `--diff` adds unified diffs of text
  files (binaries and very large files are marked).
- **Models** (`--model`): per changed `.iflw` a semantic diff per element and per property, see
  below.
- **Parameters**: per `parameters.prop` key `differs`, `only_a`, `only_b`; values only with
  `--show-values`.
- **Tenant sides**: the designtime version (used as the version: the exported manifest can lag
  behind it), the running version and status, the draft flag, and who changed the artifact last
  where the tenant reports it.

## Model diff (`--model`)

An .iflw is XML whose elements are often on one line, so a line diff of it is unreadable.
`--model` compares each changed `.iflw` as a model instead (`files[].model` in JSON; with
`--diff` too, the unified diff stays):

```text
content_differs   Orders/Orders_Route (test: 1.0.6, prod: 1.0.5)
    changed  src/main/resources/scenarioflows/integrationflow/Orders_Route.iflw
      ~ integration flow: log: All events -> Errors only
      + step CallActivity_1 (GroovyScript 'Map')
      ~ event EndEvent_1 (MessageEndEvent 'End'): moved
      ~ adapter MessageFlow_R0 (HTTP): credentialName: *** -> ***
      ~ adapter MessageFlow_Sender (ProcessDirect): address: /orders/route -> /orders/route/v2
      ~ step ServiceTask_0 (ExternalCall 'Call S/4HANA'): renamed 'Call ERP' -> 'Call S/4HANA'
      - flow SequenceFlow_1 (ServiceTask_0 -> EndEvent_1)
      + flow SequenceFlow_2 (ServiceTask_0 -> CallActivity_1)
```

- **Elements** (`model.elements`, by ID): steps, events, gateways, processes and subprocesses,
  participants (pools) and message flows (adapters: address, authentication, `componentVersion`,
  `cmdVariantUri`, ...). Each has `id`, `type` (BPMN tag), `kind` (component, adapter type or
  participant type), `name`, `change` (`added`, `removed`, `changed`), `properties` (changed
  `ifl:property` values and attributes such as `@container`, `@sourceRef`, `@targetRef`,
  `@processRef`, `@type`; for an added or removed element all its values), `renamed` (`[A, B]`)
  and `moved`.
- **Sequence flows** (`model.flows`): `source`, `target`, and as properties changed `@sourceRef`,
  `@targetRef` and `@condition` (the condition expression).
- **The integration flow's own properties** (`model.collaboration`): log level, namespace
  mapping, ...
- **Geometry**: a shape or line that only moved (BPMN DI bounds or waypoints) sets `moved` and is
  no property change; a file that differs only in formatting has an empty model diff.
- **Sensitive values**: values of properties whose keys look sensitive (password, secret, token,
  credential, API key, authorization, cookie) are `***`.
- Everything is sorted by ID, then key. A file that is not a BPMN model has `modelError`.

`--package` / `--artifact` (IDs or patterns) limit the comparison and the downloads; `--parallel`
sets the downloads at the same time per tenant (default 8). `--output json` returns `a`, `b`,
`items` and `summary`. The MCP tool `compare` does the same (`model: true` for the model diff) with `tenant`, a directory inside the
server root or `git:<ref>[:<path>]`.
