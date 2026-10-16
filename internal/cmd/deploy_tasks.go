package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Deployment defaults shared by deploy, undeploy, orchestrator, configure
// and the MCP server: a status check every 10 s; deploy, orchestrator and
// configure wait at most defaultDeployTimeout for all their deployments
// together; undeploy and the MCP server 30 checks per artifact.
const (
	defaultDeployChecks        = 30
	defaultDeployDelaySeconds  = 10
	defaultParallelDeployments = 5
	defaultDeployTimeout       = 10 * time.Minute
)

// deployWait is how deployments are watched: a status check every Interval
// until Timeout for all of them together, or, without a timeout, MaxChecks
// per artifact (the settings before deploy timeouts existed).
type deployWait struct {
	Interval  time.Duration
	Timeout   time.Duration
	MaxChecks int
}

// resolveDeployWait reads the timeout setting (flag timeoutFlag, config
// timeoutKey) and the legacy check settings: an explicit timeout wins; else
// checks × delay when the checks were set explicitly (the same total time
// as before); else the default timeout.
func resolveDeployWait(cmd *cobra.Command, timeoutFlag, timeoutKey string, checksSet bool, checks, delaySeconds int) (deployWait, error) {
	w := deployWait{Interval: time.Duration(delaySeconds) * time.Second, MaxChecks: checks}
	raw := ""
	if f := cmd.Flags().Lookup(timeoutFlag); f != nil && f.Changed {
		raw = f.Value.String()
	} else if timeoutKey != "" && viper.IsSet(timeoutKey) {
		raw = viper.GetString(timeoutKey)
	}
	switch {
	case raw != "":
		d, err := parseDeployTimeout(raw)
		if err != nil {
			return w, output.Usagef("--%s / %s: %v", timeoutFlag, timeoutKey, err)
		}
		w.Timeout = d
	case checksSet:
		w.Timeout = time.Duration(checks) * w.Interval // 0 with a zero delay: MaxChecks decides
	default:
		w.Timeout = defaultDeployTimeout
	}
	return w, nil
}

// parseDeployTimeout reads "15m", "90s", "1h30m" or plain seconds ("900").
func parseDeployTimeout(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if n, err := strconv.Atoi(s); err == nil {
		s += "s"
		if n < 0 {
			return 0, fmt.Errorf("%q is negative", s)
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration (e.g. 15m, 90s)", s)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%q must be positive", s)
	}
	return d, nil
}

// DeploymentTask is an artifact ready for deployment.
type DeploymentTask = ops.DeploymentTask

// deployTasks deploys the tasks of configure and orchestrator in two phases
// that share one deadline: first script collections, message and value
// mappings (flows reference them), then the integration flows. In each
// phase up to parallelDeployments artifacts are checked and triggered at a
// time, and all of them are watched together from one runtime list per
// interval until each has its outcome or the timeout ends. Results come
// back in task order.
func deployTasks(ctx context.Context, exe *httpclnt.HTTPExecuter, tasks []DeploymentTask,
	compareVersions bool, wait deployWait, parallelDeployments int) []ops.Result {

	var support, flows []ops.Artifact
	var supportIdx, flowIdx []int
	for i, t := range tasks {
		a := ops.Artifact{ID: t.ArtifactID, Type: ops.DesigntimeType(t.ArtifactType), PackageID: t.PackageID, Force: t.Force,
			AllowDowngrade: t.AllowDowngrade, ModifiedAt: t.ModifiedAt, Versioning: t.Versioning, ExpectedVersion: t.ExpectedVersion}
		if a.Type == "Integration" {
			flows, flowIdx = append(flows, a), append(flowIdx, i)
		} else {
			support, supportIdx = append(support, a), append(supportIdx, i)
		}
	}

	tenant := ops.NewTenant(exe)
	opts := ops.Options{Interval: wait.Interval, MaxChecks: wait.MaxChecks, CompareVersions: compareVersions, Parallelism: parallelDeployments}
	limit := fmt.Sprintf("%d checks per artifact", wait.MaxChecks)
	if wait.Timeout > 0 {
		opts.Deadline = time.Now().Add(wait.Timeout)
		limit = "at most " + wait.Timeout.String() + " in total"
	}
	started := time.Now()
	log.Info().Msgf("🚀 Deploying %d artifact(s): %d script collection(s) and mapping(s) first, then %d flow(s); up to %d triggered at a time, "+
		"all watched together (status every %s, %s)", len(tasks), len(support), len(flows), parallelDeployments, wait.Interval, limit)

	results := make([]ops.Result, len(tasks))
	for _, phase := range []struct {
		artifacts []ops.Artifact
		idx       []int
	}{{support, supportIdx}, {flows, flowIdx}} {
		if len(phase.artifacts) == 0 {
			continue
		}
		for i, r := range ops.Deploy(ctx, tenant, phase.artifacts, opts) {
			results[phase.idx[i]] = r
		}
	}
	logResults(results)
	logDeploySummary(results, time.Since(started))
	return results
}

// logDeploySummary is the last line of a deployment: how many artifacts
// ended how, and how long the whole deployment took.
func logDeploySummary(results []ops.Result, took time.Duration) {
	count := map[ops.Status]int{}
	for _, r := range results {
		count[r.Status]++
	}
	var parts []string
	for _, st := range []ops.Status{ops.StatusDeployed, ops.StatusSkipped, ops.StatusFailed, ops.StatusTimeout} {
		if count[st] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", count[st], strings.ToLower(string(st))))
		}
	}
	ev := log.Info()
	if count[ops.StatusFailed]+count[ops.StatusTimeout] > 0 {
		ev = log.Error()
	}
	ev.Msgf("Deployment of %d artifact(s) finished in %s: %s", len(results), took.Round(time.Second), strings.Join(parts, ", "))
}
