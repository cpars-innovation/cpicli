package secretscan

import (
	"encoding/base64"
	"iter"
	"path/filepath"
	"regexp"
	"strings"
)

// Kinds found only in script lines.
const (
	// KindKeyMaterial is a literal used as a cryptographic key (SecretKeySpec,
	// PBEKeySpec, an HMAC key).
	KindKeyMaterial = "key-material"
	// KindRandomLiteral is a random-looking literal: a key or token in a
	// format no rule knows.
	KindRandomLiteral = "random-literal"
)

// contextWindow is how far LineSecret looks before and after a literal for
// its context: it keeps the check linear on long lines.
const contextWindow = 200

// script languages
type lang int

const (
	langGeneric lang = iota
	langGroovy
	langJS
	langJava
	langXSLT
)

var (
	// interpolation in a Groovy (or unknown language) double-quoted string
	reGroovyInterp = regexp.MustCompile(`\$[A-Za-z_{]`)
	// "Bearer <token>" or "Basic <credentials>" as a whole literal
	reAuthLiteral = regexp.MustCompile(`(?i)^\s*(basic|bearer)\s+([A-Za-z0-9+/=._~-]{6,})\s*$`)
	// user:password@ in a URL
	reURLUserInfo = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^/\s:@?#]+:([^/\s@?#]+)@`)
	// a query parameter that carries a secret
	reQuerySecret = regexp.MustCompile(`(?i)[?&](x-amz-signature|signature|sig|api[_-]?key|access[_-]?token|client[_-]?secret|password|pwd|secret|token|key|code)=([^&#\s"']*)`)
	// literals that are not secrets whatever their shape
	reHexOrUUID = regexp.MustCompile(`^[0-9a-fA-F-]+$`)
	reEnvName   = regexp.MustCompile(`^[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+$`)
	// the code before a literal that makes it key material
	reKeyCall  = regexp.MustCompile(`(?:SecretKeySpec|PBEKeySpec|[Mm]ac\.init)\s*\([\w.()\s]*$`)
	reHmacCall = regexp.MustCompile(`(?:\bcreateHmac\s*\(\s*["'` + "`" + `][\w-]*["'` + "`" + `]|\bHmac(?:SHA|MD)\w*\s*\([^,()"'` + "`" + `]*)\s*,[\w.()\s]*$`)
	// a method that sets a named value: setHeader, setProperty, put
	reSetter = regexp.MustCompile(`(?i)^(?:(?:set|put|add|with)\w*|header|append)$`)
	// identifier-like words: token_expired, pwd-field-name
	reWords = regexp.MustCompile(`^[a-z]+(?:[_.-][a-z]+)+$`)
	// a scheme before a named value ("Bearer abc123")
	reAuthScheme = regexp.MustCompile(`(?i)^(?:basic|bearer|token|apikey)\s+`)
	// header names that hold credentials
	reSecretHeader = regexp.MustCompile(`(?i)^(?:proxy-)?authorization$|^x-(?:api-key|auth-token)$`)
	// an XML comment
	reXMLComment = regexp.MustCompile(`<!--.*?-->`)
)

// placeholderWords are literals that stand for a secret without being one.
var placeholderWords = map[string]bool{"password": true, "passwd": true, "secret": true, "token": true, "apikey": true,
	"api_key": true, "example": true, "sample": true, "placeholder": true, "redacted": true, "todo": true, "test": true}

// xpathAttrs are XSLT attributes that hold an XPath expression.
var xpathAttrs = map[string]bool{"select": true, "test": true, "match": true, "use": true, "group-by": true,
	"group-adjacent": true, "value": true}

// literal is a string literal of a script line with the context that names it.
type literal struct {
	val       string
	start     int // index of the opening quote (code), of the tag or text (XSLT)
	end       int // index after the closing quote, the tag or the text
	interp    bool
	name      string // the secret-ish name it is assigned to, if any
	keyMatter bool   // passed as key material
}

