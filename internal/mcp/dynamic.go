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

// dynamicState is the toolset state of a server in dynamic mode.
type dynamicState struct {
	mu      sync.Mutex
	sets    map[string][]string // toolset → available tools, in server order
	enabled map[string]bool     // enabled toolsets
}

// UseDynamicToolsets switches the server to dynamic toolsets: tools/list
// starts with help, doctor, list_toolsets and enable_toolset (plus the
// tools of the initial toolsets); enable_toolset adds the tools of a
// toolset and notifies the client (notifications/tools/list_changed). Only
// the tools the server already has (after mode and filters) can be
// enabled. Call before Serve.
func (s *Server) UseDynamicToolsets(initial []string) error {
	available := map[string]bool{}
	for _, t := range s.tools {
		available[t.Name] = true
	}
	d := &dynamicState{sets: map[string][]string{}, enabled: map[string]bool{}}
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
	var b strings.Builder
	b.WriteString("\n\nDynamic toolsets: at the start only help, doctor, list_toolsets and enable_toolset are listed. " +
		"Before working on a task, call enable_toolset with the toolset(s) it needs; their tools are then listed " +
		"(the client is notified). Enable only what the task needs. Toolsets:")
	for _, n := range ToolsetNames() {
		fmt.Fprintf(&b, "\n- %s: %s", n, ToolsetDescriptions[n])
	}
	fmt.Fprintf(&b, "\n- %s: tools in no other toolset, such as undeploy (if this server offers them)", OtherToolset)
	return b.String()
}

// listedTools returns the tools of tools/list.
func (s *Server) listedTools() []Tool {
	if s.dynamic == nil {
		return s.tools
	}
	s.dynamic.mu.Lock()
	defer s.dynamic.mu.Unlock()
	listed := map[string]bool{}
	for _, n := range dynamicCore {
		listed[n] = true
	}
	for set := range s.dynamic.enabled {
		for _, t := range s.dynamic.sets[set] {
			listed[t] = true
		}
	}
	out := []Tool{}
	for _, t := range s.tools {
		if listed[t.Name] {
			out = append(out, t)
		}
	}
	return out
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
	for n, on := range s.dynamic.enabled {
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
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, output.Usage(err)
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
			for _, n := range p.Toolsets {
				if _, ok := d.sets[strings.TrimSpace(n)]; !ok {
					d.mu.Unlock()
					return nil, output.Usagef("unknown toolset %q (%s)", n, strings.Join(s.toolsetNames(), ", "))
				}
			}
			for _, n := range p.Toolsets {
				d.enabled[strings.TrimSpace(n)] = true
			}
			d.mu.Unlock()
			added := []map[string]any{}
			for _, t := range s.listedTools() {
				if !before[t.Name] {
					added = append(added, map[string]any{"name": t.Name, "title": t.Title})
				}
			}
			if len(added) > 0 {
				log.Info().Strs("toolsets", p.Toolsets).Msg("Toolsets enabled")
				s.notify("notifications/tools/list_changed")
			}
			return map[string]any{"enabled": p.Toolsets, "addedTools": added}, nil
		},
	}
}
