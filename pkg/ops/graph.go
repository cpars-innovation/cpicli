package ops

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
)

// Node types of the content graph.
const (
	NodePackage      = "package"
	NodeIFlow        = "iflow"
	NodeEndpoint     = "endpoint"     // a sender or ProcessDirect/JMS receiver address
	NodeSystem       = "system"       // a host that receivers call
	NodeCredential   = "credential"   // credential name or key alias
	NodeScript       = "script"       // script content (by hash: identical copies are one node)
	NodeHeader       = "header"       // header set by a Content Modifier
	NodeProperty     = "property"     // exchange property set by a Content Modifier
	NodeCustomHeader = "customheader" // custom header property written by a script
	NodePD           = "pd"           // Partner Directory parameter PID:ID
	NodeODataEntity  = "odata_entity" // <host or service>/<entity set> an OData receiver reads or writes
	NodeIDocType     = "idoc_type"    // IDoc type (ORDERS05, ZORDERS05)
)

// Edge types of the content graph.
const (
	EdgeContains       = "contains"        // package -> iflow
	EdgeExposes        = "exposes"         // iflow -> endpoint it is started by
	EdgeCalls          = "calls"           // iflow -> ProcessDirect/JMS endpoint it sends to
	EdgeSendsTo        = "sends_to"        // iflow -> iflow, through an endpoint (via)
	EdgeCallsSystem    = "calls_system"    // iflow -> system
	EdgeUsesCredential = "uses_credential" // iflow -> credential
	EdgeUsesScript     = "uses_script"     // iflow -> script
	EdgeSetsHeader     = "sets_header"     // iflow -> header
	EdgeSetsProperty   = "sets_property"   // iflow -> property
	EdgeLogsHeader     = "logs_header"     // script -> customheader
	EdgeReadsPD        = "reads_pd"        // iflow -> pd
	EdgeReadsOData     = "reads_odata"     // iflow -> odata_entity (Query, Read)
	EdgeWritesOData    = "writes_odata"    // iflow -> odata_entity (Create, Update, Merge, Delete, Function Import, $batch)
	EdgeReceivesIDoc   = "receives_idoc"   // iflow -> idoc_type, the flow has an IDoc sender channel
	EdgeSendsIDoc      = "sends_idoc"      // iflow -> idoc_type, the flow has an IDoc receiver channel
)

// GraphNodeTypes and GraphEdgeTypes list the types for validation and docs.
var (
	GraphNodeTypes = []string{NodePackage, NodeIFlow, NodeEndpoint, NodeSystem, NodeCredential, NodeScript,
		NodeHeader, NodeProperty, NodeCustomHeader, NodePD, NodeODataEntity, NodeIDocType}
	GraphEdgeTypes = []string{EdgeContains, EdgeExposes, EdgeCalls, EdgeSendsTo, EdgeCallsSystem, EdgeUsesCredential,
		EdgeUsesScript, EdgeSetsHeader, EdgeSetsProperty, EdgeLogsHeader, EdgeReadsPD, EdgeReadsOData, EdgeWritesOData,
		EdgeReceivesIDoc, EdgeSendsIDoc}
	// pathEdgeTypes are followed by GraphPath by default: how messages move.
	pathEdgeTypes = []string{EdgeExposes, EdgeCalls, EdgeSendsTo}
)

// Graph is a discovery as nodes and edges: which flows call which, through
// which addresses, and what they share (credentials, scripts, Partner
// Directory parameters, headers, receiver systems). It is derived from a
// Discovery only; nothing else is read.
type Graph struct {
	GeneratedAt time.Time   `json:"generatedAt"`
	Source      string      `json:"source"`
	Stats       GraphStats  `json:"stats"`
	Nodes       []GraphNode `json:"nodes"`
	Edges       []GraphEdge `json:"edges"`

	index   map[string]int
	out, in map[string][]int
}

// GraphStats counts nodes and edges per type.
type GraphStats struct {
	Nodes     int            `json:"nodes"`
	Edges     int            `json:"edges"`
	NodeTypes map[string]int `json:"nodeTypes"`
	EdgeTypes map[string]int `json:"edgeTypes"`
}

// GraphNode is an entity of the content. ID is "<type>:<key>".
type GraphNode struct {
	ID    string            `json:"id"`
	Type  string            `json:"type"`
	Name  string            `json:"name"`
	Attrs map[string]string `json:"attrs,omitempty"`
}

