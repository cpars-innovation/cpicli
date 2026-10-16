package lint

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/beevik/etree"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/iflow"
)

type lintFixKind int

const (
	fixToCollection lintFixKind = iota + 1
	fixDeleteFile
	fixRemoveElement
	fixLayout
	fixFlowRefs
	fixScriptProps
)

// lintFix is what --fix does for a finding.
type lintFix struct {
	kind lintFixKind
	// fixToCollection: the script (file of the flow) goes to collection
	// (in collectionPackage; empty: the flow's package) as targetName.
	collectionPackage, collection, script, scriptHash, targetName string
	// fixDeleteFile
	file string
	// fixRemoveElement: model file and element ID; reconnect joins the
	// incoming and outgoing sequence flow.
	model, element string
	reconnect      bool
}

// FixOptions select the fixes.
type FixOptions struct {
	// Rules to fix (empty: the safe defaults; "all": every fixable rule).
	Rules  []string
	DryRun bool
}

// FixChange is one applied (or, in a dry run, planned) change.
type FixChange struct {
	Rule     string `json:"rule"`
	Artifact string `json:"artifact"`
	Path     string `json:"path"`
	Action   string `json:"action"`
}

// FixResult lists the changes.
type FixResult struct {
	DryRun  bool        `json:"dryRun,omitempty"`
	Changes []FixChange `json:"changes"`
	// Collections created or extended (package/ID) and artifacts changed
	// (paths): they need a version bump, an upload and a deployment, the
	// collections first.
	Collections []string `json:"collections"`
	Changed     []string `json:"changed"`
	NextSteps   []string `json:"nextSteps"`
}

// Run checks the artifacts of a content tree (see runLint).
func Run(ctx context.Context, o Options) (*Result, error) {
	res, _, err := runLint(ctx, o)
	return res, err
}