// LineSecret reports whether one line of a script holds a hard-coded secret,
// and what kind. path is the file path (its extension decides the language;
// unknown extensions use the generic rules). Only string literals are judged:
// runtime credential code, identifiers and comments never are.
func LineSecret(path, line string) (kind string, hit bool) {
	if strings.Contains(line, "PRIVATE KEY") && rePrivateKey.MatchString(line) {
		return KindPrivateKey, true
	}
	l := languageOf(path)
	var lits []literal
	if l == langXSLT {
		lits = xsltLiterals(line)
	} else {
		lits = codeLiterals(line, l)
		for i := range lits {
			codeContext(line, lits, i)
		}
	}
	for _, lit := range lits {
		if lit.interp {
			continue
		}
		if k := judge(lit); k != "" {
			return k, true
		}
	}
	return "", false
}

func languageOf(path string) lang {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".groovy", ".gsh", ".gy", ".gvy":
		return langGroovy
	case ".js", ".mjs", ".cjs":
		return langJS
	case ".java":
		return langJava
	case ".xsl", ".xslt":
		return langXSLT
	}
	return langGeneric
}

// judge returns the kind of secret a literal is, or "".
func judge(lit literal) string {
	v := lit.val
	if strings.TrimSpace(v) == "" {
		return ""
	}
	// each regexp runs only when a cheap test says it can match: lines can be long
	if strings.Contains(v, "eyJ") && reJWT.MatchString(v) || knownTokenPrefix(v) && reKnownToken.MatchString(v) {
		return KindToken
	}
	if len(v) >= 12 {
		if m := reAuthLiteral.FindStringSubmatch(v); m != nil {
			if k := authLiteral(m[1], m[2]); k != "" {
				return k
			}
		}
	}
	if strings.Contains(v, "://") {
		if m := reURLUserInfo.FindStringSubmatch(v); m != nil && !placeholder(m[1]) && !strings.ContainsAny(m[1], "{<%") {
			return KindURL
		}
	}
	if strings.Contains(v, "=") && strings.ContainsAny(v, "?&") {
		for _, m := range reQuerySecret.FindAllStringSubmatch(v, -1) {
			if querySecret(strings.ToLower(m[1]), m[2]) {
				return KindURL
			}
		}
	}
	if lit.keyMatter && !placeholder(v) {
		return KindKeyMaterial
	}
	if lit.name != "" && namedValue(v) {
		return KindNamed
	}
	if randomLooking(v, 20) {
		return KindRandomLiteral
	}
	return ""
}

// knownTokenPrefix reports whether v contains a prefix reKnownToken knows.
func knownTokenPrefix(v string) bool {
	for _, p := range []string{"AKIA", "gh", "xox", "AIza", "sk_live_", "glpat-"} {
		if strings.Contains(v, p) {
			return true
		}
	}
	return false
}

// authLiteral judges "Bearer x" / "Basic x": a hit when the credentials
// contain a digit or a JWT/base64 sign, or Basic decodes to user:password.
func authLiteral(scheme, cred string) string {
	if placeholder(cred) {
		return ""
	}
	k := KindToken
	if strings.EqualFold(scheme, "basic") {
		k = KindCredentials
		if dec, err := base64.StdEncoding.DecodeString(cred); err == nil && strings.Contains(string(dec), ":") {
			return k
		}
	}
	if strings.ContainsAny(cred, "0123456789.+/=") {
		return k
	}
	return ""
}

// querySecret judges the value of a secret query parameter.
func querySecret(param, v string) bool {
	switch param {
	case "token", "key", "code":
		return randomLooking(v, 16)
	}
	return len(v) >= 8 && !placeholder(v) && !reEnvName.MatchString(v) && strings.ContainsAny(v, "0123456789%")
}

// namedValue reports whether a literal assigned to a secret name is a value
// rather than a placeholder, a reference or prose.
func namedValue(v string) bool {
	v = reAuthScheme.ReplaceAllString(strings.TrimSpace(v), "")
	return len(v) >= 4 && !placeholder(v) && !strings.ContainsAny(v, " \t") &&
		!reEnvName.MatchString(v) && !reWords.MatchString(v) && !strings.Contains(v, "://")
}

// placeholder reports whether v stands for a secret without being one.
func placeholder(v string) bool {
	l := strings.ToLower(strings.TrimSpace(v))
	return l == "" || rePlaceholder.MatchString(l) || placeholderWords[l] || strings.Contains(l, "${") ||
		strings.Contains(l, "{{") || strings.Contains(l, "%s") || strings.HasPrefix(l, "your")
}

