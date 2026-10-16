package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveDeployWait(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	newCmd := func(args ...string) *cobra.Command {
		c := &cobra.Command{}
		c.Flags().String("deploy-timeout", "", "")
		require.NoError(t, c.Flags().Parse(args))
		return c
	}
	w, err := resolveDeployWait(newCmd(), "deploy-timeout", "orchestrator.deployTimeout", false, 30, 10)
	require.NoError(t, err)
	assert.Equal(t, defaultDeployTimeout, w.Timeout, "nothing set: the default")
	assert.Equal(t, 10*time.Second, w.Interval)

	w, _ = resolveDeployWait(newCmd(), "deploy-timeout", "orchestrator.deployTimeout", true, 60, 10)
	assert.Equal(t, 10*time.Minute, w.Timeout, "legacy retries × delay")

	w, _ = resolveDeployWait(newCmd("--deploy-timeout", "25m"), "deploy-timeout", "orchestrator.deployTimeout", true, 60, 10)
	assert.Equal(t, 25*time.Minute, w.Timeout, "an explicit timeout wins")

	viper.Set("orchestrator.deployTimeout", "900")
	w, _ = resolveDeployWait(newCmd(), "deploy-timeout", "orchestrator.deployTimeout", false, 30, 10)
	assert.Equal(t, 15*time.Minute, w.Timeout, "config in seconds")

	viper.Set("orchestrator.deployTimeout", "soon")
	_, err = resolveDeployWait(newCmd(), "deploy-timeout", "orchestrator.deployTimeout", false, 30, 10)
	assert.ErrorContains(t, err, "not a duration")

	w, _ = resolveDeployWait(newCmd(), "deploy-timeout", "", true, 2, 0)
	assert.Zero(t, w.Timeout, "zero delay: MaxChecks decides")
	assert.Equal(t, 2, w.MaxChecks)
}

// Script collections and mappings are deployed and confirmed before the
// flows; results come back in task order.
func TestDeployTasksPhases(t *testing.T) {
	started := []*cpitest.Runtime{{Version: "1.0.0", Status: "STARTED", DeployedOn: time.Now().Add(time.Hour)}}
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"Flow1": {Type: "Integration", DesignVersion: "1.0.0", TaskStatuses: []string{"SUCCESS"}, AfterDeploy: started},
		"Coll":  {Type: "ScriptCollection", DesignVersion: "1.0.0", TaskStatuses: []string{"SUCCESS"}, AfterDeploy: started},
		"Flow2": {Type: "Integration", DesignVersion: "1.0.0", TaskStatuses: []string{"SUCCESS"}, AfterDeploy: started},
	})
	tasks := []DeploymentTask{
		{ArtifactID: "Flow1", ArtifactType: "Integration", PackageID: "P1"},
		{ArtifactID: "Coll", ArtifactType: "ScriptCollection", PackageID: "P2"},
		{ArtifactID: "Flow2", ArtifactType: "Integration", PackageID: "P1"},
	}
	results := deployTasks(context.Background(), mock.Executer(), tasks, false, deployWait{Interval: time.Millisecond, Timeout: 5 * time.Second}, 5)
	require.Len(t, results, 3)
	for i, id := range []string{"Flow1", "Coll", "Flow2"} {
		assert.Equal(t, id, results[i].ID)
		assert.Equal(t, ops.StatusDeployed, results[i].Status, id)
	}
	var triggers []string
	for _, r := range mock.Requests() {
		if strings.HasPrefix(r, "POST /api/v1/Deploy") {
			triggers = append(triggers, r)
		}
	}
	require.Len(t, triggers, 3)
	assert.Contains(t, triggers[0], "Coll", "the script collection first")
}

func TestWebhook(t *testing.T) {
	var got struct {
		body      []byte
		sig, auth string
		event     string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.body, _ = io.ReadAll(r.Body)
		got.sig, got.auth, got.event = r.Header.Get("X-Cpictl-Signature"), r.Header.Get("Authorization"), r.Header.Get("X-Cpictl-Event")
	}))
	defer srv.Close()
	c := newRuntimesWatchCommand()
	require.NoError(t, c.Flags().Parse([]string{"--webhook", srv.URL, "--webhook-header", "Authorization: Bearer x", "--webhook-secret", "s3cret"}))
	h, err := newWebhook(c)
	require.NoError(t, err)
	h.send(context.Background(), ops.RuntimeEvent{Type: ops.EventStarted, ID: "A", Status: "STARTED"})

	var ev ops.RuntimeEvent
	require.NoError(t, json.Unmarshal(got.body, &ev))
	assert.Equal(t, "A", ev.ID)
	assert.Equal(t, "started", got.event)
	assert.Equal(t, "Bearer x", got.auth)
	assert.Equal(t, "sha256="+webhookSignature([]byte("s3cret"), got.body), got.sig)

	c = newRuntimesWatchCommand()
	require.NoError(t, c.Flags().Parse([]string{"--webhook", "http://studio.example.com/hook"}))
	_, err = newWebhook(c)
	assert.ErrorContains(t, err, "https")
}
