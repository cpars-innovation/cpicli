# Coming from FlashPipe

cpictl grew out of [FlashPipe](https://github.com/engswee/flashpipe) 3.7. The commands and
most flags are the same, so existing pipelines move over with a few renames. This page lists
everything that changed.

## Migration checklist

1. **Binary:** `flashpipe` → `cpictl` (build it, see [README](../README.md#install)).
   The `engswee/flashpipe` Docker image and GitHub Action are not used any more.
2. **Environment variables:** `FLASHPIPE_*` → `CPICTL_*`, same suffix:

   | FlashPipe | cpictl |
   |-----------|--------|
   | `FLASHPIPE_TMN_HOST` | `CPICTL_TMN_HOST` |
   | `FLASHPIPE_OAUTH_HOST` | `CPICTL_OAUTH_HOST` |
   | `FLASHPIPE_OAUTH_CLIENTID` | `CPICTL_OAUTH_CLIENTID` |
   | `FLASHPIPE_OAUTH_CLIENTSECRET` | `CPICTL_OAUTH_CLIENTSECRET` |
   | `FLASHPIPE_OAUTH_PATH` | `CPICTL_OAUTH_PATH` |
   | `FLASHPIPE_TMN_USERID` / `_PASSWORD` | `CPICTL_TMN_USERID` / `_PASSWORD` |
   | `FLASHPIPE_<ANY_FLAG>` | `CPICTL_<ANY_FLAG>` |

   `FLASHPIPE_*` variables are ignored; cpictl prints a warning naming the ones it found.
3. **Config file:** `$HOME/flashpipe.yaml` → `$HOME/cpictl.yaml`. The content (keys and
   command sections) is unchanged. A left-over `flashpipe.yaml` triggers a warning.
   Files passed with `--config` work as before.
4. **`config-generate --output <file>`** → `config-generate --output-file <file>`.
   `--output` is now the global output format (`text`/`json`).
5. **Scripts that test for exit code `1`:** cpictl uses specific exit codes (below).
   Checks for "non-zero" keep working.
6. Re-run `config-generate` once and review the diff of your deployment config (see
   [config-generate](#config-generate)).

Rename variables in a repository, for example:

```bash
grep -rl 'FLASHPIPE_' .github/ azure-pipelines*.yml | xargs sed -i 's/FLASHPIPE_/CPICTL_/g'
grep -rl 'flashpipe ' .github/ azure-pipelines*.yml | xargs sed -i 's/flashpipe /cpictl /g'
```

## New

- `--output json` on every command: one result document on stdout, JSON-line logs on stderr.
- Exit codes: 0 ok, 1 internal error, 2 usage/config, 3 auth, 4 tenant HTTP error,
  5 deploy/validate failed, 6 timeout, 7 partial failure (FlashPipe: 1 for every error).
- Commands: `undeploy`, `status`, `logs` (+ `get`, `steps`, `attachment`, `payload`),
  `validate`, `guidelines`, `endpoints`, `resources`, `download`, `packages`, `artifacts`,
  `params get`, `params set`, `credentials`, `keystore`, `mcp`.
- MCP server for AI agents ([mcp.md](mcp.md)).
- `--tmn-host` accepts `https://host` and `host:port`.

## Changed behaviour

### deploy

- **Waits for the right thing.** FlashPipe polled only the runtime status, so redeploying the
  same version could be reported as successful while the old artifact was still running
  (always the case after a parameter change). cpictl polls the BuildAndDeployStatus task and
  only accepts a runtime artifact that is newer than the one before the deployment.
- **One failure does not stop the others.** FlashPipe aborted at the first failing artifact;
  cpictl processes all artifacts and reports a result for each (exit code 7 if some succeeded).
- The first status check happens after `--delay-length` instead of immediately.
- **No silent downgrades.** If the designtime version is older than the running version
  (e.g. `1.0.9` vs `1.0.10`) and the designtime artifact was not changed after that deployment,
  the artifact is `FAILED` without being deployed: the tenant copy was probably never updated.
  A designtime artifact changed after the running deployment is deployed (rule
  `modified after deployment`). `--allow-downgrade` (config `deploy.allowDowngrade`) deploys
  anyway; in `configure` files also `allowDowngrade: true` per artifact or package
  ([configure.md](configure.md#older-designtime-versions)). This applies to `deploy`, `configure`
  and `orchestrator`. With `--versioning manifest` the repository's `Bundle-Version` is the version
  on every tenant and only the version numbers decide ([versioning.md](versioning.md)).
- **Export keeps your versions.** `sync --target git` no longer overwrites `Bundle-Version` with
  the download's value (usually `1.0.0`).
- `deploy.artifactIds` in the config file now works (FlashPipe rejected it because
  `--artifact-ids` was a required flag).

### configure and orchestrator

- Both use the same deployment logic as `deploy`, including the fixes above.
- Deployments run package by package in config order, with up to `--parallel-deployments`
  concurrent deployments per package. `configure` used to start all packages at once in
  random order.
- `configure` writes only parameters that differ from the tenant and redeploys only artifacts
  with a change; unknown keys fail the artifact before anything is written. `--force` restores
  the old behaviour, `--dry-run` now compares with the tenant (`--offline` for the file only).
- `orchestrator` has no own `--debug` flag any more; the global `--debug` does the same.
- Remote deployment configs (`--deploy-config https://...`) must be public; the
  authentication options in FlashPipe's documentation could never be set.

### config-generate

- Works without tenant credentials (it never contacted the tenant).
- Reading an existing config used different defaults than the orchestrator: omitted
  `sync`/`deploy` were written back as `false`, the `orchestrator:` section was dropped and
  package metadata from `<Package>.json` was never read. All fixed, so the first run with
  cpictl can change your file: `sync: true`/`deploy: true` where they were omitted,
  `packageDir`, `displayName`, `description` and `short_text` filled from the package JSON.

### Partner Directory

- `pd deploy` exits non-zero when any parameter fails (FlashPipe exited 0 with warnings).
- `pd deploy --full-sync` skips a partner ID whose local files cannot be read and reports an
  error. FlashPipe treated such a partner as empty and **deleted all of its remote
  parameters**. Unreadable binary files are errors instead of being skipped (which also
  caused remote deletions).

### CSRF handling

FlashPipe fetched a CSRF token before every modifying request and did not send one at all for
Partner Directory writes and configure's `$batch` request, so with Basic Auth these failed
(configure silently fell back to single requests). cpictl fetches the token once, reuses it,
and refreshes it automatically when the tenant reports it as expired.

### Logging and privacy

- Usage analytics (Matomo) are removed.
- Host, client ID and user ID are no longer logged; request bodies and CSRF tokens are never
  logged, not even with `--debug`.
- Logs never go to stdout (the orchestrator config loader used to print there).

## Unchanged

- Command names, flags (except `config-generate --output`) and config file keys.
- Deployment config (`001-deploy-config.yml`) and `configure` YAML formats.
- Directory layouts written by `snapshot`, `sync` and `pd snapshot`.
- Deployment prefix rules of `configure` and `orchestrator`.
