package ops

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// Discovery is the inventory of a tenant or a local content repository: the
// facts from which conventions (naming, adapters, error handling, logging,
// scripts) are derived.
type Discovery struct {
	GeneratedAt time.Time           `json:"generatedAt"`
	Source      string              `json:"source"`
	Summary     DiscoverySummary    `json:"summary"`
	Packages    []DiscoveredPackage `json:"packages"`
	IFlows      []IFlowFacts        `json:"iflows"`
	Errors      []string            `json:"errors,omitempty"`
}

// DiscoveredPackage is a package with its artifact counts per type.
type DiscoveredPackage struct {
	ID        string         `json:"id"`
	Name      string         `json:"name,omitempty"`
	Artifacts map[string]int `json:"artifacts"`
}

// Count is a value and how often it occurs.
type Count struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// NamingStats describes how a set of identifiers is formed.
type NamingStats struct {
	// Separators counts identifiers per separator style (underscore, hyphen,
	// dot, space, camelCase, none).
	Separators map[string]int `json:"separators"`
	// Prefixes are the most frequent first segments (at least twice).
	Prefixes []Count  `json:"prefixes"`
	Examples []string `json:"examples"`
}

// SharedScript is a script with identical content in several flows.
type SharedScript struct {
	Name   string   `json:"name"`
	Hash   string   `json:"hash"`
	IFlows []string `json:"iflows"`
}

// DiscoverySummary aggregates the facts of all discovered flows.
type DiscoverySummary struct {
	Packages      int            `json:"packages"`
	IFlows        int            `json:"iflows"`
	ArtifactTypes map[string]int `json:"artifactTypes"`

	PackageNaming NamingStats `json:"packageNaming"`
	IFlowNaming   NamingStats `json:"iflowNaming"`
	IFlowNames    NamingStats `json:"iflowDisplayNames"`

	// Triggers counts how flows are started (sender adapters and Timer).
	Triggers         []Count `json:"triggers"`
	SenderAdapters   []Count `json:"senderAdapters"`
	ReceiverAdapters []Count `json:"receiverAdapters"`
	Steps            []Count `json:"steps"`
	// WithExceptionSubprocess is the number of flows with an exception subprocess.
	WithExceptionSubprocess int     `json:"withExceptionSubprocess"`
	LogLevels               []Count `json:"logLevels"`
	ReturnExceptionToSender []Count `json:"returnExceptionToSender"`

	ScriptLanguages        []Count        `json:"scriptLanguages"`
	ScriptNames            []Count        `json:"scriptNames"`
	SharedScripts          []SharedScript `json:"sharedScripts"`
	CustomHeaderProperties []Count        `json:"customHeaderProperties"`
	HeadersSet             []Count        `json:"headersSet"`
	PropertiesSet          []Count        `json:"propertiesSet"`
	ParameterKeys          []Count        `json:"parameterKeys"`
	CredentialRefs         []Count        `json:"credentialRefs"`
	RuntimeStatuses        []Count        `json:"runtimeStatuses,omitempty"`
}

// DiscoverOptions limit a tenant discovery.
type DiscoverOptions struct {
	PackageIDs []string
	// MaxIFlows stops after this many integration flows (0: all).
	MaxIFlows int
	// Parallel downloads (default 4).
	Parallel int
}

// topN limits the aggregated lists.
const topN = 30

