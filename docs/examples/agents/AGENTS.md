# Integration content

This repository holds SAP Cloud Integration content for the tenant(s) reached through the
`cpi-dev` (development, all tools) and `cpi-qa` (read-only) MCP servers.

- Tenant rules: read `.cpi/conventions.md` before planning, building, testing or reviewing a
  flow. If it does not exist, run the `cpi-discover` skill first.
- Plans are in `.cpi/plans/`, test cases in `.cpi/tests/`, build-loop logs in `.cpi/loops/`.
- To find flows, callers, shared credentials, scripts or Partner Directory parameters, use the
  graph tools (`graph_search`, `graph_neighbors`, `graph_path`) on `.cpi/graph.json` instead of
  reading `.cpi/discovery.json`.
- Use the skills `cpi-plan`, `cpi-build`, `cpi-test` and `cpi-review` for that work. Unsure
  which tool or skill fits? Call the MCP tool `help` (it can also return a skill's instructions).
  Auth or 403 errors: call `doctor` and report what it says instead of retrying.
- Before uploading, run `drift` so tenant-only edits are not overwritten; `upload_artifacts` and
  `deploy` with `dry_run: true` show what would change and why.
- Only change the development tenant. Never deploy, undeploy or send test messages to QA or
  production; ask the user instead.
- No secrets, tokens or productive data in files, parameters or test messages.
