package ops

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type rtArtifact = cpi.RuntimeArtifact

var (
	t0 = time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	t1 = t0.Add(time.Hour)
)

func fastOpts() Options {
	return Options{Interval: time.Millisecond, MaxChecks: 5, Parallelism: 0}
}

func run(t *testing.T, artifacts map[string]*cpitest.Artifact, ids []string, opts Options) ([]Result, *cpitest.Tenant) {
	t.Helper()
	mock := cpitest.NewTenant(t, artifacts)
	var list []Artifact
	for _, id := range ids {
		list = append(list, Artifact{ID: id, Type: "Integration"})
	}
	return Deploy(context.Background(), NewTenant(mock.Executer()), list, opts), mock
}

func TestDeploy_PollsTaskStatusThenRuntime(t *testing.T) {
	results, mock := run(t, map[string]*cpitest.Artifact{
		"A": {
			Type: "Integration", DesignVersion: "1.0.1",
			TaskStatuses: []string{"DEPLOYING", "SUCCESS"},
			AfterDeploy:  []*cpitest.Runtime{{Version: "1.0.1", Status: "STARTING"}, {Version: "1.0.1", Status: "STARTED", DeployedOn: t1}},
		},
	}, []string{"A"}, fastOpts())

	require.Len(t, results, 1)
	assert.Equal(t, Result{ID: "A", Type: "Integration", TaskID: "task-A", Status: StatusDeployed, Version: "1.0.1", DesigntimeVersion: "1.0.1"}, results[0])
	assert.Equal(t, 2, mock.Count("GET /api/v1/BuildAndDeployStatus"))
	assert.NoError(t, Err(results))
}

func TestDeploy_Progress(t *testing.T) {
	var got []Progress
	opts := fastOpts()
	opts.Progress = func(p Progress) { got = append(got, p) }
	results, _ := run(t, map[string]*cpitest.Artifact{
		"A": {
			Type: "Integration", DesignVersion: "1.0.1",
			TaskStatuses: []string{"DEPLOYING", "SUCCESS"},
			AfterDeploy:  []*cpitest.Runtime{{Version: "1.0.1", Status: "STARTING"}, {Version: "1.0.1", Status: "STARTED", DeployedOn: t1}},
		},
	}, []string{"A"}, opts)
	require.NoError(t, Err(results))
	require.NotEmpty(t, got)
	assert.Equal(t, Progress{Step: 1, Total: 5, Message: "artifact A: build and deploy task DEPLOYING, check 1/5"}, got[0])
	for i, p := range got {
		assert.Equal(t, i+1, p.Step)
	}
	assert.Contains(t, got[len(got)-1].Message, "STARTED")
}

// TestDeploy_SameVersionRedeployIsNotFalseSuccess reproduces the race where the
// previous STARTED runtime artifact (same version) is still visible after the
// redeploy was triggered. It must not be reported as deployed until DeployedOn
// moves past the previous deployment.
func TestDeploy_SameVersionRedeployIsNotFalseSuccess(t *testing.T) {
	old := &cpitest.Runtime{Version: "1.0.0", Status: "STARTED", DeployedOn: t0}
	newer := &cpitest.Runtime{Version: "1.0.0", Status: "STARTED", DeployedOn: t1}

	t.Run("waits for the fresh deployment", func(t *testing.T) {
		results, mock := run(t, map[string]*cpitest.Artifact{
			"A": {Type: "Integration", DesignVersion: "1.0.0", Runtime: old,
				TaskStatuses: []string{"SUCCESS"},
				AfterDeploy:  []*cpitest.Runtime{old, old, newer}},
		}, []string{"A"}, fastOpts())
		assert.Equal(t, StatusDeployed, results[0].Status)
		// 1 baseline read + 3 polls (2 stale)
		assert.Equal(t, 4, mock.Count("GET /api/v1/IntegrationRuntimeArtifacts('A')"))
	})

	t.Run("times out if only the old deployment is visible", func(t *testing.T) {
		results, _ := run(t, map[string]*cpitest.Artifact{
			"A": {Type: "Integration", DesignVersion: "1.0.0", Runtime: old,
				AfterDeploy: []*cpitest.Runtime{old}},
		}, []string{"A"}, fastOpts())
		assert.Equal(t, StatusTimeout, results[0].Status)
		assert.Contains(t, results[0].Error, "remained unfinished after 5 checks")
	})

	t.Run("a stale ERROR from the previous deployment is not a failure", func(t *testing.T) {
		oldErr := &cpitest.Runtime{Version: "1.0.0", Status: "ERROR", DeployedOn: t0}
		results, _ := run(t, map[string]*cpitest.Artifact{
			"A": {Type: "Integration", DesignVersion: "1.0.0", Runtime: oldErr,
				AfterDeploy: []*cpitest.Runtime{oldErr, newer}},
		}, []string{"A"}, fastOpts())
		assert.Equal(t, StatusDeployed, results[0].Status)
	})
}

