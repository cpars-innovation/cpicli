package lint

import (
	"encoding/xml"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// Rules on what scripts say about themselves and on the tests of script
// collection scripts. A script collection is a library: other flows call its
// functions, so each function says what it does and how to call it, and
// tests cover it.

// collectionRules are the artifact rules that check script collections (the
// others check integration flows).
var collectionRules = map[string]bool{"collection-script-doc": true, "collection-script-tests": true, "collection-script-coverage": true,
	"hardcoded-secret": true, "hardcoded-url-in-script": true, "swallowed-exception": true, "println": true}

// collectionScriptFiles are the Groovy scripts of a script collection, sorted.
func collectionScriptFiles(a *lintArtifact) []string {
	var files []string
	for f := range a.Scripts {
		if scriptLanguage(f) == "groovy" {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	return files
}

func publicFunctions(doc ops.ScriptDoc) []ops.ScriptFunction {
	var out []ops.ScriptFunction
	for _, f := range doc.Functions {
		if !f.Private {
			out = append(out, f)
		}
	}
	return out
}

func ruleCollectionScriptDoc(lc *lintContext, a *lintArtifact) []Finding {
	if a.Type != "ScriptCollection" {
		return nil
	}
	var out []Finding
	for _, f := range collectionScriptFiles(a) {
		var missing []string
		for _, fn := range publicFunctions(ops.ParseScriptDoc(a.Scripts[f])) {
			if !fn.Documented() {
				missing = append(missing, fn.Name)
			}
		}
		if len(missing) == 0 {
			continue
		}
		out = append(out, a.finding("collection-script-doc", f, "", "", strings.Join(missing, ","),
			fmt.Sprintf("%s: %s without a doc comment: %s", scriptRef(f), plural(len(missing), "function", "functions"), strings.Join(missing, ", ")),
			"put a /** */ comment above each function: what it does (first sentence, used by discovery and graph_search), what it reads and "+
				"writes (headers, properties, body), errors it throws, and how to call it (script collection, file, function)"))
	}
	return out
}

func ruleScriptDoc(lc *lintContext, a *lintArtifact) []Finding {
	if a.Type != "Integration" {
		return nil
	}
	var out []Finding
	seen := map[string]bool{}
	for _, s := range scriptSteps(a) {
		if s.bundle != "" || s.script == "" || seen[s.script] {
			continue
		}
		seen[s.script] = true
		file := ops.ResourcesDir + "/script/" + s.script
		data, ok := a.Scripts[file]
		if !ok || scriptLanguage(file) != "groovy" {
			continue
		}
		doc := ops.ParseScriptDoc(data)
		entry := s.el.Props["scriptFunction"]
		if entry == "" {
			entry = "processData"
		}
		documented := doc.Header != ""
		for _, fn := range doc.Functions {
			if fn.Name == entry && fn.Documented() {
				documented = true
			}
		}
		if documented {
			continue
		}
		out = append(out, a.finding("script-doc", file, s.el.ID, s.el.Name, s.script,
			fmt.Sprintf("script %s says nothing about what it does", s.script),
			"add a comment at the top or above "+entry+": what the step does and what it reads and writes"))
	}
	return out
}

// testsDir is the folder with the tests of a script collection.
func (lc *lintContext) testsDir(a *lintArtifact) string {
	return filepath.Join(lc.cfg.path(lc.cfg.ScriptTests.Dir), filepath.FromSlash(a.Rel))
}

// testSources are the test files below dir (Groovy and Java), by name.
func testSources(dir string) map[string]string {
	out := map[string]string{}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".groovy", ".java":
			if data, err := os.ReadFile(p); err == nil {
				rel, _ := filepath.Rel(dir, p)
				out[filepath.ToSlash(rel)] = string(data)
			}
		}
		return nil
	})
	return out
}

func ruleCollectionScriptTests(lc *lintContext, a *lintArtifact) []Finding {
	if a.Type != "ScriptCollection" {
		return nil
	}
	dir := lc.testsDir(a)
	rel := filepath.ToSlash(filepath.Join(lc.cfg.ScriptTests.Dir, a.Rel))
	tests := testSources(dir)
	var out []Finding
	for _, f := range collectionScriptFiles(a) {
		fns := publicFunctions(ops.ParseScriptDoc(a.Scripts[f]))
		if len(fns) == 0 {
			continue
		}
		var untested []string
		for _, fn := range fns {
			re := regexp.MustCompile(`\b` + regexp.QuoteMeta(fn.Name) + `\s*\(`)
			found := false
			for _, src := range tests {
				if re.MatchString(src) {
					found = true
					break
				}
			}
			if !found {
				untested = append(untested, fn.Name)
			}
		}
		if len(untested) == 0 {
			continue
		}
		msg := fmt.Sprintf("%s: no test calls %s", scriptRef(f), strings.Join(untested, ", "))
		if len(tests) == 0 {
			msg = fmt.Sprintf("%s has no tests (%s is empty or missing)", scriptRef(f), rel)
		}
		out = append(out, a.finding("collection-script-tests", f, "", "", strings.Join(untested, ","), msg,
			fmt.Sprintf("write tests in %s (e.g. %sTest.groovy with JUnit or Spock) that call every function with normal and edge-case "+
				"messages; run them with JaCoCo so collection-script-coverage can check the coverage", rel, strings.TrimSuffix(path.Base(f), path.Ext(f)))))
	}
	return out
}