// DiscoverTenant inventories the packages and integration flows of a tenant.
// Integration flows are downloaded and analysed in memory; nothing is written
// to the tenant. Flows that cannot be read are reported in Errors.
func DiscoverTenant(ctx context.Context, exe *httpclnt.HTTPExecuter, source string, opts DiscoverOptions) (*Discovery, error) {
	exe = exe.WithContext(ctx)
	if opts.Parallel <= 0 {
		opts.Parallel = 4
	}
	d := &Discovery{GeneratedAt: time.Now().UTC().Truncate(time.Second), Source: source, Packages: []DiscoveredPackage{}, IFlows: []IFlowFacts{}}
	ip := cpi.NewIntegrationPackage(exe)
	pkgs, err := ip.GetPackagesData()
	if err != nil {
		return nil, err
	}
	runtime := map[string]string{}
	if rts, err := cpi.NewContent(exe).RuntimeArtifacts(nil); err == nil {
		for _, rt := range rts {
			runtime[rt.Id] = rt.Status
		}
	} else if httpclnt.IsAuthError(err) {
		return nil, err
	} else {
		d.Errors = append(d.Errors, fmt.Sprintf("runtime artifacts: %v", err))
	}

	type job struct{ pkg, id, name, version string }
	var jobs []job
	for _, p := range pkgs {
		if len(opts.PackageIDs) > 0 && !slices.Contains(opts.PackageIDs, p.Root.Id) {
			continue
		}
		arts, err := ip.GetAllArtifacts(p.Root.Id)
		if err != nil {
			if httpclnt.IsAuthError(err) {
				return nil, err
			}
			d.Errors = append(d.Errors, fmt.Sprintf("package %s: %v", p.Root.Id, err))
			continue
		}
		dp := DiscoveredPackage{ID: p.Root.Id, Name: p.Root.Name, Artifacts: map[string]int{}}
		for _, a := range arts {
			dp.Artifacts[a.ArtifactType]++
			if a.ArtifactType == "Integration" && (opts.MaxIFlows == 0 || len(jobs) < opts.MaxIFlows) {
				jobs = append(jobs, job{pkg: p.Root.Id, id: a.Id, name: a.Name, version: a.Version})
			}
		}
		d.Packages = append(d.Packages, dp)
	}
	for _, want := range opts.PackageIDs {
		if !slices.ContainsFunc(d.Packages, func(p DiscoveredPackage) bool { return p.ID == want }) {
			d.Errors = append(d.Errors, fmt.Sprintf("package %s not found", want))
		}
	}

	results := make([]IFlowFacts, len(jobs))
	sem := make(chan struct{}, opts.Parallel)
	var wg sync.WaitGroup
	for i, j := range jobs {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			facts := &IFlowFacts{ID: j.id}
			zipped, err := cpi.DownloadArtifact(exe, "Integration", j.id, "active")
			if err == nil {
				var zr *zip.Reader
				if zr, err = zip.NewReader(bytes.NewReader(zipped), int64(len(zipped))); err == nil {
					var root fs.FS
					if root, err = archiveRoot(zr); err == nil {
						var analyzed *IFlowFacts
						analyzed, err = AnalyzeIFlow(root)
						if analyzed != nil {
							facts = analyzed
						}
					}
				}
			}
			facts.ID, facts.PackageID, facts.Version = j.id, j.pkg, j.version
			if facts.Name == "" || facts.Name == j.id {
				facts.Name = j.name
			}
			facts.RuntimeStatus = runtime[j.id]
			if err != nil {
				facts.Error = err.Error()
			}
			results[i] = *facts
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, f := range results {
		if f.Error != "" {
			d.Errors = append(d.Errors, fmt.Sprintf("iflow %s: %s", f.ID, f.Error))
		}
	}
	d.IFlows = results
	d.Summary = summarize(d)
	return d, nil
}

// archiveRoot returns the directory of the archive that holds META-INF.
func archiveRoot(zr *zip.Reader) (fs.FS, error) {
	for _, f := range zr.File {
		if p, ok := strings.CutSuffix(f.Name, "META-INF/MANIFEST.MF"); ok {
			if p == "" {
				return zr, nil
			}
			return fs.Sub(zr, strings.TrimSuffix(p, "/"))
		}
	}
	return nil, fmt.Errorf("archive has no META-INF/MANIFEST.MF")
}

// DiscoverDir inventories the integration flows in a local directory tree
// (e.g. created by sync or snapshot): every directory with a
// META-INF/MANIFEST.MF of an integration flow. The package is taken from the
// parent directory when the flow is two levels below dir.
func DiscoverDir(ctx context.Context, dir string) (*Discovery, error) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil, output.Usagef("%s is not a directory", dir)
	}
	d := &Discovery{GeneratedAt: time.Now().UTC().Truncate(time.Second), Source: dir, Packages: []DiscoveredPackage{}, IFlows: []IFlowFacts{}}
	packages := map[string]*DiscoveredPackage{}
	err = WalkLocalArtifacts(ctx, dir, func(a LocalArtifact) {
		if a.PackageID != "" {
			if packages[a.PackageID] == nil {
				packages[a.PackageID] = &DiscoveredPackage{ID: a.PackageID, Artifacts: map[string]int{}}
			}
			packages[a.PackageID].Artifacts[a.Type]++
		}
		if a.Type == "Integration" {
			facts, err := AnalyzeIFlow(os.DirFS(a.Dir))
			if facts == nil {
				facts = &IFlowFacts{ID: path.Base(a.Rel)}
			}
			facts.PackageID, facts.Path = a.PackageID, a.Rel
			if err != nil {
				facts.Error = err.Error()
				d.Errors = append(d.Errors, fmt.Sprintf("iflow %s: %v", a.Rel, err))
			}
			d.IFlows = append(d.IFlows, *facts)
		}
	}, func(err error) { d.Errors = append(d.Errors, err.Error()) })
	if err != nil {
		return nil, err
	}
	for _, id := range sortedMapKeys(packages) {
		d.Packages = append(d.Packages, *packages[id])
	}
	d.Summary = summarize(d)
	return d, nil
}

