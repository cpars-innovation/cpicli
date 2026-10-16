package ops

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// ScriptFunction is a top-level function of a Groovy script with its doc
// comment.
type ScriptFunction struct {
	Name string `json:"name"`
	// Params is the parameter list as written ("Message message").
	Params string `json:"params,omitempty"`
	// Line is the 1-based line of the declaration.
	Line int `json:"line"`
	// Private functions are helpers, not entry points.
	Private bool `json:"private,omitempty"`
	// Doc is the comment directly above the declaration (/** */, /* */ or
	// // lines), without comment markers; Summary is its first sentence.
	Doc     string `json:"doc,omitempty"`
	Summary string `json:"summary,omitempty"`
	// Tags are the @tags of the doc comment (template: docs/script-docs.md),
	// tag name without "@" -> values in order.
	Tags map[string][]string `json:"tags,omitempty"`
}

// ScriptDoc is what a script says about itself.
type ScriptDoc struct {
	// Header is the comment at the top of the file (before the first
	// import or declaration), without comment markers.
	Header    string              `json:"header,omitempty"`
	Summary   string              `json:"summary,omitempty"`
	Tags      map[string][]string `json:"tags,omitempty"`
	Functions []ScriptFunction    `json:"functions,omitempty"`
}

var (
	// a top-level declaration: column 0, modifiers, def and/or a return
	// type, the name, the parameters, the opening brace
	reGroovyFunc = regexp.MustCompile(`(?m)^((?:(?:public|protected|private|static|final|synchronized)\s+)*)(?:def\s+(?:[\w.]+(?:<[^>\n]*>)?(?:\[\])?\s+)?|([\w.]+(?:<[^>\n]*>)?(?:\[\])?)\s+)(\w+)\s*\(([^)]*)\)\s*(?:throws\s+[\w.,\s]+)?\{`)
	notTypes     = map[string]bool{"return": true, "new": true, "else": true, "throw": true, "if": true, "for": true, "while": true,
		"switch": true, "catch": true, "class": true, "interface": true, "enum": true, "import": true, "package": true, "assert": true}
	reSentenceEnd = regexp.MustCompile(`[.!?](\s|$)`)
)

// ParseScriptDoc reads the top-level functions of a Groovy script and their
// doc comments (functions inside classes are not listed).
func ParseScriptDoc(data []byte) ScriptDoc {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	var d ScriptDoc
	d.Header = headerComment(lines)
	for _, m := range reGroovyFunc.FindAllStringSubmatchIndex(text, -1) {
		mods := text[m[2]:m[3]]
		if m[4] >= 0 && notTypes[text[m[4]:m[5]]] {
			continue
		}
		name := text[m[6]:m[7]]
		if notTypes[name] {
			continue
		}
		line := strings.Count(text[:m[0]], "\n") + 1
		f := ScriptFunction{Name: name, Params: strings.Join(strings.Fields(text[m[8]:m[9]]), " "), Line: line,
			Private: strings.Contains(mods, "private")}
		f.Doc = commentAbove(lines, line-1)
		f.Summary = summaryOf(f.Doc)
		f.Tags = tagsOf(f.Doc)
		d.Functions = append(d.Functions, f)
	}
	d.Summary = summaryOf(d.Header)
	d.Tags = tagsOf(d.Header)
	if d.Summary == "" {
		for _, f := range d.Functions {
			if !f.Private && f.Summary != "" {
				d.Summary = f.Summary
				break
			}
		}
	}
	return d
}

// headerComment is the first comment of the file when it comes before any
// code (a comment directly above the first function is that function's).
func headerComment(lines []string) string {
	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i == len(lines) {
		return ""
	}
	t := strings.TrimSpace(lines[i])
	if !strings.HasPrefix(t, "/*") && !strings.HasPrefix(t, "//") {
		return ""
	}
	start := i
	var end int
	if strings.HasPrefix(t, "/*") {
		for end = i; end < len(lines) && !strings.Contains(lines[end], "*/"); end++ {
		}
		if end == len(lines) {
			return ""
		}
	} else {
		for end = i; end+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[end+1]), "//"); end++ {
		}
	}
	// directly followed by a declaration: the function's comment, not the file's
	next := end + 1
	for next < len(lines) && (strings.TrimSpace(lines[next]) == "" || strings.HasPrefix(strings.TrimSpace(lines[next]), "@")) {
		next++
	}
	if next < len(lines) && next-end <= 2 {
		if loc := reGroovyFunc.FindStringIndex(strings.Join(lines[next:min(next+2, len(lines))], "\n")); loc != nil && loc[0] == 0 {
			return ""
		}
	}
	return cleanComment(lines[start : end+1])
}

