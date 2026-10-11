package ops

import (
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogLevelReverter(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": {Type: "Integration", DesignVersion: "1"}, "B": {Type: "Integration", DesignVersion: "1"}})
	now := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	rev := NewLogLevelReverter(mock.Executer())
	rev.Now = func() time.Time { return now }
	for _, id := range []string{"A", "B"} {
		_, err := SetLogLevel(mock.Executer(), LogLevelRequest{ArtifactID: id, Level: "TRACE"})
		require.NoError(t, err)
	}
	rev.Schedule(LogLevelRequest{ArtifactID: "A", Level: "TRACE"}, now.Add(10*time.Minute))
	rev.Schedule(LogLevelRequest{ArtifactID: "B"}, now.Add(time.Hour))
	assert.Equal(t, map[string]time.Time{"A": now.Add(10 * time.Minute), "B": now.Add(time.Hour)}, rev.Pending())

	rev.RunDue()
	assert.Equal(t, "TRACE", mock.LogLevels["A"]["mplLogLevel"], "before the deadline")
	now = now.Add(10 * time.Minute)
	rev.RunDue()
	assert.Equal(t, "INFO", mock.LogLevels["A"]["mplLogLevel"], "at the deadline")
	assert.Equal(t, "TRACE", mock.LogLevels["B"]["mplLogLevel"])

	// a zero time cancels; RevertAll reverts the rest
	rev.Schedule(LogLevelRequest{ArtifactID: "B"}, time.Time{})
	assert.Empty(t, rev.Pending())
	rev.Schedule(LogLevelRequest{ArtifactID: "B"}, now.Add(time.Hour))
	rev.RevertAll()
	assert.Equal(t, "INFO", mock.LogLevels["B"]["mplLogLevel"])
	assert.Empty(t, rev.Pending())
}
