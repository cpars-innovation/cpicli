package cmd

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/deploy"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/internal/str"
	"github.com/cpars-innovation/cpicli/pkg/catalog"
	"github.com/cpars-innovation/cpicli/pkg/lint"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// lintResult is the JSON result of lint.
type lintResult struct {
	*lint.Result
	Fix      *lint.FixResult `json:"fix,omitempty"`
	Baseline string          `json:"baseline,omitempty"`
	FailOn   string          `json:"failOn,omitempty"`
}

func NewLintCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "lint",
		Short: "Check integration flows for reuse, dead weight, Partner Directory candidates and best practices (local files)",
		Long: `Check the integration flows of a content tree (<package>/<artifact>, as snapshot
writes it) and report findings with a rule, a severity and a suggestion:

  reuse              scripts in several flows (-> script collection), mappings in
                     several flows, missing script collections
  partner-directory  routers on many literal values, lookup tables in scripts,
                     flows deployed several times with different configOverrides
  dead-weight        unconnected steps, unused scripts, resources and parameters,
                     content modifiers that do nothing, properties nobody reads
  simplify           content modifiers in a row, XML/JSON round trips, scripts
                     that only set headers, very long scripts
  robustness         no exception subprocess, swallowed exceptions
  performance        whole body as string, payload attachments, logging in loops
  configuration      fixed receiver addresses, URLs and secrets in scripts
  hygiene            outdated step versions, default step names, naming rule

Only local files are read; all flows are read for the cross-flow rules, the
filters select which flows are reported. Rules, severities and thresholds:
.cpi/lint.yaml (--rules; cpictl lint --list-rules). Known findings can be recorded in a
baseline so that --fail-on fails only on new ones.

--fix applies the mechanical fixes to the local files: scripts move into script
collections (the package's, or a shared one with scriptCollections.crossPackage),
unused scripts are deleted, unconnected steps removed (--fix-rules all also
removes content modifiers that do nothing). Review the diff, raise the versions
(version bump --changed) and deploy the collections before the flows.`,
		Example: `  cpictl lint
  cpictl lint --package UtilitiesBaseEDM --output json
  cpictl lint --changed --since origin/main --fail-on warning
  cpictl lint --update-baseline
  cpictl lint --fix --dry-run
  cpictl lint --fix --package UtilitiesBaseEDM`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if list, _ := cmd.Flags().GetBool("list-rules"); list {
				rules := lint.Rules()
				output.SetResult(cmd.Context(), map[string]any{"rules": rules})
				for _, r := range rules {
					fix := ""
					if r.Fixable {
						fix = " [fix]"
					}
					log.Info().Msgf("%-30s %-18s %-8s %s%s", r.ID, r.Group, r.Severity, r.Title, fix)
				}
				return nil
			}
			return runLint(cmd)
		},
	}
	c.Flags().String("dir", "", "Content tree (default: packages if it exists, else the current directory) (config: lint.dir)")
	c.Flags().StringSlice("package", nil, "Only report these packages (names or patterns)")
	c.Flags().StringSlice("artifact", nil, "Only report these artifacts (names or patterns)")
	c.Flags().Bool("changed", false, "Only report artifacts changed in Git since --since (committed, uncommitted, untracked)")
	c.Flags().String("since", "HEAD", "Git ref for --changed (e.g. origin/main in a pull request)")
	c.Flags().String("rules", ".cpi/lint.yaml", "Rules file: severities, thresholds, script collection names (config: lint.rules)")
	c.Flags().String("baseline", ".cpi/lint-baseline.json", "Known findings; --fail-on counts only new ones (config: lint.baseline)")
	c.Flags().Bool("update-baseline", false, "Record the current findings as known")
	c.Flags().String("fail-on", "", "Exit with code 5 when a new finding has at least this severity: info, warning, error (config: lint.failOn)")
	c.Flags().String("min-severity", "info", "Do not report findings below this severity")
	c.Flags().Bool("fix", false, "Apply the fixes to the local files")
	c.Flags().StringSlice("fix-rules", nil, "Rules to fix (default: duplicate-script, use-script-collection, unused-script, unconnected-step; all: every fixable rule)")
	c.Flags().Bool("dry-run", false, "With --fix: only list the changes")
	c.Flags().String("deploy-config", "", "Deploy config for the deployment-copies rule (default: orchestrator.deployConfig)")
	c.Flags().Bool("list-rules", false, "List the rules")
	c.Flags().String("components", ops.ComponentsFile, "Component catalogue of the target tenant, used when it exists (config: lint.components)")
	return c
}

func lintOptions(cmd *cobra.Command) (lint.Options, error) {
	dir := config.GetStringWithFallback(cmd, "dir", "lint.dir")
	if dir == "" {
		dir = "."
		if info, err := os.Stat("packages"); err == nil && info.IsDir() {
			dir = "packages"
		}
	}
	cfg, err := lint.LoadConfig(config.GetStringWithFallback(cmd, "rules", "lint.rules"))
	if err != nil {
		return lint.Options{}, err
	}
	o := lint.Options{
		Dir:         dir,
		Packages:    str.TrimSlice(config.GetStringSlice(cmd, "package")),
		Artifacts:   str.TrimSlice(config.GetStringSlice(cmd, "artifact")),
		Since:       config.GetString(cmd, "since"),
		Config:      cfg,
		Baseline:    config.GetStringWithFallback(cmd, "baseline", "lint.baseline"),
		MinSeverity: config.GetString(cmd, "min-severity"),
	}
	o.Changed, _ = cmd.Flags().GetBool("changed")
	if o.Components, err = loadComponents(config.GetStringWithFallback(cmd, "components", "lint.components")); err != nil {
		return o, err
	}
	if o.DeploymentCopies, err = deploymentCopies(cmd, dir); err != nil {
		return o, err
	}
	return o, nil
}

