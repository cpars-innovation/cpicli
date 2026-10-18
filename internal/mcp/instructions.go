package mcp

import (
	"slices"
	"strings"
)

// instructionFragment is a part of the server instructions. A fragment
// without toolsets is general and always included; the others belong to the
// tasks of their toolsets (OtherToolset: tools in no toolset, such as
// undeploy), so that a client that enables only some toolsets can get
// instructions for those (help topic "instructions").
type instructionFragment struct {
	toolsets []string
	text     string
}

// instructionFragments are the server instructions, in order.
var instructionFragments = []instructionFragment{
	{nil, `Tools for SAP Cloud Integration (CPI) on one tenant. Unsure which tool or
skill fits a task? Call help (overview, or a topic: a tool, a skill and its instructions, a CLI
command; "instructions" with toolsets gives these instructions for those toolsets only). Auth or
403 errors: doctor shows which API areas the credentials can use.`},
	{[]string{"build", "test"}, `Build loop: drift (local vs tenant: never overwrite tenant-only edits; compare shows per
artifact what differs between the tenant, a content tree and Git refs, with diffs) -> download_artifact
(existing flow, once), or copy_iflow (new flow from a template: new ID, name, sender addresses);
before a pull request bump_versions changed=true raises Bundle-Version of changed artifacts
-> edit the files locally -> layout_iflow (tidy the diagram after
editing an .iflw; never place shapes by hand) -> create_package (new
package) -> upload_artifact (upload_artifacts for several, in parallel) -> validate_artifact -> deploy -> send_test_message (wait_seconds=60)
-> on failure get_trace_tree (traceId of the result: the call tree across flows and firstFailure),
get_message_steps (failing step) and get_message_log / get_message_attachment /
get_message_store_entry for payloads; for step-by-step payloads set_log_level TRACE, send again,
get_message_trace and get_trace_message -> fix the local files and repeat.`},
	{[]string{"test"}, `Tests: write cases as .cpi/tests/<FlowId>/<case>.yaml (request + expect, see the cpi-test skill) and run
them with run_tests (flow, case, tags; apply_config sets the flow's config.yaml for the run and restores it);
list_tests shows them. A failed case has its failures (expected vs actual) and diagnostics (failing step,
error, message path); send_test_message is for exploring a single message by hand.
Script cases (.cpi/tests/<FlowId>/scripts/<script>/<case>.yaml, .cpi/tests/collections/<Id>/<case>.yaml)
test one script function locally on the SAP API stand-ins: run_script_cases (no tenant needed);
run_script_cases_online runs the same cases on a development tenant's runtime (deploys a temporary test flow).`},
	{[]string{"test"}, `Flows without an HTTP sender: ProcessDirect via send_test_message process_direct_address (test
harness flow); timer, SFTP and other polling flows: see the triggers in discover_tenant and use
list_message_logs with since and wait_seconds after triggering them. send_test_message
triggers real processing, including receiver calls. IDoc senders: send_test_message idoc (IDoc
XML with an EDI_DC40 control record; idoc_template writes a skeleton to fill with the segments).
OData senders: send_test_message odata_method, entity_set, key, query. Test tooling on a
development tenant: install_test_tools (deploy=true) installs the harness and the receiver stub
(HTTPS /cpictl/stub/*: point a receiver of the flow under test at it, each request is recorded in the
stub's message log, attachment cpictl-stub-request); set_stub_response sets its answer per rule
(status, body: simulate receiver errors).`},
	{[]string{"build", "promote", "partner-directory"}, `Configuration: get_parameters / set_parameters, then deploy to activate; config_diff compares a
configure file with the tenant.`},
	{[]string{"build"}, `Review: check_guidelines (tenant design guidelines) before a release.`},
	{[]string{"promote"}, `Promote: on the target tier's server, transport_check lists what the artifacts need and checks the
target (drafts, outside changes, dependencies, credentials, Partner Directory, parameter values)
before anything is uploaded; compare shows the differences between tiers.`},
	{[]string{"improve"}, `Improve: lint checks local flows for reuse (scripts and mappings in several flows -> script
collections), Partner Directory candidates (routing tables, lookup tables, deployment copies),
dead weight, robustness and best practices; lint_fix moves scripts into collections and removes
unused scripts and unconnected steps (local files; the cpi-improve skill plans and applies them).`},
	{[]string{"inspect", "build"}, `Inspect: list_packages, list_artifacts (cached briefly; refresh=true re-reads), list_resources,
get_resource (read without download). get_parameters artifact_ids reads several flows at once.`},
	{[]string{"monitor"}, `Operate: message_summary (volume and failures per flow, connection and error fingerprint over a
window: start here for "what is failing"), list_runtime_artifacts statuses=["ERROR"], get_runtime_status (Cloud Integration only:
list_runtimes shows integration cells and what runs there), list_message_logs,
list_service_endpoints; runtime data: list_data_stores, list_data_store_entries,
get_data_store_entry, delete_data_store_entry (confirm), list_variables, get_variable,
list_jms_queues, get_jms_broker, list_number_ranges, list_log_files / get_log_file (adapter errors
without a message log), list_idempotent_entries (skipped duplicates), list_id_mappings.`},
	{[]string{"build", "improve"}, `Components: list_components gives the adapter and step variants (cmdVariantUri) and versions the
tenant offers (.cpi/components.json); use them when writing flow XML, a newer version fails the
tenant's Check (lint rule component-newer-than-tenant).`},
	{[]string{"monitor"}, `Tenant: list_integration_connections (which systems the deployed flows connect, as the tenant
computes it), get_tenant_settings (external logging, archiving), download_log_archive (zip of
the latest http or trace logs).`},
	{[]string{"monitor", "test"}, `find_messages_by_header finds messages by a business key the flows write (e.g. an order number).`},
	{[]string{"monitor"}, `JMS: list_jms_messages shows the messages of a queue (failed ones wait for a retry);
retry_jms_messages, move_jms_messages (confirm) and set_jms_queue_state act on them;
delete_jms_messages (confirm; offered only when the server's --opt-in or --tools names it) loses them for good.`},
	{[]string{"build"}, `Locks: list_locks shows artifacts open for editing (an update fails while someone holds the
lock). unlock_artifact (confirm; offered only when the server's --opt-in or --tools names it) discards the
lock holder's unsaved changes: only on the user's explicit request.`},
	{[]string{"inspect", "build"}, `Value mappings: list_value_mappings, get_value_mapping (schemas with value pairs),
set_value_mapping (confirm) adds or changes a pair; deploy with artifact_type ValueMapping to use it.`},
	{[]string{OtherToolset}, `Deleting: delete_artifact and delete_package (confirm; offered only when --opt-in or --tools names them)
cannot be undone and refuse deployed artifacts unless undeploy_first: only on the user's request.
undeploy requires confirm=true.`},
	{[]string{"inspect", "build"}, `Conventions: discover_tenant writes an inventory of existing flows (adapters, steps, error
handling, scripts, naming); follow the conventions of the repository (e.g. .cpi/conventions.md).`},
	{[]string{"inspect", "build", "improve", "promote", "partner-directory", "monitor"}, `Graph: after discovery, graph_search, graph_neighbors and graph_path answer which flows call a
flow (ProcessDirect/JMS), use a credential, script, header or Partner Directory parameter, or
call a system, from .cpi/graph.json (local, fast; rerun discover_tenant after changes). Before
writing a script, graph_search types ["script"] with words of its purpose: reusable=true scripts
are in script collections; call them instead of writing new code. Comment scripts as in
help {"topic": "script-doc"} (lint checks it).`},
	{[]string{"build", "test"}, `Autonomous loops: loop_start before changing anything, loop_status, loop_end when done or stopped.`},
	{nil, `Every result has {ok, errorCategory, exitCode, error, result}; errorCategory: usage (fix the
arguments), auth (stop, ask the user), tenant_http (retry later), failed (the tenant rejected the
content or the message failed: read error, fix), timeout (check status), partial (see items),
stopped (a loop limit was reached: stop changing things, call loop_end and report).`},
	{[]string{"partner-directory", "promote"}, `Partner Directory: pd_dependencies (which flows read a parameter), get_pd_parameters (tenant
values), pd_diff (local vs tenant), then pd_deploy keys=["PID:ID"] to change one parameter without
redeploying flows. pd_deploy is a dry run unless dry_run=false. list_pd_partners lists the partners
(parameter counts, content types); list_alternative_partners / list_authorized_users and
create_alternative_partner / create_authorized_user manage partner identification.`},
	{[]string{"security"}, `Security material: list_credentials (including OAuth2 authorization code credentials),
list_keystore, list_keystore_history, get_certificate_chain, get_ssh_public_key, list_pgp_keys,
list_access_policies, list_audit_logs (who changed what; status missing_role = no role, do not
retry). Certificates: generate_key_pair (the tenant creates the key) -> get_csr -> the user has
it signed -> upload_certificate_chain. create_access_policy / add_artifact_reference restrict
artifacts to a role. Secrets and private keys never pass through this server (credentials, SSH
key import and PGP keyrings: ask the user to run cpictl). The delete tools (delete_keystore_entry,
delete_access_policy, delete_artifact_reference, delete_alternative_partner,
delete_authorized_user) need confirm and are offered only when --opt-in or --tools names them.`},
}

// Instructions is sent to the client on initialize: all fragments.
var Instructions = joinFragments(func(instructionFragment) bool { return true })

// ToolsetInstructions returns the instructions for these toolsets only: the
// general fragments and those of the toolsets.
func ToolsetInstructions(toolsets []string) string {
	return joinFragments(func(f instructionFragment) bool {
		return f.toolsets == nil || slices.ContainsFunc(f.toolsets, func(t string) bool { return slices.Contains(toolsets, t) })
	})
}

func joinFragments(keep func(instructionFragment) bool) string {
	var parts []string
	for _, f := range instructionFragments {
		if keep(f) {
			parts = append(parts, f.text)
		}
	}
	return strings.Join(parts, "\n\n")
}
