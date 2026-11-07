# Contributing

## Before you push

```bash
gofmt -l .                        # must print nothing
go vet ./... && go vet -tags integration ./...
go test -race ./...               # offline, < 10 s, no tenant needed
```

CI runs the same checks (`.github/workflows/ci.yml`).

## Testing

- `internal/cpitest` is an in-memory tenant (httptest). It enforces CSRF like a real tenant
  (token + session cookie for modifying Basic Auth requests; `ExpireCSRF()` simulates an
  ended session), never returns secrets, and covers deploy/undeploy, designtime
  create/update/download, configuration, logs, content, Partner Directory and security
  endpoints. New tenant calls get a mock endpoint and a test in `pkg/ops`.
- Exit codes are pinned by `internal/cmd/testdata/TestExitCodes.golden`; the command
  reference `docs/commands.md` and `docs/examples` are checked by tests as well.
- Tests that handle secrets assert that the secret never appears in results or logs.
- `make cover` reports cross-package coverage; CI fails below 50 %.

## Rules

- **Never test against a real tenant by default.** Tests use the in-memory mock tenant in
  `internal/cpitest`. Tests that need a tenant go behind `//go:build integration` and are run
  explicitly with `make test-integration` against a development tenant.
- New tenant operations go into `pkg/ops` (structured result + classified error) and are
  then exposed by a CLI command in `internal/cmd` and, where useful, an MCP tool in
  `internal/mcp`. Do not put tenant logic into cobra commands.
- Errors: wrap usage/config problems with `output.Usagef`, partial results with
  `output.Partial`; HTTP errors from `pkg/httpclnt` are classified automatically. The exit
  codes are a contract (`internal/exitcode`) and must not be renumbered.
- Logs go to stderr only; stdout is reserved for results (and the MCP protocol).
- Never log credentials, tokens, client IDs or request bodies. Secrets are read only through
  `ops.SecretSource` (env, file, stdin), never from flag values, and never returned.
- No new runtime dependencies without agreement.

## Generated files

```bash
go test ./internal/cmd -update    # regenerates docs/commands.md and the testdata/*.golden files
```

Review the diff of regenerated files before committing.

## Upstream

cpictl contains code from [FlashPipe](https://github.com/engswee/flashpipe) (remote
`upstream`). Fixes from there are taken over by cherry-pick only; never rebase onto it.
Keep the attribution in NOTICE and in the copyright headers of files that came from it.

After a dependency change, regenerate the third-party notices that every release archive carries:
`scripts/third-party-notices.sh` (licenses/THIRD_PARTY_NOTICES.md and licenses/third-party/).
