# Go API

cpictl is a Go module (`github.com/cpars-innovation/cpicli`); the packages under `pkg/` can be
used in-process. `internal/` is not importable. Tests never need a real tenant: the mock tenant
(`pkg/mocktenant`, or `cpictl mock-tenant`) answers the same API.

| Package | What |
|---------|------|
| `pkg/httpclnt` | HTTP client: OAuth / Basic Auth, CSRF, typed errors |
| `pkg/cpi` | OData clients, one call per tenant request (`cpi.InitHTTPExecuter` builds the executer) |
| `pkg/ops` | Operations with structured results, shared by the CLI and the MCP server |
| `pkg/snapshot` | Tenant -> folder snapshot (`cpictl snapshot`), its state, commit |
| `pkg/content` | Content hashing, normalisation and diffs, as cpictl compares artifacts |
| `pkg/iflow`, `pkg/lint` | iFlow model and layout, quality rules (local files only) |
| `pkg/mocktenant` | The mock tenant for tests and demos |

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