// LocalArtifact is an artifact directory (with META-INF/MANIFEST.MF) in a
// local content tree.
type LocalArtifact struct {
	// Dir is the directory, Rel its slash-separated path below the root.
	Dir, Rel string
	// ID is the Bundle-SymbolicName (the folder name if missing).
	ID string
	// PackageID is the parent folder when the artifact is two levels below
	// the root (layout <package>/<artifact>, as written by sync).
	PackageID string
	Type      string
}

// WalkLocalArtifacts calls fn for every artifact directory below dir (.git is
// skipped, artifact directories are not descended into). Unreadable entries
// are reported to onErr.
func WalkLocalArtifacts(ctx context.Context, dir string, fn func(LocalArtifact), onErr func(error)) error {
	return filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			onErr(err)
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !e.IsDir() {
			return nil
		}
		if e.Name() == ".git" {
			return filepath.SkipDir
		}
		mf, err := os.ReadFile(filepath.Join(p, "META-INF", "MANIFEST.MF"))
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		a := LocalArtifact{Dir: p, Rel: rel}
		if parts := strings.Split(rel, "/"); len(parts) == 2 {
			a.PackageID = parts[0]
		}
		h := parseManifest(mf)
		a.Type = artifactTypeOf(h["SAP-BundleType"])
		a.ID, _, _ = strings.Cut(h["Bundle-SymbolicName"], ";")
		if a.ID = strings.TrimSpace(a.ID); a.ID == "" {
			a.ID = path.Base(rel)
		}
		fn(a)
		return filepath.SkipDir
	})
}

func artifactTypeOf(bundleType string) string {
	switch bundleType {
	case "IntegrationFlow":
		return "Integration"
	case "":
		return "Unknown"
	}
	return bundleType
}

