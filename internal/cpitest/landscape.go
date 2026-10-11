package cpitest

import (
	"archive/zip"
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/catalog"
	"gopkg.in/yaml.v3"
)

// Landscape is a mock landscape: content in cpictl's snapshot layout plus
// landscape.yaml with what differs per tier, the systems the flows call and
// the message traffic. See docs/mock-tenant.md (Landscapes).
type Landscape struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	// KeyHeaders are the custom header properties that identify a business
	// transaction (written by the flows' scripts), e.g. OrderNo.
	KeyHeaders []string `yaml:"keyHeaders"`
	// Tiers in pipeline order with what differs on each.
	Tiers []TierSpec `yaml:"tiers"`
	// Systems are the receivers the flows call, matched by host.
	Systems          map[string]SystemSpec `yaml:"systems"`
	Credentials      []CredentialSpec      `yaml:"credentials"`
	Keystore         []KeystoreSpec        `yaml:"keystore"`
	PartnerDirectory map[string]string     `yaml:"partnerDirectory"`
	Traffic          []TrafficRule         `yaml:"traffic"`
	// History is how far back message logs are generated at start (default 24h).
	History Duration `yaml:"history"`
	// Seed makes the generated traffic and failures reproducible (default:
	// derived from the landscape name and tier).
	Seed uint64 `yaml:"seed"`
	// Components is a component catalogue file (cpictl components format,
	// relative to the landscape directory) for the Check's version check:
	// what the tenant's runtime profile supports. Default: the built-in
	// catalogue of an integrationcell tenant.
	Components string `yaml:"components"`

	packages   []Package
	artifacts  []landscapeArtifact
	components *catalog.Catalog
}

// TierSpec is what one tier changes on top of the landscape's content.
type TierSpec struct {
	Name string `yaml:"name"`
	// Versions overrides the designtime and running version per artifact.
	Versions map[string]string `yaml:"versions"`
	// Drafts are artifacts being edited in the Web UI (designtime version
	// "Active"; the runtime keeps running the last version).
	Drafts []string `yaml:"drafts"`
	// Locks are artifacts open for editing, with the user holding the lock.
	Locks map[string]string `yaml:"locks"`
	// Parameters are configured values per artifact (as set on this tenant).
	Parameters map[string]map[string]string `yaml:"parameters"`
	// ModifiedBy marks artifacts changed on this tenant by someone (drift).
	ModifiedBy map[string]string `yaml:"modifiedBy"`
	// Exclude are artifacts that do not exist on this tier.
	Exclude []string `yaml:"exclude"`
	// NotDeployed are artifacts that exist but do not run.
	NotDeployed []string `yaml:"notDeployed"`
	// MissingCredentials are landscape credentials absent on this tier.
	MissingCredentials []string `yaml:"missingCredentials"`
	// Keystore overrides entries by alias (e.g. a certificate expiring soon).
	Keystore         []KeystoreSpec        `yaml:"keystore"`
	PartnerDirectory map[string]string     `yaml:"partnerDirectory"`
	Systems          map[string]SystemSpec `yaml:"systems"`
	// TrafficScale multiplies every traffic rule's rate on this tier (default 1).
	TrafficScale float64 `yaml:"trafficScale"`
	// Components is this tier's component catalogue file (default: the
	// landscape's), e.g. a tenant on an older release.
	Components string `yaml:"components"`

	components *catalog.Catalog
}

// SystemSpec is the behaviour of a receiver system.
type SystemSpec struct {
	// Match is a host pattern (* matches any characters), e.g. erp-*.example.com.
	Match string `yaml:"match"`
	// LatencyMs is the time a call takes.
	LatencyMs int `yaml:"latencyMs"`
	// FailRate is the share of calls that fail (0..1).
	FailRate float64 `yaml:"failRate"`
	// Status is the HTTP status of a failed call (default 500).
	Status int `yaml:"status"`
	// Error is the error text of a failed call; {host}, {key} and {status}
	// are replaced.
	Error string `yaml:"error"`
}

// CredentialSpec is a security material entry (never a real secret).
type CredentialSpec struct {
	Name string `yaml:"name"`
	// Kind: basic (user credentials), oauth2 (client credentials) or secure (secure parameter).
	Kind     string `yaml:"kind"`
	User     string `yaml:"user"`
	TokenURL string `yaml:"tokenUrl"`
	ClientID string `yaml:"clientId"`
}