func TestDeploy_TaskFailure(t *testing.T) {
	results, _ := run(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", DesignVersion: "1", TaskStatuses: []string{"DEPLOYING", "FAIL"}, ErrorInfo: "boom"},
	}, []string{"A"}, fastOpts())
	assert.Equal(t, StatusFailed, results[0].Status)
	assert.Contains(t, results[0].Error, "ended with status FAIL")
	assert.Contains(t, results[0].Error, "boom")
}

// A first deploy on a fresh tenant: the task ends FAIL, but the runtime runs
// the new version (#24): deployed, with a warning naming both.
func TestDeploy_TaskFailureButRuntimeStarted(t *testing.T) {
	results, _ := run(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", DesignVersion: "1", TaskStatuses: []string{"FAIL"}, ErrorInfo: "boom",
			AfterDeploy: []*cpitest.Runtime{{Version: "1", Status: "STARTED", DeployedOn: t1}}},
	}, []string{"A"}, fastOpts())
	assert.Equal(t, StatusDeployed, results[0].Status)
	require.Len(t, results[0].Warnings, 1)
	assert.Contains(t, results[0].Warnings[0], "ended with status FAIL, but the runtime runs A 1 (STARTED)")
	assert.Contains(t, results[0].Warnings[0], "boom")
}

func TestDeploy_RuntimeError(t *testing.T) {
	results, _ := run(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", DesignVersion: "1", ErrorInfo: "invalid mapping",
			AfterDeploy: []*cpitest.Runtime{{Version: "1", Status: "ERROR", DeployedOn: t1}}},
	}, []string{"A"}, fastOpts())
	assert.Equal(t, StatusFailed, results[0].Status)
	assert.Contains(t, results[0].Error, "invalid mapping")
}

func TestDeploy_FallsBackToRuntimeWhenTaskStatusUnavailable(t *testing.T) {
	results, mock := run(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", DesignVersion: "1", // no TaskStatuses: endpoint returns 404
			AfterDeploy: []*cpitest.Runtime{{Version: "1", Status: "STARTED", DeployedOn: t1}}},
	}, []string{"A"}, fastOpts())
	assert.Equal(t, StatusDeployed, results[0].Status)
	assert.Equal(t, 1, mock.Count("GET /api/v1/BuildAndDeployStatus"))
}

func TestDeploy_CompareVersionsSkips(t *testing.T) {
	opts := fastOpts()
	opts.CompareVersions = true
	results, mock := run(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", DesignVersion: "2", Runtime: &cpitest.Runtime{Version: "2", Status: "STARTED", DeployedOn: t0}},
	}, []string{"A"}, opts)
	assert.Equal(t, StatusSkipped, results[0].Status)
	assert.Zero(t, mock.Count("POST "))
}

func TestDeploy_MultipleArtifactsKeepOrderAndIsolateFailures(t *testing.T) {
	started := []*cpitest.Runtime{{Version: "1", Status: "STARTED", DeployedOn: t1}}
	results, _ := run(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", DesignVersion: "1", AfterDeploy: started},
		"B": {Type: "Integration"}, // designtime missing
		"C": {Type: "Integration", DesignVersion: "1", DeployStatusCode: http.StatusInternalServerError},
		"D": {Type: "Integration", DesignVersion: "1", AfterDeploy: started},
	}, []string{"A", "B", "C", "D"}, fastOpts())

	require.Len(t, results, 4)
	var got []string
	for _, r := range results {
		got = append(got, r.ID+"="+string(r.Status))
	}
	assert.Equal(t, []string{"A=DEPLOYED", "B=FAILED", "C=FAILED", "D=DEPLOYED"}, got)
	assert.Contains(t, results[1].Error, "does not exist")

	var depErr *Error
	require.ErrorAs(t, Err(results), &depErr)
	assert.Len(t, depErr.Failed(), 2)
}

func TestDeploy_AuthErrorFailsFast(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{})
	mock.StatusOverride = http.StatusUnauthorized
	results := Deploy(context.Background(), NewTenant(mock.Executer()), []Artifact{{ID: "A", Type: "Integration"}}, fastOpts())
	assert.Equal(t, StatusFailed, results[0].Status)
	assert.Contains(t, results[0].Error, "401")
}

