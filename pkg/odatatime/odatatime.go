// Package odatatime parses the timestamps of SAP's OData v2 APIs. It has no
// dependencies outside the standard library, so packages such as pkg/scope
// can use it without linking the API client.
package odatatime

import (
	"regexp"
	"strconv"
	"time"
)

var odataDate = regexp.MustCompile(`^/Date\((-?\d+)([+-]\d{4})?\)/$`)

// Parse parses an OData v2 JSON date ("/Date(1700000000000)/"), epoch
// milliseconds, an RFC 3339 timestamp, or an ISO time without a zone (taken
// as UTC). An empty string yields the zero time.
func Parse(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if m := odataDate.FindStringSubmatch(value); m != nil {
		ms, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return time.Time{}, err
		}
		return time.UnixMilli(ms).UTC(), nil
	}
	// epoch milliseconds as a plain string (used by some entities)
	if ms, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.UnixMilli(ms).UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err == nil {
		return t, nil
	}
	// an ISO time without a zone (some runtimes answer DeployedOn so,
	// e.g. 2026-10-15T12:54:54.223): UTC, as OData's Edm.DateTime
	if t, err2 := time.Parse("2006-01-02T15:04:05.999999999", value); err2 == nil {
		return t.UTC(), nil
	}
	return time.Time{}, err
}
