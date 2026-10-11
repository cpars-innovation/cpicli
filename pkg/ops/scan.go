package ops

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// The tenant cannot filter message processing logs by custom header
// properties ($filter on CustomHeaderProperties is rejected), and it is
// shared with other teams. Scans that need header values therefore run on the
// client, always scoped by artifact (or package) and a time window, capped,
// with bounded concurrency.

// ScanScope bounds a client-side scan of message processing logs.
type ScanScope struct {
	ArtifactIDs []string
	// PackageID adds the package's integration flows to ArtifactIDs.
	PackageID    string
	Since, Until time.Time
	Statuses     []string
}

// scanConcurrency is the number of parallel CustomHeaderProperties calls.
const scanConcurrency = 8

// ScannedLog is a message with its custom header properties.
type ScannedLog struct {
	MessageLog
	Headers []cpi.NameValue `json:"-"`
}

// errScopeRequired is the usage error for unscoped scans.
func errScopeRequired() error {
	return output.Usagef("scope required (artifact_ids or package_id, and since): the tenant cannot filter by custom headers, so messages are scanned")
}

// resolveScope returns the artifact IDs of the scope.
func resolveScope(exe *httpclnt.HTTPExecuter, scope ScanScope) ([]string, error) {
	if (len(scope.ArtifactIDs) == 0 && scope.PackageID == "") || scope.Since.IsZero() {
		return nil, errScopeRequired()
	}
	ids := slices.Clone(scope.ArtifactIDs)
	if scope.PackageID != "" {
		arts, err := ListArtifacts(exe, scope.PackageID)
		if err != nil {
			return nil, err
		}
		for _, a := range arts {
			if a.Type == "Integration" && !slices.Contains(ids, a.ID) {
				ids = append(ids, a.ID)
			}
		}
		if len(ids) == 0 {
			return nil, output.Usagef("package %s has no integration flows", scope.PackageID)
		}
	}
	return ids, nil
}

// listScoped lists the messages of the scope, newest first per artifact, at
// most max in total. truncated reports that more messages exist.
func listScoped(exe *httpclnt.HTTPExecuter, scope ScanScope, max int) (logs []MessageLog, truncated bool, err error) {
	ids, err := resolveScope(exe, scope)
	if err != nil {
		return nil, false, err
	}
	for _, id := range ids {
		for skip := 0; len(logs) < max; {
			top := min(MaxMessageLogs, max-len(logs))
			page, err := QueryMessageLogs(exe, MessageLogQuery{ArtifactID: id, Statuses: scope.Statuses, Since: scope.Since, Until: scope.Until, Top: top, Skip: skip})
			if err != nil {
				return nil, false, err
			}
			logs = append(logs, page.Logs...)
			skip += len(page.Logs)
			if len(page.Logs) < top || skip >= page.Total {
				break
			}
		}
		if len(logs) >= max {
			// more may exist in this or the remaining artifacts
			truncated = true
			break
		}
	}
	return logs, truncated, nil
}

