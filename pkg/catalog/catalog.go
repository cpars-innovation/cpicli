// Package catalog is the component catalogue of a tenant: the adapter and
// step variants (cmdVariantUri) and their versions that the tenant's
// integration flow editor offers. Flows built with these variants pass the
// tenant's Check; a variant newer than the tenant knows fails there.
//
// A catalogue comes from the Web UI's editor metadata (the JSON the browser
// loads from /api/1.0/metadata, pasted or saved by the user: Import) and from
// the flows that exist (Learn: versions in use are versions the tenant
// accepted). It is stored as .cpi/components.json.
package catalog

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Schema identifies the file format.
const Schema = "cpictl.components/v1"

// Sources of an entry.
const (
	SourceEditor  = "editor"  // the Web UI's editor metadata
	SourceLearned = "learned" // seen in existing flows
)

// Catalog is a tenant's component catalogue.
type Catalog struct {
	Schema      string    `json:"schema"`
	Tenant      string    `json:"tenant,omitempty"`
	GeneratedAt time.Time `json:"generatedAt"`
	// Sources says what the catalogue was built from (editor, learned).
	Sources []string `json:"sources"`
	// SourceDates is when each source was last added: the age of the
	// editor metadata says whether to paste it again after an upgrade.
	// GeneratedAt changes with every write.
	SourceDates map[string]time.Time `json:"sourceDates,omitempty"`
	Adapters    []Adapter            `json:"adapters"`
	Steps       []Step               `json:"steps"`
	// DeployedAdapters are custom adapters deployed on the tenant (runtime
	// artifacts of an adapter type), with the deployed version.
	DeployedAdapters []DeployedAdapter `json:"deployedAdapters,omitempty"`
}

// DeployedAdapter is a custom adapter deployed on the tenant.
type DeployedAdapter struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Version string `json:"version"`
	Type    string `json:"type"`
	Status  string `json:"status,omitempty"`
}

// Adapter is one adapter variant (adapter, direction, transport and message
// protocol).
type Adapter struct {
	// Name is the adapter's name in the model (ComponentType), e.g. HTTP.
	Name              string `json:"name"`
	DisplayName       string `json:"displayName,omitempty"`
	Vendor            string `json:"vendor,omitempty"`
	Direction         string `json:"direction"`
	TransportProtocol string `json:"transportProtocol,omitempty"`
	MessageProtocol   string `json:"messageProtocol,omitempty"`
	// VariantURI is the cmdVariantUri a model uses, with the version.
	VariantURI string `json:"variantUri"`
	// Version is the variant's version (from the URI, else the editor's
	// componentVersion); AdapterVersion is the adapter's release.
	Version        string `json:"version"`
	AdapterVersion string `json:"adapterVersion,omitempty"`
	// Custom marks an adapter installed from a package (not built in).
	Custom bool   `json:"custom,omitempty"`
	Source string `json:"source"`
}

// Step is a flow step, event, gateway or process variant.
type Step struct {
	// Name is the component name of the variant (cname), e.g. GroovyScript.
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	// Flavor is the editor's flavor (e.g. Splitter_GeneralSplitter).
	Flavor     string `json:"flavor,omitempty"`
	Type       string `json:"type,omitempty"` // Activity, Event, Gateway, Pool, subProcess
	VariantURI string `json:"variantUri"`
	Version    string `json:"version,omitempty"`
	Source     string `json:"source"`
}

var reURIPart = regexp.MustCompile(`(?:^|/)([A-Za-z]+)::`)

// ParseURI splits a cmdVariantUri into its parts (ctype, cname, vendor, tp,
// mp, direction, version). Values may contain "/" (cname::PKCS#7/CMS ...).
func ParseURI(uri string) map[string]string {
	parts := map[string]string{}
	idx := reURIPart.FindAllStringSubmatchIndex(uri, -1)
	for i, m := range idx {
		end := len(uri)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		parts[uri[m[2]:m[3]]] = uri[m[1]:end]
	}
	return parts
}

// Key is the URI without its version: the same variant in any version.
func Key(uri string) string {
	if i := strings.LastIndex(uri, "/version::"); i >= 0 {
		return uri[:i]
	}
	return uri
}

// variantKey identifies a variant for comparisons: the URI without its
// version, the "sap:" prefix of the component name and the vendor SAP (older
// flows write cname::JMS where the editor writes cname::sap:JMS).
func variantKey(uri string) string {
	p := ParseURI(uri)
	if len(p) == 0 {
		return uri
	}
	vendor := p["vendor"]
	if strings.EqualFold(vendor, "SAP") {
		vendor = ""
	}
	return strings.Join([]string{p["ctype"], strings.TrimPrefix(p["cname"], "sap:"), vendor, p["tp"], p["mp"], p["direction"]}, "|")
}

