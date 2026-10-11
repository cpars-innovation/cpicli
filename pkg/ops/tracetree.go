package ops

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// TraceProperties are the custom header property names a tracer writes
// (W3C trace context in the message processing log).
type TraceProperties struct {
	Trace, Span, Parent string
}

// DefaultTraceProperties match the CPIMessageTracer convention.
var DefaultTraceProperties = TraceProperties{Trace: "trace-id", Span: "span-id", Parent: "parent-span-id"}

// TraceTreeQuery selects the messages of one trace.
type TraceTreeQuery struct {
	TraceID string
	// Scope for the fallback scan when no message carries the trace ID as
	// ApplicationMessageId.
	Scope      ScanScope
	Properties TraceProperties
	// MaxScan caps the fallback scan (default 200).
	MaxScan int
}

// TraceNode is one message of a trace.
type TraceNode struct {
	MessageGuid    string       `json:"messageGuid"`
	ArtifactID     string       `json:"artifactId,omitempty"`
	PackageID      string       `json:"packageId,omitempty"`
	Status         string       `json:"status"`
	LogStart       *time.Time   `json:"logStart,omitempty"`
	LogEnd         *time.Time   `json:"logEnd,omitempty"`
	SpanID         string       `json:"spanId,omitempty"`
	ParentSpanID   string       `json:"parentSpanId,omitempty"`
	ErrorText      string       `json:"errorText,omitempty"`
	ErrorTruncated bool         `json:"errorTruncated,omitempty"`
	Children       []*TraceNode `json:"children"`
}

// TraceFailure points at the earliest failed message of a trace.
type TraceFailure struct {
	MessageGuid string `json:"messageGuid"`
	ArtifactID  string `json:"artifactId,omitempty"`
	ErrorText   string `json:"errorText,omitempty"`
}

// TraceTree is the call tree of a trace.
type TraceTree struct {
	TraceID       string `json:"traceId,omitempty"`
	CorrelationID string `json:"correlationId,omitempty"`
	// Source is how the messages were found: application_message_id or
	// scan (by trace ID), correlation_id or message (by message GUID).
	Source   string       `json:"source"`
	Messages int          `json:"messages"`
	Roots    []*TraceNode `json:"roots"`
	// Hops are the steps from flow to flow (parent to child message);
	// PathKey identifies the route (the same flows connected the same way).
	Hops []TraceHop `json:"hops"`
	// Keys are the key header values the path was joined by.
	Keys         map[string]string `json:"keys,omitempty"`
	PathKey      string            `json:"pathKey"`
	FirstFailure *TraceFailure     `json:"firstFailure,omitempty"`
	Scanned      int               `json:"scanned,omitempty"`
	Truncated    bool              `json:"truncated,omitempty"`
}

var traceIDPattern32 = regexp.MustCompile(`^[0-9a-f]{32}$`)

// TraceTreeFor builds the call tree of a trace. It first looks for messages
// whose ApplicationMessageId is the trace ID (one query; the tracer sets
// SAP_ApplicationID = trace ID). Otherwise it scans the given scope for
// messages whose trace property matches. Nodes are linked by span and parent
// span (or, without them, by PredecessorMessageGuid); children are ordered by
// start time.
func TraceTreeFor(ctx context.Context, exe *httpclnt.HTTPExecuter, q TraceTreeQuery) (*TraceTree, error) {
	q.TraceID = strings.ToLower(strings.TrimSpace(q.TraceID))
	if !traceIDPattern32.MatchString(q.TraceID) {
		return nil, output.Usagef("trace_id must be 32 hex characters")
	}
	props := q.Properties
	if props.Trace == "" {
		props.Trace = DefaultTraceProperties.Trace
	}
	if props.Span == "" {
		props.Span = DefaultTraceProperties.Span
	}
	if props.Parent == "" {
		props.Parent = DefaultTraceProperties.Parent
	}
	if q.MaxScan <= 0 {
		q.MaxScan = 200
	}
	tree := &TraceTree{TraceID: q.TraceID, Source: "application_message_id", Roots: []*TraceNode{}}

	byAppID, err := QueryMessageLogs(exe, MessageLogQuery{ApplicationMessageID: q.TraceID, Top: MaxMessageLogs})
	if err != nil {
		return nil, err
	}
	var found []ScannedLog
	if len(byAppID.Logs) > 0 {
		// span IDs are only in the custom header properties: read them for
		// the matches (no scan)
		if found, err = withHeaders(ctx, exe, byAppID.Logs); err != nil {
			return nil, err
		}
	} else {
		if (len(q.Scope.ArtifactIDs) == 0 && q.Scope.PackageID == "") || q.Scope.Since.IsZero() {
			return nil, errScopeRequired()
		}
		res, err := ScanByHeaders(ctx, exe, q.Scope, q.MaxScan, func(h []cpi.NameValue) bool {
			return strings.EqualFold(headerValue(h, props.Trace), q.TraceID)
		})
		if err != nil {
			return nil, err
		}
		tree.Source, tree.Scanned, tree.Truncated, found = "scan", res.Scanned, res.Truncated, res.Matches
	}

	linkTraceTree(tree, exe, found, props, false)
	return tree, nil
}

