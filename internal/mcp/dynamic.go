package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/rs/zerolog/log"
)

// OtherToolset holds, in dynamic mode, the available tools that are in no
// toolset (such as undeploy), so that every tool the filters allow can be
// reached.
const OtherToolset = "other"

// dynamicCore are listed from the start in dynamic mode.
var dynamicCore = []string{"help", "doctor", "list_toolsets", "enable_toolset"}

// CoreTools are listed from the start when the server loads tools on demand
// (the default): finding your way, and reading the tenant and its messages.
// Tools that change the tenant come with their toolset (enable_toolset), and
// with it its instructions.
var CoreTools = append(slices.Clone(dynamicCore), "list_packages", "list_artifacts", "list_runtime_artifacts",
	"message_summary", "list_message_logs", "get_message_log", "get_trace_tree")

// dynamicState is the toolset state of a server in dynamic mode.
type dynamicState struct {
	mu      sync.Mutex
	sets    map[string][]string // toolset → available tools, in server order
	enabled map[string]bool     // enabled toolsets
	core    []string            // listed from the start
	// all: every tool is listed, for a client that loads tool schemas on
	// demand itself (see deferringClients)
	all bool
	// allInstructions are the instructions when all tools are listed
	allInstructions string
}

// deferringClients load the schemas of MCP tools only when the model needs
// one (Claude Code's tool search). Listing a few tools and enabling the rest
// would only add a round trip for them, so in on-demand mode they get every
// tool. Matched against clientInfo.name of initialize.
var deferringClients = map[string]bool{"claude-code": true}

// UseDynamicToolsets switches the server to dynamic toolsets: tools/list
// starts with help, doctor, list_toolsets and enable_toolset (plus the
// tools of the initial toolsets); enable_toolset adds the tools of a
// toolset and notifies the client (notifications/tools/list_changed). Only
// the tools the server already has (after mode and filters) can be
// enabled. Call before Serve.
func (s *Server) UseDynamicToolsets(initial []string) error {
	return s.useDynamic(initial, dynamicCore)
}

// UseOnDemandTools is UseDynamicToolsets with CoreTools listed from the
// start. A client that loads tool schemas on demand itself gets every tool
// and allInstructions instead.
func (s *Server) UseOnDemandTools(initial []string, allInstructions string) error {
	if err := s.useDynamic(initial, CoreTools); err != nil {
		return err
	}
	s.dynamic.allInstructions = allInstructions
	return nil
}

