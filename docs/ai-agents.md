# Using AI agents safely

An AI agent that uses cpictl sends what it reads to its model provider: tool results of `cpictl mcp`,
and the stdout and stderr of every `cpictl` command it runs in a shell. Tenant data holds personal data
(names, e-mail addresses, IBANs in payloads and error texts) and sometimes secrets. This page says how to
keep that data away from the model.

## The safe setup

1. **Gateway mode.** Point the profile at a cpictl gateway (`gateway:` in the profile) instead of a SAP
   service key: the developer's machine holds no CPI key, the gateway enforces the team's access scope
   and its own masking rules, and cpictl redacts its output by default.
2. **An access scope** (`--scope-file`, [access-scope.md](access-scope.md)) or `--read-only` for
   `cpictl mcp`: the agent sees and changes only the team's packages.
3. **Redaction on.** `cpictl mcp` withholds payloads by default. For the CLI, set `redact: true` in the
   profile (or `CPICTL_REDACT=true`, or `--redact`); in gateway mode it is on already.

## What is withheld and masked

| | `cpictl mcp` (default) | CLI with `--redact` |
|---|---|---|
| Message bodies: attachments, message store entries, trace payloads, data store entries, variables, test message responses | withheld (size only) | withheld (size only); `--out FILE` still writes them |
| Message error texts (`errorText`, step errors, error group samples) | withheld (size only) | withheld |
| Business keys (custom header values, trace tree keys) | `••••` | `••••` |
| Values under sensitive names (passwords, tokens, authorization, API keys, …) | `••••` | `••••` |
| Personal data and secret shapes anywhere else: IBAN, e-mail, phone, payment card, JWT, `Bearer`/`Basic`, private keys, credentials and signatures in URLs | masked | masked, on stdout and stderr |
| Script lines with a hard-coded secret literal | `••••` (the line keeps its indentation) | `••••` |
| Partner Directory entries marked secure | `••••`, `"secure": true` | `••••` |

`cpictl mcp --send-payloads` and `cpictl --no-redact` send bodies and error texts; the value masker
still runs in `cpictl mcp`. A masked value always reads `••••`, and a write that carries it is refused
(MCP write tools, `pd deploy`): a masked view must never be written back to the tenant.

`auth`, `whoami`, `profile` and `version` show the user's own sign-in and are not redacted.

When cpictl runs under an agent (it sees `CLAUDECODE`, `CODEX_*`, `CURSOR_AGENT` or `GEMINI_CLI`) with
redaction off, it says so once on stderr.

## Masking rules

`--mask-config FILE` (for `cpictl mcp` and the CLI) adds rules or limits the built-in detectors. YAML or
JSON:

```yaml
builtIns: [iban, email, phone, card, private-key, jwt, authorization, url-credentials, url-secret]  # default: all
names: ["^x-tenant-key$", "customerNo"]   # more sensitive names (regex, case-insensitive)
rules:
  - kind: path                             # every value below these XML elements or JSON keys
    pattern: //Customer/Name
  - kind: regex                            # every match
    pattern: 'ORD-\d{6}'
```

An unknown detector, an invalid regex or path, or a regex that matches empty text is an error: a rule that
silently does nothing would leak. Go programs (the cpictl gateway, CPI Studio) use the same masker:
`pkg/mask` (`mask.New(cfg)`, `Text`, `JSON`, `Field`, `InPlace`, `Writer`).

## Secure Partner Directory entries

Most Partner Directory entries are routing and value mapping data an agent needs. Mark the few that hold
secrets in the content repository, at `.cpi/secrets/partner-directory.json`:

```json
{ "entries": [{ "pid": "FI_Bank", "id": "ApiToken" }],
  "patterns": [{ "pid": "*", "id": "*Token*" }] }
```

A marked entry's value is never returned by `get_pd_parameters` or `cpictl pd get` (with or without
`--content`); its name, kind and size stay visible with `"secure": true`, so the agent can tell the user
to set it. A file that cannot be read, is not valid or has a mark without `pid` or `id` withholds **every**
Partner Directory value (fail closed) and says why. `cpictl mcp` reads the file below `--root`, the CLI
from the repository it runs in. `pd snapshot` keeps writing real values into the repository: it is the
customer's own.

## Hard-coded secrets in scripts

The lint rule `hard-coded-secret` (security, warning) flags script lines with a hard-coded secret literal:
private keys, credentials or signatures in URLs, key material (`SecretKeySpec("…")`), a quoted
`Bearer`/`Basic` token, a secret-named variable assigned a literal, and random-looking literals. It
judges literals only, so runtime credential code (`cred.getPassword()`, `"Bearer " + accessToken`,
`"${token}"`), UUIDs and hex digests are no findings. Findings name the line and the kind, never the
value. Make it an error in `.cpi/lint.yaml` ([lint.md](lint.md)) to fail CI on it.