// GraphEdge is a directed relation between two nodes.
type GraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
	// Adapter is the adapter of exposes, calls, calls_system and OData
	// edges.
	Adapter string `json:"adapter,omitempty"`
	// Via explains derived edges: the endpoint of sends_to, the parameter
	// whose value made the address; for uses_script of a collection script
	// the function the step calls; the operation of reads_odata and
	// writes_odata; where the IDoc type of receives_idoc and sends_idoc was
	// found.
	Via string `json:"via,omitempty"`
}

// linkedAdapters are the adapters whose receiver address matches a sender
// address on the same tenant.
var linkedAdapters = []string{"ProcessDirect", "JMS"}

type graphBuilder struct {
	g     *Graph
	edges map[string]bool
}

func (b *graphBuilder) node(typ, key, name string, attrs map[string]string) string {
	id := typ + ":" + key
	if i, ok := b.g.index[id]; ok {
		n := &b.g.Nodes[i]
		for k, v := range attrs {
			if n.Attrs[k] == "" && v != "" {
				if n.Attrs == nil {
					n.Attrs = map[string]string{}
				}
				n.Attrs[k] = v
			}
		}
		return id
	}
	n := GraphNode{ID: id, Type: typ, Name: name}
	for k, v := range attrs {
		if v != "" {
			if n.Attrs == nil {
				n.Attrs = map[string]string{}
			}
			n.Attrs[k] = v
		}
	}
	b.g.index[id] = len(b.g.Nodes)
	b.g.Nodes = append(b.g.Nodes, n)
	return id
}

func (b *graphBuilder) edge(e GraphEdge) {
	key := e.From + "\x00" + e.Type + "\x00" + e.To
	if b.edges[key] {
		return
	}
	b.edges[key] = true
	b.g.Edges = append(b.g.Edges, e)
}

