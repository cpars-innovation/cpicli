package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/iflow"
	"github.com/cpars-innovation/cpicli/pkg/lint"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func NewIFlowCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "iflow",
		Short: "Work with integration flow files (copy a template under a new ID, lay out the diagram)",
	}
	cp := &cobra.Command{
		Use:   "copy",
		Short: "Copy an integration flow under a new ID, name and sender address",
		Long: `Copy an integration flow (a template or any reference flow) into a local
directory and rename it, so that it can be uploaded as a new flow:

  META-INF/MANIFEST.MF  Bundle-SymbolicName (";singleton:=true" kept), Bundle-Name,
                        Bundle-Version (default 1.0.0); long values wrapped at 72 bytes
  metainfo.prop         description (the source's description is removed when
                        --description is not given)
  <SourceID>.iflw       renamed to <ID>.iflw (other model file names are kept)
  .project              project name
  sender addresses      HTTPS/SOAP path, ProcessDirect address, SFTP directory,
                        JMS queue, ...: in the model, or in parameters.prop when the
                        address is a {{parameter}}

Every sender address must get a new value (--address), or --keep-addresses must
be given: two deployed flows on the same HTTP path or ProcessDirect address fail
to deploy, and two flows polling the same directory or queue take each other's
messages. One --address NEW is enough when the flow has one sender address;
otherwise give --address OLD=NEW per address (OLD as in the source, e.g.
{{Orders_Path}}). A value containing "=" is always read as OLD=NEW.

Receivers, steps, scripts and parameters other than the addresses are copied
unchanged. The result lists files that still contain the source ID (process
names, scripts, log texts): check them. The source is read from the tenant
(--from, read only) or a local directory (--from-dir, offline). Nothing is
written to the tenant: upload the copy with 'cpictl update artifact'. On an
error the target directory is left as it was.`,
		Example: `  # template on the tenant, one HTTPS sender
  cpictl iflow copy --from Template_Sync_HTTPS --id SD_Orders_S4_Sync \
    --name "SD Orders to S/4 (sync)" --description "Order intake from the web shop" \
    --address /sd/orders/s4 --dir content/SDOrders/SD_Orders_S4_Sync

  # local template with two senders (a parameterised HTTPS path and a ProcessDirect test entry)
  cpictl iflow copy --from-dir content/Templates/Template_Async --id FI_Invoices_In \
    --address '{{Inbound_Path}}=/fi/invoices' --address /test/Template_Async=/test/FI_Invoices_In

  # then
  cpictl update artifact --artifact-id SD_Orders_S4_Sync --package-id SDOrders \
    --dir-artifact content/SDOrders/SD_Orders_S4_Sync`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "from-dir"},
		RunE: func(cmd *cobra.Command, args []string) error {
			addresses, _ := cmd.Flags().GetStringArray("address")
			keep, _ := cmd.Flags().GetBool("keep-addresses")
			o := ops.IFlowCopyOptions{
				FromID: config.GetString(cmd, "from"), FromVersion: config.GetString(cmd, "from-version"), FromDir: config.GetString(cmd, "from-dir"),
				TargetDir: config.GetString(cmd, "dir"), ID: config.GetString(cmd, "id"), Name: config.GetString(cmd, "name"),
				Description: config.GetString(cmd, "description"), Version: config.GetString(cmd, "version"),
				Addresses: addresses, KeepAddresses: keep,
			}
			if o.TargetDir == "" {
				o.TargetDir = o.ID
				if o.FromDir != "" {
					o.TargetDir = filepath.Join(filepath.Dir(filepath.Clean(o.FromDir)), o.ID)
				}
			}
			var exe *httpclnt.HTTPExecuter
			if o.FromID != "" {
				exe = tenantExecuter(cmd)
			}
			res, err := ops.CopyIFlow(exe, o)
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), res)
			log.Info().Msgf("Copied %s to %s as %s (%d files)", res.From, res.Dir, res.ID, res.Files)
			for _, c := range res.Changes {
				log.Info().Msg("  " + c)
			}
			for _, r := range res.Remaining {
				log.Warn().Msgf("still mentions %s: %s", res.SourceID, r)
			}
			return nil
		},
	}
	cp.Flags().String("from", "", "ID of the source flow on the tenant")
	cp.Flags().String("from-version", "active", "Version of the source flow on the tenant")
	cp.Flags().String("from-dir", "", "Local directory of the source flow (instead of --from)")
	cp.Flags().String("id", "", "ID of the new flow")
	cp.Flags().String("name", "", "Display name of the new flow (default: the ID)")
	cp.Flags().String("description", "", "Description of the new flow")
	cp.Flags().String("version", "1.0.0", "Version of the new flow")
	cp.Flags().String("dir", "", "Target directory, must not exist or be empty (default: <ID> next to --from-dir, or ./<ID>)")
	cp.Flags().StringArray("address", nil, "New sender address: NEW (one sender) or OLD=NEW (repeatable)")
	cp.Flags().Bool("keep-addresses", false, "Allow sender addresses that stay the same as in the source")
	_ = cp.MarkFlagRequired("id")
	c.AddCommand(cp, newIFlowLayoutCommand(), newIFlowDiagramCommand())
	return c
}

func newIFlowLayoutCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "layout <path>...",
		Short: "Lay out the diagram of integration flows: steps in flow order, no overlaps, right-angled lines (local files)",
		Long: `Recompute the diagram (BPMNDiagram) of .iflw files: steps left to right in flow
order, branches one below the other, exception subprocesses below the main flow,
senders left and receivers right of the integration process, level with the steps
they talk to, and right-angled lines that bend between columns. Only the diagram
changes, never the steps, their configuration or the sequence flows; a second run
changes nothing.

Paths are .iflw files or directories (searched recursively: an artifact folder, a
package, the whole content tree).

  --mode tidy   keeps the order of steps and branches (default)
  --mode full   also reorders branches to reduce crossing lines
  --check       only reports problems (missing shapes, overlaps, shapes outside the
                pool, lines through steps, cramped shapes); exit code 5 when any

Spacing and mode can be set in .cpi/lint.yaml (layout: {mode, hgap, vgap}).
Review the result in the Web UI before deploying.`,
		Example: `  cpictl iflow layout packages/Orders/OrderIntake
  cpictl iflow layout packages --check
  cpictl iflow layout packages/Orders --mode full --dry-run`,
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := lint.LoadConfig(config.GetStringWithFallback(cmd, "rules", "lint.rules"))
			if err != nil {
				return err
			}
			o := cfg.Layout
			if cmd.Flags().Changed("mode") || o.Mode == "" {
				o.Mode = config.GetString(cmd, "mode")
			}
			if v, _ := cmd.Flags().GetFloat64("hgap"); cmd.Flags().Changed("hgap") {
				o.HGap = v
			}
			if v, _ := cmd.Flags().GetFloat64("vgap"); cmd.Flags().Changed("vgap") {
				o.VGap = v
			}
			if _, err := o.Normalized(); err != nil {
				return output.Usage(err)
			}
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			check, _ := cmd.Flags().GetBool("check")
			rep, err := iflow.LayoutPaths(args, o, dryRun, check)
			if err != nil {
				return output.Usage(err)
			}
			output.SetResult(cmd.Context(), rep)
			logLayout(rep)
			switch {
			case rep.Failed > 0:
				return output.Failed(fmt.Errorf("%d file(s) failed", rep.Failed))
			case check && rep.Changed > 0:
				return output.Failed(fmt.Errorf("%d file(s) with layout problems", rep.Changed))
			}
			return nil
		},
	}
	c.Flags().String("mode", iflow.LayoutTidy, "tidy (keep the order of steps and branches) or full (also reorder branches) (config: .cpi/lint.yaml layout.mode)")
	c.Flags().Float64("hgap", iflow.DefaultHGap, "Space between columns of steps (config: .cpi/lint.yaml layout.hgap)")
	c.Flags().Float64("vgap", iflow.DefaultVGap, "Space between rows (config: .cpi/lint.yaml layout.vgap)")
	c.Flags().Bool("check", false, "Only report layout problems; exit code 5 when any")
	c.Flags().Bool("dry-run", false, "Compute the layout but write nothing")
	c.Flags().String("rules", ".cpi/lint.yaml", "Lint config with the layout settings (config: lint.rules)")
	return c
}

func newIFlowDiagramCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "diagram <path>",
		Short: "Read the diagram geometry of an integration flow: shape bounds, line waypoints, pools, steps and adapters (local file)",
		Long: `Read the diagram (BPMNDiagram) of an .iflw file as a viewer needs it: the bounds
of each shape and the waypoints of each line by BPMN element ID, the pools
(participants) and lanes, the steps, events and gateways with their type, name
and component, the sequence flows and the message flows with their adapter.
Elements without a shape or line are listed under undrawn.

The path is an .iflw file or a directory with exactly one (an artifact folder).
Use --output json for the result; the text output is a summary.`,
		Example:      `  cpictl iflow diagram packages/Orders/OrderIntake --output json`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			files, err := iflow.ModelFiles(args)
			if err != nil {
				return output.Usage(err)
			}
			if len(files) != 1 {
				return output.Usage(fmt.Errorf("%s: %d .iflw files, want exactly one", args[0], len(files)))
			}
			data, err := os.ReadFile(files[0])
			if err != nil {
				return output.Usage(err)
			}
			d, err := iflow.ReadDiagram(data)
			if err != nil {
				return output.Usage(fmt.Errorf("%s: %w", files[0], err))
			}
			output.SetResult(cmd.Context(), d)
			log.Info().Msgf("%s: %d shape(s), %d line(s), %d pool(s), %d node(s), %d sequence flow(s), %d message flow(s), %d not drawn",
				files[0], len(d.Shapes), len(d.Edges), len(d.Pools), len(d.Nodes), len(d.SequenceFlows), len(d.MessageFlows), len(d.Undrawn))
			return nil
		},
	}
}

func logLayout(rep *iflow.LayoutReport) {
	prefix := ""
	if rep.DryRun {
		prefix = "[DRY RUN] "
	}
	for _, f := range rep.Files {
		switch {
		case f.Error != "":
			log.Error().Msgf("%s: %s", f.Path, f.Error)
		case rep.Check:
			for _, i := range f.Issues {
				log.Warn().Str("kind", i.Kind).Msgf("%s: %s", f.Path, i.Message)
			}
		case f.Changed:
			log.Info().Msgf("%s%s: %d shape(s) moved, %d line(s) rerouted, %d added (%d problem(s) before)", prefix, f.Path, f.Moved, f.Rerouted, f.Added, len(f.Issues))
		default:
			log.Debug().Msgf("%s: unchanged", f.Path)
		}
	}
	what := "laid out"
	if rep.Check {
		what = "with problems"
	}
	log.Info().Msgf("%s%d file(s), %d %s, %d problem(s) found, %d failed (mode %s)", prefix, len(rep.Files), rep.Changed, what, rep.Issues, rep.Failed, rep.Mode)
}
