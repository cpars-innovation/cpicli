package mcp

// toolSummaries are the descriptions tools/list sends for tools with a long
// description: what the tool does, what it needs, its side effects and the
// next step. help {"topic": "<tool>"} returns the full description, so
// nothing is lost; the listing that is sent on every turn stays small.
var toolSummaries = map[string]string{
	"graph_search": "Find nodes in the content graph discover_tenant wrote: flows, packages, endpoints, systems, credentials, scripts, " +
		"headers, properties, custom headers, PD parameters, OData entities, IDoc types, components (adapter/step versions), mappings, " +
		"value mappings. Matches key, name and attributes (all words), best first. Scripts show reusable (script collection: call it, " +
		"don't copy), functions, reads/writes, usages and shape (same shape = near-duplicate). Local file only.",
	"get_trace_tree": "The call tree of a message's run across flows, with the hops, a pathKey (same route = same key) and firstFailure. " +
		"Give trace_id (send_test_message's traceId) or message_guid (any message; key_headers join runs that left the tenant and came " +
		"back). Next: get_message_log with include_steps for firstFailure.messageGuid.",
	"lint": "Check the flows of a local content tree (<package>/<artifact>) for reuse, Partner Directory candidates, dead weight, " +
		"simplification, robustness, performance, configuration and hygiene; findings with rule, severity, file, step and suggestion. " +
		"Settings .cpi/lint.yaml, baseline .cpi/lint-baseline.json. Local files only; fixes: lint_fix.",
	"copy_iflow": "Copy a flow (tenant from_artifact_id or local from_dir) into target_dir under a new ID and name: manifest, metainfo, " +
		".iflw file name, .project and sender addresses. Every sender address needs a new value in addresses ([\"NEW\"] for one, else " +
		"\"OLD=NEW\") unless keep_addresses: the same address fails the deployment. Check the files the result lists as still naming " +
		"the source ID. Local files; next upload_artifacts, validate_artifact, deploy.",
	"run_tests": "Run the test cases of .cpi/tests against the deployed flows: send, wait for the log, check the expectations; " +
		"per case the failures (expected/actual) and diagnostics (messageGuid, lastError, failedStep). apply_config points receivers " +
		"at the stub for the run and restores the values. Real processing: development tenant only.",
	"transport_check": "Before moving artifacts to this tenant (the target tier): their dependencies and pre-checks (draft on the " +
		"target, changes outside the pipeline, missing dependencies, credentials, key aliases, expiring certificates, PD parameters, " +
		"configuration), each pass/warn/fail/skip. Read-only.",
	"graph_neighbors": "The edges and nodes around a node of the content graph: who calls a flow (direction in, edge_types " +
		"[\"sends_to\"]), what a flow uses (out), which flows use a credential, script, header, PD parameter, OData entity or IDoc type " +
		"(that node, in), downstream chains (depth). Local file only.",
	"compare": "Compare two sides per artifact: tenant, a local content tree, or git:<ref>[:<path>]. Status same, only_a, only_b, " +
		"content_differs, version_differs, parameters_differ; diff adds unified diffs, model a per-element diff of changed .iflw files. " +
		"Read-only; downloads the tenant's artifacts (limit with packages / artifacts).",
	"run_script_cases": "Run script cases (.cpi/tests/<FlowId>/scripts/... and .cpi/tests/collections/...) locally with Gradle " +
		"(needs a JDK): call one script function with a message and check body, headers, properties, return value and exception. " +
		"Nothing is sent to a tenant.",
	"send_test_message": "Send a test message to an endpoint of a deployed flow (or process_direct_address via the test harness) " +
		"and return the HTTP status, response and message GUID; wait_seconds also returns the processing log. Real processing, " +
		"including receiver calls: test data on a development tenant. On failure: get_message_log with include_steps.",
	"run_script_cases_online": "Run run_script_cases' cases on the tenant's runtime: deploys a generated test flow per script " +
		"function in the test tooling package (about a minute), calls it per case, deletes it. Changes the tenant; refused on " +
		"production tiers. Cases with pd, value_mappings, secure, keystore, log or returns are skipped.",
	"lint_fix": "Apply lint's mechanical fixes to local files: shared scripts into a script collection, references to equal " +
		"collection scripts, delete unused scripts and unconnected steps (rules [\"noop-content-modifier\"] also empty content " +
		"modifiers). dry_run lists the changes. Then bump_versions, upload (collections first), validate, deploy, test.",
	"list_message_logs": "Message processing logs (newest first) by artifact, status, time or IDs, with the error text of failed " +
		"messages. wait_seconds waits until the messages are final (after send_test_message use its wait_seconds instead). " +
		"package_id (needs since) covers a package; custom_header {name, value} as find_messages_by_header.",
	"install_test_tools": "Upload (and with deploy, deploy) cpictl's test tooling: CPICTL_Test_Harness (reaches ProcessDirect " +
		"flows for send_test_message) and CPICTL_Test_Stub (a receiver stub that records requests and answers per set_stub_response " +
		"rule). Development tenants only; refused on production.",
	"layout_iflow": "Recompute the diagram of local .iflw files (steps, branches, subprocesses, lines; never the logic). Call it " +
		"after editing an .iflw, before upload_artifacts, instead of placing shapes by hand. mode tidy (default) or full; check only " +
		"reports.",
	"deploy": "Deploy designtime artifacts and wait: DEPLOYED, SKIPPED (draft on the tenant), FAILED (tenant error) or TIMEOUT per " +
		"artifact. An older designtime version than the running one is refused unless it was modified after that deployment or " +
		"allow_downgrade. Next: send_test_message.",
	"scan_secrets": "Report security material in the Partner Directory and flow configuration parameters (keys, tokens, " +
		"authorization values, URLs with credentials, password-like values). Never returns a value: kind, reason, fingerprint.",
	"message_summary": "Messages and failures of a time window: per flow by status and duration, per connection between flows, " +
		"failures grouped by error fingerprint with count, first/last and newest GUID. Start here for \"what is failing\"; then " +
		"get_trace_tree or get_message_log.",
	"discover_tenant": "Inventory packages and flows (adapters, steps, scripts, parameters, credentials, naming) into output_file " +
		"and graph.json (for graph_search / graph_neighbors / graph_path); returns the summary. Read-only; downloads every flow, " +
		"limit with package_ids. local_dir reads a local repository instead.",
	"bump_versions": "Raise Bundle-Version in the manifests of the artifacts below dir; changed=true only for artifacts changed " +
		"since the commit that last set their version. Before a pull request in repositories that version manifests. Local files; " +
		"dry_run reports only.",
	"help": "Index of the tools by toolset, tasks with their tools and skill, and the skills. topic: a tool (full description and " +
		"schema), a skill or skill file, a CLI command, a section, or \"instructions\". Reads nothing remote.",
	"upload_artifacts": "Create or update designtime artifacts from local directories, in parallel: CREATED, UPDATED, UNCHANGED, " +
		"or SKIPPED when the tenant's artifact is a draft (not overwritten). Packages must exist (create_package). dry_run only " +
		"compares. Next validate_artifact, deploy.",
	"drift": "Per artifact of a local content tree: local vs tenant designtime version and content, and the runtime version; state " +
		"in_sync, tenant_newer (download first or the tenant's change is lost), local_newer, diverged, not_on_tenant. Run it before " +
		"upload_artifacts. Read-only.",
	"pd_dependencies": "Find Partner Directory references in local content (.iflw pd: references, dynamic ones, getParameter in " +
		"scripts); unknownPids flags PIDs without a local folder. Before pd_deploy: every listed flow is affected. Local files only.",
	"set_stub_response": "Set what the receiver stub answers for <stubUrl>/<rule>/...: status, content type, body, headers " +
		"(default 200 {}); clear removes it. Rule IDs: <flowId>.<name>. Refused on production.",
	"loop_start": "Open a build/test/fix loop before changing the tenant; the server refuses further changes (errorCategory " +
		"stopped) at max_iterations, same_error_limit, wall_clock_minutes or max_deploys. Returns loop_id; finish with loop_end.",
}

// summarized returns the tools with the summary as description, for
// tools/list.
func summarized(tools []Tool) []Tool {
	for i, t := range tools {
		if s, ok := toolSummaries[t.Name]; ok {
			tools[i].Description = s
		}
	}
	return tools
}