// BuildGraph derives the content graph from a discovery.
//
// Addresses that are {{parameters}} are resolved with the flow's
// parameters.prop value (edge via "parameter <key>"). An address that cannot
// be resolved gets a node of its own flow, so two flows are never linked by
// the same parameter name alone.
func BuildGraph(d *Discovery) *Graph {
	b := &graphBuilder{g: &Graph{GeneratedAt: d.GeneratedAt, Source: d.Source, index: map[string]int{}}, edges: map[string]bool{}}
	for _, p := range d.Packages {
		b.node(NodePackage, p.ID, firstNonEmpty(p.Name, p.ID), nil)
	}
	// scripts of script collections: one node per collection script, with
	// what it says it does (summary, functions) so that search finds it
	collectionScript := func(collection, script string) string {
		return b.node(NodeScript, collection+"/"+script, collection+"/"+script, map[string]string{"collection": collection})
	}
	for _, c := range d.ScriptCollections {
		for _, s := range c.Scripts {
			var fns []string
			for _, fn := range s.Functions {
				if !fn.Private {
					fns = append(fns, strings.TrimSpace(fn.Name+": "+fn.Summary))
				}
			}
			attrs := scriptAttrs(s, true)
			attrs["collection"], attrs["package"], attrs["functions"] = c.ID, c.PackageID, strings.Join(fns, "; ")
			script := b.node(NodeScript, c.ID+"/"+s.Name, c.ID+"/"+s.Name, attrs)
			for _, h := range s.CustomHeaders {
				b.edge(GraphEdge{From: script, To: b.node(NodeCustomHeader, h, h, nil), Type: EdgeLogsHeader})
			}
		}
	}
	for _, f := range d.IFlows {
		flow := b.node(NodeIFlow, f.ID, firstNonEmpty(f.Name, f.ID), map[string]string{
			"package": f.PackageID, "version": f.Version, "runtimeStatus": f.RuntimeStatus, "path": f.Path,
			"logLevel": f.LogLevel, "triggers": triggerSummary(f.Triggers), "error": f.Error, "idocTypes": idocTypeSummary(f.IDocTypes),
		})
		if f.PackageID != "" {
			b.edge(GraphEdge{From: b.node(NodePackage, f.PackageID, f.PackageID, nil), To: flow, Type: EdgeContains})
		}
		for _, t := range f.Triggers {
			if t.Address == "" {
				continue
			}
			ep, via := b.endpoint(f, t)
			b.edge(GraphEdge{From: flow, To: ep, Type: EdgeExposes, Adapter: t.Adapter, Via: via})
		}
		for _, r := range f.Receivers {
			if r.Address == "" {
				continue
			}
			if slices.Contains(linkedAdapters, r.Adapter) {
				ep, via := b.endpoint(f, r)
				b.edge(GraphEdge{From: flow, To: ep, Type: EdgeCalls, Adapter: r.Adapter, Via: via})
				continue
			}
			addr, via, ok := resolveAddress(r.Address, f.AddressParameters)
			if host := urlHost(addr); ok && host != "" {
				b.edge(GraphEdge{From: flow, To: b.node(NodeSystem, host, host, nil), Type: EdgeCallsSystem, Adapter: r.Adapter, Via: via})
			}
		}
		for _, c := range f.CredentialRefs {
			if !strings.Contains(c, "{{") && !strings.Contains(c, "${") {
				b.edge(GraphEdge{From: flow, To: b.node(NodeCredential, c, c, nil), Type: EdgeUsesCredential})
			}
		}
		for _, r := range f.CollectionScripts {
			b.edge(GraphEdge{From: flow, To: collectionScript(r.Collection, r.Script), Type: EdgeUsesScript, Via: r.Function})
		}
		for _, s := range f.Scripts {
			script := b.node(NodeScript, s.Hash, s.Name, scriptAttrs(s, false))
			b.edge(GraphEdge{From: flow, To: script, Type: EdgeUsesScript})
			for _, h := range s.CustomHeaders {
				b.edge(GraphEdge{From: script, To: b.node(NodeCustomHeader, h, h, nil), Type: EdgeLogsHeader})
			}
		}
		for _, h := range f.HeadersSet {
			b.edge(GraphEdge{From: flow, To: b.node(NodeHeader, h, h, nil), Type: EdgeSetsHeader})
		}
		for _, p := range f.PropertiesSet {
			b.edge(GraphEdge{From: flow, To: b.node(NodeProperty, p, p, nil), Type: EdgeSetsProperty})
		}
		for _, ref := range f.PDReferences {
			b.edge(GraphEdge{From: flow, To: b.node(NodePD, ref, ref, nil), Type: EdgeReadsPD})
		}
		b.sapEdges(f, flow)
	}
	b.g.buildIndex()
	// flow -> flow through the addresses they share
	var derived []GraphEdge
	for _, n := range b.g.Nodes {
		if n.Type != NodeEndpoint {
			continue
		}
		var callers, receivers []string
		for _, i := range b.g.in[n.ID] {
			switch e := b.g.Edges[i]; e.Type {
			case EdgeCalls:
				callers = append(callers, e.From)
			case EdgeExposes:
				receivers = append(receivers, e.From)
			}
		}
		for _, from := range callers {
			for _, to := range receivers {
				derived = append(derived, GraphEdge{From: from, To: to, Type: EdgeSendsTo, Adapter: n.Attrs["adapter"], Via: n.ID})
			}
		}
	}
	for _, e := range derived {
		b.edge(e)
	}
	b.countScriptUsages()
	g := b.g
	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	sort.Slice(g.Edges, func(i, j int) bool {
		a, c := g.Edges[i], g.Edges[j]
		return a.From+"\x00"+a.Type+"\x00"+a.To < c.From+"\x00"+c.Type+"\x00"+c.To
	})
	g.buildIndex()
	return g
}

