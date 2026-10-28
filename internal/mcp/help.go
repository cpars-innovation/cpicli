package mcp

import (
	"cmp"
	"context"
	"encoding/json"
	"github.com/cpars-innovation/cpicli/pkg/scope"
	"slices"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/internal/skills"
)

// CommandInfo describes a CLI command for help.
type CommandInfo struct {
	Command string `json:"command"`
	Short   string `json:"short"`
	Long    string `json:"long,omitempty"`
	Usage   string `json:"usage,omitempty"`
	Flags   string `json:"flags,omitempty"`
}

// HelpInfo is what the help tool reports: the tools of this server after
// filtering, and the CLI.
type HelpInfo struct {
	// Scope is the access scope of the server (nil: unrestricted).
	Scope   *scope.Scope
	Tools   []Tool
	Removed []string
	// Reasons say why each removed tool is not offered (optional).
	Reasons  map[string]string
	Mode     string
	ReadOnly bool
	Commands []CommandInfo
	// Loop is the server's loop policy.
	Loop LoopPolicy
	// MaskPatterns and RedactPayloads are the masking options of the server
	// (see Redactor).
	MaskPatterns   []string
	RedactPayloads bool
	// ServerInstructions are the server-specific parts of the instructions
	// (mode, loop policy), appended to help topic "instructions".
	ServerInstructions string
	// ActiveToolsets returns the toolsets enabled now (dynamic toolsets);
	// nil: the toolsets with at least one available tool.
	ActiveToolsets func() []string
	// BranchPolicy is the server's branch policy (nil: none).
	BranchPolicy *BranchPolicy
	// Listed reports whether a tool is in the client's tool list now (nil:
	// unknown): help then leaves out its input schema, which the client has.
	Listed func(name string) bool
}

// Workflow is a task with the skill and tools for it.
type Workflow struct {
	Task  string   `json:"task"`
	Skill string   `json:"skill,omitempty"`
	Tools []string `json:"tools"`
	// Unavailable are tools of the workflow this server does not offer.
	Unavailable []string `json:"unavailable,omitempty"`
	Docs        string   `json:"docs"`
}

const docsURL = "https://github.com/cpars-innovation/cpicli/blob/main/docs/"

var workflows = []Workflow{
	{Task: "Learn how the tenant builds flows and write .cpi/conventions.md", Skill: "cpi-discover",
		Tools: []string{"discover_tenant", "graph_search", "graph_neighbors", "list_resources", "get_resource", "get_parameters", "list_credentials"}, Docs: "plugin.md"},
	{Task: "Turn a request (brief) into a design in .cpi/plans", Skill: "cpi-plan",
		Tools: []string{"graph_search", "graph_neighbors", "list_packages", "list_credentials"}, Docs: "new-flows.md"},
	{Task: "Create a new flow from a template", Skill: "cpi-build",
		Tools: []string{"copy_iflow", "layout_iflow", "create_package", "upload_artifacts", "validate_artifact", "deploy"}, Docs: "new-flows.md"},
	{Task: "Change an existing flow and get it deployed", Skill: "cpi-build",
		Tools: []string{"loop_start", "drift", "download_artifact", "layout_iflow", "upload_artifacts", "validate_artifact", "deploy", "list_runtime_artifacts", "loop_end"}, Docs: "mcp.md"},
	{Task: "Test a flow (HTTP, ProcessDirect via harness, polling, timers) and trace failures", Skill: "cpi-test",
		Tools: []string{"list_tests", "run_tests", "send_test_message", "list_message_logs", "get_message_log", "get_trace_tree", "set_log_level", "get_message_trace", "get_message_content"}, Docs: "testing.md"},
	{Task: "Review a flow against conventions and tenant checks", Skill: "cpi-review",
		Tools: []string{"validate_artifact", "check_guidelines", "graph_neighbors", "list_runtime_artifacts"}, Docs: "plugin.md"},
	{Task: "Find and apply improvements across flows: script collections, Partner Directory candidates, dead weight, best practices", Skill: "cpi-improve",
		Tools: []string{"lint", "lint_fix", "layout_iflow", "graph_neighbors", "drift", "bump_versions", "upload_artifacts", "validate_artifact", "deploy", "pd_dependencies", "pd_deploy"}, Docs: "lint.md"},
	{Task: "Compare tiers and prepare moving artifacts to the next tier (dependencies, pre-checks on the target)",
		Tools: []string{"compare", "transport_check", "drift", "list_runtime_artifacts"}, Docs: "transport.md"},
	{Task: "Find who calls a flow, or who uses a credential, script, header or PD parameter",
		Tools: []string{"graph_search", "graph_neighbors", "graph_path"}, Docs: "graph.md"},
	{Task: "Diagnose failed messages and runtime errors, follow a business key, handle JMS queues", Skill: "cpi-operate",
		Tools: []string{"message_summary", "list_runtime_artifacts", "find_messages_by_header", "get_message_log", "get_message_content", "get_trace_tree", "list_jms_messages", "retry_jms_messages", "list_log_files", "list_integration_connections"}, Docs: "monitoring.md"},
	{Task: "Change parameters or Partner Directory values",
		Tools: []string{"get_parameters", "set_parameters", "config_diff", "pd_dependencies", "pd_diff", "pd_deploy"}, Docs: "partner-directory.md"},
}

