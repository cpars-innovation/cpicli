package mcp

import (
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
)

// Effect is what a tool can change.
type Effect string

const (
	// EffectRead changes nothing.
	EffectRead Effect = "read"
	// EffectLocal writes files inside the server root only.
	EffectLocal Effect = "local"
	// EffectTenant changes the tenant or triggers processing on it.
	EffectTenant Effect = "tenant"
)

// toolEffects classifies every tool. TestEveryToolHasAnEffect fails for a
// tool missing here, so a new tool cannot slip into read-only servers.
var toolEffects = map[string]Effect{
	"list_packages": EffectRead, "list_artifacts": EffectRead,
	"list_message_logs": EffectRead, "get_message_log": EffectRead,
	"get_message_trace":      EffectRead,
	"list_runtime_artifacts": EffectRead, "list_service_endpoints": EffectRead,
	"validate_artifact": EffectRead, "check_guidelines": EffectRead,
	"list_resources": EffectRead, "get_resource": EffectRead,
	"list_credentials": EffectRead, "list_keystore": EffectRead, "get_parameters": EffectRead,
	"get_pd_parameters": EffectRead, "pd_diff": EffectRead, "get_trace_tree": EffectRead,
	"pd_dependencies": EffectRead, "config_diff": EffectRead,
	"list_data_stores": EffectRead, "list_data_store_entries": EffectRead, "get_data_store_entry": EffectRead,
	"delete_data_store_entry": EffectTenant, "list_locks": EffectRead, "list_runtimes": EffectRead, "list_value_mappings": EffectRead, "get_value_mapping": EffectRead, "set_value_mapping": EffectTenant,
	"delete_artifact": EffectTenant, "delete_package": EffectTenant, "list_components": EffectRead, "list_integration_connections": EffectRead, "get_tenant_settings": EffectRead, "scan_secrets": EffectRead, "download_log_archive": EffectLocal, "unlock_artifact": EffectTenant, "list_variables": EffectRead,
	"list_jms_queues": EffectRead, "get_jms_broker": EffectRead, "list_number_ranges": EffectRead,
	"list_jms_messages": EffectRead, "find_messages_by_header": EffectRead,
	"retry_jms_messages": EffectTenant, "move_jms_messages": EffectTenant, "delete_jms_messages": EffectTenant, "set_jms_queue_state": EffectTenant,
	"list_log_files": EffectRead, "get_log_file": EffectRead, "list_idempotent_entries": EffectRead, "list_id_mappings": EffectRead,
	"drift": EffectRead, "doctor": EffectRead, "lint": EffectRead, "compare": EffectRead, "message_summary": EffectRead, "transport_check": EffectRead,
	"graph_search": EffectRead, "list_tests": EffectRead, "help": EffectRead, "list_toolsets": EffectRead, "enable_toolset": EffectRead, "graph_neighbors": EffectRead, "graph_path": EffectRead,
	"loop_status": EffectRead, "loop_start": EffectLocal, "loop_end": EffectLocal,
	"list_keystore_history": EffectRead, "get_certificate_chain": EffectRead, "get_csr": EffectRead, "get_ssh_public_key": EffectRead,
	"list_pgp_keys": EffectRead, "list_access_policies": EffectRead, "list_audit_logs": EffectRead, "list_pd_partners": EffectRead,
	"list_alternative_partners": EffectRead, "list_authorized_users": EffectRead, "get_message_content": EffectRead,

	"download_artifact": EffectLocal, "discover_tenant": EffectLocal, "copy_iflow": EffectLocal, "bump_versions": EffectLocal, "lint_fix": EffectLocal, "layout_iflow": EffectLocal,
	"idoc_template": EffectLocal, "run_script_cases": EffectLocal,

	"create_package": EffectTenant, "upload_artifacts": EffectTenant, "set_parameters": EffectTenant,
	"deploy": EffectTenant, "undeploy": EffectTenant, "pd_deploy": EffectTenant,
	"send_test_message": EffectTenant, "run_tests": EffectTenant, "run_script_cases_online": EffectTenant, "set_log_level": EffectTenant,
	"install_test_tools": EffectTenant, "set_stub_response": EffectTenant,
	"generate_key_pair": EffectTenant, "upload_certificate_chain": EffectTenant, "delete_keystore_entry": EffectTenant,
	"create_access_policy": EffectTenant, "add_artifact_reference": EffectTenant, "delete_access_policy": EffectTenant,
	"delete_artifact_reference": EffectTenant, "create_alternative_partner": EffectTenant, "delete_alternative_partner": EffectTenant,
	"create_authorized_user": EffectTenant, "delete_authorized_user": EffectTenant,
}