// sapEdges links a flow to the OData entities its OData receivers read or
// write and to the IDoc types it receives or sends.
func (b *graphBuilder) sapEdges(f IFlowFacts, flow string) {
	for _, r := range f.Receivers {
		o := r.OData
		if o == nil || o.EntitySet == "" {
			continue
		}
		typ := map[string]string{ODataRead: EdgeReadsOData, ODataWrite: EdgeWritesOData}[ODataOperationKind(o.Operation, o.Batch)]
		if typ == "" {
			continue
		}
		service, attrs := o.Host, map[string]string{"version": o.Version, "entitySet": o.EntitySet, "host": o.Host}
		switch {
		case service != "":
		case r.Adapter == "SuccessFactors":
			service = "SuccessFactors"
		default:
			// no resolvable host: an entity of this flow only
			service = firstNonEmpty(r.Address, r.Adapter)
			attrs["unresolved"], attrs["iflow"] = "true", f.ID
		}
		key := service + "/" + o.EntitySet
		if attrs["unresolved"] != "" {
			key += "@" + f.ID
		}
		id := b.node(NodeODataEntity, key, service+"/"+o.EntitySet, attrs)
		op := firstNonEmpty(o.Operation, "$batch")
		n := &b.g.Nodes[b.g.index[id]]
		if ops := strings.Split(n.Attrs["operations"], ", "); !slices.Contains(ops, op) {
			n.Attrs["operations"] = strings.TrimPrefix(n.Attrs["operations"]+", "+op, ", ")
		}
		b.edge(GraphEdge{From: flow, To: id, Type: typ, Adapter: r.Adapter, Via: op})
	}
	for _, t := range f.IDocTypes {
		for dir, typ := range map[string]string{"Sender": EdgeReceivesIDoc, "Receiver": EdgeSendsIDoc} {
			if f.HasIDocChannel(dir) {
				b.edge(GraphEdge{From: flow, To: b.node(NodeIDocType, t.Type, t.Type, nil), Type: typ, Via: strings.Join(t.Sources, ", ")})
			}
		}
	}
}

func idocTypeSummary(types []IDocType) string {
	names := make([]string, 0, len(types))
	for _, t := range types {
		names = append(names, t.Type)
	}
	return strings.Join(names, ", ")
}

// endpoint returns the node of a sender or linked receiver address.
func (b *graphBuilder) endpoint(f IFlowFacts, t Trigger) (id, via string) {
	addr, via, ok := resolveAddress(t.Address, f.AddressParameters)
	if !ok {
		// unresolved: an endpoint of this flow only
		return b.node(NodeEndpoint, t.Adapter+":"+addr+"@"+f.ID, addr,
			map[string]string{"adapter": t.Adapter, "unresolved": "true", "iflow": f.ID}), ""
	}
	return b.node(NodeEndpoint, t.Adapter+":"+addr, addr, map[string]string{"adapter": t.Adapter}), via
}

// resolveAddress replaces {{parameter}} references with their values. ok is
// false when a reference has no value or the address is computed at runtime
// (${...}).
func resolveAddress(addr string, params map[string]string) (resolved, via string, ok bool) {
	addr = strings.TrimSpace(addr)
	var used []string
	ok = true
	resolved = reParameterRef.ReplaceAllStringFunc(addr, func(ref string) string {
		key := ref[2 : len(ref)-2]
		if v, found := params[key]; found && v != "" {
			used = append(used, key)
			return v
		}
		ok = false
		return ref
	})
	if strings.Contains(resolved, "${") {
		ok = false
	}
	if len(used) > 0 {
		via = "parameter " + strings.Join(used, ", ")
	}
	return resolved, via, ok
}