// Fix applies the fixable findings of the selected rules to the local
// files. Nothing is sent to a tenant.
func Fix(ctx context.Context, o Options, fo FixOptions) (*FixResult, error) {
	rules := map[string]bool{}
	switch {
	case len(fo.Rules) == 0:
		rules = fixByDefault
	case len(fo.Rules) == 1 && fo.Rules[0] == "all":
		rules = fixableRules
	default:
		for _, r := range fo.Rules {
			if !fixableRules[r] {
				return nil, output.Usagef("rule %q has no fix (fixable: %s)", r, strings.Join(sortedKeys(fixableRules), ", "))
			}
			rules[r] = true
		}
	}
	o.Baseline = "" // fix known findings too
	res, lc, err := runLint(ctx, o)
	if err != nil {
		return nil, err
	}
	out := &FixResult{DryRun: fo.DryRun, Changes: []FixChange{}, Collections: []string{}, Changed: []string{}, NextSteps: []string{}}
	changed := map[string]bool{}
	collections := map[string]bool{}

	// per artifact, the edits of its models
	type artifactEdits struct {
		a       *lintArtifact
		scripts []Finding // to collection
		removes []Finding
		deletes []Finding
		layouts []Finding
		refs    []Finding
	}
	edits := map[string]*artifactEdits{}
	var order []string
	for _, f := range res.Findings {
		if f.fix == nil || !rules[f.Rule] {
			continue
		}
		a := lc.byID[f.Artifact]
		if a == nil {
			continue
		}
		e := edits[a.Rel]
		if e == nil {
			e = &artifactEdits{a: a}
			edits[a.Rel] = e
			order = append(order, a.Rel)
		}
		switch f.fix.kind {
		case fixToCollection:
			e.scripts = append(e.scripts, f)
		case fixRemoveElement:
			e.removes = append(e.removes, f)
		case fixDeleteFile:
			e.deletes = append(e.deletes, f)
		case fixLayout:
			e.layouts = append(e.layouts, f)
		case fixFlowRefs, fixScriptProps:
			e.refs = append(e.refs, f)
		}
	}
	sort.Strings(order)

	// script collections: content first, so that flows reference what exists
	written := map[string]string{} // collection dir + hash -> file name
	for _, rel := range order {
		e := edits[rel]
		for _, f := range e.scripts {
			fx := f.fix
			pkg := fx.collectionPackage
			if pkg == "" {
				pkg = e.a.PackageID
			}
			collDir := filepath.Join(lc.opts.Dir, filepath.FromSlash(pkg), fx.collection)
			name, err := placeInCollection(collDir, fx, e.a, written, fo.DryRun)
			if err != nil {
				return nil, err
			}
			fx.targetName = name
			key := pkg + "/" + fx.collection
			if !collections[key] {
				collections[key] = true
				out.Collections = append(out.Collections, key)
			}
			out.Changes = append(out.Changes, FixChange{Rule: f.Rule, Artifact: e.a.ID, Path: e.a.Rel,
				Action: fmt.Sprintf("%s -> script collection %s (%s); the local copy is deleted", path.Base(fx.script), key, name)})
		}
	}
	for _, rel := range order {
		e := edits[rel]
		// with the layout rule, models that lose steps are laid out too
		relayout := map[string]bool{}
		for _, f := range e.layouts {
			relayout[f.fix.model] = true
		}
		if rules["layout"] {
			for _, f := range e.removes {
				relayout[f.fix.model] = true
			}
		}
		docFixes := map[string][]func(*etree.Document) bool{}
		for _, f := range e.refs {
			fn := iflow.AddFlowReferences
			if f.fix.kind == fixScriptProps {
				fn = iflow.CompleteScriptSteps
			}
			docFixes[f.fix.model] = append(docFixes[f.fix.model], fn)
		}
		laidOut, err := applyModelEdits(e.a, e.scripts, e.removes, relayout, docFixes, lc.cfg.Layout, fo.DryRun)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rel, err)
		}
		for _, f := range e.refs {
			action := "sequence flow references added: "
			if f.fix.kind == fixScriptProps {
				action = "script step properties added: "
			}
			out.Changes = append(out.Changes, FixChange{Rule: f.Rule, Artifact: e.a.ID, Path: e.a.Rel, Action: action + f.fix.model})
		}
		for _, file := range laidOut {
			out.Changes = append(out.Changes, FixChange{Rule: "layout", Artifact: e.a.ID, Path: e.a.Rel, Action: "diagram laid out: " + file})
		}
		for _, f := range e.removes {
			action := fmt.Sprintf("removed %q (%s)", f.ElementName, f.Element)
			if f.fix.reconnect {
				action += ", its neighbours connected"
			}
			out.Changes = append(out.Changes, FixChange{Rule: f.Rule, Artifact: e.a.ID, Path: e.a.Rel, Action: action})
		}
		deleted := map[string]bool{}
		for _, f := range append(slicesClone(e.scripts), e.deletes...) {
			file := f.fix.file
			if f.fix.kind == fixToCollection {
				file = f.fix.script
			}
			if deleted[file] {
				continue
			}
			deleted[file] = true
			if !fo.DryRun {
				if err := os.Remove(filepath.Join(e.a.Dir, filepath.FromSlash(file))); err != nil && !os.IsNotExist(err) {
					return nil, err
				}
			}
			if f.fix.kind == fixDeleteFile {
				out.Changes = append(out.Changes, FixChange{Rule: f.Rule, Artifact: e.a.ID, Path: e.a.Rel, Action: "deleted " + file})
			}
		}
		if len(e.scripts)+len(e.removes)+len(e.deletes)+len(e.refs)+len(laidOut) > 0 && !changed[rel] {
			changed[rel] = true
			out.Changed = append(out.Changed, rel)
		}
	}
	if len(out.Collections) > 0 {
		out.NextSteps = append(out.NextSteps,
			"add the script collections to the deploy config and deploy them before the flows that use them ("+strings.Join(out.Collections, ", ")+")")
	}
	if len(out.Changed) > 0 {
		out.NextSteps = append(out.NextSteps,
			"raise the versions: cpictl version bump --changed",
			"review the diff, then upload, validate, deploy and test the changed flows on the development tenant")
	}
	return out, nil
}

func slicesClone(s []Finding) []Finding { return append([]Finding(nil), s...) }

const scriptCollectionManifest = `Manifest-Version: 1.0
Bundle-ManifestVersion: 2
Bundle-SymbolicName: %[1]s
Bundle-Name: %[1]s
Bundle-Version: 1.0.0
Origin-Bundle-SymbolicName: %[1]s
Origin-Bundle-Name: %[1]s
SAP-BundleType: ScriptCollection
SAP-NodeType: IFLMAP
Import-Package: org.osgi.framework
`

