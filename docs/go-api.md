# Go API

cpictl can be used as a Go library: `pkg/ops` holds the operations the CLI and the MCP server
run, with structured results; the packages below hold the models and helpers they take. All
functions that talk to a tenant take a `*httpclnt.HTTPExecuter` (`cpi.InitHTTPExecuter`).
Nothing in `internal/` is part of the API.

Each section is self-contained.

## Deploy configs and orchestrator plans (`pkg/ops`, `pkg/models`, `pkg/lint`)

The deploy config of `cpictl orchestrator` (docs/orchestrator.md) is `models.DeployConfig`
(`ops.DeployConfig` is the same type); `models.Package` and `models.Artifact` are its entries
(`artifactId` is `Artifact.Id`). Unmarshalling with `gopkg.in/yaml.v3` applies the defaults
(`sync` and `deploy` true).

```go
cfg, err := ops.LoadDeployConfig("deploy-config.yml") // file, folder or http(s) URL
plan, err := ops.PlanOrchestration(ctx, exe, "packages", cfg, ops.OrchestratorOptions{
	Versioning:     versioning.Manifest,
	ArtifactFilter: []string{"OrderIntake"},
	SnapshotState:  ".cpi/snapshot-state.json", // optional: compare without downloads
	Host:           host,                       // ignore a state of another tenant
})
for _, it := range plan { // ops.PlanItem, as in orchestrator --plan --output json
	fmt.Println(it.Artifact, it.Upload, it.Deploy, it.Reason, it.Error)
}
copies := lint.DeploymentCopiesOf(cfg) // for lint.Options.DeploymentCopies
```

- `LoadDeployConfig(path)`: a folder's files are merged as with `--merge-configs` (each
  file's `deploymentPrefix` goes into its package and artifact IDs; `Package.MergedPrefix`
  records it).
- `PlanOrchestration(ctx, exe, dir, cfg, o)` is `orchestrator --plan`: read only, one
  `PlanItem` per artifact (`Upload` create / update / unchanged / fails / skipped-draft;
  `Deploy` and `Reason`; `Error` when it would fail). `dir` is the packages directory.
  `OrchestratorOptions` mirror the flags: `Mode` (`OrchestratorUpdateAndDeploy`,
  `OrchestratorUpdateOnly`, `OrchestratorDeployOnly`), `DeploymentPrefix`, `PackageFilter`,
  `ArtifactFilter`, `Versioning`, `SnapshotState`/`Host`, `VerifyDownload`, `DraftHandling`
  (`DraftHandlingSkip`, `DraftHandlingError`), `Parallel`, `WorkDir`.
- A full run: `orc, _ := ops.NewOrchestrator(exe, dir, o)`; `tasks, _ := orc.Process(ctx, cfg)`
  uploads (or, with `o.Plan`, plans) and returns the `DeploymentTask`s; deploy them with
  `ops.Deploy` and count the results with `orc.RecordDeployments`; `orc.Stats` holds the
  counters and `orc.Stats.Artifacts()` the status per artifact ID; `orc.Close()` removes the
  temporary work directory. `ops.PlanDeployments(exe, tasks)` predicts deployments only.
- `lint.DeploymentCopiesOf(cfg)` maps each artifact folder to the IDs it is deployed as (configs
  with a `deploymentPrefix` are left out). It lives in `pkg/lint` because `pkg/lint` imports
  `pkg/ops`.

## Configure models (`pkg/models`)

`models.ConfigureConfig`, `ConfigurePackage`, `ConfigureArtifact` and `ConfigurationParameter`
are the configure file (docs/configure.md), so `ops.DiffArtifactConfig`, `ops.ConfigDiff` and
`ops.TransportCheckOptions.Configure` can be fed from memory:

```go
cfg := &models.ConfigureConfig{Packages: []models.ConfigurePackage{{
	ID: "Orders",
	Artifacts: []models.ConfigureArtifact{{ID: "OrderIntake", Type: "Integration", Version: "active",
		Parameters: []models.ConfigurationParameter{{Key: "Host", Value: "erp.example.com"}}}},
}}}
```

Unmarshalling with `gopkg.in/yaml.v3` applies the file defaults (`version: active`);
`models.EffectiveAllowDowngrade` resolves `allowDowngrade`.

## Versioning (`pkg/versioning`)