func urlHost(addr string) string {
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func triggerSummary(ts []Trigger) string {
	parts := make([]string, 0, len(ts))
	for _, t := range ts {
		parts = append(parts, strings.TrimSpace(t.Adapter+" "+t.Address))
	}
	return strings.Join(parts, ", ")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func (g *Graph) buildIndex() {
	g.index = make(map[string]int, len(g.Nodes))
	for i, n := range g.Nodes {
		g.index[n.ID] = i
	}
	g.out, g.in = map[string][]int{}, map[string][]int{}
	g.Stats = GraphStats{Nodes: len(g.Nodes), Edges: len(g.Edges), NodeTypes: map[string]int{}, EdgeTypes: map[string]int{}}
	for _, n := range g.Nodes {
		g.Stats.NodeTypes[n.Type]++
	}
	for i, e := range g.Edges {
		g.out[e.From] = append(g.out[e.From], i)
		g.in[e.To] = append(g.in[e.To], i)
		g.Stats.EdgeTypes[e.Type]++
	}
}

// WriteGraph writes g as indented JSON to file, creating its directory.
func WriteGraph(g *Graph, file string) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(file, append(data, '\n'), 0o644)
}

// GraphFile is the graph file written next to a discovery file.
func GraphFile(discoveryFile string) string {
	return filepath.Join(filepath.Dir(discoveryFile), "graph.json")
}

var graphCache struct {
	sync.Mutex
	entries map[string]graphCacheEntry
}

type graphCacheEntry struct {
	mod  time.Time
	size int64
	g    *Graph
}

// LoadGraph reads a graph file (cached until the file changes). Without the
// file, it builds the graph from discovery.json in the same directory.
func LoadGraph(file string) (*Graph, error) {
	info, err := os.Stat(file)
	if os.IsNotExist(err) {
		disc := filepath.Join(filepath.Dir(file), "discovery.json")
		if _, derr := os.Stat(disc); derr == nil {
			return graphFromDiscoveryFile(disc)
		}
		return nil, output.Usagef("%s not found: run discover (MCP: discover_tenant) first", file)
	}
	if err != nil {
		return nil, err
	}
	graphCache.Lock()
	defer graphCache.Unlock()
	if e, ok := graphCache.entries[file]; ok && e.mod.Equal(info.ModTime()) && e.size == info.Size() {
		return e.g, nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	g := &Graph{}
	if err := json.Unmarshal(data, g); err != nil {
		return nil, output.Usagef("%s is not a graph file: %v", file, err)
	}
	g.buildIndex()
	if graphCache.entries == nil {
		graphCache.entries = map[string]graphCacheEntry{}
	}
	graphCache.entries[file] = graphCacheEntry{mod: info.ModTime(), size: info.Size(), g: g}
	return g, nil
}

func graphFromDiscoveryFile(file string) (*Graph, error) {
	d, err := ReadDiscovery(file)
	if err != nil {
		return nil, err
	}
	return BuildGraph(d), nil
}

// ReadDiscovery reads a file written by WriteDiscovery.
func ReadDiscovery(file string) (*Discovery, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, output.Usagef("%s not found: run discover (MCP: discover_tenant) first", file)
		}
		return nil, err
	}
	d := &Discovery{}
	if err := json.Unmarshal(data, d); err != nil {
		return nil, output.Usagef("%s is not a discovery file: %v", file, err)
	}
	return d, nil
}

// Node returns the node with this ID.
func (g *Graph) Node(id string) (GraphNode, bool) {
	i, ok := g.index[id]
	if !ok {
		return GraphNode{}, false
	}
	return g.Nodes[i], true
}

// Resolve finds a node by ID ("iflow:Orders_In"), by key of any type
// ("Orders_In", iflows first) or by name (case-insensitive, must be unique).
func (g *Graph) Resolve(ref string) (GraphNode, error) {
	ref = strings.TrimSpace(ref)
	if n, ok := g.Node(ref); ok {
		return n, nil
	}
	for _, typ := range GraphNodeTypes {
		if n, ok := g.Node(typ + ":" + ref); ok {
			return n, nil
		}
	}
	var found []GraphNode
	for _, n := range g.Nodes {
		if strings.EqualFold(n.Name, ref) {
			found = append(found, n)
		}
	}
	switch len(found) {
	case 0:
		return GraphNode{}, output.Usagef("no node %q in the graph: use graph_search (cpictl graph search) to find it", ref)
	case 1:
		return found[0], nil
	}
	ids := make([]string, 0, len(found))
	for _, n := range found[:min(len(found), 10)] {
		ids = append(ids, n.ID)
	}
	return GraphNode{}, output.Usagef("%q matches %d nodes, use the node ID: %s", ref, len(found), strings.Join(ids, ", "))
}

// GraphMatch is a search hit.
type GraphMatch struct {
	GraphNode
	// Degree is the number of edges of the node.
	Degree int `json:"degree"`
}

// GraphSearchResult lists the best matches.
type GraphSearchResult struct {
	Matches []GraphMatch `json:"matches"`
	// Total is the number of matching nodes (Matches is limited).
	Total int `json:"total"`
}

// Search finds nodes whose key, name or attribute values contain query
// (case-insensitive); exact and prefix matches come first. types limits the
// node types (empty: all).
func (g *Graph) Search(query string, types []string, limit int) (*GraphSearchResult, error) {
	if err := checkTypes(types, GraphNodeTypes, "node"); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	q := strings.ToLower(strings.TrimSpace(query))
	type hit struct {
		m    GraphMatch
		rank int
	}
	var hits []hit
	for _, n := range g.Nodes {
		if len(types) > 0 && !slices.Contains(types, n.Type) {
			continue
		}
		rank := matchRank(q, n)
		if rank < 0 {
			continue
		}
		hits = append(hits, hit{GraphMatch{GraphNode: n, Degree: len(g.out[n.ID]) + len(g.in[n.ID])}, rank})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].rank != hits[j].rank {
			return hits[i].rank < hits[j].rank
		}
		if hits[i].m.Degree != hits[j].m.Degree {
			return hits[i].m.Degree > hits[j].m.Degree
		}
		return hits[i].m.ID < hits[j].m.ID
	})
	res := &GraphSearchResult{Matches: []GraphMatch{}, Total: len(hits)}
	for _, h := range hits[:min(len(hits), limit)] {
		res.Matches = append(res.Matches, h.m)
	}
	return res, nil
}

