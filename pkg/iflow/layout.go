package iflow

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/beevik/etree"
)

// Layout modes.
const (
	// LayoutTidy keeps the order of steps and branches and recomputes
	// positions, spacing and lines.
	LayoutTidy = "tidy"
	// LayoutFull also reorders branches to reduce crossing lines.
	LayoutFull = "full"
)

// LayoutOptions control the diagram layout.
type LayoutOptions struct {
	Mode string `yaml:"mode" json:"mode,omitempty"`
	// HGap is the space between columns of steps, VGap between rows.
	HGap float64 `yaml:"hgap" json:"hgap,omitempty"`
	VGap float64 `yaml:"vgap" json:"vgap,omitempty"`
}

// Default spacing.
const (
	DefaultHGap = 60
	DefaultVGap = 40
)

// Normalized fills in the defaults and checks the mode.
func (o LayoutOptions) Normalized() (LayoutOptions, error) {
	if o.Mode == "" {
		o.Mode = LayoutTidy
	}
	if o.Mode != LayoutTidy && o.Mode != LayoutFull {
		return o, fmt.Errorf("layout mode %q: tidy or full", o.Mode)
	}
	if o.HGap <= 0 {
		o.HGap = DefaultHGap
	}
	if o.VGap <= 0 {
		o.VGap = DefaultVGap
	}
	return o, nil
}

// LayoutResult says what Layout changed.
type LayoutResult struct {
	Changed bool `json:"changed"`
	// Moved shapes (moved or resized), Rerouted lines, Added shapes and
	// lines that were missing from the diagram.
	Moved    int `json:"moved"`
	Rerouted int `json:"rerouted"`
	Added    int `json:"added"`
}

// NewDocument returns an etree document that writes the XML of an .iflw
// as it was read: only &, < and > (and " in attributes) are escaped.
func NewDocument() *etree.Document {
	d := etree.NewDocument()
	d.WriteSettings.CanonicalText = true
	d.WriteSettings.CanonicalAttrVal = true
	return d
}

// diagram sizes
const (
	eventSize    = 32
	gatewaySize  = 40
	stepW, stepH = 100, 60
	endpointW    = 100
	endpointH    = 140
	margin       = 40
	topMargin    = 60
	endpointGap  = 80
	poolPadL     = 50
	poolPadR     = 40
	poolPadT     = 40
	poolPadB     = 40
	poolGap      = 60
	subPadX      = 30
	subPadTop    = 40
	subPadBottom = 30
	channelStep  = 6
)

// nonNodes are children of a process that are not drawn as flow nodes.
var nonNodes = map[string]bool{
	"sequenceFlow": true, "extensionElements": true, "documentation": true, "incoming": true, "outgoing": true,
	"laneSet": true, "textAnnotation": true, "association": true, "conditionExpression": true,
	"multiInstanceLoopCharacteristics": true, "standardLoopCharacteristics": true, "ioSpecification": true,
	"dataInputAssociation": true, "dataOutputAssociation": true, "property": true,
}

type rect struct{ x, y, w, h float64 }

func (r rect) cx() float64     { return r.x + r.w/2 }
func (r rect) cy() float64     { return r.y + r.h/2 }
func (r rect) right() float64  { return r.x + r.w }
func (r rect) bottom() float64 { return r.y + r.h }
func (r rect) center() point   { return point{r.cx(), r.cy()} }

func (r rect) rounded() rect {
	return rect{math.Round(r.x), math.Round(r.y), math.Round(r.w), math.Round(r.h)}
}

type point struct{ x, y float64 }

// lnode is a node of the layout graph: a flow node or a dummy on a line
// that spans several columns.
type lnode struct {
	id           string
	el           *etree.Element
	tag          string
	w, h         float64
	x, y         float64 // centre, relative to the block
	layer, order int
	seq, dfs     int
	key          float64
	preds, succs []*lnode
	dummy        bool
	free         bool
	sub          *lblock
	old          *rect
	block        *lblock
}

// lchain is a sequence flow through its dummies, in layer order.
type lchain struct {
	flow     string
	nodes    []*lnode
	reversed bool
}

// lblock is the laid out content of a process or subprocess.
type lblock struct {
	nodes      []*lnode // flow nodes
	layers     [][]*lnode
	chains     []*lchain
	colL, colR []float64
	w, h       float64
	ox, oy     float64 // absolute origin once placed
	pool       string  // participant of the process (lines to endpoints)
}

type layouter struct {
	o       LayoutOptions
	byID    map[string]*etree.Element
	shapes  map[string]*etree.Element
	edges   map[string]*etree.Element
	old     map[string]rect
	pos     map[string]rect
	routes  map[string][]point
	node    map[string]*lnode
	parent  map[string]string // node -> enclosing subprocess
	poolOf  map[string]rect   // node -> pool rectangle
	placed  []string          // shapes in placement order
	routed  []string          // lines in routing order
	channel map[string]int    // pool rectangle key -> channels used
}

