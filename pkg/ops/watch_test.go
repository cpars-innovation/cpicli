package ops

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiffRuntime(t *testing.T) {
	at := t1
	prev := []RuntimeState{
		{ID: "A", Version: "1.0.0", Status: "STARTED", DeployedOn: t0},
		{ID: "B", Version: "1.0.0", Status: "STARTING", DeployedOn: t0},
		{ID: "C", Version: "1.0.0", Status: "STARTED", DeployedOn: t0},
		{ID: "D", Version: "1.0.0", Status: "STARTED", DeployedOn: t0},
	}
	cur := []RuntimeState{
		{ID: "A", Version: "1.0.0", Status: "STARTED", DeployedOn: t0},             // unchanged
		{ID: "B", Version: "1.0.0", Status: "STARTED", DeployedOn: t0},             // started
		{ID: "C", Version: "1.0.0", Status: "STARTING", DeployedOn: t1},            // redeployed, same version
		{ID: "E", Version: "2.0.0", Status: "ERROR", DeployedOn: t1},               // new, failed
		{ID: "F", Version: "1.0.0", Status: "STARTED", Runtime: "integrationcell"}, // new on a cell
	} // D undeployed
	var got []string
	for _, ev := range DiffRuntime(prev, cur, at) {
		got = append(got, ev.Type+" "+ev.ID+" "+ev.Status+" "+ev.Runtime)
		assert.Equal(t, at, ev.Time)
	}
	assert.Equal(t, []string{
		"started B STARTED ",
		"deployed C STARTING ",
		"undeployed D  ",
		"deployed E ERROR ",
		"error E ERROR ",
		"deployed F STARTED integrationcell",
	}, got)
}

// The watcher reads the runtime list once per interval and reports what
// changed, with the error text for a flow in ERROR.
func TestWatchRuntime(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", DesignVersion: "1.0.1", Runtime: &cpitest.Runtime{Version: "1.0.0", Status: "STARTED", DeployedOn: t0}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var events []RuntimeEvent
	done := make(chan error, 1)
	go func() {
		done <- WatchRuntime(ctx, mock.Executer(), WatchOptions{Interval: 5 * time.Millisecond, Initial: true}, func(ev RuntimeEvent) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, ev)
		})
	}()
	waitFor := func(n int) {
		t.Helper()
		require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(events) >= n }, 2*time.Second, 5*time.Millisecond)
	}
	waitFor(1)
	mock.SetRuntime("A", &cpitest.Runtime{Version: "1.0.1", Status: "ERROR", DeployedOn: t1})
	mock.SetErrorInfo("A", "Unable to resolve the script")
	waitFor(3)
	cancel()
	assert.ErrorIs(t, <-done, context.Canceled)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, EventPresent, events[0].Type)
	assert.Equal(t, EventDeployed, events[1].Type)
	assert.Equal(t, "1.0.0", events[1].PreviousVersion)
	assert.Equal(t, EventError, events[2].Type)
	assert.Equal(t, "Unable to resolve the script", events[2].Error)
	// only the list is read, plus the error text once
	assert.Equal(t, 1, mock.Count("GET /api/v1/IntegrationRuntimeArtifacts('A')"))
	assert.Equal(t, 1, mock.Count("GET /api/v1/IntegrationRuntimeArtifacts('A')/ErrorInformation"))
}

// Many deployments are watched from one list per interval, and a total
// timeout ends the waiting for all of them.
func TestDeploySharedListAndTimeout(t *testing.T) {
	artifacts := map[string]*cpitest.Artifact{}
	var ids []string
	for _, id := range []string{"A", "B", "C", "D", "E", "F", "G", "H"} {
		artifacts[id] = &cpitest.Artifact{Type: "Integration", DesignVersion: "1.0.0", TaskStatuses: []string{"SUCCESS"},
			AfterDeploy: []*cpitest.Runtime{{Version: "1.0.0", Status: "STARTING", DeployedOn: t1}, {Version: "1.0.0", Status: "STARTED", DeployedOn: t1}}}
		ids = append(ids, id)
	}
	results, mock := run(t, artifacts, ids, Options{Interval: 40 * time.Millisecond, Timeout: 5 * time.Second, Parallelism: 2})
	require.NoError(t, Err(results))
	assert.Zero(t, mock.Count("GET /api/v1/IntegrationRuntimeArtifacts('A')"))
	assert.Less(t, mock.Count("GET /api/v1/IntegrationRuntimeArtifacts"), 8*3, "fewer list reads than one per artifact and check")

	stuck := map[string]*cpitest.Artifact{"A": {Type: "Integration", DesignVersion: "1.0.0", TaskStatuses: []string{"SUCCESS"},
		AfterDeploy: []*cpitest.Runtime{{Version: "1.0.0", Status: "STARTING", DeployedOn: t1}}}}
	start := time.Now()
	results, _ = run(t, stuck, []string{"A"}, Options{Interval: 10 * time.Millisecond, Timeout: 80 * time.Millisecond, MaxChecks: 1000})
	assert.Less(t, time.Since(start), 2*time.Second, "the timeout ends the wait, not MaxChecks")
	assert.Equal(t, StatusTimeout, results[0].Status)
	assert.Contains(t, results[0].Error, "after the deploy timeout")
}