// matchRank: 0 exact key or name, 1 prefix, 2 contained in key or name, 3
// contained in an attribute, 4 every word of a query of several words
// somewhere in the key, name or attributes ("format date" finds a script
// whose summary says "Formats an SAP date"); -1 no match. An empty query
// matches everything.
func matchRank(q string, n GraphNode) int {
	if q == "" {
		return 0
	}
	key := strings.ToLower(strings.TrimPrefix(n.ID, n.Type+":"))
	name := strings.ToLower(n.Name)
	switch {
	case key == q || name == q || strings.ToLower(n.ID) == q:
		return 0
	case strings.HasPrefix(key, q) || strings.HasPrefix(name, q):
		return 1
	case strings.Contains(key, q) || strings.Contains(name, q):
		return 2
	}
	for _, v := range n.Attrs {
		if strings.Contains(strings.ToLower(v), q) {
			return 3
		}
	}
	if words := strings.Fields(q); len(words) > 1 {
		all := []string{key, name}
		for _, v := range n.Attrs {
			all = append(all, strings.ToLower(v))
		}
		text := strings.Join(all, " ")
		for _, w := range words {
			if !strings.Contains(text, w) {
				return -1
			}
		}
		return 4
	}
	return -1
}

// NeighborOptions select the neighbourhood of a node.
type NeighborOptions struct {
	// Direction is out (what the node uses or calls), in (what uses or
	// calls it) or both (default).
	Direction string
	// EdgeTypes limits the edges followed (empty: all).
	EdgeTypes []string
	// Depth is the number of hops, 1 to 3 (default 1).
	Depth int
	// Limit is the maximum number of edges returned (default 200).
	Limit int
}

// Subgraph is a part of the graph.
type Subgraph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
	// Truncated is true when the limit cut the result.
	Truncated bool `json:"truncated,omitempty"`
}

// Neighbors returns the nodes reachable from ref within opts.Depth hops.
func (g *Graph) Neighbors(ref string, opts NeighborOptions) (*Subgraph, error) {
	start, err := g.Resolve(ref)
	if err != nil {
		return nil, err
	}
	if opts.Direction == "" {
		opts.Direction = "both"
	}
	if !slices.Contains([]string{"in", "out", "both"}, opts.Direction) {
		return nil, output.Usagef("invalid direction %q (in, out, both)", opts.Direction)
	}
	if err := checkTypes(opts.EdgeTypes, GraphEdgeTypes, "edge"); err != nil {
		return nil, err
	}
	if opts.Depth <= 0 {
		opts.Depth = 1
	}
	if opts.Depth > 3 {
		return nil, output.Usagef("depth must be 1 to 3")
	}
	if opts.Limit <= 0 {
		opts.Limit = 200
	}
	res := &Subgraph{Nodes: []GraphNode{start}, Edges: []GraphEdge{}}
	seen := map[string]bool{start.ID: true}
	seenEdge := map[int]bool{}
	frontier := []string{start.ID}
	for depth := 0; depth < opts.Depth && len(frontier) > 0; depth++ {
		var next []string
		for _, id := range frontier {
			for _, i := range g.adjacent(id, opts.Direction) {
				e := g.Edges[i]
				if seenEdge[i] || (len(opts.EdgeTypes) > 0 && !slices.Contains(opts.EdgeTypes, e.Type)) {
					continue
				}
				if len(res.Edges) >= opts.Limit {
					res.Truncated = true
					return res, nil
				}
				seenEdge[i] = true
				res.Edges = append(res.Edges, e)
				other := e.To
				if other == id {
					other = e.From
				}
				if !seen[other] {
					seen[other] = true
					n, _ := g.Node(other)
					res.Nodes = append(res.Nodes, n)
					next = append(next, other)
				}
			}
		}
		frontier = next
	}
	return res, nil
}

