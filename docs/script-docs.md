# Script comments

Scripts are found by what their comments say. `discover` reads the comments of every Groovy
script into `.cpi/discovery.json` and `.cpi/graph.json`, `graph_search` searches them, and lint
reports scripts whose comments are missing or say nothing. The goal: a person or an agent looking
for "format a date" finds the function that already does it, instead of writing it again.

## Template

One comment at the top of a flow script, or above each public function of a script collection:

```groovy
/**
 * Formats the SAP date in header SAP_Date as ISO 8601 into header ISO_Date.
 *
 * Optional details: when to use it, edge cases, what it does not do.
 *
 * @reads    header SAP_Date (yyyyMMdd)
 * @reads    property dateFormat (optional, default yyyy-MM-dd)
 * @writes   header ISO_Date
 * @body     unchanged
 * @throws   IllegalArgumentException when SAP_Date is not yyyyMMdd
 * @keywords date, format, ISO 8601, SAP date
 * @example  script collection Common_Scripts, file DateUtils.groovy, function formatSapDate
 */
def Message formatSapDate(Message message) {
```

| Part | What to write |
|------|---------------|
| First sentence | What it does, with the words someone would search for. Shown by discover and `graph_search`. Not "TODO", "Script." or the function name; at least three words. |
| Further text | Optional: when to use it, edge cases, what it does not do. |
| `@reads` | Each header or property the code reads by name (`header X`, `property Y`), or `body` with what is expected. |
| `@writes` | Each header or property it writes, or `body` with what it produces. |
| `@body` | What happens to the body (unchanged, replaced, parsed). |
| `@throws` | Errors and when they occur. |
| `@keywords` | Comma-separated search words, synonyms and business terms (`IDoc number, DOCNUM`). |
| `@example` | How a step calls it (collection, file, function) or a call with input and result. |
| `@param`, `@return` | For helper functions. |
| `@deprecated` | What to use instead. |

A tag's text continues on the following lines until the next tag or a blank line. Helper functions
that flows must not call are `private`; they need no comment.

## What uses it

- **discover** (`.cpi/discovery.json`): per script `summary`, `functions` (with `doc`, `summary`
  and `tags`), `keywords`, `reads` and `writes` (from the code), `shape`, `docStatus`
  (`ok`, `incomplete`, `missing`) and `docIssues`.
- **graph** (`.cpi/graph.json`, [graph.md](graph.md)): script nodes carry `reusable` (`true` for
  scripts of script collections), `summary`, `keywords`, `functions`, `reads`, `writes`,
  `docStatus`, `usages` (flows that use it) and `shape` (equal for scripts that differ only in
  comments and formatting). `graph_search` with several words finds nodes that contain all of
  them, so `"format date"` finds a script whose summary says "Formats the SAP date".
- **lint** ([lint.md](lint.md)): `script-doc` (info) for flow scripts without a usable first
  sentence; for script collections `collection-script-doc` (warning, a public function without a
  comment), `collection-script-doc-quality` (warning, a placeholder first sentence or a header or
  property the code reads or writes that the comments do not name) and `collection-script-keywords`
  (info, no `@keywords`).
- **`cpictl script-doc <files>`**: the same checks for single files, offline, with `--fail` for
  CI. Whether a file belongs to a script collection is read from the MANIFEST.MF of its artifact
  folder.

## Hook for agents

The cpi plugin ([plugin.md](plugin.md)) runs `cpictl script-doc --hook` after every file Claude
Code writes. For a Groovy script with incomplete comments it returns the missing parts to the
agent (exit code 2), which then fixes them; anything else passes silently. Without the plugin, add
it to `.claude/settings.json`:

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Write|Edit|MultiEdit",
        "hooks": [{ "type": "command", "command": "cpictl script-doc --hook", "timeout": 10 }]
      }
    ]
  }
}
```

Other agents: run `cpictl script-doc <file>` after writing a script, or `lint` before a commit.
