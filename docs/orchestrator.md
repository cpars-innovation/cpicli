# Orchestrator and config-generate

`cpictl orchestrator` updates and deploys many packages and artifacts from a local
directory tree in one run, driven by a deployment config file. `cpictl config-generate`
creates or refreshes that config file from the directory tree.

## Directory layout

```
packages/                          # --packages-dir
└── MyPackage/                     # packageDir
    ├── MyPackage.json             # optional package metadata ({"d": {"Id", "Name", "Description", "ShortText"}})
    └── MyIFlow/                   # artifactDir
        ├── META-INF/MANIFEST.MF
        └── src/main/resources/
            ├── parameters.prop
            └── scenarioflows/integrationflow/MyIFlow.iflw
```

This is the layout written by `cpictl snapshot` / `sync` (Git directory naming by ID).

## Deployment config

Example: [examples/deploy-config.yml](examples/deploy-config.yml).

```yaml
deploymentPrefix: DEV            # optional, see "Prefixes"
packages:
  - integrationSuiteId: MyPackage
    packageDir: MyPackage        # relative to --packages-dir
    displayName: My Package      # optional
    description: ...             # optional, defaults to the display name
    short_text: ...              # optional, defaults to the display name
    sync: true                   # default true: update package + artifacts on the tenant
    deploy: true                 # default true: deploy the package's artifacts
    artifacts:
      - artifactId: MyIFlow
        artifactDir: MyIFlow     # relative to packageDir
        displayName: My iFlow
        type: IntegrationFlow    # SAP-BundleType from MANIFEST.MF, see below
        sync: true               # default true
        deploy: true             # default true
        configOverrides:         # optional, merged into parameters.prop before upload
          ReceiverHost: dev.example.com
          Timeout: 30
        versioning: keep         # optional exception; the mode comes from --versioning / CPICTL_VERSIONING
```

`versioning` (package or artifact) overrides `--versioning` for exceptions that hold on every
branch; see [versioning.md](versioning.md).

`type` accepts the MANIFEST `SAP-BundleType` values (`IntegrationFlow`, `MessageMapping`,
`ScriptCollection`, `ValueMapping`; case-insensitive) or the API names (`Integration`, ...).
Unknown values are treated as integration flows.

`configOverrides` are only applied when the artifact has a `parameters.prop`
(`src/main/resources/`, `src/main/resources/script/` or the artifact root).

## Running

```bash
cpictl orchestrator --packages-dir ./packages --deploy-config ./001-deploy-config.yml
```