// Layout recomputes the diagram (BPMNDiagram) of an .iflw model: steps left
// to right in flow order, branches one below the other, exception
// subprocesses below the main flow, senders left of the integration process,
// receivers above (or below) the step that calls them, right-angled lines. Only the diagram changes, never
// the steps, their configuration or the sequence flows.
func Layout(data []byte, o LayoutOptions) ([]byte, *LayoutResult, error) {
	o, err := o.Normalized()
	if err != nil {
		return nil, nil, err
	}
	doc := NewDocument()
	if err := doc.ReadFromBytes(data); err != nil {
		return nil, nil, err
	}
	l := newLayouter(o, doc)
	plane := findFirst(doc.Root(), "BPMNPlane")
	if plane == nil {
		return nil, nil, fmt.Errorf("the model has no diagram (BPMNPlane)")
	}
	l.layoutAll(doc.Root())
	res := l.write(doc, plane)
	var out bytes.Buffer
	if _, err := doc.WriteTo(&out); err != nil {
		return nil, nil, err
	}
	res.Changed = !bytes.Equal(out.Bytes(), data)
	if !res.Changed {
		return data, res, nil
	}
	return out.Bytes(), res, nil
}

func newLayouter(o LayoutOptions, doc *etree.Document) *layouter {
	l := &layouter{o: o, byID: map[string]*etree.Element{}, shapes: map[string]*etree.Element{}, edges: map[string]*etree.Element{},
		old: map[string]rect{}, pos: map[string]rect{}, routes: map[string][]point{}, node: map[string]*lnode{},
		parent: map[string]string{}, poolOf: map[string]rect{}, channel: map[string]int{}}
	walk(doc.Root(), func(el *etree.Element) {
		switch el.Tag {
		case "BPMNShape":
			id := el.SelectAttrValue("bpmnElement", "")
			l.shapes[id] = el
			if b := child(el, "Bounds"); b != nil {
				l.old[id] = rect{num(b, "x"), num(b, "y"), num(b, "width"), num(b, "height")}
			}
		case "BPMNEdge":
			l.edges[el.SelectAttrValue("bpmnElement", "")] = el
		default:
			if id := el.SelectAttrValue("id", ""); id != "" && !inDiagram(el) {
				l.byID[id] = el
			}
		}
	})
	return l
}

func (l *layouter) layoutAll(root *etree.Element) {
	var participants, flows []*etree.Element
	if collab := child(root, "collaboration"); collab != nil {
		for _, c := range collab.ChildElements() {
			switch c.Tag {
			case "participant":
				participants = append(participants, c)
			case "messageFlow":
				flows = append(flows, c)
			}
		}
	}
	// processes in the order of their participants
	poolFor := map[string]string{}
	var processes []*etree.Element
	seen := map[string]bool{}
	var endpoints []*etree.Element
	for _, p := range participants {
		ref := p.SelectAttrValue("processRef", "")
		if ref == "" {
			endpoints = append(endpoints, p)
			continue
		}
		if proc := l.byID[ref]; proc != nil && !seen[ref] {
			seen[ref] = true
			processes = append(processes, proc)
			poolFor[ref] = p.SelectAttrValue("id", "")
		}
	}
	for _, c := range root.ChildElements() {
		if c.Tag == "process" && !seen[c.SelectAttrValue("id", "")] {
			processes = append(processes, c)
		}
	}

	var senders, receivers []*etree.Element
	for _, p := range endpoints {
		id := p.SelectAttrValue("id", "")
		src, tgt := false, false
		for _, f := range flows {
			src = src || f.SelectAttrValue("sourceRef", "") == id
			tgt = tgt || f.SelectAttrValue("targetRef", "") == id
		}
		if src || (!tgt && strings.Contains(p.SelectAttrValue("type", ""), "Sender")) {
			senders = append(senders, p)
		} else {
			receivers = append(receivers, p)
		}
	}

	blocks := make([]*lblock, len(processes))
	contentW := 0.0
	for i, proc := range processes {
		blocks[i] = l.layoutContainer(proc)
		blocks[i].pool = poolFor[proc.SelectAttrValue("id", "")]
		contentW = math.Max(contentW, blocks[i].w)
	}
	x0 := float64(margin)
	if len(senders) > 0 {
		x0 += endpointW + endpointGap
	}
	poolW := math.Max(contentW+poolPadL+poolPadR, 400)
	heights := make([]float64, len(blocks))
	for i, b := range blocks {
		heights[i] = math.Max(b.h+poolPadT+poolPadB, 160)
	}
	placePools := func(bandAbove, bandBelow []bool) []rect {
		y := float64(topMargin)
		var pools []rect
		for i, b := range blocks {
			if bandAbove != nil && bandAbove[i] {
				y += endpointH + endpointGap
			}
			h := heights[i]
			pool := rect{x0, y, poolW, h}.rounded()
			pools = append(pools, pool)
			if b.pool != "" {
				l.set(b.pool, pool)
			}
			l.place(b, pool.x+poolPadL, pool.y+poolPadT+math.Round((h-poolPadT-poolPadB-b.h)/2), pool)
			y += h + poolGap
			if bandBelow != nil && bandBelow[i] {
				y += endpointH + endpointGap
			}
		}
		return pools
	}
	pools := placePools(nil, nil)

	// receivers above (else below) the step that calls them, with a straight
	// line when nothing of the pool is in between; the others to the right
	poolIndex := map[rect]int{}
	for i, p := range pools {
		poolIndex[p] = i
	}
	type stacked struct {
		id, step string
		pool     int
		below    bool
	}
	var near []stacked
	var right []*etree.Element
	bandAbove, bandBelow := make([]bool, len(pools)), make([]bool, len(pools))
	for _, p := range receivers {
		id := p.SelectAttrValue("id", "")
		step := ""
		for _, f := range flows {
			if f.SelectAttrValue("targetRef", "") == id {
				if _, ok := l.node[f.SelectAttrValue("sourceRef", "")]; ok {
					step = f.SelectAttrValue("sourceRef", "")
					break
				}
			}
		}
		i, ok := poolIndex[l.poolOf[step]]
		if step == "" || !ok {
			right = append(right, p)
			continue
		}
		r, pool := l.pos[step], pools[i]
		switch {
		case l.clearV(r.cx(), pool.y, r.y, step):
			near, bandAbove[i] = append(near, stacked{id, step, i, false}), true
		case l.clearV(r.cx(), r.bottom(), pool.bottom(), step):
			near, bandBelow[i] = append(near, stacked{id, step, i, true}), true
		default:
			right = append(right, p)
		}
	}
	if len(near) > 0 {
		pools = placePools(bandAbove, bandBelow)
		sort.SliceStable(near, func(i, j int) bool { return l.pos[near[i].step].cx() < l.pos[near[j].step].cx() })
		for i := range pools {
			for _, below := range []bool{false, true} {
				var group []stacked
				for _, n := range near {
					if n.pool == i && n.below == below {
						group = append(group, n)
					}
				}
				if len(group) == 0 {
					continue
				}
				desired, seps := make([]float64, len(group)), make([]float64, len(group))
				for k, n := range group {
					desired[k], seps[k] = l.pos[n.step].cx()-endpointW/2, endpointW+20
				}
				xs := isotonic(desired, seps)
				shift := math.Max(0, margin-xs[0])
				y := pools[i].y - endpointGap - endpointH
				if below {
					y = pools[i].bottom() + endpointGap
				}
				for k, n := range group {
					l.set(n.id, rect{xs[k] + shift, y, endpointW, endpointH}.rounded())
				}
			}
		}
	}
	poolRight := x0 + poolW
	if len(pools) == 0 {
		poolRight = x0 + 400
	}
	defaultY := float64(topMargin) + 80
	if len(pools) > 0 {
		defaultY = pools[0].cy()
	}

	// senders left, level with the step they talk to
	l.placeEndpoints(senders, flows, "targetRef", margin, defaultY)
	l.placeEndpoints(right, flows, "sourceRef", poolRight+endpointGap, defaultY)

	// lines
	for _, b := range blocks {
		l.routeBlock(b)
	}
	for _, f := range flows {
		l.routeMessageFlow(f)
	}
}