// randomLooking reports whether v looks like random key material: at least
// min characters, no spaces, upper and lower case and digits (or base64
// signs) changing often, the way they do in generated keys and seldom in
// identifiers. UUIDs and hex digests are not.
func randomLooking(v string, min int) bool {
	if len(v) < min || reHexOrUUID.MatchString(v) || strings.HasPrefix(v, "/") || strings.Contains(v, "//") {
		return false
	}
	const (
		upper = iota + 1
		lower
		digit
	)
	var up, low, dig, b64 bool
	alnum, switches, prev := 0, 0, 0
	for i := 0; i < len(v); i++ {
		c, cls := v[i], 0
		switch {
		case c >= 'A' && c <= 'Z':
			up, cls = true, upper
		case c >= 'a' && c <= 'z':
			low, cls = true, lower
		case c >= '0' && c <= '9':
			dig, cls = true, digit
		case c == '+' || c == '/' || c == '=':
			b64 = true
		case c == '-' || c == '_':
		default:
			return false
		}
		if cls == 0 {
			continue
		}
		if prev != 0 && cls != prev {
			switches++
		}
		prev = cls
		alnum++
	}
	if alnum < min*3/4 || float64(switches) < 0.35*float64(alnum-1) {
		return false
	}
	return up && low && (dig || (b64 && len(v) >= 24))
}

// codeLiterals extracts the string literals of a Groovy, JavaScript or Java
// line, stopping at a line comment.
func codeLiterals(line string, l lang) []literal {
	var out []literal
	for i := 0; i < len(line); {
		c := line[i]
		switch {
		case c == '/' && i+1 < len(line) && line[i+1] == '/':
			return out
		case c == '/' && i+1 < len(line) && line[i+1] == '*':
			j := strings.Index(line[i+2:], "*/")
			if j < 0 {
				return out
			}
			i += j + 4
		case c == '"' || c == '\'' || (c == '`' && l == langJS):
			q := line[i : i+1]
			triple := q + q + q
			if l != langJS && c != '`' && strings.HasPrefix(line[i:], triple) {
				j := strings.Index(line[i+3:], triple)
				if j < 0 {
					return out // a multi-line string: the rest of the line is inside it
				}
				out = append(out, newLiteral(line[i+3:i+3+j], c, l, i, i+6+j))
				i += j + 6
				continue
			}
			j := closingQuote(line, i+1, c)
			if j < 0 {
				return out
			}
			out = append(out, newLiteral(line[i+1:j], c, l, i, j+1))
			i = j + 1
		default:
			i++
		}
	}
	return out
}

func closingQuote(line string, from int, q byte) int {
	for j := from; j < len(line); j++ {
		switch line[j] {
		case '\\':
			j++
		case q:
			return j
		}
	}
	return -1
}

func newLiteral(v string, quote byte, l lang, start, end int) literal {
	interp := strings.Contains(v, "${")
	if quote == '"' && (l == langGroovy || l == langGeneric) && strings.Contains(v, "$") && reGroovyInterp.MatchString(v) {
		interp = true
	}
	return literal{val: v, start: start, end: end, interp: interp}
}

// codeContext sets the name and key-material context of lits[i] from the
// code around it.
func codeContext(line string, lits []literal, i int) {
	lit := &lits[i]
	before := line[max(0, lit.start-contextWindow):lit.start]
	// cheap guards first: the window regexps are not anchored at the start
	if (strings.Contains(before, "KeySpec") || strings.Contains(before, "ac.init")) && reKeyCall.MatchString(before) ||
		strings.Contains(before, "Hmac") && reHmacCall.MatchString(before) {
		lit.keyMatter = true
	}
	after := strings.TrimSpace(line[lit.end:min(len(line), lit.end+contextWindow)])
	if !wholeValueEnd(after) {
		return // part of an expression: a concatenation, a call, a comparison
	}
	b := strings.TrimRight(before, " \t")
	// setHeader("Authorization", "...") and setProperty("password", "...")
	if i > 0 && strings.TrimSpace(line[lits[i-1].end:lit.start]) == "," && !lits[i-1].interp {
		call := strings.TrimRight(line[max(0, lits[i-1].start-contextWindow):lits[i-1].start], " \t")
		if strings.HasSuffix(call, "(") && reSetter.MatchString(trailingIdent(strings.TrimRight(call[:len(call)-1], " \t"))) &&
			secretName(lits[i-1].val) {
			lit.name = lits[i-1].val
		}
		return
	}
	if b == "" {
		return
	}
	op := b[len(b)-1]
	if op != '=' && op != ':' {
		return
	}
	b = b[:len(b)-1]
	if op == '=' && b != "" && strings.ContainsRune("=!<>+-*/%&|^?~", rune(b[len(b)-1])) {
		return // a comparison or a compound assignment
	}
	if op == ':' && strings.HasSuffix(b, "?") {
		return // a ternary or an Elvis default
	}
	if n := assignedName(strings.TrimRight(b, " \t")); n != "" && secretName(n) {
		lit.name = n
	}
}

