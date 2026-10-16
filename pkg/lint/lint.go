// Package lint checks the integration flows of a local content tree and
// applies the mechanical fixes (see docs/lint.md).
package lint

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cpars-innovation/cpicli/pkg/catalog"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/iflow"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
	"gopkg.in/yaml.v3"
)

// Lint severities.
const (
	SevError   = "error"
	SevWarning = "warning"
	SevInfo    = "info"
	SevOff     = "off"
)

var severityRank = map[string]int{SevInfo: 1, SevWarning: 2, SevError: 3}

// Finding is one problem or improvement in one artifact.
type Finding struct {
	Rule     string `json:"rule"`
	Group    string `json:"group"`
	Severity string `json:"severity"`
	Package  string `json:"package,omitempty"`
	// Vendor is the package's vendor (its <package>.json from snapshot),
	// "SAP" for an artifact ID starting with com.sap. without one.
	Vendor   string `json:"vendor,omitempty"`
	Artifact string `json:"artifact"`
	// Path is the artifact folder below the linted directory.
	Path string `json:"path"`
	// File is relative to the artifact folder; Element is the model element
	// (step ID) with its name.
	File        string `json:"file,omitempty"`
	Element     string `json:"element,omitempty"`
	ElementName string `json:"elementName,omitempty"`
	Message     string `json:"message"`
	Suggestion  string `json:"suggestion,omitempty"`
	// Fixable: lint --fix can apply the suggestion.
	Fixable bool `json:"fixable,omitempty"`
	// Related are other artifacts involved (e.g. flows with the same script).
	Related []string `json:"related,omitempty"`
	// Fingerprint identifies the finding across runs (baseline).
	Fingerprint string `json:"fingerprint"`
	// Baseline: the finding is in the baseline file (known, not new).
	Baseline bool `json:"baseline,omitempty"`

	// key identifies the finding within its artifact and rule.
	key string
	// fix carries what --fix needs.
	fix *lintFix
}

// Config is .cpi/lint.yaml.
type Config struct {
	// Rules sets the severity per rule ("off" disables it).
	Rules map[string]string `yaml:"rules" json:"rules,omitempty"`
	// Settings are the thresholds of the rules.
	Settings Settings `yaml:"settings" json:"settings"`
	// ScriptCollections decides where reused scripts go.
	ScriptCollections ScriptCollectionConfig `yaml:"scriptCollections" json:"scriptCollections"`
	// Naming are regular expressions for IDs (empty: not checked).
	Naming struct {
		IFlowID string `yaml:"iflowId" json:"iflowId,omitempty"`
	} `yaml:"naming" json:"naming"`
	// Layout is the spacing and mode of the layout rule's fix and of
	// cpictl iflow layout.
	Layout iflow.LayoutOptions `yaml:"layout" json:"layout"`
	// ScriptTests are where the tests of script collection scripts are and
	// how much of them they must cover.
	ScriptTests ScriptTestConfig `yaml:"scriptTests" json:"scriptTests"`
	// Repository names the configure and deploy files the configure-* and
	// flow-without-deployment rules compare the flows with (relative to
	// the project root; empty: not checked).
	Repository struct {
		Configure    string `yaml:"configure" json:"configure,omitempty"`
		DeployConfig string `yaml:"deployConfig" json:"deployConfig,omitempty"`
	} `yaml:"repository" json:"repository"`
	// IgnoreVendors leaves the artifacts of these package vendors out of
	// the findings (default SAP: content SAP delivers, which customers do
	// not edit); [] checks every vendor. Cross-flow rules still see them.
	IgnoreVendors []string `yaml:"ignoreVendors" json:"ignoreVendors"`
	// Ignore suppresses findings: artifact and rule patterns ("*" for all).
	Ignore []struct {
		Artifact string `yaml:"artifact" json:"artifact"`
		Rule     string `yaml:"rule" json:"rule"`
	} `yaml:"ignore" json:"ignore,omitempty"`
	// baseDir resolves the relative paths of the config: the project root
	// (the folder holding .cpi) for .cpi/lint.yaml.
	baseDir string
}