// Version is the version part of a URI ("" when it has none).
func Version(uri string) string { return ParseURI(uri)["version"] }

// componentName is the model's ComponentType for an adapter cname
// (sap:HTTP -> HTTP).
func componentName(cname string) string { return strings.TrimPrefix(cname, "sap:") }

// editorMetadata is the part of the Web UI's editor metadata the catalogue
// needs.
type editorMetadata struct {
	AdapterMetadata struct {
		Senders   []editorAdapter `json:"listOfSenderAdapters"`
		Receivers []editorAdapter `json:"listOfReceiverAdapters"`
	} `json:"adapterMetadata"`
	Shapes map[string][]struct {
		Attributes struct {
			Type        string `json:"type"`
			Flavor      string `json:"flavor"`
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
			Version     string `json:"version"`
			CmdURI      string `json:"cmdURI"`
		} `json:"attributes"`
	} `json:"shapeMetadataTOMap"`
}

type editorAdapter struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	AdapterURI  string `json:"adapterURI"`
	Details     []struct {
		MessageProtocol   string `json:"messageProtocol"`
		TransportProtocol string `json:"transportProtocol"`
		Vendor            string `json:"vendor"`
		VariantURI        string `json:"adapterVariantURI"`
		ComponentVersion  string `json:"componentVersion"`
		PackageID         string `json:"packageId"`
	} `json:"adapterDetails"`
}

// Import reads the Web UI's editor metadata (an array with one object, or the
// object) into a catalogue.
func Import(data []byte) (*Catalog, error) {
	var list []editorMetadata
	if err := json.Unmarshal(data, &list); err != nil {
		var one editorMetadata
		if err2 := json.Unmarshal(data, &one); err2 != nil {
			return nil, fmt.Errorf("not the editor metadata of the Web UI (expected adapterMetadata and shapeMetadataTOMap): %w", err)
		}
		list = []editorMetadata{one}
	}
	c := &Catalog{Schema: Schema, Sources: []string{SourceEditor}, SourceDates: map[string]time.Time{SourceEditor: now()}}
	for _, md := range list {
		for dir, adapters := range map[string][]editorAdapter{"Sender": md.AdapterMetadata.Senders, "Receiver": md.AdapterMetadata.Receivers} {
			for _, a := range adapters {
				for _, d := range a.Details {
					if d.VariantURI == "" {
						continue
					}
					p := ParseURI(d.VariantURI)
					c.Adapters = append(c.Adapters, Adapter{Name: componentName(p["cname"]), DisplayName: a.DisplayName, Vendor: d.Vendor,
						Direction: cmp.Or(p["direction"], dir), TransportProtocol: d.TransportProtocol, MessageProtocol: d.MessageProtocol,
						VariantURI: d.VariantURI, Version: cmp.Or(p["version"], d.ComponentVersion), AdapterVersion: ParseURI(a.AdapterURI)["version"],
						Custom: d.PackageID != "", Source: SourceEditor})
				}
			}
		}
		for _, shapes := range md.Shapes {
			for _, s := range shapes {
				a := s.Attributes
				if a.CmdURI == "" {
					continue
				}
				c.Steps = append(c.Steps, Step{Name: ParseURI(a.CmdURI)["cname"], DisplayName: cmp.Or(a.DisplayName, a.Name), Flavor: a.Flavor,
					Type: a.Type, VariantURI: a.CmdURI, Version: Version(a.CmdURI), Source: SourceEditor})
			}
		}
	}
	if len(c.Adapters) == 0 && len(c.Steps) == 0 {
		return nil, fmt.Errorf("no adapters or steps found: expected the Web UI's editor metadata (adapterMetadata, shapeMetadataTOMap)")
	}
	c.normalize()
	return c, nil
}

// Use is a variant in use in a flow model (from Learn's caller).
type Use struct {
	// URI is the element's cmdVariantUri.
	URI string
	// Adapter is set for message flows (ComponentType); Direction too.
	Adapter, Direction string
	TransportProtocol  string
	MessageProtocol    string
	Type               string
}

