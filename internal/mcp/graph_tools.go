package mcp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cpars-innovation/cpicli/pkg/ops"
)

const defaultGraphFile = ".cpi/graph.json"

// graphTools query the content graph that discover_tenant writes. They read a
// local file only.
func graphTools(cfg Config) []Tool {
	annotations := map[string]any{"readOnlyHint": true, "openWorldHint": false}
	load := func(file string) (*ops.Graph, error) {
		if file == "" {
			file = defaultGraphFile
		}
		p, err := resolvePath(cfg.Root, file)
		if err != nil {
			return nil, err
		}
		return ops.LoadGraph(p)
	}
	fileProp := str(`Graph file relative to the server root, default "` + defaultGraphFile + `" (written by discover_tenant)`)
	nodeRef := "node ID (<type>:<key>, e.g. iflow:Orders_In, endpoint:ProcessDirect:/billing/in, credential:SFTP_User), a key or a unique name"
	edgeTypes := strArray("Only these edge types: " + strings.Join(ops.GraphEdgeTypes, ", "))
	return []Tool{
		{
			Name: "graph_search", Title: "Search the content graph",
			Description: "Find flows, packages, endpoints (sender, ProcessDirect and JMS addresses), systems (receiver hosts), credentials, scripts, headers, " +
				"properties, custom headers, Partner Directory parameters, OData entities (host/entity set) and IDoc types (ORDERS05) in the graph that " +
				"discover_tenant wrote; matches key, name and attributes (a query of several words matches nodes that contain all of them), best first. " +
				"Scripts carry what is needed to reuse them: reusable=true for scripts of script collections (call those instead of copying), summary, " +
				"keywords, functions, reads and writes (headers/properties by name), usages (flows using it), docStatus (ok, incomplete, missing) and " +
				"shape (equal shape = same code apart from comments and formatting: search the shape value to find near-duplicates). " +
				"Faster and smaller than reading discovery.json. Node types: " + strings.Join(ops.GraphNodeTypes, ", ") + ". Reads a local file only; " +
				"the graph reflects the last discovery.",
			InputSchema: object(props{
				"query": str("Text to find (case-insensitive); empty lists all nodes of the given types"),
				"types": strArray("Only these node types"),
				"limit": integer("Maximum number of matches (default 50)"),
				"file":  fileProp,
			}),
			Annotations: annotations,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Query string   `json:"query"`
					Types []string `json:"types"`
					Limit int      `json:"limit"`
					File  string   `json:"file"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				g, err := load(a.File)
				if err != nil {
					return nil, err
				}
				return g.Search(a.Query, a.Types, a.Limit)
			},
		},
		{
			Name: "graph_neighbors", Title: "Show what a node is connected to",
			Description: "Return the edges and nodes around a node of the content graph. Examples: who calls a flow " +
				`(node "Billing", direction "in", edge_types ["sends_to"]); what a flow uses (direction "out"); ` +
				"which flows use a credential, script, header or Partner Directory parameter (that node, direction \"in\"); " +
				`which flows read or write an OData entity or receive or send an IDoc type (node "idoc_type:ORDERS05", direction "in"); ` +
				"the whole downstream chain (edge_types [\"sends_to\"], depth 3). Edges: " + strings.Join(ops.GraphEdgeTypes, ", ") +
				". sends_to links flows through matching ProcessDirect/JMS addresses (via names the endpoint). Reads a local file only.",
			InputSchema: object(props{
				"node":       str("The " + nodeRef),
				"direction":  map[string]any{"type": "string", "enum": []string{"in", "out", "both"}, "description": "out: what the node uses or calls; in: what uses or calls it; both (default)"},
				"edge_types": edgeTypes,
				"depth":      integer("Hops, 1 to 3 (default 1)"),
				"limit":      integer("Maximum number of edges (default 200)"),
				"file":       fileProp,
			}, "node"),
			Annotations: annotations,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Node      string   `json:"node"`
					Direction string   `json:"direction"`
					EdgeTypes []string `json:"edge_types"`
					Depth     int      `json:"depth"`
					Limit     int      `json:"limit"`
					File      string   `json:"file"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				g, err := load(a.File)
				if err != nil {
					return nil, err
				}
				return g.Neighbors(a.Node, ops.NeighborOptions{Direction: a.Direction, EdgeTypes: a.EdgeTypes, Depth: a.Depth, Limit: a.Limit})
			},
		},
		{
			Name: "graph_path", Title: "Find how two nodes are connected",
			Description: "Shortest connection between two nodes of the content graph, following edges in both directions. " +
				"Default edges exposes, calls, sends_to answer how a message gets from one flow (or endpoint) to another; " +
				"add e.g. uses_credential or reads_pd to find other links. found=false when there is none within max_depth. Reads a local file only.",
			InputSchema: object(props{
				"from":       str("Start: " + nodeRef),
				"to":         str("Target: " + nodeRef),
				"edge_types": strArray("Edge types to follow (default: exposes, calls, sends_to)"),
				"max_depth":  integer("Maximum number of hops (default 6)"),
				"file":       fileProp,
			}, "from", "to"),
			Annotations: annotations,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					From      string   `json:"from"`
					To        string   `json:"to"`
					EdgeTypes []string `json:"edge_types"`
					MaxDepth  int      `json:"max_depth"`
					File      string   `json:"file"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				g, err := load(a.File)
				if err != nil {
					return nil, err
				}
				return g.Path(a.From, a.To, a.EdgeTypes, a.MaxDepth)
			},
		},
	}
}
