package ops

import (
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

var (
	reGUIDLike   = regexp.MustCompile(`[A-Za-z0-9_-]{28}|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	reTimestamp  = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:?\d{2})?`)
	reLongNumber = regexp.MustCompile(`\d{6,}`)
)

// NormalizeError removes what differs between two occurrences of the same
// error: timestamps, message and other IDs, long numbers, whitespace.
func NormalizeError(s string) string {
	s = reTimestamp.ReplaceAllString(s, "<time>")
	s = reGUIDLike.ReplaceAllString(s, "<id>")
	s = reLongNumber.ReplaceAllString(s, "<n>")
	return strings.Join(strings.Fields(s), " ")
}

// ErrorFingerprint identifies an error independent of the message: the
// artifact, the failing step (model step ID, may be empty) and the
// normalized error text.
func ErrorFingerprint(artifactID, modelStepID, errorText string) string {
	sum := sha256.Sum256([]byte(artifactID + "|" + modelStepID + "|" + NormalizeError(errorText)))
	return hex.EncodeToString(sum[:8])
}

// MessageSummaryQuery selects the messages of a summary.
type MessageSummaryQuery struct {
	Since, Until time.Time
	// Artifacts are patterns (path.Match); empty: all flows.
	Artifacts []string
	// MaxMessages caps the messages read (0: 5000), ErrorSamples the error
	// texts fetched for fingerprints (0: 50).
	MaxMessages, ErrorSamples int
	// Buckets splits the window into equal time buckets for the error
	// groups' counts (0: 24).
	Buckets int
	// Graph adds the connections of the content graph (sends_to), counted
	// by correlation ID when the logs have no predecessor.
	Graph *Graph
}

// ErrorGroup is a group of failures with the same fingerprint. Count,
// Occurrences and Buckets cover the sampled failures (ErrorSamples).
type ErrorGroup struct {
	Fingerprint string `json:"fingerprint"`
	Artifact    string `json:"artifact"`
	// Step is the model step (.iflw element ID) where the error happened,
	// as the tenant's ErrorInformation reports it ("" if it doesn't).
	Step        string    `json:"step,omitempty"`
	Sample      string    `json:"sample"`
	Count       int       `json:"count"`
	First       time.Time `json:"first"`
	Last        time.Time `json:"last"`
	MessageGuid string    `json:"messageGuid"`
	// Occurrences are the sampled failures, newest first (at most
	// maxOccurrences).
	Occurrences []ErrorOccurrence `json:"occurrences"`
	// Buckets counts the failures per time bucket of the window, oldest
	// first (MessageSummary.BucketSeconds each).
	Buckets []int `json:"buckets"`
}

// ErrorOccurrence is one failure of an error group.
type ErrorOccurrence struct {
	Time        time.Time `json:"time"`
	MessageGuid string    `json:"messageGuid"`
}

// maxOccurrences bounds ErrorGroup.Occurrences.
const maxOccurrences = 100

// FlowSummary is the message statistics of one flow.
type FlowSummary struct {
	Artifact      string         `json:"artifact"`
	Package       string         `json:"package,omitempty"`
	Total         int            `json:"total"`
	ByStatus      map[string]int `json:"byStatus"`
	Failed        int            `json:"failed"`
	AvgDurationMs int64          `json:"avgDurationMs"`
	MaxDurationMs int64          `json:"maxDurationMs"`
	LastFailure   *time.Time     `json:"lastFailure,omitempty"`
}

// EdgeSummary is the traffic between two flows.
type EdgeSummary struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Messages that went from From to To, Failed of them failed in To.
	Messages int `json:"messages"`
	Failed   int `json:"failed"`
	// Source: predecessor (the message log links the calls) or correlation
	// (a graph edge, counted by shared correlation ID).
	Source string `json:"source"`
	Via    string `json:"via,omitempty"`
}

