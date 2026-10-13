package testrun

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// restoreTimeout bounds restoring a flow's configuration after the run
// (with a context of its own: it also runs after a cancellation).
const restoreTimeout = 15 * time.Minute

// configWarnings compares each flow's config.yaml with the tenant when the
// run does not apply it: differing keys (never values) are reported.
func (r *runner) configWarnings(cases []Case) []string {
	var warnings []string
	seen := map[string]bool{}
	type target struct {
		flow   string
		values map[string]string
	}
	var targets []target
	for _, c := range cases {
		if c.Skip != "" {
			continue
		}
		if c.Config != nil {
			targets = append(targets, target{c.Flow, c.Config})
		}
		for _, f := range sortedKeys(c.FlowConfig) {
			targets = append(targets, target{f, c.FlowConfig[f]})
		}
	}
	for _, t := range targets {
		c := struct {
			Flow   string
			Config map[string]string
		}{t.flow, t.values}
		if seen[c.Flow] {
			continue
		}
		seen[c.Flow] = true
		params, err := ops.GetConfiguration(r.exe, c.Flow, "")
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %s could not be compared with the tenant: %v", c.Flow, ConfigFile, err))
			continue
		}
		current := map[string]string{}
		for _, p := range params {
			current[p.Key] = p.Value
		}
		var differ []string
		for _, k := range sortedKeys(c.Config) {
			if v, ok := current[k]; !ok || v != c.Config[k] {
				differ = append(differ, k)
			}
		}
		if len(differ) > 0 {
			warnings = append(warnings, fmt.Sprintf("%s: the tenant's values of %s differ from %s; run with --apply-config (MCP apply_config) to test with them",
				c.Flow, strings.Join(differ, ", "), ConfigFile))
		}
	}
	return warnings
}

// applyConfig sets the test values of flow, redeploys it and returns a
// restore function that sets the previous values back and redeploys again.
// restore is set whenever something was changed, also with an error.
// deployPhase is deploy reported as the deploy phase of flow.
func (r *runner) deployPhase(ctx context.Context, flow string) error {
	r.phase(flow, "", PhaseDeploy, PhaseStarted, nil)
	err := r.deploy(ctx, flow)
	r.phaseEnd(flow, "", PhaseDeploy, err)
	return err
}

func (r *runner) applyConfig(ctx context.Context, flow string, values map[string]string) (change *ConfigChange, err error) {
	change = &ConfigChange{Flow: flow, Applied: []string{}, Restored: []string{}}
	r.phase(flow, "", PhaseApplyConfig, PhaseStarted, nil)
	defer func() { r.phaseEnd(flow, "", PhaseApplyConfig, err) }()
	fail := func(err error) (*ConfigChange, error) {
		change.Error = err.Error()
		return change, err
	}
	tenant := ops.NewTenant(r.exe)
	design, exists, err := tenant.DesigntimeVersion("Integration", flow)
	if err != nil {
		return fail(err)
	}
	if !exists {
		return fail(output.Usagef("integration flow %s does not exist", flow))
	}
	running, err := tenant.RuntimeArtifact(flow)
	if err != nil {
		return fail(err)
	}
	switch {
	case running == nil:
		return fail(output.Usagef("%s is not deployed", flow))
	case ops.IsDraftVersion(design):
		return fail(output.Usagef("%s is in draft on the tenant: save the version first (a redeployment would not run the tested version)", flow))
	case running.Version != design:
		return fail(output.Usagef("designtime version %s of %s differs from the running %s: a redeployment would deploy another version; deploy it first", design, flow, running.Version))
	}
	params, err := ops.GetConfiguration(r.exe, flow, "")
	if err != nil {
		return fail(err)
	}
	previous := map[string]string{}
	for _, p := range params {
		previous[p.Key] = p.Value
	}
	var unknown []string
	set := map[string]string{}
	for _, k := range sortedKeys(values) {
		v, ok := previous[k]
		switch {
		case !ok:
			unknown = append(unknown, k)
		case v == values[k]:
			change.Unchanged = append(change.Unchanged, k)
		default:
			set[k] = values[k]
		}
	}
	if len(unknown) > 0 {
		return fail(output.Usagef("%s has no parameter(s) %s", flow, strings.Join(unknown, ", ")))
	}
	if len(set) == 0 {
		return change, nil
	}

	upd, err := ops.UpdateConfiguration(r.exe, flow, "", set, false)
	if upd != nil {
		change.Applied = append(change.Applied, upd.Updated...)
	}
	// restore works on ch, the change or its copy in the Result (retry)
	change.restore = func(ctx context.Context, ch *ConfigChange) {
		fresh, cancel := context.WithTimeout(context.WithoutCancel(ctx), restoreTimeout)
		defer cancel()
		r.phase(flow, "", PhaseRestoreConfig, PhaseStarted, nil)
		back := map[string]string{}
		for _, k := range ch.Applied {
			back[k] = previous[k]
		}
		exe := r.exe.WithContext(fresh)
		res, err := ops.UpdateConfiguration(exe, flow, "", back, false)
		if res != nil {
			ch.Restored = append(ch.Restored, res.Updated...)
			ch.Restored = append(ch.Restored, res.Unchanged...)
			slices.Sort(ch.Restored)
		}
		if err != nil {
			ch.RestoreError = err.Error()
			r.phaseEnd(flow, "", PhaseRestoreConfig, err)
			return
		}
		ch.Deploys++
		if err := r.deployPhase(fresh, flow); err != nil {
			ch.RestoreError = "the previous values are set but the redeployment failed: " + err.Error()
			r.phaseEnd(flow, "", PhaseRestoreConfig, errors.New(ch.RestoreError))
			return
		}
		r.phaseEnd(flow, "", PhaseRestoreConfig, nil)
		ch.restore = nil // restored: nothing to retry
	}
	if err != nil {
		change.Error = err.Error()
		return change, err
	}
	change.Deploys++
	if err := r.deployPhase(ctx, flow); err != nil {
		change.Error = err.Error()
		return change, err
	}
	return change, nil
}

// deploy redeploys flow and waits until it is started.
func (r *runner) deploy(ctx context.Context, flow string) error {
	results := ops.Deploy(ctx, ops.NewTenant(r.exe.WithContext(ctx)), []ops.Artifact{{ID: flow, Type: "Integration"}},
		ops.Options{Interval: r.o.DeployInterval, MaxChecks: r.o.DeployMaxChecks, Parallelism: 1})
	return ops.Err(results)
}