// placeInCollection puts the script into the collection folder (created when
// missing) and returns its name there: the existing file with the same
// content, else the script's name (with a hash suffix when that name holds
// other content).
func placeInCollection(collDir string, fx *lintFix, a *lintArtifact, written map[string]string, dryRun bool) (string, error) {
	data := a.Scripts[fx.script]
	key := collDir + "\x00" + fx.scriptHash
	if name, ok := written[key]; ok {
		return name, nil
	}
	scriptDir := filepath.Join(collDir, "src", "main", "resources", "script")
	if fx.targetName != "" {
		written[key] = fx.targetName
		return fx.targetName, nil
	}
	// same content already there?
	if entries, err := os.ReadDir(scriptDir); err == nil {
		for _, e := range entries {
			if d, err := os.ReadFile(filepath.Join(scriptDir, e.Name())); err == nil && hashOf(normalizeScript(d)) == fx.scriptHash {
				written[key] = e.Name()
				return e.Name(), nil
			}
		}
	}
	name := path.Base(fx.script)
	if existing, err := os.ReadFile(filepath.Join(scriptDir, name)); err == nil && hashOf(normalizeScript(existing)) != fx.scriptHash {
		ext := path.Ext(name)
		name = strings.TrimSuffix(name, ext) + "_" + fx.scriptHash[:6] + ext
	}
	written[key] = name
	if dryRun {
		return name, nil
	}
	if _, err := os.Stat(filepath.Join(collDir, "META-INF", "MANIFEST.MF")); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Join(collDir, "META-INF"), 0o755); err != nil {
			return "", err
		}
		id := filepath.Base(collDir)
		if err := os.WriteFile(filepath.Join(collDir, "META-INF", "MANIFEST.MF"), []byte(fmt.Sprintf(scriptCollectionManifest, id)), 0o644); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(collDir, "metainfo.prop"), []byte("description=Scripts shared by several integration flows (created by cpictl lint --fix)\n"), 0o644); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		return "", err
	}
	return name, os.WriteFile(filepath.Join(scriptDir, name), []byte(normalizeScript(data)+"\n"), 0o644)
}

// applyModelEdits rewrites script references to collections, removes
// elements and lays out the models in relayout; it returns the models laid
// out.
func applyModelEdits(a *lintArtifact, scripts, removes []Finding, relayout map[string]bool, docFixes map[string][]func(*etree.Document) bool, layout iflow.LayoutOptions, dryRun bool) ([]string, error) {
	byModel := map[string][]Finding{}
	for _, f := range removes {
		byModel[f.fix.model] = append(byModel[f.fix.model], f)
	}
	var laidOut []string
	models := sortedModelFiles(a)
	for _, file := range models {
		if len(scripts) == 0 && len(byModel[file]) == 0 && !relayout[file] && len(docFixes[file]) == 0 {
			continue
		}
		p := filepath.Join(a.Dir, filepath.FromSlash(file))
		doc := iflow.NewDocument()
		if err := doc.ReadFromFile(p); err != nil {
			return nil, err
		}
		touched := false
		for _, f := range scripts {
			if rewriteScriptRefs(doc, path.Base(f.fix.script), f.fix.collection, f.fix.targetName) {
				touched = true
			}
		}
		for _, f := range byModel[file] {
			if removeModelElement(doc, f.fix.element, f.fix.reconnect) {
				touched = true
			}
		}
		for _, fn := range docFixes[file] {
			if fn(doc) {
				touched = true
			}
		}
		if !touched && !relayout[file] {
			continue
		}
		data, err := doc.WriteToBytes()
		if err != nil {
			return nil, err
		}
		if relayout[file] {
			out, res, err := iflow.Layout(data, layout)
			if err != nil {
				return nil, err
			}
			if res.Changed {
				data, touched = out, true
				laidOut = append(laidOut, file)
			}
		}
		if touched && !dryRun {
			if err := os.WriteFile(p, data, 0o644); err != nil {
				return nil, err
			}
		}
	}
	return laidOut, nil
}

func walkXML(el *etree.Element, fn func(*etree.Element)) {
	fn(el)
	for _, c := range el.ChildElements() {
		walkXML(c, fn)
	}
}

