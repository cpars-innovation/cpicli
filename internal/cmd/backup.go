package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func NewBackupCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "backup",
		Short: "Back up a tenant into one archive, compare backups, verify them",
		Long: `Disaster recovery for a Cloud Integration tenant (docs/backup.md).

backup create writes one archive (.tar.gz) with every package and its artifacts
(zip as downloaded), externalised parameters, the Partner Directory, what is
deployed, credentials (metadata, plus credentials.yml with one environment
variable per secret: the tenant never returns secrets), the keystore (metadata
and certificates; private keys never leave the tenant), number ranges, access
policies and the names of variables, data stores and JMS queues. manifest.json
lists every file with its checksum, index.json what a comparison needs.

backup diff compares two backups, or a backup with the tenant (metadata only,
quick). backup verify checks an archive's checksums and artifact zips.
backup restore puts a backup back into a tenant (--dry-run: the plan only).

Uploading to S3 or Azure Blob Storage and rotation are done by the pipeline and
the storage's lifecycle rules (docs/backup.md has examples).`,
	}
	c.AddCommand(newBackupCreateCommand(), newBackupVerifyCommand(), newBackupDiffCommand(), newBackupRestoreCommand())
	return c
}

func newBackupCreateCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "create",
		Short: "Write a backup of the tenant into one archive",
		Long: `Writes cpi-backup_<tenant>_<UTC time>.tar.gz into --dir (or the file --output).
With --schedule-folders the archive goes into daily/, weekly/ (Sundays) or
monthly/ (the 1st) below --dir, so storage lifecycle rules can keep each for a
different time.

Exit code 0 when everything was backed up; 7 when an area or artifact could not
be read (the archive is written, the manifest lists the warnings); other codes
when no archive could be written.`,
		Example: `  cpictl backup create --dir ./backups
  cpictl backup create --dir ./backups --schedule-folders --cells
  cpictl backup create --output nightly.tar.gz --packages 'SD_*,FI_*'`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sd := serviceDetails(cmd)
			out := config.GetString(cmd, "output-file")
			if out == "" {
				dir := config.GetString(cmd, "dir")
				now := time.Now().UTC()
				if sched, _ := cmd.Flags().GetBool("schedule-folders"); sched {
					dir = filepath.Join(dir, ops.BackupScheduleFolder(now))
				}
				out = filepath.Join(dir, ops.BackupFileName(sd.Host, now))
			}
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			packages, _ := cmd.Flags().GetStringSlice("packages")
			cells, _ := cmd.Flags().GetBool("cells")
			parallel, _ := cmd.Flags().GetInt("parallel")
			res, err := ops.CreateBackup(cmd.Context(), tenantExecuter(cmd), sd.Host, ops.BackupOptions{Output: out, Packages: packages,
				Cells: cells, Parallel: parallel, CpictlVersion: cmd.Root().Version, Progress: progressLogger(cmd)})
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), res)
			m := res.Manifest
			log.Info().Msgf("Backup %s (%s, sha256 %s): %d packages, %d artifacts, %d deployed, %d credentials, %d keystore entries, %d Partner Directory parameters",
				res.File, humanSize(res.Size), res.SHA256[:12], m.Counts["packages"], m.Counts["artifacts"], m.Counts["deployed"],
				m.Counts["credentials"], m.Counts["keystoreEntries"], m.Counts["partnerDirectoryParameters"])
			if !m.Complete {
				return output.Partial(fmt.Errorf("backup written, but %d item(s) could not be backed up (see warnings in the manifest)", len(m.Warnings)))
			}
			return nil
		},
	}
	c.Flags().String("dir", ".", "Folder for the archive")
	c.Flags().String("output-file", "", "Archive file (instead of a generated name in --dir)")
	c.Flags().Bool("schedule-folders", false, "Write into daily/, weekly/ (Sundays) or monthly/ (1st of the month) below --dir")
	c.Flags().StringSlice("packages", nil, "Only these packages (patterns with *)")
	c.Flags().Bool("cells", false, "Also record what is deployed on integration cells")
	c.Flags().Int("parallel", 8, "Artifacts downloaded at a time")
	return c
}

func newBackupVerifyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "verify <archive>",
		Short: "Check a backup archive: checksums, artifact zips, completeness",
		Long: `Reads the whole archive and checks that every file has the checksum listed in
manifest.json, that nothing is missing or extra, that every artifact zip is
readable and names its artifact, and that the backup was complete when it was
written. Offline. Exit code 5 when a problem is found.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ops.VerifyBackup(args[0])
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), res)
			for _, p := range res.Problems {
				log.Error().Msg(p)
			}
			if !res.OK {
				return output.Failed(fmt.Errorf("%s: %d problem(s)", args[0], len(res.Problems)))
			}
			log.Info().Msgf("%s: OK (%d files, tenant %s, %s)", args[0], res.Files, res.Manifest.Tenant, res.Manifest.CreatedAt.Format(time.RFC3339))
			return nil
		},
	}
}

func newBackupDiffCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "diff <archive> [<archive>]",
		Short: "Compare two backups, or a backup with the tenant",
		Long: `Compares the index of two backups (artifacts by content checksum), or of a
backup with the tenant as it is now (one archive: artifacts by version and
change time, nothing is downloaded). Lists what was added, removed or changed:
packages, artifacts, deployments, credentials, keystore entries, Partner
Directory parameters, number ranges, variables, data stores, JMS queues.`,
		Example: `  cpictl backup diff backups/monday.tar.gz backups/tuesday.tar.gz
  cpictl backup diff backups/last.tar.gz                # against the tenant
  cpictl backup diff backups/last.tar.gz --fail-on-change`,
		Args:         cobra.RangeArgs(1, 2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, a, err := ops.ReadBackup(args[0])
			if err != nil {
				return err
			}
			var b *ops.BackupIndex
			against := "tenant"
			if len(args) == 2 {
				if _, b, err = ops.ReadBackup(args[1]); err != nil {
					return err
				}
				against = args[1]
			} else {
				cells, _ := cmd.Flags().GetBool("cells")
				var warnings []string
				b, warnings, err = ops.BuildTenantIndex(cmd.Context(), tenantExecuter(cmd), serviceDetails(cmd).Host, ops.BackupOptions{Cells: cells})
				if err != nil {
					return err
				}
				for _, w := range warnings {
					log.Warn().Msg(w)
				}
			}
			changes := ops.DiffBackupIndexes(a, b)
			output.SetResult(cmd.Context(), map[string]any{"from": args[0], "to": against, "changes": changes})
			for _, c := range changes {
				switch c.Change {
				case "added":
					log.Info().Msgf("+ %-17s %s  %s", c.Area, c.Key, c.After)
				case "removed":
					log.Info().Msgf("- %-17s %s  %s", c.Area, c.Key, c.Before)
				default:
					log.Info().Msgf("~ %-17s %s  %s -> %s", c.Area, c.Key, c.Before, c.After)
				}
			}
			log.Info().Msgf("%d change(s) from %s to %s", len(changes), args[0], against)
			if fail, _ := cmd.Flags().GetBool("fail-on-change"); fail && len(changes) > 0 {
				return output.Failed(fmt.Errorf("%d change(s)", len(changes)))
			}
			return nil
		},
	}
	c.Flags().Bool("cells", false, "Against the tenant: also integration cells")
	c.Flags().Bool("fail-on-change", false, "Exit with code 5 when something changed")
	return c
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// progressLogger logs every 50th step of an operation at info level (the
// others at debug level).
func progressLogger(cmd *cobra.Command) ops.ProgressFunc {
	return func(p ops.Progress) {
		ev := log.Debug()
		if p.Step%50 == 0 || p.Step == p.Total {
			ev = log.Info()
		}
		ev.Msgf("%d/%d %s", p.Step, p.Total, p.Message)
	}
}

func newBackupRestoreCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "restore <archive>",
		Short: "Restore a backup into the tenant (--dry-run: write the plan only)",
		Long: `Restores what the tenant API can take back, in this order: packages, artifacts
(the zips as backed up), externalised parameters, the Partner Directory,
credentials whose secret is set in its environment variable (credentials.yml),
certificates, access policies, number ranges, and deploys what was running.
Everything else is a manual step in the plan: secrets you have not set, key
pairs (their private keys never left the tenant), standard packages from
Discover, artifacts on integration cells.

Nothing is deleted: content the backup does not have stays on the tenant.
Existing objects are only changed where they differ. Artifact content is
restored as backed up; the tenant's designtime version stays (a new artifact
gets the backup's version). Number ranges are never lowered (numbers already
issued would repeat).

