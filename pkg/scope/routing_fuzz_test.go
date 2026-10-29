package scope

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// The runtime routing test (cpicli#58) checks the guard's endpoint decision
// against routeModel, a deliberately simple and generous model of how the
// runtime might route a URL. The model knows several readings of a URL or an
// entry point where the runtime's behaviour is not pinned (cpicli#59): a
// ";…" variant of an entry point's parameters may count as the same, and an
// entry point may be decoded as a whole before it is split. Whatever the
// guard allows must be the allowed flow's under every reading: no other flow
// may own it as specifically as the allowed flow's best match. The guard may
// refuse more than the model requires (fail-closed).

const (
	fzAllowed = "SD_A" // the flow the scope grants test on
	fzOther   = "FI_B" // another team's flow
)

type fzEntry struct{ url, flow string }

type fzCase struct {
	entries    []fzEntry
	base, path string
}

func (c fzCase) String() string {
	var b strings.Builder
	for _, e := range c.entries {
		fmt.Fprintf(&b, "  %s %s\n", e.flow, e.url)
	}
	return fmt.Sprintf("POST %s%s\nentry points:\n%s", c.base, c.path, b.String())
}

// checkRouting fails t when the guard allows c's request and the model finds
// a reading under which another flow owns the URL.
func checkRouting(t *testing.T, c fzCase) {
	t.Helper()
	g := testGuard(t, `{"version":1,"packages":["SD_*"],"actions":["read","test"]}`)
	var flows []map[string]any
	for _, e := range c.entries {
		flows = append(flows, map[string]any{"Name": e.flow, "EntryPoints": map[string]any{"results": []map[string]string{{"Url": e.url}}}})
	}
	body, _ := json.Marshal(map[string]any{"d": map[string]any{"results": flows}})
	endpoints(g, string(body))
	if g.Before(&httpclnt.GuardRequest{Ctx: context.Background(), Method: "POST", Base: c.base, Path: c.path}) != nil {
		return
	}
	if why := routeModel(c); why != "" {
		t.Fatalf("the guard allows a URL the runtime may route elsewhere: %s\n%s", why, c)
	}
}

// --- the model -------------------------------------------------------------

type mSeg struct {
	name, params string
	encoded      bool // parameters percent-encoded as written
}

// mPath is one reading of an entry point.
type mPath struct {
	segs []mSeg
	wild bool // the last segment's name is a prefix
	// strict: the reading the guard must use (per-segment decoding of a
	// well-formed entry point); a strict match is certain, any other only
	// possible
	strict bool
}