// Class is a finer classification of what a tool does than Effect, for
// approval routing in clients: read and local as the effect; tenant tools
// are write, deploy, process, log or destructive.
type Class string

const (
	ClassRead  Class = "read"
	ClassLocal Class = "local"
	// ClassWrite changes designtime content or configuration (packages,
	// artifacts, parameters, value mappings, security material, partners).
	ClassWrite Class = "write"
	// ClassDeploy starts or stops runtime artifacts: deploy, undeploy, pd_deploy.
	ClassDeploy Class = "deploy"
	// ClassProcess triggers message processing: test messages, JMS retry,
	// move and queue state.
	ClassProcess Class = "process"
	// ClassLog changes the log level of a flow.
	ClassLog Class = "log"
	// ClassDestructive deletes or unlocks: what cannot be undone.
	ClassDestructive Class = "destructive"
)

// tenantClasses classifies every tenant tool. TestEveryToolHasAClass fails
// for a tenant tool missing here.
var tenantClasses = map[string]Class{
	"create_package": ClassWrite, "upload_artifacts": ClassWrite, "set_parameters": ClassWrite,
	"set_value_mapping": ClassWrite, "generate_key_pair": ClassWrite, "upload_certificate_chain": ClassWrite,
	"create_access_policy": ClassWrite, "add_artifact_reference": ClassWrite, "create_alternative_partner": ClassWrite,
	"create_authorized_user": ClassWrite, "set_stub_response": ClassWrite,

	"deploy": ClassDeploy, "undeploy": ClassDeploy, "pd_deploy": ClassDeploy, "install_test_tools": ClassDeploy, "run_script_cases_online": ClassDeploy,

	"send_test_message": ClassProcess, "run_tests": ClassProcess, "retry_jms_messages": ClassProcess, "move_jms_messages": ClassProcess, "set_jms_queue_state": ClassProcess,

	"set_log_level": ClassLog,

	"delete_data_store_entry": ClassDestructive, "delete_artifact": ClassDestructive, "delete_package": ClassDestructive,
	"delete_jms_messages": ClassDestructive, "delete_keystore_entry": ClassDestructive, "delete_access_policy": ClassDestructive,
	"delete_artifact_reference": ClassDestructive, "delete_alternative_partner": ClassDestructive, "delete_authorized_user": ClassDestructive,
	"unlock_artifact": ClassDestructive,
}

// ClassOf returns the class of a tool; unknown tenant tools count as
// destructive.
func ClassOf(name string) Class {
	switch EffectOf(name) {
	case EffectRead:
		return ClassRead
	case EffectLocal:
		return ClassLocal
	}
	if c, ok := tenantClasses[name]; ok {
		return c
	}
	return ClassDestructive
}

// operateTenantTools are the tenant tools of mode operate: operations as in
// the Web UI's Monitor, no content changes.
var operateTenantTools = []string{"set_log_level", "retry_jms_messages", "move_jms_messages", "set_jms_queue_state"}

// optInTools are offered only when the user's --opt-in or --tools names them exactly
// (not by a pattern), whatever the mode: they can destroy someone else's work
// or content that cannot be restored.
var optInTools = []string{"unlock_artifact", "delete_artifact", "delete_package", "delete_jms_messages",
	"delete_keystore_entry", "delete_access_policy", "delete_artifact_reference", "delete_alternative_partner", "delete_authorized_user"}

// ToolFilter limits the tools a server offers.
type ToolFilter struct {
	// ReadOnly drops every tool that changes the tenant or triggers
	// processing (local file writes stay available).
	ReadOnly bool
	// Allow keeps only matching tools (names or path.Match patterns such as
	// "list_*"); empty keeps all.
	Allow []string
	// Deny drops matching tools; it wins over Allow.
	Deny []string
	// OptIn offers these opt-in tools (exact names) in addition, without
	// limiting the others as Allow does. Deny, ReadOnly and the mode still
	// apply.
	OptIn []string
}