// layoutContainer lays out the flow nodes of a process or subprocess
// relative to (0,0).
func (l *layouter) layoutContainer(c *etree.Element) *lblock {
	b := &lblock{}
	ids := map[string]*lnode{}
	var flows []*etree.Element
	for _, ch := range c.ChildElements() {
		id := ch.SelectAttrValue("id", "")
		switch {
		case ch.Tag == "sequenceFlow":
			flows = append(flows, ch)
		case id == "" || nonNodes[ch.Tag]:
		default:
			n := &lnode{id: id, el: ch, tag: ch.Tag, seq: len(b.nodes), block: b}
			if r, ok := l.old[id]; ok {
				n.old = &r
			}
			if ch.Tag == "subProcess" {
				n.sub = l.layoutContainer(ch)
				n.w = math.Max(n.sub.w+2*subPadX, 160)
				n.h = math.Max(n.sub.h+subPadTop+subPadBottom, 100)
				for _, inner := range n.sub.nodes {
					l.parent[inner.id] = id
				}
			} else {
				n.w, n.h = nodeSize(n)
			}
			ids[id] = n
			l.node[id] = n
			b.nodes = append(b.nodes, n)
		}
	}
	type edge struct {
		id   string
		s, t *lnode
	}
	var edges []edge
	connected := map[*lnode]bool{}
	for _, f := range flows {
		s, t := ids[f.SelectAttrValue("sourceRef", "")], ids[f.SelectAttrValue("targetRef", "")]
		if s == nil || t == nil || s == t {
			continue
		}
		edges = append(edges, edge{f.SelectAttrValue("id", ""), s, t})
		connected[s], connected[t] = true, true
	}
	var graph, free []*lnode
	for _, n := range b.nodes {
		if n.tag == "subProcess" && !connected[n] {
			n.free = true
			free = append(free, n)
		} else {
			graph = append(graph, n)
		}
	}

	// depth-first search from the start events: discovery order and the
	// edges that close a cycle (laid out reversed)
	out := map[*lnode][]edge{}
	in := map[*lnode]int{}
	for _, e := range edges {
		out[e.s] = append(out[e.s], e)
		in[e.t]++
	}
	state := map[*lnode]int{} // 1 on stack, 2 done
	back := map[string]bool{}
	dfs := 0
	var visit func(n *lnode)
	visit = func(n *lnode) {
		state[n] = 1
		n.dfs = dfs
		dfs++
		for _, e := range out[n] {
			switch state[e.t] {
			case 0:
				visit(e.t)
			case 1:
				back[e.id] = true
			}
		}
		state[n] = 2
	}
	var roots []*lnode
	for _, n := range graph {
		if n.tag == "startEvent" && in[n] == 0 {
			roots = append(roots, n)
		}
	}
	for _, n := range graph {
		if n.tag != "startEvent" && in[n] == 0 {
			roots = append(roots, n)
		}
	}
	roots = append(roots, graph...)
	for _, n := range roots {
		if state[n] == 0 {
			visit(n)
		}
	}

	// longest path layering over the acyclic graph
	type dag struct {
		id       string
		s, t     *lnode
		reversed bool
	}
	var dags []dag
	indeg := map[*lnode]int{}
	succ := map[*lnode][]dag{}
	for _, e := range edges {
		d := dag{e.id, e.s, e.t, back[e.id]}
		if d.reversed {
			d.s, d.t = e.t, e.s
		}
		dags = append(dags, d)
		indeg[d.t]++
		succ[d.s] = append(succ[d.s], d)
	}
	queue := append([]*lnode(nil), graph...)
	sort.SliceStable(queue, func(i, j int) bool { return queue[i].dfs < queue[j].dfs })
	var ready []*lnode
	for _, n := range queue {
		if indeg[n] == 0 {
			ready = append(ready, n)
		}
	}
	for len(ready) > 0 {
		n := ready[0]
		ready = ready[1:]
		for _, d := range succ[n] {
			d.t.layer = max(d.t.layer, n.layer+1)
			if indeg[d.t]--; indeg[d.t] == 0 {
				ready = append(ready, d.t)
			}
		}
	}
	nLayers := 0
	for _, n := range graph {
		nLayers = max(nLayers, n.layer+1)
	}
	b.layers = make([][]*lnode, nLayers)
	for _, n := range graph {
		b.layers[n.layer] = append(b.layers[n.layer], n)
	}
	// dummies for lines over several columns
	for _, d := range dags {
		chain := &lchain{flow: d.id, reversed: d.reversed, nodes: []*lnode{d.s}}
		prev := d.s
		for layer := d.s.layer + 1; layer < d.t.layer; layer++ {
			dn := &lnode{id: d.id + "#" + strconv.Itoa(layer), dummy: true, layer: layer, dfs: d.s.dfs, seq: d.t.dfs, block: b}
			b.layers[layer] = append(b.layers[layer], dn)
			link(prev, dn)
			chain.nodes = append(chain.nodes, dn)
			prev = dn
		}
		link(prev, d.t)
		chain.nodes = append(chain.nodes, d.t)
		b.chains = append(b.chains, chain)
	}

	l.order(b)
	l.coordinates(b)

	// free subprocesses in a row below
	top := 0.0
	for _, layer := range b.layers {
		for _, n := range layer {
			top = math.Max(top, n.y+n.h/2+l.o.VGap)
		}
	}
	if len(graph) == 0 {
		top = 0
	}
	x := 0.0
	for _, n := range free {
		n.x, n.y = x+n.w/2, top+n.h/2
		x += n.w + l.o.HGap
	}

	// normalise to (0,0)
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, n := range b.nodes {
		minX, minY = math.Min(minX, n.x-n.w/2), math.Min(minY, n.y-n.h/2)
		maxX, maxY = math.Max(maxX, n.x+n.w/2), math.Max(maxY, n.y+n.h/2)
	}
	if len(b.nodes) == 0 {
		return b
	}
	for _, layer := range b.layers {
		for _, n := range layer {
			n.x, n.y = n.x-minX, n.y-minY
		}
	}
	for _, n := range free {
		n.x, n.y = n.x-minX, n.y-minY
	}
	for i := range b.colL {
		b.colL[i] -= minX
		b.colR[i] -= minX
	}
	b.w, b.h = maxX-minX, maxY-minY
	return b
}