// Hop links of a trace: how the parent of a message was found.
const (
	HopSpan        = "span"        // the tracer's parent span ID
	HopPredecessor = "predecessor" // the log's predecessor message
	HopInferred    = "inferred"    // same correlation ID, the latest message that started before
	HopHeader      = "header"      // same key header value (another correlation ID), the latest message that started before
)

// TraceHop is one step of a message path from one flow to the next.
type TraceHop struct {
	From        string `json:"from"`
	To          string `json:"to"`
	FromMessage string `json:"fromMessage"`
	ToMessage   string `json:"toMessage"`
	Link        string `json:"link"`
}

// linkTraceTree builds the tree, hops, path key and first failure of the
// messages found. With infer, a message without span or predecessor link is
// attached to the latest message that started before it (link header when
// the two have different correlation IDs: joined by a key header).
func linkTraceTree(tree *TraceTree, exe *httpclnt.HTTPExecuter, found []ScannedLog, props TraceProperties, infer bool) {
	mpl := cpi.NewMessageLogs(exe)
	nodes := make([]*TraceNode, 0, len(found))
	bySpan, byGuid := map[string]*TraceNode{}, map[string]*TraceNode{}
	predecessor, correlation := map[*TraceNode]string{}, map[*TraceNode]string{}
	for _, l := range found {
		n := &TraceNode{MessageGuid: l.MessageGuid, ArtifactID: l.ArtifactID, PackageID: l.PackageID, Status: l.Status,
			LogStart: l.LogStart, LogEnd: l.LogEnd, Children: []*TraceNode{},
			SpanID: strings.ToLower(headerValue(l.Headers, props.Span)), ParentSpanID: strings.ToLower(headerValue(l.Headers, props.Parent))}
		if slices.Contains(errorMessageStatuses, l.Status) {
			if text, err := mpl.ErrorText(l.MessageGuid); err == nil {
				n.ErrorText, n.ErrorTruncated = truncate(text, 0)
			}
		}
		nodes = append(nodes, n)
		byGuid[n.MessageGuid] = n
		if n.SpanID != "" {
			bySpan[n.SpanID] = n
		}
		predecessor[n], correlation[n] = l.PredecessorMessageGuid, l.CorrelationID
	}
	sortByStart(nodes)

	parentOf, linkOf := map[*TraceNode]*TraceNode{}, map[*TraceNode]string{}
	isAncestor := func(a, n *TraceNode) bool { // a is n or above n
		for ; n != nil; n = parentOf[n] {
			if n == a {
				return true
			}
		}
		return false
	}
	setParent := func(n, p *TraceNode, link string) bool {
		if p == nil || isAncestor(n, p) {
			return false
		}
		parentOf[n], linkOf[n] = p, link
		return true
	}
	for _, n := range nodes {
		if n.ParentSpanID == "" || !setParent(n, bySpan[n.ParentSpanID], HopSpan) {
			setParent(n, byGuid[predecessor[n]], HopPredecessor)
		}
	}
	if infer {
		for i, n := range nodes {
			if parentOf[n] != nil || i == 0 || n.LogStart == nil {
				continue
			}
			for j := i - 1; j >= 0; j-- {
				p := nodes[j]
				link := HopInferred
				if correlation[p] != correlation[n] {
					link = HopHeader
				}
				if p.LogStart != nil && p.LogStart.Before(*n.LogStart) && setParent(n, p, link) {
					break
				}
			}
		}
	}

	tree.Hops = []TraceHop{}
	pairs := map[string]bool{}
	for _, n := range nodes {
		p := parentOf[n]
		if p == nil {
			tree.Roots = append(tree.Roots, n)
			pairs[n.ArtifactID] = true
			continue
		}
		p.Children = append(p.Children, n)
		tree.Hops = append(tree.Hops, TraceHop{From: p.ArtifactID, To: n.ArtifactID, FromMessage: p.MessageGuid, ToMessage: n.MessageGuid, Link: linkOf[n]})
		pairs[p.ArtifactID+">"+n.ArtifactID] = true
	}
	tree.Messages = len(nodes)
	tree.PathKey = pathKey(pairs)

	var failed []*TraceNode
	for _, n := range nodes {
		if n.Status == "FAILED" || n.Status == "ESCALATED" {
			failed = append(failed, n)
		}
	}
	if len(failed) > 0 {
		f := failed[0]
		tree.FirstFailure = &TraceFailure{MessageGuid: f.MessageGuid, ArtifactID: f.ArtifactID, ErrorText: f.ErrorText}
	}
}