func TestDeploy_InvalidType(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{})
	results := Deploy(context.Background(), NewTenant(mock.Executer()), []Artifact{{ID: "A", Type: "Bogus"}}, fastOpts())
	assert.Equal(t, StatusFailed, results[0].Status)
	assert.Empty(t, mock.Requests())
}

func TestIsFresh(t *testing.T) {
	before := &rtArtifact{Version: "1", Status: "STARTED", DeployedOn: t0}
	now := t1
	cases := []struct {
		name          string
		rt, before    *rtArtifact
		taskConfirmed bool
		want          bool
	}{
		{"no previous deployment", &rtArtifact{Version: "1", DeployedOn: t0}, nil, false, true},
		{"newer DeployedOn", &rtArtifact{Version: "1", DeployedOn: t1}, before, false, true},
		{"same DeployedOn", &rtArtifact{Version: "1", DeployedOn: t0}, before, false, false},
		{"no baseline timestamp, after trigger", &rtArtifact{Version: "1", DeployedOn: now.Add(-time.Minute)}, &rtArtifact{Version: "1"}, false, true},
		{"no baseline timestamp, long before trigger", &rtArtifact{Version: "1", DeployedOn: t0}, &rtArtifact{Version: "1"}, false, false},
		{"no timestamps, version changed", &rtArtifact{Version: "2"}, &rtArtifact{Version: "1"}, false, true},
		{"no timestamps, same version, task confirmed", &rtArtifact{Version: "1"}, &rtArtifact{Version: "1"}, true, true},
		{"no timestamps, same version", &rtArtifact{Version: "1"}, &rtArtifact{Version: "1"}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, isFresh(c.rt, c.before, now, 2*time.Minute, c.taskConfirmed))
		})
	}
}

func TestUndeploy(t *testing.T) {
	deployed := &cpitest.Runtime{Version: "1", Status: "STARTED", DeployedOn: t0}
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"A": {Runtime: deployed, UndeployAfter: 2},
		"B": {}, // not deployed
		"C": {Runtime: deployed, UndeployAfter: -1},
	})
	results := Undeploy(context.Background(), NewTenant(mock.Executer()),
		[]Artifact{{ID: "A"}, {ID: "B"}, {ID: "C"}}, fastOpts())

	assert.Equal(t, StatusUndeployed, results[0].Status)
	assert.Equal(t, "1", results[0].Version)
	assert.Equal(t, StatusNotDeployed, results[1].Status)
	assert.Equal(t, StatusTimeout, results[2].Status)
	assert.Equal(t, 0, mock.Count("DELETE /api/v1/IntegrationRuntimeArtifacts('B')"))
	assert.Equal(t, 1, mock.Count("DELETE /api/v1/IntegrationRuntimeArtifacts('A')"))
}

func TestDeploy_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", DesignVersion: "1", AfterDeploy: []*cpitest.Runtime{{Status: "STARTING"}}},
	})
	opts := fastOpts()
	opts.Interval = time.Hour
	results := Deploy(ctx, NewTenant(mock.Executer()), []Artifact{{ID: "A", Type: "Integration"}}, opts)
	assert.Equal(t, StatusFailed, results[0].Status)
	assert.Contains(t, results[0].Error, "context canceled")
}

// All write requests of a run share one CSRF token, and an expired token is
// refreshed transparently (the mock tenant enforces CSRF for Basic Auth).
func TestDeploy_CSRFTokenSharedAndRefreshed(t *testing.T) {
	started := []*cpitest.Runtime{{Version: "1", Status: "STARTED", DeployedOn: t1}}
	artifacts := map[string]*cpitest.Artifact{}
	var list []Artifact
	for _, id := range []string{"A", "B", "C", "D"} {
		artifacts[id] = &cpitest.Artifact{Type: "Integration", DesignVersion: "1", AfterDeploy: started}
		list = append(list, Artifact{ID: id, Type: "Integration"})
	}
	mock := cpitest.NewTenant(t, artifacts)
	tenant := NewTenant(mock.Executer())

	results := Deploy(context.Background(), tenant, list, fastOpts())
	require.NoError(t, Err(results))
	assert.Equal(t, 1, mock.CSRFFetches(), "one token for four deploy triggers")

	mock.ExpireCSRF()
	results = Undeploy(context.Background(), tenant, list[:1], fastOpts())
	require.NoError(t, Err(results))
	assert.Equal(t, 2, mock.CSRFFetches(), "expired token refreshed once")
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"1.0.10", "1.0.9", 1},
		{"1.0.9", "1.0.10", -1},
		{"1.0.0", "1.0.0", 0},
		{"1.0", "1.0.0", 0},
		{"2", "1.9.9", 1},
		{"1.0.01", "1.0.1", 0},
		{"1.0.0-SNAPSHOT", "1.0.0", 1},
		{"1.0.beta", "1.0.9", 1},
		{"1.0.alpha", "1.0.beta", -1},
		{"Active", "1.0.10", 1},
		{"99999999999999999999.0", "1.0", 1},
	} {
		assert.Equal(t, c.want, CompareVersions(c.a, c.b), "%s vs %s", c.a, c.b)
	}
}

