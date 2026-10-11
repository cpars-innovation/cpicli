package ops

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// Messages by custom header property: the tenant cannot filter
// MessageProcessingLogs by a custom header ($filter answers 400), but it can
// filter MessageProcessingLogCustomHeaderProperties by Name and Value and
// expand each match's Log. That finds the messages in one call, without a
// scope. Tenants without that entity set fall back to the scoped scan.

// HeaderSearchQuery selects messages by one custom header property value.
type HeaderSearchQuery struct {
	Header CustomHeaderFilter
	// Optional narrowing, applied to the matches. ArtifactIDs/PackageID and
	// Since are also the scope of the fallback scan.
	ArtifactIDs   []string
	PackageID     string
	Since, Until  time.Time
	Statuses      []string
	Top           int // default 20, at most MaxMessageLogs
	IncludeErrors bool
}

// headerSearchUnavailable reports that the tenant does not serve the custom
// header entity set (or not this query on it).
func headerSearchUnavailable(err error) bool {
	switch httpclnt.StatusCode(err) {
	case http.StatusNotFound, http.StatusNotImplemented, http.StatusBadRequest:
		return true
	}
	return false
}

// findByHeader returns the messages with the header value (server-side), in
// the narrowing of q. ok is false when the tenant cannot search this way.
func findByHeader(exe *httpclnt.HTTPExecuter, q HeaderSearchQuery) (logs []MessageLog, ok bool, err error) {
	found, err := cpi.NewMessageLogs(exe).FindByCustomHeader(q.Header.Name, q.Header.Value)
	if err != nil {
		if headerSearchUnavailable(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if len(found) == 0 {
		return nil, true, nil
	}
	var flows []string
	if len(q.ArtifactIDs) > 0 || q.PackageID != "" {
		flows = slices.Clone(q.ArtifactIDs)
		if q.PackageID != "" {
			arts, err := ListArtifacts(exe, q.PackageID)
			if err != nil {
				return nil, true, err
			}
			for _, a := range arts {
				flows = append(flows, a.ID)
			}
		}
	}
	for _, l := range found {
		m := toMessageLog(l)
		if flows != nil && !slices.Contains(flows, m.ArtifactID) {
			continue
		}
		if len(q.Statuses) > 0 && !slices.ContainsFunc(q.Statuses, func(s string) bool { return strings.EqualFold(s, m.Status) }) {
			continue
		}
		if !q.Since.IsZero() && logEnd(m).Before(q.Since) {
			continue
		}
		if !q.Until.IsZero() && m.LogStart != nil && m.LogStart.After(q.Until) {
			continue
		}
		logs = append(logs, m)
	}
	sort.SliceStable(logs, func(i, j int) bool { return logEnd(logs[i]).After(logEnd(logs[j])) })
	return logs, true, nil
}

// SearchMessagesByHeader returns the messages whose custom header property
// Name equals Value, newest first, at most Top. Without the entity set on the
// tenant it scans the scope instead (needs ArtifactIDs or PackageID, and
// Since; Filter then says "client-side").
func SearchMessagesByHeader(ctx context.Context, exe *httpclnt.HTTPExecuter, q HeaderSearchQuery) (*MessageLogList, error) {
	exe = exe.WithContext(ctx)
	if q.Header.Name == "" {
		return nil, output.Usagef("header name is required (name=value)")
	}
	if q.Top <= 0 {
		q.Top = 20
	}
	if q.Top > MaxMessageLogs {
		return nil, output.Usagef("top must be at most %d", MaxMessageLogs)
	}
	logs, ok, err := findByHeader(exe, q)
	if err != nil {
		return nil, err
	}
	if !ok {
		scope := ScanScope{ArtifactIDs: q.ArtifactIDs, PackageID: q.PackageID, Since: q.Since, Until: q.Until, Statuses: q.Statuses}
		if (len(scope.ArtifactIDs) == 0 && scope.PackageID == "") || scope.Since.IsZero() {
			return nil, output.Usagef("this tenant cannot search custom headers (MessageProcessingLogCustomHeaderProperties): give a scope to scan (artifact or package, and since)")
		}
		return QueryMessageLogsByHeader(ctx, exe, scope, q.Header, q.Top, q.IncludeErrors)
	}
	list := &MessageLogList{Total: len(logs), Logs: []MessageLog{}, Filter: fmt.Sprintf("custom header %s = %q", q.Header.Name, q.Header.Value)}
	mpl := cpi.NewMessageLogs(exe)
	for _, l := range logs {
		if len(list.Logs) == q.Top {
			list.Truncated = true
			break
		}
		if q.IncludeErrors && slices.Contains(errorMessageStatuses, l.Status) {
			if text, err := mpl.ErrorText(l.MessageGuid); err == nil {
				l.ErrorText, l.ErrorTruncated = truncate(text, 0)
			}
		}
		list.Logs = append(list.Logs, l)
	}
	return list, nil
}
