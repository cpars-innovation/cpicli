package testrun

import (
	"context"
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
	for _, c := range cases {
		if c.Config == nil || seen[c.Flow] || c.Skip != "" {
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
func (r *runner) applyConfig(ctx context.Context, flow string, values map[string]string) (*ConfigChange, func(), error) {
	change := &ConfigChange{Flow: flow, Applied: []string{}, Restored: []string{}}
	fail := func(err error) (*ConfigChange, func(), error) {
		change.Error = err.Error()
		return change, nil, err
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
		return change, nil, nil
	}

	upd, err := ops.UpdateConfiguration(r.exe, flow, "", set, false)
	if upd != nil {
		change.Applied = append(change.Applied, upd.Updated...)
	}
	restore := func() {
		fresh, cancel := context.WithTimeout(context.WithoutCancel(ctx), restoreTimeout)
		defer cancel()
		back := map[string]string{}
		for _, k := range change.Applied {
			back[k] = previous[k]
		}
		exe := r.exe.WithContext(fresh)
		res, err := ops.UpdateConfiguration(exe, flow, "", back, false)
		if res != nil {
			change.Restored = append(change.Restored, res.Updated...)
			change.Restored = append(change.Restored, res.Unchanged...)
			slices.Sort(change.Restored)
		}
		if err != nil {
			change.RestoreError = err.Error()
			return
		}
		change.Deploys++
		if err := r.deploy(fresh, flow); err != nil {
			change.RestoreError = "the previous values are set but the redeployment failed: " + err.Error()
		}
	}
	if err != nil {
		change.Error = err.Error()
		return change, restore, err
	}
	change.Deploys++
	if err := r.deploy(ctx, flow); err != nil {
		change.Error = err.Error()
		return change, restore, err
	}
	return change, restore, nil
}

// deploy redeploys flow and waits until it is started.
func (r *runner) deploy(ctx context.Context, flow string) error {
	results := ops.Deploy(ctx, ops.NewTenant(r.exe.WithContext(ctx)), []ops.Artifact{{ID: flow, Type: "Integration"}},
		ops.Options{Interval: r.o.DeployInterval, MaxChecks: r.o.DeployMaxChecks, Parallelism: 1})
	return ops.Err(results)
}