// toolsetsFor returns every toolset with the tools of it that are
// available, so that a client can select tools by task itself, and the
// members this server does not offer with the reason (notOffered), so an
// agent sees what exists and how to get it. "other" holds the available
// tools in no toolset (help excluded). Each toolset carries the effect and
// class of its available tools.
func toolsetsFor(available map[string]Tool, reasons map[string]string) []map[string]any {
	out := []map[string]any{}
	inSome := map[string]bool{"help": true}
	entry := func(name, description string, tools []string) map[string]any {
		effects := map[string]any{}
		for _, t := range tools {
			effects[t] = map[string]any{"effect": EffectOf(t), "class": ClassOf(t)}
		}
		return map[string]any{"name": name, "description": description, "tools": tools, "toolEffects": effects}
	}
	for _, n := range ToolsetNames() {
		tools := []string{}
		notOffered := map[string]string{}
		for _, t := range Toolsets[n] {
			if _, ok := available[t]; ok {
				tools = append(tools, t)
				inSome[t] = true
			} else {
				notOffered[t] = cmp.Or(reasons[t], "not offered by this server")
			}
		}
		e := entry(n, ToolsetDescriptions[n], tools)
		if len(notOffered) > 0 {
			e["notOffered"] = notOffered
		}
		out = append(out, e)
	}
	var other []string
	for name := range available {
		if !inSome[name] {
			other = append(other, name)
		}
	}
	if len(other) > 0 {
		slices.Sort(other)
		out = append(out, entry(OtherToolset, "tools in no other toolset", other))
	}
	return out
}

