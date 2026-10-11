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
	"list_packages": EffectRead, "list_artifacts": EffectRead, "get_runtime_status": EffectRead,
	"list_message_logs": EffectRead, "get_message_log": EffectRead, "get_message_steps": EffectRead,
	"get_message_attachment": EffectRead, "get_message_store_entry": EffectRead,
	"get_message_trace": EffectRead, "get_trace_message": EffectRead,
	"list_runtime_artifacts": EffectRead, "list_service_endpoints": EffectRead,
	"validate_artifact": EffectRead, "check_guidelines": EffectRead,
	"list_resources": EffectRead, "get_resource": EffectRead,
	"list_credentials": EffectRead, "list_keystore": EffectRead, "get_parameters": EffectRead,
	"get_pd_parameters": EffectRead, "pd_diff": EffectRead, "get_trace_tree": EffectRead,
	"pd_dependencies": EffectRead, "config_diff": EffectRead,
	"list_data_stores": EffectRead, "list_data_store_entries": EffectRead, "get_data_store_entry": EffectRead,
	"delete_data_store_entry": EffectTenant, "list_locks": EffectRead, "unlock_artifact": EffectTenant, "list_variables": EffectRead, "get_variable": EffectRead,
	"list_jms_queues": EffectRead, "get_jms_broker": EffectRead, "list_number_ranges": EffectRead,
	"list_jms_messages": EffectRead, "find_messages_by_header": EffectRead,
	"retry_jms_messages": EffectTenant, "move_jms_messages": EffectTenant, "delete_jms_messages": EffectTenant, "set_jms_queue_state": EffectTenant,
	"list_log_files": EffectRead, "get_log_file": EffectRead, "list_idempotent_entries": EffectRead, "list_id_mappings": EffectRead,
	"drift": EffectRead, "doctor": EffectRead, "lint": EffectRead, "compare": EffectRead, "message_summary": EffectRead, "transport_check": EffectRead,
	"graph_search": EffectRead, "help": EffectRead, "list_toolsets": EffectRead, "enable_toolset": EffectRead, "graph_neighbors": EffectRead, "graph_path": EffectRead,
	"loop_status": EffectRead, "loop_start": EffectLocal, "loop_end": EffectLocal,

	"download_artifact": EffectLocal, "discover_tenant": EffectLocal, "copy_iflow": EffectLocal, "bump_versions": EffectLocal, "lint_fix": EffectLocal, "layout_iflow": EffectLocal,

	"create_package": EffectTenant, "upload_artifact": EffectTenant, "upload_artifacts": EffectTenant, "set_parameters": EffectTenant,
	"deploy": EffectTenant, "undeploy": EffectTenant, "pd_deploy": EffectTenant,
	"send_test_message": EffectTenant, "set_log_level": EffectTenant,
}

// operateTenantTools are the tenant tools of mode operate: operations as in
// the Web UI's Monitor, no content changes.
var operateTenantTools = []string{"set_log_level", "retry_jms_messages", "move_jms_messages", "set_jms_queue_state"}

// optInTools are offered only when the user's --tools names them exactly
// (not by a pattern), whatever the mode: they can destroy someone else's work.
var optInTools = []string{"unlock_artifact", "delete_jms_messages"}

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
	modeFilter, err := ModeFilter(mode)
	if err != nil {
		return nil, nil, err
	}
	kept, removed, err = Filter(tools, user)
	if err != nil {
		return nil, nil, err
	}
	kept = slices.DeleteFunc(kept, func(t Tool) bool {
		if slices.Contains(optInTools, t.Name) && !slices.Contains(user.Allow, t.Name) {
			removed = append(removed, t.Name)
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
	return kept, append(removed, more...), nil
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
		return "\n\nMode: full. All tools without restrictions, including pd_deploy full_sync (which deletes remote parameters: run pd_diff first). undeploy and delete_data_store_entry still need confirm; unlock_artifact and delete_jms_messages only when --tools names them."
	}
	return ""
}

// Toolsets are named groups of tools for one kind of task. An agent works
// better with the tools of its task than with all of them; a session for a
// task gets its toolset(s) (--toolset), combined with --tools and the mode.
// doctor is in every toolset, help is always added.
var Toolsets = map[string][]string{
	"inspect": {"list_packages", "list_artifacts", "list_resources", "get_resource", "get_parameters", "list_runtime_artifacts",
		"get_runtime_status", "list_service_endpoints", "graph_search", "graph_neighbors", "graph_path", "discover_tenant", "drift", "compare"},
	"build": {"list_packages", "list_artifacts", "list_resources", "get_resource", "get_parameters", "set_parameters", "download_artifact",
		"copy_iflow", "layout_iflow", "create_package", "upload_artifact", "upload_artifacts", "validate_artifact", "check_guidelines",
		"deploy", "get_runtime_status", "drift", "bump_versions", "lint", "graph_search", "graph_neighbors", "list_credentials",
		"list_locks", "loop_start", "loop_status", "loop_end"},
	"test": {"send_test_message", "list_service_endpoints", "list_message_logs", "find_messages_by_header", "get_message_log", "get_message_steps",
		"get_message_attachment", "get_message_store_entry", "get_trace_tree", "set_log_level", "get_message_trace",
		"get_trace_message", "get_runtime_status", "loop_start", "loop_status", "loop_end"},
	"monitor": {"message_summary", "list_message_logs", "find_messages_by_header", "get_message_log", "get_message_steps", "get_message_attachment",
		"get_message_store_entry", "get_trace_tree", "get_message_trace", "get_trace_message", "set_log_level",
		"list_runtime_artifacts", "get_runtime_status", "list_log_files", "get_log_file", "list_data_stores",
		"list_data_store_entries", "get_data_store_entry", "list_variables", "get_variable", "list_jms_queues", "get_jms_broker",
		"list_jms_messages", "retry_jms_messages", "move_jms_messages", "set_jms_queue_state", "list_number_ranges", "list_idempotent_entries", "list_id_mappings", "list_keystore", "graph_neighbors"},
	"promote": {"compare", "transport_check", "drift", "list_artifacts", "get_parameters", "config_diff", "list_credentials",
		"list_keystore", "pd_dependencies", "pd_diff", "get_runtime_status", "list_runtime_artifacts", "graph_neighbors"},
	"improve": {"lint", "lint_fix", "layout_iflow", "graph_search", "graph_neighbors", "graph_path", "drift", "compare",
		"bump_versions", "list_artifacts", "get_resource"},
	"partner-directory": {"get_pd_parameters", "pd_diff", "pd_dependencies", "pd_deploy", "config_diff", "graph_search"},
	"security":          {"list_credentials", "list_keystore"},
}

// ToolsetDescriptions say what each toolset is for.
var ToolsetDescriptions = map[string]string{
	"inspect":           "understand content: packages, artifacts, resources, parameters, runtime status, endpoints, graph, discovery, drift, compare",
	"build":             "change flows on a development tenant: download, copy, layout, upload, validate, guidelines, parameters, deploy, lint, loop tools",
	"test":              "send test messages and diagnose them: message logs, steps, attachments, traces, log level, loop tools",
	"monitor":           "operations: message summary, message logs and traces, header search, runtime status, log files, data stores, variables, JMS queues and messages (retry, move), number ranges, keystore",
	"promote":           "move content between tiers: compare, transport_check, drift, config_diff, credentials, keystore, Partner Directory dependencies",
	"improve":           "quality: lint, lint_fix, layout_iflow, graph, drift, compare, bump_versions",
	"partner-directory": "Partner Directory parameters: read, diff, dependencies, deploy",
	"security":          "security material (read): credentials and keystore entries",
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