// Learn adds the variants in use to the catalogue: a variant a flow uses was
// accepted by the tenant. Entries from the editor are kept; a learned
// version newer than the editor's is ignored (it came from another tenant).
func (c *Catalog) Learn(uses []Use) {
	editor := map[string]bool{}
	for _, a := range c.Adapters {
		if a.Source == SourceEditor {
			editor[variantKey(a.VariantURI)] = true
		}
	}
	for _, s := range c.Steps {
		if s.Source == SourceEditor {
			editor[variantKey(s.VariantURI)] = true
		}
	}
	learned := false
	for _, u := range uses {
		p := ParseURI(u.URI)
		if u.URI == "" || p["version"] == "" || editor[variantKey(u.URI)] {
			continue // a URI without a version says nothing about versions
		}
		learned = true
		if u.Adapter != "" || p["ctype"] == "AdapterVariant" {
			c.Adapters = append(c.Adapters, Adapter{Name: cmp.Or(u.Adapter, componentName(p["cname"])), Direction: cmp.Or(u.Direction, p["direction"]),
				TransportProtocol: cmp.Or(u.TransportProtocol, p["tp"]), MessageProtocol: cmp.Or(u.MessageProtocol, p["mp"]),
				Vendor: p["vendor"], VariantURI: u.URI, Version: p["version"], Source: SourceLearned})
			continue
		}
		c.Steps = append(c.Steps, Step{Name: p["cname"], Type: u.Type, VariantURI: u.URI, Version: p["version"], Source: SourceLearned})
	}
	if learned && !slices.Contains(c.Sources, SourceLearned) {
		c.Sources = append(c.Sources, SourceLearned)
	}
	if learned {
		if c.SourceDates == nil {
			c.SourceDates = map[string]time.Time{}
		}
		c.SourceDates[SourceLearned] = now()
	}
	if c.Schema == "" {
		c.Schema = Schema
	}
	c.normalize()
}

// normalize keeps the newest version per variant and sorts the entries.
func (c *Catalog) normalize() {
	c.Adapters = newestAdapters(c.Adapters)
	c.Steps = newestSteps(c.Steps)
	sort.Strings(c.Sources)
}

func newestAdapters(list []Adapter) []Adapter {
	best := map[string]Adapter{}
	var keys []string
	for _, a := range list {
		k := variantKey(a.VariantURI)
		cur, ok := best[k]
		if !ok {
			keys = append(keys, k)
		}
		if !ok || better(a.Source, a.Version, cur.Source, cur.Version) {
			best[k] = a
		}
	}
	sort.Strings(keys)
	out := make([]Adapter, 0, len(keys))
	for _, k := range keys {
		out = append(out, best[k])
	}
	return out
}

func newestSteps(list []Step) []Step {
	best := map[string]Step{}
	var keys []string
	for _, s := range list {
		k := variantKey(s.VariantURI)
		cur, ok := best[k]
		if !ok {
			keys = append(keys, k)
		}
		if !ok || better(s.Source, s.Version, cur.Source, cur.Version) {
			best[k] = s
		}
	}
	sort.Strings(keys)
	out := make([]Step, 0, len(keys))
	for _, k := range keys {
		out = append(out, best[k])
	}
	return out
}

// better reports whether an entry replaces the current one: the editor's
// entry wins over a learned one, else the newer version.
func better(src, v, curSrc, curV string) bool {
	if src != curSrc {
		return src == SourceEditor
	}
	return CompareVersions(v, curV) > 0
}

// Latest returns the catalogue's version of a variant (any version of uri)
// and whether the catalogue knows it.
func (c *Catalog) Latest(uri string) (string, bool) {
	k := variantKey(uri)
	for _, a := range c.Adapters {
		if variantKey(a.VariantURI) == k {
			return a.Version, true
		}
	}
	for _, s := range c.Steps {
		if variantKey(s.VariantURI) == k {
			return s.Version, true
		}
	}
	return "", false
}

// Offered returns the version of a variant (any version of uri) as the
// tenant's editor offers it: only entries from the editor metadata count,
// learned versions are what flows use, not an upper bound.
func (c *Catalog) Offered(uri string) (string, bool) {
	v, _, ok := c.OfferedURI(uri)
	return v, ok
}

// OfferedURI is Offered with the editor's cmdVariantUri of the variant.
func (c *Catalog) OfferedURI(uri string) (version, variantURI string, ok bool) {
	k := variantKey(uri)
	for _, a := range c.Adapters {
		if a.Source == SourceEditor && variantKey(a.VariantURI) == k {
			return a.Version, a.VariantURI, true
		}
	}
	for _, s := range c.Steps {
		if s.Source == SourceEditor && variantKey(s.VariantURI) == k {
			return s.Version, s.VariantURI, true
		}
	}
	return "", "", false
}

// CompareVersions compares dotted versions numerically ("" is lowest).
func CompareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	if a == "" && b != "" {
		return -1
	}
	if b == "" && a != "" {
		return 1
	}
	return 0
}

// now is the time stamped on sources (UTC, whole seconds).
var now = func() time.Time { return time.Now().UTC().Truncate(time.Second) }

// Load reads a catalogue file.
func Load(path string) (*Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if c.Schema != Schema {
		return nil, fmt.Errorf("%s: not a component catalogue (schema %q, expected %q)", path, c.Schema, Schema)
	}
	return &c, nil
}

// Save writes the catalogue (stable order, indented, LF).
func (c *Catalog) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
