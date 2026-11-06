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
   profile (or `CPICTL_REDACT=true`, or `--redact`). In gateway mode it is always on: `--no-redact` is ignored
   there, a `--mask-config` can only add masking (every detector stays on), `--send-payloads` is ignored and
   `set_masking` can only tighten. A repository's `cpictl.yaml` can never loosen masking (`no-redact`,
   `send-payloads`, `redact-payloads`, `masking-control`, `mask-config` there are errors).

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

`cpictl mcp --send-payloads` and `cpictl --no-redact` (not in gateway mode) send bodies and error texts; the
value masker still runs in `cpictl mcp`. A masked value always reads `••••`, and a write that carries it is
refused: MCP write tools, `pd deploy`, and every artifact upload whose files contain it. A masked view must
never be written back to the tenant.

How values are found:

- **Detectors** (everywhere): IBAN (mod-97, also lower case and followed by more text), e-mail (also with
  umlauts), phone with a country prefix, payment cards (Luhn and card-shaped; not inside names like
  `Flow-4000…`), JWT, `Bearer`/`Basic` (also `bearer:` and short `user:password` credentials), private keys
  (also cut off), credentials and signatures in URLs, connection-string secrets (`SharedAccessKey=`,
  `AccountKey=`, `Password=`, `Pwd=`).
- **Sensitive names**: a value under a key like `password`, `pwd`, `token`, `secret`, `apiKey` is masked whole,
  also in name/value objects of any spelling (`{"key":"SFTP_Password","value":…}`, `{"id":"ClientSecret",…}`,
  `{"Name":…,"Value":…}`). Keys that name security material instead of holding it (`credentialName`,
  `keyAlias`, `tokenServiceUrl`, `passwordType`) are not masked whole.
- **Identifiers** (`…Id`, `…Guid`, hashes, versions) stay usable for the next call: only the detectors for
  e-mail, tokens, keys, URL credentials and connection strings run on them, an IBAN only as the whole
  value, and not card and phone (an ID like `SAP_ApplicationID` holding a card number is therefore not
  masked: add a name or rule for such fields). A sensitive identifier (`tokenId`) is masked whole.
- **Writes**: an upload, a configuration change (`configure`, `set_parameters`) or a Partner Directory
  deploy that carries `••••` is refused.

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

## Who decides what is masked

- **The operator, at the start:** `--mask-config`, `--mask-pattern`, `--send-payloads` and `--masking-control`
  of `cpictl mcp`; `redact: true`, `--mask-config` for the CLI.
- **The MCP client, while the server runs:** `get_masking` shows the policy, `set_masking` changes it.
  `--masking-control tighten` (the default) lets it only add masking (detectors, names, rules, withholding
  payloads), so it is safe to leave to the agent. `full` lets it change everything, for a client that keeps
  the tool away from its agent and decides itself what the agent sees: CPI Studio, the cpictl gateway.
  `fixed` turns `set_masking` off. See [mcp.md](mcp.md#masking).
- **Pipelines and other programs:** `cpictl mask [file...]` masks files or stdin with the same masker
  (`--mask-config`, `--detector`, `--name`, `--rule path://… | regex:…`, `--lines` to keep the layout);
  `cpictl mask config` checks rules and shows the policy. Go programs use `pkg/mask` directly.

Regex rules are compiled once when the policy is set; a rule with a fixed start (`ORD-\d+`) only runs on
text that contains it, so rules cost little on text they cannot match.

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
