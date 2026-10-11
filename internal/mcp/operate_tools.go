package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// operateTools manage JMS messages (as Manage Message Queues in the Web UI)
// and find messages by a custom header value. delete_jms_messages is opt-in
// (see optInTools).
func operateTools(cfg Config, readOnly map[string]any) []Tool {
	type selArgs struct {
		Queue      string   `json:"queue"`
		IDs        []string `json:"ids"`
		All        bool     `json:"all"`
		FailedOnly bool     `json:"failed_only"`
		Confirm    bool     `json:"confirm"`
	}
	selProps := func(confirm string, extra props) props {
		p := props{
			"queue":       str("Queue name"),
			"ids":         strArray("JMS message IDs (jmsMessageId from list_jms_messages)"),
			"all":         boolean("All messages of the queue instead of ids"),
			"failed_only": boolean("With all: only failed messages"),
			"confirm":     boolean(confirm),
		}
		for k, v := range extra {
			p[k] = v
		}
		return p
	}
	sel := func(a selArgs) ops.JMSSelection {
		return ops.JMSSelection{Queue: a.Queue, IDs: a.IDs, All: a.All, FailedOnly: a.FailedOnly}
	}
	changing := map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": true}
	return []Tool{
		{
			Name: "list_jms_messages", Title: "List the messages of a JMS queue",
			Description: "Messages waiting in a JMS queue: jmsMessageId, failed (processing failed, waits for the next retry), retryCount, nextRetry, messageGuid of the last attempt (get_message_log), createdAt. " +
				"Use it when list_jms_queues shows messages piling up. Next: retry_jms_messages or move_jms_messages on the user's request.",
			InputSchema: object(props{"queue": str("Queue name (list_jms_queues)"), "failed_only": boolean("Only failed messages"),
				"top": integer("Maximum messages, default 100")}, "queue"),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Queue      string `json:"queue"`
					FailedOnly bool   `json:"failed_only"`
					Top        int    `json:"top"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				return ops.ListQueueMessages(cfg.Exe, a.Queue, a.FailedOnly, a.Top)
			},
		},
		{
			Name: "retry_jms_messages", Title: "Retry JMS messages",
			Description: "Process messages of a JMS queue again now instead of waiting for the next retry (the flow calls its receivers again). Give ids or all=true. Requires confirm=true.",
			InputSchema: object(selProps("Must be true: the messages are processed again", nil), "queue", "confirm"),
			Annotations: changing,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a selArgs
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if !a.Confirm {
					return nil, output.Usagef("retry_jms_messages processes the messages of %s again; call again with confirm=true", a.Queue)
				}
				return ops.RetryQueueMessages(cfg.Exe, sel(a))
			},
		},
		{
			Name: "move_jms_messages", Title: "Move JMS messages to another queue",
			Description: "Move messages of a JMS queue to another existing queue, e.g. from an error queue back to the queue of the flow that processes them. Give ids or all=true. Requires confirm=true.",
			InputSchema: object(selProps("Must be true: the messages leave the queue", props{"target_queue": str("Queue to move the messages to")}), "queue", "target_queue", "confirm"),
			Annotations: changing,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					selArgs
					TargetQueue string `json:"target_queue"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if !a.Confirm {
					return nil, output.Usagef("move_jms_messages moves messages from %s to %s; call again with confirm=true", a.Queue, a.TargetQueue)
				}
				return ops.MoveQueueMessages(cfg.Exe, sel(a.selArgs), a.TargetQueue)
			},
		},
		{
			Name: "delete_jms_messages", Title: "Delete JMS messages",
			Description: "Delete messages from a JMS queue: they are never processed and cannot be restored. Only on the user's explicit request, after list_jms_messages. Give ids or all=true. Requires confirm=true.",
			InputSchema: object(selProps("Must be true: the messages are lost", nil), "queue", "confirm"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a selArgs
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if !a.Confirm {
					return nil, output.Usagef("delete_jms_messages removes messages from %s for good; call again with confirm=true only if the user asked for it", a.Queue)
				}
				return ops.DeleteQueueMessages(cfg.Exe, sel(a))
			},
		},
		{
			Name: "set_jms_queue_state", Title: "Activate or deactivate a JMS queue",
			Description: "Deactivate a JMS queue (active=false: messages are kept but not handed to the consumer flow, e.g. while a receiver is down) or activate it again.",
			InputSchema: object(props{"queue": str("Queue name"), "active": boolean("true: activate, false: deactivate")}, "queue", "active"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Queue  string `json:"queue"`
					Active *bool  `json:"active"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if a.Active == nil {
					return nil, output.Usagef("active is required")
				}
				return ops.SetQueueState(cfg.Exe, a.Queue, *a.Active)
			},
		},
		{
			Name: "find_messages_by_header", Title: "Find messages by a custom header value",
			Description: "Messages (newest first) whose custom header property name equals value, e.g. an order number the flows write with addCustomHeaderProperty. " +
				"Searches the whole tenant in one call; artifact_id, package_id, since, until and statuses narrow the result. Tenants without the custom header search are scanned instead (needs artifact_id or package_id, and since).",
			InputSchema: object(props{
				"name":           str("Custom header property name"),
				"value":          str("Value"),
				"artifact_id":    str("Only messages of this integration flow"),
				"package_id":     str("Only messages of this package's integration flows"),
				"since":          str("Only messages that ended after this time: RFC 3339 or duration back from now (30m, 2h, 1d)"),
				"until":          str("Only messages that started before this time"),
				"statuses":       map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": cpi.MessageLogStatuses}, "description": "Only these statuses"},
				"top":            map[string]any{"type": "integer", "minimum": 1, "maximum": ops.MaxMessageLogs, "description": "Maximum number of messages, default 20"},
				"include_errors": boolean("Include the error text of failed messages (default true)"),
			}, "name", "value"),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Name          string   `json:"name"`
					Value         string   `json:"value"`
					ArtifactID    string   `json:"artifact_id"`
					PackageID     string   `json:"package_id"`
					Since         string   `json:"since"`
					Until         string   `json:"until"`
					Statuses      []string `json:"statuses"`
					Top           int      `json:"top"`
					IncludeErrors *bool    `json:"include_errors"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				now := time.Now()
				since, err := ops.ParseTimeArg(a.Since, now)
				if err != nil {
					return nil, output.Usagef("invalid since: %v", err)
				}
				until, err := ops.ParseTimeArg(a.Until, now)
				if err != nil {
					return nil, output.Usagef("invalid until: %v", err)
				}
				q := ops.HeaderSearchQuery{Header: ops.CustomHeaderFilter{Name: a.Name, Value: a.Value}, PackageID: a.PackageID,
					Since: since, Until: until, Statuses: a.Statuses, Top: a.Top, IncludeErrors: a.IncludeErrors == nil || *a.IncludeErrors}
				if a.ArtifactID != "" {
					q.ArtifactIDs = []string{a.ArtifactID}
				}
				return ops.SearchMessagesByHeader(ctx, cfg.Exe, q)
			},
		},
	}
}