// HelpTool describes what this server, the skills and the CLI offer. It is
// added after filtering, so it reports the tools that are really available.
func HelpTool(info HelpInfo) Tool {
	available := map[string]Tool{}
	for _, t := range info.Tools {
		available[t.Name] = t
	}
	available["help"] = Tool{Name: "help"}
	readOnly := func() bool {
		// the effective state: no available tool changes the tenant (by
		// --read-only, the mode or the tool filters)
		return info.ReadOnly || !slices.ContainsFunc(info.Tools, func(t Tool) bool { return EffectOf(t.Name) == EffectTenant })
	}
	mode := cmp.Or(info.Mode, "none")
	server := func() map[string]any {
		server := map[string]any{"mode": mode, "readOnly": readOnly(), "tools": len(info.Tools) + 1, "disabledTools": nonNil(info.Removed),
			"loop": map[string]any{"required": info.Loop.Required, "caps": map[string]any{"maxIterations": info.Loop.Caps.MaxIterations,
				"sameErrorLimit": info.Loop.Caps.SameErrorLimit, "wallClockMinutes": info.Loop.Caps.WallClockMinutes, "maxDeploys": info.Loop.Caps.MaxDeploys}},
			"masking": map[string]any{"maskPatterns": nonNil(info.MaskPatterns), "redactPayloads": info.RedactPayloads}}
		if sc := info.Scope; sc != nil {
			server["scope"] = map[string]any{"active": true, "sha256": sc.Hash(), "scope": sc}
		} else {
			server["scope"] = map[string]any{"active": false}
		}
		if p := info.BranchPolicy; p != nil {
			server["branchPolicy"] = map[string]any{"file": p.File, "readonlyBranches": p.ReadonlyBranches, "now": p.Decide()}
		}
		return server
	}
	tools := func() []map[string]any {
		tools := []map[string]any{}
		for _, t := range info.Tools {
			tools = append(tools, map[string]any{"name": t.Name, "title": t.Title, "effect": EffectOf(t.Name), "class": ClassOf(t.Name)})
		}
		return tools
	}
	// index is help without topic: what an agent needs to pick a tool, skill
	// or topic, in a few hundred tokens; the details are topics
	index := func() map[string]any {
		srv := map[string]any{"mode": mode, "readOnly": readOnly(), "tools": len(info.Tools) + 1, "scope": info.Scope != nil} // + help
		if len(info.Removed) > 0 {
			srv["disabledTools"] = len(info.Removed)
		}
		if info.Loop.Required {
			srv["loopRequired"] = true
		}
		sets := map[string]string{}
		for _, set := range toolsetsFor(available, info.Reasons) {
			if t := set["tools"].([]string); len(t) > 0 {
				sets[set["name"].(string)] = strings.Join(t, " ")
			}
		}
		var flows []string
		for _, w := range workflowsFor(available) {
			line := w.Task + ": " + strings.Join(w.Tools, " ")
			if w.Skill != "" {
				line += " (skill " + w.Skill + ")"
			}
			if len(w.Unavailable) > 0 {
				line += " (not offered: " + strings.Join(w.Unavailable, " ") + ")"
			}
			flows = append(flows, line)
		}
		sk := map[string]string{}
		for _, s := range skills.List() {
			sk[s.Name] = firstSentence(s.Description)
		}
		return map[string]any{"server": srv, "toolsets": sets, "workflows": flows, "skills": sk,
			"more": `help {"topic": X}: X is a tool name (description and input schema), a skill name (its SKILL.md), ` +
				`"<skill>/<file>", a CLI command such as "iflow copy", or "server", "tools", "toolsets", "workflows", "skills", "agents", "cli"; ` +
				`{"topic": "instructions", "toolsets": [...]} gives the server instructions for those toolsets. Docs: ` + docsURL}
	}
	return Tool{
		Name: "help", Title: "What this server, the skills and the CLI can do",
		Description: "Index of the available tools by toolset, the tasks with their tools and skill, and the cpi skills. With topic: one tool's " +
			"description and input schema, a skill's instructions or file, a CLI command, or a full section (server, tools, toolsets, workflows, cli); " +
			"topic \"instructions\" with toolsets returns the server instructions for those toolsets. Use it when unsure which tool or skill fits, or to read a skill in a client that does not load skills. Reads nothing remote.",
		InputSchema: object(props{
			"topic":    str(`Optional: a tool name, a skill name, "<skill>/<file>", a CLI command ("iflow copy"), or "server", "tools", "toolsets", "workflows", "skills", "agents", "cli", "instructions"`),
			"toolsets": strArray(`With topic "instructions": the toolsets to describe (default: the active toolsets of this server)`),
		}),
		Annotations: map[string]any{"readOnlyHint": true, "openWorldHint": false},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a struct {
				Topic    string   `json:"topic"`
				Toolsets []string `json:"toolsets"`
			}
			if err := decode(raw, &a); err != nil {
				return nil, err
			}
			topic := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(a.Topic), "cpictl "))
			switch topic {
			case "":
				return index(), nil
			case "server":
				return map[string]any{"server": server()}, nil
			case "tools":
				return map[string]any{"tools": tools()}, nil
			case "toolsets":
				return map[string]any{"toolsets": toolsetsFor(available, info.Reasons)}, nil
			case "workflows":
				return map[string]any{"workflows": workflowsFor(available)}, nil
			case "skills":
				return map[string]any{"skills": skills.List()}, nil
			case "agents":
				return map[string]any{"agents": skills.Agents()}, nil
			case "cli":
				return map[string]any{"cli": commandSummaries(info.Commands), "reference": docsURL + "commands.md"}, nil
			case "instructions":
				return instructionsFor(info, available, a.Toolsets)
			}
			if t, ok := available[topic]; ok && t.Name != "help" {
				res := map[string]any{"tool": t.Name, "title": t.Title, "available": true, "effect": EffectOf(t.Name), "class": ClassOf(t.Name),
					"description": t.Description, "inputSchema": t.InputSchema}
				if info.Listed != nil && info.Listed(t.Name) {
					res["inputSchema"] = "in your tool list"
				}
				return res, nil
			}
			if slices.Contains(info.Removed, topic) {
				reason := "disabled on this server by its mode or tool filter"
				if r := info.Reasons[topic]; r != "" {
					reason = r
				}
				return map[string]any{"tool": topic, "available": false, "effect": EffectOf(topic), "class": ClassOf(topic),
					"reason": reason + "; ask the user, or use another server"}, nil
			}
			if name, file, ok := strings.Cut(topic, "/"); ok {
				if _, isSkill := skills.Find(name); isSkill {
					text, err := skills.Read(name, file)
					if err != nil {
						return nil, err
					}
					return map[string]any{"skill": name, "file": file, "content": text}, nil
				}
			}
			if s, ok := skills.Find(topic); ok {
				text, err := skills.Read(s.Name, "")
				if err != nil {
					return nil, err
				}
				return map[string]any{"skill": s.Name, "description": s.Description, "files": s.Files, "content": text,
					"note": "reference files: help {topic: \"" + s.Name + "/<file>\"}"}, nil
			}
			for _, c := range info.Commands {
				if c.Command == topic {
					return c, nil
				}
			}
			return nil, output.Usagef("no tool, skill or command %q%s", topic, suggest(topic, available, info.Commands))
		},
	}
}