// ScriptTestConfig locates the tests of script collection scripts and their
// coverage report. Relative paths are relative to the project root (the
// folder that holds .cpi).
type ScriptTestConfig struct {
	// Dir holds the tests in the layout of the content tree:
	// <dir>/<package>/<collection>/ (default tests/scripts).
	Dir string `yaml:"dir" json:"dir"`
	// CoverageReport is the JaCoCo XML report of the test run (default
	// build/reports/jacoco/test/jacocoTestReport.xml).
	CoverageReport string `yaml:"coverageReport" json:"coverageReport"`
	// MinCoverage is the line coverage per script in percent (default 80).
	MinCoverage float64 `yaml:"minCoverage" json:"minCoverage"`
}

// path resolves a path of the config.
// Path resolves a path of the config (scriptTests.dir, coverageReport)
// against the project root.
func (c *Config) Path(p string) string { return c.path(p) }

func (c *Config) path(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.baseDir, filepath.FromSlash(p))
}

// configBase is the project root of a rules file: the parent of .cpi, else
// the file's folder.
func configBase(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	dir := filepath.Dir(abs)
	if filepath.Base(dir) == ".cpi" {
		return filepath.Dir(dir)
	}
	return dir
}

// Settings are the thresholds.
type Settings struct {
	DuplicateMinFlows int `yaml:"duplicateMinFlows" json:"duplicateMinFlows"`
	RouterMinValues   int `yaml:"routerMinValues" json:"routerMinValues"`
	LookupMinEntries  int `yaml:"lookupMinEntries" json:"lookupMinEntries"`
	LargeScriptLines  int `yaml:"largeScriptLines" json:"largeScriptLines"`
}

// ScriptCollectionConfig names the collections reused scripts move to.
type ScriptCollectionConfig struct {
	// CrossPackage: flows may reference a collection in another package
	// (check that your tenant supports it). false: one collection per
	// package, also for scripts reused across packages.
	CrossPackage bool `yaml:"crossPackage" json:"crossPackage"`
	// PackageCollection is the ID of a package's collection; {package} is
	// replaced by the package ID.
	PackageCollection string `yaml:"packageCollection" json:"packageCollection"`
	// SharedPackage and SharedCollection hold scripts reused across packages
	// (CrossPackage only).
	SharedPackage    string `yaml:"sharedPackage" json:"sharedPackage"`
	SharedCollection string `yaml:"sharedCollection" json:"sharedCollection"`
}

// DefaultConfig returns the defaults.
func DefaultConfig() *Config {
	return &Config{
		Rules:    map[string]string{},
		Settings: Settings{DuplicateMinFlows: 2, RouterMinValues: 4, LookupMinEntries: 10, LargeScriptLines: 300},
		ScriptCollections: ScriptCollectionConfig{PackageCollection: "{package}_Scripts",
			SharedPackage: "SharedScripts", SharedCollection: "Shared_Scripts"},
		IgnoreVendors: []string{"SAP"},
		ScriptTests:   ScriptTestConfig{Dir: "tests/scripts", CoverageReport: "build/reports/jacoco/test/jacocoTestReport.xml", MinCoverage: 80},
	}
}

// LoadConfig reads a lint config over the defaults (a missing file:
// defaults).
func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()
	cfg.baseDir = configBase(path)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, output.Usagef("%s: %v", path, err)
	}
	for rule, sev := range cfg.Rules {
		if _, ok := lintRuleByID[rule]; !ok {
			return nil, output.Usagef("%s: unknown rule %q (cpictl lint --list-rules)", path, rule)
		}
		if _, ok := severityRank[sev]; !ok && sev != SevOff {
			return nil, output.Usagef("%s: rule %s: severity %q (off, info, warning, error)", path, rule, sev)
		}
	}
	d := DefaultConfig().Settings
	if cfg.Settings.DuplicateMinFlows < 2 {
		cfg.Settings.DuplicateMinFlows = d.DuplicateMinFlows
	}
	if cfg.Settings.RouterMinValues < 2 {
		cfg.Settings.RouterMinValues = d.RouterMinValues
	}
	if cfg.Settings.LookupMinEntries < 2 {
		cfg.Settings.LookupMinEntries = d.LookupMinEntries
	}
	if cfg.Settings.LargeScriptLines < 1 {
		cfg.Settings.LargeScriptLines = d.LargeScriptLines
	}
	if cfg.ScriptCollections.PackageCollection == "" {
		cfg.ScriptCollections.PackageCollection = "{package}_Scripts"
	}
	dt := DefaultConfig().ScriptTests
	cfg.ScriptTests.Dir = cmp.Or(cfg.ScriptTests.Dir, dt.Dir)
	cfg.ScriptTests.CoverageReport = cmp.Or(cfg.ScriptTests.CoverageReport, dt.CoverageReport)
	if cfg.ScriptTests.MinCoverage <= 0 || cfg.ScriptTests.MinCoverage > 100 {
		cfg.ScriptTests.MinCoverage = dt.MinCoverage
	}
	return cfg, nil
}