--dry-run writes nothing and shows the plan; --plan-file saves the plan (or,
without --dry-run, the report) as Markdown, or as JSON for a .json name.
A restore that writes needs --confirm. The archive is verified first: a
damaged archive is refused.

Exit code 0 when everything was restored, 7 when steps failed (listed), 2
without --confirm.`,
		Example: `  cpictl backup restore nightly.tar.gz --dry-run --plan-file restore-plan.md
  CPI_SECRET_USER_ERP_USER=... cpictl backup restore nightly.tar.gz --confirm --plan-file restore-report.md
  cpictl backup restore nightly.tar.gz --only packages,artifacts,parameters --packages 'SD_*' --confirm
  cpictl backup restore nightly.tar.gz --only number-ranges --dry-run`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			confirm, _ := cmd.Flags().GetBool("confirm")
			if !dryRun && !confirm {
				return output.Usagef("a restore writes to the tenant: run it with --dry-run first, then with --confirm")
			}
			only, _ := cmd.Flags().GetStringSlice("only")
			packages, _ := cmd.Flags().GetStringSlice("packages")
			parallel, _ := cmd.Flags().GetInt("parallel")
			delay, _ := cmd.Flags().GetInt("delay-length")
			wait, err := resolveDeployWait(cmd, "deploy-timeout", "deploy.timeout", false, defaultDeployChecks, delay)
			if err != nil {
				return err
			}
			sd := serviceDetails(cmd)
			res, err := ops.RestoreBackup(cmd.Context(), tenantExecuter(cmd), args[0], ops.RestoreOptions{DryRun: dryRun, Areas: only,
				Packages: packages, Parallel: parallel, TargetHost: sd.Host,
				Deploy: ops.Options{Interval: wait.Interval, MaxChecks: wait.MaxChecks, Timeout: wait.Timeout, CompareVersions: false}})
			if res != nil {
				output.SetResult(cmd.Context(), res)
				logRestore(res)
				if plan := config.GetString(cmd, "plan-file"); plan != "" {
					if werr := ops.WriteRestorePlan(res, plan); werr != nil {
						return werr
					}
					log.Info().Msgf("%s written to %s", map[bool]string{true: "Plan", false: "Report"}[res.DryRun], plan)
				}
			}
			return err
		},
	}
	c.Flags().Bool("dry-run", false, "Show what the restore would do; write nothing")
	c.Flags().String("plan-file", "", "Write the plan (dry run) or the report to this file: Markdown, or JSON for a .json name")
	c.Flags().Bool("confirm", false, "Confirm a restore that writes to the tenant")
	c.Flags().StringSlice("only", nil, "Only these areas: "+strings.Join(ops.RestoreAreas, ", "))
	c.Flags().StringSlice("packages", nil, "Only these packages (patterns with *) for packages, artifacts, parameters and deploy")
	c.Flags().Int("parallel", 4, "Artifacts uploaded at a time")
	c.Flags().Int("delay-length", defaultDeployDelaySeconds, "Seconds between deployment status checks")
	c.Flags().String("deploy-timeout", "", "How long to wait for the deployments, e.g. 30m (default 10m; config: deploy.timeout)")
	return c
}

func logRestore(res *ops.RestoreResult) {
	prefix := ""
	if res.DryRun {
		prefix = "[DRY RUN] "
	}
	for _, s := range res.Steps {
		if s.Action == ops.RestoreUnchanged {
			continue
		}
		msg := fmt.Sprintf("%s%s %s: %s", prefix, s.Area, s.Object, s.Action)
		if s.Detail != "" {
			msg += " (" + s.Detail + ")"
		}
		switch s.Action {
		case ops.RestoreFailed:
			log.Error().Msg(msg + ": " + s.Error)
		case ops.RestoreManual:
			log.Warn().Msg(msg)
		default:
			log.Info().Msg(msg)
		}
	}
	c := res.Counts
	log.Info().Msgf("%sRestore of %s (tenant %s): %d create, %d update, %d deploy, %d unchanged, %d manual, %d failed",
		prefix, res.Archive, res.SourceTenant, c[ops.RestoreCreate], c[ops.RestoreUpdate], c[ops.RestoreDeployIt], c[ops.RestoreUnchanged], c[ops.RestoreManual], c[ops.RestoreFailed])
}