func link(a, b *lnode) {
	a.succs = append(a.succs, b)
	b.preds = append(b.preds, a)
}

func nodeSize(n *lnode) (w, h float64) {
	switch {
	case strings.HasSuffix(n.tag, "Event"):
		return eventSize, eventSize
	case strings.HasSuffix(n.tag, "Gateway"):
		return gatewaySize, gatewaySize
	}
	if o := n.old; o != nil && o.w >= stepW && o.w <= 300 && o.h >= stepH && o.h <= 200 {
		return o.w, o.h // widened for a long name
	}
	return stepW, stepH
}

// order sorts the nodes of each layer: tidy by their current height in the
// diagram, full by flow order and then by the barycentre of the neighbours.
func (l *layouter) order(b *lblock) {
	if l.o.Mode == LayoutTidy {
		for _, layer := range b.layers {
			for _, n := range layer {
				if !n.dummy {
					n.key = l.tidyKey(n, map[*lnode]bool{})
				}
			}
		}
		for _, c := range b.chains {
			first, last := c.nodes[0], c.nodes[len(c.nodes)-1]
			for i, n := range c.nodes[1 : len(c.nodes)-1] {
				f := float64(i+1) / float64(len(c.nodes)-1)
				n.key = first.key + (last.key-first.key)*f
			}
		}
		for _, layer := range b.layers {
			sort.SliceStable(layer, func(i, j int) bool {
				if layer[i].key != layer[j].key {
					return layer[i].key < layer[j].key
				}
				return less(layer[i], layer[j])
			})
			renumber(layer)
		}
		return
	}
	for _, layer := range b.layers {
		sort.SliceStable(layer, func(i, j int) bool { return less(layer[i], layer[j]) })
		renumber(layer)
	}
	bary := func(ns []*lnode, self *lnode) float64 {
		if len(ns) == 0 {
			return float64(self.order)
		}
		s := 0.0
		for _, n := range ns {
			s += float64(n.order)
		}
		return s / float64(len(ns))
	}
	for it := 0; it < 4; it++ {
		for i := 1; i < len(b.layers); i++ {
			layer := b.layers[i]
			for _, n := range layer {
				n.key = bary(n.preds, n)
			}
			sort.SliceStable(layer, func(a, c int) bool { return layer[a].key < layer[c].key })
			renumber(layer)
		}
		for i := len(b.layers) - 2; i >= 0; i-- {
			layer := b.layers[i]
			for _, n := range layer {
				n.key = bary(n.succs, n)
			}
			sort.SliceStable(layer, func(a, c int) bool { return layer[a].key < layer[c].key })
			renumber(layer)
		}
	}
}

