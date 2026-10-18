package cmd

import (
	"fmt"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/cpars-innovation/cpicli/pkg/pdrepo"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// NewPDCommand groups the Partner Directory inspection commands (pd-snapshot
// and pd-deploy stay top-level commands).
func NewPDCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "pd",
		Short: "Partner Directory: partners, parameters (get, diff, deps), alternative partners, authorized users",
	}

	get := &cobra.Command{
		Use:   "get",
		Short: "Show the Partner Directory parameters of a partner ID on the tenant",
		Example: `  cpictl pd get --pid ONE_OMS
  cpictl pd get --pid ONE_OMS --key now_email --content
  cpictl pd get --pid ONE_OMS --key partner_cert --inspect
  cpictl pd get --pid ONE_OMS --key mappings --inspect --zip-entry map/a.xsl`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			content, _ := cmd.Flags().GetBool("content")
			opts := ops.PDGetOptions{IncludeContent: content, MaxBytes: contentLimit(cmd)}
			if inspect, _ := cmd.Flags().GetBool("inspect"); inspect {
				entry, _ := cmd.Flags().GetString("zip-entry")
				opts.Inspect = &ops.PDInspectOptions{ZipEntry: entry, TextBytes: max(opts.MaxBytes, 0)}
			}
			res, err := ops.GetPDParametersWith(cpi.NewPartnerDirectory(tenantExecuter(cmd)), config.GetString(cmd, "pid"),
				config.GetStringSlice(cmd, "key"), opts)
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), res)
			for _, p := range res.Strings {
				log.Info().Msgf("string %-30s %s", p.ID, p.Value)
			}
			for _, b := range res.Binaries {
				log.Info().Msgf("binary %-30s %s, %d bytes, sha256 %s", b.ID, b.ContentType, b.Size, b.SHA256)
				logPDInspection("  ", b.Inspection)
			}
			for _, k := range res.Missing {
				log.Warn().Msgf("%s not found", k)
			}
			return nil
		},
	}
	get.Flags().String("pid", "", "Partner ID")
	get.Flags().StringSlice("key", nil, "Only these parameter IDs (repeatable)")
	get.Flags().Bool("content", false, "Include binary content")
	get.Flags().Bool("inspect", false, "Inspect binaries: detected type, gz/zlib content, zip entries, certificates")
	get.Flags().String("zip-entry", "", "With --inspect: read and inspect this zip entry")
	addContentFlags(get)
	_ = get.MarkFlagRequired("pid")

	diff := &cobra.Command{
		Use:   "diff",
		Short: "Compare local Partner Directory files with the tenant",
		Long: `Compare the local tree (layout of pd-snapshot) with the tenant: per parameter
create, update, unchanged or remote_only (only on the tenant: pd-deploy --full-sync
would delete it). Exit code 7 when a PID could not be read locally.`,
		Example:      `  cpictl pd diff --resources-path ./partner-directory --pids ONE_OMS`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ops.PDDiff(cpi.NewPartnerDirectory(tenantExecuter(cmd)), pdrepo.New(config.GetString(cmd, "resources-path")),
				config.GetStringSlice(cmd, "pids"))
			if res != nil {
				output.SetResult(cmd.Context(), res)
				for _, it := range res.Items {
					if it.Change != ops.PDUnchanged {
						log.Info().Msgf("%-11s %s %s/%s %s", it.Change, it.Kind, it.Pid, it.ID, it.Detail)
					}
				}
				log.Info().Msgf("create %d, update %d, unchanged %d, remote_only %d", res.Summary[ops.PDCreate], res.Summary[ops.PDUpdate],
					res.Summary[ops.PDUnchanged], res.Summary[ops.PDRemoteOnly])
			}
			return err
		},
	}
	diff.Flags().String("resources-path", "./partner-directory", "Path to partner directory parameters")
	diff.Flags().StringSlice("pids", nil, "Only these partner IDs")

	deps := &cobra.Command{
		Use:   "deps",
		Short: "Show which flows read which Partner Directory parameters (local files)",
		Long: `Scan local content for Partner Directory references: pd:<PID>:<ID>:<Binary|String>
in .iflw models, dynamic pd:${...} references and getParameter(id, pid, ...) in
Groovy scripts. With --resources-path, PIDs that are referenced but have no local
directory are listed as unknown. Nothing is read from the tenant.`,
		Example:      `  cpictl pd deps --local-dir ./content --resources-path ./partner-directory --pid ONE_OMS`,
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ops.FindPDDependencies(cmd.Context(), config.GetString(cmd, "local-dir"), ops.PDDependenciesFilter{
				Pid: config.GetString(cmd, "pid"), ID: config.GetString(cmd, "id"), ResourcesPath: config.GetString(cmd, "resources-path")})
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), res)
			for _, r := range res.References {
				pid := r.Pid
				if pid == "" {
					pid = "?"
				}
				log.Info().Msgf("%s:%s %-6s %s  %s %s", pid, r.ID, r.Kind, r.Artifact, r.File, r.Step)
			}
			for _, d := range res.Dynamic {
				log.Info().Msgf("dynamic %s  %s %s  %s", d.Artifact, d.File, d.Step, d.Expression)
			}
			if len(res.UnknownPids) > 0 {
				log.Warn().Msgf("Referenced PIDs without local files: %s", strings.Join(res.UnknownPids, ", "))
			}
			return nil
		},
	}
	deps.Flags().String("local-dir", ".", "Local content directory")
	deps.Flags().String("resources-path", "", "Local Partner Directory tree (to report unknown PIDs)")
	deps.Flags().String("pid", "", "Only this partner ID")
	deps.Flags().String("id", "", "Only this parameter ID")

	c.AddCommand(get, diff, deps)
	c.AddCommand(pdPartnerCommands()...)
	return c
}

// logPDInspection logs what InspectPDBinary found (nil: nothing).
func logPDInspection(indent string, in *ops.PDInspection) {
	if in == nil {
		return
	}
	msg := "detected " + in.DetectedType
	if in.Mismatch {
		msg += " (declared " + in.DeclaredType + ")"
	}
	if in.Truncated {
		msg += ", cut"
	}
	if in.Mismatch {
		log.Warn().Msg(indent + msg)
	} else {
		log.Info().Msg(indent + msg)
	}
	for _, c := range in.Certificates {
		state := fmt.Sprintf("valid until %s (%d days)", c.NotAfter.Format("2006-01-02"), c.DaysLeft)
		if c.Expired {
			state = "expired " + c.NotAfter.Format("2006-01-02")
		}
		log.Info().Msgf("%scertificate %s, issuer %s, %s", indent, c.Subject, c.Issuer, state)
	}
	for _, e := range in.Entries {
		log.Info().Msgf("%s%-40s %d bytes", indent, e.Name, e.Size)
	}
	if in.EntriesTruncated {
		log.Info().Msg(indent + "... more entries")
	}
	for _, w := range in.Warnings {
		log.Warn().Msg(indent + w)
	}
	if in.Inner != nil {
		logPDInspection(indent+"  ", in.Inner)
	}
	if in.Entry != nil {
		logPDInspection(indent+"  ", in.Entry)
	}
}
