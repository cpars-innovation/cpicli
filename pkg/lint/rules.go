package lint

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/cpars-innovation/cpicli/pkg/catalog"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/cpars-innovation/cpicli/pkg/iflow"
	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// Lint rule groups.
const (
	GroupReuse         = "reuse"
	GroupPartnerDir    = "partner-directory"
	GroupDeadWeight    = "dead-weight"
	GroupSimplify      = "simplify"
	GroupRobustness    = "robustness"
	GroupPerformance   = "performance"
	GroupConfiguration = "configuration"
	GroupHygiene       = "hygiene"
)

// lintRule is one check: per artifact or across all artifacts.
type lintRule struct {
	id, group, severity, title string
	artifact                   func(*lintContext, *lintArtifact) []Finding
	cross                      func(*lintContext) []Finding
}

// RuleInfo describes a rule (lint --list-rules).
type RuleInfo struct {
	ID       string `json:"id"`
	Group    string `json:"group"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Fixable  bool   `json:"fixable,omitempty"`
}

// fixableRules can be applied by lint --fix (fixByDefault: without --fix-rules).
var fixableRules = map[string]bool{
	"duplicate-script": true, "use-script-collection": true, "unused-script": true, "unconnected-step": true, "noop-content-modifier": true,
	"layout": true,
}
var fixByDefault = map[string]bool{"duplicate-script": true, "use-script-collection": true, "unused-script": true, "unconnected-step": true}

var lintRules = []lintRule{
	// reuse
	{id: "duplicate-script", group: GroupReuse, severity: SevWarning, title: "The same script is in several flows: move it to a script collection", cross: ruleDuplicateScripts},
	{id: "use-script-collection", group: GroupReuse, severity: SevWarning, title: "A script equals one in an existing script collection: reference the collection", cross: ruleUseCollection},
	{id: "similar-script", group: GroupReuse, severity: SevInfo, title: "Scripts that differ only in comments and formatting", cross: ruleSimilarScripts},
	{id: "duplicate-mapping", group: GroupReuse, severity: SevWarning, title: "The same message mapping or XSLT is in several flows: make it a shared mapping artifact", cross: ruleDuplicateMappings},
	{id: "missing-script-collection", group: GroupReuse, severity: SevWarning, title: "A referenced script collection is not in the repository", artifact: ruleMissingCollection},
	{id: "collection-script-doc", group: GroupReuse, severity: SevWarning, title: "A function of a script collection has no doc comment (what it does, how to call it)", artifact: ruleCollectionScriptDoc},
	// partner directory
	{id: "router-literals", group: GroupPartnerDir, severity: SevWarning, title: "A router decides on many literal values: a routing table for the Partner Directory", artifact: ruleRouterLiterals},
	{id: "lookup-table-in-script", group: GroupPartnerDir, severity: SevInfo, title: "A script holds a lookup table: Partner Directory or a value mapping", artifact: ruleLookupTables},
	{id: "deployment-copies", group: GroupPartnerDir, severity: SevInfo, title: "One flow is deployed several times with different configOverrides: one flow reading partner values from the Partner Directory", cross: ruleDeploymentCopies},
	// dead weight
	{id: "unconnected-step", group: GroupDeadWeight, severity: SevWarning, title: "A step cannot be reached from the start event", artifact: ruleUnconnected},
	{id: "unused-script", group: GroupDeadWeight, severity: SevWarning, title: "A script file is not used by any step", artifact: ruleUnusedScripts},
	{id: "unused-resource", group: GroupDeadWeight, severity: SevInfo, title: "A mapping, schema or other resource is not referenced", artifact: ruleUnusedResources},
	{id: "unused-parameter", group: GroupDeadWeight, severity: SevWarning, title: "An externalised parameter is not used", artifact: ruleUnusedParameters},
	{id: "noop-content-modifier", group: GroupDeadWeight, severity: SevWarning, title: "A content modifier changes nothing", artifact: ruleNoopModifier},
	{id: "property-never-read", group: GroupDeadWeight, severity: SevInfo, title: "An exchange property is set but never read in the flow", artifact: rulePropertyNeverRead},
	// simplify
	{id: "consecutive-content-modifiers", group: GroupSimplify, severity: SevInfo, title: "Content modifiers in a row can be one", artifact: ruleConsecutiveModifiers},
	{id: "converter-roundtrip", group: GroupSimplify, severity: SevWarning, title: "XML to JSON and back (or the reverse) in a row", artifact: ruleConverterRoundtrip},
	{id: "trivial-script", group: GroupSimplify, severity: SevInfo, title: "A script only sets headers or properties: a content modifier does that", artifact: ruleTrivialScripts},
	{id: "large-script", group: GroupSimplify, severity: SevInfo, title: "A very long script: split it or move parts to a script collection", artifact: ruleLargeScripts},
	// robustness
	{id: "no-exception-subprocess", group: GroupRobustness, severity: SevWarning, title: "The integration process has no exception subprocess", artifact: ruleNoExceptionSubprocess},
	{id: "swallowed-exception", group: GroupRobustness, severity: SevWarning, title: "A script catches an exception and ignores it", artifact: ruleSwallowedExceptions},
	{id: "collection-script-tests", group: GroupRobustness, severity: SevWarning, title: "A function of a script collection is not called by any test", artifact: ruleCollectionScriptTests},
	{id: "collection-script-coverage", group: GroupRobustness, severity: SevWarning, title: "A script collection script is below the line coverage minimum (JaCoCo report)", artifact: ruleCollectionScriptCoverage},
	{id: "idoc-no-idempotency", group: GroupRobustness, severity: SevInfo, title: "A flow receives IDocs without an Idempotent Process Call or JMS decoupling", artifact: ruleIDocNoIdempotency},
	// performance and data protection
	{id: "body-as-string", group: GroupPerformance, severity: SevInfo, title: "A script reads the whole body as a string", artifact: ruleBodyAsString},
	{id: "payload-attachment", group: GroupPerformance, severity: SevWarning, title: "A script logs the payload as an attachment", artifact: rulePayloadAttachment},
	{id: "logging-in-loop", group: GroupPerformance, severity: SevWarning, title: "A script writes log attachments in a loop", artifact: ruleLoggingInLoop},
	{id: "println", group: GroupPerformance, severity: SevInfo, title: "A script prints to standard output", artifact: rulePrintln},
	{id: "odata-query-without-paging", group: GroupPerformance, severity: SevWarning, title: "An OData receiver queries without $top and without paging", artifact: ruleODataQueryWithoutPaging},
	{id: "odata-single-calls-in-loop", group: GroupPerformance, severity: SevInfo, title: "An OData receiver creates or updates one record per call in a loop: use $batch", artifact: ruleODataSingleCallsInLoop},
	// configuration
	{id: "hardcoded-endpoint", group: GroupConfiguration, severity: SevWarning, title: "A receiver address is not externalised", artifact: ruleHardcodedEndpoints},
	{id: "hardcoded-url-in-script", group: GroupConfiguration, severity: SevWarning, title: "A script contains a URL", artifact: ruleURLsInScripts},
	{id: "hardcoded-secret", group: GroupConfiguration, severity: SevError, title: "A script or parameter holds a password, token or key", artifact: ruleSecrets},
	{id: "odata-hardcoded-query", group: GroupConfiguration, severity: SevInfo, title: "OData query options or resource path hold environment-specific values (sap-client, company code)", artifact: ruleODataHardcodedQuery},
	{id: "idoc-sap-client-literal", group: GroupConfiguration, severity: SevWarning, title: "An IDoc receiver address has a literal sap-client or host", artifact: ruleIDocSAPClientLiteral},
	{id: "idoc-control-record-literal", group: GroupConfiguration, severity: SevInfo, title: "A mapping or content modifier sets IDoc control record partners (EDI_DC40) to literals", artifact: ruleIDocControlRecordLiteral},
	// hygiene
	{id: "prefer-script-v2", group: GroupHygiene, severity: SevInfo, title: "A Groovy script step uses version 1 (prefer 2.x, Groovy 4)", artifact: rulePreferScriptV2},
	{id: "outdated-component", group: GroupHygiene, severity: SevInfo, title: "A step or adapter uses an older version than the tenant offers or other flows use", cross: ruleOutdatedComponents},
	{id: "component-newer-than-tenant", group: GroupRobustness, severity: SevError, title: "A step or adapter version is newer than the tenant offers (Check and deploy fail)", artifact: ruleComponentNewerThanTenant},
	{id: "script-doc", group: GroupHygiene, severity: SevInfo, title: "A flow script has no comment saying what it does", artifact: ruleScriptDoc},
	{id: "default-step-name", group: GroupHygiene, severity: SevInfo, title: "A step has its default name", artifact: ruleDefaultNames},
	{id: "layout", group: GroupHygiene, severity: SevInfo, title: "The diagram has overlapping or undrawn shapes, or lines through steps", artifact: ruleLayout},
	{id: "naming", group: GroupHygiene, severity: SevWarning, title: "The flow ID does not follow the naming rule of .cpi/lint.yaml", artifact: ruleNaming},
}

var lintRuleByID = func() map[string]lintRule {
	m := map[string]lintRule{}
	for _, r := range lintRules {
		m[r.id] = r
	}
	return m
}()

// Rules lists the rules.
func Rules() []RuleInfo {
	out := make([]RuleInfo, 0, len(lintRules))
	for _, r := range lintRules {
		out = append(out, RuleInfo{ID: r.id, Group: r.group, Severity: r.severity, Title: r.title, Fixable: fixableRules[r.id]})
	}
	return out
}

// --- helpers -----------------------------------------------------------------

// scriptStep is a script step of a model.
type scriptStep struct {
	file string // model file
	el   *iflow.Element
	// script is the file name; bundle the script collection (empty: local).
	script, bundle string
}

// scriptRef is how a script step references a script file: its path under
// script/ ("check.groovy", "v2/check.groovy" for Groovy step version 2).
func scriptRef(file string) string {
	return strings.TrimPrefix(file, ops.ResourcesDir+"/script/")
}

// isV2Script reports a Groovy step version 2 script (script/v2/, Groovy 4,
// com.sap.it.script.v2.api).
func isV2Script(file string) bool { return strings.HasPrefix(scriptRef(file), "v2/") }

func scriptSteps(a *lintArtifact) []scriptStep {
	var out []scriptStep
	for file, m := range a.Models {
		for _, id := range m.Order {
			el := m.Elements[id]
			if el.Props["activityType"] == "Script" || strings.HasSuffix(el.Kind(), "Script") {
				out = append(out, scriptStep{file: file, el: el, script: el.Props["script"], bundle: el.Props["scriptBundleId"]})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].el.ID < out[j].el.ID })
	return out
}

func sortedModelFiles(a *lintArtifact) []string {
	files := make([]string, 0, len(a.Models))
	for f := range a.Models {
		files = append(files, f)
	}
	sort.Strings(files)
	return files
}

// eachElement calls fn for the elements of every model in document order.
func eachElement(a *lintArtifact, fn func(file string, m *iflow.Model, el *iflow.Element)) {
	for _, file := range sortedModelFiles(a) {
		m := a.Models[file]
		for _, id := range m.Order {
			fn(file, m, m.Elements[id])
		}
	}
}

func scriptLanguage(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".groovy", ".gsh":
		return "groovy"
	case ".js":
		return "js"
	}
	return ""
}

// normalizeScript: line endings, trailing blanks and surrounding empty lines
// do not count.
func normalizeScript(data []byte) string {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

var reComment = regexp.MustCompile(`(?s)/\*.*?\*/|(?m)//[^\n]*$`)
var reSpace = regexp.MustCompile(`\s+`)

// structuralScript drops comments and all whitespace.
func structuralScript(data []byte) string {
	return reSpace.ReplaceAllString(reComment.ReplaceAllString(string(data), ""), "")
}

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

func lineOf(data []byte, offset int) int {
	return strings.Count(string(data[:offset]), "\n") + 1
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func listIDs(ids []string, max int) string {
	if len(ids) <= max {
		return strings.Join(ids, ", ")
	}
	return strings.Join(ids[:max], ", ") + fmt.Sprintf(" and %d more", len(ids)-max)
}

// --- reuse -------------------------------------------------------------------

// scriptUse is one local script file of a flow.
type scriptUse struct {
	a    *lintArtifact
	file string
	data []byte
}

// collectionScripts maps normalized script content to the collections (ID ->
// file) that hold it.
func collectionScripts(lc *lintContext) map[string][][2]string {
	out := map[string][][2]string{}
	for _, a := range lc.artifacts {
		if a.Type != "ScriptCollection" {
			continue
		}
		for file, data := range a.Scripts {
			k := hashOf(normalizeScript(data))
			out[k] = append(out[k], [2]string{a.ID, path.Base(file)})
		}
	}
	return out
}

// localScriptGroups groups the local scripts of flows (referenced by a step)
// by key.
func localScriptGroups(lc *lintContext, key func([]byte) string) map[string][]scriptUse {
	groups := map[string][]scriptUse{}
	for _, a := range lc.artifacts {
		if a.Type != "Integration" {
			continue
		}
		used := map[string]bool{}
		for _, s := range scriptSteps(a) {
			if s.bundle == "" && s.script != "" {
				used[s.script] = true
			}
		}
		files := make([]string, 0, len(a.Scripts))
		for f := range a.Scripts {
			files = append(files, f)
		}
		sort.Strings(files)
		for _, f := range files {
			// v2 scripts stay out of the script collection moves: whether a
			// collection can hold them is not verified
			if !used[scriptRef(f)] || scriptLanguage(f) == "" || isV2Script(f) {
				continue
			}
			data := a.Scripts[f]
			if len(strings.TrimSpace(string(data))) == 0 {
				continue
			}
			groups[key(data)] = append(groups[key(data)], scriptUse{a: a, file: f, data: data})
		}
	}
	return groups
}

// collectionFor decides where a script used by these flows belongs.
func (lc *lintContext) collectionFor(uses []scriptUse) (pkg, id string, shared bool) {
	pkgs := map[string]bool{}
	for _, u := range uses {
		pkgs[u.a.PackageID] = true
	}
	sc := lc.cfg.ScriptCollections
	if len(pkgs) > 1 && sc.CrossPackage {
		return sc.SharedPackage, sc.SharedCollection, true
	}
	return "", "", false
}

func packageCollectionID(cfg ScriptCollectionConfig, pkg string) string {
	return strings.ReplaceAll(cfg.PackageCollection, "{package}", pkg)
}

func ruleDuplicateScripts(lc *lintContext) []Finding {
	inCollection := collectionScripts(lc)
	var out []Finding
	for k, uses := range localScriptGroups(lc, func(d []byte) string { return hashOf(normalizeScript(d)) }) {
		flows := map[string]bool{}
		for _, u := range uses {
			flows[u.a.ID] = true
		}
		if len(flows) < lc.cfg.Settings.DuplicateMinFlows || len(inCollection[k]) > 0 {
			continue // too few, or use-script-collection applies
		}
		ids := make([]string, 0, len(flows))
		for id := range flows {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		sharedPkg, sharedID, shared := lc.collectionFor(uses)
		for _, u := range uses {
			pkg, coll := u.a.PackageID, packageCollectionID(lc.cfg.ScriptCollections, u.a.PackageID)
			where := "the package's script collection " + coll
			if shared {
				pkg, coll, where = sharedPkg, sharedID, "the shared script collection "+sharedID+" (package "+sharedPkg+")"
			}
			others := slicesWithout(ids, u.a.ID)
			f := u.a.finding("duplicate-script", u.file, "", "", path.Base(u.file),
				fmt.Sprintf("%s is identical in %d flows (also in %s)", path.Base(u.file), len(ids), listIDs(others, 5)),
				"move it to "+where+" and reference it from every flow; then change it once")
			f.Related = others
			f.fix = &lintFix{kind: fixToCollection, collectionPackage: pkg, collection: coll, script: u.file, scriptHash: k}
			out = append(out, f)
		}
	}
	return out
}

func slicesWithout(ids []string, id string) []string {
	var out []string
	for _, x := range ids {
		if x != id {
			out = append(out, x)
		}
	}
	return out
}

func ruleUseCollection(lc *lintContext) []Finding {
	inCollection := collectionScripts(lc)
	var out []Finding
	for k, uses := range localScriptGroups(lc, func(d []byte) string { return hashOf(normalizeScript(d)) }) {
		colls := inCollection[k]
		if len(colls) == 0 {
			continue
		}
		for _, u := range uses {
			// prefer a collection of the same package
			best := colls[0]
			for _, c := range colls {
				if ca := lc.byID[c[0]]; ca != nil && ca.PackageID == u.a.PackageID {
					best = c
					break
				}
			}
			ca := lc.byID[best[0]]
			pkg := ""
			if ca != nil {
				pkg = ca.PackageID
			}
			if pkg != u.a.PackageID && !lc.cfg.ScriptCollections.CrossPackage {
				continue // not referencable from this package
			}
			f := u.a.finding("use-script-collection", u.file, "", "", path.Base(u.file),
				fmt.Sprintf("%s equals %s in the script collection %s", path.Base(u.file), best[1], best[0]),
				"reference the collection instead of the local copy")
			f.Related = []string{best[0]}
			f.fix = &lintFix{kind: fixToCollection, collectionPackage: pkg, collection: best[0], script: u.file, scriptHash: k, targetName: best[1]}
			out = append(out, f)
		}
	}
	return out
}

func ruleSimilarScripts(lc *lintContext) []Finding {
	var out []Finding
	for _, uses := range localScriptGroups(lc, func(d []byte) string { return hashOf(structuralScript(d)) }) {
		exact := map[string]bool{}
		flows := map[string]bool{}
		for _, u := range uses {
			exact[hashOf(normalizeScript(u.data))] = true
			flows[u.a.ID] = true
		}
		if len(exact) < 2 || len(flows) < lc.cfg.Settings.DuplicateMinFlows {
			continue // identical ones are duplicate-script
		}
		ids := make([]string, 0, len(flows))
		for id := range flows {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, u := range uses {
			others := slicesWithout(ids, u.a.ID)
			f := u.a.finding("similar-script", u.file, "", "", path.Base(u.file),
				fmt.Sprintf("%s has the same code as scripts in %s, apart from comments and formatting", path.Base(u.file), listIDs(others, 5)),
				"unify the versions and move the script to a script collection")
			f.Related = others
			out = append(out, f)
		}
	}
	return out
}

var mappingExt = map[string]bool{".mmap": true, ".xsl": true, ".xslt": true}

func ruleDuplicateMappings(lc *lintContext) []Finding {
	type use struct {
		a    *lintArtifact
		file string
	}
	groups := map[string][]use{}
	for _, a := range lc.artifacts {
		if a.Type != "Integration" {
			continue
		}
		for _, f := range a.Files {
			if !mappingExt[strings.ToLower(path.Ext(f))] {
				continue
			}
			data, err := readArtifactFile(a, f)
			if err != nil {
				continue
			}
			k := hashOf(normalizeScript(data))
			groups[k] = append(groups[k], use{a, f})
		}
	}
	var out []Finding
	for _, uses := range groups {
		flows := map[string]bool{}
		for _, u := range uses {
			flows[u.a.ID] = true
		}
		if len(flows) < lc.cfg.Settings.DuplicateMinFlows {
			continue
		}
		ids := make([]string, 0, len(flows))
		for id := range flows {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, u := range uses {
			others := slicesWithout(ids, u.a.ID)
			kind := "message mapping"
			if path.Ext(u.file) != ".mmap" {
				kind = "XSLT mapping"
			}
			f := u.a.finding("duplicate-mapping", u.file, "", "", path.Base(u.file),
				fmt.Sprintf("the %s %s is identical in %d flows (also in %s)", kind, path.Base(u.file), len(ids), listIDs(others, 5)),
				"make it one message mapping artifact in the package (or a shared package) and reference it from the flows")
			f.Related = others
			out = append(out, f)
		}
	}
	return out
}

func ruleMissingCollection(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	seen := map[string]bool{}
	for _, s := range scriptSteps(a) {
		if s.bundle == "" || seen[s.bundle] {
			continue
		}
		seen[s.bundle] = true
		if c := lc.byID[s.bundle]; c != nil && c.Type == "ScriptCollection" {
			continue
		}
		out = append(out, a.finding("missing-script-collection", s.file, s.el.ID, s.el.Name, s.bundle,
			fmt.Sprintf("the script collection %s is referenced but not in the repository", s.bundle),
			"snapshot the package that holds it, or fix the reference"))
	}
	return out
}

// --- partner directory -----------------------------------------------------

var reLiteralCondition = regexp.MustCompile(`\$\{(header|property)\.([\w.\-]+)\}\s*(?:=|==|in)\s*'([^']*)'`)

func ruleRouterLiterals(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	eachElement(a, func(file string, m *iflow.Model, el *iflow.Element) {
		if el.Tag != "exclusiveGateway" {
			return
		}
		values := map[string]map[string]bool{}
		for _, id := range el.Outgoing {
			f := m.Flow(id)
			if f == nil {
				continue
			}
			cond := f.Condition
			if cond == "" {
				cond = f.Props["expression"]
			}
			for _, mm := range reLiteralCondition.FindAllStringSubmatch(cond, -1) {
				k := mm[1] + "." + mm[2]
				if values[k] == nil {
					values[k] = map[string]bool{}
				}
				values[k][mm[3]] = true
			}
		}
		for k, vals := range values {
			if len(vals) < lc.cfg.Settings.RouterMinValues {
				continue
			}
			out = append(out, a.finding("router-literals", file, el.ID, el.Name, k,
				fmt.Sprintf("the router %q decides on %d literal values of %s", el.Name, len(vals), k),
				"keep the routing table in the Partner Directory (one PID per value: receiver address, credentials, ...) and route with one dynamic receiver; a new partner is then data, not a new route"))
		}
	})
	return out
}

var reMapEntry = regexp.MustCompile(`["']([^"'\n]{1,80})["']\s*:\s*["']([^"'\n]*)["']`)
var reCaseLiteral = regexp.MustCompile(`case\s+["']([^"'\n]+)["']\s*:`)

func ruleLookupTables(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	for _, file := range sortedKeys(a.Scripts) {
		if scriptLanguage(file) == "" {
			continue
		}
		data := a.Scripts[file]
		n := len(reMapEntry.FindAll(data, -1))
		c := len(reCaseLiteral.FindAll(data, -1))
		if max(n, c) < lc.cfg.Settings.LookupMinEntries {
			continue
		}
		what := fmt.Sprintf("%d literal map entries", n)
		if c > n {
			what = fmt.Sprintf("%d literal case branches", c)
		}
		out = append(out, a.finding("lookup-table-in-script", file, "", "", path.Base(file),
			fmt.Sprintf("%s holds a lookup table (%s)", path.Base(file), what),
			"move the table to a value mapping (code lists) or the Partner Directory (partner-specific values) so it changes without a deployment"))
	}
	return out
}

func ruleDeploymentCopies(lc *lintContext) []Finding {
	var out []Finding
	keys := make([]string, 0, len(lc.opts.DeploymentCopies))
	for k := range lc.opts.DeploymentCopies {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		copies := lc.opts.DeploymentCopies[k]
		if len(copies) < 2 {
			continue
		}
		var src *lintArtifact
		for _, a := range lc.artifacts {
			if a.Rel == k {
				src = a
			}
		}
		if src == nil {
			continue
		}
		var ids []string
		overrides := map[string]bool{}
		for _, c := range copies {
			ids = append(ids, c.ID)
			for _, o := range c.Overrides {
				overrides[o] = true
			}
		}
		f := src.finding("deployment-copies", "", "", "", k,
			fmt.Sprintf("deployed %d times (%s), differing in configOverrides %s", len(copies), listIDs(ids, 5), strings.Join(sortedKeys(overrides), ", ")),
			"deploy it once and read the per-partner values from the Partner Directory (PID from a header or the sender): one deployment, partners as data")
		f.Related = ids
		out = append(out, f)
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- dead weight -------------------------------------------------------------

func ruleUnconnected(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	for _, file := range sortedModelFiles(a) {
		for _, el := range a.Models[file].Unreachable() {
			f := a.finding("unconnected-step", file, el.ID, el.Name, el.ID,
				fmt.Sprintf("%s %q cannot be reached from the start event", el.Kind(), el.Name),
				"remove it, or connect it if it is missing from the flow")
			f.fix = &lintFix{kind: fixRemoveElement, model: file, element: el.ID}
			out = append(out, f)
		}
	}
	return out
}

func ruleUnusedScripts(lc *lintContext, a *lintArtifact) []Finding {
	used := map[string]bool{}
	for _, s := range scriptSteps(a) {
		if s.bundle == "" {
			used[s.script] = true
		}
	}
	var out []Finding
	for _, file := range sortedKeys(a.Scripts) {
		name := path.Base(file)
		if used[scriptRef(file)] || scriptLanguage(file) == "" {
			continue
		}
		// a script may be loaded by another script
		referenced := false
		for other, data := range a.Scripts {
			if other != file && strings.Contains(string(data), strings.TrimSuffix(name, path.Ext(name))) {
				referenced = true
			}
		}
		if referenced {
			continue
		}
		f := a.finding("unused-script", file, "", "", name, name+" is not used by any script step", "delete it")
		f.fix = &lintFix{kind: fixDeleteFile, file: file}
		out = append(out, f)
	}
	return out
}

var resourceKinds = map[string]bool{".mmap": true, ".xsl": true, ".xslt": true, ".xsd": true, ".wsdl": true, ".edmx": true, ".json": true, ".opmap": true}

func ruleUnusedResources(lc *lintContext, a *lintArtifact) []Finding {
	var others strings.Builder
	others.WriteString(a.ModelText)
	for _, d := range a.Scripts {
		others.Write(d)
	}
	text := others.String()
	var out []Finding
	for _, file := range a.Files {
		ext := strings.ToLower(path.Ext(file))
		if !resourceKinds[ext] || strings.Contains(file, "/scenarioflows/") {
			continue
		}
		name := path.Base(file)
		stem := strings.TrimSuffix(name, path.Ext(name))
		if strings.Contains(text, name) || strings.Contains(text, "/"+stem) || strings.Contains(text, ">"+stem+"<") {
			continue
		}
		// schemas imported by other resources
		usedElsewhere := false
		for _, o := range a.Files {
			if o == file || !resourceKinds[strings.ToLower(path.Ext(o))] {
				continue
			}
			if data, err := readArtifactFile(a, o); err == nil && strings.Contains(string(data), name) {
				usedElsewhere = true
				break
			}
		}
		if usedElsewhere {
			continue
		}
		out = append(out, a.finding("unused-resource", file, "", "", name, name+" is not referenced by the model, a script or another resource", "delete it if nothing outside the flow needs it"))
	}
	return out
}

func ruleUnusedParameters(lc *lintContext, a *lintArtifact) []Finding {
	if len(a.Params) == 0 {
		return nil
	}
	var text strings.Builder
	text.WriteString(a.ModelText)
	for _, f := range a.Files {
		if strings.HasSuffix(f, "parameters.prop") || strings.HasSuffix(f, "parameters.propdef") {
			continue
		}
		if data, err := readArtifactFile(a, f); err == nil && len(data) < 4<<20 {
			text.Write(data)
		}
	}
	all := text.String()
	var out []Finding
	for _, key := range sortedKeys(a.Params) {
		if strings.Contains(all, "{{"+key+"}}") {
			continue
		}
		out = append(out, a.finding("unused-parameter", ops.ResourcesDir+"/parameters.prop", "", "", key,
			"the parameter "+key+" is not used", "remove it from parameters.prop and parameters.propdef (and from configure files)"))
	}
	return out
}

func isContentModifier(el *iflow.Element) bool {
	return el.Kind() == "Enricher" || el.Props["activityType"] == "Enricher"
}

func emptyTable(v string) bool {
	return strings.TrimSpace(v) == "" || !strings.Contains(v, "<row")
}

func ruleNoopModifier(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	eachElement(a, func(file string, m *iflow.Model, el *iflow.Element) {
		if !isContentModifier(el) || !emptyTable(el.Props["headerTable"]) || !emptyTable(el.Props["propertyTable"]) || strings.TrimSpace(el.Props["wrapContent"]) != "" {
			return
		}
		f := a.finding("noop-content-modifier", file, el.ID, el.Name, el.ID,
			fmt.Sprintf("the content modifier %q sets no header, property or body", el.Name), "remove it")
		if len(el.Incoming) == 1 && len(el.Outgoing) == 1 {
			f.fix = &lintFix{kind: fixRemoveElement, model: file, element: el.ID, reconnect: true}
		}
		out = append(out, f)
	})
	return out
}

func rulePropertyNeverRead(lc *lintContext, a *lintArtifact) []Finding {
	var scripts strings.Builder
	for _, d := range a.Scripts {
		scripts.Write(d)
	}
	text := a.ModelText + scripts.String()
	var out []Finding
	seen := map[string]bool{}
	eachElement(a, func(file string, m *iflow.Model, el *iflow.Element) {
		if !isContentModifier(el) {
			return
		}
		for _, name := range ops.TableNames(el.Props["propertyTable"]) {
			if seen[name] {
				continue
			}
			seen[name] = true
			if strings.Contains(text, "property."+name) || strings.Contains(text, `"`+name+`"`) || strings.Contains(text, `'`+name+`'`) ||
				strings.Count(text, ">"+name+"<") > 1 {
				continue
			}
			out = append(out, a.finding("property-never-read", file, el.ID, el.Name, name,
				"the exchange property "+name+" is set but never read in this flow", "remove it"))
		}
	})
	return out
}

// --- simplify ----------------------------------------------------------------

func ruleConsecutiveModifiers(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	eachElement(a, func(file string, m *iflow.Model, el *iflow.Element) {
		if !isContentModifier(el) {
			return
		}
		next := m.Next(el)
		if next == nil || !isContentModifier(next) || len(next.Incoming) != 1 {
			return
		}
		out = append(out, a.finding("consecutive-content-modifiers", file, el.ID, el.Name, el.ID+">"+next.ID,
			fmt.Sprintf("the content modifiers %q and %q follow each other", el.Name, next.Name),
			"merge them into one (unless the second reads what the first sets)"))
	})
	return out
}

func ruleConverterRoundtrip(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	eachElement(a, func(file string, m *iflow.Model, el *iflow.Element) {
		k := el.Kind()
		if k != "XmlToJsonConverter" && k != "JsonToXmlConverter" {
			return
		}
		next := m.Next(el)
		if next == nil {
			return
		}
		nk := next.Kind()
		if (k == "XmlToJsonConverter" && nk == "JsonToXmlConverter") || (k == "JsonToXmlConverter" && nk == "XmlToJsonConverter") {
			out = append(out, a.finding("converter-roundtrip", file, el.ID, el.Name, el.ID,
				fmt.Sprintf("%q converts and %q converts back right after", el.Name, next.Name), "remove both, or the step between them is missing"))
		}
	})
	return out
}

var reSetterLine = regexp.MustCompile(`^\s*(message\.)?set(Header|Property)\s*\(\s*["'][^"']*["']\s*,\s*["'][^"']*["']\s*\)\s*;?\s*$`)
var reBoilerplate = regexp.MustCompile(`^\s*(import\s|def\s+Message\s+processData|Message\s+processData|return\s+message|[{}]\s*$|$)`)

func ruleTrivialScripts(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	for _, file := range sortedKeys(a.Scripts) {
		if scriptLanguage(file) != "groovy" {
			continue
		}
		code := reComment.ReplaceAllString(string(a.Scripts[file]), "")
		setters, other := 0, 0
		for _, l := range strings.Split(code, "\n") {
			switch {
			case reSetterLine.MatchString(l):
				setters++
			case reBoilerplate.MatchString(l):
			default:
				other++
			}
		}
		if setters > 0 && other == 0 {
			out = append(out, a.finding("trivial-script", file, "", "", path.Base(file),
				fmt.Sprintf("%s only sets %d %s to literal values", path.Base(file), setters, plural(setters, "header or property", "headers or properties")),
				"use a content modifier instead"))
		}
	}
	return out
}

func ruleLargeScripts(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	for _, file := range sortedKeys(a.Scripts) {
		if scriptLanguage(file) == "" {
			continue
		}
		n := strings.Count(string(a.Scripts[file]), "\n") + 1
		if n <= lc.cfg.Settings.LargeScriptLines {
			continue
		}
		out = append(out, a.finding("large-script", file, "", "", path.Base(file),
			fmt.Sprintf("%s has %d lines", path.Base(file), n), "split it into functions in a script collection, or use standard steps for parts of it"))
	}
	return out
}

// --- robustness --------------------------------------------------------------

func ruleNoExceptionSubprocess(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	for _, file := range sortedModelFiles(a) {
		m := a.Models[file]
		for _, id := range m.Order {
			el := m.Elements[id]
			if el.Tag != "process" || !strings.Contains(el.Kind(), "IntegrationProcess") || strings.Contains(el.Kind(), "Local") {
				continue
			}
			has := false
			for _, oid := range m.Order {
				o := m.Elements[oid]
				if o.Container == el.ID && o.Tag == "subProcess" && (o.TriggeredByEvent || strings.Contains(o.Kind(), "Exception") || strings.Contains(o.Kind(), "ErrorEventSubProcess")) {
					has = true
				}
			}
			if !has {
				out = append(out, a.finding("no-exception-subprocess", file, el.ID, el.Name, el.ID,
					fmt.Sprintf("the integration process %q has no exception subprocess", el.Name),
					"add the house exception subprocess (error logging, alerting, response to the sender)"))
			}
		}
	}
	return out
}

var reEmptyCatch = regexp.MustCompile(`catch\s*\([^)]*\)\s*\{\s*(//[^\n]*\s*)*\}`)

func ruleSwallowedExceptions(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	for _, file := range sortedKeys(a.Scripts) {
		data := a.Scripts[file]
		for _, loc := range reEmptyCatch.FindAllIndex(data, -1) {
			line := lineOf(data, loc[0])
			out = append(out, a.finding("swallowed-exception", file, "", "", strconv.Itoa(line),
				fmt.Sprintf("%s line %d catches an exception and does nothing", path.Base(file), line),
				"rethrow it (the exception subprocess handles it) or handle it explicitly"))
		}
	}
	return out
}

// --- performance and data protection ------------------------------------------

var reBodyAsString = regexp.MustCompile(`getBody\s*\(\s*(java\.lang\.)?String(\.class)?\s*\)`)
var rePayloadAttachment = regexp.MustCompile(`addAttachmentAs(String|Binary)\s*\([^\n]*(body|Body|getBody)`)
var reLoop = regexp.MustCompile(`\.(each|eachWithIndex|collect|forEach)\s*\{|\bfor\s*\(|\bwhile\s*\(`)
var rePrintln = regexp.MustCompile(`\b(println|System\.out\.print)`)

func scriptFindings(a *lintArtifact, rule string, re *regexp.Regexp, msg, suggestion string) []Finding {
	var out []Finding
	for _, file := range sortedKeys(a.Scripts) {
		if scriptLanguage(file) == "" {
			continue
		}
		data := a.Scripts[file]
		if loc := re.FindIndex(data); loc != nil {
			n := len(re.FindAllIndex(data, -1))
			line := lineOf(data, loc[0])
			out = append(out, a.finding(rule, file, "", "", path.Base(file),
				fmt.Sprintf("%s line %d%s: %s", path.Base(file), line, map[bool]string{true: fmt.Sprintf(" (and %d more)", n-1), false: ""}[n > 1], msg), suggestion))
		}
	}
	return out
}

func ruleBodyAsString(lc *lintContext, a *lintArtifact) []Finding {
	return scriptFindings(a, "body-as-string", reBodyAsString, "reads the whole body into memory as a string",
		"for large payloads read it as a stream (getBody(java.io.Reader)) or use standard steps")
}

func rulePayloadAttachment(lc *lintContext, a *lintArtifact) []Finding {
	return scriptFindings(a, "payload-attachment", rePayloadAttachment, "writes the payload into the message processing log",
		"log payloads only on errors or behind a switch (externalised parameter or log level); they hold business data and slow the tenant")
}

func ruleLoggingInLoop(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	for _, file := range sortedKeys(a.Scripts) {
		data := string(a.Scripts[file])
		for _, loc := range reLoop.FindAllStringIndex(data, -1) {
			// the loop body: up to the matching brace
			start := strings.Index(data[loc[0]:], "{")
			if start < 0 {
				continue
			}
			depth, end := 0, len(data)
			for i := loc[0] + start; i < len(data); i++ {
				if data[i] == '{' {
					depth++
				} else if data[i] == '}' {
					depth--
					if depth == 0 {
						end = i
						break
					}
				}
			}
			if strings.Contains(data[loc[0]:end], "addAttachmentAs") {
				line := lineOf([]byte(data), loc[0])
				out = append(out, a.finding("logging-in-loop", file, "", "", strconv.Itoa(line),
					fmt.Sprintf("%s line %d writes log attachments inside a loop", path.Base(file), line),
					"collect the information and write one attachment after the loop"))
				break
			}
		}
	}
	return out
}

func rulePrintln(lc *lintContext, a *lintArtifact) []Finding {
	return scriptFindings(a, "println", rePrintln, "prints to standard output, which nobody reads on the tenant",
		"remove it, or use the message processing log")
}

// --- configuration -----------------------------------------------------------

var networkAdapters = map[string]bool{"HTTP": true, "HTTPS": true, "SOAP": true, "OData": true, "ODataV4": true, "HCIOData": true, "IDOC": true, "IDoc": true,
	"SFTP": true, "FTP": true, "Mail": true, "AS2": true, "REST": true, "SuccessFactors": true, "Ariba": true, "AMQP": true, "Kafka": true, "RFC": true}

func ruleHardcodedEndpoints(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	for _, file := range sortedModelFiles(a) {
		for _, ad := range a.Models[file].Adapters {
			if ad.Props["direction"] != "Receiver" {
				continue
			}
			adapter := ad.Props["ComponentType"]
			if !networkAdapters[adapter] {
				continue
			}
			for _, k := range []string{"httpAddressWithoutQuery", "address", "host", "url", "path", "directory"} {
				v := strings.TrimSpace(ad.Props[k])
				if v == "" || strings.Contains(v, "{{") || strings.Contains(v, "${") {
					continue
				}
				if k == "path" || k == "directory" {
					if adapter != "SFTP" && adapter != "FTP" {
						continue
					}
				}
				out = append(out, a.finding("hardcoded-endpoint", file, ad.ID, ad.Name, k,
					fmt.Sprintf("the %s receiver %q has the fixed %s %s", adapter, ad.Name, k, v),
					"externalise it ({{parameter}}) or read it from the Partner Directory, so dev, test and prod differ only in configuration"))
				break
			}
		}
	}
	return out
}

var reURL = regexp.MustCompile(`["']https?://[^"'\s]+["']`)
var reNamespaceURL = regexp.MustCompile(`(?i)(xmlns|namespace|w3\.org|xml\.org|schemas\.|sap\.com/xi|purl\.org|json-schema)`)

func ruleURLsInScripts(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	for _, file := range sortedKeys(a.Scripts) {
		if scriptLanguage(file) == "" {
			continue
		}
		lines := strings.Split(string(a.Scripts[file]), "\n")
		for i, l := range lines {
			u := reURL.FindString(l)
			if u == "" || reNamespaceURL.MatchString(l) {
				continue
			}
			out = append(out, a.finding("hardcoded-url-in-script", file, "", "", strconv.Itoa(i+1),
				fmt.Sprintf("%s line %d contains the URL %s", path.Base(file), i+1, strings.Trim(u, `"'`)),
				"use an externalised parameter or the Partner Directory"))
			break
		}
	}
	return out
}

var reSecret = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|apikey|api_key|client_secret|token)\b\s*[=:]\s*["']([^"'\s]{4,})["']`)

var (
	reSecretRefKey = regexp.MustCompile(`(?i)(_ref|_alias|_name|credential|_id)$`)
	reIdentifier   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.\-]*$`)
)

// reAddressKey matches parameter names of addresses: TokenURL, Token_Uri,
// TokenEndpoint, TokenPath, TokenHost.
var reAddressKey = regexp.MustCompile(`(?i)(url|uri|endpoint|path|host|address)$`)

// secretQueryParams are query parameters that carry a secret.
var secretQueryParams = map[string]bool{"token": true, "access_token": true, "signature": true, "sig": true, "key": true,
	"apikey": true, "api_key": true, "password": true, "pwd": true, "secret": true, "client_secret": true, "code": true}

// urlSecret reports whether v is an absolute URL and whether it carries a
// secret: a password in the user info or a secret query parameter.
func urlSecret(v string) (isURL, carries bool) {
	u, err := url.Parse(v)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false, false
	}
	if _, ok := u.User.Password(); ok {
		return true, true
	}
	for k := range u.Query() {
		if secretQueryParams[strings.ToLower(k)] {
			return true, true
		}
	}
	return true, false
}

func ruleSecrets(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	for _, file := range sortedKeys(a.Scripts) {
		data := a.Scripts[file]
		for _, loc := range reSecret.FindAllSubmatchIndex(data, -1) {
			line := lineOf(data, loc[0])
			out = append(out, a.finding("hardcoded-secret", file, "", "", strconv.Itoa(line),
				fmt.Sprintf("%s line %d assigns a literal to %s", path.Base(file), line, string(data[loc[2]:loc[3]])),
				"store it as a secure parameter or credential and read it with the SecureStoreService"))
		}
	}
	for _, key := range sortedKeys(a.Params) {
		lk := strings.ToLower(key)
		v := strings.TrimSpace(a.Params[key])
		if v == "" || strings.HasPrefix(v, "{{") {
			continue
		}
		// the name of a credential or secure parameter, not the secret itself
		if reSecretRefKey.MatchString(key) && reIdentifier.MatchString(v) {
			continue
		}
		// an address (CI_ODataAPI_TokenURL = https://.../oauth/token) is
		// not a secret unless it carries one
		if isURL, carries := urlSecret(v); isURL || reAddressKey.MatchString(key) {
			if carries {
				out = append(out, a.finding("hardcoded-secret", ops.ResourcesDir+"/parameters.prop", "", "", key,
					"the parameter "+key+" holds a URL with credentials (user info or a secret query parameter)", "use a credential name instead of the value"))
			}
			continue
		}
		for _, s := range []string{"password", "secret", "apikey", "api_key", "token"} {
			if strings.Contains(lk, s) {
				out = append(out, a.finding("hardcoded-secret", ops.ResourcesDir+"/parameters.prop", "", "", key,
					"the parameter "+key+" holds a value that looks like a secret", "use a credential name instead of the value"))
				break
			}
		}
	}
	return out
}

// --- hygiene -----------------------------------------------------------------

func compareVersionStrings(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
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
	return 0
}

func ruleOutdatedComponents(lc *lintContext) []Finding {
	latest := map[string]string{}
	kindOf := func(el *iflow.Element) string {
		if el.Tag == "messageFlow" {
			return "adapter " + el.Props["ComponentType"] + " " + el.Props["direction"]
		}
		return el.Kind()
	}
	visit := func(fn func(a *lintArtifact, file string, el *iflow.Element)) {
		for _, a := range lc.artifacts {
			if a.Type != "Integration" {
				continue
			}
			for _, file := range sortedModelFiles(a) {
				m := a.Models[file]
				for _, id := range m.Order {
					fn(a, file, m.Elements[id])
				}
				for _, ad := range m.Adapters {
					fn(a, file, ad)
				}
			}
		}
	}
	visit(func(a *lintArtifact, file string, el *iflow.Element) {
		v := el.Version()
		if v == "" {
			return
		}
		k := kindOf(el)
		if cur, ok := latest[k]; !ok || compareVersionStrings(v, cur) > 0 {
			latest[k] = v
		}
	})
	var out []Finding
	visit(func(a *lintArtifact, file string, el *iflow.Element) {
		uri := el.Props["cmdVariantUri"]
		if offered, ok := tenantVersion(lc, uri); ok {
			// the catalogue knows what the tenant offers: compare with that
			v := catalog.Version(uri)
			if v == "" || catalog.CompareVersions(v, offered) >= 0 || (el.Kind() == "GroovyScript" && majorOf(v) < 2 && majorOf(offered) >= 2) {
				return
			}
			out = append(out, a.finding("outdated-component", file, el.ID, el.Name, el.ID,
				fmt.Sprintf("%s %q has version %s; the tenant offers %s", kindOf(el), el.Name, v, offered),
				"update the step in the Web UI editor (newer versions fix bugs and add options)"))
			return
		}
		v := el.Version()
		k := kindOf(el)
		if v == "" || compareVersionStrings(v, latest[k]) >= 0 {
			return
		}
		if k == "GroovyScript" && majorOf(v) < 2 && majorOf(latest[k]) >= 2 {
			return // a migration, not an update: prefer-script-v2
		}
		out = append(out, a.finding("outdated-component", file, el.ID, el.Name, el.ID,
			fmt.Sprintf("%s %q has version %s; other flows use %s", k, el.Name, v, latest[k]),
			"update the step in the Web UI editor (newer versions fix bugs and add options)"))
	})
	return out
}

// tenantVersion is the version of a variant in the component catalogue's
// editor metadata (what the tenant's editor offers).
func tenantVersion(lc *lintContext, uri string) (string, bool) {
	if lc.opts.Components == nil || uri == "" {
		return "", false
	}
	return lc.opts.Components.Offered(uri)
}

func ruleComponentNewerThanTenant(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	for _, file := range sortedModelFiles(a) {
		m := a.Models[file]
		els := make([]*iflow.Element, 0, len(m.Order)+len(m.Adapters))
		for _, id := range m.Order {
			els = append(els, m.Elements[id])
		}
		els = append(els, m.Adapters...)
		for _, el := range els {
			uri := el.Props["cmdVariantUri"]
			if lc.opts.Components == nil || uri == "" {
				continue
			}
			offered, offeredURI, ok := lc.opts.Components.OfferedURI(uri)
			if v := catalog.Version(uri); !ok || v == "" || offered == "" || catalog.CompareVersions(v, offered) <= 0 {
				continue
			}
			out = append(out, a.finding("component-newer-than-tenant", file, el.ID, el.Name, el.ID,
				fmt.Sprintf("%s %q uses version %s; the tenant offers %s", el.Kind(), el.Name, catalog.Version(uri), offered),
				fmt.Sprintf("use %s (cpictl components list), or update the component catalogue if the tenant was upgraded", offeredURI)))
		}
	}
	return out
}

func majorOf(v string) int {
	n, _ := strconv.Atoi(strings.SplitN(v, ".", 2)[0])
	return n
}

// rulePreferScriptV2 points at Groovy script steps of version 1: version 2
// runs Groovy 4 with the com.sap.it.script.v2.api API (package script.v2,
// files under script/v2/).
func rulePreferScriptV2(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	for _, s := range scriptSteps(a) {
		v := s.el.Version()
		if s.el.Kind() != "GroovyScript" || v == "" || majorOf(v) >= 2 {
			continue
		}
		out = append(out, a.finding("prefer-script-v2", s.file, s.el.ID, s.el.Name, s.el.ID,
			fmt.Sprintf("Groovy script step %q is version %s (Groovy 2.4, com.sap.gateway.ip.core.customdev.util.Message)", s.el.Name, v),
			"for new and changed scripts use the Groovy script step 2.x: Groovy 4, package script.v2, import com.sap.it.script.v2.api.Message, file under script/v2/; "+
				"this is a rewrite of the script, not a version change"))
	}
	return out
}

var reDefaultName = regexp.MustCompile(`^(Groovy Script|JavaScript|Script|Content Modifier|Router|Message Mapping|XSLT Mapping|Mapping|Filter|Splitter|General Splitter|Iterating Splitter|Gather|Aggregator|Request Reply|Send|Converter|XML to JSON Converter|JSON to XML Converter|Encoder|Decoder|Write|Select|Process Call|Looping Process Call|Local Integration Process|Exception Subprocess|Write Variables|Persist|Data Store Operations|Multicast|Join)\s*\d+$`)

func ruleDefaultNames(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	eachElement(a, func(file string, m *iflow.Model, el *iflow.Element) {
		if el.Name != "" && reDefaultName.MatchString(strings.TrimSpace(el.Name)) {
			out = append(out, a.finding("default-step-name", file, el.ID, el.Name, el.ID,
				fmt.Sprintf("the step %q has its default name", el.Name), "name it after what it does"))
		}
	})
	return out
}

func ruleNaming(lc *lintContext, a *lintArtifact) []Finding {
	pattern := lc.cfg.Naming.IFlowID
	if pattern == "" {
		return nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil || re.MatchString(a.ID) {
		return nil
	}
	return []Finding{a.finding("naming", "", "", "", a.ID,
		fmt.Sprintf("the ID %s does not match %s", a.ID, pattern), "rename it with iflow copy (a new ID) when it is next changed")}
}

func ruleLayout(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	for _, file := range sortedModelFiles(a) {
		data, err := readArtifactFile(a, file)
		if err != nil {
			continue
		}
		issues, err := iflow.CheckLayout(data)
		if err != nil || len(issues) == 0 {
			continue
		}
		count := map[string]int{}
		var related []string
		for _, i := range issues {
			count[i.Kind]++
			if i.Element != "" && len(related) < 10 && !slices.Contains(related, i.Element) {
				related = append(related, i.Element)
			}
		}
		var parts []string
		for _, k := range []string{iflow.IssueMissing, iflow.IssueOverlap, iflow.IssueOutside, iflow.IssueCrossing, iflow.IssueCramped} {
			if n := count[k]; n > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", n, k))
			}
		}
		f := a.finding("layout", file, "", "", file,
			fmt.Sprintf("diagram: %s (%s)", strings.Join(parts, ", "), issues[0].Message),
			"cpictl iflow layout "+filepath.ToSlash(filepath.Join(a.Rel, file))+" (or lint --fix --fix-rules layout); only the diagram changes")
		f.Related = related
		f.fix = &lintFix{kind: fixLayout, model: file}
		out = append(out, f)
	}
	return out
}