// Filter applies f and returns the remaining tools and the names of the
// removed ones. Patterns that match no tool are usage errors, so a typo never
// silently leaves a tool enabled.
func Filter(tools []Tool, f ToolFilter) (kept []Tool, removed []string, err error) {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	for _, p := range append(slices.Clone(f.Allow), f.Deny...) {
		if _, err := path.Match(p, ""); err != nil {
			return nil, nil, output.Usagef("invalid tool pattern %q", p)
		}
		if !slices.ContainsFunc(names, func(n string) bool { return match(p, n) }) {
			if by, ok := removedTools[p]; ok {
				return nil, nil, output.Usagef("tool %s was removed in cpictl 0.27.0: use %s", p, by)
			}
			return nil, nil, output.Usagef("tool pattern %q matches no tool (tools: %s)", p, strings.Join(names, ", "))
		}
	}
	for _, t := range tools {
		keep := (len(f.Allow) == 0 || slices.ContainsFunc(f.Allow, func(p string) bool { return match(p, t.Name) })) &&
			!slices.ContainsFunc(f.Deny, func(p string) bool { return match(p, t.Name) }) &&
			!(f.ReadOnly && EffectOf(t.Name) == EffectTenant)
		if keep {
			kept = append(kept, t)
		} else {
			removed = append(removed, t.Name)
		}
	}
	if len(kept) == 0 {
		return nil, nil, output.Usagef("the tool filter leaves no tools")
	}
	return kept, removed, nil
}

func match(pattern, name string) bool {
	ok, _ := path.Match(pattern, name)
	return ok
}

// EffectOf returns what a tool can change; unknown tools count as changing
// the tenant.
func EffectOf(name string) Effect {
	if e, ok := toolEffects[name]; ok {
		return e
	}
	return EffectTenant
}

// FilteredInstructions appends a note on removed tools to the instructions,
// so that the agent does not plan with tools it cannot call.
func FilteredInstructions(instructions string, f ToolFilter, removed []string) string {
	if len(removed) == 0 {
		return instructions
	}
	note := fmt.Sprintf("\n\nNot available on this server (disabled by configuration): %s.", strings.Join(removed, ", "))
	if f.ReadOnly {
		note += " This server is read-only: it cannot change the tenant or send messages; ask the user to make changes."
	}
	return instructions + note
}

// Modes are presets for what a server may do.
var Modes = []string{"discover", "operate", "develop", "full"}

// ModeFilter returns the tool filter of a mode ("" = no preset):
//   - discover: read-only (no tenant changes, no messages sent)
//   - operate: read tools, local files, set_log_level and JMS retry/move/queue state
//   - develop: all tools; pd_deploy refuses full_sync (see Config.DenyFullSync)
//   - full: all tools without restrictions (as without a mode)
func ModeFilter(mode string) (ToolFilter, error) {
	switch mode {
	case "", "develop", "full":
		return ToolFilter{}, nil
	case "discover":
		return ToolFilter{ReadOnly: true}, nil
	case "operate":
		var allow []string
		for name, e := range toolEffects {
			if e != EffectTenant || slices.Contains(operateTenantTools, name) {
				allow = append(allow, name)
			}
		}
		sort.Strings(allow)
		return ToolFilter{Allow: allow}, nil
	}
	return ToolFilter{}, output.Usagef("invalid mode %q (%s)", mode, strings.Join(Modes, ", "))
}

// ApplyFilters applies the user's filter, then the mode's: the most
// restrictive wins. User patterns are validated against all tools, so naming
// a tool the mode removes anyway is not an error.
func ApplyFilters(tools []Tool, mode string, user ToolFilter) (kept []Tool, removed []string, err error) {
	kept, reasons, err := ApplyFiltersWithReasons(tools, mode, user)
	for _, r := range reasons {
		removed = append(removed, r.Name)
	}
	return kept, removed, err
}

// RemovedTool is a tool the server does not offer, and why.
type RemovedTool struct {
	Name string `json:"name"`
	// Reason names what removed it: the mode, --read-only, --tools /
	// --toolset, --disable-tools, or the opt-in rule.
	Reason string `json:"reason"`
}