The versioning modes of docs/versioning.md: `Mode` with `Unset`, `Manifest`, `Keep`,
`TenantBump` (`Modes` lists the valid ones), and the rule names of results (`RuleManifest`,
`RuleBump`, `RuleKeep`, `RuleTenant`, `RuleGuard`).

- `Parse(s)` validates user input ("" is `Unset`).
- `Resolve(artifact, pkg, global)`: the artifact's mode, else the package's, else the global one.
- `Bump(version, level)` (patch, minor, major), `Max(versions...)`, `Compare(a, b)` (segment by
  segment, "1.0.10" > "1.0.9").

## Manifest (`pkg/manifest`)

`META-INF/MANIFEST.MF` of an artifact:

- `Version(dir)` / `SetVersion(dir, v)` read and write `Bundle-Version` of an artifact
  directory (other lines are kept).
- `Parse(data)` returns the headers (continuation lines joined); `SetHeaders(mf, headers)`
  replaces or appends headers, wrapping at 72 bytes; `Path(dir)`, `WrapLine(s)`.

## Partner Directory files (`pkg/pdrepo`, `pkg/ops`)

`pdrepo.New(resourcesPath)` is the local Partner Directory (`<PID>/String.properties`,
`<PID>/Binary/`, docs/partner-directory.md), as `pd-snapshot`, `pd-deploy` and `pd-diff` use it.

```go
api, local := cpi.NewPartnerDirectory(exe), pdrepo.New("partner-directory")
snap, err := ops.PDSnapshot(api, local, true, nil)      // pd-snapshot
diff, err := ops.PDDiff(api, local, []string{"ONE_OMS"}) // pd-diff
res, err := ops.PDDeploy(api, local, ops.PDDeployOptions{DryRun: true})
```

`PDSnapshot(api, repo, replace, pids)` downloads string and binary parameters; `replace` false
adds only missing parameters; `pids` limits it (empty: all). `PDSnapshotResult` has the numbers
and the PIDs written.

## Log level auto-revert (`pkg/ops`)

TRACE exposes payloads, so a raised log level should be time-boxed:

```go
rev := ops.NewLogLevelReverter(exe)
ops.SetLogLevel(exe, ops.LogLevelRequest{ArtifactID: "OrderIntake", Level: "TRACE"})
rev.Schedule(ops.LogLevelRequest{ArtifactID: "OrderIntake"}, time.Now().Add(10*time.Minute))
rev.RunDue()    // e.g. before each request: reverts flows past their deadline to INFO
rev.RevertAll() // on shutdown
```

`Schedule` with a zero time cancels; `Pending()` lists the deadlines. The MCP server uses the
same reverter for `set_log_level`.

## Package artifacts (`pkg/ops`)

`ops.ListArtifacts(exe, packageID)` returns the designtime artifacts of a package with
`ModifiedBy` and `ModifiedAt` (nil when the tenant does not report them).

## Snapshot (`pkg/snapshot`)

`snapshot.Take` is what `cpictl snapshot` runs ([snapshot.md](snapshot.md) has the rules): every
editable package into `<dir>/<package>/<artifact>`, local edits protected, a state file recording
what it saw.

```go
exe := cpi.InitHTTPExecuter(details) // retries throttled reads
res, err := snapshot.Take(ctx, exe, "/repo/packages", snapshot.Options{
	Packages:    []string{"Billing", "Orders"}, // or Exclude
	Incremental: true,
	Tenant:      cpi.TenantID(details.Host), // a state of another tenant is not used
	Progress: func(it snapshot.Item) { // one call per artifact, one at a time
		fmt.Println(it.Package, it.Artifact, it.Label(), it.Action)
	},
})
// err: usage errors, "No packages found", or the failed packages (res is
// still filled; exit code 7 when some succeeded)
for _, it := range res.Items { /* status: new, changed, unchanged, deleted, local-modified, local-only, derived, draft */ }
```

| Option | Meaning |
|--------|---------|
| `Packages`, `Exclude` | package IDs to include / exclude (one of both) |
| `DraftHandling` | `SKIP` (default; status `draft`, kept in the state with `Draft`), `ADD`, `ERROR` |
| `Incremental`, `DryRun`, `OverwriteLocal`, `Prune`, `KeepOrphanParameters` | as the CLI flags |
| `SkipPackageDetails` | do not write `<package>/<package>.json` |
| `Parallel` | downloads at the same time across packages (default 8) |
| `StatePath` / `State` | state file read before and written after the run (default `<dir>/.cpi/snapshot-state.json`); `State` alone: in memory, nothing read or written |
| `Tenant` | `cpi.TenantID(host)`: a state of another tenant is replaced |
| `WorkDir`, `KeepWorkDir` | where the run's temporary folder is created; removed after the run unless kept |
| `Derived` | deployment copies that are not written (`DerivedIndex`, as built from a deploy config) |
| `Progress` | called once per artifact as soon as its outcome is known |

