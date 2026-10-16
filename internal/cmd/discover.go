package cmd

import (
	"path/filepath"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func NewDiscoverCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "discover",
		Short: "Inventory existing integration flows to derive conventions",
		Long: `Inventory the packages and integration flows of the tenant (or of a local
directory with --dir) and write the facts as JSON: adapters, steps, exception
subprocesses, log levels, scripts (identical scripts across flows), externalised
parameter keys, credential names, headers and properties that are set, and
naming patterns of packages and flows.

The tenant is only read; every integration flow is downloaded and analysed in
memory. The file is the input for writing a repository's conventions (see the
cpi-discover skill).

Next to it, graph.json holds the same content as a graph for fast lookups (see
'cpictl graph'): which flows call which through ProcessDirect and JMS addresses,
which flows share credentials, scripts, Partner Directory parameters and headers,
and which systems they call. --graph=false skips it.`,
		Example: `  cpictl discover --output-file .cpi/discovery.json
  cpictl discover --package-ids SalesOrders,Finance
  cpictl discover --dir ./content   # local repository from 'sync' or 'snapshot', offline`,
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "dir"},
		RunE: func(cmd *cobra.Command, args []string) error {
			var d *ops.Discovery
			var err error
			if dir := config.GetString(cmd, "dir"); dir != "" {
				d, err = ops.DiscoverDir(cmd.Context(), dir)
			} else {
				d, err = ops.DiscoverTenant(cmd.Context(), tenantExecuter(cmd), config.GetString(cmd, "tmn-host"), ops.DiscoverOptions{
					PackageIDs: config.GetStringSlice(cmd, "package-ids"), MaxIFlows: config.GetInt(cmd, "max-iflows"),
				})
			}
			if err != nil {
				return err
			}
			file := config.GetString(cmd, "output-file")
			if err := ops.WriteDiscovery(d, file); err != nil {
				return err
			}
			res := map[string]any{"file": file, "source": d.Source, "summary": d.Summary, "errors": d.Errors}
			if withGraph, _ := cmd.Flags().GetBool("graph"); withGraph {
				g := ops.BuildGraphFor(d, file)
				if err := ops.WriteGraph(g, ops.GraphFile(file)); err != nil {
					return err
				}
				res["graphFile"], res["graph"] = ops.GraphFile(file), g.Stats
				log.Info().Msgf("Graph with %d nodes and %d edges written to %s", g.Stats.Nodes, g.Stats.Edges, ops.GraphFile(file))
			}
			components := config.GetString(cmd, "components")
			if components == "" {
				components = filepath.Join(filepath.Dir(file), "components.json")
			}
			if components != "-" {
				tenant := ""
				if config.GetString(cmd, "dir") == "" {
					tenant = config.GetString(cmd, "tmn-host")
				}
				cat, err := ops.LearnComponents(d, components, tenant)
				if err != nil {
					return err
				}
				res["componentsFile"] = components
				log.Info().Msgf("Component catalogue %s: %d adapter and %d step variants (%s)", components, len(cat.Adapters), len(cat.Steps), strings.Join(cat.Sources, ", "))
			}
			output.SetResult(cmd.Context(), res)
			log.Info().Msgf("Discovered %d package(s) and %d integration flow(s), written to %s", d.Summary.Packages, d.Summary.IFlows, file)
			for _, e := range d.Errors {
				log.Warn().Msg(e)
			}
			return nil
		},
	}
	c.Flags().String("output-file", ".cpi/discovery.json", "JSON file to write")
	c.Flags().StringSlice("package-ids", nil, "Only these packages (default: all)")
	c.Flags().Int("max-iflows", 0, "Stop after this many integration flows (0: all)")
	c.Flags().String("dir", "", "Analyse this local directory instead of the tenant")
	c.Flags().Bool("graph", true, "Also write graph.json next to the output file")
	c.Flags().String("components", "", "Add the variants the flows use to this component catalogue (default: components.json next to the output file; -: skip; see 'cpictl components')")
	return c
}
