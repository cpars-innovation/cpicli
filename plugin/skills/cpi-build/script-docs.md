# Script comments: find before you write, document what you write

Scripts are found by what their comments say. discover reads them into `.cpi/discovery.json`
and `.cpi/graph.json`; `graph_search` searches them. A script without a good comment is invisible
to the next person (or agent) looking for one, who then writes it again.

## Before writing a script: look for one

1. `graph_search` with `types: ["script"]` and two or three words of the purpose
   (`"format date"`, `"idoc number"`, `"log payload"`). Every word must appear in the script's
   name, summary, keywords, functions or the headers it reads and writes.
2. `reusable: "true"` means the script is in a script collection: call its function from the
   script step (`script collection`, file, function, see iflow-structure.md) instead of copying
   it. Check `docStatus` (`ok` = documented) and `reads` / `writes`: they say which headers and
   properties it needs and sets.
3. `reusable: "false"` with `usages` > 1 is a flow script copied into several flows: a candidate
   for a script collection (cpi-improve), not something to copy once more.
4. `graph_neighbors` on the script node lists the flows that use it (`uses_script`).
5. Near-duplicates: scripts with the same `shape` have the same code apart from comments and
   formatting; `graph_search` with the shape value lists them.

Only when nothing fits, write a new script (in the collection the conventions name, if it is
generic).

## The comment template

One comment at the top of a flow script, or above each public function of a script collection
(`cpictl script-doc` / MCP `help {"topic": "script-doc"}` show the same):

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

- **First sentence**: what it does, with the words someone would search for. It is what
  discovery and `graph_search` show. Not "TODO", not "Script.", not the function name.
- **@reads / @writes**: every header and property the code reads or writes by name
  (`header X`, `property Y`), plus `body` with what is expected or produced. lint compares them
  with the code.
- **@body**: what happens to the body (unchanged, replaced by ..., parsed as XML).
- **@throws**: errors and when.
- **@keywords**: comma-separated search words, including synonyms and the business terms
  (`IDoc number, DOCNUM, SAP_ApplicationID`).
- **@example**: how a step calls it (collection, file, function) or a call with input and
  result.
- `@param` / `@return` for helper functions, `@deprecated` with what to use instead.
- Helpers that flows must not call are `private`.

## What checks it

| Check | When |
|-------|------|
| Hook `cpictl script-doc --hook` (cpi plugin) | after every write of a `.groovy` file: missing parts come back to you right away; fix them before you continue |
| `cpictl script-doc <files>` | any time, offline |
| lint `script-doc` (info) | a flow script without a usable first sentence |
| lint `collection-script-doc` (warning) | a public collection function without a comment |
| lint `collection-script-doc-quality` (warning) | placeholder first sentence, headers or properties the code uses but the comments do not name |
| lint `collection-script-keywords` (info) | a collection script without `@keywords` |
