# End-to-end test on a development tenant

`go test ./...` uses mock tenants only. `internal/cmd/e2e_test.go` runs the pipeline against a
real **development** tenant. It is compiled only with the build tag `e2e` and runs only with
`CPICTL_E2E=1`, so a normal test run or CI never reaches a tenant. Run it before a release,
or after changing upload, snapshot or deploy code.

```bash
export CPICTL_E2E=1
export CPICTL_TMN_HOST=... CPICTL_OAUTH_HOST=... CPICTL_OAUTH_CLIENTID=... CPICTL_OAUTH_CLIENTSECRET=...
go test -tags e2e -run TestE2E -v -timeout 30m ./internal/cmd
```

| Variable | Default | |
|----------|---------|-|
| `CPICTL_E2E_PACKAGE` | `CpictlE2E` | package created (or reused) for the test |
| `CPICTL_E2E_FLOW` | `CpictlE2E_Flow` | flow created from `test/testdata/artifacts/create/Integration_Test_IFlow` |
| `CPICTL_E2E_KEEP` | | `1`: leave the flow deployed |

What it checks, in order (each step with `--output json`):

1. `packages create`, `iflow copy` (new version per run), then `orchestrator --plan` predicts the
   upload and the deployment, and `orchestrator --versioning manifest` uploads and deploys.
2. `snapshot` twice: the second run changes no file and reports every artifact `unchanged`.
3. `orchestrator --plan` compares with the snapshot state (`compared: snapshot`, no download):
   `unchanged`, no deployment.
4. A change to the model and `version bump`: the plan says `update` and deploy, the run updates
   and deploys once.
5. `snapshot` after the deployment gives no diff (the deployed content equals the repository).
6. `iflow layout` on the flow; if the diagram changed, it is uploaded, deployed and validated:
   open the flow in the Web UI and check the diagram.

At the end the flow is undeployed; the designtime artifact and the package stay (delete them in
the Web UI when you no longer need them).

## Runtime conformance

The access scope and the mock tenant rest on assumptions about how a tenant's runtime routes URLs and how its API
reports changes. `TestConformance` checks them on a development tenant (cpicli#59):

```bash
CPICTL_CONFORMANCE=1 CPICTL_TMN_HOST=... CPICTL_OAUTH_HOST=... CPICTL_OAUTH_CLIENTID=... CPICTL_OAUTH_CLIENTSECRET=... \
CPICTL_RUNTIME_OAUTH_CLIENTID=... CPICTL_RUNTIME_OAUTH_CLIENTSECRET=... \
  go test -tags e2e -run TestConformance -v -timeout 30m ./internal/cmd
```

It deploys copies of two demo flows into the package `CpictlConformance` and probes them:

- whether a path differing only in case reaches the flow (pinned: the access scope compares paths
  case-sensitively, so the test fails if the runtime does not);
- whether an encoded path segment, a `;x=1` path parameter, and OData version variants (`;V=1`, `;v=01`,
  `;v=1;x=1`) reach it;
- whether a configuration change moves the designtime `ModifiedAt`;
- how long the tenant takes to list a new deployment in `ServiceEndpoints`.

Then it undeploys the flows (`CPICTL_E2E_KEEP=1` keeps them). The answers are written to
`conformance-report.json` (`CPICTL_CONFORMANCE_REPORT`). Copy that file to
`internal/conformance/testdata/tenant.json` and commit it: from then on, every `go test` checks that the mock tenant
answers the same, and a mock that differs fails until it is fixed. Run it before a release that changes the access
scope, and when SAP changes the runtime.

## Manual checks

Some behaviour needs the Web UI and is not automated:

- **Drafts**: open the flow in the Web UI, change something and do not save. Run
  `cpictl orchestrator ... --plan`: the flow shows `upload: skipped-draft  no deploy`, exit 0.
  Run it without `--plan`: one warning, `skipped-draft` in the summary, nothing uploaded or
  deployed. `--draft-handling ERROR` gives exit code 5. Then discard the draft.
- **Draft in snapshot**: with the draft open, `cpictl snapshot --draft-handling ADD ...` writes a
  numeric `Bundle-Version` and reports `changed (draft)`.
- **Layout**: open a flow laid out with `cpictl iflow layout --mode full` in the Web UI (both
  views, and after *Edit* and *Save as version*): the diagram is shown as written.