// pathKey identifies the route of a path independent of the messages: the
// same flows connected the same way give the same key.
func pathKey(pairs map[string]bool) string {
	sum := sha256.Sum256([]byte(strings.Join(sortedKeys(pairs), "\n")))
	return hex.EncodeToString(sum[:8])
}

// MessagePathQuery selects the path of one message.
type MessagePathQuery struct {
	MessageGuid string
	// MaxMessages caps the messages of the correlation ID that are read
	// (default 200).
	MaxMessages int
	Properties  TraceProperties
	// KeyHeaders are custom header properties that flows write (business
	// keys such as an order number). Messages of the scope with the same
	// value join the path, also when they have another correlation ID (the
	// message left the tenant and came back). They are looked up through
	// MessageProcessingLogCustomHeaderProperties (Scope narrows the matches);
	// tenants without it scan Scope, at most MaxScan messages (default 200),
	// and then need Scope.
	KeyHeaders []string
	Scope      ScanScope
	MaxScan    int
}

// MessagePathFor follows one message across flows: all messages with its
// correlation ID, linked by the tracer's span IDs when present, else by the
// predecessor message, else by start time (link inferred). Without a
// correlation ID the path is the message alone.
func MessagePathFor(ctx context.Context, exe *httpclnt.HTTPExecuter, q MessagePathQuery) (*TraceTree, error) {
	guid := strings.TrimSpace(q.MessageGuid)
	if guid == "" {
		return nil, output.Usagef("message_guid is required")
	}
	if q.MaxMessages <= 0 {
		q.MaxMessages = 200
	}
	props := q.Properties
	props.Span = cmp.Or(props.Span, DefaultTraceProperties.Span)
	props.Parent = cmp.Or(props.Parent, DefaultTraceProperties.Parent)

	l, err := cpi.NewMessageLogs(exe).Get(guid)
	if err != nil {
		return nil, err
	}
	if l == nil {
		return nil, output.Usagef("message %s not found", guid)
	}
	tree := &TraceTree{CorrelationID: l.CorrelationId, Source: "correlation_id", Roots: []*TraceNode{}}
	logs := []MessageLog{toMessageLog(*l)}
	if l.CorrelationId != "" {
		logs = nil
		for skip := 0; ; skip += MaxMessageLogs {
			page, err := QueryMessageLogs(exe, MessageLogQuery{CorrelationID: l.CorrelationId, Top: MaxMessageLogs, Skip: skip})
			if err != nil {
				return nil, err
			}
			logs = append(logs, page.Logs...)
			if len(logs) >= q.MaxMessages {
				tree.Truncated = len(logs) > q.MaxMessages || page.Total > len(logs)
				logs = logs[:min(len(logs), q.MaxMessages)]
				break
			}
			if len(page.Logs) < MaxMessageLogs || skip+len(page.Logs) >= page.Total {
				break
			}
		}
		if !slices.ContainsFunc(logs, func(m MessageLog) bool { return m.MessageGuid == guid }) {
			logs = append(logs, toMessageLog(*l))
		}
	} else {
		tree.Source = "message"
	}
	found, err := withHeaders(ctx, exe, logs)
	if err != nil {
		return nil, err
	}
	if len(q.KeyHeaders) > 0 {
		if err := joinByKeyHeaders(ctx, exe, tree, &found, q); err != nil {
			return nil, err
		}
	}
	linkTraceTree(tree, exe, found, props, true)
	return tree, nil
}