| Flag | Default | Description |
|------|---------|-------------|
| `--packages-dir`, `-d` | | Root of the package directories |
| `--deploy-config`, `-c` | | Config file, folder or public URL |
| `--update` / `--update-only` / `--deploy-only` | update + deploy | Operation mode |
| `--deployment-prefix`, `-p` | | Overrides `deploymentPrefix` of the config(s) |
| `--package-filter` | | Comma-separated package IDs to include (without prefix) |
| `--artifact-filter` | | Comma-separated artifacts to include (without prefix): an `artifactId` selects that ID only; an `artifactDir` (folder name, as change detection passes it) or the folder's source ID (`Bundle-SymbolicName`) selects every ID deployed from that folder |
| `--config-pattern` | `*.y*ml` | File pattern when `--deploy-config` is a folder |
| `--merge-configs` | `false` | Merge all config files into one run |
| `--parallel` | `8` | Artifacts uploaded (compared) at the same time, across all packages |
| `--parallel-deployments` | `5` | Deployments triggered at a time; all triggered ones are watched together ([deployments.md](deployments.md)) |
| `--deploy-timeout` | `10m` | How long to wait for all deployments of the run together |
| `--deploy-delay` | `10` | Seconds between status checks (one runtime list for all deployments) |
| `--deploy-retries` | | Deprecated: when set without `--deploy-timeout`, the timeout is retries × delay |
| `--keep-temp` | `false` | Keep the temporary working directory |
| `--snapshot-state` | `.cpi/snapshot-state.json` (current directory, else above `--packages-dir`) | Snapshot state to compare with instead of downloading; `off` always downloads |
| `--defer-deploy` | `false` | Skip phase 2: add the deployments to `.cpi/pending-deploy.json` (`--pending-file`) for one `cpictl deploy --pending`, see [ci.md](ci.md#pipeline-snapshot-update-configure-deploy-once) |
| `--plan` | `false` | Only report per artifact what would be uploaded and deployed, and why; nothing is written |
| `--verify-download` | `false` | Download every existing artifact for the comparison anyway |
| `--draft-handling` | `SKIP` | Artifacts in draft on the tenant: `SKIP` (not uploaded, not deployed, reported) or `ERROR` (the run fails, exit 5); `--fail-on-draft` is the same as `ERROR`, see [drafts](#drafts) |

All of these can be set in the global config file under `orchestrator:` (camelCase keys, e.g.
`packagesDir`, `deployConfig`, `parallelDeployments`, `mode: update-only`).

### What happens

The orchestrator only reads `--packages-dir`: renamed IDs and names, `configOverrides`,
`deploymentPrefix` and manifest changes are applied to a temporary copy, so the directory can be
the Git working tree. One `artifactDir` can be deployed under several `artifactId`s, each with its
own `configOverrides`; `--plan` lists every ID.

**Phase 1, update** (skipped with `--deploy-only`), per package with `sync: true`:

1. The package is created or updated (ID, name, description, short text).
2. Each artifact with `sync: true` is copied to a temporary directory; `MANIFEST.MF` gets the
   final ID and name (`Bundle-SymbolicName`, `Bundle-Name`), `configOverrides` are merged into
   `parameters.prop`, and the artifact is created or updated on the tenant if its content differs
   ([comparison](#comparison-without-downloads)).
   If the content changed but the version equals the running one, phase 2 deploys it with force
   (the runtime keeps running until then; earlier versions undeployed it right away).

**Phase 2, deploy** (skipped with `--update-only`): artifacts with `deploy: true` (whose update
did not fail) are deployed package by package, up to `--parallel-deployments` at a time. An
artifact whose runtime version already equals the designtime version is skipped.

With `--output json` the result contains the statistics, `artifacts` (one entry per deployed
artifact ID), `counts` and one deployment result per artifact. Failures give exit code 7 when
anything succeeded, otherwise 5.

| `artifacts[].status` | Meaning |
|----------------------|---------|
| `created`, `updated` | uploaded in phase 1 |
| `unchanged` | the tenant had the content already |
| `failed` | the upload failed (see `reason`), not deployed |
| `skipped-draft` | in draft on the tenant: not uploaded, not deployed ([drafts](#drafts)) |
| `not-uploaded` | deployed without an upload (`--deploy-only`, `sync: false`) |

`artifacts[].deploy` is the deployment result (`DEPLOYED`, `SKIPPED`, `FAILED`, `TIMEOUT`),
empty when not deployed. `counts` has `created`, `updated`, `unchanged`, `failed`,
`skippedDraft`, `notUploaded`, `deployed` and `deployFailed`.

### Drafts

An artifact in draft on the tenant is being edited in the Web UI. By default
(`--draft-handling SKIP`, config `orchestrator.draftHandling`, `CPICTL_DRAFT_HANDLING`) the
orchestrator leaves it alone and goes on with the others:

- it is neither uploaded nor deployed; the log has one warning per ID
  (`<id> is in draft on the tenant: not uploaded, not deployed`) and the summary lists them;
- its status is `skipped-draft`, `counts.skippedDraft` and `stats.skippedDrafts` (ID, package,
  tenant designtime version, running version, repository `Bundle-Version`) list them, and the
  [job summary](ci.md#job-summary) has a section for them;
- drafts alone do not fail the run (exit code 0).

The check is per deployed ID: when only a deployment copy (another `artifactId` with the same
`artifactDir`, or a `deploymentPrefix` variant) is in draft, only that ID is skipped; the source
is handled on its own. Artifacts deployed without an upload (`--deploy-only`, `sync: false`) are
checked too (one request per package). `--plan` predicts the skip
(`upload: skipped-draft  no deploy`).

`--draft-handling ERROR` (or `--fail-on-draft`) keeps the earlier behaviour: a draft is a
failure and the run ends with exit code 5. Save the version in the Web UI (or discard the draft)
and run again.

### Plan

`--plan` runs the comparison of phase 1 and the version checks of phase 2 without writing
anything (only GET requests). Per artifact:

| Field | Values |
|-------|--------|
| `upload` | `create`, `update`, `unchanged`, `skipped-draft` (in draft on the tenant, not deployed), `fails` (see `error`) |
| `compared` | `snapshot` or `download` (see below) |
| `designtime` | the designtime version after the upload (versioning mode applied) |
| `running`, `runtimeStatus` | what runs now |
| `deploy`, `reason` | whether phase 2 would deploy, e.g. `not deployed yet`, `designtime 1.0.6, running 1.0.5`, `content changed, same version 1.0.5: undeployed and deployed again`, `running 1.0.5 already (STARTED)` |

```bash
cpictl orchestrator -d ./packages -c ./001-deploy-config.yml --plan --output json \
  | jq '.result.plan[] | select(.upload != "unchanged" or .deploy)'
```

Anything that would fail (a refused version, a missing `Bundle-Version` in `manifest` mode) is
listed with `error` and gives exit code 5, so a pipeline can stop before changing the tenant.
`configure --plan` (same as `--dry-run`) reports the same for parameters, and the MCP tools
`upload_artifacts` and `deploy` take `dry_run`.

### Comparison without downloads

To know whether an existing artifact needs an upload, its content is compared with the tenant's.
When the pipeline ran `snapshot` before, the orchestrator compares with what the snapshot saw
instead of downloading every artifact again:

```
snapshot (incremental)  ->  copy the managed parts into the snapshot  ->  orchestrator
writes .cpi/snapshot-state.json      (scripts, mappings, ...)             compares with the state
```

Per artifact, one small call reads the designtime version and `ModifiedAt`. When both are still
what the snapshot recorded, the local content (after the manifest and parameter changes above) is
hashed and compared with the hash of the tenant's content in the state: equal means unchanged,
no download and no upload. The hash covers what the download comparison covers (`META-INF`,
`src/main/resources`, `metainfo.prop`; value mappings: `META-INF` and `value_mapping.xml`),
without `parameters.prop` (parameters are written separately) and without `Bundle-Version`
(set by the [versioning mode](versioning.md)).

The artifact is downloaded and compared as before when:

- it is not in the state (new on the tenant, not in the snapshot, older state without hashes),
- the tenant changed it since the snapshot (version or `ModifiedAt` differ, e.g. a Web UI edit),
- the tenant reports no `ModifiedAt`,
- the state is of another tenant (it records the host), or `--snapshot-state off`,
- `--verify-download` is set.

The summary and the JSON statistics report `comparedWithSnapshot`, `downloadedForComparison`,
`artifactsChanged` and `artifactsUnchanged`. Not verified on a real tenant yet: that every Web UI
edit changes `ModifiedAt` (check once on a development tenant: run `snapshot --incremental`, edit
a flow in the Web UI, run it again; the flow must be downloaded, not skipped). Until then, `--verify-download` in a
nightly run is a cheap safety net.

### Config sources

- **File**: one config.
- **Folder**: all files matching `--config-pattern`, recursively, in alphabetical order.
  Without `--merge-configs` each file is processed on its own with its own prefix.
  With `--merge-configs` all packages are processed in one run; each file's prefix is applied
  to its packages, `--deployment-prefix` is ignored and duplicate final package IDs are an error.
- **URL**: `https://...` (public only; authentication is not supported).

### Prefixes

With prefix `DEV`:

| | Without prefix | With prefix |
|---|---|---|
| Package ID | `MyPackage` | `DEVMyPackage` |
| Package name | `My Package` | `DEV - My Package` |
| Artifact ID | `MyIFlow` | `DEV_MyIFlow` |

Prefixes may contain letters, digits and `_`. Note that `configure` uses a different rule
(prefix + ID for packages *and* artifacts, no `_`), see [configure.md](configure.md).

## config-generate

```bash
cpictl config-generate --packages-dir ./packages --output-file ./001-deploy-config.yml
```

- Scans `--packages-dir`: every sub-directory is a package, every sub-directory of a package
  is an artifact.
- Package name, description and short text come from `<Package>/<Package>.json`;
  artifact display name and type from `MANIFEST.MF` (`Bundle-Name`, `SAP-BundleType`).
- If the output file exists, its settings are preserved: `deploymentPrefix`, the
  `orchestrator` section, `sync`/`deploy` flags, display names, types and `configOverrides`.
  Missing `sync`/`deploy` keep their default `true`.
- Packages and artifacts that no longer exist on disk are removed from the file.
- `--package-filter` / `--artifact-filter` restrict the scan.

`config-generate` works offline: it needs no tenant settings.