// withHeaders fetches the custom header properties of logs concurrently.
func withHeaders(ctx context.Context, exe *httpclnt.HTTPExecuter, logs []MessageLog) ([]ScannedLog, error) {
	mpl := cpi.NewMessageLogs(exe)
	out := make([]ScannedLog, len(logs))
	errs := make([]error, len(logs))
	sem := make(chan struct{}, scanConcurrency)
	var wg sync.WaitGroup
	for i, l := range logs {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			headers, err := mpl.CustomHeaderProperties(l.MessageGuid)
			out[i], errs[i] = ScannedLog{MessageLog: l, Headers: headers}, err
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ScanResult is the outcome of a scoped scan.
type ScanResult struct {
	Matches []ScannedLog
	// Scanned is the number of messages whose headers were read.
	Scanned int
	// Truncated means the scan stopped at its cap: there may be more matches.
	Truncated bool
}

// ScanByHeaders lists the messages of the scope (at most max) and returns
// those whose custom header properties satisfy match.
func ScanByHeaders(ctx context.Context, exe *httpclnt.HTTPExecuter, scope ScanScope, max int, match func([]cpi.NameValue) bool) (*ScanResult, error) {
	exe = exe.WithContext(ctx)
	logs, truncated, err := listScoped(exe, scope, max)
	if err != nil {
		return nil, err
	}
	scanned, err := withHeaders(ctx, exe, logs)
	if err != nil {
		return nil, err
	}
	res := &ScanResult{Scanned: len(scanned), Truncated: truncated}
	for _, l := range scanned {
		if match(l.Headers) {
			res.Matches = append(res.Matches, l)
		}
	}
	return res, nil
}

func headerValue(headers []cpi.NameValue, name string) string {
	for _, h := range headers {
		if h.Name == name {
			return h.Value
		}
	}
	return ""
}

// CustomHeaderFilter selects messages by one custom header property.
type CustomHeaderFilter struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// maxHeaderScan caps a custom-header search.
const maxHeaderScan = 500

// QueryMessageLogsByHeader returns the messages of the scope whose custom
// header property Name equals Value, newest first, at most top. It scans at
// most top*10 messages (capped at 500).
func QueryMessageLogsByHeader(ctx context.Context, exe *httpclnt.HTTPExecuter, scope ScanScope, h CustomHeaderFilter, top int, includeErrors bool) (*MessageLogList, error) {
	exe = exe.WithContext(ctx)
	if h.Name == "" {
		return nil, output.Usagef("custom_header needs a name")
	}
	if top <= 0 {
		top = 20
	}
	res, err := ScanByHeaders(ctx, exe, scope, min(top*10, maxHeaderScan), func(headers []cpi.NameValue) bool {
		return headerValue(headers, h.Name) == h.Value
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(res.Matches, func(i, j int) bool { return logEnd(res.Matches[i].MessageLog).After(logEnd(res.Matches[j].MessageLog)) })
	list := &MessageLogList{Logs: []MessageLog{}, Scanned: res.Scanned, Truncated: res.Truncated,
		Filter: fmt.Sprintf("custom header %s = %q (client-side)", h.Name, h.Value)}
	mpl := cpi.NewMessageLogs(exe)
	for _, m := range res.Matches {
		if len(list.Logs) == top {
			list.Truncated = true
			break
		}
		l := m.MessageLog
		if includeErrors && slices.Contains(errorMessageStatuses, l.Status) {
			if text, err := mpl.ErrorText(l.MessageGuid); err == nil {
				l.ErrorText, l.ErrorTruncated = truncate(text, 0)
			}
		}
		list.Logs = append(list.Logs, l)
	}
	list.Total = len(list.Logs)
	return list, nil
}

func logEnd(l MessageLog) time.Time {
	if l.LogEnd != nil {
		return *l.LogEnd
	}
	if l.LogStart != nil {
		return *l.LogStart
	}
	return time.Time{}
}

// QueryPackageMessageLogs lists the messages of a package's integration flows
// (newest first, at most top), one query per flow.
func QueryPackageMessageLogs(exe *httpclnt.HTTPExecuter, scope ScanScope, top int) (*MessageLogList, error) {
	if top <= 0 {
		top = 20
	}
	if scope.Since.IsZero() {
		return nil, output.Usagef("package_id needs since (the tenant is shared: scans are time-boxed)")
	}
	logs, truncated, err := listScoped(exe, scope, min(top*10, maxHeaderScan))
	if err != nil {
		return nil, err
	}
	sort.SliceStable(logs, func(i, j int) bool { return logEnd(logs[i]).After(logEnd(logs[j])) })
	if len(logs) > top {
		logs, truncated = logs[:top], true
	}
	return &MessageLogList{Total: len(logs), Logs: logs, Truncated: truncated, Filter: "package " + scope.PackageID}, nil
}