// assignedName returns the name at the end of b: name, "name", ['name'].
func assignedName(b string) string {
	if strings.HasSuffix(b, "]") {
		b = strings.TrimRight(b[:len(b)-1], " \t")
		if q := quotedName(b); q != "" && strings.HasSuffix(strings.TrimRight(b[:len(b)-len(q)-2], " \t"), "[") {
			return q
		}
		return ""
	}
	if q := quotedName(b); q != "" {
		return q
	}
	return trailingIdent(b)
}

// quotedName returns the quoted name at the end of b, or "".
func quotedName(b string) string {
	if b == "" || !strings.ContainsRune("\"'`", rune(b[len(b)-1])) {
		return ""
	}
	i := strings.LastIndexByte(b[:len(b)-1], b[len(b)-1])
	if i < 0 {
		return ""
	}
	n := b[i+1 : len(b)-1]
	if n == "" || strings.IndexFunc(n, func(r rune) bool { return !isIdent(r) && r != '.' && r != '-' }) >= 0 {
		return ""
	}
	return n
}

// trailingIdent returns the identifier at the end of b.
func trailingIdent(b string) string {
	i := len(b)
	for i > 0 && isIdent(rune(b[i-1])) {
		i--
	}
	return b[i:]
}

func isIdent(r rune) bool {
	return r == '_' || r == '$' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

// wholeValueEnd reports whether the code after a literal ends the value
// it is: the end of the line, a statement, an argument or a comment, or a
// conversion to bytes or characters.
func wholeValueEnd(after string) bool {
	if after == "" || strings.ContainsRune(";,)}]", rune(after[0])) {
		return true
	}
	for _, p := range []string{"//", "/*", ".getBytes", ".toCharArray", ".bytes", "as "} {
		if strings.HasPrefix(after, p) {
			return true
		}
	}
	return false
}

// secretName reports whether a variable, property or header name says it
// holds security material.
func secretName(n string) bool {
	return SecretName(n) || reSecretHeader.MatchString(n)
}

// xsltLiterals extracts the literals of an XSLT line: XPath string literals
// in select (and similar) attributes, other attribute values and element
// text, each named by its variable, parameter, attribute or element.
func xsltLiterals(line string) []literal {
	if strings.Contains(line, "<!--") {
		line = reXMLComment.ReplaceAllStringFunc(line, func(s string) string { return strings.Repeat(" ", len(s)) })
		if i := strings.Index(line, "<!--"); i >= 0 {
			line = line[:i]
		}
	}
	var out []literal
	text := func(from, to int, owner string) {
		if v := strings.TrimSpace(line[from:to]); v != "" {
			out = append(out, literal{val: v, start: from, end: to, name: owner,
				interp: strings.Contains(v, "${") || strings.Contains(v, "{{")})
		}
	}
	prev, owner := 0, ""
	for t := range tags(line) {
		text(prev, t.start, owner)
		prev, owner = t.end, ""
		local := t.name[strings.LastIndex(t.name, ":")+1:]
		xsl := strings.HasPrefix(t.name, "xsl:")
		nameAttr := ""
		for _, a := range t.attrs {
			if a[0] == "name" {
				nameAttr = a[1]
			}
		}
		for _, a := range t.attrs {
			an, v := strings.ToLower(a[0]), a[1]
			switch {
			case an == "name" || strings.HasPrefix(an, "xmlns"):
			case xsl && xpathAttrs[an]:
				single := xpathString(strings.TrimSpace(v))
				for _, s := range xpathLiterals(v) {
					lit := literal{val: s, start: t.start, end: t.end}
					if single && secretName(nameAttr) {
						lit.name = nameAttr
					}
					out = append(out, lit)
				}
			default:
				lit := literal{val: v, start: t.start, end: t.end, interp: strings.Contains(v, "{")}
				if secretName(a[0]) {
					lit.name = a[0]
				}
				out = append(out, lit)
			}
		}
		if t.closing || t.selfClosing {
			continue
		}
		switch {
		case xsl && (local == "variable" || local == "param" || local == "with-param" || local == "attribute"):
			owner = nameAttr
		case !xsl:
			owner = local
		}
		if !secretName(owner) {
			owner = ""
		}
	}
	text(prev, len(line), owner)
	return out
}

// tag is an XML tag of a line.
type tag struct {
	start, end           int
	name                 string
	attrs                [][2]string // name, value
	closing, selfClosing bool
}

// tags yields the complete tags of a line in order.
func tags(line string) iter.Seq[tag] {
	return func(yield func(tag) bool) {
		for i := 0; i < len(line); i++ {
			if line[i] != '<' {
				continue
			}
			t, ok := parseTag(line, i)
			if !ok {
				continue
			}
			if !yield(t) {
				return
			}
			i = t.end - 1
		}
	}
}

// parseTag parses the tag that starts at line[i] == '<'.
func parseTag(line string, i int) (tag, bool) {
	t := tag{start: i}
	j := i + 1
	if j < len(line) && line[j] == '/' {
		t.closing = true
		j++
	}
	n := j
	for j < len(line) && isNameByte(line[j]) {
		j++
	}
	if j == n {
		return t, false
	}
	t.name = line[n:j]
	for j < len(line) {
		switch c := line[j]; {
		case c == '>':
			t.end = j + 1
			return t, true
		case c == '/' && j+1 < len(line) && line[j+1] == '>':
			t.selfClosing, t.end = true, j+2
			return t, true
		case c == '<':
			return t, false
		case isNameByte(c):
			a := j
			for j < len(line) && isNameByte(line[j]) {
				j++
			}
			name := line[a:j]
			for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
				j++
			}
			if j >= len(line) || line[j] != '=' {
				continue
			}
			j++
			for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
				j++
			}
			if j >= len(line) || (line[j] != '"' && line[j] != '\'') {
				continue
			}
			e := strings.IndexByte(line[j+1:], line[j])
			if e < 0 {
				return t, false
			}
			t.attrs = append(t.attrs, [2]string{name, line[j+1 : j+1+e]})
			j += e + 2
		default:
			j++
		}
	}
	return t, false
}

