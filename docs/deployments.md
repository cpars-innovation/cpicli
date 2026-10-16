# Deployments: how cpictl waits, and watching the runtime

## How deploy, configure and orchestrator wait

A deployment on Cloud Integration is asynchronous: the deploy request returns a task, and the
flow shows up on the runtime as STARTING, then STARTED or ERROR. cpictl watches every deployment
it triggers until it has an outcome.

- **One request per interval for all deployments.** cpictl reads the whole runtime list
  (`IntegrationRuntimeArtifacts`, one request) every `delay` seconds and answers the status of
  every artifact it waits for from it. Deploying 200 flows costs one request per interval, not
  200. A deploy task's own status (`BuildAndDeployStatus`) is asked only while its flow is not
  on the runtime yet.
- **Trigger all, then watch all.** Up to `parallelDeployments` artifacts are checked and
  triggered at a time; once triggered they wait without holding a slot, so all deployments of a
  run are in progress together. Nothing waits for the slowest artifact of a batch before the
  next batch starts.
- **Dependencies first.** configure, orchestrator and `deploy --pending` deploy in two phases:
  script collections, message mappings and value mappings first (flows reference them), then
  all integration flows, across all packages. Before, they went package by package.
- **One timeout for the whole run** (`deploy timeout`, default **10 minutes**): every
  deployment that has no outcome by then ends as TIMEOUT (exit code 6). A deployment that fails
  ends at the next check, as before. A flow on an integration cell is found through the Web UI
  runtime list, as before.
- **Logs:** one line per artifact when its state changes (`STARTING`, `STARTED`, ...; the
  checks in between at debug level), one result line per artifact, then a summary:
  `Deployment of 42 artifact(s) finished in 2m13s: 40 deployed, 1 skipped, 1 failed`.

### Settings

| Command | Timeout | Interval | Triggered at a time |
|---------|---------|----------|---------------------|
| `deploy` | `--deploy-timeout` / `deploy.timeout` | `--delay-length` / `deploy.delayLength` (10) | all (with `--pending`: `parallelDeployments`, 5) |
| `configure` | `--deploy-timeout` / `configure.deployTimeout` | `--deploy-delay` / `configure.deployDelaySeconds` (10) | `--parallel-deployments` / `configure.parallelDeployments` (5) |
| `orchestrator` | `--deploy-timeout` / `orchestrator.deployTimeout` | `--deploy-delay` / `orchestrator.deployDelaySeconds` (10) | `--parallel-deployments` / `orchestrator.parallelDeployments` (5) |

- The timeout is a duration (`15m`, `90s`, `1h`) or plain seconds (`900`). Environment:
  `CPICTL_DEPLOY_TIMEOUT` for all three commands.
- **The old check counts still work** (`deploy.maxCheckLimit`, `--max-check-limit`,
  `configure.deployRetries`, `orchestrator.deployRetries`, `--deploy-retries`): when one of them
  is set and no timeout is, the timeout is checks × interval, the same total time as before
  (30 × 10 s = 5 minutes). They are deprecated; set a timeout instead.
- With so many deployments in progress at once, a shorter interval is cheap: `--deploy-delay 5`
  costs one request every 5 seconds whatever the number of flows.
- `undeploy`, `delete` and the MCP server keep their check counts per artifact
  (`--max-check-limit`, `--max-checks`); they read the status from the shared list as well.

### Migrating pipeline configuration

| Before | After |
|--------|-------|
| `deploy: { maxCheckLimit: 60 }` (10 minutes per artifact) | `deploy: { timeout: 10m }` |
| `orchestrator: { deployRetries: 90, deployDelaySeconds: 10 }` | `orchestrator: { deployTimeout: 15m, deployDelaySeconds: 10 }` |
| `configure: { deployRetries: 30 }` | `configure: { deployTimeout: 5m }` or nothing (default 10m) |
| `--max-check-limit 60` / `--deploy-retries 60` | `--deploy-timeout 10m` |
| `CPICTL_MAX_CHECK_LIMIT=60` | `CPICTL_DEPLOY_TIMEOUT=10m` |

How to choose the timeout: the time the **whole run** may take to bring every flow up, not one
flow. The per-artifact limits of before were per batch; with all deployments in progress
together, the longest single deployment plus a margin is enough. Watch the summary line of a
few runs (`finished in 2m13s`) and set about twice that.

## Watching the runtime: `cpictl runtimes watch`

Reports every change on the tenant's runtime as an event, from any source (cpictl, the Web UI,
other tools), with one request per interval:

```bash
cpictl runtimes watch                               # JSON lines on stdout
cpictl runtimes watch --interval 5s --artifacts 'SD_*' --initial
cpictl runtimes watch --webhook https://studio.example.com/hooks/cpi \
  --webhook-header "Authorization: Bearer $TOKEN"   # CPICTL_WEBHOOK_SECRET signs the requests
```

```json
{"type":"deployed","time":"2026-10-16T23:17:16Z","id":"Orders_In","version":"1.0.1","previousVersion":"1.0.0","status":"STARTING","previousStatus":"STARTED","deployedOn":"2026-10-16T23:17:14.712Z","deployedBy":"sb-...|it!b641068"}
{"type":"started","time":"2026-10-16T23:17:21Z","id":"Orders_In","version":"1.0.1","status":"STARTED","previousStatus":"STARTING"}
```

| Event | When |
|-------|------|
| `deployed` | new on the runtime, deployed again (new `deployedOn`, also with the same version), or a new version |
| `started` | status became STARTED |
| `error` | status became ERROR; `error` holds the tenant's error text |
| `status` | any other status change (STARTING, STOPPING, ...) |
| `undeployed` | gone from the runtime |
| `present` | with `--initial`: every artifact found at the start |

- `--cells` also reads integration cells through the Web UI runtime list (`runtime` names the
  cell); that costs two more requests per interval and runtime.
- **Webhook:** each event is POSTed as JSON (`Content-Type: application/json`,
  `X-Cpictl-Event: <type>`), 3 attempts. With `--webhook-secret` or `CPICTL_WEBHOOK_SECRET`,
  `X-Cpictl-Signature: sha256=<hex HMAC-SHA256 of the body>`; check it on the receiving side.
  https only (http for localhost).
- A failed read is logged and retried at the next interval; an authentication error ends the
  watch (exit 3). `--duration` stops it after a time.
- Under an access scope ([access-scope.md](access-scope.md)) only artifacts in the scope appear.

**Go:** `ops.WatchRuntime(ctx, exe, ops.WatchOptions{...}, emit)` runs the same watcher in your
process (CPI Studio: one per tenant in the backend, pushing events to the browser);
`ops.DiffRuntime(prev, cur, at)` is the comparison on its own.

Why not a flow that reports its own deployment: a timer step that runs once after deployment
reports only flows that started; a deployment that fails never starts the flow. It would also
change every flow on the tenant.
