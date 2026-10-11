# Snapshot

`cpictl snapshot` writes every editable package of a tenant into a folder in the tenant layout:
one folder per package, one exact copy per artifact. The folder can be the repository developers
work in (`packages/`, tracked in Git, deployed with `orchestrator --packages-dir packages`) or a
backup repository. A snapshot's output is a Git diff that people review and commit.

```text
packages/<pkg>/<pkg>.json
packages/<pkg>/<artifact>/META-INF/MANIFEST.MF
packages/<pkg>/<artifact>/metainfo.prop
packages/<pkg>/<artifact>/src/main/resources/...
packages/<pkg>/<artifact>/value_mapping.xml        (value mappings)
packages/.cpi/snapshot-state.json                  (committed)
```

```bash
cpictl snapshot --dir-git-repo packages --git-skip-commit --dry-run          # what would change
cpictl snapshot --dir-git-repo packages --git-skip-commit                    # write it
cpictl snapshot --dir-git-repo packages --git-skip-commit --incremental      # skip unchanged downloads
cpictl snapshot --dir-git-repo packages --ids-include UtilitiesBaseEDMEnergyDataManagement --dry-run
```

`--ids-include` / `--ids-exclude` select packages. The deploy config is read from
`--deploy-config`, else `snapshot.deployConfig` or `orchestrator.deployConfig` of the config file
([deployment copies](#deployment-copies)).

## What happens per artifact

| Status | Meaning | Action |
|--------|---------|--------|
| `new` | on the tenant, not local | written |
| `changed` | tenant version, `ModifiedAt` or content differs from local | written: the folder is replaced, so files deleted on the tenant are deleted locally |
| `unchanged` | local equals the tenant | nothing |
| `local-modified` | the local folder differs from what the last snapshot wrote (developer edits) | **skipped**; `--overwrite-local` overwrites it |
| `deleted` | local, written by an earlier snapshot, no longer on the tenant (also whole packages) | kept; `--prune` removes it |
| `local-only` | local, not on the tenant, never snapshotted (a new artifact not uploaded yet) | always kept |
| `derived` | a deployment copy of another artifact ([below](#deployment-copies)) | not written |
| `draft` | in draft on the tenant, `--draft-handling SKIP` ([below](#drafts)) | not written |

The text output has one line per artifact that is not unchanged, and a count per status;
`--output json` returns `counts`, `warnings` and `artifacts` (package, artifact, status, action,
source, note, warning, `draft`, `orphanParameters`). In GitHub Actions the same table goes to the
job summary. Packages outside `--ids-include` (or in `--ids-exclude`) are counted in one line
(`180 package(s) not in --ids-include`); `--debug` lists them.

### Drafts

`--draft-handling` decides about artifacts in draft on the tenant (being edited in the Web UI):
`SKIP` (default) leaves the local folder alone and reports the artifact as `draft` (JSON `status:
"draft"`, `draft: true`); the state file keeps it with `draft: true` (the entry of the last saved
version the snapshot saw, or a new entry without hashes). `ERROR` fails the package, `ADD` writes
the draft.
A draft has no version number (the tenant reports `Active`), so `ADD` writes a numeric
`Bundle-Version`: the repository's when it is higher, else the last saved version known (the
snapshot state's, or the running one), else `1.0.0`. The artifact is marked as draft in the report
(`new (draft)`, `changed (draft)`, JSON `draft: true`) and in the state file. A
`Bundle-Version: Active` written by an earlier cpictl is replaced the same way.

### Orphan parameters

The tenant keeps configured values for parameters that the flow no longer declares (for example
`STUB_HTTP_URL` after a rename to `STUB_HTTP_BASEURL`). Keys of `parameters.prop` that
`parameters.propdef` does not declare are reported per artifact (`orphanParameters`, and in the
text output) and not written; `--keep-orphan-parameters` (config
`snapshot.keepOrphanParameters`) writes them anyway. Artifacts without a `parameters.propdef` are
not checked.

### Local edits

The state file records per artifact the hash of every local file as the snapshot left it
(`filesHash`). A local folder with another hash has local edits: the snapshot skips it and reports
`local-modified`, whether the tenant changed or not. When the tenant changed too, the snapshot
downloads it and compares: if the tenant has exactly the local content (the local edit was
deployed), the artifact counts as `unchanged`, the local files stay and the state is updated.

- `--overwrite-local` takes the tenant's version anyway (configOverrides keys stay local, see below).
- `--fail-on-local-modified` ends with exit code 5 when an artifact is local-modified, for CI.
- An artifact without an earlier snapshot record (first run on an existing repository) cannot be
  told apart: a difference counts as `changed`. Run `--dry-run` first, on a clean Git tree.

### Deletions

Files that are gone from an artifact on the tenant are deleted locally: a written artifact folder
is replaced by the tenant's content. A whole artifact or package that is gone from the tenant is
only reported (`deleted`); `--prune` removes its folder, never when it was edited locally, and never
`local-only` folders. Packages outside `--ids-include` / `--ids-exclude` are left alone.

## Commit and work folder

Unless `--git-skip-commit`, the snapshot commits (it never pushes). `--dir-git-repo` is the root of
a Git working tree or a folder inside one: the repository is found in it or above it (as `git
rev-parse --show-toplevel` does), and only changes below `--dir-git-repo` are staged and
committed. Changes elsewhere in the working tree stay as they are; when the Git index already holds
staged changes outside `--dir-git-repo`, the commit is refused rather than mixing them
in.

- `--git-commit-changed-only` stages only what this snapshot wrote or removed (artifact folders,
  package files, the state file) instead of every change below `--dir-git-repo`.
- `--git-commit-user` / `--git-commit-email` set the author (default: the GitHub Actions bot),
  `--git-commit-msg` the message.

Downloads are extracted into a temporary folder of the run, created in `--dir-work` (default: the
system's temporary directory) and removed after the run; `--keep-work-dir` keeps it for debugging.
`snapshot restore` uses a work folder the same way.

## Stable output

Snapshotting the same tenant twice gives an empty `git status`:

- **parameters.prop** and **metainfo.prop**: the timestamp comment that the tenant writes
  (`#Thu Oct 08 14:11:02 UTC 2026`) and every other comment line containing `:` are dropped; other
  comment lines (such as `#`) are kept at the top; the entries are sorted by key (continuation
  lines stay with their key). The first snapshot with this version rewrites `metainfo.prop` once.
- **Line endings**: text files (`.iflw .groovy .gsh .js .xml .xsd .xsl .xslt .prop .propdef
  .properties .MF .json .wsdl .edmx .mmap .project .txt`) are written with LF; all other files
  (`.jar`, `.zip`, ...) byte for byte.
- **Bundle-Version**: the repository's version is kept, or the tenant's designtime version when it
  is higher ([versioning.md](versioning.md#export-and-download)).
- **State file**: sorted keys, no run timestamps; it changes only when something changed.

A folder is written only when its files differ from the tenant's; an unchanged artifact is not
touched.

## Deployment copies

The orchestrator can deploy one folder under several IDs, each with its own `configOverrides`
(`artifactDir: UtilitiesBase_MDX_to_EDM_Outbound` deployed as `UtilitiesBase_MDX_to_EDM_Outbound`
and as `UtilitiesBase_Herrenberg_MDX_to_EDM_Outbound`), and under a `deploymentPrefix`. On the
tenant these are separate artifacts. A snapshot does not write them, because an edit to such a
folder would never be deployed:

- an artifact whose ID is an `artifactId` with a different `artifactDir`,
- an artifact `<prefix>_<artifactId>` and a package `<prefix><packageId>` for the config's
  `deploymentPrefix` (and for `--deployment-prefix` values, for prefixes given on the command line).

They are listed as `derived (source: <package>/<artifactDir>)`. The copy is compared with its
source folder; when it differs in anything other than what a deployment changes
(`parameters.prop`, `META-INF/MANIFEST.MF`, `metainfo.prop`, `.project`, the model's file name),
the snapshot warns: someone edited the copy on the tenant, and the next deployment from the source
would overwrite that edit. The comparison runs again only when the copy changed on the tenant.

A local folder of a deployment copy (from snapshots before this rule) is reported; `--prune`
removes it. `--include-derived` turns the rule off and writes copies like any other artifact.

**configOverrides of the source.** When the source itself is deployed with `configOverrides`,
the tenant's `parameters.prop` contains the deployed values. The snapshot keeps the repository's
values for exactly those keys (the base values the overrides are applied to) and takes every
other key from the tenant.

## Incremental snapshots

A full snapshot downloads every artifact that has no local edits and writes what differs.
`--incremental` also skips the **download** when all of these are unchanged since the last
snapshot:

| Signal | Source | Catches |
|--------|--------|---------|
| designtime version | package listing (no extra call) | uploads, *Save as version* |
| `ModifiedAt` | package listing (no extra call) | Web UI edits without a new version |
| SHA-256 of the configured parameters | one small call per integration flow | *Configure* changes |
| SHA-256 of every local file | local files | local edits (`local-modified`) and missing folders |

An artifact for which the tenant does not report `ModifiedAt`, or whose configuration cannot be
read, is always downloaded. `--dry-run` uses the same signals. The state is
`<dir-git-repo>/.cpi/snapshot-state.json` (`--state-file` to move it); commit it with the
snapshot. It also records the tenant's host and a hash of each artifact's content, which the
[orchestrator](orchestrator.md#comparison-without-downloads) uses to skip downloads.

Recommended: `--incremental` for frequent runs and a full run regularly as the safety net.
Deleting the state file makes the next run treat every difference as `changed`.

## Parallel downloads and failures

Up to `--parallel` artifacts (default 8) are downloaded at the same time, across all packages.
Throttled reads (`429`, `502`-`504`) are retried (`--read-retries`). A failing package or artifact
does not stop the others: the rest is written and the state saved, and the run ends with exit
code 7 and the failures in the result.

All settings can also be set in the config file under `snapshot` (`incremental`, `parallel`,
`stateFile`, `deployConfig`, `prune`, ...), see [commands.md](commands.md#snapshot).
