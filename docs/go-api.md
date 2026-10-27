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
`<PID>/Binary/`, docs/partner-directory.md), as `pd snapshot`, `pd deploy` and `pd diff` use it.

```go
api, local := cpi.NewPartnerDirectory(exe), pdrepo.New("partner-directory")
snap, err := ops.PDSnapshot(api, local, true, nil)      // pd snapshot
diff, err := ops.PDDiff(api, local, []string{"ONE_OMS"}) // pd diff
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

## Diagram geometry (`pkg/iflow`)

`iflow.ReadDiagram(data)` reads the diagram (BPMN DI) of an .iflw for a flow viewer, with the
same reader as `iflow.Layout` and `iflow.CheckLayout`:

```go
d, err := iflow.ReadDiagram(data) // error for invalid XML or a non-BPMN document
r := d.Shapes["CallActivity_1"]   // iflow.Rect{X, Y, Width, Height} by bpmnElement
pts := d.Edges["SequenceFlow_1"]  // []iflow.Point, sequence and message flows
for _, n := range d.Nodes {        // flow nodes in document order (subprocess first)
	fmt.Println(n.ID, n.Type, n.Name, n.Kind, n.Container, n.Drawn)
}
```

- `Pools` are all participants: integration processes (`ProcessRef`, `Type`
  `IntegrationProcess`) and senders/receivers (`EndpointSender`, `EndpointRecevier`), with
  `Bounds`. `Lanes` (if any) have their process, `FlowNodes` and `Bounds`.
- `Nodes` have the BPMN `Type` (`callActivity`, `startEvent`, `exclusiveGateway`, ...),
  `Kind` (`Element.Kind`: the component of `cmdVariantUri`, else `activityType`),
  `ActivityType`, `CmdVariantURI` and `EventSubProcess`.
- `SequenceFlows` and `MessageFlows` have source and target; message flows have the `Adapter`
  (`ComponentType`: HTTP, SOAP, ProcessDirect, ...) and `Direction`.
- Elements without a shape or line have `Drawn: false` and are listed in `Undrawn`; a model
  without a diagram has everything undrawn.
- `d.Model` is the `iflow.Model` (`Parse`) the diagram was read with (not in the JSON).

All types have camelCase JSON tags; `cpictl iflow diagram <file|artifact dir> --output json`
prints the same structure.

## Model diff (`pkg/iflow`)

`iflow.DiffModels(a, b)` compares two .iflw models per element and per property instead of per
line (`cpictl compare --model` and the MCP `compare` tool with `model: true` use it):

```go
d, err := iflow.DiffModels(a, b) // error for invalid XML or a non-BPMN document
for _, e := range d.Elements {   // steps, events, gateways, processes, participants, adapters
	fmt.Println(e.Change, e.Type, e.ID, e.Kind, e.Name, e.Renamed, e.Moved)
	for _, p := range e.Properties { // ifl:property values and @attributes
		fmt.Println(p.Key, p.Change, p.A, p.B)
	}
}
_ = d.Flows         // []iflow.FlowChange: sequence flows (@sourceRef, @targetRef, @condition)
_ = d.Collaboration // []iflow.PropertyChange: the integration flow's own properties
for _, line := range d.Summary() {
	fmt.Println(line) // "~ adapter MessageFlow_Sender (ProcessDirect): address: /a -> /b"
}

