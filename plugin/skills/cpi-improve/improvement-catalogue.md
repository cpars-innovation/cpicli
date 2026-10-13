# Improvement catalogue

Per lint rule: what it means, when to act, how to change it in Cloud Integration, and the risk.
`fix` = `lint_fix` can do it (always review the diff).

## Reuse

| Rule | Act when | How | Risk |
|------|----------|-----|------|
| `duplicate-script` (fix) | 2+ flows (more = higher priority) | move to the package's or the shared collection, reference it ([script-collections.md](script-collections.md)) | low; deploy the collection first |
| `use-script-collection` (fix) | always | reference the existing collection, delete the local copy | low |
| `similar-script` | the differences are accidental | unify (keep the best version), then as `duplicate-script` | medium: test every variant's flow |
| `duplicate-mapping` | 2+ flows | one message mapping artifact in the package, the flows reference it | medium: mapping step reference changes |
| `missing-script-collection` | always | snapshot the package holding it, or fix the ID | the flow does not deploy without it |
| `collection-script-doc` | always for collections | a `/** */` comment per public function: purpose (first sentence, what search finds), reads, writes, errors, how to call it ([script-tests.md](../cpi-build/script-tests.md)) | none (comments only); re-upload the collection |

## Partner Directory

| Rule | Act when | How | Risk |
|------|----------|-----|------|
| `router-literals` | the branches differ mainly in receiver or parameters | routing table in the Partner Directory, one dynamic receiver ([pattern 1](partner-directory-patterns.md#pattern-1-routing-table-router-literals)) | medium-high: test every branch |
| `lookup-table-in-script` | the table changes or is partner-specific | value mapping (code lists) or Partner Directory | medium |
| `deployment-copies` | copies differ only in configOverrides | one flow, partner values from the Partner Directory ([pattern 3](partner-directory-patterns.md#pattern-3-one-flow-deployed-per-partner-deployment-copies)) | high: migration per partner |

## Dead weight

| Rule | Act when | How | Risk |
|------|----------|-----|------|
| `unconnected-step` (fix) | always (check it is not a half-finished change) | remove it | none |
| `unused-script` (fix) | always | delete it | low: a script loaded dynamically by name (rare) |
| `unused-resource` | always, after a check | delete it | low: resources used outside the flow (rare) |
| `unused-parameter` | always | remove from `parameters.prop`, `parameters.propdef` and configure files | low: configure files still setting it fail with an unknown key |
| `noop-content-modifier` (fix on request) | always | remove it and connect its neighbours | low; check the step really has no body (empty body field) |
| `property-never-read` | when sure no subprocess or script reads it dynamically | remove the property | low |

## Simplify

| Rule | Act when | How | Risk |
|------|----------|-----|------|
| `consecutive-content-modifiers` | the second does not read what the first sets | merge into one | low |
| `converter-roundtrip` | always | remove both converters (or add the missing step between them) | medium: namespaces and arrays may differ after a round trip |
| `trivial-script` | always | a content modifier with the same headers/properties | low |
| `large-script` | the script mixes concerns | split into functions, move reusable parts to a collection, use standard steps | medium |

## Robustness

| Rule | Act when | How | Risk |
|------|----------|-----|------|
| `no-exception-subprocess` | conventions require one (usually) | add the house exception subprocess (copy it from the template flow) | low |
| `swallowed-exception` | always | rethrow, or handle it explicitly (set a status, log, route) | medium: errors now become visible (that is the point) |
| `collection-script-tests` | always for collections | tests in `tests/scripts/<package>/<collection>/` calling every public function with today's inputs ([script-tests.md](../cpi-build/script-tests.md)) | none (test code, not uploaded) |
| `collection-script-coverage` | below the minimum, or no report | more test cases for the uncovered branches (JaCoCo HTML report); run the tests before lint | none |
| `idoc-no-idempotency` | the receiver is not idempotent itself (creates documents) | an Idempotent Process Call around the processing, keyed on the IDoc number (`SapIDocDbId` or `SapIDocTransferId` header), or decouple: IDoc to a JMS queue, a second flow processes it | medium: the key must be unique per IDoc; JMS needs queue capacity and a retry policy |

## Performance and data protection

| Rule | Act when | How | Risk |
|------|----------|-----|------|
| `payload-attachment` | always on productive paths | log payloads only on errors or behind a switch (parameter or log level) | low |
| `logging-in-loop` | always | collect, write one attachment after the loop | low |
| `body-as-string` | large or growing payloads | stream (`getBody(java.io.Reader)`), or use standard steps | medium |
| `println` | always | remove, or use the message processing log | none |
| `odata-query-without-paging` | the entity set can grow (master data, documents) | switch on paging (Process in Pages) with a page size inside a Looping Process Call, or `$top` when only the first records are needed | low; with paging the flow handles each page, check aggregation after the loop |
| `odata-single-calls-in-loop` | many records per message | collect the records, send one `$batch` request (one changeset per record, or per group that must succeed together) | medium: error handling changes (partial failures per changeset); test with a failing record |

## Configuration and security

| Rule | Act when | How | Risk |
|------|----------|-----|------|
| `hardcoded-secret` | **always, first** | credential or secure parameter, read with the SecureStoreService; rotate the leaked value | the value is in Git history: rotate it |
| `hardcoded-endpoint` | always | `{{parameter}}` per environment, or Partner Directory per partner | low |
| `hardcoded-url-in-script` | always | parameter or Partner Directory value passed in a property | low |
| `odata-hardcoded-query` | the value differs per landscape or partner | `{{parameter}}` in the query options, or `${property.x}` filled from the Partner Directory | low; set the parameter in every landscape |
| `idoc-sap-client-literal` | always | externalise host and client (`https://{{S4_Host}}/sap/bc/srt/idoc?sap-client={{S4_Client}}`) or the whole address | low; set the values per landscape before the next deploy |
| `idoc-control-record-literal` | the partners differ per landscape or partner | logical systems and ports from `{{parameters}}` or the Partner Directory, passed to the mapping as properties or parameters | medium: a wrong partner makes SAP reject or misroute the IDoc; test in every landscape |

## Hygiene (in passing)

| Rule | Act when | How | Risk |
|------|----------|-----|------|
| `outdated-component` | the flow is changed anyway | update the step version in the Web UI editor (it migrates the settings) | medium: newer versions change defaults; test |
| `script-doc` | the flow is changed anyway | one sentence at the top of the script: what the step does | none |
| `default-step-name` | the flow is changed anyway | name steps after what they do | none |
| `naming` | new flows; existing ones only with a migration | `iflow copy` to a new ID, switch, undeploy the old | high for existing flows |