// commentAbove is the comment that ends right above line index i (blank lines
// and annotations in between are allowed).
func commentAbove(lines []string, i int) string {
	j := i - 1
	for j >= 0 && (strings.TrimSpace(lines[j]) == "" || strings.HasPrefix(strings.TrimSpace(lines[j]), "@")) {
		j--
	}
	if j < 0 {
		return ""
	}
	t := strings.TrimSpace(lines[j])
	switch {
	case strings.HasSuffix(t, "*/"):
		k := j
		for k >= 0 && !strings.Contains(lines[k], "/*") {
			k--
		}
		if k < 0 {
			return ""
		}
		return cleanComment(lines[k : j+1])
	case strings.HasPrefix(t, "//"):
		k := j
		for k-1 >= 0 && strings.HasPrefix(strings.TrimSpace(lines[k-1]), "//") {
			k--
		}
		return cleanComment(lines[k : j+1])
	}
	return ""
}

func cleanComment(lines []string) string {
	var out []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		t = strings.TrimPrefix(t, "/**")
		t = strings.TrimPrefix(t, "/*")
		t = strings.TrimSuffix(t, "*/")
		t = strings.TrimPrefix(t, "//")
		t = strings.TrimPrefix(strings.TrimSpace(t), "*")
		out = append(out, strings.TrimSpace(t))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// SummaryOf is the first sentence of a comment (tags and blank lines end
// it).
func SummaryOf(doc string) string { return summaryOf(doc) }

// summaryOf is the first sentence of a comment (tags and blank lines end
// it).
func summaryOf(doc string) string {
	var parts []string
	for _, l := range strings.Split(doc, "\n") {
		if l == "" || strings.HasPrefix(l, "@") {
			if len(parts) > 0 {
				break
			}
			continue
		}
		parts = append(parts, l)
	}
	s := strings.Join(parts, " ")
	if loc := reSentenceEnd.FindStringIndex(s); loc != nil {
		s = s[:loc[0]+1]
	}
	return strings.TrimSpace(s)
}

// Documented tells whether a doc comment says something (not only tags).
func (f ScriptFunction) Documented() bool { return f.Summary != "" }

// tagsOf reads the @tags of a comment: "@name text" starts a value, the
// lines after it continue it until the next tag or a blank line.
func tagsOf(doc string) map[string][]string {
	var tags map[string][]string
	var name string
	for _, l := range strings.Split(doc, "\n") {
		switch {
		case strings.HasPrefix(l, "@"):
			n, v, _ := strings.Cut(l[1:], " ")
			name = strings.ToLower(strings.TrimSpace(n))
			if name == "" {
				continue
			}
			if tags == nil {
				tags = map[string][]string{}
			}
			tags[name] = append(tags[name], strings.TrimSpace(v))
		case l == "":
			name = ""
		case name != "":
			vs := tags[name]
			vs[len(vs)-1] = strings.TrimSpace(vs[len(vs)-1] + " " + l)
		}
	}
	return tags
}

// Keywords are the comma-separated words of all @keywords tags of the file
// and its functions, in order, without duplicates.
func (d ScriptDoc) Keywords() []string {
	var out []string
	add := func(tags map[string][]string) {
		for _, v := range tags["keywords"] {
			for _, k := range strings.Split(v, ",") {
				if k = strings.TrimSpace(k); k != "" && !slices.Contains(out, k) {
					out = append(out, k)
				}
			}
		}
	}
	add(d.Tags)
	for _, f := range d.Functions {
		add(f.Tags)
	}
	return out
}

// text is everything the script's comments say (to check what they name).
func (d ScriptDoc) text() string {
	parts := []string{d.Header}
	for _, f := range d.Functions {
		parts = append(parts, f.Doc)
	}
	return strings.Join(parts, "\n")
}

var (
	reScriptAccess = regexp.MustCompile(`\b(get|set)(Header|Property)\(\s*["']([^"'$]+)["']`)
	rePlaceholder  = regexp.MustCompile(`(?i)\b(todo|tbd|fixme|xxx|lorem ipsum)\b|^(groovy |this )?script\.?$|^(description|n/a|none|test)\.?$|^\.+$`)
)

// ScriptAccess lists the headers and properties a script reads and writes
// by literal name ("header SAP_Date", "property orderId"), sorted.
func ScriptAccess(data []byte) (reads, writes []string) {
	for _, m := range reScriptAccess.FindAllSubmatch(data, -1) {
		name := strings.ToLower(string(m[2])) + " " + string(m[3])
		if string(m[1]) == "get" {
			reads = append(reads, name)
		} else {
			writes = append(writes, name)
		}
	}
	return sortedUnique(reads), sortedUnique(writes)
}

var reStructural = regexp.MustCompile(`(?s)/\*.*?\*/|(?m)//[^\n]*$|\s+`)

// ScriptShape identifies scripts that differ only in comments and
// formatting (sha256 of the code without comments and whitespace, 12 hex
// digits).
func ScriptShape(data []byte) string {
	sum := sha256.Sum256(reStructural.ReplaceAll(data, nil))
	return hex.EncodeToString(sum[:6])
}

// ScriptDocTemplate is the comment template for Groovy scripts: a first
// sentence that says what it does (discover and graph_search show it), then
// the tags. For a script of a script collection, every public function has
// one, @reads and @writes name every header and property the code reads or
// writes, and @keywords make it findable.
const ScriptDocTemplate = `/**
 * Formats the SAP date in header SAP_Date as ISO 8601 into header ISO_Date.
 *
 * Optional details: when to use it, edge cases, what it does not do.
 *
 * @reads    header SAP_Date (yyyyMMdd)
 * @reads    property dateFormat (optional, default yyyy-MM-dd)
 * @writes   header ISO_Date
 * @body     unchanged
 * @throws   IllegalArgumentException when SAP_Date is not yyyyMMdd
 * @keywords date, format, ISO 8601, SAP date
 * @example  script collection Common_Scripts, file DateUtils.groovy, function formatSapDate
 */
def Message formatSapDate(Message message) {`

// Script doc statuses.
const (
	DocOK         = "ok"
	DocIncomplete = "incomplete"
	DocMissing    = "missing"
)

// Script doc issue kinds.
const (
	DocIssueMissing      = "missing"      // nothing says what the script or function does
	DocIssuePlaceholder  = "placeholder"  // the first sentence says nothing (TODO, "Script.", too short)
	DocIssueUndocumented = "undocumented" // a header or property the code reads or writes is not named
	DocIssueNoKeywords   = "no-keywords"  // a script collection script without @keywords
)

// ScriptDocIssue is something a script's comments lack.
type ScriptDocIssue struct {
	Kind string `json:"kind"`
	// Function is empty for the file.
	Function string `json:"function,omitempty"`
	Message  string `json:"message"`
}

// PoorSummary tells whether a first sentence says nothing: empty, a
// placeholder, fewer than three words or only the function's name.
func PoorSummary(summary, function string) bool {
	s := strings.TrimSpace(summary)
	if s == "" || rePlaceholder.MatchString(s) || len(strings.Fields(s)) < 3 {
		return true
	}
	plain := strings.ToLower(strings.Trim(s, " ."))
	return function != "" && (plain == strings.ToLower(function) || strings.ReplaceAll(plain, " ", "") == strings.ToLower(function))
}

// CheckScriptDoc lists what the comments of a Groovy script lack.
//
// A flow script (collection false) needs a first sentence that says what it
// does, at the top of the file or above a public function. A script of a
// script collection is code for others to reuse, so each public function
// needs one, every header and property the code reads or writes by name has
// to be named in its comments (the @reads and @writes tags of the
// template), and @keywords make it findable.
func CheckScriptDoc(data []byte, collection bool) []ScriptDocIssue {
	doc := ParseScriptDoc(data)
	var out []ScriptDocIssue
	if !collection {
		if doc.Summary == "" {
			return []ScriptDocIssue{{Kind: DocIssueMissing, Message: "no comment says what the script does"}}
		}
		if PoorSummary(doc.Summary, "") {
			out = append(out, ScriptDocIssue{Kind: DocIssuePlaceholder, Message: fmt.Sprintf("the description %q says nothing", doc.Summary)})
		}
		return out
	}
	public := 0
	for _, f := range doc.Functions {
		if f.Private {
			continue
		}
		public++
		switch {
		case f.Summary == "":
			out = append(out, ScriptDocIssue{Kind: DocIssueMissing, Function: f.Name, Message: f.Name + " has no doc comment"})
		case PoorSummary(f.Summary, f.Name):
			out = append(out, ScriptDocIssue{Kind: DocIssuePlaceholder, Function: f.Name,
				Message: fmt.Sprintf("%s: the description %q says nothing", f.Name, f.Summary)})
		}
	}
	if public == 0 && doc.Summary == "" {
		out = append(out, ScriptDocIssue{Kind: DocIssueMissing, Message: "no comment says what the script does"})
	}
	text := doc.text()
	reads, writes := ScriptAccess(data)
	var missing []string
	for _, a := range append(reads, writes...) {
		if name := a[strings.Index(a, " ")+1:]; !strings.Contains(text, name) && !slices.Contains(missing, a) {
			missing = append(missing, a)
		}
	}
	if len(missing) > 0 {
		out = append(out, ScriptDocIssue{Kind: DocIssueUndocumented,
			Message: "the comments do not name " + strings.Join(missing, ", ") + " (use @reads / @writes)"})
	}
	if len(doc.Keywords()) == 0 {
		out = append(out, ScriptDocIssue{Kind: DocIssueNoKeywords, Message: "no @keywords: other flows find the script by them"})
	}
	return out
}

// DocStatusOf sums up the issues: missing when nothing is documented,
// incomplete when something is missing, else ok. A missing @keywords alone
// does not make it incomplete.
func DocStatusOf(issues []ScriptDocIssue, functions int) string {
	missing, other := 0, 0
	for _, i := range issues {
		switch i.Kind {
		case DocIssueMissing:
			missing++
		case DocIssueNoKeywords:
		default:
			other++
		}
	}
	switch {
	case missing > 0 && other == 0 && missing >= max(functions, 1):
		return DocMissing
	case missing+other > 0:
		return DocIncomplete
	}
	return DocOK
}
