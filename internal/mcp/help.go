package mcp

import (
	"context"
	"encoding/json"
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
		Tools: []string{"copy_iflow", "layout_iflow", "create_package", "upload_artifact", "validate_artifact", "deploy"}, Docs: "new-flows.md"},
	{Task: "Change an existing flow and get it deployed", Skill: "cpi-build",
		Tools: []string{"loop_start", "drift", "download_artifact", "layout_iflow", "upload_artifact", "validate_artifact", "deploy", "get_runtime_status", "loop_end"}, Docs: "mcp.md"},
	{Task: "Test a flow (HTTP, ProcessDirect via harness, polling, timers) and trace failures", Skill: "cpi-test",
		Tools: []string{"send_test_message", "list_message_logs", "get_message_steps", "get_trace_tree", "set_log_level", "get_message_trace", "get_trace_message"}, Docs: "testing.md"},
	{Task: "Review a flow against conventions and tenant checks", Skill: "cpi-review",
		Tools: []string{"validate_artifact", "check_guidelines", "graph_neighbors", "get_runtime_status"}, Docs: "plugin.md"},
	{Task: "Find and apply improvements across flows: script collections, Partner Directory candidates, dead weight, best practices", Skill: "cpi-improve",
		Tools: []string{"lint", "lint_fix", "layout_iflow", "graph_neighbors", "drift", "bump_versions", "upload_artifacts", "validate_artifact", "deploy", "pd_dependencies", "pd_deploy"}, Docs: "lint.md"},
	{Task: "Compare tiers and prepare moving artifacts to the next tier (dependencies, pre-checks on the target)",
		Tools: []string{"compare", "transport_check", "drift", "get_runtime_status"}, Docs: "transport.md"},
	{Task: "Find who calls a flow, or who uses a credential, script, header or PD parameter",
		Tools: []string{"graph_search", "graph_neighbors", "graph_path"}, Docs: "graph.md"},
	{Task: "Diagnose failed messages and runtime errors, follow a business key, handle JMS queues", Skill: "cpi-operate",
		Tools: []string{"message_summary", "list_runtime_artifacts", "find_messages_by_header", "get_message_log", "get_message_steps", "get_trace_tree", "list_jms_messages", "retry_jms_messages", "list_log_files", "list_integration_connections"}, Docs: "monitoring.md"},
	{Task: "Change parameters or Partner Directory values",
		Tools: []string{"get_parameters", "set_parameters", "config_diff", "pd_dependencies", "pd_diff", "pd_deploy"}, Docs: "partner-directory.md"},
}

// toolsetsFor returns the toolsets with the tools of each that are
// available (toolsets without any are left out), so that a client can
// select tools by task itself. "other" holds the available tools in no
// toolset (help excluded). Each toolset carries the effect and class of its
// tools.
func toolsetsFor(available map[string]Tool) []map[string]any {
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
		for _, t := range Toolsets[n] {
			if _, ok := available[t]; ok {
				tools = append(tools, t)
				inSome[t] = true
			}
		}
		if len(tools) > 0 {
			out = append(out, entry(n, ToolsetDescriptions[n], tools))
		}
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
	overview := func() map[string]any {
		tools := []map[string]any{}
		for _, t := range info.Tools {
			tools = append(tools, map[string]any{"name": t.Name, "title": t.Title, "effect": EffectOf(t.Name), "class": ClassOf(t.Name)})
		}
		mode := info.Mode
		if mode == "" {
			mode = "none"
		}
		// readOnly is the effective state: no available tool changes the
		// tenant (by --read-only, the mode or the tool filters)
		readOnly := info.ReadOnly || !slices.ContainsFunc(info.Tools, func(t Tool) bool { return EffectOf(t.Name) == EffectTenant })
		server := map[string]any{"mode": mode, "readOnly": readOnly, "tools": len(info.Tools), "disabledTools": nonNil(info.Removed),
			"toolsets": toolsetsFor(available),
			"loop": map[string]any{"required": info.Loop.Required, "caps": map[string]any{"maxIterations": info.Loop.Caps.MaxIterations,
				"sameErrorLimit": info.Loop.Caps.SameErrorLimit, "wallClockMinutes": info.Loop.Caps.WallClockMinutes, "maxDeploys": info.Loop.Caps.MaxDeploys}},
			"masking": map[string]any{"maskPatterns": nonNil(info.MaskPatterns), "redactPayloads": info.RedactPayloads}}
		return map[string]any{
			"server":    server,
			"workflows": workflowsFor(available),
			"tools":     tools,
			"toolsets":  toolsetsFor(available),
			"skills":    skills.List(),
			"agents":    skills.Agents(),
			"cli":       commandSummaries(info.Commands),
			"topics": `help {"topic": X}: X is a tool name (description and input schema), a skill name (its SKILL.md), ` +
				`"<skill>/<file>" (a reference file of a skill), a CLI command such as "iflow copy" (usage and flags), ` +
				`or "tools", "toolsets", "skills", "cli", "workflows"`,
			"docs": docsURL,
		}
	}
	return Tool{
		Name: "help", Title: "What this server, the skills and the CLI can do",
		Description: "Overview of the available tools (after the server's mode and filters), the cpi skills (plan, build, test, review, discover) " +
			"with their instructions, the cpictl CLI commands, and which tools and skill to use for common tasks. With topic: details of one tool, " +
			"skill, skill file or CLI command. Use it when unsure which tool or skill fits, or to read a skill in a client that does not load skills. Reads nothing remote.",
		InputSchema: object(props{
			"topic": str(`Optional: a tool name, a skill name, "<skill>/<file>", a CLI command ("iflow copy"), or "tools", "toolsets", "skills", "cli", "workflows"`),
		}),
		Annotations: map[string]any{"readOnlyHint": true, "openWorldHint": false},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a struct {
				Topic string `json:"topic"`
			}
			if err := decode(raw, &a); err != nil {
				return nil, err
			}
			topic := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(a.Topic), "cpictl "))
			ov := overview()
			switch topic {
			case "":
				return ov, nil
			case "tools", "toolsets", "skills", "workflows", "server":
				return map[string]any{topic: ov[topic]}, nil
			case "cli":
				return map[string]any{"cli": commandSummaries(info.Commands), "reference": docsURL + "commands.md"}, nil
			}
			if t, ok := available[topic]; ok && t.Name != "help" {
				return map[string]any{"tool": t.Name, "title": t.Title, "available": true, "effect": EffectOf(t.Name), "class": ClassOf(t.Name),
					"description": t.Description, "inputSchema": t.InputSchema}, nil
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
		return `; call help without topic for the overview`
	}
	return "; did you mean: " + strings.Join(hits[:min(len(hits), 10)], ", ")
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