func less(a, b *lnode) bool {
	if a.dfs != b.dfs {
		return a.dfs < b.dfs
	}
	return a.seq < b.seq
}

func renumber(layer []*lnode) {
	for i, n := range layer {
		n.order = i
	}
}

// tidyKey is the current vertical position of a node; new nodes take the
// position of their predecessors (or successors).
func (l *layouter) tidyKey(n *lnode, seen map[*lnode]bool) float64 {
	if n.old != nil {
		return n.old.cy()
	}
	if seen[n] {
		return math.MaxFloat32
	}
	seen[n] = true
	for i, ns := range [][]*lnode{n.preds, n.succs} {
		for _, p := range ns {
			for p.dummy { // a dummy has one predecessor and one successor
				if i == 0 {
					p = p.preds[0]
				} else {
					p = p.succs[0]
				}
			}
			if k := l.tidyKey(p, seen); k != math.MaxFloat32 {
				return k + 0.5
			}
		}
	}
	return math.MaxFloat32
}

// coordinates assigns columns and heights: each node as close as possible
// to the median of its neighbours, keeping the order and the gaps.
func (l *layouter) coordinates(b *lblock) {
	b.colL = make([]float64, len(b.layers))
	b.colR = make([]float64, len(b.layers))
	x := 0.0
	for i, layer := range b.layers {
		w := 0.0
		for _, n := range layer {
			w = math.Max(w, n.w)
		}
		b.colL[i], b.colR[i] = x, x+w
		for _, n := range layer {
			n.x = x + w/2
		}
		x += w + l.o.HGap
	}
	sep := func(a, c *lnode) float64 {
		gap := l.o.VGap
		if a.dummy || c.dummy {
			gap = l.o.VGap / 2
		}
		return (a.h+c.h)/2 + gap
	}
	for _, layer := range b.layers {
		y := 0.0
		for i, n := range layer {
			if i > 0 {
				y += sep(layer[i-1], n)
			}
			n.y = y
		}
	}
	pass := func(layer []*lnode, neighbours func(*lnode) []*lnode) {
		if len(layer) == 0 {
			return
		}
		desired := make([]float64, len(layer))
		seps := make([]float64, len(layer))
		for i, n := range layer {
			desired[i] = n.y
			if ns := neighbours(n); len(ns) > 0 {
				ys := make([]float64, len(ns))
				for j, m := range ns {
					ys[j] = m.y
				}
				desired[i] = median(ys)
			}
			if i > 0 {
				seps[i-1] = sep(layer[i-1], n)
			}
		}
		for i, y := range isotonic(desired, seps) {
			layer[i].y = y
		}
	}
	preds := func(n *lnode) []*lnode { return n.preds }
	succs := func(n *lnode) []*lnode { return n.succs }
	for it := 0; it < 6; it++ {
		for i := 1; i < len(b.layers); i++ {
			pass(b.layers[i], preds)
		}
		for i := len(b.layers) - 2; i >= 0; i-- {
			pass(b.layers[i], succs)
		}
	}
	for i := 1; i < len(b.layers); i++ {
		pass(b.layers[i], preds)
	}
}

func median(v []float64) float64 {
	sort.Float64s(v)
	n := len(v)
	if n%2 == 1 {
		return v[n/2]
	}
	return (v[n/2-1] + v[n/2]) / 2
}

// isotonic returns the positions closest (least squares) to desired that
// keep the order and at least seps[i] between position i and i+1 (pool
// adjacent violators).
func isotonic(desired, seps []float64) []float64 {
	n := len(desired)
	off := make([]float64, n)
	for i := 1; i < n; i++ {
		off[i] = off[i-1] + seps[i-1]
	}
	type run struct {
		sum   float64
		count int
	}
	var runs []run
	for i := 0; i < n; i++ {
		runs = append(runs, run{desired[i] - off[i], 1})
		for len(runs) > 1 {
			a, c := runs[len(runs)-2], runs[len(runs)-1]
			if a.sum/float64(a.count) <= c.sum/float64(c.count) {
				break
			}
			runs = append(runs[:len(runs)-2], run{a.sum + c.sum, a.count + c.count})
		}
	}
	out := make([]float64, 0, n)
	for _, r := range runs {
		m := r.sum / float64(r.count)
		for j := 0; j < r.count; j++ {
			out = append(out, m+off[len(out)])
		}
	}
	return out
}