// joinByKeyHeaders adds the messages of the scope that carry the run's key
// header values, and the rest of their runs.
func joinByKeyHeaders(ctx context.Context, exe *httpclnt.HTTPExecuter, tree *TraceTree, found *[]ScannedLog, q MessagePathQuery) error {
	// the key may be written by any flow of the run, not only the start message's
	keys := map[string]string{}
	for _, name := range q.KeyHeaders {
		for _, l := range *found {
			if v := headerValue(l.Headers, name); v != "" {
				keys[name] = v
				break
			}
		}
	}
	tree.Keys = keys
	if len(keys) == 0 {
		return nil
	}
	res, err := keyHeaderMatches(ctx, exe, keys, q)
	if err != nil {
		return err
	}
	tree.Scanned, tree.Truncated = res.Scanned, tree.Truncated || res.Truncated
	have := map[string]bool{}
	for _, l := range *found {
		have[l.MessageGuid] = true
	}
	added := map[string]bool{}
	add := func(l ScannedLog) {
		if !have[l.MessageGuid] {
			*found = append(*found, l)
			have[l.MessageGuid], added[l.MessageGuid] = true, true
		}
	}
	groups := map[string]bool{tree.CorrelationID: true}
	for _, l := range res.Matches {
		add(l)
		if l.CorrelationID != "" && !groups[l.CorrelationID] {
			groups[l.CorrelationID] = true
			// the rest of that run, e.g. flows called without the header
			page, err := QueryMessageLogs(exe, MessageLogQuery{CorrelationID: l.CorrelationID, Top: MaxMessageLogs})
			if err != nil {
				return err
			}
			var more []MessageLog
			for _, m := range page.Logs {
				if !have[m.MessageGuid] && len(*found)+len(more) < q.MaxMessages {
					more = append(more, m)
				}
			}
			scanned, err := withHeaders(ctx, exe, more)
			if err != nil {
				return err
			}
			for _, m := range scanned {
				add(m)
			}
		}
	}
	if len(added) > 0 {
		tree.Source += "+header"
	}
	return nil
}

// keyHeaderMatches finds the messages carrying one of the key values:
// through MessageProcessingLogCustomHeaderProperties (one query per key, no
// scope needed; a scope given still narrows), else by scanning the scope.
func keyHeaderMatches(ctx context.Context, exe *httpclnt.HTTPExecuter, keys map[string]string, q MessagePathQuery) (*ScanResult, error) {
	var logs []MessageLog
	seen := map[string]bool{}
	for _, name := range sortedKeys(keys) {
		found, ok, err := findByHeader(exe, HeaderSearchQuery{Header: CustomHeaderFilter{Name: name, Value: keys[name]},
			ArtifactIDs: q.Scope.ArtifactIDs, PackageID: q.Scope.PackageID, Since: q.Scope.Since, Until: q.Scope.Until, Statuses: q.Scope.Statuses})
		if err != nil {
			return nil, err
		}
		if !ok {
			return scanKeyHeaders(ctx, exe, keys, q)
		}
		for _, l := range found {
			if !seen[l.MessageGuid] {
				seen[l.MessageGuid] = true
				logs = append(logs, l)
			}
		}
	}
	res := &ScanResult{}
	if len(logs) > q.MaxMessages {
		logs, res.Truncated = logs[:q.MaxMessages], true
	}
	// headers carry the span IDs used for linking
	matches, err := withHeaders(ctx, exe, logs)
	if err != nil {
		return nil, err
	}
	res.Matches = matches
	return res, nil
}

// scanKeyHeaders is the fallback of keyHeaderMatches: a client-side scan of
// the scope (at most MaxScan messages).
func scanKeyHeaders(ctx context.Context, exe *httpclnt.HTTPExecuter, keys map[string]string, q MessagePathQuery) (*ScanResult, error) {
	if q.MaxScan <= 0 {
		q.MaxScan = 200
	}
	return ScanByHeaders(ctx, exe, q.Scope, q.MaxScan, func(h []cpi.NameValue) bool {
		for name, v := range keys {
			if headerValue(h, name) == v {
				return true
			}
		}
		return false
	})
}

func sortByStart(nodes []*TraceNode) {
	sort.SliceStable(nodes, func(i, j int) bool {
		a, b := nodes[i].LogStart, nodes[j].LogStart
		switch {
		case a == nil:
			return false
		case b == nil:
			return true
		}
		return a.Before(*b)
	})
}

// NewTraceparent returns a W3C traceparent header value and its trace ID.
func NewTraceparent() (traceparent, traceID string) {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	traceID = hex.EncodeToString(b[:16])
	return "00-" + traceID + "-" + hex.EncodeToString(b[16:]) + "-01", traceID
}

var reTraceparent = regexp.MustCompile(`^[0-9a-f]{2}-([0-9a-f]{32})-[0-9a-f]{16}-[0-9a-f]{2}$`)

// TraceIDOf returns the trace ID of a traceparent value ("" if invalid).
func TraceIDOf(traceparent string) string {
	if m := reTraceparent.FindStringSubmatch(strings.ToLower(strings.TrimSpace(traceparent))); m != nil {
		return m[1]
	}
	return ""
}