// jacocoReport is the part of a JaCoCo XML report the coverage rule reads.
type jacocoReport struct {
	Packages []struct {
		Name    string `xml:"name,attr"`
		Sources []struct {
			Name     string `xml:"name,attr"`
			Counters []struct {
				Type    string `xml:"type,attr"`
				Missed  int    `xml:"missed,attr"`
				Covered int    `xml:"covered,attr"`
			} `xml:"counter"`
		} `xml:"sourcefile"`
	} `xml:"package"`
}

// lineCoverage is the line coverage of the report by source file name.
type lineCoverage struct {
	byFile map[string][]float64
	err    error
}

var coverageCache sync.Map // report path -> *lineCoverage (per process; tests use their own paths)

func readCoverage(file string) *lineCoverage {
	info, statErr := os.Stat(file)
	key := file
	if statErr == nil {
		key = fmt.Sprintf("%s@%d", file, info.ModTime().UnixNano())
	}
	if c, ok := coverageCache.Load(key); ok {
		return c.(*lineCoverage)
	}
	c := &lineCoverage{byFile: map[string][]float64{}}
	data, err := os.ReadFile(file)
	if err != nil {
		c.err = err
	} else {
		var r jacocoReport
		// JaCoCo reports reference a DTD; the decoder does not fetch it
		dec := xml.NewDecoder(strings.NewReader(string(data)))
		dec.Strict = false
		if err := dec.Decode(&r); err != nil {
			c.err = err
		}
		for _, p := range r.Packages {
			for _, s := range p.Sources {
				for _, ct := range s.Counters {
					if ct.Type == "LINE" && ct.Missed+ct.Covered > 0 {
						c.byFile[s.Name] = append(c.byFile[s.Name], 100*float64(ct.Covered)/float64(ct.Missed+ct.Covered))
					}
				}
			}
		}
	}
	coverageCache.Store(key, c)
	return c
}

func ruleCollectionScriptCoverage(lc *lintContext, a *lintArtifact) []Finding {
	if a.Type != "ScriptCollection" || len(collectionScriptFiles(a)) == 0 {
		return nil
	}
	st := lc.cfg.ScriptTests
	report := lc.cfg.path(st.CoverageReport)
	cov := readCoverage(report)
	if cov.err != nil {
		return []Finding{a.finding("collection-script-coverage", "", "", "", "no-report",
			fmt.Sprintf("coverage not measured: no readable JaCoCo report at %s", filepath.ToSlash(st.CoverageReport)),
			fmt.Sprintf("run the tests in %s with JaCoCo before lint (or set scriptTests.coverageReport in .cpi/lint.yaml)", filepath.ToSlash(st.Dir)))}
	}
	reportInfo, _ := os.Stat(report)
	var out []Finding
	for _, f := range collectionScriptFiles(a) {
		name := path.Base(f)
		values := cov.byFile[name]
		if info, err := os.Stat(filepath.Join(a.Dir, filepath.FromSlash(f))); err == nil && reportInfo != nil && info.ModTime().After(reportInfo.ModTime()) {
			out = append(out, a.finding("collection-script-coverage", f, "", "", "stale",
				fmt.Sprintf("%s changed after the coverage report was written: its coverage is unknown", scriptRef(f)),
				"run the tests again, then lint"))
			continue
		}
		switch {
		case len(values) == 0:
			out = append(out, a.finding("collection-script-coverage", f, "", "", "not-in-report",
				fmt.Sprintf("%s is not in the coverage report (no test loads it)", scriptRef(f)),
				"add the script collection's scripts to the test sources and test them"))
		case len(values) > 1:
			// several scripts with this name in the report: not attributable
		case values[0] < st.MinCoverage:
			out = append(out, a.finding("collection-script-coverage", f, "", "", "below",
				fmt.Sprintf("%s: line coverage %.0f%% (minimum %.0f%%)", scriptRef(f), values[0], st.MinCoverage),
				"add tests for the uncovered branches (the JaCoCo HTML report shows them line by line)"))
		}
	}
	return out
}
