// Package conformance probes how a tenant's runtime routes URLs and how its
// API reports changes: the assumptions the access scope (pkg/scope) and the
// mock tenant rest on (cpicli#59). The same probes run against the mock
// tenant in every go test, and against a development tenant on request
// (go test -tags e2e -run TestConformance ./internal/cmd, docs/e2e.md); the
// tenant's answers, recorded in testdata/tenant.json, are what the mock must
// answer too.
package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// Target is a tenant to probe: its API, a way to send to its runtime, and two
// deployed flows.
type Target struct {
	API *httpclnt.HTTPExecuter
	// Send sends a request to a runtime path (with query) and returns the
	// HTTP status.
	Send func(ctx context.Context, method, path string) (int, error)
	// HTTPFlow has an HTTPS sender; ODataFlow an OData V2 sender and an
	// externalised parameter ConfigKey ("" skips their probes).
	HTTPFlow, ODataFlow, ConfigKey string
}

// Fact is one observed behaviour.
type Fact struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Got  bool   `json:"got"`
	// Pinned: the access scope relies on Want; a tenant answering
	// otherwise needs a guard change.
	Pinned bool   `json:"pinned,omitempty"`
	Want   bool   `json:"want,omitempty"`
	Note   string `json:"note,omitempty"`
}

// Holds reports whether a pinned fact has the value the guard relies on.
func (f Fact) Holds() bool { return !f.Pinned || f.Got == f.Want }

// Run probes t and returns the facts it could establish.
func Run(ctx context.Context, t Target) ([]Fact, error) {
	var facts []Fact
	if t.HTTPFlow != "" {
		p, err := EntryPath(t.API, t.HTTPFlow, "/http/")
		if err != nil {
			return nil, err
		}
		fs, err := routingFacts(ctx, t, p)
		if err != nil {
			return nil, err
		}
		facts = append(facts, fs...)
	}
	if t.ODataFlow != "" {
		p, err := EntryPath(t.API, t.ODataFlow, "/gw/odata/")
		if err != nil {
			return nil, err
		}
		fs, err := odataFacts(ctx, t, p)
		if err != nil {
			return nil, err
		}
		facts = append(facts, fs...)
		if t.ConfigKey != "" {
			f, err := modifiedAtFact(t, t.ODataFlow, t.ConfigKey)
			if err != nil {
				return nil, err
			}
			facts = append(facts, f)
		}
	}
	return facts, nil
}

// EntryPath returns the path of the flow's entry point that starts with
// prefix, from the tenant's ServiceEndpoints.
func EntryPath(api *httpclnt.HTTPExecuter, flow, prefix string) (string, error) {
	urls, err := EntryPoints(api)
	if err != nil {
		return "", err
	}
	for _, u := range urls[flow] {
		if pu, err := url.Parse(u); err == nil && strings.HasPrefix(pu.EscapedPath(), prefix) {
			return strings.TrimSuffix(pu.EscapedPath(), "/*"), nil
		}
	}
	return "", fmt.Errorf("flow %s lists no entry point under %s (deployed and started?)", flow, prefix)
}