func isNameByte(c byte) bool {
	return c == ':' || c == '.' || c == '-' || c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// xpathString reports whether an XPath expression is one string literal.
func xpathString(e string) bool {
	if len(e) < 2 || (e[0] != '\'' && e[0] != '"') {
		return false
	}
	return strings.IndexByte(e[1:], e[0]) == len(e)-2
}

// xpathLiterals returns the string literals of an XPath expression.
func xpathLiterals(e string) []string {
	var out []string
	for i := 0; i < len(e); i++ {
		if e[i] != '\'' && e[i] != '"' {
			continue
		}
		j := strings.IndexByte(e[i+1:], e[i])
		if j < 0 {
			break
		}
		out = append(out, e[i+1:i+1+j])
		i += j + 1
	}
	return out
}

// IsScript reports whether LineSecret judges files with this path: Groovy,
// JavaScript, Java and XSLT.
func IsScript(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".groovy", ".gsh", ".gy", ".js", ".java", ".xsl", ".xslt":
		return true
	}
	return false
}

// MaskLines replaces every line of a script that LineSecret flags by its
// indentation and masked (cpicli#74): the file keeps its layout, so a patch
// made against the masked view still applies to the other lines. It returns
// the number of masked lines; the text unchanged when there are none.
func MaskLines(path, text, masked string) (string, int) {
	if !IsScript(path) {
		return text, 0
	}
	lines := strings.Split(text, "\n")
	n := 0
	for i, l := range lines {
		body := strings.TrimSuffix(l, "\r")
		if _, hit := LineSecret(path, body); hit {
			n++
			lines[i] = body[:len(body)-len(strings.TrimLeft(body, " \t"))] + masked + l[len(body):]
		}
	}
	if n == 0 {
		return text, 0
	}
	return strings.Join(lines, "\n"), n
}