// place fixes the absolute positions of a block and its subprocesses.
func (l *layouter) place(b *lblock, ox, oy float64, pool rect) {
	b.ox, b.oy = ox, oy
	for _, n := range b.nodes {
		r := rect{ox + n.x - n.w/2, oy + n.y - n.h/2, n.w, n.h}.rounded()
		l.set(n.id, r)
		l.poolOf[n.id] = pool
		if n.sub != nil {
			l.place(n.sub, r.x+subPadX, r.y+subPadTop, pool)
		}
	}
}

func (l *layouter) set(id string, r rect) {
	if _, ok := l.pos[id]; !ok {
		l.placed = append(l.placed, id)
	}
	l.pos[id] = r
}

func (l *layouter) centre(n *lnode) point {
	if !n.dummy {
		return l.pos[n.id].center()
	}
	return point{math.Round(n.block.ox + n.x), math.Round(n.block.oy + n.y)}
}

// routeBlock draws the sequence flows of a block with right angles: the
// bend lies in the gap between two columns, so no line crosses a step.
func (l *layouter) routeBlock(b *lblock) {
	for _, n := range b.nodes {
		if n.sub != nil {
			l.routeBlock(n.sub)
		}
	}
	for _, c := range b.chains {
		pts := []point{l.centre(c.nodes[0])}
		for i := 1; i < len(c.nodes); i++ {
			a, t := c.nodes[i-1], c.nodes[i]
			pa, pt := l.centre(a), l.centre(t)
			switch {
			case math.Abs(pa.y-pt.y) < 0.5:
			case i == 1 && isSplit(a, c.reversed) && b.clearColumn(a.layer, pa.y, pt.y, a):
				pts = append(pts, point{pa.x, pt.y})
			case i == len(c.nodes)-1 && isJoin(t, c.reversed) && b.clearColumn(t.layer, pa.y, pt.y, t):
				pts = append(pts, point{pt.x, pa.y})
			default:
				m := math.Round(b.ox + (b.colR[a.layer]+b.colL[t.layer])/2)
				pts = append(pts, point{m, pa.y}, point{m, pt.y})
			}
			pts = append(pts, pt)
		}
		if c.reversed {
			for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
				pts[i], pts[j] = pts[j], pts[i]
			}
		}
		l.route(c.flow, simplify(pts))
	}
}

func isSplit(n *lnode, reversed bool) bool {
	return strings.HasSuffix(n.tag, "Gateway") && !reversed && len(n.succs) > 1
}

func isJoin(n *lnode, reversed bool) bool {
	return strings.HasSuffix(n.tag, "Gateway") && !reversed && len(n.preds) > 1
}

// clearColumn reports whether a vertical line in a column between y1 and
// y2 passes no other node.
func (b *lblock) clearColumn(layer int, y1, y2 float64, self *lnode) bool {
	lo, hi := math.Min(y1, y2), math.Max(y1, y2)
	for _, n := range b.layers[layer] {
		if n == self {
			continue
		}
		top, bottom := b.oy+n.y-n.h/2, b.oy+n.y+n.h/2
		if n.dummy {
			top, bottom = b.oy+n.y-2, b.oy+n.y+2
		}
		if bottom > lo && top < hi {
			return false
		}
	}
	return true
}

func (l *layouter) route(id string, pts []point) {
	if _, ok := l.routes[id]; !ok {
		l.routed = append(l.routed, id)
	}
	l.routes[id] = pts
}

// placeEndpoints puts senders (or receivers) in a column at x, each level
// with the step it is connected to.
func (l *layouter) placeEndpoints(eps, flows []*etree.Element, other string, x, defaultY float64) {
	if len(eps) == 0 {
		return
	}
	type ep struct {
		id      string
		desired float64
		seq     int
	}
	var list []ep
	for i, p := range eps {
		id := p.SelectAttrValue("id", "")
		e := ep{id, defaultY, i}
		self := "sourceRef"
		if other == "sourceRef" {
			self = "targetRef"
		}
		for _, f := range flows {
			if f.SelectAttrValue(self, "") != id {
				continue
			}
			if r, ok := l.pos[f.SelectAttrValue(other, "")]; ok {
				e.desired = r.cy()
				break
			}
		}
		list = append(list, e)
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].desired < list[j].desired })
	desired := make([]float64, len(list))
	seps := make([]float64, len(list))
	for i, e := range list {
		desired[i] = e.desired
		seps[i] = endpointH + 20
	}
	ys := isotonic(desired, seps)
	for i, e := range list {
		l.set(e.id, rect{x, ys[i] - endpointH/2, endpointW, endpointH}.rounded())
	}
}

// routeMessageFlow connects a sender or receiver with its step: a straight
// line when nothing is in the way, else around through the gap next to the
// pool and the free band at the top (or bottom) of the pool.
func (l *layouter) routeMessageFlow(f *etree.Element) {
	id := f.SelectAttrValue("id", "")
	s, t := f.SelectAttrValue("sourceRef", ""), f.SelectAttrValue("targetRef", "")
	rs, okS := l.pos[s]
	rt, okT := l.pos[t]
	if !okS || !okT {
		return
	}
	_, sInner := l.node[s]
	_, tInner := l.node[t]
	switch {
	case !sInner && tInner:
		l.route(id, l.endpointRoute(rs, t))
	case sInner && !tInner:
		pts := l.endpointRoute(rt, s)
		for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
			pts[i], pts[j] = pts[j], pts[i]
		}
		l.route(id, pts)
	default:
		l.route(id, []point{rs.center(), rt.center()})
	}
}

