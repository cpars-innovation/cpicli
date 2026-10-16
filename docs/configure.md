# Configuring parameters

Three ways to change externalised parameters of integration flows:

| Command | Use for |
|---------|---------|
| `params get` / `params set` | One artifact, interactive or scripted |
| `configure` | Many artifacts from YAML files (per environment), optionally deploy afterwards |
| `configure pull` | Write the current tenant values into `configure` YAML files |

Parameter changes only take effect at runtime after a deployment.

## params

```bash
cpictl params get --artifact-id MyIFlow
cpictl params set --artifact-id MyIFlow --param ReceiverHost=api.example.com --param Timeout=60
cpictl params set --artifact-id MyIFlow --param Timeout=60 --dry-run
cpictl deploy --artifact-ids MyIFlow --compare-versions=false
```

`params set` writes only values that differ. An unknown key fails the call (exit code 2)
before anything is written. `--compare-versions=false` is needed on the following deploy
because a parameter change does not change the artifact version.

## configure

```bash
cpictl configure --config-path ./config/dev.yml
cpictl configure --config-path ./config/          # all *.yml / *.yaml in the folder (not recursive)
cpictl configure --config-path ./config/dev.yml --dry-run            # diff against the tenant
cpictl configure --config-path ./config/dev.yml --dry-run --offline  # file only, no tenant calls
cpictl configure --config-path ./config/dev.yml --force              # write and deploy everything
```

`configure` compares every artifact's parameters with the tenant first and writes only the keys
that differ. Artifacts marked for deployment are deployed when at least one key changed (always:
a configuration change does not change the version), and otherwise only when the runtime does
not run the designtime version yet (for example after an upload earlier in the pipeline). A
second run on an unchanged tenant therefore writes and deploys nothing. A key that the artifact
does not have (`unknown_key`, usually a typo) fails that artifact before anything is written.

Artifacts without `parameters` (script collections, message and value mappings, flows that only
need a deployment) are not read or written; with `deploy: true` they are deployed unless the
runtime already runs their designtime version. Only integration flows have externalised
parameters: `parameters` on another type is an error for that artifact.
`--force` writes all parameters and deploys all marked artifacts, as earlier versions did. The
MCP tool `config_diff` shows the same comparison.

Example: [examples/configure.yml](examples/configure.yml).

```yaml
deploymentPrefix: ""                # optional, prepended to package and artifact IDs
packages:
  - integrationSuiteId: MyPackage
    displayName: My Package         # informational
    deploy: false                   # true: deploy all configured artifacts of this package
    allowDowngrade: false           # optional: see "Older designtime versions" below
    artifacts:
      - artifactId: MyIFlow
        type: Integration           # Integration, MessageMapping, ScriptCollection, ValueMapping
        version: active             # default active
        deploy: true                # deploy this artifact after configuration
        allowDowngrade: true        # optional, wins over the package's setting
        versioning: keep            # optional exception to --versioning (docs/versioning.md)
        parameters:
          - key: ReceiverHost
            value: api.example.com
          - key: Timeout
            value: "60"
        batch:                      # optional
          enabled: true             # default true
          batchSize: 90             # parameters per $batch request
```

How it runs:

1. **Configure**: for each artifact the parameters are written, by default with OData
   `$batch` requests (`--batch-size`, default 90). If a batch fails, the parameters are written
   one by one. Keys that do not exist on the artifact are skipped and counted as failed.
2. **Deploy**: artifacts with `deploy: true` (or in a package with `deploy: true`) that were
   configured successfully are deployed: always after a parameter change, otherwise only when
   the runtime version differs from the designtime version. Script collections and mappings go
   first, then the flows; up to `--parallel-deployments` (default 5) are triggered at a time and
   all are watched together, from one runtime list every `--deploy-delay` (default 10) seconds,
   for at most `--deploy-timeout` (default 10m) in total ([deployments.md](deployments.md)).

