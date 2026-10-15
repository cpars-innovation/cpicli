package cpi

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseODataTime(t *testing.T) {
	ts, err := ParseODataTime("/Date(1700000000000)/")
	require.NoError(t, err)
	assert.Equal(t, time.UnixMilli(1700000000000).UTC(), ts)

	ts, err = ParseODataTime("/Date(1700000000000+0000)/")
	require.NoError(t, err)
	assert.Equal(t, int64(1700000000000), ts.UnixMilli())

	ts, err = ParseODataTime("")
	require.NoError(t, err)
	assert.True(t, ts.IsZero())

	ts, err = ParseODataTime("2026-01-02T03:04:05Z")
	require.NoError(t, err)
	assert.Equal(t, 2026, ts.Year())

	// without a zone (a trial runtime's DeployedOn, #18): UTC
	ts, err = ParseODataTime("2026-10-15T12:54:54.223")
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 10, 15, 12, 54, 54, 223_000_000, time.UTC), ts)
	ts, err = ParseODataTime("2026-10-15T12:54:54")
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 10, 15, 12, 54, 54, 0, time.UTC), ts)

	_, err = ParseODataTime("2026-10-15")
	assert.Error(t, err, "a date alone is not a timestamp")
	_, err = ParseODataTime("yesterday")
	assert.Error(t, err)
}

func TestParseHost(t *testing.T) {
	cases := map[string][3]any{
		"tenant.hana.ondemand.com":          {"https", "tenant.hana.ondemand.com", 443},
		"https://tenant.hana.ondemand.com/": {"https", "tenant.hana.ondemand.com", 443},
		"tenant.example.com:8443":           {"https", "tenant.example.com", 8443},
		"http://127.0.0.1:8081":             {"http", "127.0.0.1", 8081},
		"http://localhost:8081":             {"http", "localhost", 8081},
		// plain http is never used for remote hosts
		"http://tenant.hana.ondemand.com": {"https", "tenant.hana.ondemand.com", 443},
	}
	for in, want := range cases {
		scheme, host, port := ParseHost(in)
		assert.Equal(t, want, [3]any{scheme, host, port}, in)
	}
}