func (l *layouter) endpointRoute(ep rect, el string) []point {
	r := l.pos[el]
	pool := l.poolOf[el]
	c := r.center()
	// above or below the pool: down (up) to the step, the bend in the gap
	if above, below := ep.bottom() <= pool.y, ep.y >= pool.bottom(); above || below {
		if math.Abs(ep.cx()-c.x) < 0.5 {
			return []point{ep.center(), c}
		}
		midY := math.Round((ep.bottom() + pool.y) / 2)
		if below {
			midY = math.Round((pool.bottom() + ep.y) / 2)
		}
		return []point{ep.center(), {ep.cx(), midY}, {c.x, midY}, c}
	}
	left := ep.cx() < pool.x
	gapX := math.Round((ep.right() + pool.x) / 2)
	edgeX := pool.x
	if !left {
		gapX = math.Round((pool.right() + ep.x) / 2)
		edgeX = pool.right()
	}
	if l.clearH(c.y, edgeX, c.x, el) {
		if c.y > ep.y+10 && c.y < ep.bottom()-10 {
			return []point{{ep.cx(), c.y}, c}
		}
		return simplify([]point{ep.center(), {gapX, ep.cy()}, {gapX, c.y}, c})
	}
	key := fmt.Sprint(pool)
	k := l.channel[key]
	l.channel[key]++
	for _, chY := range []float64{pool.y + poolPadT/2 - float64(k*channelStep), pool.bottom() - poolPadB/2 + float64(k*channelStep)} {
		chY = math.Round(chY)
		if l.clearV(c.x, c.y, chY, el) && l.clearH(chY, edgeX, c.x, el) {
			return simplify([]point{ep.center(), {gapX, ep.cy()}, {gapX, chY}, {c.x, chY}, c})
		}
	}
	return []point{ep.center(), c}
}

// clearH reports whether a horizontal line at y between x1 and x2 crosses
// no shape other than el and its subprocesses.
func (l *layouter) clearH(y, x1, x2 float64, el string) bool {
	lo, hi := math.Min(x1, x2), math.Max(x1, x2)
	return l.clear(el, func(r rect) bool { return y > r.y && y < r.bottom() && r.right() > lo && r.x < hi })
}

func (l *layouter) clearV(x, y1, y2 float64, el string) bool {
	lo, hi := math.Min(y1, y2), math.Max(y1, y2)
	return l.clear(el, func(r rect) bool { return x > r.x && x < r.right() && r.bottom() > lo && r.y < hi })
}

func (l *layouter) clear(el string, hits func(rect) bool) bool {
	skip := map[string]bool{el: true}
	for p := l.parent[el]; p != ""; p = l.parent[p] {
		skip[p] = true
	}
	for id := range l.node {
		if !skip[id] && hits(l.pos[id]) {
			return false
		}
	}
	return true
}

