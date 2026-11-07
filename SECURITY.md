# Security

cpictl runs with SAP service keys and users' gateway sign-ins. Its access scope (`pkg/scope`) is what lets
several teams share one tenant through `cpictl mcp` and the cpictl gateway. Mistakes there let one team reach
another's flows, so security fixes follow the process below.

## Reporting a vulnerability

Report it privately to security@cpars.de (DRAFT: confirm the address before merging). Include a test against the
mock tenant (`internal/cpitest`, `cpictl mock-tenant`) where you can. Never use a real tenant or real credentials
to prove a finding.

## Supported versions

Only the latest release gets security fixes. Pin a release; update when an advisory names it.

## How fixes are released

1. **Regression test first.** Every fix comes with a test that fails on the previous release. Endpoint routing
   is also checked against a model of the runtime (`pkg/scope/routing_fuzz_test.go`), which CI fuzzes on every
   push.
2. **Independent verification before the tag.** A release whose changes since the last tag touch the paths
   below needs a verification by someone who did not write the fix: the cpictl gateway's verifier, or a fresh
   reviewer on the mock tenant. The release workflow enforces it: it refuses such a release unless it is started
   with **security_verified** set, which records that the verification was done.
   - `pkg/scope`, `pkg/httpclnt`, `pkg/pdaddress`
   - masking: `pkg/mask`, `pkg/secretscan`, `internal/cmd/redact.go`
   - `internal/auth`, `internal/mcp`
   - credential handling: `internal/cmd/root.go`, `internal/cmd/service_details.go`, `internal/cmd/profile*.go`,
     `pkg/ops/security*.go`, `pkg/ops/keymaterial.go`
   - `pkg/testrun/refpath.go`
3. **Advisory.** Each fix is announced to customers and internal consumers (the cpictl gateway, CPI Studio)
   with the affected and fixed versions; the CHANGELOG entry says **What to do**. Versions up to 0.38.1 are
   public: fixes that affect them are also published as GitHub Security Advisories.
4. **Signed release.** `SHA256SUMS` is signed with the cpictl release key (cosign); archives carry build
   provenance where the GitHub plan offers attestations for private repositories (README, Install).