// instructionsFor is help topic "instructions": the server instructions for
// the toolsets (default: the active ones).
func instructionsFor(info HelpInfo, available map[string]Tool, toolsets []string) (any, error) {
	source := "requested"
	if len(toolsets) == 0 {
		source = "active"
		if info.ActiveToolsets != nil {
			toolsets = info.ActiveToolsets()
		} else {
			for _, set := range toolsetsFor(available, info.Reasons) {
				// doctor is part of every toolset, not a reason for "other"
				tools := set["tools"].([]string)
				if len(tools) > 0 && (set["name"] != OtherToolset || slices.ContainsFunc(tools, func(t string) bool { return t != "doctor" })) {
					toolsets = append(toolsets, set["name"].(string))
				}
			}
		}
	}
	for i, n := range toolsets {
		toolsets[i] = strings.TrimSpace(n)
		if _, ok := Toolsets[toolsets[i]]; !ok && toolsets[i] != OtherToolset {
			return nil, output.Usagef("invalid toolset %q (%s, %s)", n, strings.Join(ToolsetNames(), ", "), OtherToolset)
		}
	}
	return map[string]any{"toolsets": nonNil(toolsets), "toolsetsSource": source,
		"instructions": ToolsetInstructions(toolsets) + info.ServerInstructions}, nil
}

func workflowsFor(available map[string]Tool) []Workflow {
	res := make([]Workflow, 0, len(workflows))
	for _, w := range workflows {
		w.Docs = docsURL + w.Docs
		var tools, missing []string
		for _, t := range w.Tools {
			if _, ok := available[t]; ok {
				tools = append(tools, t)
			} else {
				missing = append(missing, t)
			}
		}
		w.Tools, w.Unavailable = nonNil(tools), missing
		res = append(res, w)
	}
	return res
}

func commandSummaries(cmds []CommandInfo) []CommandInfo {
	res := make([]CommandInfo, 0, len(cmds))
	for _, c := range cmds {
		res = append(res, CommandInfo{Command: c.Command, Short: c.Short})
	}
	return res
}

func suggest(topic string, tools map[string]Tool, cmds []CommandInfo) string {
	q := strings.ToLower(topic)
	var hits []string
	for name := range tools {
		if strings.Contains(name, q) {
			hits = append(hits, name)
		}
	}
	for _, s := range skills.Names() {
		if strings.Contains(s, q) {
			hits = append(hits, s)
		}
	}
	for _, c := range cmds {
		if strings.Contains(c.Command, q) {
			hits = append(hits, c.Command)
		}
	}
	slices.Sort(hits)
	if len(hits) == 0 {
		return `; call help without topic for the index`
	}
	return "; did you mean: " + strings.Join(hits[:min(len(hits), 10)], ", ")
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// firstSentence is the text up to the first ". " (all of it if none).
func firstSentence(text string) string {
	if i := strings.Index(text, ". "); i >= 0 {
		return text[:i+1]
	}
	return text
}