// Options select what is linted.
type Options struct {
	// Dir is the content tree (layout <package>/<artifact>).
	Dir string
	// Packages and Artifacts limit the reported findings (names or
	// path.Match patterns); all artifacts are read for cross-flow rules.
	Packages, Artifacts []string
	// Changed limits the findings to artifacts changed in Git since Since
	// (default HEAD: uncommitted changes).
	Changed bool
	Since   string
	Config  *Config
	// Baseline is the baseline file (empty: none).
	Baseline string
	// DeploymentCopies: artifactDir key "<package>/<dir>" -> deployed IDs
	// with their configOverrides keys (from the deploy config).
	DeploymentCopies map[string][]DeploymentCopy
	// MinSeverity drops findings below it (default info).
	MinSeverity string
	// Components is the target tenant's component catalogue (nil: rules
	// compare the flows with each other only).
	Components *catalog.Catalog
	// Configure are the configure files the flows' parameters are compared
	// with (configure-* rules; none: not checked).
	Configure []ConfigureFile
	// DeployEntries are the artifact folders the deploy config lists
	// (flow-without-deployment; nil: not checked).
	DeployEntries map[string]bool
}

// DeploymentCopy is one ID an artifact folder is deployed as.
type DeploymentCopy struct {
	ID        string   `json:"id"`
	Overrides []string `json:"overrides,omitempty"`
}

// Result is the outcome of Lint.
type Result struct {
	Dir       string         `json:"dir"`
	Artifacts int            `json:"artifacts"`
	Checked   int            `json:"checked"`
	Findings  []Finding      `json:"findings"`
	Counts    map[string]int `json:"counts"`
	// New counts the findings per severity that are not in the baseline.
	New map[string]int `json:"new"`
	// ByRule counts the findings per rule.
	ByRule map[string]int `json:"byRule"`
	// SkippedVendor counts the artifacts left out by Config.IgnoreVendors.
	SkippedVendor int `json:"skippedVendor,omitempty"`
}

// vendorCache reads the vendor of each package once.
type vendorCache map[string]string

// of returns the vendor of an artifact's package: the Vendor of
// <package folder>/<package>.json (written by snapshot), else "SAP" for an
// artifact ID starting with com.sap. (SAP's content, linted without its
// package details), else "".
func (c vendorCache) of(la ops.LocalArtifact) string {
	if la.PackageID != "" {
		pkgDir := filepath.Dir(la.Dir)
		v, ok := c[pkgDir]
		if !ok {
			if d, err := cpi.GetPackageDetails(filepath.Join(pkgDir, la.PackageID+".json")); err == nil && d != nil {
				v = strings.TrimSpace(d.Root.Vendor)
			}
			c[pkgDir] = v
		}
		if v != "" {
			return v
		}
	}
	if strings.HasPrefix(la.ID, "com.sap.") {
		return "SAP"
	}
	return ""
}

// lintArtifact is an artifact as the rules see it.
type lintArtifact struct {
	ops.LocalArtifact
	// Models by file (relative to the artifact folder).
	Models    map[string]*iflow.Model
	ModelText string
	// Scripts by file (relative); Files are all files below src/main/resources.
	Scripts map[string][]byte
	Files   []string
	Params  map[string]string
	Vendor  string
	inScope bool
}

func (a *lintArtifact) finding(rule, file, element, elementName, key, msg, suggestion string) Finding {
	return Finding{Rule: rule, Package: a.PackageID, Vendor: a.Vendor, Artifact: a.ID, Path: a.Rel, File: file, Element: element,
		ElementName: elementName, Message: msg, Suggestion: suggestion, key: key}
}

type lintContext struct {
	skippedVendor int
	opts          Options
	cfg           *Config
	artifacts     []*lintArtifact
	byID          map[string]*lintArtifact
}