// KeystoreSpec is a certificate in the keystore.
type KeystoreSpec struct {
	Alias         string `yaml:"alias"`
	ExpiresInDays int    `yaml:"expiresInDays"`
}

// TrafficRule generates messages that start at a flow.
type TrafficRule struct {
	// Start is the flow that receives the messages (HTTPS sender or timer).
	Start string `yaml:"start"`
	// PerHour is the number of messages per hour.
	PerHour float64 `yaml:"perHour"`
	// Key generates the business key of each message: {n} is a counter
	// starting at KeyStart, e.g. "SO{n}". Empty: no key.
	Key      string `yaml:"key"`
	KeyStart int64  `yaml:"keyStart"`
	// Tiers limits the rule to some tiers (default: all).
	Tiers []string `yaml:"tiers"`
	// FollowUps are messages that arrive later at another flow with a new
	// correlation ID and the same key (the transaction left the tenant and
	// came back).
	FollowUps []FollowUp `yaml:"followUps"`
}

// FollowUp is a later message of the same business transaction.
type FollowUp struct {
	Start string   `yaml:"start"`
	Every int      `yaml:"every"` // every n-th message of the rule (default 1)
	After Duration `yaml:"after"`
	Tiers []string `yaml:"tiers"`
}

// Duration is a time.Duration written as "24h" or "20m" in YAML.
type Duration time.Duration

// UnmarshalYAML reads a Go duration string.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	*d = Duration(v)
	return nil
}

type landscapeArtifact struct {
	ID, Package, Name, Type, Version string
	Zip                              []byte
	content                          *artifactContent
}