func TestDeploy_DowngradeGuard(t *testing.T) {
	newer := &cpitest.Runtime{Version: "1.0.10", Status: "STARTED", DeployedOn: t0}
	results, mock := run(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", DesignVersion: "1.0.9", Runtime: newer},
	}, []string{"A"}, fastOpts())
	require.Len(t, results, 1)
	assert.Equal(t, StatusFailed, results[0].Status)
	assert.Contains(t, results[0].Error, "designtime version 1.0.9 is older than running 1.0.10")
	assert.Equal(t, "1.0.9", results[0].DesigntimeVersion)
	assert.Equal(t, "1.0.10", results[0].RuntimeVersion)
	assert.Equal(t, 0, mock.Count("POST /api/v1/DeployIntegrationDesigntimeArtifact"), "no deploy may be triggered")

	opts := fastOpts()
	opts.AllowDowngrade = true
	results, mock = run(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", DesignVersion: "1.0.9", Runtime: newer, TaskStatuses: []string{"SUCCESS"},
			AfterDeploy: []*cpitest.Runtime{{Version: "1.0.9", Status: "STARTED", DeployedOn: t1}}},
	}, []string{"A"}, opts)
	assert.Equal(t, StatusDeployed, results[0].Status, results[0].Error)
	assert.Equal(t, 1, mock.Count("POST /api/v1/DeployIntegrationDesigntimeArtifact"))

	results, _ = run(t, map[string]*cpitest.Artifact{
		"B": {Type: "Integration", DesignVersion: "1.0.0", TaskStatuses: []string{"SUCCESS"},
			AfterDeploy: []*cpitest.Runtime{{Version: "1.0.0", Status: "STARTED", DeployedOn: t1}}},
	}, []string{"B"}, fastOpts())
	assert.Equal(t, StatusDeployed, results[0].Status, "not deployed yet: no guard")
	assert.Empty(t, results[0].RuntimeVersion)
}

