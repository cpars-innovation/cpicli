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