| Flag | Description |
|------|-------------|
| `--config-path`, `-c` | File or folder (required) |
| `--deployment-prefix`, `-p` | Overrides `deploymentPrefix`; the final IDs are `prefix + ID` for packages and artifacts |
| `--package-filter`, `--artifact-filter` | Comma-separated IDs (without prefix) to include |
| `--dry-run`, `--plan` | Show what would be changed (compares with the tenant) and which artifacts would be deployed and why (`plan` in the JSON result); nothing is written |
| `--offline` | With `--dry-run`: only the file, without tenant calls |
| `--parallel` | Parameter reads at the same time (default 8); writes stay one after another. `configure pull` has the same flag |
| `--defer-deploy` | Do not deploy: add the deployments to `.cpi/pending-deploy.json` (`--pending-file`) for one `cpictl deploy --pending` ([ci.md](ci.md#pipeline-snapshot-update-configure-deploy-once)) |
| `--force` | Write every parameter and deploy every marked artifact, even without changes |
| `--versioning` | `manifest`, `keep` or `tenant-bump` for artifacts and packages without `versioning` in the file; env `CPICTL_VERSIONING` ([versioning.md](versioning.md)) |
| `--allow-downgrade` | Allow older designtime versions for artifacts and packages without `allowDowngrade` in the file (config `configure.allowDowngrade`, else `deploy.allowDowngrade`) |
| `--disable-batch` | Always write parameters one by one |

All flags can be set in the global config file under `configure:` (`configPath`,
`deploymentPrefix`, `packageFilter`, `artifactFilter`, `dryRun`, `deployTimeout`,
`deployDelaySeconds`, `parallelDeployments`, `batchSize`, `disableBatch`; `deployRetries` is
deprecated, see [deployments.md](deployments.md#migrating-pipeline-configuration)).

With `--output json` the result contains the statistics, the `diff` (per key: `update` with local
and tenant value, `unchanged`, `unknown_key`) and one deployment result per deployed artifact. Failures give exit code 7 when anything succeeded, otherwise 5.

> **Prefix rule differs from the orchestrator.** `configure` builds `prefix + ID` for packages
> and artifacts (prefix `DEV_` → `DEV_MyPackage`, `DEV_MyIFlow`). The orchestrator builds
> `prefix + ID` for packages but `prefix + "_" + ID` for artifacts (prefix `DEV` →
> `DEVMyPackage`, `DEV_MyIFlow`). Choose the prefix per command so the IDs match.

### Older designtime versions

A deployment whose designtime version is lower than the running one (designtime `1.0.13`,
runtime `1.0.15`) usually means the tenant copy was never updated, so it is refused by default.
Sometimes the designtime is the newer content anyway, for example when the runtime came from a
manual deployment of an older build with a bumped version. Two rules decide, and the deciding
rule is logged on the artifact's result line (`[rule: ...]`, JSON field `rule`):

| Rule | When | Outcome |
|------|------|---------|
| `modified after deployment` | the designtime artifact was changed after the running version was deployed (designtime `ModifiedAt` later than runtime `DeployedOn`) | deployed: the designtime holds the newer content |
| `version` | otherwise, or when the tenant does not report one of the two times | `FAILED` before anything is triggered; the error names both times |
| `allowDowngrade` | `allowDowngrade: true` applies (below) | deployed |

`allowDowngrade` is resolved per artifact: the artifact's `allowDowngrade`, else its package's,
else `--allow-downgrade` / `configure.allowDowngrade` / `deploy.allowDowngrade`.
`allowDowngrade: false` on an artifact keeps the guard even when the package or the flag allows
downgrades.

When `configure` writes parameters, it reads the designtime modification time *before* writing
them: a parameter change may count as a modification on the tenant, and must not make older
content look new.

## configure pull

```bash
cpictl configure pull --output-dir ./config/dev                        # all packages
cpictl configure pull --output-dir ./config/dev --package-ids PkgA,PkgB
```

Writes one `<PackageID>.yml` per package in the `configure` format above, with the current
parameter values of all integration flows. Packages that do not exist are skipped with a
warning. Config keys: `configure.pull.outputDir`, `configure.pull.packageIds`.