// EntryPoints returns the entry point URLs of every flow, as ServiceEndpoints
// lists them.
func EntryPoints(api *httpclnt.HTTPExecuter) (map[string][]string, error) {
	b, err := get(api, "/api/v1/ServiceEndpoints?$expand=EntryPoints")
	if err != nil {
		return nil, err
	}
	var v struct {
		D struct {
			Results []struct {
				Name        string
				EntryPoints struct {
					Results []struct {
						URL string `json:"Url"`
					} `json:"results"`
				}
			} `json:"results"`
		} `json:"d"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, e := range v.D.Results {
		for _, ep := range e.EntryPoints.Results {
			out[e.Name] = append(out[e.Name], ep.URL)
		}
	}
	return out, nil
}

// reached: the request got past routing (anything but 404); 401 and 403 are
// an error of the probe's credentials, not an answer.
func reached(ctx context.Context, t Target, method, path string) (bool, error) {
	st, err := t.Send(ctx, method, path)
	if err != nil {
		return false, err
	}
	if st == http.StatusUnauthorized || st == http.StatusForbidden {
		return false, fmt.Errorf("%s %s: HTTP %d (check the runtime credentials, role ESBMessaging.send)", method, path, st)
	}
	return st != http.StatusNotFound, nil
}

func routingFacts(ctx context.Context, t Target, p string) ([]Fact, error) {
	ok, err := reached(ctx, t, http.MethodPost, p)
	if err != nil || !ok {
		return nil, cmpErr(err, fmt.Errorf("POST %s did not reach the flow", p))
	}
	dir, last := p[:strings.LastIndex(p, "/")+1], p[strings.LastIndex(p, "/")+1:]
	swapped := swapCase(last)
	enc := last[:len(last)-1] + fmt.Sprintf("%%%02X", last[len(last)-1])
	probes := []struct {
		id, text, path string
		pinned, want   bool
		note           string
	}{
		{"case", "a path differing only in case reaches the flow", dir + swapped, true, false,
			"the guard compares paths case-sensitively; a runtime that does not would let a case variant of another team's address through"},
		{"encoded", "a percent-encoded path segment reaches the flow (the runtime decodes before routing)", dir + enc, false, false,
			"the guard decodes either way"},
		{"param", "a ;parameter on the last path segment reaches the flow (the runtime drops it)", p + ";x=1", false, false,
			"the guard ignores parameters an entry point does not have either way"},
	}
	var facts []Fact
	for _, pr := range probes {
		got, err := reached(ctx, t, http.MethodPost, pr.path)
		if err != nil {
			return nil, err
		}
		facts = append(facts, Fact{ID: pr.id, Text: pr.text, Got: got, Pinned: pr.pinned, Want: pr.want, Note: pr.note})
	}
	return facts, nil
}

func odataFacts(ctx context.Context, t Target, root string) ([]Fact, error) {
	i := strings.LastIndex(root, ";")
	if i < 0 {
		return nil, fmt.Errorf("OData service root %s has no ;v=", root)
	}
	ok, err := reached(ctx, t, http.MethodGet, root+"/$metadata")
	if err != nil || !ok {
		return nil, cmpErr(err, fmt.Errorf("GET %s/$metadata did not reach the service", root))
	}
	base, version := root[:i], strings.TrimPrefix(root[i+1:], "v=")
	var facts []Fact
	for _, v := range []struct{ id, params string }{
		{"odata-case", "V=" + version}, {"odata-zero", "v=0" + version}, {"odata-extra", "v=" + version + ";x=1"},
	} {
		got, err := reached(ctx, t, http.MethodGet, base+";"+v.params+"/$metadata")
		if err != nil {
			return nil, err
		}
		facts = append(facts, Fact{ID: v.id, Text: fmt.Sprintf("the OData service ;v=%s answers as ;%s", version, v.params), Got: got,
			Note: "the guard refuses such variants unless an exact entry point decides"})
	}
	return facts, nil
}

// modifiedAtFact changes a configuration parameter and reads ModifiedAt
// before and after; the value is restored.
func modifiedAtFact(t Target, flow, key string) (Fact, error) {
	conf := cpi.NewConfiguration(t.API)
	params, err := conf.Get(flow, "active")
	if err != nil {
		return Fact{}, err
	}
	p := cpi.FindParameterByKey(key, params.Root.Results)
	if p == nil {
		return Fact{}, fmt.Errorf("flow %s has no parameter %s", flow, key)
	}
	before, err := modifiedAt(t.API, flow)
	if err != nil {
		return Fact{}, err
	}
	time.Sleep(1100 * time.Millisecond) // timestamps in milliseconds or seconds
	if err := conf.Update(flow, "active", key, p.ParameterValue+"-cpictl-probe"); err != nil {
		return Fact{}, err
	}
	after, err := modifiedAt(t.API, flow)
	restoreErr := conf.Update(flow, "active", key, p.ParameterValue)
	if err != nil {
		return Fact{}, err
	}
	if restoreErr != nil {
		return Fact{}, fmt.Errorf("restoring %s of %s: %w", key, flow, restoreErr)
	}
	return Fact{ID: "modified-on-configure", Text: "a configuration change moves the designtime ModifiedAt", Got: after.After(before),
		Note: "if not, the ProcessDirect check cannot see a changed address parameter (docs/access-scope.md, limits)"}, nil
}

func modifiedAt(api *httpclnt.HTTPExecuter, flow string) (time.Time, error) {
	info, ok, err := cpi.GetDesigntimeInfo(api, "Integration", flow, "active")
	switch {
	case err != nil:
		return time.Time{}, err
	case !ok:
		return time.Time{}, fmt.Errorf("flow %s not found", flow)
	case info.ModifiedAt.IsZero():
		return time.Time{}, fmt.Errorf("the tenant reports no ModifiedAt for %s", flow)
	}
	return info.ModifiedAt, nil
}

func get(api *httpclnt.HTTPExecuter, path string) ([]byte, error) {
	resp, err := api.Exec(http.MethodGet, path, http.NoBody, map[string]string{"Accept": "application/json"})
	if err != nil {
		return nil, err
	}
	b, err := api.ReadRespBody(resp)
	if err == nil && resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	return b, err
}

func swapCase(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 32)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func cmpErr(err, otherwise error) error {
	if err != nil {
		return err
	}
	return otherwise
}