// MessageSummary is message volume and failures per flow and per
// connection over a time window.
type MessageSummary struct {
	Since     time.Time     `json:"since"`
	Until     time.Time     `json:"until"`
	Scanned   int           `json:"scanned"`
	Truncated bool          `json:"truncated,omitempty"`
	Flows     []FlowSummary `json:"flows"`
	Edges     []EdgeSummary `json:"edges"`
	Errors    []ErrorGroup  `json:"errors"`
	// BucketSeconds is the length of one bucket of ErrorGroup.Buckets.
	BucketSeconds int64 `json:"bucketSeconds"`
	// UnsampledFailures were not fingerprinted (ErrorSamples reached).
	UnsampledFailures int `json:"unsampledFailures,omitempty"`
}

// SummarizeMessages reads the message processing logs of a time window
// (newest first, up to MaxMessages) and summarizes them per flow, per
// connection between flows and per error fingerprint.
func SummarizeMessages(exe *httpclnt.HTTPExecuter, q MessageSummaryQuery) (*MessageSummary, error) {
	if q.MaxMessages <= 0 {
		q.MaxMessages = 5000
	}
	if q.ErrorSamples <= 0 {
		q.ErrorSamples = 50
	}
	if q.Buckets <= 0 {
		q.Buckets = 24
	}
	if q.Until.IsZero() {
		q.Until = time.Now()
	}
	if q.Since.IsZero() || !q.Since.Before(q.Until) {
		return nil, output.Usagef("since must be before until")
	}
	mpl := cpi.NewMessageLogs(exe)
	var logs []cpi.MessageLog
	truncated := false
	for skip := 0; ; skip += MaxMessageLogs {
		page, total, err := mpl.Query(cpi.MessageLogQuery{Since: q.Since, Until: q.Until, Top: MaxMessageLogs, Skip: skip})
		if err != nil {
			return nil, err
		}
		for _, l := range page {
			if MatchAny(q.Artifacts, l.ArtifactId) {
				logs = append(logs, l)
			}
		}
		if len(page) < MaxMessageLogs || skip+len(page) >= total {
			break
		}
		if skip+len(page) >= q.MaxMessages {
			truncated = true
			break
		}
	}
	res := &MessageSummary{Since: q.Since, Until: q.Until, Scanned: len(logs), Truncated: truncated,
		Flows: []FlowSummary{}, Edges: []EdgeSummary{}, Errors: []ErrorGroup{}}

	flows := map[string]*FlowSummary{}
	durations := map[string]int64{}
	byGUID := map[string]cpi.MessageLog{}
	byCorrelation := map[string][]cpi.MessageLog{}
	for _, l := range logs {
		byGUID[l.MessageGuid] = l
		if l.CorrelationId != "" {
			byCorrelation[l.CorrelationId] = append(byCorrelation[l.CorrelationId], l)
		}
		f := flows[l.ArtifactId]
		if f == nil {
			f = &FlowSummary{Artifact: l.ArtifactId, Package: l.PackageId, ByStatus: map[string]int{}}
			flows[l.ArtifactId] = f
		}
		f.Total++
		f.ByStatus[l.Status]++
		if !l.LogStart.IsZero() && !l.LogEnd.IsZero() {
			d := l.LogEnd.Sub(l.LogStart).Milliseconds()
			durations[l.ArtifactId] += d
			f.MaxDurationMs = max(f.MaxDurationMs, d)
		}
		if failedStatus(l.Status) {
			f.Failed++
			if t := l.LogStart; !t.IsZero() && (f.LastFailure == nil || t.After(*f.LastFailure)) {
				tt := t
				f.LastFailure = &tt
			}
		}
	}
	for id, f := range flows {
		if f.Total > 0 {
			f.AvgDurationMs = durations[id] / int64(f.Total)
		}
		res.Flows = append(res.Flows, *f)
	}
	sort.Slice(res.Flows, func(i, j int) bool {
		if res.Flows[i].Failed != res.Flows[j].Failed {
			return res.Flows[i].Failed > res.Flows[j].Failed
		}
		return res.Flows[i].Artifact < res.Flows[j].Artifact
	})

	// connections: the message log's predecessor links, else graph edges by
	// shared correlation ID
	edges := map[string]*EdgeSummary{}
	edge := func(from, to, source, via string) *EdgeSummary {
		key := from + "\x00" + to
		e := edges[key]
		if e == nil {
			e = &EdgeSummary{From: from, To: to, Source: source, Via: via}
			edges[key] = e
		}
		return e
	}
	for _, l := range logs {
		if p, ok := byGUID[l.PredecessorMessageGuid]; ok && l.PredecessorMessageGuid != "" && p.ArtifactId != l.ArtifactId {
			e := edge(p.ArtifactId, l.ArtifactId, "predecessor", "")
			e.Messages++
			if failedStatus(l.Status) {
				e.Failed++
			}
		}
	}
	if q.Graph != nil {
		for _, ge := range q.Graph.Edges {
			if ge.Type != EdgeSendsTo {
				continue
			}
			from, to := strings.TrimPrefix(ge.From, NodeIFlow+":"), strings.TrimPrefix(ge.To, NodeIFlow+":")
			if _, seen := edges[from+"\x00"+to]; seen || flows[from] == nil || flows[to] == nil {
				continue
			}
			e := edge(from, to, "correlation", strings.TrimPrefix(ge.Via, NodeEndpoint+":"))
			for _, group := range byCorrelation {
				var hasFrom bool
				var toMsg *cpi.MessageLog
				for i := range group {
					switch group[i].ArtifactId {
					case from:
						hasFrom = true
					case to:
						toMsg = &group[i]
					}
				}
				if hasFrom && toMsg != nil {
					e.Messages++
					if failedStatus(toMsg.Status) {
						e.Failed++
					}
				}
			}
		}
	}
	for _, k := range sortedKeys(edges) {
		if edges[k].Messages > 0 {
			res.Edges = append(res.Edges, *edges[k])
		}
	}

	// error groups from up to ErrorSamples error texts, newest first
	bucket := q.Until.Sub(q.Since) / time.Duration(q.Buckets)
	if bucket <= 0 {
		bucket = 1
	}
	res.BucketSeconds = int64(max(bucket.Seconds(), 1))
	groups := map[string]*ErrorGroup{}
	sampled := 0
	for _, l := range logs {
		if !failedStatus(l.Status) {
			continue
		}
		if sampled >= q.ErrorSamples {
			res.UnsampledFailures++
			continue
		}
		sampled++
		text, err := mpl.ErrorText(l.MessageGuid)
		if err != nil {
			text = ""
		}
		step := ""
		if info, err := mpl.ErrorInformation(l.MessageGuid); err == nil && info != nil {
			step = info.LastErrorModelStepID
		}
		fp := ErrorFingerprint(l.ArtifactId, step, text)
		g := groups[fp]
		if g == nil {
			sample, _ := truncate(NormalizeError(text), 500)
			g = &ErrorGroup{Fingerprint: fp, Artifact: l.ArtifactId, Step: step, Sample: sample, First: l.LogStart, Last: l.LogStart,
				MessageGuid: l.MessageGuid, Occurrences: []ErrorOccurrence{}, Buckets: make([]int, q.Buckets)}
			groups[fp] = g
		}
		g.Count++
		if len(g.Occurrences) < maxOccurrences {
			g.Occurrences = append(g.Occurrences, ErrorOccurrence{Time: l.LogStart, MessageGuid: l.MessageGuid})
		}
		if i := int(l.LogStart.Sub(q.Since) / bucket); i >= 0 && i < q.Buckets {
			g.Buckets[i]++
		} else if i >= q.Buckets {
			g.Buckets[q.Buckets-1]++ // at Until
		}
		if l.LogStart.Before(g.First) {
			g.First = l.LogStart
		}
		if l.LogStart.After(g.Last) {
			g.Last, g.MessageGuid = l.LogStart, l.MessageGuid
		}
	}
	for _, g := range groups {
		sort.SliceStable(g.Occurrences, func(i, j int) bool { return g.Occurrences[i].Time.After(g.Occurrences[j].Time) })
		res.Errors = append(res.Errors, *g)
	}
	sort.Slice(res.Errors, func(i, j int) bool {
		if res.Errors[i].Count != res.Errors[j].Count {
			return res.Errors[i].Count > res.Errors[j].Count
		}
		return res.Errors[i].Fingerprint < res.Errors[j].Fingerprint
	})
	return res, nil
}

func failedStatus(s string) bool {
	return slices.Contains(errorMessageStatuses, s)
}
