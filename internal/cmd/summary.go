package cmd

import (
	"fmt"
	"github.com/cpars-innovation/cpicli/pkg/lint"
	"github.com/cpars-innovation/cpicli/pkg/snapshot"
	"os"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// markdownSummary is implemented by results that render a job summary
// (--summary, $GITHUB_STEP_SUMMARY).
type markdownSummary interface {
	MarkdownSummary() string
}

// maxSummaryRows limits each table of a job summary.
const maxSummaryRows = 200

// summaryTarget is the file a job summary is appended to: --summary, else
// $GITHUB_STEP_SUMMARY; "off" or nothing: none.
func summaryTarget(cmd *cobra.Command) string {
	if cmd == nil {
		return ""
	}
	path, _ := cmd.Flags().GetString("summary")
	if path == "" {
		path = os.Getenv("GITHUB_STEP_SUMMARY")
	}
	if path == "off" {
		return ""
	}
	return path
}

// writeSummary appends the markdown summary of a command's result.
func writeSummary(cmd *cobra.Command, result any, code int, elapsed time.Duration) {
	s, ok := result.(markdownSummary)
	path := summaryTarget(cmd)
	if !ok || path == "" {
		return
	}
	icon := "✅"
	if code != 0 {
		icon = "❌"
	}
	text := fmt.Sprintf("### %s cpictl %s (exit code %d, %s)\n\n%s\n", icon, commandName(cmd), code, formatElapsed(elapsed), s.MarkdownSummary())
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		_, err = f.WriteString(text)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		log.Warn().Msgf("Cannot write the job summary to %s: %v", path, err)
	}
}