// routeModel returns "" when every reading of c's request reaches the
// allowed flow (or nothing), else what may reach another flow.
func routeModel(c fzCase) string {
	if !strings.EqualFold(strings.TrimSuffix(strings.TrimSuffix(c.base, ":443"), "/"), "https://rt") {
		return "the request is for another host"
	}
	req, ok := modelRequest(c.path)
	if !ok {
		return "the request path cannot be read"
	}
	// the best certain match decides on the runtime; any other flow that
	// may match as specifically could be chosen instead
	best, bestFlows := -1, map[string]bool{}
	type possible struct {
		flow string
		spec int
	}
	var maybe []possible
	for _, e := range c.entries {
		for _, r := range modelEntry(e.url) {
			spec, certain, ok := r.matches(req)
			if !ok {
				continue
			}
			if certain {
				switch {
				case spec > best:
					best, bestFlows = spec, map[string]bool{e.flow: true}
				case spec == best:
					bestFlows[e.flow] = true
				}
			}
			maybe = append(maybe, possible{e.flow, spec})
		}
	}
	switch {
	case best < 0:
		return "no entry point certainly covers it"
	case len(bestFlows) > 1 || !bestFlows[fzAllowed]:
		return fmt.Sprintf("the most specific certain match is %v's", keys(bestFlows))
	}
	for _, m := range maybe {
		if m.flow != fzAllowed && m.spec >= best {
			return fmt.Sprintf("%s may own it as specifically (%d >= %d)", m.flow, m.spec, best)
		}
	}
	return ""
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// modelRequest reads a request path as the runtime routes it: segments
// decoded one by one, parameters after the first ";".
func modelRequest(p string) ([]mSeg, bool) {
	p, _, _ = strings.Cut(p, "?")
	if !strings.HasPrefix(p, "/") {
		return nil, false
	}
	var out []mSeg
	for _, raw := range strings.Split(p[1:], "/") {
		name, params, _ := strings.Cut(raw, ";")
		n, err1 := url.PathUnescape(name)
		ps, err2 := url.PathUnescape(params)
		if err1 != nil || err2 != nil {
			return nil, false
		}
		out = append(out, mSeg{n, ps, strings.Contains(params, "%")})
	}
	return out, true
}

// modelEntry returns the readings of an entry point URL: decoded per segment
// (strict, when the URL is well-formed), and decoded as a whole, with dot
// segments resolved and the query dropped (possible).
func modelEntry(u string) []mPath {
	rest := strings.TrimPrefix(u, "https://rt")
	var out []mPath
	if strict, ok := strictEntry(rest); ok {
		out = append(out, strict)
	}
	whole := rest
	whole, _, _ = strings.Cut(whole, "?")
	if dec, err := url.PathUnescape(whole); err == nil {
		wild := strings.HasSuffix(dec, "*")
		dec = path.Clean("/" + strings.TrimSuffix(dec, "*"))
		if wild && !strings.HasSuffix(dec, "/") && strings.HasSuffix(strings.TrimSuffix(whole, "*"), "/") {
			dec += "/"
		}
		// which segment's parameters were encoded: per segment when the
		// decoding kept the segments as written, else all of them
		decSegs := strings.Split(strings.TrimPrefix(dec, "/"), "/")
		rawPath := strings.TrimSuffix(whole, "*")
		if !wild {
			rawPath = strings.TrimSuffix(rawPath, "/")
		}
		rawSegs := strings.Split(strings.TrimPrefix(rawPath, "/"), "/")
		sameShape := len(rawSegs) == len(decSegs)
		for i := 0; sameShape && i < len(rawSegs); i++ {
			d, err := url.PathUnescape(rawSegs[i])
			sameShape = err == nil && d == decSegs[i]
		}
		var segs []mSeg
		for i, raw := range decSegs {
			name, params, _ := strings.Cut(raw, ";")
			enc := strings.Contains(whole, "%")
			if sameShape {
				_, rawParams, _ := strings.Cut(rawSegs[i], ";")
				enc = strings.Contains(rawParams, "%")
			}
			segs = append(segs, mSeg{name, params, enc})
		}
		out = append(out, mPath{segs: segs, wild: wild})
	}
	return out
}

func strictEntry(rest string) (mPath, bool) {
	if strings.ContainsAny(rest, "?#") || !strings.HasPrefix(rest, "/") {
		return mPath{}, false
	}
	rest = strings.TrimSuffix(rest, "/")
	p := mPath{strict: true}
	if r, ok := strings.CutSuffix(rest, "*"); ok {
		p.wild, rest = true, r
	}
	raws := strings.Split(strings.TrimPrefix(rest, "/"), "/")
	for i, raw := range raws {
		name, params, _ := strings.Cut(raw, ";")
		n, err1 := url.PathUnescape(name)
		ps, err2 := url.PathUnescape(params)
		if err1 != nil || err2 != nil || name == "." || name == ".." || (raw == "" && !(p.wild && i == len(raws)-1)) ||
			strings.ContainsAny(n, "/\\;*") || strings.ContainsAny(ps, "/\\*") || strings.Count(ps, ";") != strings.Count(params, ";") {
			return mPath{}, false
		}
		p.segs = append(p.segs, mSeg{n, ps, strings.Contains(params, "%")})
	}
	if p.wild && p.segs[len(p.segs)-1].params != "" {
		return mPath{}, false
	}
	return p, true
}

// matches reports whether the reading covers the request, how specifically
// (the exact entry point highest, then by the length it fixes), and whether
// the match is certain: under the strict reading, with the entry point's
// parameters exactly the request's. A parameter variant (other case, leading
// zeros, other or more parameters) is only a possible match.
func (p mPath) matches(req []mSeg) (spec int, certain, ok bool) {
	if len(req) < len(p.segs) {
		return 0, false, false
	}
	certain = p.strict
	for i, s := range p.segs {
		r := req[i]
		if p.wild && i == len(p.segs)-1 {
			if !strings.HasPrefix(r.name, s.name) {
				return 0, false, false
			}
		} else {
			if r.name != s.name {
				return 0, false, false
			}
			switch {
			case s.params == "":
			case s.encoded || r.encoded:
				certain = false // encoded parameters may read either way
			case s.params != r.params:
				if !paramsMaySatisfy(s.params, r.params) {
					return 0, false, false
				}
				certain = false
			}
		}
		spec += 1 + len(s.name)
		if s.params != "" {
			spec += 1 + len(s.params)
		}
	}
	switch {
	case p.wild:
	case len(req) == len(p.segs):
		spec = 1 << 20
	default:
		spec++
	}
	return spec, certain, true
}

// paramsMaySatisfy: the request's parameters may be read as the entry
// point's unless they provably differ: the same key, spelled exactly, with
// distinct canonical integer values, and no other spelling of that key.
func paramsMaySatisfy(entry, req string) bool {
	canon := regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
	for _, ep := range strings.Split(entry, ";") {
		k, ev, ok := strings.Cut(ep, "=")
		if !ok || k == "" || !canon.MatchString(ev) {
			continue
		}
		var vals []string
		other := false
		for _, rp := range strings.Split(req, ";") {
			rk, rv, _ := strings.Cut(rp, "=")
			if rk == k {
				vals = append(vals, rv)
			} else if strings.EqualFold(strings.TrimSpace(rk), k) {
				other = true
			}
		}
		if len(vals) == 0 || other {
			continue
		}
		differ := true
		for _, v := range vals {
			if !canon.MatchString(v) || v == ev {
				differ = false
			}
		}
		if differ {
			return false
		}
	}
	return true
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// --- generating cases -------------------------------------------------------

// choices reads small decisions from fuzz input (zero when it runs out).
type choices struct {
	b []byte
	i int
}

func (c *choices) n(k int) int {
	if c.i >= len(c.b) {
		return 0
	}
	v := int(c.b[c.i]) % k
	c.i++
	return v
}

var (
	fzNames  = []string{"http", "cxf", "gw", "odata", "api", "shared", "billing", "bill", "a", "b", "SRV", "srv", "x"}
	fzParams = []string{"v=1", "v=2", "V=2", "v=02", "x=1", "v=2;x=1", "x=1;v=2", "jsessionid=1", "", "v= 2",
		"v=+2", "v=2.0", "v='2'", "v=２", "v=", "v=1;v=2", " v=1", "v=3", "v%3D1", "v=%32", "%76=2"}
)

// encode writes a name with some characters percent-encoded (either hex
// case).
func encode(c *choices, s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c.n(6) {
		case 0:
			fmt.Fprintf(&b, "%%%02X", s[i])
		case 1:
			fmt.Fprintf(&b, "%%%02x", s[i])
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func genSegs(c *choices, max int, request bool) []string {
	var segs []string
	for range 1 + c.n(max) {
		s := fzNames[c.n(len(fzNames))]
		if request {
			s = encode(c, s)
		}
		switch c.n(5) {
		case 0:
			s += ";" + fzParams[c.n(len(fzParams))]
		case 1:
			if !request {
				s += ";" + fzParams[c.n(2)] // entry points carry ;v=1 or ;v=2
			}
		}
		segs = append(segs, s)
	}
	return segs
}

func genCase(c *choices) fzCase {
	var fc fzCase
	for range 1 + c.n(4) {
		segs := genSegs(c, 3, false)
		u := "https://rt/" + strings.Join(segs, "/")
		switch c.n(10) {
		case 0:
			u += "/*"
		case 1:
			u += "*"
		case 2:
			u += "/"
		case 3: // odd entry points
			u += []string{"/a%2Fb", "/./x", "//x", "?q=1", "/b%3Bv=2", "/%zz"}[c.n(6)]
		}
		flow := fzAllowed
		if c.n(2) == 1 {
			flow = fzOther
		}
		fc.entries = append(fc.entries, fzEntry{u, flow})
	}
	// a request near one of the entry points, often below it
	segs := genSegs(c, 5, true)
	if e := fc.entries[c.n(len(fc.entries))]; c.n(3) > 0 {
		base := strings.Split(strings.TrimSuffix(strings.TrimPrefix(e.url, "https://rt/"), "*"), "/")
		segs = append(base[:min(len(base), 1+c.n(len(base)))], segs[:c.n(len(segs)+1)]...)
		for i := range segs {
			if c.n(4) == 0 {
				segs[i] = encode(c, segs[i])
			}
		}
	}
	fc.path = "/" + strings.Join(segs, "/")
	if c.n(6) == 0 {
		fc.path += "/"
	}
	fc.base = []string{"https://rt:443", "https://RT:443", "https://rt"}[c.n(3)]
	return fc
}

// The cases that took patch releases (cpicli#53–#57) stay in the corpus.
func TestRoutingRegressions(t *testing.T) {
	sd := func(u string) fzEntry { return fzEntry{"https://rt" + u, fzAllowed} }
	fi := func(u string) fzEntry { return fzEntry{"https://rt" + u, fzOther} }
	for _, c := range []fzCase{
		{[]fzEntry{sd("/http/shared"), fi("/http/shared/billing")}, "https://rt:443", "/http/shared/billing"},
		{[]fzEntry{sd("/http/shared/*"), fi("/http/shared/billing")}, "https://rt:443", "/http/shared/bill%69ng"},
		{[]fzEntry{sd("/http/shared/*"), fi("/http/shared/billing")}, "https://rt:443", "/http/shared/billing;x"},
		{[]fzEntry{sd("/cxf/*"), fi("/cxf/billing")}, "https://rt:443", "/cxf/billing;jsessionid=1"},
		{[]fzEntry{sd("/gw/odata/SAP/SRV;v=1"), fi("/gw/odata/SAP/SRV;v=2")}, "https://rt:443", "/gw/odata/SAP/SRV;v=2/Invoices"},
		{[]fzEntry{fi("/gw/odata/SAP/SRV;v=2"), sd("/gw/odata/SAP/SRV;v=1")}, "https://rt:443", "/gw/odata/SAP/SRV;v=2/Invoices"},
		{[]fzEntry{sd("/gw/odata/SAP"), fi("/gw/odata/SAP/SRV;v=2")}, "https://rt:443", "/gw/odata/SAP/SRV;V=2/Invoices"},
		{[]fzEntry{sd("/gw/odata/SAP"), fi("/gw/odata/SAP/SRV;v=2")}, "https://rt:443", "/gw/odata/SAP/SRV;x=1;v=2/Invoices"},
		{[]fzEntry{sd("/http/shared/*"), fi("/http/shared/a%2Fb")}, "https://rt:443", "/http/shared/a/b"},
		{[]fzEntry{sd("/http/*"), fi("/http/odd/./x")}, "https://rt:443", "/http/odd/x"},
		// found by FuzzRouting (#58): two entry points the runtime may read as
		// one service; a wildcard after parameters
		{[]fzEntry{sd("/b;v= 2"), fi("/b;v=02")}, "https://rt:443", "/b;v=%202/"},
		{[]fzEntry{sd("/gw;v=02/odata"), fi("/gw;jsessionid=1*")}, "https://RT:443", "/gw;v=02/odata/%73rv"},
		// found by FuzzRouting on the #65 fix: an unreadable, encoded entry point
		{[]fzEntry{sd("/b"), sd("/b"), fi("/b/b%3Bv=2")}, "https://rt:443", "/%62/%62;v=1/%68%74%74%70;v=1/%68%74%74%70;v=1/"},
		{[]fzEntry{sd("/b"), sd("/b;v=3/b/b"), fi("/b;x=1;v=2/b;v%3D1/b"), sd("/b")}, "https://rt:443", "/b;v=3/%62/%62/"},
		{[]fzEntry{sd("/b"), fi("/b/b;v%3D1/b;x=1;v=2/*")}, "https://rt:443", "/%62/%62/%62;v=1/%68%74%74%70;v=1/"},
	} {
		checkRouting(t, c)
	}
	// and the model itself flags each of them when the guard is not asked
	if routeModel(fzCase{[]fzEntry{sd("/http/shared/*"), fi("/http/shared/billing")}, "https://rt:443", "/http/shared/bill%69ng"}) == "" {
		t.Fatal("the model misses the escaped-path case")
	}
	if routeModel(fzCase{[]fzEntry{sd("/gw/odata/SAP"), fi("/gw/odata/SAP/SRV;v=2")}, "https://rt:443", "/gw/odata/SAP/SRV;V=2/x"}) == "" {
		t.Fatal("the model misses the parameter-variant case")
	}
}

// A fixed sweep of generated cases runs with every go test.
func TestRoutingSweep(t *testing.T) {
	r := rand.New(rand.NewPCG(58, 58))
	for range 3000 {
		b := make([]byte, 64)
		for i := range b {
			b[i] = byte(r.UintN(256))
		}
		checkRouting(t, genCase(&choices{b: b}))
	}
}

// go test ./pkg/scope -run '^$' -fuzz FuzzRouting
func FuzzRouting(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("seed-escaped-%69"))
	f.Add([]byte{3, 1, 2, 0, 9, 1, 1, 4, 2, 0, 0, 1, 7, 3, 0, 2})
	f.Fuzz(func(t *testing.T, data []byte) {
		checkRouting(t, genCase(&choices{b: data}))
	})
}