// runLint checks the artifacts of a content tree against the rules. Only
// local files are read.
func runLint(ctx context.Context, o Options) (*Result, *lintContext, error) {
	if o.Config == nil {
		o.Config = DefaultConfig()
	}
	if info, err := os.Stat(o.Dir); err != nil || !info.IsDir() {
		return nil, nil, output.Usagef("%s is not a directory", o.Dir)
	}
	for _, p := range append(slices.Clone(o.Packages), o.Artifacts...) {
		if _, err := path.Match(p, ""); err != nil {
			return nil, nil, output.Usagef("invalid pattern %q", p)
		}
	}
	lc := &lintContext{opts: o, cfg: o.Config, byID: map[string]*lintArtifact{}}
	var changed map[string]bool
	if o.Changed {
		var err error
		if changed, err = changedArtifactDirs(ctx, o.Dir, o.Since); err != nil {
			return nil, nil, err
		}
	}
	vendors := vendorCache{}
	var walkErr error
	err := ops.WalkLocalArtifacts(ctx, o.Dir, func(la ops.LocalArtifact) {
		a, err := loadLintArtifact(la)
		if err != nil {
			walkErr = errors.Join(walkErr, fmt.Errorf("%s: %w", la.Rel, err))
			return
		}
		a.Vendor = vendors.of(la)
		a.inScope = ops.MatchAny(o.Packages, a.PackageID) && ops.MatchAny(o.Artifacts, a.ID) && (changed == nil || changed[a.Rel])
		if a.inScope && a.Vendor != "" && slices.ContainsFunc(o.Config.IgnoreVendors, func(v string) bool { return strings.EqualFold(v, a.Vendor) }) {
			a.inScope = false
			lc.skippedVendor++
		}
		lc.artifacts = append(lc.artifacts, a)
		lc.byID[a.ID] = a
	}, func(err error) { walkErr = errors.Join(walkErr, err) })
	if err != nil {
		return nil, nil, err
	}
	if walkErr != nil {
		return nil, nil, walkErr
	}
	sort.Slice(lc.artifacts, func(i, j int) bool { return lc.artifacts[i].Rel < lc.artifacts[j].Rel })

	res := &Result{Dir: o.Dir, Artifacts: len(lc.artifacts), SkippedVendor: lc.skippedVendor, Findings: []Finding{}, Counts: map[string]int{}, New: map[string]int{}, ByRule: map[string]int{}}
	var all []Finding
	for _, a := range lc.artifacts {
		if a.inScope {
			res.Checked++
		}
		if !a.inScope || (a.Type != "Integration" && a.Type != "ScriptCollection") {
			continue
		}
		for _, r := range lintRules {
			if a.Type == "ScriptCollection" && !collectionRules[r.id] {
				continue
			}
			if r.artifact != nil && lc.enabled(r) {
				for _, f := range r.artifact(lc, a) {
					all = append(all, lc.complete(r, f))
				}
			}
		}
	}
	for _, r := range lintRules {
		if r.cross != nil && lc.enabled(r) {
			for _, f := range r.cross(lc) {
				if a := lc.byID[f.Artifact]; a != nil && a.inScope {
					all = append(all, lc.complete(r, f))
				}
			}
		}
	}
	known, err := loadBaseline(o.Baseline)
	if err != nil {
		return nil, nil, err
	}
	min := severityRank[o.MinSeverity]
	for _, f := range all {
		if lc.ignored(f) || severityRank[f.Severity] < min {
			continue
		}
		f.Baseline = known[f.Fingerprint]
		res.Findings = append(res.Findings, f)
		res.Counts[f.Severity]++
		res.ByRule[f.Rule]++
		if !f.Baseline {
			res.New[f.Severity]++
		}
	}
	sort.SliceStable(res.Findings, func(i, j int) bool {
		a, b := res.Findings[i], res.Findings[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if severityRank[a.Severity] != severityRank[b.Severity] {
			return severityRank[a.Severity] > severityRank[b.Severity]
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		return a.key < b.key
	})
	return res, lc, nil
}

func (lc *lintContext) enabled(r lintRule) bool {
	return lc.severity(r) != SevOff
}

func (lc *lintContext) severity(r lintRule) string {
	if s, ok := lc.cfg.Rules[r.id]; ok {
		return s
	}
	return r.severity
}

func (lc *lintContext) complete(r lintRule, f Finding) Finding {
	f.Rule, f.Group = r.id, r.group
	if f.Severity == "" || lc.cfg.Rules[r.id] != "" {
		f.Severity = lc.severity(r)
	}
	f.Fixable = f.fix != nil
	sum := sha256.Sum256([]byte(strings.Join([]string{r.id, f.Path, f.File, f.Element, f.key}, "\x00")))
	f.Fingerprint = hex.EncodeToString(sum[:8])
	return f
}

func (lc *lintContext) ignored(f Finding) bool {
	for _, ig := range lc.cfg.Ignore {
		a, r := ig.Artifact, ig.Rule
		if a == "" {
			a = "*"
		}
		if r == "" {
			r = "*"
		}
		if ok, _ := path.Match(a, f.Artifact); !ok {
			continue
		}
		if ok, _ := path.Match(r, f.Rule); ok {
			return true
		}
	}
	return false
}

func loadLintArtifact(la ops.LocalArtifact) (*lintArtifact, error) {
	a := &lintArtifact{LocalArtifact: la, Models: map[string]*iflow.Model{}, Scripts: map[string][]byte{}, Params: map[string]string{}}
	root := filepath.Join(la.Dir, filepath.FromSlash(ops.ResourcesDir))
	var text strings.Builder
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(la.Dir, p)
		rel = filepath.ToSlash(rel)
		a.Files = append(a.Files, rel)
		switch {
		case strings.HasSuffix(rel, ".iflw"):
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			m, err := iflow.Parse(data)
			if err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			a.Models[rel] = m
			text.Write(data)
		case strings.HasPrefix(rel, ops.ResourcesDir+"/script/"):
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			a.Scripts[rel] = data
		case rel == ops.ResourcesDir+"/parameters.prop":
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			a.Params = ops.PropertyValues(data)
		}
		return nil
	})
	sort.Strings(a.Files)
	a.ModelText = text.String()
	return a, err
}