// mdTable renders rows as a markdown table (cells escaped, at most
// maxSummaryRows rows).
func mdTable(header []string, rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("| " + strings.Join(header, " | ") + " |\n")
	b.WriteString("|" + strings.Repeat("---|", len(header)) + "\n")
	for i, r := range rows {
		if i == maxSummaryRows {
			fmt.Fprintf(&b, "\n%d more row(s) in the JSON result.\n", len(rows)-maxSummaryRows)
			break
		}
		cells := make([]string, len(r))
		for j, c := range r {
			c = strings.ReplaceAll(strings.ReplaceAll(c, "|", "\\|"), "\n", " ")
			if len(c) > 300 {
				c = c[:300] + "…"
			}
			cells[j] = c
		}
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
	return b.String() + "\n"
}

func deploymentRows(results []ops.Result) [][]string {
	rows := make([][]string, 0, len(results))
	// failures first
	for _, failed := range []bool{true, false} {
		for _, r := range results {
			if r.Status.Succeeded() == failed {
				continue
			}
			rows = append(rows, []string{r.ID, string(r.Status), r.Version, strings.TrimSpace(r.Rule + " " + r.Reason), r.Error})
		}
	}
	return rows
}

func deploymentsTable(results []ops.Result) string {
	return mdTable([]string{"Artifact", "Status", "Version", "Rule", "Error"}, deploymentRows(results))
}

func planTable(items []PlanItem) string {
	rows := make([][]string, 0, len(items))
	for _, it := range items {
		if it.Upload == "unchanged" && !it.Deploy && it.Error == "" {
			continue // nothing happens
		}
		deploy := "no"
		if it.Deploy {
			deploy = "yes"
		}
		rows = append(rows, []string{it.Artifact, it.Upload, deploy, it.Designtime, it.Running, it.Reason, it.Error})
	}
	out := mdTable([]string{"Artifact", "Upload", "Deploy", "Designtime", "Running", "Reason", "Error"}, rows)
	if skipped := len(items) - len(rows); skipped > 0 {
		out += fmt.Sprintf("%d artifact(s) unchanged and not deployed.\n", skipped)
	}
	return out
}

func (r orchestratorResult) MarkdownSummary() string {
	var b strings.Builder
	if s := r.Stats; s != nil {
		fmt.Fprintf(&b, "Mode %s: %d artifact(s), %d changed, %d unchanged, %d update failure(s), %d skipped (draft); compared %d with the snapshot, %d downloaded; %d deployed, %d deployment failure(s).\n\n",
			r.Mode, s.ArtifactsTotal, s.ArtifactsChanged, s.ArtifactsUnchanged, s.UpdateFailures, s.ArtifactsSkippedDraft, s.ComparedWithSnapshot, s.DownloadedForComparison,
			s.ArtifactsDeployedSuccess, s.ArtifactsDeployedFailed)
	}
	if s := r.Stats; s != nil && len(s.SkippedDrafts) > 0 {
		fmt.Fprintf(&b, "**Skipped: in draft on the tenant** (%d, not uploaded, not deployed; save the version in the Web UI, then run again)\n\n", len(s.SkippedDrafts))
		var rows [][]string
		for _, d := range s.SkippedDrafts {
			rows = append(rows, []string{d.ID, d.Package, d.Designtime, d.Running, d.LocalVersion})
		}
		b.WriteString(mdTable([]string{"Artifact", "Package", "Tenant designtime", "Running", "Repository Bundle-Version"}, rows))
	}
	if len(r.Plan) > 0 {
		b.WriteString("**Plan** (nothing was changed)\n\n" + planTable(r.Plan))
	}
	b.WriteString(deploymentsTable(r.Deployments))
	return b.String()
}

func (r configureResult) MarkdownSummary() string {
	var b strings.Builder
	if s := r.Stats; s != nil {
		fmt.Fprintf(&b, "%d artifact(s): %d configured, %d unchanged, %d failed; %d parameter(s) updated; %d deployed, %d deployment failure(s).\n\n",
			s.ArtifactsProcessed, s.ArtifactsConfigured, s.ArtifactsUnchanged, s.ArtifactsFailed, s.ParametersUpdated,
			s.DeploymentTasksSuccessful, s.DeploymentTasksFailed)
	}
	var rows [][]string
	for _, d := range r.Diff {
		if d.Change != ops.ConfigUnchanged {
			rows = append(rows, []string{d.ArtifactID, d.Key, d.Change}) // values may be sensitive: not shown
		}
	}
	if len(rows) > 0 {
		label := "**Parameters**"
		if r.DryRun {
			label += " (dry run: nothing was written)"
		}
		b.WriteString(label + "\n\n" + mdTable([]string{"Artifact", "Key", "Change"}, rows))
	}
	if len(r.Plan) > 0 {
		b.WriteString("**Deployments** (plan)\n\n" + planTable(r.Plan))
	}
	b.WriteString(deploymentsTable(r.Deployments))
	return b.String()
}

func (r artifactResults) MarkdownSummary() string {
	return deploymentsTable(r.Results)
}

func (r planResult) MarkdownSummary() string {
	return "**Plan** (nothing was triggered)\n\n" + planTable(r.Plan)
}

func (r *snapshotResult) MarkdownSummary() string {
	var b strings.Builder
	if r.DryRun {
		b.WriteString("**Dry run**: nothing was written.\n\n")
	}
	fmt.Fprintf(&b, "%d of %d package(s): %s; %d warning(s); %d artifact(s) downloaded, %.1fs.\n\n",
		r.Succeeded, r.Packages, snapshot.FormatCounts(r.Counts), r.Warnings, r.Downloaded, r.Seconds)
	var rows [][]string
	for _, it := range r.Artifacts {
		if it.Status == snapshot.StatusUnchanged && it.Warning == "" {
			continue
		}
		note := it.Note
		if len(it.OrphanParameters) > 0 {
			note = strings.TrimSpace(note + " orphan parameters: " + strings.Join(it.OrphanParameters, ", "))
		}
		rows = append(rows, []string{it.Package + "/" + it.Artifact, it.Label(), it.Action, it.Source, strings.TrimSpace(note + " " + it.Warning)})
	}
	b.WriteString(mdTable([]string{"Artifact", "Status", "Action", "Source", "Note"}, rows))
	if len(r.Failed) > 0 {
		b.WriteString("Failed: " + strings.Join(r.Failed, ", ") + "\n")
	}
	return b.String()
}

// planResult is the JSON result of deploy --plan.
type planResult struct {
	Plan []PlanItem `json:"plan"`
}

func (r lintResult) MarkdownSummary() string {
	if r.Result == nil {
		return ""
	}
	var b strings.Builder
	if r.Fix != nil {
		fmt.Fprintf(&b, "**Fix**%s: %d change(s) in %d artifact(s); script collections: %s.\n\n",
			map[bool]string{true: " (dry run)", false: ""}[r.Fix.DryRun], len(r.Fix.Changes), len(r.Fix.Changed), strings.Join(r.Fix.Collections, ", "))
	}
	fmt.Fprintf(&b, "%d artifact(s) checked: %d error(s), %d warning(s), %d info; new: %d error(s), %d warning(s), %d info.\n\n",
		r.Checked, r.Counts[lint.SevError], r.Counts[lint.SevWarning], r.Counts[lint.SevInfo], r.New[lint.SevError], r.New[lint.SevWarning], r.New[lint.SevInfo])
	var rows [][]string
	for _, f := range r.Findings {
		if !f.Baseline && f.Severity != lint.SevInfo {
			rows = append(rows, []string{f.Path, f.Severity, f.Rule, f.Message, f.Suggestion})
		}
	}
	b.WriteString(mdTable([]string{"Artifact", "Severity", "Rule", "Finding", "Suggestion"}, rows))
	return b.String()
}
