package mcp

import (
	"context"
	"encoding/json"
	"maps"
	"slices"

	"github.com/cpars-innovation/cpicli/internal/output"
)

// removedTools are tools another tool covers (deprecated in 0.25.0, removed
// in 0.27.0): a call fails with a usage error naming the replacement. Only
// tools of the same effect were merged, so that approval per tool keeps
// working. Their implementations live on in the replacements.
var removedTools = map[string]string{
	"get_message_steps":       "get_message_log with include_steps",
	"get_message_attachment":  "get_message_content with attachment_id",
	"get_message_store_entry": "get_message_content with store_entry_id",
	"get_trace_message":       "get_message_trace with trace_id",
	"get_runtime_status":      "list_runtime_artifacts with artifact_ids",
	"upload_artifact":         "upload_artifacts (one or several artifacts)",
	"get_variable":            "list_variables with name",
}

// consolidate lets the tools that replace the removed ones do their work
// (the removed tools' handlers stay the implementation), then drops the
// removed tools.
func consolidate(tools []Tool) []Tool {
	idx := func(name string) int { return slices.IndexFunc(tools, func(t Tool) bool { return t.Name == name }) }
	handler := func(name string) func(context.Context, json.RawMessage) (any, error) {
		if i := idx(name); i >= 0 {
			return tools[i].Handler
		}
		return nil
	}
	// patch adds properties to a tool and routes calls that use them.
	patch := func(name, description string, add props, route func(ctx context.Context, args map[string]any, base func(context.Context, json.RawMessage) (any, error)) (any, bool, error)) {
		i := idx(name)
		if i < 0 {
			return
		}
		t := &tools[i]
		schema := maps.Clone(t.InputSchema)
		p := maps.Clone(schema["properties"].(props))
		maps.Copy(p, add)
		schema["properties"] = p
		if description != "" {
			t.Description = description
		}
		base := t.Handler
		t.InputSchema = schema
		t.Handler = func(ctx context.Context, raw json.RawMessage) (any, error) {
			args := map[string]any{}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, output.Usagef("invalid arguments: %v", err)
			}
			if res, done, err := route(ctx, args, base); done {
				return res, err
			}
			return base(ctx, raw)
		}
	}
	// call runs a tool's handler with the given arguments.
	call := func(ctx context.Context, h func(context.Context, json.RawMessage) (any, error), args map[string]any) (any, error) {
		raw, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		return h(ctx, raw)
	}
	pick := func(args map[string]any, keys ...string) map[string]any {
		out := map[string]any{}
		for _, k := range keys {
			if v, ok := args[k]; ok {
				out[k] = v
			}
		}
		return out
	}

	// get_message_log + steps
	if steps := handler("get_message_steps"); steps != nil {
		patch("get_message_log",
			"Details of one message: status, full error text, errorInformation (lastErrorModelStepId: the .iflw element where it failed), custom header properties, adapter attributes (e.g. HTTP status), attachments and persisted messages. "+
				"include_steps adds the step-level trace (runs, steps, first failing step; step_properties adds each step's properties). Payloads: get_message_content.",
			props{"include_steps": boolean("Add the processing steps and the first failing step"), "step_properties": boolean("With include_steps: each step's properties (e.g. adapter addresses)")},
			func(ctx context.Context, args map[string]any, base func(context.Context, json.RawMessage) (any, error)) (any, bool, error) {
				include, _ := args["include_steps"].(bool)
				props, _ := args["step_properties"].(bool)
				delete(args, "include_steps")
				delete(args, "step_properties")
				log, err := call(ctx, base, args)
				if err != nil || !include {
					return log, true, err
				}
				s, err := call(ctx, steps, map[string]any{"message_guid": args["message_guid"], "properties": props})
				if err != nil {
					return log, true, err
				}
				out, err := merged(log, map[string]any{"steps": s})
				return out, true, err
			})
	}

	// get_message_trace + one traced message
	if traced := handler("get_trace_message"); traced != nil {
		if i := idx("get_message_trace"); i >= 0 {
			delete(tools[i].InputSchema, "required") // message_guid or trace_id
		}
		patch("get_message_trace",
			"Steps of a message that was processed with log level TRACE, with the trace IDs of the message at each step (optionally only one model step); with trace_id, the payload, headers and properties at that step "+
				"(sensitive header values masked, payload truncated to max_bytes). Empty with a hint when the flow was not on TRACE.",
			props{"trace_id": str("A trace ID from this tool's result: read the message at that step"), "max_bytes": maxBytesSchema()},
			func(ctx context.Context, args map[string]any, base func(context.Context, json.RawMessage) (any, error)) (any, bool, error) {
				if id, _ := args["trace_id"].(string); id != "" {
					res, err := call(ctx, traced, pick(args, "trace_id", "max_bytes"))
					return res, true, err
				}
				if g, _ := args["message_guid"].(string); g == "" {
					return nil, true, output.Usagef("give message_guid (the traced steps) or trace_id (the message at one step)")
				}
				delete(args, "max_bytes")
				res, err := call(ctx, base, args)
				return res, true, err
			})
	}

	// list_runtime_artifacts + status of given artifacts
	if status := handler("get_runtime_status"); status != nil {
		patch("list_runtime_artifacts",
			"Artifacts deployed on the tenant with status, version and deployment time. statuses [\"ERROR\"] is a tenant-wide health check (with error messages); "+
				"artifact_ids gives the state of those artifacts, deployed or not (e.g. after a deploy timeout), with the tenant's error for ERROR.",
			props{"artifact_ids": strArray("Only these artifacts, deployed or not")},
			func(ctx context.Context, args map[string]any, base func(context.Context, json.RawMessage) (any, error)) (any, bool, error) {
				if ids, ok := args["artifact_ids"]; ok {
					res, err := call(ctx, status, map[string]any{"artifact_ids": ids})
					return res, true, err
				}
				return nil, false, nil
			})
	}

	// list_variables + one variable's value
	if get := handler("get_variable"); get != nil {
		patch("list_variables",
			"Global variables and variables of integration flows (Write Variables step): name, flow, visibility, last update. artifact_id keeps that flow's and the global ones; "+
				"name reads that variable's value (artifact_id empty: a global variable), truncated to max_bytes.",
			props{"name": str("Read this variable's value"), "max_bytes": maxBytesSchema()},
			func(ctx context.Context, args map[string]any, base func(context.Context, json.RawMessage) (any, error)) (any, bool, error) {
				if n, _ := args["name"].(string); n != "" {
					res, err := call(ctx, get, pick(args, "name", "artifact_id", "max_bytes"))
					return res, true, err
				}
				delete(args, "max_bytes")
				res, err := call(ctx, base, args)
				return res, true, err
			})
	}

	// get_message_content: attachments and persisted messages
	att, store := handler("get_message_attachment"), handler("get_message_store_entry")
	if att != nil && store != nil {
		tools = append(tools, Tool{
			Name: "get_message_content", Title: "Read a message log attachment or a persisted message",
			Description: "Content of a message log attachment (attachment_id from get_message_log attachments, e.g. a payload a script logged) or of a message a Persist step wrote " +
				"(store_entry_id from get_message_log messageStoreEntries). Text inline, binary base64; truncated to max_bytes.",
			InputSchema: object(props{"attachment_id": str("Attachment ID"), "store_entry_id": str("Message store entry ID"), "max_bytes": maxBytesSchema()}),
			Annotations: map[string]any{"readOnlyHint": true, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					AttachmentID string `json:"attachment_id"`
					EntryID      string `json:"store_entry_id"`
					MaxBytes     int    `json:"max_bytes"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				args := map[string]any{}
				if a.MaxBytes != 0 {
					args["max_bytes"] = a.MaxBytes
				}
				switch {
				case (a.AttachmentID == "") == (a.EntryID == ""):
					return nil, output.Usagef("give attachment_id or store_entry_id")
				case a.AttachmentID != "":
					args["attachment_id"] = a.AttachmentID
					return call(ctx, att, args)
				}
				args["entry_id"] = a.EntryID
				return call(ctx, store, args)
			},
		})
	}
	return slices.DeleteFunc(tools, func(t Tool) bool { return removedTools[t.Name] != "" })
}

// merged returns v (a JSON object) with the extra fields added.
func merged(v any, extra map[string]any) (map[string]any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	maps.Copy(out, extra)
	return out, nil
}
