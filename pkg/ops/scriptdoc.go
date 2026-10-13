package ops

import (
	"regexp"
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
}

// ScriptDoc is what a script says about itself.
type ScriptDoc struct {
	// Header is the comment at the top of the file (before the first
	// import or declaration), without comment markers.
	Header    string           `json:"header,omitempty"`
	Summary   string           `json:"summary,omitempty"`
	Functions []ScriptFunction `json:"functions,omitempty"`
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
		d.Functions = append(d.Functions, f)
	}
	d.Summary = summaryOf(d.Header)
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
