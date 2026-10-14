package lint

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/cpars-innovation/cpicli/pkg/iflow"
)

// The header contract between flows: headers cross ProcessDirect, and a
// called flow that tests a header against null treats an empty header as
// set. A caller that sets the header to "" where it used to leave it absent
// changes the callee's behaviour.

var (
	reRow  = regexp.MustCompile(`(?s)<row>(.*?)</row>`)
	reCell = regexp.MustCompile(`(?s)<cell id=['"]([^'"]+)['"]>(.*?)</cell>`)
	// setHeader("X", "") or setHeader("X", '')
	reSetEmptyHeader = regexp.MustCompile(`setHeader\(\s*["']([^"'$]+)["']\s*,\s*(?:""|'')\s*\)`)
)

// tableRows returns the rows of a Content Modifier table as cell id ->
// value.
func tableRows(table string) []map[string]string {
	var out []map[string]string
	for _, row := range reRow.FindAllStringSubmatch(table, -1) {
		cells := map[string]string{}
		for _, c := range reCell.FindAllStringSubmatch(row[1], -1) {
			cells[c[1]] = strings.TrimSpace(c[2])
		}
		out = append(out, cells)
	}
	return out
}

// emptyHeaders lists the headers a flow sets to a literal empty value: a
// Content Modifier row of type constant without value, or setHeader with "".
func emptyHeaders(a *lintArtifact) map[string]string {
	out := map[string]string{} // header -> where
	eachElement(a, func(file string, m *iflow.Model, el *iflow.Element) {
		if !isContentModifier(el) {
			return
		}
		for _, r := range tableRows(el.Props["headerTable"]) {
			if r["Name"] != "" && !strings.EqualFold(r["Action"], "Delete") && strings.EqualFold(r["Type"], "constant") && r["Value"] == "" {
				out[r["Name"]] = fmt.Sprintf("content modifier %q", el.Name)
			}
		}
	})
	for _, file := range sortedKeys(a.Scripts) {
		for _, m := range reSetEmptyHeader.FindAllStringSubmatch(string(a.Scripts[file]), -1) {
			if _, ok := out[m[1]]; !ok {
				out[m[1]] = "script " + scriptRef(file)
			}
		}
	}
	return out
}

// testsAgainstNull reports where a flow tests a header against null:
// getHeader("X") != null (directly or through a variable) in a script, or
// ${header.X} = null / != null in a condition.
func testsAgainstNull(a *lintArtifact, header string) string {
	q := regexp.QuoteMeta(header)
	read := `(?:getHeader\(\s*["']` + q + `["']\s*(?:,[^)]*)?\)|headers?\s*(?:\.get\(\s*["']` + q + `["']\s*\)|\[\s*["']` + q + `["']\s*\]))`
	direct := regexp.MustCompile(read + `\s*[!=]=\s*null|null\s*[!=]=\s*` + read)
	assign := regexp.MustCompile(`(?:def|String|Object|var)\s+(\w+)\s*=\s*[\w.()]*?` + read)
	for _, file := range sortedKeys(a.Scripts) {
		text := string(a.Scripts[file])
		if direct.MatchString(text) {
			return "script " + scriptRef(file)
		}
		for _, m := range assign.FindAllStringSubmatch(text, -1) {
			v := regexp.QuoteMeta(m[1])
			if regexp.MustCompile(`\b` + v + `\s*[!=]=\s*null|null\s*[!=]=\s*` + v + `\b`).MatchString(text) {
				return "script " + scriptRef(file)
			}
		}
	}
	simple := regexp.MustCompile(`\$\{header\.` + q + `\}\s*(?:=|==|!=)\s*null`)
	for _, file := range sortedModelFiles(a) {
		m := a.Models[file]
		for _, f := range m.Flows {
			if simple.MatchString(f.Condition) {
				if el := m.Elements[f.Source]; el != nil {
					return fmt.Sprintf("the condition of %q", el.Name)
				}
				return "a condition"
			}
		}
	}
	return ""
}

// processDirect lists the literal ProcessDirect addresses a flow sends to
// (receivers) or listens on (senders).
func processDirect(a *lintArtifact, direction string) []string {
	var out []string
	for _, file := range sortedModelFiles(a) {
		for _, ad := range a.Models[file].Adapters {
			if ad.Props["ComponentType"] == "ProcessDirect" && ad.Props["direction"] == direction {
				if addr := strings.TrimSpace(ad.Props["address"]); addr != "" && !strings.Contains(addr, "${") && !strings.Contains(addr, "{{") {
					out = append(out, addr)
				}
			}
		}
	}
	return out
}

// ruleHeaderEmptyVsAbsent: a flow sets a header to "" and calls, through
// ProcessDirect, a flow that tests the header against null.
func ruleHeaderEmptyVsAbsent(lc *lintContext) []Finding {
	listeners := map[string][]*lintArtifact{}
	for _, a := range lc.artifacts {
		for _, addr := range processDirect(a, "Sender") {
			listeners[addr] = append(listeners[addr], a)
		}
	}
	var out []Finding
	for _, caller := range lc.artifacts {
		empty := emptyHeaders(caller)
		if len(empty) == 0 {
			continue
		}
		for _, addr := range processDirect(caller, "Receiver") {
			for _, callee := range listeners[addr] {
				names := make([]string, 0, len(empty))
				for h := range empty {
					names = append(names, h)
				}
				sort.Strings(names)
				for _, h := range names {
					where := testsAgainstNull(callee, h)
					if where == "" {
						continue
					}
					f := caller.finding("header-empty-vs-absent", "", "", "", addr+"|"+h,
						fmt.Sprintf("%s sets the header %s to an empty value, and %s (ProcessDirect %s) tests it against null in %s: an empty header is not null, so the callee treats it as set",
							empty[h], h, callee.ID, addr, where),
						"leave the header absent (no row, or remove it) when there is no value, or make the callee test for empty as well")
					f.Related = []string{callee.ID}
					out = append(out, f)
				}
			}
		}
	}
	return out
}