// The case from a configure pipeline: the runtime (1.0.15) came from a
// manual deployment of an older build; designtime 1.0.13 holds the newer
// content. Whether it was changed after that deployment decides.
func TestDeploy_DowngradeRules(t *testing.T) {
	running := func() *cpitest.Runtime { return &cpitest.Runtime{Version: "1.0.15", Status: "STARTED", DeployedOn: t0} }
	after := []*cpitest.Runtime{{Version: "1.0.13", Status: "STARTED", DeployedOn: t1.Add(time.Hour)}}
	deployOne := func(a *cpitest.Artifact, art Artifact, opts Options) (Result, *cpitest.Tenant) {
		t.Helper()
		a.Type, a.TaskStatuses, a.AfterDeploy = "Integration", []string{"SUCCESS"}, after
		mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": a})
		art.ID, art.Type = "A", "Integration"
		res := Deploy(context.Background(), NewTenant(mock.Executer()), []Artifact{art}, opts)
		require.Len(t, res, 1)
		return res[0], mock
	}

	t.Run("modified after the deployment: deploy", func(t *testing.T) {
		r, mock := deployOne(&cpitest.Artifact{DesignVersion: "1.0.13", Runtime: running(), ModifiedAt: t1}, Artifact{}, fastOpts())
		assert.Equal(t, StatusDeployed, r.Status, r.Error)
		assert.Equal(t, RuleModifiedAfterDeployment, r.Rule)
		assert.Equal(t, 1, mock.Count("POST /api/v1/DeployIntegrationDesigntimeArtifact"))
	})
	t.Run("not modified since the deployment: refuse", func(t *testing.T) {
		r, mock := deployOne(&cpitest.Artifact{DesignVersion: "1.0.13", Runtime: running(), ModifiedAt: t0.Add(-time.Hour)}, Artifact{}, fastOpts())
		assert.Equal(t, StatusFailed, r.Status)
		assert.Equal(t, RuleVersion, r.Rule)
		assert.Contains(t, r.Error, "was not modified since that deployment")
		assert.Contains(t, r.Error, "allowDowngrade: true")
		assert.Equal(t, 0, mock.Count("POST /api/v1/DeployIntegrationDesigntimeArtifact"))
	})
	t.Run("no modification time: version rule", func(t *testing.T) {
		r, _ := deployOne(&cpitest.Artifact{DesignVersion: "1.0.13", Runtime: running()}, Artifact{}, fastOpts())
		assert.Equal(t, StatusFailed, r.Status)
		assert.Equal(t, RuleVersion, r.Rule)
		assert.Contains(t, r.Error, "does not report when the designtime artifact was modified")
	})
	t.Run("no deployment time: version rule", func(t *testing.T) {
		rt := running()
		rt.DeployedOn = time.Time{}
		r, _ := deployOne(&cpitest.Artifact{DesignVersion: "1.0.13", Runtime: rt, ModifiedAt: t1}, Artifact{}, fastOpts())
		assert.Equal(t, StatusFailed, r.Status)
		assert.Contains(t, r.Error, "does not report its deployment time")
	})
	t.Run("per artifact allowDowngrade", func(t *testing.T) {
		r, _ := deployOne(&cpitest.Artifact{DesignVersion: "1.0.13", Runtime: running()}, Artifact{AllowDowngrade: true}, fastOpts())
		assert.Equal(t, StatusDeployed, r.Status, r.Error)
		assert.Equal(t, RuleAllowDowngrade, r.Rule)
	})
	t.Run("a modification time given by the caller wins", func(t *testing.T) {
		// configure passes the time from before it wrote parameters
		before := t0.Add(-time.Hour)
		r, _ := deployOne(&cpitest.Artifact{DesignVersion: "1.0.13", Runtime: running(), ModifiedAt: t1}, Artifact{ModifiedAt: &before}, fastOpts())
		assert.Equal(t, StatusFailed, r.Status)
		assert.Equal(t, RuleVersion, r.Rule)
	})
	t.Run("newer designtime version: version rule", func(t *testing.T) {
		r, _ := deployOne(&cpitest.Artifact{DesignVersion: "1.0.16", Runtime: running()}, Artifact{}, fastOpts())
		assert.Equal(t, StatusDeployed, r.Status, r.Error)
		assert.Equal(t, RuleVersion, r.Rule)
	})
}

// A flow bound to an integration cell deploys there; the public runtime API
// lists only Cloud Integration, so the deploy finds it in the Web UI's
// runtime list (#24).
func TestDeploy_OnIntegrationCell(t *testing.T) {
	now := time.Now()
	results, _ := run(t, map[string]*cpitest.Artifact{
		"Cell": {Type: "Integration", DesignVersion: "1", TaskStatuses: []string{"SUCCESS"}, RuntimeLocation: "integrationcell",
			AfterDeploy: []*cpitest.Runtime{{Version: "1", Status: "STARTED", DeployedOn: now, Location: "integrationcell"}}},
		"Broken": {Type: "Integration", DesignVersion: "1", TaskStatuses: []string{"SUCCESS"}, RuntimeLocation: "integrationcell",
			AfterDeploy: []*cpitest.Runtime{{Version: "1", Status: "ERROR", DeployedOn: now, Location: "integrationcell"}}},
		"Old": {Type: "Integration", DesignVersion: "2", TaskStatuses: []string{"SUCCESS"}, RuntimeLocation: "integrationcell",
			AfterDeploy: []*cpitest.Runtime{{Version: "1", Status: "STARTED", DeployedOn: t0, Location: "integrationcell"}}},
		"Nowhere": {Type: "Integration", DesignVersion: "1", TaskStatuses: []string{"SUCCESS"}, AfterDeploy: []*cpitest.Runtime{nil}},
	}, []string{"Cell", "Broken", "Old", "Nowhere"}, fastOpts())
	assert.Equal(t, StatusDeployed, results[0].Status, results[0].Error)
	assert.Equal(t, "integrationcell", results[0].RuntimeLocation)
	assert.Equal(t, "1", results[0].Version)

	assert.Equal(t, StatusFailed, results[1].Status)
	assert.Contains(t, results[1].Error, "deployment on runtime integrationcell unsuccessful, ended with status ERROR (FAILED)")

	assert.Equal(t, StatusTimeout, results[2].Status, "the cell still shows the previous deployment")
	assert.Contains(t, results[2].Error, "the build and deploy task succeeded, but Old is not on the Cloud Integration runtime and runtime integrationcell still shows the deployment of 2026-01-01T10:00:00Z (version 1)")
	assert.Contains(t, results[3].Error, "Nowhere is not on the Cloud Integration runtime and on no other runtime either")
}