// ApplyFiltersWithReasons is ApplyFilters, with the reason for each removed
// tool (for the error result of a call to it).
func ApplyFiltersWithReasons(tools []Tool, mode string, user ToolFilter) (kept []Tool, removed []RemovedTool, err error) {
	modeFilter, err := ModeFilter(mode)
	if err != nil {
		return nil, nil, err
	}
	for _, n := range user.OptIn {
		if !slices.Contains(optInTools, n) {
			return nil, nil, output.Usagef("%q is not an opt-in tool (opt-in tools: %s)", n, strings.Join(optInTools, ", "))
		}
	}
	filter := user
	if len(user.Allow) > 0 {
		// an opt-in tool is offered next to the tools --tools selects
		filter.Allow = append(slices.Clone(user.Allow), user.OptIn...)
	}
	kept, names, err := Filter(tools, filter)
	if err != nil {
		return nil, nil, err
	}
	for _, n := range names {
		removed = append(removed, RemovedTool{Name: n, Reason: userFilterReason(n, user)})
	}
	kept = slices.DeleteFunc(kept, func(t Tool) bool {
		if slices.Contains(optInTools, t.Name) && !slices.Contains(user.Allow, t.Name) && !slices.Contains(user.OptIn, t.Name) {
			removed = append(removed, RemovedTool{Name: t.Name, Reason: "opt-in: offered only when --opt-in or --tools names it exactly (it can destroy content or someone else's work)"})
			return true
		}
		return false
	})
	// mode allow lists may name tools the user already removed
	if len(modeFilter.Allow) > 0 {
		var allow []string
		for _, t := range kept {
			if slices.Contains(modeFilter.Allow, t.Name) {
				allow = append(allow, t.Name)
			}
		}
		if len(allow) == 0 {
			return nil, nil, output.Usagef("the tool filter leaves no tools")
		}
		modeFilter.Allow = allow
	}
	kept, more, err := Filter(kept, modeFilter)
	if err != nil {
		return nil, nil, err
	}
	for _, n := range more {
		reason := "not offered in mode " + mode
		if modeFilter.ReadOnly {
			reason += " (read-only)"
		}
		removed = append(removed, RemovedTool{Name: n, Reason: reason})
	}
	return kept, removed, nil
}

// userFilterReason says which part of the user's filter removed a tool.
func userFilterReason(name string, f ToolFilter) string {
	switch {
	case slices.ContainsFunc(f.Deny, func(p string) bool { return match(p, name) }):
		return "disabled by --disable-tools"
	case f.ReadOnly && EffectOf(name) == EffectTenant:
		return "the server is read-only (--read-only) and the tool changes the tenant or triggers processing"
	}
	return "not selected by --tools / --toolset"
}

// ModeInstructions describes the mode for the server instructions.
func ModeInstructions(mode string) string {
	switch mode {
	case "discover":
		return "\n\nMode: discover. Read-only: inspect content, logs and configuration; no changes, no test messages."
	case "operate":
		return "\n\nMode: operate. Monitoring and diagnosis: read tools, set_log_level, retry_jms_messages, move_jms_messages and set_jms_queue_state; no content changes or deployments."
	case "develop":
		return "\n\nMode: develop. Build loop on a development tenant; pd_deploy full_sync is refused, undeploy needs confirm. Open a loop with loop_start before changing anything."
	case "full":
		return "\n\nMode: full. All tools without restrictions, including pd_deploy full_sync (which deletes remote parameters: run pd_diff first). undeploy, delete_data_store_entry and the other delete tools still need confirm; unlock_artifact and the delete tools of content, JMS messages, security material and partners only when --opt-in or --tools names them."
	}
	return ""
}