// deploymentCopies reads the deploy config: artifact folders deployed under
// several IDs (without a prefix).
func deploymentCopies(cmd *cobra.Command, base string) (map[string][]lint.DeploymentCopy, error) {
	path := config.GetString(cmd, "deploy-config")
	if path == "" {
		path = viper.GetString("orchestrator.deployConfig")
	}
	if path == "" {
		return nil, nil
	}
	loader := deploy.NewConfigLoader()
	if err := loader.DetectSource(path); err != nil {
		return nil, output.Usagef("deploy config %s: %v", path, err)
	}
	files, err := loader.LoadConfigs()
	if err != nil {
		return nil, output.Usagef("deploy config %s: %v", path, err)
	}
	out := map[string][]lint.DeploymentCopy{}
	for _, f := range files {
		if f.Config.DeploymentPrefix != "" {
			continue // environment copies, not partner copies
		}
		for _, pkg := range f.Config.Packages {
			for _, a := range pkg.Artifacts {
				key := filepath.ToSlash(filepath.Join(cmp.Or(pkg.PackageDir, pkg.ID), cmp.Or(a.ArtifactDir, a.Id)))
				c := lint.DeploymentCopy{ID: a.Id}
				for k := range a.ConfigOverrides {
					c.Overrides = append(c.Overrides, k)
				}
				out[key] = append(out[key], c)
			}
		}
	}
	return out, nil
}

func runLint(cmd *cobra.Command) error {
	o, err := lintOptions(cmd)
	if err != nil {
		return err
	}
	failOn := config.GetStringWithFallback(cmd, "fail-on", "lint.failOn")
	if failOn != "" && failOn != lint.SevInfo && failOn != lint.SevWarning && failOn != lint.SevError {
		return output.Usagef("--fail-on %q: info, warning or error", failOn)
	}
	res := lintResult{Baseline: o.Baseline, FailOn: failOn}
	if fix, _ := cmd.Flags().GetBool("fix"); fix {
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		rules := str.TrimSlice(config.GetStringSlice(cmd, "fix-rules"))
		if res.Fix, err = lint.Fix(cmd.Context(), o, lint.FixOptions{Rules: rules, DryRun: dryRun}); err != nil {
			return err
		}
		logFix(res.Fix)
	}
	if res.Result, err = lint.Run(cmd.Context(), o); err != nil {
		return err
	}
	if update, _ := cmd.Flags().GetBool("update-baseline"); update {
		if err := lint.WriteBaseline(o.Baseline, res.Findings); err != nil {
			return err
		}
		log.Info().Msgf("Baseline %s: %d known finding(s)", o.Baseline, len(res.Findings))
		res.New = map[string]int{}
		for i := range res.Findings {
			res.Findings[i].Baseline = true
		}
	}
	output.SetResult(cmd.Context(), res)
	logLint(res.Result)
	if failOn != "" {
		n := 0
		for sev, c := range res.New {
			if lint.AtLeast(sev, failOn) {
				n += c
			}
		}
		if n > 0 {
			return output.Failed(fmt.Errorf("%d new finding(s) with severity %s or higher", n, failOn))
		}
	}
	return nil
}

func logFix(fix *lint.FixResult) {
	prefix := ""
	if fix.DryRun {
		prefix = "[DRY RUN] "
	}
	for _, c := range fix.Changes {
		log.Info().Str("rule", c.Rule).Msgf("%s%s: %s", prefix, c.Path, c.Action)
	}
	log.Info().Msgf("%s%d change(s) in %d artifact(s); script collections: %s", prefix, len(fix.Changes), len(fix.Changed), cmp.Or(strings.Join(fix.Collections, ", "), "none"))
	for _, s := range fix.NextSteps {
		log.Info().Msg("Next: " + s)
	}
}

func logLint(res *lint.Result) {
	current := ""
	for _, f := range res.Findings {
		if f.Path != current {
			current = f.Path
			log.Info().Msg("── " + f.Path)
		}
		ev := log.Info()
		switch f.Severity {
		case lint.SevError:
			ev = log.Error()
		case lint.SevWarning:
			ev = log.Warn()
		}
		known := ""
		if f.Baseline {
			known = " (known)"
		}
		fix := ""
		if f.Fixable {
			fix = " [fix]"
		}
		ev.Str("rule", f.Rule).Str("severity", f.Severity).Msgf("  %-7s %-28s %s%s%s -> %s", f.Severity, f.Rule, f.Message, known, fix, f.Suggestion)
	}
	log.Info().Msgf("%d artifact(s), %d checked: %d error(s), %d warning(s), %d info; new: %d error(s), %d warning(s), %d info",
		res.Artifacts, res.Checked, res.Counts[lint.SevError], res.Counts[lint.SevWarning], res.Counts[lint.SevInfo],
		res.New[lint.SevError], res.New[lint.SevWarning], res.New[lint.SevInfo])
}

// loadComponents reads a component catalogue; a missing file is no catalogue.
func loadComponents(file string) (*catalog.Catalog, error) {
	if file == "" {
		return nil, nil
	}
	c, err := catalog.Load(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return c, err
}