// props returns the ifl:property key -> value element of an element.
func xmlProps(el *etree.Element) map[string]*etree.Element {
	out := map[string]*etree.Element{}
	for _, ext := range el.ChildElements() {
		if ext.Tag != "extensionElements" {
			continue
		}
		for _, p := range ext.ChildElements() {
			if p.Tag != "property" {
				continue
			}
			k, v := p.SelectElement("key"), p.SelectElement("value")
			if k != nil && v != nil {
				out[strings.TrimSpace(k.Text())] = v
			}
		}
	}
	return out
}

// rewriteScriptRefs points the local script steps using script at the
// collection.
func rewriteScriptRefs(doc *etree.Document, script, collection, target string) bool {
	touched := false
	walkXML(doc.Root(), func(el *etree.Element) {
		props := xmlProps(el)
		s, b := props["script"], props["scriptBundleId"]
		if s == nil || strings.TrimSpace(s.Text()) != script || (b != nil && strings.TrimSpace(b.Text()) != "") {
			return
		}
		s.SetText(target)
		if b == nil {
			// add the scriptBundleId property next to script
			ext := s.Parent().Parent()
			p := ext.CreateElement(s.Parent().Tag)
			p.Space = s.Parent().Space
			k := p.CreateElement("key")
			k.SetText("scriptBundleId")
			v := p.CreateElement("value")
			v.SetText(collection)
		} else {
			b.SetText(collection)
		}
		touched = true
	})
	return touched
}

// removeModelElement deletes an element, its diagram shape and its sequence
// flows; reconnect joins its single incoming and outgoing flow instead.
func removeModelElement(doc *etree.Document, id string, reconnect bool) bool {
	var target *etree.Element
	flows := map[string]*etree.Element{}
	shapes := map[string]*etree.Element{}
	byID := map[string]*etree.Element{}
	walkXML(doc.Root(), func(el *etree.Element) {
		if v := el.SelectAttrValue("id", ""); v != "" {
			byID[v] = el
			if v == id {
				target = el
			}
		}
		if el.Tag == "sequenceFlow" {
			flows[el.SelectAttrValue("id", "")] = el
		}
		if el.Tag == "BPMNShape" || el.Tag == "BPMNEdge" {
			shapes[el.SelectAttrValue("bpmnElement", "")] = el
		}
	})
	if target == nil {
		return false
	}
	var in, out []*etree.Element
	for _, f := range flows {
		if f.SelectAttrValue("targetRef", "") == id {
			in = append(in, f)
		}
		if f.SelectAttrValue("sourceRef", "") == id {
			out = append(out, f)
		}
	}
	dropRef := func(owner *etree.Element, tag, flowID string) {
		if owner == nil {
			return
		}
		for _, c := range owner.ChildElements() {
			if c.Tag == tag && strings.TrimSpace(c.Text()) == flowID {
				owner.RemoveChild(c)
			}
		}
	}
	remove := func(el *etree.Element) {
		if el != nil && el.Parent() != nil {
			el.Parent().RemoveChild(el)
		}
	}
	if reconnect && len(in) == 1 && len(out) == 1 {
		i, o := in[0], out[0]
		next := byID[o.SelectAttrValue("targetRef", "")]
		i.CreateAttr("targetRef", o.SelectAttrValue("targetRef", ""))
		if next != nil {
			for _, c := range next.ChildElements() {
				if c.Tag == "incoming" && strings.TrimSpace(c.Text()) == o.SelectAttrValue("id", "") {
					c.SetText(i.SelectAttrValue("id", ""))
				}
			}
		}
		remove(shapes[o.SelectAttrValue("id", "")])
		remove(o)
	} else {
		for _, f := range in {
			dropRef(byID[f.SelectAttrValue("sourceRef", "")], "outgoing", f.SelectAttrValue("id", ""))
			remove(shapes[f.SelectAttrValue("id", "")])
			remove(f)
		}
		for _, f := range out {
			dropRef(byID[f.SelectAttrValue("targetRef", "")], "incoming", f.SelectAttrValue("id", ""))
			remove(shapes[f.SelectAttrValue("id", "")])
			remove(f)
		}
	}
	remove(shapes[id])
	remove(target)
	return true
}
