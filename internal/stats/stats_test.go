package stats

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOffUnderTestsWithoutFile(t *testing.T) {
	t.Setenv("CPICTL_STATS_FILE", "")
	assert.Empty(t, Path(), "go test never writes to the home directory")
	t.Setenv("CPICTL_STATS_FILE", "/x/stats.jsonl")
	assert.Equal(t, "/x/stats.jsonl", Path())
	t.Setenv("CPICTL_STATS", "off")
	assert.Empty(t, Path())
}

func TestRecordAndSummarize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "stats.jsonl")
	t.Setenv("CPICTL_STATS_FILE", path)
	for _, ms := range []int{100, 200, 300, 400, 5000} {
		Record("snapshot", SourceCLI, 0, time.Duration(ms)*time.Millisecond)
	}
	Record("snapshot", SourceCLI, 7, time.Second)
	Record("deploy", SourceMCP, 0, 50*time.Millisecond)
	Record("", SourceCLI, 0, time.Second) // unnamed: not recorded

	info, err := os.Stat(path)
	require.NoError(t, err)
	if runtime.GOOS != "windows" { // NTFS has no POSIX modes
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}

	entries, err := Read(path, time.Time{})
	require.NoError(t, err)
	require.Len(t, entries, 7)
	rows := Summarize(entries)
	require.Len(t, rows, 2)
	assert.Equal(t, Row{Command: "snapshot", Source: "cli", Runs: 6, Failed: 1, P50Ms: 300, P95Ms: 5000, MaxMs: 5000, TotalMs: 7000, LastUsed: rows[0].LastUsed}, rows[0])
	assert.Equal(t, "deploy", rows[1].Command)
	assert.Equal(t, "mcp", rows[1].Source)

	entries, err = Read(path, time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// The file never grows much past MaxBytes; the newest entries survive.
func TestFileIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.jsonl")
	t.Setenv("CPICTL_STATS_FILE", path)
	// pre-fill just below the limit, then append past it
	line := `{"t":"2026-01-01T00:00:00Z","cmd":"old","src":"cli","exit":0,"ms":1}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat(line, MaxBytes/len(line))), 0o600))
	for range 100 {
		Record("new", SourceCLI, 0, time.Millisecond)
	}
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.LessOrEqual(t, info.Size(), int64(MaxBytes))
	assert.Greater(t, info.Size(), int64(MaxBytes/4))

	entries, err := Read(path, time.Time{})
	require.NoError(t, err)
	assert.Equal(t, "new", entries[len(entries)-1].Command)
	news := 0
	for _, e := range entries {
		if e.Command == "new" {
			news++
		}
	}
	assert.Equal(t, 100, news, "nothing recent is lost")
	assert.Equal(t, "old", entries[0].Command, "the first line after compaction is a whole entry")
}

func TestReadSkipsBrokenLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("garbage\n{\"cmd\":\"x\",\"src\":\"cli\"}\n{\"t\":1}\n"), 0o600))
	entries, err := Read(path, time.Time{})
	require.NoError(t, err)
	assert.Len(t, entries, 1)

	entries, err = Read(filepath.Join(t.TempDir(), "missing"), time.Time{})
	require.NoError(t, err)
	assert.Empty(t, entries)
}