// Toolsets are named groups of tools for one kind of task. An agent works
// better with the tools of its task than with all of them; a session for a
// task gets its toolset(s) (--toolset), combined with --tools and the mode.
// doctor is in every toolset, help is always added.
var Toolsets = map[string][]string{
	"inspect": {
		"list_packages", "list_artifacts", "list_resources", "get_resource", "get_parameters", "list_runtime_artifacts",
		"list_service_endpoints", "graph_search", "graph_neighbors", "graph_path", "discover_tenant", "drift", "compare",
		"list_value_mappings", "get_value_mapping"},
	"build": {
		"list_packages", "list_artifacts", "list_resources", "get_resource", "get_parameters", "set_parameters", "download_artifact",
		"copy_iflow", "layout_iflow", "create_package", "upload_artifacts", "validate_artifact", "check_guidelines", "deploy",
		"list_runtime_artifacts", "list_runtimes", "drift", "bump_versions", "lint", "graph_search", "graph_neighbors",
		"list_credentials", "list_locks", "list_components", "list_value_mappings", "get_value_mapping", "set_value_mapping",
		"loop_start", "loop_status", "loop_end"},
	"test": {
		"list_tests", "run_tests", "run_script_cases", "run_script_cases_online", "send_test_message", "idoc_template",
		"list_service_endpoints", "list_message_logs", "find_messages_by_header", "get_message_log", "get_message_content",
		"get_trace_tree", "set_log_level", "get_message_trace", "list_runtime_artifacts", "install_test_tools", "set_stub_response",
		"loop_start", "loop_status", "loop_end"},
	"monitor": {
		"message_summary", "list_message_logs", "find_messages_by_header", "get_message_log", "get_message_content", "get_trace_tree",
		"get_message_trace", "set_log_level", "list_runtime_artifacts", "list_runtimes", "list_log_files", "get_log_file",
		"list_data_stores", "list_data_store_entries", "get_data_store_entry", "list_variables", "list_jms_queues", "get_jms_broker",
		"list_jms_messages", "retry_jms_messages", "move_jms_messages", "set_jms_queue_state", "list_number_ranges",
		"list_idempotent_entries", "list_id_mappings", "list_keystore", "list_audit_logs", "graph_neighbors",
		"list_integration_connections", "get_tenant_settings", "download_log_archive"},
	"promote": {
		"compare", "transport_check", "drift", "list_artifacts", "get_parameters", "config_diff", "list_credentials", "list_keystore",
		"pd_dependencies", "pd_diff", "list_runtime_artifacts", "graph_neighbors"},
	"improve": {
		"list_components", "lint", "lint_fix", "layout_iflow", "graph_search", "graph_neighbors", "graph_path", "drift", "compare",
		"bump_versions", "list_artifacts", "get_resource"},
	"partner-directory": {
		"list_pd_partners", "get_pd_parameters", "pd_diff", "pd_dependencies", "pd_deploy", "config_diff", "graph_search",
		"list_alternative_partners", "create_alternative_partner", "list_authorized_users", "create_authorized_user", "scan_secrets"},
	"security": {
		"list_credentials", "list_keystore", "list_keystore_history", "get_certificate_chain", "get_csr", "get_ssh_public_key",
		"generate_key_pair", "upload_certificate_chain", "list_pgp_keys", "list_access_policies", "create_access_policy",
		"add_artifact_reference", "list_audit_logs", "scan_secrets", "lint"},
}

// ToolsetDescriptions say what each toolset is for.
var ToolsetDescriptions = map[string]string{
	"inspect":           "understand content: packages, artifacts, resources, parameters, runtime status, endpoints, graph, discovery, drift, compare",
	"build":             "change flows on a development tenant: download, copy, layout, upload, validate, guidelines, parameters, deploy, lint, loop tools",
	"test":              "test cases (list_tests, run_tests), test messages and their diagnosis: message logs, steps, attachments, traces, log level, test tooling (harness, receiver stub), loop tools",
	"monitor":           "operations: message summary, message logs and traces, header search, runtime status, log files, data stores, variables, JMS queues and messages (retry, move), number ranges, keystore",
	"promote":           "move content between tiers: compare, transport_check, drift, config_diff, credentials, keystore, Partner Directory dependencies",
	"improve":           "quality: lint, lint_fix, layout_iflow, graph, drift, compare, bump_versions",
	"partner-directory": "Partner Directory: partners, parameters (read, diff, dependencies, deploy), alternative partners, authorized users",
	"security":          "security material: credentials, keystore (history, chains, CSRs, key pair generation), PGP keys, access policies, audit log",
}

// ToolsetNames lists the toolsets, sorted.
func ToolsetNames() []string {
	names := make([]string, 0, len(Toolsets))
	for n := range Toolsets {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// ToolsetTools expands toolset names to tool names (with doctor).
func ToolsetTools(names []string) ([]string, error) {
	out := []string{"doctor"}
	for _, n := range names {
		tools, ok := Toolsets[strings.TrimSpace(n)]
		if !ok {
			return nil, output.Usagef("invalid toolset %q (%s)", n, strings.Join(ToolsetNames(), ", "))
		}
		for _, t := range tools {
			if !slices.Contains(out, t) {
				out = append(out, t)
			}
		}
	}
	return out, nil
}
