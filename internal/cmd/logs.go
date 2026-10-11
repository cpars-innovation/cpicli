package cmd

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func NewLogsCommand() *cobra.Command {
	c := &cobra.Command{
		Use:          "logs",
		Short:        "Query message processing logs",
		SilenceUsage: true,
		Long: `Query message processing logs (MPL) of the runtime, newest first.

--since/--until take a duration back from now (30m, 2h, 1d) or an RFC 3339
timestamp. --wait polls until at least one matching message exists and all
matching messages reached a final status (COMPLETED, FAILED, ESCALATED,
CANCELLED, DISCARDED, ABANDONED); use it after sending a test message with
--since set to the send time. Exit code 6 if the wait times out.

Statuses: ` + strings.Join(cpi.MessageLogStatuses, ", "),
		Example: `  cpictl logs --artifact-id OrderIntake --since 1h
  cpictl logs --artifact-id OrderIntake --status FAILED --errors --output json
  cpictl logs --artifact-id OrderIntake --since 2m --wait 60s --errors
  cpictl logs --package-id Orders --since 1h --header OrderId=4711
  cpictl logs search --header OrderNo=4711
  cpictl logs get --message-guid AFq478Bblxi4wCjBcDb_G0vAGGZG
  cpictl logs tree --trace-id 0af7651916cd43dd8448eb211c80319c`,
		RunE: func(cmd *cobra.Command, args []string) error {
			q, err := messageLogQuery(cmd)
			if err != nil {
				return err
			}
			exe := tenantExecuter(cmd)
			var res *ops.MessageLogList
			header, pkg := config.GetString(cmd, "header"), config.GetString(cmd, "package-id")
			if header != "" || pkg != "" {
				scope := ops.ScanScope{PackageID: pkg, Since: q.Since, Until: q.Until, Statuses: q.Statuses}
				if q.ArtifactID != "" {
					scope.ArtifactIDs = []string{q.ArtifactID}
				}
				if header == "" {
					res, err = ops.QueryPackageMessageLogs(exe, scope, q.Top)
				} else {
					name, value, ok := strings.Cut(header, "=")
					if !ok {
						return output.Usagef("invalid --header %q, expected name=value", header)
					}
					res, err = ops.SearchMessagesByHeader(cmd.Context(), exe, ops.HeaderSearchQuery{Header: ops.CustomHeaderFilter{Name: name, Value: value},
						ArtifactIDs: scope.ArtifactIDs, PackageID: pkg, Since: q.Since, Until: q.Until, Statuses: q.Statuses, Top: q.Top, IncludeErrors: q.IncludeErrors})
				}
			} else if wait, _ := cmd.Flags().GetDuration("wait"); wait > 0 {
				res, err = ops.WaitForMessageLogs(cmd.Context(), exe, q, wait, 5*time.Second)
			} else {
				res, err = ops.QueryMessageLogs(exe, q)
			}
			if res != nil {
				output.SetResult(cmd.Context(), res)
				for _, l := range res.Logs {
					end := ""
					if l.LogEnd != nil {
						end = l.LogEnd.Local().Format(time.DateTime)
					}
					log.Info().Str("messageGuid", l.MessageGuid).Msgf("%s  %-10s %s  %s%s", end, l.Status, l.ArtifactID, l.MessageGuid, errSuffix(firstLine(l.ErrorText)))
				}
				log.Info().Msgf("%d of %d message(s)", len(res.Logs), res.Total)
				if res.Scanned > 0 {
					log.Info().Msgf("%d message(s) scanned, truncated: %v", res.Scanned, res.Truncated)
				}
			}
			return err
		},
	}
	f := c.Flags()
	f.String("artifact-id", "", "Integration flow ID")
	f.StringSlice("status", nil, "Comma separated statuses, e.g. FAILED,RETRY")
	f.String("since", "", "Messages that ended after this time (duration like 1h or RFC 3339)")
	f.String("until", "", "Messages that started before this time (duration like 1h or RFC 3339)")
	f.String("correlation-id", "", "Correlation ID")
	f.String("application-message-id", "", "Application message ID")
	f.Int("top", 20, "Maximum number of messages (max 200)")
	f.Int("skip", 0, "Skip the first n messages")
	f.Bool("errors", false, "Include the error text of failed messages")
	f.Duration("wait", 0, "Wait up to this long for final messages (e.g. 60s)")
	f.String("package-id", "", "Messages of all integration flows of this package (needs --since)")
	f.String("header", "", "Only messages with this custom header property, name=value (as 'logs search'; tenants without the custom header search are scanned: needs --artifact-id or --package-id, and --since)")

	get := &cobra.Command{
		Use:          "get",
		Short:        "Show one message: status, error text and step, custom headers, adapter attributes, attachments",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			guid := config.GetString(cmd, "message-guid")
			d, err := ops.GetMessageLog(tenantExecuter(cmd), guid, 0)
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), d)
			log.Info().Msgf("%s  %s  %s", d.MessageGuid, d.Status, d.ArtifactID)
			if d.ErrorText != "" {
				log.Info().Msgf("Error: %s", d.ErrorText)
			}
			for _, h := range d.CustomHeaderProperties {
				log.Info().Msgf("Header %s = %s", h.Name, h.Value)
			}
			if d.ErrorInformation != nil && d.ErrorInformation.LastErrorModelStepID != "" {
				log.Info().Msgf("Failed at model step %s", d.ErrorInformation.LastErrorModelStepID)
			}
			for _, h := range d.AdapterAttributes {
				log.Info().Msgf("Adapter %s: %s = %s", h.Adapter, h.Name, h.Value)
			}
			for _, a := range d.Attachments {
				log.Info().Msgf("Attachment %s (%s, %d bytes)", a.Name, a.ContentType, a.Size)
			}
			return nil
		},
	}
	get.Flags().String("message-guid", "", "Message GUID")

	steps := &cobra.Command{
		Use:          "steps",
		Short:        "Show the processing steps of a message and the step that failed",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			props, _ := cmd.Flags().GetBool("properties")
			res, err := ops.GetMessageStepsWith(tenantExecuter(cmd), config.GetString(cmd, "message-guid"), props)
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), res)
			for _, r := range res.Runs {
				for _, st := range r.Steps {
					log.Info().Msgf("%-10s %-30s %s%s", st.Status, st.ModelStepID, st.Activity, errSuffix(st.Error))
					for _, p := range st.Properties {
						log.Info().Msgf("%-10s   %s = %s", "", p.Name, p.Value)
					}
				}
			}
			if res.FailedStep != nil {
				log.Warn().Msgf("Failed step: %s (%s)%s", res.FailedStep.ModelStepID, res.FailedStep.Activity, errSuffix(res.FailedStep.Error))
			}
			return nil
		},
	}
	steps.Flags().String("message-guid", "", "Message GUID")
	steps.Flags().Bool("properties", false, "Include the properties of each step (e.g. adapter addresses)")

	search := &cobra.Command{
		Use:   "search",
		Short: "Find messages by a custom header property value (e.g. an order number)",
		Long: `Find messages by a custom header property that flows write
(messageLog.addCustomHeaderProperty), such as an order number, newest first.

The tenant searches MessageProcessingLogCustomHeaderProperties: no scope is
needed. --artifact-id, --package-id, --since, --until and --status narrow the
result. Tenants without that entity set are scanned instead, which needs
--artifact-id or --package-id, and --since.`,
		Example: `  cpictl logs search --header OrderNo=4711
  cpictl logs search --header OrderNo=4711 --since 2d --status FAILED --errors`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			now := time.Now()
			since, err := ops.ParseTimeArg(config.GetString(cmd, "since"), now)
			if err != nil {
				return output.Usagef("invalid --since: %v", err)
			}
			until, err := ops.ParseTimeArg(config.GetString(cmd, "until"), now)
			if err != nil {
				return output.Usagef("invalid --until: %v", err)
			}
			name, value, ok := strings.Cut(config.GetString(cmd, "header"), "=")
			if !ok || name == "" {
				return output.Usagef("--header name=value is required")
			}
			errs, _ := cmd.Flags().GetBool("errors")
			q := ops.HeaderSearchQuery{Header: ops.CustomHeaderFilter{Name: name, Value: value}, PackageID: config.GetString(cmd, "package-id"),
				Since: since, Until: until, Statuses: config.GetStringSlice(cmd, "status"), Top: config.GetInt(cmd, "top"), IncludeErrors: errs}
			if id := config.GetString(cmd, "artifact-id"); id != "" {
				q.ArtifactIDs = []string{id}
			}
			res, err := ops.SearchMessagesByHeader(cmd.Context(), tenantExecuter(cmd), q)
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), res)
			for _, l := range res.Logs {
				end := ""
				if l.LogEnd != nil {
					end = l.LogEnd.Local().Format(time.DateTime)
				}
				log.Info().Str("messageGuid", l.MessageGuid).Msgf("%s  %-10s %s  %s%s", end, l.Status, l.ArtifactID, l.MessageGuid, errSuffix(firstLine(l.ErrorText)))
			}
			log.Info().Msgf("%d of %d message(s) with %s", len(res.Logs), res.Total, res.Filter)
			return nil
		},
	}
	sf0 := search.Flags()
	sf0.String("header", "", "Custom header property, name=value")
	sf0.String("artifact-id", "", "Only messages of this integration flow")
	sf0.String("package-id", "", "Only messages of this package's integration flows")
	sf0.String("since", "", "Only messages that ended after this time (duration like 1h or RFC 3339)")
	sf0.String("until", "", "Only messages that started before this time")
	sf0.StringSlice("status", nil, "Only these statuses, e.g. FAILED,RETRY")
	sf0.Int("top", 20, "Maximum number of messages (max 200)")
	sf0.Bool("errors", false, "Include the error text of failed messages")

	attachment := &cobra.Command{
		Use:          "attachment",
		Short:        "Download a log attachment (ID from 'logs get')",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ops.GetMessageAttachment(tenantExecuter(cmd), config.GetString(cmd, "id"), contentLimit(cmd))
			if err != nil {
				return err
			}
			return emitContent(cmd, res, res.Content)
		},
	}
	attachment.Flags().String("id", "", "Attachment ID")
	addContentFlags(attachment)

	payload := &cobra.Command{
		Use:          "payload",
		Short:        "Download a persisted message (message store entry ID from 'logs get')",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ops.GetMessageStoreEntry(tenantExecuter(cmd), config.GetString(cmd, "id"), contentLimit(cmd))
			if err != nil {
				return err
			}
			return emitContent(cmd, res, res.Content)
		},
	}
	payload.Flags().String("id", "", "Message store entry ID")
	addContentFlags(payload)

	trace := &cobra.Command{
		Use:   "trace",
		Short: "List the traced steps of a message (flow on log level TRACE)",
		Long: `List the steps of a message that was processed with log level TRACE and the
trace IDs of the message at each step. Read one with 'logs trace-message'.
Set the level with 'cpictl log-level --level TRACE' (active for 10 minutes).`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ops.GetMessageTrace(tenantExecuter(cmd), config.GetString(cmd, "message-guid"), config.GetString(cmd, "model-step-id"))
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), res)
			for _, st := range res.Steps {
				for _, tr := range st.Traces {
					log.Info().Msgf("%-12s %-30s %s (%d bytes)", tr.TraceID, st.ModelStepID, st.Activity, tr.PayloadSize)
				}
			}
			if res.Hint != "" {
				log.Warn().Msg(res.Hint)
			}
			return nil
		},
	}
	trace.Flags().String("message-guid", "", "Message GUID")
	trace.Flags().String("model-step-id", "", "Only this element of the iFlow model")

	traceMessage := &cobra.Command{
		Use:          "trace-message",
		Short:        "Show payload, headers and exchange properties of a traced step (ID from 'logs trace')",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ops.GetTraceMessage(tenantExecuter(cmd), config.GetString(cmd, "id"), contentLimit(cmd))
			if err != nil {
				return err
			}
			for _, h := range res.Headers {
				log.Info().Msgf("header %s: %s", h.Name, h.Value)
			}
			for _, p := range res.ExchangeProperties {
				log.Info().Msgf("property %s: %s", p.Name, p.Value)
			}
			return emitContent(cmd, res, res.Payload)
		},
	}
	traceMessage.Flags().String("id", "", "Trace ID")
	addContentFlags(traceMessage)

	tree := &cobra.Command{
		Use:   "tree",
		Short: "Show the path of a message or trace across flows and its first failure",
		Long: `Build the call tree across flows from a trace ID or from any message GUID.

--trace-id (W3C trace ID, e.g. the traceId of 'cpictl send'): messages are found by
ApplicationMessageId = trace ID, otherwise by scanning the scope (--artifact-ids or
--package-id, and --since) for the trace-id custom header.

--message (a message GUID): all messages with its correlation ID. With --key-header
(custom header properties your flows write, e.g. OrderNo), messages with the same
value join the path, with the rest of their run: the path continues when the message
left the tenant and came back with a new correlation ID. They are found with the
custom header search (as 'logs search'; a scope narrows it); tenants without it scan
the scope (--artifact-ids or --package-id, and --since; at most --max-scan messages).

Nodes are linked by span-id / parent-span-id (names configurable), else by the
predecessor message, else (--message only) by start time ("inferred", or "header"
across correlation IDs). The result has the hops between flows and a path key that
is the same for every message taking the same route.`,
		Example: `  cpictl logs tree --trace-id 0af7651916cd43dd8448eb211c80319c
  cpictl logs tree --message AGXyz... --key-header OrderNo --package-id Orders --since 2h`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			now := time.Now()
			since, err := ops.ParseTimeArg(config.GetString(cmd, "since"), now)
			if err != nil {
				return output.Usagef("invalid --since: %v", err)
			}
			until, err := ops.ParseTimeArg(config.GetString(cmd, "until"), now)
			if err != nil {
				return output.Usagef("invalid --until: %v", err)
			}
			traceID, message := config.GetString(cmd, "trace-id"), config.GetString(cmd, "message")
			if (traceID == "") == (message == "") {
				return output.Usagef("give either --trace-id or --message")
			}
			scope := ops.ScanScope{ArtifactIDs: nonEmpty(config.GetStringSlice(cmd, "artifact-ids")), PackageID: config.GetString(cmd, "package-id"), Since: since, Until: until}
			props := ops.TraceProperties{Trace: config.GetString(cmd, "trace-property"), Span: config.GetString(cmd, "span-property"), Parent: config.GetString(cmd, "parent-property")}
			var res *ops.TraceTree
			if message != "" {
				res, err = ops.MessagePathFor(cmd.Context(), tenantExecuter(cmd), ops.MessagePathQuery{MessageGuid: message, Properties: props,
					KeyHeaders: nonEmpty(config.GetStringSlice(cmd, "key-header")), Scope: scope, MaxScan: config.GetInt(cmd, "max-scan"),
					MaxMessages: config.GetInt(cmd, "max-messages")})
			} else {
				res, err = ops.TraceTreeFor(cmd.Context(), tenantExecuter(cmd), ops.TraceTreeQuery{TraceID: traceID, MaxScan: config.GetInt(cmd, "max-scan"), Scope: scope, Properties: props})
			}
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), res)
			var print func(n *ops.TraceNode, depth int)
			print = func(n *ops.TraceNode, depth int) {
				log.Info().Msgf("%s%-10s %s  %s%s", strings.Repeat("  ", depth), n.Status, n.ArtifactID, n.MessageGuid, errSuffix(firstLine(n.ErrorText)))
				for _, c := range n.Children {
					print(c, depth+1)
				}
			}
			for _, r := range res.Roots {
				print(r, 0)
			}
			for _, h := range res.Hops {
				log.Info().Msgf("hop  %s -> %s (%s)", h.From, h.To, h.Link)
			}
			if res.FirstFailure != nil {
				log.Warn().Msgf("First failure: %s %s%s", res.FirstFailure.ArtifactID, res.FirstFailure.MessageGuid, errSuffix(firstLine(res.FirstFailure.ErrorText)))
			}
			return nil
		},
	}
	tf := tree.Flags()
	tf.String("trace-id", "", "Trace ID (32 hex characters)")
	tf.String("message", "", "Message GUID: follow its correlation ID (and --key-header) across flows")
	tf.StringSlice("key-header", nil, "With --message: custom header properties that join runs with the same value")
	tf.Int("max-messages", 200, "With --message: maximum messages of the path")
	tf.StringSlice("artifact-ids", nil, "Scope of the fallback scan")
	tf.String("package-id", "", "Scope of the fallback scan: all flows of this package")
	tf.String("since", "", "Start of the scan window (duration like 1h or RFC 3339)")
	tf.String("until", "", "End of the scan window")
	tf.Int("max-scan", 200, "Maximum messages scanned")
	tf.String("trace-property", ops.DefaultTraceProperties.Trace, "Custom header property with the trace ID")
	tf.String("span-property", ops.DefaultTraceProperties.Span, "Custom header property with the span ID")
	tf.String("parent-property", ops.DefaultTraceProperties.Parent, "Custom header property with the parent span ID")

	summary := &cobra.Command{
		Use:   "summary",
		Short: "Message volume and failures per flow, per connection between flows and per error fingerprint",
		Long: `Summarize the message processing logs of a time window: per flow the count per
status, failures, average and longest duration; per connection between flows the
messages and failures (linked by the logs' predecessor, or for the connections of
the content graph by shared correlation ID); failures grouped by error fingerprint
(the error text without IDs, timestamps and long numbers) with a sample, count,
first and last occurrence. Reads at most --max-messages, newest first.`,
		Example: `  cpictl logs summary --since 1h
  cpictl logs summary --since 24h --artifact 'Orders_*' --graph .cpi/graph.json --output json`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			now := time.Now()
			since, err := ops.ParseTimeArg(config.GetString(cmd, "since"), now)
			if err != nil {
				return output.Usagef("invalid --since: %v", err)
			}
			until, err := ops.ParseTimeArg(config.GetString(cmd, "until"), now)
			if err != nil {
				return output.Usagef("invalid --until: %v", err)
			}
			q := ops.MessageSummaryQuery{Since: since, Until: until, Artifacts: nonEmpty(config.GetStringSlice(cmd, "artifact")),
				MaxMessages: config.GetInt(cmd, "max-messages"), ErrorSamples: config.GetInt(cmd, "error-samples")}
			graphFile := config.GetString(cmd, "graph")
			if graphFile == "" && fileExists(filepath.Join(".cpi", "graph.json")) {
				graphFile = filepath.Join(".cpi", "graph.json")
			}
			if graphFile != "" {
				if q.Graph, err = ops.LoadGraph(graphFile); err != nil {
					return output.Usagef("graph %s: %v", graphFile, err)
				}
			}
			res, err := ops.SummarizeMessages(tenantExecuter(cmd), q)
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), res)
			for _, f := range res.Flows {
				ev := log.Info()
				if f.Failed > 0 {
					ev = log.Warn()
				}
				ev.Msgf("%-40s %5d message(s), %4d failed, avg %dms, max %dms", f.Artifact, f.Total, f.Failed, f.AvgDurationMs, f.MaxDurationMs)
			}
			for _, e := range res.Edges {
				log.Info().Msgf("%s -> %s: %d message(s), %d failed (%s)", e.From, e.To, e.Messages, e.Failed, e.Source)
			}
			for _, g := range res.Errors {
				log.Warn().Msgf("%dx %s %s (last %s, %s)", g.Count, g.Artifact, firstLine(g.Sample), g.Last.Format(time.RFC3339), g.MessageGuid)
			}
			trunc := ""
			if res.Truncated {
				trunc = " (truncated: raise --max-messages or shorten the window)"
			}
			log.Info().Msgf("%d message(s) in %d flow(s)%s", res.Scanned, len(res.Flows), trunc)
			return nil
		},
	}
	sf := summary.Flags()
	sf.String("since", "1h", "Start of the window (duration like 1h or RFC 3339)")
	sf.String("until", "", "End of the window (default: now)")
	sf.StringSlice("artifact", nil, "Only these flows (IDs or patterns)")
	sf.String("graph", "", "Content graph for connections without predecessor links (default: .cpi/graph.json if it exists)")
	sf.Int("max-messages", 5000, "Messages read at most, newest first")
	sf.Int("error-samples", 50, "Error texts read for fingerprints (one request each)")

	c.AddCommand(get, search, steps, attachment, payload, trace, traceMessage, tree, summary)
	return c
}

func NewLogLevelCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "log-level",
		Short: "Set the message processing log level of a deployed integration flow",
		Long: `Set the log level of a deployed integration flow: NONE, INFO, DEBUG or TRACE.
TRACE records payload and headers at every step for 10 minutes, then the tenant
falls back to the previous level; read traces with 'logs trace'. Traces contain
business data: use them on development tenants.

Uses the operations command of the Web UI (there is no OData API for it).`,
		Example:      `  cpictl log-level --artifact-id OrderIntake --level TRACE`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ops.SetLogLevel(tenantExecuter(cmd), ops.LogLevelRequest{
				ArtifactID: config.GetString(cmd, "artifact-id"), Level: config.GetString(cmd, "level"),
				NodeType: config.GetString(cmd, "node-type"), RuntimeLocationID: config.GetString(cmd, "runtime-location-id"),
			})
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), res)
			log.Info().Msg(res.String())
			return nil
		},
	}
	c.Flags().String("artifact-id", "", "Integration flow ID (deployed)")
	c.Flags().String("level", "", "NONE, INFO, DEBUG or TRACE")
	c.Flags().String("node-type", "IFLMAP", "Runtime node type")
	c.Flags().String("runtime-location-id", "cloudintegration", "Runtime location (edge integration cells use their own)")
	_ = c.MarkFlagRequired("artifact-id")
	_ = c.MarkFlagRequired("level")
	return c
}

func messageLogQuery(cmd *cobra.Command) (ops.MessageLogQuery, error) {
	q := ops.MessageLogQuery{
		ArtifactID:           config.GetString(cmd, "artifact-id"),
		Statuses:             nonEmpty(config.GetStringSlice(cmd, "status")),
		CorrelationID:        config.GetString(cmd, "correlation-id"),
		ApplicationMessageID: config.GetString(cmd, "application-message-id"),
		Top:                  config.GetInt(cmd, "top"),
		Skip:                 config.GetInt(cmd, "skip"),
		IncludeErrors:        config.GetBool(cmd, "errors"),
	}
	var err error
	if q.Since, err = ops.ParseTimeArg(config.GetString(cmd, "since"), time.Now()); err != nil {
		return q, output.Usagef("invalid --since: %v", err)
	}
	if q.Until, err = ops.ParseTimeArg(config.GetString(cmd, "until"), time.Now()); err != nil {
		return q, output.Usagef("invalid --until: %v", err)
	}
	return q, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