// without geometry: moved shapes are no change, the diagram is not read
d, err = iflow.DiffModelsWith(a, b, iflow.DiffOptions{IgnoreGeometry: true})
```

- `Change` is `iflow.ChangeAdded`, `ChangeRemoved` or `ChangeChanged`. Added and removed elements
  list all their values; changed ones only what differs. A name change is `Renamed` (`[A, B]`),
  not a property.
- Attributes are properties with an `@` key: `@container` (the process or subprocess),
  `@sourceRef` / `@targetRef` (message and sequence flows), `@processRef` (pools), `@type` (the
  BPMN tag changed), `@triggeredByEvent`, `@condition` (sequence flows), `@name` (collaboration).
- `Moved` marks a changed shape bounds or line waypoints (BPMN DI); geometry is never a property.
- Values of keys for which `iflow.SensitiveName` is true are `iflow.MaskedValue` (`***`), except keys ending in `Name` or `Alias` (artifact references such as `credentialName`).
  `ops.SensitiveName` is the same check.
- Sorted by ID, then key; `d.Empty()` is true for models that differ only in formatting.
- `ops.CompareOptions.Model` adds it to each changed `.iflw` of `ops.Compare`
  (`FileChange.Model`, `FileChange.ModelError`).
- `iflow.Parse` now also returns `Model.Participants` (with `Element.ProcessRef`) and
  `Model.Collaboration` (the collaboration's properties).

## Test runs (`pkg/testrun`)

`pkg/testrun` is what `cpictl test` and the MCP tools `list_tests` / `run_tests` run
([testing.md](testing.md#test-runs) has the case format and the rules; the case file schema is
[test-case.schema.json](test-case.schema.json)).

```go
cases, err := testrun.Load(".cpi/tests", testrun.Filter{Flows: []string{"Orders_*"}, Tags: []string{"smoke"}})
// strict: invalid YAML and unknown keys are usage errors "file:line: ..."; sorted by flow, order, name
newExe := func(url string) (*httpclnt.HTTPExecuter, string, error) {
	return cpi.NewEndpointExecuter(runtimeDetails, url) // runtime credentials (ESBMessaging.send)
}
res, err := testrun.Run(ctx, exe, newExe, cases, testrun.Options{
	Parallel:    4,                                   // stub_calls cases always run alone
	Progress:    func(c testrun.CaseResult) { ... },  // each case when final, one call at a time
	ApplyConfig: true,                                // config.yaml: apply, redeploy, run, restore
	Tier:        "dev",                               // production guard (ops.GuardProduction)
	// Harness, StubCalls (default ops.StubCalls), Now, PollInterval, DeployInterval, DeployMaxChecks
})
if err != nil { ... }        // production guard or missing executers: nothing ran
for _, c := range res.Cases { // Flow, Case, File, Status (passed|failed|error|skipped), DurationMs,
	_ = c.Failures            // MessageGuid, HTTPStatus, MessageStatus, Error, ErrorCategory
	_ = c.Diagnostics         // MessageGuid, LastError, ErrorInformation, FailedStep, TraceRoot, FirstFailure
}
_ = res.Config                // per flow: keys applied and restored, deploys, errors (never values)
testrun.WriteJUnit(w, res)    // a testsuite per flow, a testcase per case
return res.Err()              // nil, output.Failed (5), output.Partial (7) or the cause's exit code
```

- `Case` is the YAML file (`Request`, `Expect`, `Tags`, `Skip`, `Order`, `Flow`); `Name`, `File`
  and `Config` (the flow's `config.yaml`) are set by `Load`. Cases can also be built in code;
  `body_file` and `idoc_file` are read relative to `File`. `testrun.LoadCase(file)` reads one.
- `Run` returns the outcome of every case in the order given; a case that could not run is
  `StatusError` with `Error` and `ErrorCategory`, never a `Run` error.
- `Result.Err()`: a failed case (or a configuration that could not be restored) is
  `output.Failed`; errors next to passed cases `output.Partial`; only errors the most specific
  cause (auth, tenant HTTP, usage, failed, timeout, error). Skipped cases count as neither.
- `testrun.List(cases)` groups cases per flow (trigger, tags, config keys) without payloads.
- `testrun.XPath(doc, path)` and `testrun.JSONPath(doc, path)` are the lookups of
  `response_xpath` and `response_json`.
- `ops.StubCalls(ctx, exe, since)` reads the receiver stub's calls (`[]ops.StubCall`); pass
  another function as `Options.StubCalls` to test without the stub.

## Designtime changes (`pkg/ops`)

```go
res, err := ops.DesigntimeChangedSince(ctx, exe, ops.ChangedSinceOptions{
    Since:       lastCheck,
    Fingerprint: lastFingerprint, // "" on the first call
})
// res.Changed, res.ChangedArtifacts (newest first, with ModifiedBy), res.Fingerprint, res.Latest
```

Keep `res.Fingerprint` for the next call: it also changes when an artifact or package is deleted. It is
empty when a package could not be listed (`res.Warnings`).

## Security material (`pkg/ops`)

```go
f, err := ops.LoadCredentialsFile("credentials.yml")    // or build an ops.CredentialsFile
res, err := ops.ApplyCredentials(exe, f, dryRun)          // kind, name, action per entry; never a secret
```

`ops.CredentialsFile` holds user credentials, OAuth2 client credentials, secure parameters, key pairs
(`ops.KeyPairSpec`, PKCS#12), SSH keys (`ops.SSHKeySpec`) and PGP secret keys (`ops.PGPSecretKeySpec`). A service
that holds the values itself gives each one as `ops.SecretSource{Stdin: strings.NewReader(value)}`. See
[vault.md](vault.md).

## Backup and restore (`pkg/ops`)

`ops.CreateBackup`, `ops.VerifyBackup`, `ops.DiffBackupIndexes`, and `ops.RestoreBackup(ctx, exe, archive,
ops.RestoreOptions{DryRun: true})` with `ops.WriteRestorePlan` / `ops.RestorePlanMarkdown`. See
[backup.md](backup.md#restore).