// changedArtifactDirs returns the artifact folders (relative to dir) with
// files changed since a Git ref, including uncommitted and untracked ones.
func changedArtifactDirs(ctx context.Context, dir, since string) (map[string]bool, error) {
	if since == "" {
		since = "HEAD"
	}
	top, err := ops.Git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, output.Usagef("--changed needs a Git repository: %v", err)
	}
	root := strings.TrimSpace(top)
	absDir, _ := filepath.Abs(dir)
	if r, err := filepath.EvalSymlinks(absDir); err == nil {
		absDir = r
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	if err := ops.CheckGitRef(since); err != nil {
		return nil, err
	}
	diff, err := ops.Git(ctx, root, "diff", "--name-only", since, "--")
	if err != nil {
		return nil, err
	}
	untracked, err := ops.Git(ctx, root, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, line := range strings.Split(diff+"\n"+untracked, "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		rel, err := filepath.Rel(absDir, filepath.Join(root, filepath.FromSlash(line)))
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) >= 2 {
			out[parts[0]+"/"+parts[1]] = true
		}
		out[parts[0]] = true
	}
	return out, nil
}

// lint baseline: known findings that do not fail CI.
type lintBaseline struct {
	Findings []string `json:"findings"`
}

func loadBaseline(path string) (map[string]bool, error) {
	known := map[string]bool{}
	if path == "" {
		return known, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return known, nil
	}
	if err != nil {
		return nil, err
	}
	var b lintBaseline
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, output.Usagef("baseline %s: %v", path, err)
	}
	for _, f := range b.Findings {
		known[f] = true
	}
	return known, nil
}

// WriteBaseline records the findings as known (sorted, for stable diffs).
func WriteBaseline(path string, findings []Finding) error {
	b := lintBaseline{Findings: []string{}}
	for _, f := range findings {
		b.Findings = append(b.Findings, f.Fingerprint)
	}
	sort.Strings(b.Findings)
	b.Findings = slices.Compact(b.Findings)
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// AtLeast reports whether severity is at least min.
func AtLeast(severity, min string) bool {
	return severityRank[severity] >= severityRank[min]
}

func readArtifactFile(a *lintArtifact, rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join(a.Dir, filepath.FromSlash(rel)))
}
