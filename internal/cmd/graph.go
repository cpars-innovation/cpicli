package cmd

import (
	"strings"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func NewGraphCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "graph",
		Short: "Query the content graph written by discover (offline)",
		Long: `Query .cpi/graph.json, the graph that 'cpictl discover' writes next to
discovery.json. Nodes: ` + strings.Join(ops.GraphNodeTypes, ", ") + `.
Node IDs are <type>:<key> (iflow:Orders_In, endpoint:ProcessDirect:/billing/in,
pd:SAP_SYSTEM_001:Identity); commands also accept a key or a unique name.
Edges: ` + strings.Join(ops.GraphEdgeTypes, ", ") + `.
sends_to links flows through matching ProcessDirect and JMS addresses;
{{parameter}} addresses are resolved with the flow's parameters.prop value.

The graph shows what discovery saw: rerun discover after changes. Without
graph.json, discovery.json in the same folder is used.`,
		Example: `  cpictl graph search billing
  cpictl graph neighbors Billing --direction in --edge-types sends_to
  cpictl graph neighbors credential:SFTP_User
  cpictl graph path Orders_In Invoice_Send`,
		Annotations: map[string]string{annotationOffline: "true"},
	}
	c.PersistentFlags().String("file", ".cpi/graph.json", "Graph file")

	search := &cobra.Command{
		Use:          "search QUERY",
		Short:        "Find nodes by key, name or attribute",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			g, err := ops.LoadGraph(config.GetString(cmd, "file"))
			if err != nil {
				return err
			}
			query := ""
			if len(args) > 0 {
				query = args[0]
			}
			res, err := g.Search(query, config.GetStringSlice(cmd, "types"), config.GetInt(cmd, "limit"))
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), res)
			for _, m := range res.Matches {
				log.Info().Msgf("%-50s %-30s %d edge(s)", m.ID, m.Name, m.Degree)
			}
			if res.Total > len(res.Matches) {
				log.Info().Msgf("%d of %d matches", len(res.Matches), res.Total)
			}
			return nil
		},
	}
	search.Flags().StringSlice("types", nil, "Only these node types")
	search.Flags().Int("limit", 50, "Maximum number of matches")

	neighbors := &cobra.Command{
		Use:          "neighbors NODE",
		Short:        "Show what a node is connected to",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			g, err := ops.LoadGraph(config.GetString(cmd, "file"))
			if err != nil {
				return err
			}
			sub, err := g.Neighbors(args[0], ops.NeighborOptions{Direction: config.GetString(cmd, "direction"),
				EdgeTypes: config.GetStringSlice(cmd, "edge-types"), Depth: config.GetInt(cmd, "depth"), Limit: config.GetInt(cmd, "limit")})
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), sub)
			logEdges(sub.Edges)
			if sub.Truncated {
				log.Warn().Msg("result truncated: narrow with --edge-types or raise --limit")
			}
			return nil
		},
	}
	neighbors.Flags().String("direction", "both", "out (what the node uses or calls), in (what uses or calls it), both")
	neighbors.Flags().StringSlice("edge-types", nil, "Only these edge types")
	neighbors.Flags().Int("depth", 1, "Hops (1 to 3)")
	neighbors.Flags().Int("limit", 200, "Maximum number of edges")

	pathCmd := &cobra.Command{
		Use:   "path FROM TO",
		Short: "Find the shortest connection between two nodes",
		Long: `Find the shortest connection between two nodes, following edges in both
directions. By default only exposes, calls and sends_to are followed (how
messages move between flows); --edge-types widens it, e.g. uses_credential.`,
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			g, err := ops.LoadGraph(config.GetString(cmd, "file"))
			if err != nil {
				return err
			}
			res, err := g.Path(args[0], args[1], config.GetStringSlice(cmd, "edge-types"), config.GetInt(cmd, "max-depth"))
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), res)
			if !res.Found {
				log.Info().Msgf("no connection between %s and %s", args[0], args[1])
				return nil
			}
			logEdges(res.Edges)
			return nil
		},
	}
	pathCmd.Flags().StringSlice("edge-types", nil, "Edge types to follow (default: exposes, calls, sends_to)")
	pathCmd.Flags().Int("max-depth", 6, "Maximum number of hops")

	build := &cobra.Command{
		Use:   "build",
		Short: "Write graph.json from an existing discovery.json",
		Long: `Build the graph from a discovery file without discovering again. Discovery
files written before cpictl had the graph lack receiver addresses and Partner
Directory references: run discover again for the full graph.`,
		Example:      `  cpictl graph build --discovery-file .cpi/discovery.json`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			src := config.GetString(cmd, "discovery-file")
			d, err := ops.ReadDiscovery(src)
			if err != nil {
				return err
			}
			g := ops.BuildGraphFor(d, src)
			file := ops.GraphFile(src)
			if cmd.Flags().Changed("file") {
				file = config.GetString(cmd, "file")
			}
			if err := ops.WriteGraph(g, file); err != nil {
				return err
			}
			output.SetResult(cmd.Context(), map[string]any{"file": file, "stats": g.Stats})
			log.Info().Msgf("Graph with %d nodes and %d edges written to %s", g.Stats.Nodes, g.Stats.Edges, file)
			return nil
		},
	}
	build.Flags().String("discovery-file", ".cpi/discovery.json", "Discovery file to read")

	c.AddCommand(search, neighbors, pathCmd, build)
	return c
}

func logEdges(edges []ops.GraphEdge) {
	for _, e := range edges {
		via := ""
		if e.Via != "" {
			via = "  (via " + e.Via + ")"
		}
		if e.Version != "" {
			via += "  version " + e.Version
		}
		log.Info().Msgf("%s -%s-> %s%s", e.From, e.Type, e.To, via)
	}
}