// clientInitialized switches an on-demand server to every tool for a
// client that defers tool schemas itself; it returns the instructions for
// the client.
func (s *Server) clientInitialized(client string) string {
	d := s.dynamic
	if d == nil || d.allInstructions == "" || !deferringClients[client] {
		return s.Instructions
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.all = true
	return d.allInstructions
}

func (s *Server) useDynamic(initial, core []string) error {
	available := map[string]bool{}
	for _, t := range s.tools {
		available[t.Name] = true
	}
	d := &dynamicState{sets: map[string][]string{}, enabled: map[string]bool{}, core: core}
	inSome := map[string]bool{}
	for name, tools := range Toolsets {
		for _, t := range s.tools {
			if slices.Contains(tools, t.Name) {
				d.sets[name] = append(d.sets[name], t.Name)
				inSome[t.Name] = true
			}
		}
	}
	for _, t := range s.tools {
		if !inSome[t.Name] && !slices.Contains(dynamicCore, t.Name) {
			d.sets[OtherToolset] = append(d.sets[OtherToolset], t.Name)
		}
	}
	for _, n := range initial {
		n = strings.TrimSpace(n)
		if _, ok := Toolsets[n]; !ok && n != OtherToolset {
			return output.Usagef("invalid toolset %q (%s)", n, strings.Join(ToolsetNames(), ", "))
		}
		d.enabled[n] = true
	}
	s.dynamic = d
	s.tools = append(s.tools, s.listToolsetsTool(), s.enableToolsetTool())
	return nil
}

// DynamicInstructions explains dynamic toolsets to the agent.
func DynamicInstructions() string {
	return toolsetInstructions("at the start only help, doctor, list_toolsets and enable_toolset are listed.")
}

// OnDemandInstructions explains the on-demand tool list to the agent.
func OnDemandInstructions() string {
	return toolsetInstructions("at the start only the core tools are listed (" + strings.Join(CoreTools, ", ") +
		"); every other tool, including those that change the tenant or send messages, comes with its toolset.")
}

func toolsetInstructions(start string) string {
	var b strings.Builder
	b.WriteString("\n\nTools on demand: " + start + " " +
		"Before working on a task, call enable_toolset with the toolset(s) it needs; their tools are then listed " +
		"(the client is notified) and the result carries the instructions for them. Enable only what the task needs. Toolsets:")
	for _, n := range ToolsetNames() {
		fmt.Fprintf(&b, "\n- %s: %s", n, ToolsetDescriptions[n])
	}
	fmt.Fprintf(&b, "\n- %s: tools in no other toolset, such as undeploy (if this server offers them)", OtherToolset)
	return b.String()
}

// listedTools returns the tools of tools/list.
func (s *Server) listedTools() []Tool {
	return summarized(s.offeredTools())
}

// offeredTools are the tools the server offers now.
func (s *Server) offeredTools() []Tool {
	if s.dynamic == nil {
		out := []Tool{}
		for _, t := range s.tools {
			if !s.hidden(t) {
				out = append(out, t)
			}
		}
		return out
	}
	s.dynamic.mu.Lock()
	defer s.dynamic.mu.Unlock()
	if s.dynamic.all {
		out := []Tool{}
		for _, t := range s.tools {
			if t.Name != "list_toolsets" && t.Name != "enable_toolset" && !s.hidden(t) {
				out = append(out, t)
			}
		}
		return out
	}
	listed := map[string]bool{}
	for _, n := range s.dynamic.core {
		listed[n] = true
	}
	for set := range s.dynamic.enabled {
		for _, t := range s.dynamic.sets[set] {
			listed[t] = true
		}
	}
	out := []Tool{}
	for _, t := range s.tools {
		if listed[t.Name] && !s.hidden(t) {
			out = append(out, t)
		}
	}
	return out
}

// IsListed reports whether a tool is in the tool list now.
func (s *Server) IsListed(name string) bool {
	return slices.ContainsFunc(s.listedTools(), func(t Tool) bool { return t.Name == name })
}

// EnabledToolsets returns the toolsets enabled now in dynamic mode, sorted
// (nil without dynamic toolsets).
func (s *Server) EnabledToolsets() []string {
	if s.dynamic == nil {
		return nil
	}
	s.dynamic.mu.Lock()
	defer s.dynamic.mu.Unlock()
	names := []string{}
	sets := s.dynamic.enabled
	if s.dynamic.all {
		sets = map[string]bool{}
		for n := range s.dynamic.sets {
			sets[n] = true
		}
	}
	for n, on := range sets {
		if on {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	return names
}

func (s *Server) toolsetNames() []string {
	names := make([]string, 0, len(s.dynamic.sets))
	for n := range s.dynamic.sets {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

func (s *Server) listToolsetsTool() Tool {
	return Tool{
		Name: "list_toolsets", Title: "List the toolsets",
		Description: "The toolsets of this server: what each is for, its tools (only those this server offers) and whether it is enabled. " +
			"Enable one with enable_toolset.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Annotations: map[string]any{"readOnlyHint": true},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			d := s.dynamic
			d.mu.Lock()
			defer d.mu.Unlock()
			out := []map[string]any{}
			for _, n := range s.toolsetNames() {
				desc := ToolsetDescriptions[n]
				if n == OtherToolset {
					desc = "tools in no other toolset"
				}
				out = append(out, map[string]any{"name": n, "description": desc, "tools": d.sets[n], "enabled": d.enabled[n]})
			}
			return map[string]any{"toolsets": out}, nil
		},
	}
}

func (s *Server) enableToolsetTool() Tool {
	return Tool{
		Name: "enable_toolset", Title: "Enable toolsets",
		Description: "Make the tools of one or more toolsets available (see list_toolsets). The tool list changes; " +
			"enable only what the current task needs.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"toolsets": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Toolset names"},
			},
			"required": []string{"toolsets"},
		},
		Annotations: map[string]any{"readOnlyHint": true, "idempotentHint": true},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				Toolsets []string `json:"toolsets"`
				Toolset  string   `json:"toolset"` // the name agents guess first
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, output.Usage(err)
			}
			if p.Toolset != "" {
				p.Toolsets = append(p.Toolsets, p.Toolset)
			}
			if len(p.Toolsets) == 0 {
				return nil, output.Usagef("toolsets is required (%s)", strings.Join(s.toolsetNames(), ", "))
			}
			d := s.dynamic
			before := map[string]bool{}
			for _, t := range s.listedTools() {
				before[t.Name] = true
			}
			d.mu.Lock()
			var was []string
			for n, on := range d.enabled {
				if on {
					was = append(was, n)
				}
			}
			for _, n := range p.Toolsets {
				if _, ok := d.sets[strings.TrimSpace(n)]; !ok {
					d.mu.Unlock()
					return nil, output.Usagef("unknown toolset %q (%s)", n, strings.Join(s.toolsetNames(), ", "))
				}
			}
			for i, n := range p.Toolsets {
				p.Toolsets[i] = strings.TrimSpace(n)
				d.enabled[p.Toolsets[i]] = true
			}
			d.mu.Unlock()
			added := []string{}
			for _, t := range s.listedTools() {
				if !before[t.Name] {
					added = append(added, t.Name)
				}
			}
			if len(added) > 0 {
				log.Info().Strs("toolsets", p.Toolsets).Msg("Toolsets enabled")
				s.notify("notifications/tools/list_changed")
			}
			res := map[string]any{"enabled": p.Toolsets, "addedTools": added}
			if text := addedToolsetInstructions(was, p.Toolsets); text != "" {
				res["instructions"] = text
			}
			return res, nil
		},
	}
}

// hidden: t is deprecated and the tool that replaces it is on this server
// (a filter that keeps only the old name keeps it listed).
func (s *Server) hidden(t Tool) bool {
	if t.Deprecated == "" {
		return false
	}
	replacement, _, _ := strings.Cut(t.Deprecated, " ")
	return slices.ContainsFunc(s.tools, func(o Tool) bool { return o.Name == replacement })
}