// LoadLandscape reads a landscape directory: landscape.yaml and the content
// under packages/ in the layout cpictl snapshot writes
// (packages/<package>/<package>.json, packages/<package>/<artifact>/...).
func LoadLandscape(fsys fs.FS) (*Landscape, error) {
	data, err := fs.ReadFile(fsys, "landscape.yaml")
	if err != nil {
		return nil, fmt.Errorf("landscape: %w", err)
	}
	l := &Landscape{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(l); err != nil {
		return nil, fmt.Errorf("landscape.yaml: %w", err)
	}
	if err := l.loadPackages(fsys); err != nil {
		return nil, err
	}
	if l.components, err = loadComponents(fsys, l.Components); err != nil {
		return nil, err
	}
	for i := range l.Tiers {
		if l.Tiers[i].components, err = loadComponents(fsys, l.Tiers[i].Components); err != nil {
			return nil, err
		}
	}
	if err := l.validate(); err != nil {
		return nil, fmt.Errorf("landscape.yaml: %w", err)
	}
	return l, nil
}

// loadComponents reads a component catalogue of a landscape (nil without one).
func loadComponents(fsys fs.FS, name string) (*catalog.Catalog, error) {
	if name == "" {
		return nil, nil
	}
	data, err := fs.ReadFile(fsys, path.Clean(strings.TrimPrefix(name, "./")))
	if err != nil {
		return nil, fmt.Errorf("landscape: components: %w", err)
	}
	c, err := parseCatalog(data, name)
	if err != nil {
		return nil, fmt.Errorf("landscape: components: %w", err)
	}
	return c, nil
}

// LoadLandscapeDir reads a landscape from a directory on disk.
func LoadLandscapeDir(dir string) (*Landscape, error) {
	return LoadLandscape(os.DirFS(dir))
}

func (l *Landscape) loadPackages(fsys fs.FS) error {
	pkgs, err := fs.ReadDir(fsys, "packages")
	if err != nil {
		return fmt.Errorf("landscape: packages: %w", err)
	}
	for _, p := range pkgs {
		if !p.IsDir() {
			continue
		}
		dir := path.Join("packages", p.Name())
		pkg := Package{ID: p.Name(), Name: p.Name(), Version: "1.0.0"}
		if meta, err := fs.ReadFile(fsys, path.Join(dir, p.Name()+".json")); err == nil {
			var m struct {
				D struct{ Id, Name, Version, ShortText, Description string } `json:"d"`
			}
			if err := json.Unmarshal(meta, &m); err != nil {
				return fmt.Errorf("landscape: %s: %w", path.Join(dir, p.Name()+".json"), err)
			}
			if m.D.Name != "" {
				pkg.Name = m.D.Name
			}
			pkg.ShortText, pkg.Description = m.D.ShortText, m.D.Description
			if m.D.Version != "" {
				pkg.Version = m.D.Version
			}
		}
		l.packages = append(l.packages, pkg)
		arts, err := fs.ReadDir(fsys, dir)
		if err != nil {
			return err
		}
		for _, a := range arts {
			if !a.IsDir() {
				continue
			}
			art, err := readArtifactDir(fsys, path.Join(dir, a.Name()))
			if err != nil {
				return fmt.Errorf("landscape: %s/%s: %w", p.Name(), a.Name(), err)
			}
			if art == nil {
				continue
			}
			art.Package = pkg.ID
			l.artifacts = append(l.artifacts, *art)
		}
	}
	return nil
}

// readArtifactDir zips an artifact directory (nil without a manifest).
func readArtifactDir(fsys fs.FS, dir string) (*landscapeArtifact, error) {
	if _, err := fs.Stat(fsys, path.Join(dir, "META-INF", "MANIFEST.MF")); err != nil {
		return nil, nil
	}
	var names []string
	err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			names = append(names, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	c := &artifactContent{Manifest: map[string]string{}, Resources: map[string]Resource{}, Parameters: map[string]string{}, Files: map[string][]byte{}}
	for _, n := range names {
		data, err := fs.ReadFile(fsys, n)
		if err != nil {
			return nil, err
		}
		rel := strings.TrimPrefix(n, dir+"/")
		w, err := zw.Create(rel)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
		c.addFile(rel, data)
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(strings.SplitN(c.Manifest["Bundle-SymbolicName"], ";", 2)[0])
	if id == "" {
		id = path.Base(dir)
	}
	return &landscapeArtifact{ID: id, Name: cmpOr(c.Manifest["Bundle-Name"], id), Type: c.artifactType(),
		Version: cmpOr(c.Manifest["Bundle-Version"], "1.0.0"), Zip: buf.Bytes(), content: c}, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

var reTierName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func (l *Landscape) validate() error {
	if l.Name == "" {
		return fmt.Errorf("name is required")
	}
	if len(l.Tiers) == 0 {
		return fmt.Errorf("at least one tier is required")
	}
	ids := map[string]bool{}
	for _, a := range l.artifacts {
		if ids[a.ID] {
			return fmt.Errorf("artifact %s appears twice", a.ID)
		}
		ids[a.ID] = true
	}
	known := func(what, id string) error {
		if !ids[id] {
			return fmt.Errorf("%s: unknown artifact %s", what, id)
		}
		return nil
	}
	creds := map[string]bool{}
	for _, c := range l.Credentials {
		if c.Name == "" || !slices.Contains([]string{"basic", "oauth2", "secure"}, c.Kind) {
			return fmt.Errorf("credential %q: name and kind basic, oauth2 or secure are required", c.Name)
		}
		creds[c.Name] = true
	}
	tiers := map[string]bool{}
	for _, t := range l.Tiers {
		if !reTierName.MatchString(t.Name) || tiers[t.Name] {
			return fmt.Errorf("tier %q: lowercase letters, digits and hyphens, unique", t.Name)
		}
		tiers[t.Name] = true
		if t.TrafficScale < 0 {
			return fmt.Errorf("tier %s: trafficScale must not be negative", t.Name)
		}
		for id := range t.Versions {
			if err := known("tier "+t.Name+" versions", id); err != nil {
				return err
			}
		}
		for id := range t.Parameters {
			if err := known("tier "+t.Name+" parameters", id); err != nil {
				return err
			}
		}
		for id := range t.ModifiedBy {
			if err := known("tier "+t.Name+" modifiedBy", id); err != nil {
				return err
			}
		}
		for id := range t.Locks {
			if err := known("tier "+t.Name+" locks", id); err != nil {
				return err
			}
		}
		for _, list := range [][]string{t.Drafts, t.Exclude, t.NotDeployed} {
			for _, id := range list {
				if err := known("tier "+t.Name, id); err != nil {
					return err
				}
			}
		}
		for _, c := range t.MissingCredentials {
			if !creds[c] {
				return fmt.Errorf("tier %s missingCredentials: unknown credential %s", t.Name, c)
			}
		}
		for name := range t.Systems {
			if _, ok := l.Systems[name]; !ok {
				return fmt.Errorf("tier %s systems: unknown system %s", t.Name, name)
			}
		}
	}
	for name, s := range l.Systems {
		if s.Match == "" || s.FailRate < 0 || s.FailRate > 1 {
			return fmt.Errorf("system %s: match is required and failRate between 0 and 1", name)
		}
	}
	for _, r := range l.Traffic {
		if err := known("traffic", r.Start); err != nil {
			return err
		}
		if r.PerHour <= 0 {
			return fmt.Errorf("traffic %s: perHour must be positive", r.Start)
		}
		for _, f := range r.FollowUps {
			if err := known("traffic "+r.Start+" followUps", f.Start); err != nil {
				return err
			}
		}
		for _, t := range r.Tiers {
			if !tiers[t] {
				return fmt.Errorf("traffic %s: unknown tier %s", r.Start, t)
			}
		}
	}
	return nil
}

// TierNames are the landscape's tiers in pipeline order.
func (l *Landscape) TierNames() []string {
	out := make([]string, 0, len(l.Tiers))
	for _, t := range l.Tiers {
		out = append(out, t.Name)
	}
	return out
}

func (l *Landscape) tier(name string) (*TierSpec, error) {
	for i := range l.Tiers {
		if l.Tiers[i].Name == name {
			return &l.Tiers[i], nil
		}
	}
	return nil, fmt.Errorf("landscape %s has no tier %q (%s)", l.Name, name, strings.Join(l.TierNames(), ", "))
}

// systems returns the systems of a tier (tier overrides replace fields that are set).
func (l *Landscape) systems(t *TierSpec) map[string]SystemSpec {
	out := map[string]SystemSpec{}
	for name, s := range l.Systems {
		if o, ok := t.Systems[name]; ok {
			if o.Match != "" {
				s.Match = o.Match
			}
			if o.LatencyMs != 0 {
				s.LatencyMs = o.LatencyMs
			}
			if o.FailRate != 0 {
				s.FailRate = o.FailRate
			}
			if o.Status != 0 {
				s.Status = o.Status
			}
			if o.Error != "" {
				s.Error = o.Error
			}
		}
		out[name] = s
	}
	return out
}

// seed is the random seed of a tier: the landscape's seed, else derived from
// the landscape and tier names (so tiers differ, and runs repeat).
func (l *Landscape) seed(tier string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(l.Name + "/" + tier))
	return l.Seed ^ h.Sum64()
}

// SeedLandscape fills the tenant with one tier of a landscape: packages and
// artifacts (deployed unless listed otherwise), configured parameters,
// security material, Partner Directory and the message history.
func SeedLandscape(m *Tenant, l *Landscape, tier string, now time.Time) error {
	t, err := l.tier(tier)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Artifacts == nil {
		m.Artifacts = map[string]*Artifact{}
	}
	used := map[string]bool{}
	for i, la := range l.artifacts {
		if slices.Contains(t.Exclude, la.ID) {
			continue
		}
		used[la.Package] = true
		version := cmpOr(t.Versions[la.ID], la.Version)
		a := &Artifact{Type: la.Type, DesignVersion: version, Package: la.Package, Name: la.Name,
			ModifiedAt: now.Add(-time.Duration(48+i*24) * time.Hour), ModifiedBy: "ci-pipeline", ConfigBumpsModified: true}
		a.applyContent(la.Zip)
		for k, v := range t.Parameters[la.ID] {
			a.Parameters[k] = v
		}
		if by := t.ModifiedBy[la.ID]; by != "" {
			a.ModifiedBy, a.ModifiedAt = by, now.Add(-5*time.Hour)
		}
		if !slices.Contains(t.NotDeployed, la.ID) {
			a.Runtime = &Runtime{Version: version, Status: "STARTED", DeployedOn: a.ModifiedAt}
		}
		if slices.Contains(t.Drafts, la.ID) {
			a.DesignVersion = "Active"
			if t.ModifiedBy[la.ID] == "" {
				a.ModifiedBy = "dev.user@customer.example"
			}
			a.ModifiedAt = now.Add(-30 * time.Minute)
		}
		m.Artifacts[la.ID] = a
		if by := t.Locks[la.ID]; by != "" {
			h := fnv.New64a()
			_, _ = h.Write([]byte(l.Name + "/" + tier + "/" + la.ID))
			m.Locks = append(m.Locks, Lock{ResourceID: fmt.Sprintf("%016x%016x", h.Sum64(), l.seed(tier)), ArtifactID: la.ID,
				CreatedBy: by, CreatedAt: now.Add(-25 * time.Minute)})
		}
		if a.Runtime != nil {
			m.registerEndpoints(la.ID, a)
		}
	}
	m.Packages = nil
	for _, p := range l.packages {
		if used[p.ID] {
			m.Packages = append(m.Packages, p)
		}
	}

	m.Credentials = map[string]map[string]map[string]any{}
	for _, c := range l.Credentials {
		if slices.Contains(t.MissingCredentials, c.Name) {
			continue
		}
		coll, props := credentialEntry(c)
		if m.Credentials[coll] == nil {
			m.Credentials[coll] = map[string]map[string]any{}
		}
		m.Credentials[coll][c.Name] = props
	}
	keystore := map[string]int{}
	var aliases []string
	for _, list := range [][]KeystoreSpec{l.Keystore, t.Keystore} {
		for _, k := range list {
			if _, ok := keystore[k.Alias]; !ok {
				aliases = append(aliases, k.Alias)
			}
			keystore[k.Alias] = k.ExpiresInDays
		}
	}
	m.Keystore = nil
	for _, alias := range aliases {
		e, err := demoCert(alias, now.Add(time.Duration(keystore[alias])*24*time.Hour).Truncate(time.Second))
		if err != nil {
			return err
		}
		m.Keystore = append(m.Keystore, e)
	}
	m.PDStrings = map[string]string{}
	for _, pd := range []map[string]string{l.PartnerDirectory, t.PartnerDirectory} {
		for k, v := range pd {
			m.PDStrings[k] = v
		}
	}
	if m.Raw == nil {
		m.Raw = map[string]any{}
	}
	m.Raw["/api/v1/DataStores"] = []map[string]any{}
	m.Raw["/api/v1/LogFiles"] = []map[string]any{}
	m.FilterMessageLogs, m.Live = true, true
	m.landscape, m.tierSpec = l, t
	m.Components = t.components
	if m.Components == nil {
		m.Components = l.components
	}
	m.MessageLogSteps = [][]MessageLog{m.history(now)}
	return nil
}

// SeedDir loads a landscape directory and seeds one tier of it.
func SeedDir(m *Tenant, dir, tier string, now time.Time) error {
	l, err := LoadLandscapeDir(dir)
	if err != nil {
		return err
	}
	return SeedLandscape(m, l, tier, now)
}

func credentialEntry(c CredentialSpec) (string, map[string]any) {
	switch c.Kind {
	case "oauth2":
		return "OAuth2ClientCredentials", map[string]any{"Name": c.Name, "TokenServiceUrl": cmpOr(c.TokenURL, "https://"+strings.ToLower(c.Name)+".example.com/oauth/token"),
			"ClientId": cmpOr(c.ClientID, "cpi"), "ClientSecret": "secret"}
	case "secure":
		return "SecureParameters", map[string]any{"Name": c.Name, "SecureParam": "secret"}
	default:
		return "UserCredentials", map[string]any{"Name": c.Name, "Kind": "default", "User": cmpOr(c.User, strings.ToLower(c.Name)), "Password": "secret"}
	}
}

//go:embed landscapes
var builtinLandscapes embed.FS

// BuiltinLandscape returns a landscape shipped with cpictl (demo).
func BuiltinLandscape(name string) (*Landscape, error) {
	sub, err := fs.Sub(builtinLandscapes, "landscapes/"+name)
	if err != nil {
		return nil, err
	}
	if _, err := fs.Stat(sub, "landscape.yaml"); err != nil {
		return nil, fmt.Errorf("no built-in landscape %q", name)
	}
	return LoadLandscape(sub)
}