`Result` has the counts (`Packages`, `Succeeded`, `Downloaded`, `Skipped`, `Counts`, `Warnings`,
`Failed`), the `Items` (sorted), the `State` after the run and `Paths`: what the run wrote or
removed, relative to `dir`. Cancelling `ctx` stops starting further packages and artifacts
(requests already sent finish).

**State.** `snapshot.LoadState(path)`, `NewState()`, `(*State).Save(path)`,
`(*State).Artifact(id)`. Per artifact (`Artifacts["<package>/<artifact>"]`) an `ArtifactState`:
`Type`, `Version`, `ModifiedAt`, `ConfigHash` (configured parameters), `ContentHash`,
`UploadHash` (the tenant's content as `content.UploadHash` computes it), `FilesHash`
(`content.TreeHash` of the folder as written), `Derived`, `Draft`. Comparing the states of two
runs shows what changed on the tenant between them.

**Commit.** `snapshot.Commit(dir, snapshot.CommitOptions{...})` commits the changes below `dir`
(never pushes). `dir` may be a folder inside the working tree: the repository is found above it,
and only changes below it are staged; staged changes elsewhere in the index make it refuse.

```go
c, err := snapshot.Commit("/repo/packages", snapshot.CommitOptions{
	Message:    "Import from DEV",
	AuthorName: "Studio", AuthorEmail: "studio@example.com", // default: the GitHub Actions bot
	Paths:      res.Paths, // only what the snapshot changed; nil: everything below dir
})
// c.Hash == "" when there was nothing to commit
```

`snapshot.WritePackageDetails(dir, id, details)` writes `<dir>/<id>.json` when the details
differ.

## Upload with a baseline (`pkg/ops`)

`ops.UploadArtifact` creates or updates a designtime artifact from a folder. With the snapshot
state of the target tenant as `Baseline`, an existing artifact is compared with the hash the
snapshot recorded instead of downloading it, as long as the tenant's version and `ModifiedAt` are
still the snapshot's (`Compared: "snapshot"`; otherwise `"download"` with `CompareReason`).
`KeepRuntime` never undeploys: a content change with the running version sets `Redeploy` (deploy
it afterwards with force) instead of undeploying the runtime artifact.

```go
state, _ := snapshot.LoadState("/repo/packages/.cpi/snapshot-state.json")
up, err := ops.UploadArtifact(exe, ops.UploadRequest{
	ID: "Orders_In", Type: "Integration", PackageID: "Orders", Dir: "/repo/packages/Orders/Orders_In",
	Baseline: state, KeepRuntime: true,
})
if up.Redeploy { /* ops.Deploy with force */ }
```

## Content (`pkg/content`)

The comparisons cpictl uses to decide "changed", so that other tools agree with it. All work on
local files only.

| Function | |
|----------|-|
| `UploadHash(fsys, artifactType)` | what an upload compares (no `Bundle-Version`); equal: an upload reports `UNCHANGED` |
| `ContentHash(fsys)` | the significant content, `Bundle-Version` included |
| `TreeHash(dir)` | every file, exact bytes (equal: `git status` is clean) |
| `NormalizeTree(dir)`, `NormalizeProperties(data)` | the stable form a snapshot writes: LF, no timestamp comments, sorted keys |
| `TreeDiff(a, b, skip)`, `DiffDirectories(a, b)` | which files differ |
| `UnifiedDiff(a, b, nameA, nameB, context)` | a unified diff of two texts (`ok` false when too large) |
| `PropertiesEqual`, `RestoreProperties`, `PropdefNames`, `DropOrphanParameters` | parameters.prop helpers |

```go
h, _ := content.UploadHash(os.DirFS("/repo/packages/Orders/Orders_In"), "Integration")
changed := h != state.Artifacts["Orders/Orders_In"].UploadHash
```

The hashes are stable across cpictl versions (a test pins them): states written by one version
are read by the next.
