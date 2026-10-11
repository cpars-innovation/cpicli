package ops

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/pdrepo"
)

// PDReference is a literal Partner Directory reference of an artifact.
type PDReference struct {
	Pid string `json:"pid"` // "" when only the key is literal (Groovy)
	ID  string `json:"id"`
	// Kind is Binary or String ("" when unknown, e.g. Groovy getParameter).
	Kind     string `json:"kind,omitempty"`
	Artifact string `json:"artifact"`
	Package  string `json:"package,omitempty"`
	File     string `json:"file"`
	// Step is the id of the .iflw element that holds the reference.
	Step string `json:"step,omitempty"`
}

// PDDynamicReference is a reference whose PID or ID is computed at runtime.
type PDDynamicReference struct {
	Artifact   string `json:"artifact"`
	Package    string `json:"package,omitempty"`
	File       string `json:"file"`
	Step       string `json:"step,omitempty"`
	Expression string `json:"expression"`
}

// PDDependencies lists which artifacts read which Partner Directory
// parameters.
type PDDependencies struct {
	References []PDReference        `json:"references"`
	Dynamic    []PDDynamicReference `json:"dynamic"`
	// UnknownPids are referenced PIDs without a local directory under the
	// resources path (only when one was given).
	UnknownPids []string `json:"unknownPids,omitempty"`
}

var (
	rePDLiteral = regexp.MustCompile(`pd:([A-Za-z0-9_]+):([A-Za-z0-9_.-]+):(Binary|String)`)
	rePDDynamic = regexp.MustCompile(`pd:[^\s"'<>]*\$\{[^\s"'<>]*`)
	// PartnerDirectoryService.getParameter(id, pid, type)
	reGroovyParam = regexp.MustCompile(`getParameter\(\s*["']([^"']+)["']\s*,\s*(?:["']([^"']+)["']|[^,)]+)`)
	reIflwElement = regexp.MustCompile(`<bpmn2:(?:callActivity|serviceTask|messageFlow|subProcess|startEvent|endEvent|intermediateCatchEvent|intermediateThrowEvent|exclusiveGateway|participant)\b[^>]*\bid="([^"]+)"`)
)

// PDDependenciesFilter narrows the result.
type PDDependenciesFilter struct {
	Pid, ID string
	// ResourcesPath is the local Partner Directory tree used for UnknownPids.
	ResourcesPath string
}

// FindPDDependencies scans local content (the layout of sync / discover
// --dir) for Partner Directory references: pd:<PID>:<ID>:<Binary|String> in
// .iflw models, dynamic pd:${...} references, and literal arguments of
// getParameter(id, pid, ...) in Groovy scripts. Nothing is read from the
// tenant.
func FindPDDependencies(ctx context.Context, dir string, f PDDependenciesFilter) (*PDDependencies, error) {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, output.Usagef("%s is not a directory", dir)
	}
	res := &PDDependencies{References: []PDReference{}, Dynamic: []PDDynamicReference{}}
	var walkErr error
	err := WalkLocalArtifacts(ctx, dir, func(a LocalArtifact) {
		_ = filepath.WalkDir(a.Dir, func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(p))
			if ext != ".iflw" && ext != ".groovy" && ext != ".gsh" {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				walkErr = err
				return nil
			}
			rel, _ := filepath.Rel(dir, p)
			rel = filepath.ToSlash(rel)
			text := string(data)
			if ext == ".iflw" {
				scanIflw(res, a, rel, text)
			} else {
				for _, m := range reGroovyParam.FindAllStringSubmatch(text, -1) {
					res.References = append(res.References, PDReference{Pid: m[2], ID: m[1], Artifact: a.ID, Package: a.PackageID, File: rel})
				}
			}
			return nil
		})
	}, func(err error) { walkErr = err })
	if err != nil {
		return nil, err
	}
	if walkErr != nil {
		return nil, walkErr
	}

	res.References = slices.DeleteFunc(res.References, func(r PDReference) bool {
		return (f.Pid != "" && r.Pid != f.Pid) || (f.ID != "" && r.ID != f.ID)
	})
	sort.Slice(res.References, func(i, j int) bool {
		a, b := res.References[i], res.References[j]
		return a.Pid+"\x00"+a.ID+"\x00"+a.Artifact+"\x00"+a.File < b.Pid+"\x00"+b.ID+"\x00"+b.Artifact+"\x00"+b.File
	})
	if f.ResourcesPath != "" {
		local, err := pdrepo.New(f.ResourcesPath).GetLocalPIDs()
		if err != nil {
			return nil, output.Usagef("cannot read %s: %v", f.ResourcesPath, err)
		}
		for _, r := range res.References {
			if r.Pid != "" && !slices.Contains(local, r.Pid) && !slices.Contains(res.UnknownPids, r.Pid) {
				res.UnknownPids = append(res.UnknownPids, r.Pid)
			}
		}
		sort.Strings(res.UnknownPids)
	}
	return res, nil
}

func scanIflw(res *PDDependencies, a LocalArtifact, rel, text string) {
	elements := reIflwElement.FindAllStringSubmatchIndex(text, -1)
	stepAt := func(offset int) string {
		step := ""
		for _, e := range elements {
			if e[0] > offset {
				break
			}
			step = text[e[2]:e[3]]
		}
		return step
	}
	for _, m := range rePDLiteral.FindAllStringSubmatchIndex(text, -1) {
		res.References = append(res.References, PDReference{Pid: text[m[2]:m[3]], ID: text[m[4]:m[5]], Kind: text[m[6]:m[7]],
			Artifact: a.ID, Package: a.PackageID, File: rel, Step: stepAt(m[0])})
	}
	for _, m := range rePDDynamic.FindAllStringIndex(text, -1) {
		res.Dynamic = append(res.Dynamic, PDDynamicReference{Artifact: a.ID, Package: a.PackageID, File: rel, Step: stepAt(m[0]), Expression: text[m[0]:m[1]]})
	}
}
