# Claude Code plugin

The plugin `cpi` turns Claude Code into an integration developer for *your* tenant. It
combines three things:

| Layer | Where it lives | Who maintains it |
|-------|----------------|------------------|
| **Tools**: the cpictl MCP server (upload, deploy, test, logs, ...) | this repository (`cpictl mcp`) | cpicli releases |
| **Roles**: how to plan, build, test and review a flow (skills, reviewer agent) | this repository (`plugin/`) | cpicli releases |
| **Conventions**: how *your* tenant builds flows (naming, error handling, logging, scripts, tests) | your content repository (`.cpi/`) | your team |

Cursor, OpenCode, Codex or Gemini CLI: the MCP server and the skills work there too, see [agents.md](agents.md).

The roles are generic and the same for everybody. Everything that differs between tenants is
data in `.cpi/` of the repository that holds your integration content. The skills read it in
every task. Plugin updates therefore never overwrite your rules, the rules are reviewed and
versioned together with the flows they describe, and each repository (customer, landscape,
team) can have its own conventions.

## Install

1. Install `cpictl` and put it on the `PATH` (or set `CPICTL_BIN` to its path).
2. Set the tenant connection in the environment that starts Claude Code
   ([configuration.md](configuration.md)): `CPICTL_TMN_HOST`, `CPICTL_OAUTH_HOST`,
   `CPICTL_OAUTH_CLIENTID`, `CPICTL_OAUTH_CLIENTSECRET`, and for test messages
   `CPICTL_RUNTIME_OAUTH_CLIENTID` / `CPICTL_RUNTIME_OAUTH_CLIENTSECRET`. The plugin's MCP
   configuration contains no credentials; the server inherits them from the environment.
   Use a development tenant.
3. In Claude Code:

   ```text
   /plugin marketplace add cpars-innovation/cpicli
   /plugin install cpi@cpicli
   ```

   (or `claude plugin marketplace add cpars-innovation/cpicli` and
   `claude plugin install cpi@cpicli` from the shell).

The MCP server runs with `--root` set to the project directory, so local paths of tool calls
stay inside the repository you opened.

## Skills and agent

| Name | Use it to | Changes |
|------|-----------|---------|
| `cpi-discover` | Analyse the existing flows and write `.cpi/conventions.md` (first time and refresh) | local files |
| `cpi-plan` | Turn a requirement into a design in `.cpi/plans/<FlowId>.md`, agreed with you | local files |
| `cpi-build` | Implement the design: edit files, upload, validate, deploy, fix until it runs | tenant (designtime, runtime) |
| `cpi-test` | Write test cases in `.cpi/tests/<FlowId>/`, choose the route by trigger (direct, test harness, test entry), send them, trace and diagnose failures ([testing.md](testing.md)) | triggers processing |
| `cpi-review` | Review against conventions, plan, tenant checks and a checklist | none |
| `cpi-improve` | Lint packages or flows ([lint.md](lint.md)), write proposals to `.cpi/improvements/` (script collections, Partner Directory, dead steps, best practices), apply one agreed proposal at a time | local files; tenant (dev) when applying |
| `cpi-operate` | Find what is failing and why, follow a message by its business key, handle messages waiting in JMS queues (retry, move), check runtime errors, locks, certificates, connections and tenant settings ([monitoring.md](monitoring.md)) | tenant (retries, moves, log level) only with your go |
| agent `cpi-reviewer` | Does the review for cpi-review with a fresh context and read-only tools | none |

Skills are picked automatically from what you ask ("build an interface that ...", "why do the
orders fail?"), or called directly, e.g. `/cpi:cpi-discover`. To see what is available, run
`/skills` in Claude Code, ask "what can the cpi server do?" (the MCP tool `help` lists tools,
skills and workflows), or `cpictl skills list` in a terminal.

Why a reviewer *agent* and the rest as skills: planning, building and testing are
conversations with you and need the full context of the task, so they run in the main
session. A review is better done by someone who did not write the code: the agent starts
without the builder's context and can only read (its tool list contains no upload, deploy,
parameter or send tool).