func (g *Graph) adjacent(id, direction string) []int {
	switch direction {
	case "out":
		return g.out[id]
	case "in":
		return g.in[id]
	}
	return append(slices.Clone(g.out[id]), g.in[id]...)
}

// GraphPathResult is the shortest connection between two nodes.
type GraphPathResult struct {
	Found bool        `json:"found"`
	Nodes []GraphNode `json:"nodes"`
	// Edges are in path order; an edge may point against the path direction.
	Edges []GraphEdge `json:"edges"`
}

// Path finds the shortest connection between two nodes over edgeTypes
// (default: exposes, calls, sends_to — how messages move), following edges
// in both directions, up to maxDepth hops (default 6).
func (g *Graph) Path(from, to string, edgeTypes []string, maxDepth int) (*GraphPathResult, error) {
	a, err := g.Resolve(from)
	if err != nil {
		return nil, err
	}
	b, err := g.Resolve(to)
	if err != nil {
		return nil, err
	}
	if err := checkTypes(edgeTypes, GraphEdgeTypes, "edge"); err != nil {
		return nil, err
	}
	if len(edgeTypes) == 0 {
		edgeTypes = pathEdgeTypes
	}
	if maxDepth <= 0 {
		maxDepth = 6
	}
	res := &GraphPathResult{Nodes: []GraphNode{}, Edges: []GraphEdge{}}
	prev := map[string]int{a.ID: -1} // node -> edge index used to reach it
	frontier := []string{a.ID}
	for depth := 0; depth < maxDepth && len(frontier) > 0 && !hasKey(prev, b.ID); depth++ {
		var next []string
		for _, id := range frontier {
			for _, i := range g.adjacent(id, "both") {
				e := g.Edges[i]
				if !slices.Contains(edgeTypes, e.Type) {
					continue
				}
				other := e.To
				if other == id {
					other = e.From
				}
				if !hasKey(prev, other) {
					prev[other] = i
					next = append(next, other)
				}
			}
		}
		frontier = next
	}
	if !hasKey(prev, b.ID) {
		return res, nil
	}
	res.Found = true
	for id := b.ID; ; {
		n, _ := g.Node(id)
		res.Nodes = append(res.Nodes, n)
		i := prev[id]
		if i < 0 {
			break
		}
		e := g.Edges[i]
		res.Edges = append(res.Edges, e)
		if e.To == id {
			id = e.From
		} else {
			id = e.To
		}
	}
	slices.Reverse(res.Nodes)
	slices.Reverse(res.Edges)
	return res, nil
}

func hasKey[V any](m map[string]V, k string) bool {
	_, ok := m[k]
	return ok
}

func checkTypes(types, valid []string, kind string) error {
	for _, t := range types {
		if !slices.Contains(valid, t) {
			return output.Usagef("invalid %s type %q (%s)", kind, t, strings.Join(valid, ", "))
		}
	}
	return nil
}

// scriptAttrs are what a script node says for reuse: what it does
// (summary, keywords), what it touches (reads, writes), whether its doc is
// good enough to reuse it (docStatus), scripts that differ only in comments
// and formatting (same shape) and whether it is meant for reuse (reusable:
// the scripts of script collections).
func scriptAttrs(s ScriptFacts, collection bool) map[string]string {
	a := map[string]string{"language": s.Language, "lines": fmt.Sprint(s.Lines), "summary": s.Summary,
		"reusable": fmt.Sprint(collection), "shape": s.Shape}
	set := func(k, v string) {
		if v != "" {
			a[k] = v
		}
	}
	set("keywords", strings.Join(s.Keywords, ", "))
	set("reads", strings.Join(s.Reads, ", "))
	set("writes", strings.Join(s.Writes, ", "))
	set("docStatus", s.DocStatus)
	return a
}

// countScriptUsages sets "usages" on each script node: the number of flows
// that use it (a flow script used by several flows is one node, a
// candidate for a script collection).
func (b *graphBuilder) countScriptUsages() {
	count := map[string]int{}
	for _, e := range b.g.Edges {
		if e.Type == EdgeUsesScript {
			count[e.To]++
		}
	}
	for i, n := range b.g.Nodes {
		if n.Type != NodeScript {
			continue
		}
		if n.Attrs == nil {
			b.g.Nodes[i].Attrs = map[string]string{}
		}
		b.g.Nodes[i].Attrs["usages"] = fmt.Sprint(count[n.ID])
	}
}