func summarize(d *Discovery) DiscoverySummary {
	s := DiscoverySummary{Packages: len(d.Packages), IFlows: len(d.IFlows), ArtifactTypes: map[string]int{}}
	var pkgIDs, flowIDs, flowNames []string
	for _, p := range d.Packages {
		pkgIDs = append(pkgIDs, p.ID)
		for t, n := range p.Artifacts {
			s.ArtifactTypes[t] += n
		}
	}
	counters := map[string]map[string]int{}
	add := func(list string, values ...string) {
		if counters[list] == nil {
			counters[list] = map[string]int{}
		}
		for _, v := range values {
			if v != "" {
				counters[list][v]++
			}
		}
	}
	scripts := map[string]*SharedScript{}
	for _, f := range d.IFlows {
		flowIDs = append(flowIDs, f.ID)
		if f.Name != "" {
			flowNames = append(flowNames, f.Name)
		}
		for _, tr := range f.Triggers {
			add("triggers", tr.Adapter)
		}
		add("sender", f.SenderAdapters...)
		add("receiver", f.ReceiverAdapters...)
		for step, n := range f.Steps {
			counters["steps"] = increment(counters["steps"], step, n)
		}
		if f.ExceptionSubprocess {
			s.WithExceptionSubprocess++
		}
		add("log", f.LogLevel)
		add("returnException", f.ReturnExceptionToSender)
		add("params", f.Parameters...)
		add("creds", f.CredentialRefs...)
		add("headers", f.HeadersSet...)
		add("properties", f.PropertiesSet...)
		add("runtime", f.RuntimeStatus)
		for _, sc := range f.Scripts {
			add("lang", sc.Language)
			add("scriptNames", sc.Name)
			add("customHeaders", sc.CustomHeaders...)
			key := sc.Hash
			if scripts[key] == nil {
				scripts[key] = &SharedScript{Name: sc.Name, Hash: sc.Hash}
			}
			if !slices.Contains(scripts[key].IFlows, f.ID) {
				scripts[key].IFlows = append(scripts[key].IFlows, f.ID)
			}
		}
	}
	s.PackageNaming, s.IFlowNaming, s.IFlowNames = naming(pkgIDs), naming(flowIDs), naming(flowNames)
	s.Triggers = top(counters["triggers"], 1)
	s.SenderAdapters, s.ReceiverAdapters = top(counters["sender"], 1), top(counters["receiver"], 1)
	s.Steps, s.LogLevels, s.ReturnExceptionToSender = top(counters["steps"], 1), top(counters["log"], 1), top(counters["returnException"], 1)
	s.ScriptLanguages, s.ScriptNames = top(counters["lang"], 1), top(counters["scriptNames"], 2)
	s.CustomHeaderProperties = top(counters["customHeaders"], 1)
	s.HeadersSet, s.PropertiesSet = top(counters["headers"], 1), top(counters["properties"], 1)
	s.ParameterKeys, s.CredentialRefs = top(counters["params"], 1), top(counters["creds"], 1)
	s.RuntimeStatuses = top(counters["runtime"], 1)
	s.SharedScripts = []SharedScript{}
	for _, sc := range scripts {
		if len(sc.IFlows) > 1 {
			sort.Strings(sc.IFlows)
			s.SharedScripts = append(s.SharedScripts, *sc)
		}
	}
	sort.Slice(s.SharedScripts, func(i, j int) bool {
		a, b := s.SharedScripts[i], s.SharedScripts[j]
		if len(a.IFlows) != len(b.IFlows) {
			return len(a.IFlows) > len(b.IFlows)
		}
		return a.Name < b.Name
	})
	if len(s.SharedScripts) > topN {
		s.SharedScripts = s.SharedScripts[:topN]
	}
	return s
}

func increment(m map[string]int, k string, n int) map[string]int {
	if m == nil {
		m = map[string]int{}
	}
	m[k] += n
	return m
}

// top returns the most frequent values (at least min occurrences).
func top(m map[string]int, min int) []Count {
	out := []Count{}
	for v, n := range m {
		if n >= min {
			out = append(out, Count{Value: v, Count: n})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Value < out[j].Value
	})
	if len(out) > topN {
		out = out[:topN]
	}
	return out
}

var reSegment = regexp.MustCompile(`[_\-. ]`)

func naming(ids []string) NamingStats {
	st := NamingStats{Separators: map[string]int{}, Prefixes: []Count{}, Examples: []string{}}
	prefixes := map[string]int{}
	for _, id := range ids {
		style := "none"
		switch {
		case strings.Contains(id, "_"):
			style = "underscore"
		case strings.Contains(id, "-"):
			style = "hyphen"
		case strings.Contains(id, "."):
			style = "dot"
		case strings.Contains(id, " "):
			style = "space"
		case strings.ToLower(id) != id && strings.ToUpper(id) != id:
			style = "camelCase"
		}
		st.Separators[style]++
		if parts := reSegment.Split(id, 2); len(parts) == 2 && parts[0] != "" {
			prefixes[parts[0]]++
		}
	}
	st.Prefixes = top(prefixes, 2)
	if len(st.Prefixes) > 10 {
		st.Prefixes = st.Prefixes[:10]
	}
	sorted := slices.Clone(ids)
	sort.Strings(sorted)
	for i := 0; i < len(sorted) && i < 8; i++ {
		// spread the examples over the sorted list
		st.Examples = append(st.Examples, sorted[i*len(sorted)/min(8, len(sorted))])
	}
	return st
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// WriteDiscovery writes d as indented JSON to file, creating its directory.
func WriteDiscovery(d *Discovery, file string) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(file, append(data, '\n'), 0o644)
}