## Getting started in a content repository

```text
> Discover the conventions of our tenant, packages SalesOrders and Finance are representative.
```

`cpi-discover` calls `discover_tenant` (or `cpictl discover`; `--dir` analyses content that
is already in the repository), reads a few typical flows and writes:

```
.cpi/
├── conventions.md      # the rules, with evidence and strength (rule / common / observed)
├── discovery.json      # the facts, regenerate with `cpictl discover`
├── graph.json          # the same as a graph: who calls whom, what flows share (graph_* tools)
├── templates/
│   ├── brief.md        # what a requester fills in for a new flow (adapt it to your team)
│   └── scripts/        # the house logging / error handling scripts, copied by new flows
├── briefs/             # filled-in briefs, with sample files in briefs/<name>/
├── plans/              # designs written by cpi-plan
└── tests/<FlowId>/     # test cases and payloads used by cpi-test
```

Review `conventions.md`, answer the open questions and commit it. From then on, add rules
whenever you notice something ("all receiver URLs are externalised as `<System>_Host`",
"never call the SAP S/4 receiver from tests"). The skills follow the file; the reviewer checks
against it.

## Templates and briefs

New flows are built from **reference flows of your tenant**, not from generic files: a generic
`.iflw` carries component versions and a style that do not match your tenant. `cpi-discover`
proposes one reference flow per pattern (sync HTTPS to OData, SFTP polling, ProcessDirect
sub-flow, ...) in the *Templates* section of `conventions.md`; the team marks them `agreed` and
keeps those flows deployable and up to date. Many teams keep dedicated `Template_*` flows in a
package of their own for this. The house logging and error handling scripts go to
`.cpi/templates/scripts/`. `cpi-build` creates a new flow with `copy_iflow` from the agreed
template: new ID, name, description and sender addresses, nothing renamed by hand
([new-flows.md](new-flows.md)).

A request for a new flow starts with a **brief**: `cpi-plan` copies `.cpi/templates/brief.md`
to `.cpi/briefs/<name>.md`, the requester fills in purpose, sender, receivers, messages (sample
files next to it), mapping, error behaviour, environments and acceptance criteria. IDs, names
and descriptions may stay empty: the planner proposes them from the naming conventions and the
requester confirms. `cpi-plan` then asks only for what is missing and writes the plan.

```text
> Create a brief for the new invoice interface to Coupa.
> Plan the flow from .cpi/briefs/coupa-invoices.md.
```

## Adapting the roles

- **Team rules** belong in `.cpi/conventions.md` (including "Review checklist additions").
- **Different workflows**: add your own project skills in `.claude/skills/<name>/SKILL.md`
  of the content repository. Plugin skills are namespaced (`/cpi:cpi-build`), so your skills
  never clash with them; describe in yours when to use them instead.
- **Several tenants**: one MCP server per tenant. Add a second server for QA in the
  repository's `.mcp.json` with different environment variables (example: [examples/claude-code.mcp.json](examples/claude-code.mcp.json)); keep agents on the
  development tenant.

## Other MCP clients

Skills and agents are a Claude Code feature. Other clients still get the server's
instructions (the build loop and which tool to use when) and can be pointed at
`.cpi/conventions.md` in their own rules file.

## Safety

To limit the plugin's server, set `CPICTL_READ_ONLY=true`, `CPICTL_TOOLS` or
`CPICTL_DISABLE_TOOLS` in the environment ([mcp.md](mcp.md#limiting-tools)).

The plugin adds no permissions: every tool call goes through Claude Code's permission
prompts and the server's own rules (undeploy needs `confirm`, Partner Directory deploys are
dry runs by default, security material is read-only, paths stay inside the repository).
`send_test_message` triggers real processing; keep test data synthetic and list receivers
that must not be called in the Testing section of the conventions.