// simplify drops repeated points and points in the middle of a straight
// segment.
func simplify(pts []point) []point {
	var out []point
	for _, p := range pts {
		p = point{math.Round(p.x), math.Round(p.y)}
		if n := len(out); n > 0 && out[n-1] == p {
			continue
		}
		if n := len(out); n >= 2 {
			a, b := out[n-2], out[n-1]
			if (a.x == b.x && b.x == p.x) || (a.y == b.y && b.y == p.y) {
				out[n-1] = p
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

// write applies the positions and lines to the diagram.
func (l *layouter) write(doc *etree.Document, plane *etree.Element) *LayoutResult {
	res := &LayoutResult{}
	prefix := prefixes(l)
	var added []*etree.Element
	for _, id := range l.placed {
		r := l.pos[id]
		shape := l.shapes[id]
		if shape == nil {
			shape = etree.NewElement("BPMNShape")
			shape.Space = plane.Space
			shape.CreateAttr("bpmnElement", id)
			shape.CreateAttr("id", "BPMNShape_"+id)
			b := etree.NewElement("Bounds")
			b.Space = prefix.dc
			shape.AddChild(b)
			l.shapes[id] = shape
			added = append(added, shape)
			res.Added++
		} else if o, ok := l.old[id]; !ok || o.rounded() != r {
			res.Moved++
		}
		b := child(shape, "Bounds")
		if b == nil {
			b = shape.CreateElement(prefix.dc + ":Bounds")
		}
		b.CreateAttr("height", fmtNum(r.h))
		b.CreateAttr("width", fmtNum(r.w))
		b.CreateAttr("x", fmtNum(r.x))
		b.CreateAttr("y", fmtNum(r.y))
	}
	for _, id := range l.routed {
		pts := l.routes[id]
		edge := l.edges[id]
		if edge == nil {
			el := l.byID[id]
			edge = etree.NewElement("BPMNEdge")
			edge.Space = plane.Space
			edge.CreateAttr("bpmnElement", id)
			edge.CreateAttr("id", "BPMNEdge_"+id)
			for _, a := range [][2]string{{"sourceElement", "sourceRef"}, {"targetElement", "targetRef"}} {
				if shape := l.shapes[el.SelectAttrValue(a[1], "")]; shape != nil {
					edge.CreateAttr(a[0], shape.SelectAttrValue("id", ""))
				}
			}
			l.edges[id] = edge
			added = append(added, edge)
			res.Added++
		} else if samePoints(waypoints(edge), pts) {
			continue
		} else {
			res.Rerouted++
		}
		var kids []*etree.Element
		for _, c := range edge.ChildElements() {
			if c.Tag != "waypoint" && c.Tag != "BPMNLabel" {
				kids = append(kids, c)
			}
		}
		for _, p := range pts {
			w := etree.NewElement("waypoint")
			w.Space = prefix.di
			w.CreateAttr("x", fmtNum(p.x))
			w.CreateAttr(prefix.xsi+":type", prefix.dc+":Point")
			w.CreateAttr("y", fmtNum(p.y))
			kids = append(kids, w)
		}
		if edge.Parent() == nil {
			for _, k := range kids {
				edge.AddChild(k)
			}
		} else {
			setChildren(edge, kids)
		}
	}
	// the prefixes used must be declared (SAP's files declare them all)
	root := doc.Root()
	for prefix, ns := range map[string]string{prefix.dc: "http://www.omg.org/spec/DD/20100524/DC", prefix.di: "http://www.omg.org/spec/DD/20100524/DI",
		prefix.xsi: "http://www.w3.org/2001/XMLSchema-instance"} {
		if res.Moved+res.Rerouted+res.Added > 0 && root.SelectAttr("xmlns:"+prefix) == nil {
			root.CreateAttr("xmlns:"+prefix, ns)
		}
	}
	if len(added) > 0 {
		setChildren(plane, append(plane.ChildElements(), added...))
		for _, el := range added {
			setChildren(el, el.ChildElements())
		}
	}
	return res
}

type nsPrefixes struct{ dc, di, xsi string }

// prefixes finds the namespace prefixes the model uses for bounds and
// waypoints.
func prefixes(l *layouter) nsPrefixes {
	p := nsPrefixes{dc: "dc", di: "di", xsi: "xsi"}
	for _, s := range l.shapes {
		if b := child(s, "Bounds"); b != nil && b.Space != "" {
			p.dc = b.Space
			break
		}
	}
	for _, e := range l.edges {
		for _, w := range e.ChildElements() {
			if w.Tag == "waypoint" && w.Space != "" {
				p.di = w.Space
				for _, a := range w.Attr {
					if a.Key == "type" && a.Space != "" {
						p.xsi = a.Space
					}
				}
			}
		}
	}
	return p
}

// setChildren replaces the children of el, indented like the rest of the
// file (no indentation when the file has none).
func setChildren(el *etree.Element, kids []*etree.Element) {
	indent, unit, ok := indentation(el)
	el.Child = nil
	for _, k := range kids {
		if ok {
			el.CreateText("\n" + indent + unit)
		}
		el.AddChild(k)
	}
	if ok && len(kids) > 0 {
		el.CreateText("\n" + indent)
	}
}

// indentation returns the indentation of el and one indentation step.
func indentation(el *etree.Element) (indent, unit string, ok bool) {
	unit = "    "
	if p := el.Parent(); p != nil {
		idx := el.Index()
		if idx > 0 {
			if cd, isCD := p.Child[idx-1].(*etree.CharData); isCD {
				if i := strings.LastIndex(cd.Data, "\n"); i >= 0 {
					indent, ok = cd.Data[i+1:], true
				}
			}
		}
	}
	for _, t := range el.Child {
		if cd, isCD := t.(*etree.CharData); isCD {
			if i := strings.LastIndex(cd.Data, "\n"); i >= 0 && len(cd.Data[i+1:]) > len(indent) {
				unit = cd.Data[i+1+len(indent):]
				ok = true
				break
			}
		}
	}
	return indent, unit, ok
}

func waypoints(edge *etree.Element) []point {
	var pts []point
	for _, w := range edge.ChildElements() {
		if w.Tag == "waypoint" {
			pts = append(pts, point{num(w, "x"), num(w, "y")})
		}
	}
	return pts
}

func samePoints(a, b []point) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Round(a[i].x) != b[i].x || math.Round(a[i].y) != b[i].y {
			return false
		}
	}
	return true
}

func fmtNum(v float64) string { return strconv.FormatFloat(math.Round(v), 'f', 1, 64) }

func num(el *etree.Element, attr string) float64 {
	v, _ := strconv.ParseFloat(el.SelectAttrValue(attr, "0"), 64)
	return v
}

func walk(el *etree.Element, fn func(*etree.Element)) {
	fn(el)
	for _, c := range el.ChildElements() {
		walk(c, fn)
	}
}

func child(el *etree.Element, tag string) *etree.Element {
	for _, c := range el.ChildElements() {
		if c.Tag == tag {
			return c
		}
	}
	return nil
}

func findFirst(el *etree.Element, tag string) *etree.Element {
	var found *etree.Element
	walk(el, func(e *etree.Element) {
		if found == nil && e.Tag == tag {
			found = e
		}
	})
	return found
}

func inDiagram(el *etree.Element) bool {
	for p := el; p != nil; p = p.Parent() {
		if p.Tag == "BPMNDiagram" {
			return true
		}
	}
	return false
}
