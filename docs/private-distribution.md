# cpictl as a private repository

This branch prepares cpictl for a private repository with a proprietary license. Nothing changes until the owner
merges it and switches the repository to private. Versions up to and including **0.38.1** were published under the
Apache License 2.0 and stay available under it.

## What changes for consumers

How a pipeline breaks depends on how it fetches cpictl, not on the version it pins:

| How cpictl is fetched | Versions up to 0.38.1 | Later versions |
|---|---|---|
| Release download from GitHub (`curl …/releases/download/…`) | **breaks**: private releases need a token | token |
| `git clone`, or `go get` with `GOPROXY=direct` | **breaks** | token |
| `go get` through the default proxy (`proxy.golang.org`) | keeps working (cached public versions) | not available |
| With `GOPRIVATE=github.com/cpars-innovation/*` set | direct from GitHub, **token**, also for old versions | token |
| Built images, vendored code, mirrored binaries | keep working | — |

### Go consumers (cpictl gateway, CPI Studio's studio-api)

```bash
go env -w GOPRIVATE=github.com/cpars-innovation/*
git config --global url."https://x-access-token:${GH_TOKEN}@github.com/cpars-innovation/".insteadOf "https://github.com/cpars-innovation/"
go get github.com/cpars-innovation/cpicli@v0.39.0
```

In GitHub Actions, give the workflow a token with read access to cpicli. A fine-grained PAT or a GitHub App token
works; the default `GITHUB_TOKEN` of another repository has no access. Set the same two lines before `go mod
download`. Docker builds need the token as a build secret (`--secret id=gh_token`), never as a build argument.

### Binary consumers (session images, pipelines)

```bash
gh release download v0.39.0 --repo cpars-innovation/cpicli \
  --pattern 'cpictl_v0.39.0_linux_amd64.tar.gz' --pattern SHA256SUMS --pattern SHA256SUMS.sigstore.json
sha256sum -c SHA256SUMS --ignore-missing
cosign verify-blob SHA256SUMS --bundle SHA256SUMS.sigstore.json --key cosign.pub --insecure-ignore-tlog
```

`gh` needs `GH_TOKEN` with read access. Get `cosign.pub` from cpars innovation (it is also in the repository).
Recommended: mirror the binaries you depend on to your own artifact store, and point pipelines there.

### Verification of releases

- **Signature.** `SHA256SUMS` is signed with the cpictl release key (`cosign.pub`). Nothing is written to the public
  Sigstore log, which would reveal the repository and workflow names.
- **Build provenance.** Attestations (`gh attestation verify`) exist for a private repository only on GitHub
  Enterprise Cloud. Set the repository variable `ATTESTATIONS=true` there; without it the step is skipped.

## Checklist for the switch (owner)

1. **Legal.**
   - Replace the draft `LICENSE` with the approved license agreement.
   - Confirm the security contact in `SECURITY.md`.
   - Confirm that cpars innovation owns the code written by employees, contractors and AI tools.
   - The parts from contributors outside cpars (FlashPipe: Eng Swee Yeoh, Vadim Klimov, Ariel Bravo) stay
     Apache-2.0. Keep `NOTICE` and `licenses/`.
2. **Advisories.** Publish the prepared security advisories for 0.34.1–0.35.8 (cpicli#60) while the repository is
   public: they concern versions that stay public.
3. **Release key.** Run `cosign generate-key-pair` and put the private key and its password in the secrets
   `COSIGN_PRIVATE_KEY` and `COSIGN_PASSWORD`. Commit `cosign.pub` and give it to customers.
4. **Mirror first.** Point the gateway's and Studio's pipelines at a token-based fetch (above) and check them on a
   branch. Mirror the release binaries they use.
5. **Merge this branch**, tag the first proprietary release (e.g. 0.39.0), and put a line in the CHANGELOG: "0.38.1
   is the last version under the Apache License 2.0".
6. **Make the repository private** (Settings, Danger Zone). GitHub drops stars and watchers.
7. **Costs.** Actions minutes are no longer free (CI with race tests and the 60 s fuzz run on every push). CodeQL
   on a private repository needs GitHub Advanced Security. Decide whether to keep both or to run CI on pull
   requests only.
8. **Plugin and skills.** Take down any public copy of the Claude Code plugin or the skills, or move them with the
   repository.
