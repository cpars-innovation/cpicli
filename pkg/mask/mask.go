// Package mask masks values in tenant data before an AI agent's model sees
// it: personal data (IBAN, e-mail, phone, payment card), secret shapes (JWT,
// Bearer and Basic credentials, private keys, credentials and signatures in
// URLs), values under sensitive names, and the caller's own path and regex
// rules. cpictl mcp, the cpictl CLI, the cpictl gateway and CPI Studio mask
// with it, so they mask the same way (cpicli#70).
//
// A masked value becomes Dots. A masker never returns, logs or counts a
// value it masked: Stats holds only the number of values per detector.
// Package mask uses the standard library only.
package mask

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Dots replaces a masked value. Writes that carry it are refused where a
// masked view could be written back (CPI Studio, pd_deploy).
const Dots = "••••"

// Built-in detectors, in the order they run: an IBAN contains digit runs a
// card or phone rule could take, and a URL's credentials an e-mail rule.
const (
	DetectIBAN           = "iban"
	DetectEmail          = "email"
	DetectPhone          = "phone"
	DetectCard           = "card"
	DetectPrivateKey     = "private-key"
	DetectJWT            = "jwt"
	DetectAuthorization  = "authorization"
	DetectURLCredentials = "url-credentials"
	DetectURLSecret      = "url-secret"
	// DetectConnectionString: SharedAccessKey=, AccountKey=, Password= … in
	// connection strings (Azure Service Bus and Storage, ODBC, JDBC).
	DetectConnectionString = "connection-string"
)

// Counters of Stats besides the detectors.
const (
	CountName  = "name"  // a value under a sensitive name
	CountPath  = "path"  // a value under a path rule
	CountRegex = "regex" // a regex rule's match
)

// BuiltIns are all built-in detectors in their order.
var BuiltIns = []string{DetectPrivateKey, DetectJWT, DetectAuthorization, DetectURLCredentials, DetectURLSecret,
	DetectConnectionString, DetectIBAN, DetectEmail, DetectPhone, DetectCard}

// identifierDetectors are the detectors that run on identifier fields
// (IdentifierKey): not card and phone, which take digit runs of IDs.
var identifierDetectors = []string{DetectPrivateKey, DetectJWT, DetectAuthorization, DetectURLCredentials, DetectURLSecret,
	DetectConnectionString, DetectEmail}

// Rule is a caller's rule: a regex over text, or a path of element or key
// names like //Customer/Name, which masks every value below it (XML
// elements and JSON keys alike).
type Rule struct {
	Kind    string `json:"kind" yaml:"kind"` // regex, path
	Pattern string `json:"pattern" yaml:"pattern"`
}

// Config says what a masker masks.
type Config struct {
	// BuiltIns are the detectors to run; nil means all (BuiltIns).
	BuiltIns []string `json:"builtIns,omitempty" yaml:"builtIns,omitempty"`
	// Names are regexes (case-insensitive) of further sensitive names: the
	// value of a header, property, parameter, JSON key, XML element or
	// attribute with such a name is masked whole, like the built-in
	// sensitive names (SensitiveName).
	Names []string `json:"names,omitempty" yaml:"names,omitempty"`
	// Rules are path and regex rules.
	Rules []Rule `json:"rules,omitempty" yaml:"rules,omitempty"`
}

// Stats counts masked values per detector or rule kind. It never holds a
// value.
type Stats map[string]int

// Total is the number of masked values.
func (s Stats) Total() int {
	n := 0
	for _, v := range s {
		n += v
	}
	return n
}

// Add adds o to s.
func (s Stats) Add(o Stats) {
	for k, v := range o {
		s[k] += v
	}
}

// String lists the counts, e.g. "email 2, iban 1".
func (s Stats) String() string {
	var parts []string
	for _, k := range slices.Sorted(maps.Keys(s)) {
		parts = append(parts, fmt.Sprintf("%s %d", k, s[k]))
	}
	return strings.Join(parts, ", ")
}

// sensitiveWords are the parts of sensitive names (lower case).
var sensitiveWords = []string{"authorization", "cookie", "password", "passwd", "pwd", "passphrase", "secret", "token",
	"credential", "apikey", "api-key", "api_key", "privatekey", "private_key"}

// SensitiveName reports whether a value with this name is masked whole:
// authorization, cookies, tokens, passwords, passphrases, secrets,
// credentials, API keys and private keys (case-insensitive).
func SensitiveName(name string) bool {
	lower := strings.ToLower(name)
	for _, w := range sensitiveWords {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

// Masker masks with one Config. It is safe for concurrent use.
type Masker struct {
	detectors []string
	names     []*regexp.Regexp
	paths     [][]string
	regexes   []*regexp.Regexp
	prefixes  []string // each regex's literal prefix: a string without it is skipped

	sensitive sync.Map // name -> bool: names repeat (element and key names)
}

var pathRE = regexp.MustCompile(`^//([A-Za-z_][\w.-]*(?:/[A-Za-z_][\w.-]*)*)$`)

// ParsePath returns a path rule's names, or nil when the pattern is not a
// path like //Customer/Name.
func ParsePath(pattern string) []string {
	m := pathRE.FindStringSubmatch(strings.TrimSpace(pattern))
	if m == nil {
		return nil
	}
	return strings.Split(m[1], "/")
}

// New builds a masker. An unknown detector, an invalid regex or path, or a
// regex that matches empty text is an error: a masking rule that silently
// does nothing would leak.
func New(c Config) (*Masker, error) {
	m := &Masker{}
	want := c.BuiltIns
	if want == nil {
		want = BuiltIns
	}
	for _, d := range want {
		if !slices.Contains(BuiltIns, d) {
			return nil, fmt.Errorf("unknown detector %q (one of %s)", d, strings.Join(BuiltIns, ", "))
		}
	}
	for _, d := range BuiltIns { // fixed order
		if slices.Contains(want, d) {
			m.detectors = append(m.detectors, d)
		}
	}
	for _, n := range c.Names {
		re, err := regexp.Compile("(?i)" + n)
		if err != nil {
			return nil, fmt.Errorf("name pattern %q: %w", n, err)
		}
		m.names = append(m.names, re)
	}
	for _, r := range c.Rules {
		switch r.Kind {
		case "path":
			p := ParsePath(r.Pattern)
			if p == nil {
				return nil, fmt.Errorf("path rule %q: a path is names after //, like //Customer/Name", r.Pattern)
			}
			m.paths = append(m.paths, p)
		case "regex":
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				return nil, fmt.Errorf("regex rule %q: %w", r.Pattern, err)
			}
			if re.MatchString("") {
				return nil, fmt.Errorf("regex rule %q matches empty text", r.Pattern)
			}
			prefix, _ := re.LiteralPrefix()
			m.regexes, m.prefixes = append(m.regexes, re), append(m.prefixes, prefix)
		default:
			return nil, fmt.Errorf("rule kind %q: regex or path", r.Kind)
		}
	}
	return m, nil
}

// Default is a masker with every built-in detector and no rules of its own.
func Default() *Masker {
	m, _ := New(Config{})
	return m
}

// Sensitive reports whether a value with this name is masked whole: a
// built-in sensitive name or one of Config.Names.
func (m *Masker) Sensitive(name string) bool {
	if v, ok := m.sensitive.Load(name); ok {
		return v.(bool)
	}
	is := SensitiveName(name)
	for _, re := range m.names {
		is = is || re.MatchString(name)
	}
	if len(name) <= 128 {
		m.sensitive.Store(name, is)
	}
	return is
}

// Field masks the value of a header, exchange property or parameter: whole
// when the name is sensitive, otherwise like Text.
func (m *Masker) Field(name, value string) (string, Stats) {
	st := Stats{}
	if value != "" && value != Dots && m.Sensitive(name) {
		st[CountName]++
		return Dots, st
	}
	out := m.text(value, nil, st)
	return out, st
}

// Text masks text: JSON by keys (it stays valid JSON, numbers are kept as
// written), XML element by element, then the detectors and regex rules over
// the text. Text without anything to mask is returned unchanged.
func (m *Masker) Text(s string) (string, Stats) {
	st := Stats{}
	out := m.text(s, nil, st)
	if st.Total() == 0 {
		return s, st
	}
	return out, st
}

// Mask is Text, reporting only whether something was masked.
func (m *Masker) Mask(s string) (string, bool) {
	out, st := m.Text(s)
	return out, st.Total() > 0
}

func (m *Masker) text(s string, stack []string, st Stats) string {
	// a byte order mark stays where it was; the document after it is masked
	if rest, ok := strings.CutPrefix(s, "\ufeff"); ok {
		return "\ufeff" + m.text(rest, stack, st)
	}
	t := strings.TrimSpace(s)
	if (strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}")) || (strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]")) {
		if v, err := decodeOrdered(t); err == nil {
			var buf bytes.Buffer
			m.writeJSON(&buf, v, stack, strings.Contains(s, "\n"), 0, false, st)
			return buf.String()
		}
	}
	if strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
		// JSON that does not parse as one document: a sequence (NDJSON),
		// trailing data or a truncated document. Mask each document of a
		// sequence by keys; anything else line by line, which still masks
		// "name": "value" pairs (cpicli#77).
		if out, ok := m.jsonSeq(s, stack, st); ok {
			return out
		}
		return m.quotedTail(m.inPlace(m.otherValues(s, st), st), st)
	}
	if strings.HasPrefix(t, "<") {
		s = m.xml(s, st)
	}
	if strings.Contains(s, `"`) {
		s = m.quotedTail(m.quotedPairs(m.otherValues(s, st), st), st)
	}
	return m.plain(s, st)
}

// jsonSeq masks a sequence of JSON documents (NDJSON, or a document
// followed by others) and keeps what lies between them. It reports false
// when the text is not entirely such a sequence.
func (m *Masker) jsonSeq(s string, stack []string, st Stats) (string, bool) {
	dec := json.NewDecoder(strings.NewReader(s))
	var b strings.Builder
	seq := Stats{} // counted into st only when the whole text is a sequence
	last := 0
	for n := 0; ; n++ {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if err != io.EOF || n < 2 {
				return "", false
			}
			break
		}
		end := int(dec.InputOffset())
		doc := strings.TrimSpace(s[last:end])
		start := end - len(doc)
		if start < last || s[start:end] != doc {
			return "", false
		}
		v, err := decodeOrdered(doc)
		if err != nil {
			return "", false
		}
		b.WriteString(s[last:start])
		var buf bytes.Buffer
		m.writeJSON(&buf, v, stack, strings.Contains(doc, "\n"), 0, false, seq)
		b.WriteString(buf.String())
		last = end
	}
	b.WriteString(s[last:])
	st.Add(seq)
	return b.String(), true
}

// quotedPairs masks "name": "value" pairs under sensitive names in text
// that is not a JSON document, such as a log line carrying a body.
func (m *Masker) quotedPairs(s string, st Stats) string {
	return reQuotedPair.ReplaceAllStringFunc(s, func(x string) string {
		p := reQuotedPair.FindStringSubmatch(x)
		if p[3] == "" || p[3] == Dots || !m.Sensitive(p[2]) || ReferenceKey(p[2]) {
			return x
		}
		st[CountName]++
		return p[1] + `"` + Dots + `"`
	})
}

// a "name": key in JSON-like text
var reQuotedKey = regexp.MustCompile(`"([^"\\]*)"\s*:\s*`)

// otherValues masks numbers, booleans, lists and objects under sensitive
// names in JSON-like text that does not parse, as JSON masking hides them
// whole (HidesWhole): "pin": 1234, "password": ["x"]. A list or object
// that is not closed is masked to the end of the text.
func (m *Masker) otherValues(s string, st Stats) string {
	var b strings.Builder
	last := 0
	for _, loc := range reQuotedKey.FindAllStringSubmatchIndex(s, -1) {
		if loc[0] < last || loc[1] >= len(s) {
			continue
		}
		c := s[loc[1]]
		if c == '"' { // strings are masked elsewhere
			continue
		}
		var v any = []any{}
		end := loc[1]
		if c != '[' && c != '{' {
			// a bare token: true, false, a number or broken text. It ends at
			// a delimiter or a quote, so no later key lies inside it.
			for end < len(s) && !strings.ContainsRune(",}] \t\r\n\"", rune(s[end])) {
				end++
			}
			if v = scalar(s[loc[1]:end]); end == loc[1] || v == nil { // null holds nothing
				continue
			}
		}
		// the name decides before a list or object is scanned: it is
		// scanned only when it is masked, and the next key is looked for
		// after it, so every byte is scanned at most once
		if !m.HidesWhole(s[loc[2]:loc[3]], v) {
			continue
		}
		if c == '[' || c == '{' {
			end = closing(s, loc[1])
		}
		st[CountName]++
		b.WriteString(s[last:loc[1]])
		b.WriteString(`"` + Dots + `"`)
		last = end
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// closing returns the end of the list or object starting at i, or the end
// of s when it is not closed.
func closing(s string, i int) int {
	depth, inStr := 0, false
	for ; i < len(s); i++ {
		switch c := s[i]; {
		case inStr && c == '\\':
			i++
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '[' || c == '{':
			depth++
		case c == ']' || c == '}':
			if depth--; depth == 0 {
				return i + 1
			}
		}
	}
	return len(s)
}

// a value cut off at the end of truncated JSON: "password": "hunt
var reQuotedTail = regexp.MustCompile(`("([^"\\]*)"\s*:\s*)"((?:[^"\\]|\\.)*)\\?$`)

// quotedTail masks a value under a sensitive name that the text cuts off.
func (m *Masker) quotedTail(s string, st Stats) string {
	p := reQuotedTail.FindStringSubmatchIndex(s)
	if p == nil || p[6] == p[7] || !m.Sensitive(s[p[4]:p[5]]) {
		return s
	}
	st[CountName]++
	return s[:p[3]] + `"` + Dots
}

// JSON masks a JSON document, such as a tool result: every string leaf as
// Text, and every value under a sensitive key or a path rule. Key order and
// numbers are kept as written; the output is compact.
func (m *Masker) JSON(raw []byte) ([]byte, Stats, error) {
	st := Stats{}
	v, err := decodeOrdered(string(raw))
	if err != nil {
		return nil, st, err
	}
	var b bytes.Buffer
	m.writeJSON(&b, v, nil, false, 0, false, st)
	if st.Total() == 0 {
		return raw, st, nil
	}
	return b.Bytes(), st, nil
}

// --- detectors ---------------------------------------------------------

var (
	// a private key block, also cut off before its END line
	rePEMPrivate = regexp.MustCompile(`-----BEGIN ((?:RSA |EC |DSA |OPENSSH |ENCRYPTED |PGP )?PRIVATE KEY(?: BLOCK)?)-----[\s\S]*?(?:-----END [A-Z ]*-----|\z)`)
	reJWT        = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)
	reAuth       = regexp.MustCompile(`(?i)\b(bearer|basic)(\s+|\s*[:=]\s*)([A-Za-z0-9+/._~-]{4,}=*)`)
	// user:password@host; the password may hold / and @ (the last @
	// in the token ends it: masking a path beats leaking a password)
	reURLCreds = regexp.MustCompile(`\b([a-zA-Z][a-zA-Z0-9+.-]*://)([^\s/:@"'<>]+):([^\s"'<>]*)@([^\s/@:"'<>]+)([:/?#\s"'<>]|$)`)
	// Key=value in a connection string: no blanks around "=", unlike code
	reConnSecret = regexp.MustCompile(`(?i)\b(SharedAccessKey|AccountKey|SharedSecret|SharedAccessSignature|ClientSecret|Password|Pwd)(=)("(?:[^"]|"")*"|'(?:[^']|'')*'|\{(?:[^}]|\}\})*\}|["'{][^\n]*|[^;"'\s<>&]+)`)
	// a call: password=getPassword()
	reCall      = regexp.MustCompile(`^[\w.]+\(\)$`)
	reURLSecret = regexp.MustCompile(`(?i)([?&;](?:sig|signature|x-amz-signature|x-amz-credential|x-amz-security-token|x-goog-signature|x-goog-credential|api_?key|apikey|access_?token|id_token|refresh_token|token|client_secret|password|passwd|pwd|secret|code|key|sv|se|skoid|sktid)=)([^&\s"'<>#]+)`)
	// an IBAN candidate; its valid prefix is masked (a currency or more
	// text may follow without a break)
	ibanRE  = regexp.MustCompile(`(?i)\b[A-Z]{2}\d{2}(?: ?[A-Z0-9]){11,30}`)
	emailRE = regexp.MustCompile(`[\p{L}\p{N}._%+-]+@[\p{L}\p{N}-]+(?:\.[\p{L}\p{N}-]+)*\.\p{L}{2,}`)
	phoneRE = regexp.MustCompile(`(?:\+|\b00)[1-9]\d{0,2}(?:[ ./-]?\(?\d+\)?){2,6}`)
	// a run of digit groups; cardIn finds the card in it
	cardRE = regexp.MustCompile(`\b\d(?:[ -]?\d){12,}`)
	nonDig = regexp.MustCompile(`\D`)
)

// urlSecretParams that are only masked when the value looks like a secret
// (they also carry ordinary values: key=value, code=DE).
var urlSecretLoose = map[string]bool{"key=": true, "code=": true, "token=": true, "sv=": true, "se=": true}

func (m *Masker) plain(s string, st Stats) string {
	if len(s) < 6 && len(m.regexes) == 0 {
		return s // shorter than anything a detector finds
	}
	digits := 0
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			digits++
		}
	}
	for _, d := range m.detectors {
		if may(d, s, digits) {
			s = detect(d, s, st)
		}
	}
	for i, re := range m.regexes {
		if p := m.prefixes[i]; p != "" && !strings.Contains(s, p) {
			continue
		}
		s = re.ReplaceAllStringFunc(s, func(string) string { st[CountRegex]++; return Dots })
	}
	return s
}

// may is a cheap test whether detector d can find anything in s (digits:
// the number of ASCII digits in s), so that most strings pass without a
// regular expression.
func may(d, s string, digits int) bool {
	switch d {
	case DetectPrivateKey:
		return strings.Contains(s, "PRIVATE KEY")
	case DetectJWT:
		return strings.Contains(s, "eyJ")
	case DetectAuthorization:
		return containsFold(s, "bearer") || containsFold(s, "basic")
	case DetectConnectionString:
		return strings.Contains(s, "=") && (containsFold(s, "key") || containsFold(s, "secret") || containsFold(s, "signature") ||
			containsFold(s, "password") || containsFold(s, "pwd"))
	case DetectURLCredentials:
		return strings.Contains(s, "://") && strings.Contains(s, "@")
	case DetectURLSecret:
		return strings.Contains(s, "=") && strings.ContainsAny(s, "?&;")
	case DetectIBAN:
		return digits >= 4 && len(s) >= 15 && ibanStart(s)
	case DetectEmail:
		return strings.Contains(s, "@")
	case DetectPhone:
		return digits >= 8 && (strings.Contains(s, "+") || strings.Contains(s, "00")) && digitRun(s) >= 8
	case DetectCard:
		return digits >= 13 && digitRun(s) >= 13
	}
	return true
}

func isUpper(c byte) bool { return c >= 'A' && c <= 'Z' }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// containsFold is strings.Contains ignoring ASCII case (sub in lower case).
func containsFold(s, sub string) bool {
	n := len(sub)
	for i := 0; i+n <= len(s); i++ {
		if strings.EqualFold(s[i:i+n], sub) {
			return true
		}
	}
	return false
}

func isLetter(c byte) bool { return isUpper(c) || c >= 'a' && c <= 'z' }

// ibanStart: two letters followed by two digits somewhere in s.
func ibanStart(s string) bool {
	for i := 0; i+3 < len(s); i++ {
		if isLetter(s[i]) && isLetter(s[i+1]) && isDigit(s[i+2]) && isDigit(s[i+3]) {
			return true
		}
	}
	return false
}

// digitRun is the most digits in s joined by single spaces, dashes, dots,
// slashes or brackets (how cards and phone numbers are written).
func digitRun(s string) int {
	best, run, sep := 0, 0, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case isDigit(c):
			run++
			sep = false
			best = max(best, run)
		case !sep && run > 0 && strings.IndexByte(" -./()", c) >= 0:
			sep = true
		default:
			run, sep = 0, false
		}
	}
	return best
}

func detect(d, s string, st Stats) string {
	switch d {
	case DetectPrivateKey:
		return rePEMPrivate.ReplaceAllStringFunc(s, func(x string) string {
			st[d]++
			kind := rePEMPrivate.FindStringSubmatch(x)[1]
			if !strings.Contains(x, "-----END ") {
				return "-----BEGIN " + kind + "-----" + Dots
			}
			return "-----BEGIN " + kind + "-----" + Dots + "-----END " + kind + "-----"
		})
	case DetectJWT:
		return reJWT.ReplaceAllStringFunc(s, func(string) string { st[d]++; return Dots })
	case DetectAuthorization:
		return reAuth.ReplaceAllStringFunc(s, func(x string) string {
			p := reAuth.FindStringSubmatch(x)
			if p[3] == Dots || !credentialLike(p[3]) && !(strings.EqualFold(p[1], "basic") && basicCredentials(p[3])) {
				return x
			}
			st[d]++
			return p[1] + p[2] + Dots
		})
	case DetectURLCredentials:
		return reURLCreds.ReplaceAllStringFunc(s, func(x string) string {
			p := reURLCreds.FindStringSubmatch(x)
			st[d]++
			return p[1] + Dots + ":" + Dots + "@" + p[4] + p[5]
		})
	case DetectURLSecret:
		return reURLSecret.ReplaceAllStringFunc(s, func(x string) string {
			p := reURLSecret.FindStringSubmatch(x)
			name := strings.ToLower(p[1][1:])
			if p[2] == Dots || urlSecretLoose[name] && !credentialLike(p[2]) {
				return x
			}
			st[d]++
			return p[1] + Dots
		})
	case DetectConnectionString:
		return reConnSecret.ReplaceAllStringFunc(s, func(x string) string {
			p := reConnSecret.FindStringSubmatch(x)
			v := p[3]
			if v == Dots || strings.Contains(v, Dots) || reCall.MatchString(v) || strings.HasPrefix(v, "{{") || strings.HasPrefix(v, "${") {
				return x // a call (password=getPassword()) or a placeholder
			}
			st[d]++
			// quoted (doubled quotes escape), braced (}} escapes), or
			// unterminated: masked to the end of the line
			if q := v[0]; q == '"' || q == '\'' || q == '{' {
				closing := map[byte]byte{'"': '"', '\'': '\'', '{': '}'}[q]
				if len(v) > 1 && v[len(v)-1] == closing {
					return p[1] + p[2] + string(q) + Dots + string(closing)
				}
				return p[1] + p[2] + string(q) + Dots
			}
			return p[1] + p[2] + Dots
		})
	case DetectIBAN:
		return ibanRE.ReplaceAllStringFunc(s, func(x string) string {
			// the longest valid prefix: "DE89… EUR" matches with the currency
			for end := len(x); end >= 15; end-- {
				cand := strings.TrimRight(x[:end], " ")
				if len(cand) != end || !ibanValid(cand) {
					continue
				}
				st[d]++
				c := strings.ToUpper(strings.ReplaceAll(cand, " ", ""))
				return c[:4] + " " + Dots + " " + Dots + " " + Dots + " " + c[len(c)-4:] + x[end:]
			}
			return x
		})
	case DetectEmail:
		return emailRE.ReplaceAllStringFunc(s, func(string) string { st[d]++; return Dots + "@" + Dots })
	case DetectPhone:
		var b strings.Builder
		last := 0
		for _, loc := range phoneRE.FindAllStringIndex(s, -1) {
			// a number that runs straight on into a letter is part of a
			// hex ID (…-0049-1771-53ff-…), not a phone number (cpicli#76)
			// a 00… run inside a hex ID (a GUID, a hash) is part of the ID;
			// a +number never is (cpicli#76)
			if s[loc[0]] != '+' && inHexID(s, loc[0], loc[1]) {
				continue
			}
			if n := len(digitsOf(s[loc[0]:loc[1]])); n < 8 || n > 16 {
				continue
			}
			st[d]++
			b.WriteString(s[last:loc[0]])
			b.WriteString("+•• " + Dots + " " + Dots)
			last = loc[1]
		}
		if last == 0 {
			return s
		}
		b.WriteString(s[last:])
		return b.String()
	case DetectCard:
		var b strings.Builder
		last := 0
		for _, loc := range cardRE.FindAllStringIndex(s, -1) {
			if loc[0] < last {
				continue
			}
			x := s[loc[0]:loc[1]]
			// the longest card-shaped, Luhn-valid part that starts and ends
			// at a group boundary: "qty 12 4111…", "4111 … 1111 12/27".
			// A number glued to a name (ORD_4111…, Flow4111…) is no card.
			start, end := cardIn(x, loc[0] > 0 && (isLetter(s[loc[0]-1]) || s[loc[0]-1] == '_'), inHexID(s, loc[0], loc[1]))
			if start < 0 {
				continue
			}
			digits := digitsOf(x[start:end])
			st[d]++
			b.WriteString(s[last : loc[0]+start])
			b.WriteString(Dots + " " + Dots + " " + Dots + " " + digits[len(digits)-4:])
			last = loc[0] + end
		}
		if last == 0 {
			return s
		}
		b.WriteString(s[last:])
		return b.String()
	}
	return s
}

// cardNetwork: digits start with the prefix of a card network. 4, 5 and 6
// are all card ranges (Visa; Mastercard, Maestro, RuPay 508; Maestro,
// Discover, UnionPay, RuPay 60/652); on 2, 3 and 8 only the issued ranges
// (Mir 2200-2204, Mastercard 2221-2720; American Express 34/37, Diners
// 300-305/36/38, JCB 3528-3589; RuPay and UnionPay 81/82). Lists of years
// (2021 2022 …) start with none.
func cardNetwork(d string) bool {
	p := func(n int) int { v, _ := strconv.Atoi(d[:n]); return v }
	switch d[0] {
	case '4', '5', '6':
		return true
	case '2':
		return p(4) >= 2200 && p(4) <= 2204 || p(4) >= 2221 && p(4) <= 2720
	case '3':
		return p(2) == 34 || p(2) == 37 || p(2) == 36 || p(2) == 38 || p(3) >= 300 && p(3) <= 305 || p(4) >= 3528 && p(4) <= 3589
	case '8':
		return p(2) == 81 || p(2) == 82
	}
	return false
}

// cardIn finds the longest card number in x, a run of digit groups, that
// starts and ends at a group boundary; glued: x follows a letter or _, so
// its first group cannot start a card. -1 when there is none.
func cardIn(x string, glued, gluedEnd bool) (start, end int) {
	var starts, ends []int
	for i := 0; i < len(x); i++ {
		if !isDigit(x[i]) {
			continue
		}
		if i == 0 && !glued || i > 0 && !isDigit(x[i-1]) {
			starts = append(starts, i)
		}
		// a run inside a hex ID (a GUID, a hash) does not end where the ID
		// goes on; a card glued to text (IDoc, flat files) does (cpicli#76)
		if i == len(x)-1 && !gluedEnd || i < len(x)-1 && !isDigit(x[i+1]) {
			ends = append(ends, i+1)
		}
	}
	best, bs, be := 0, -1, -1
	for _, a := range starts {
		for _, z := range ends[sort.SearchInts(ends, a+1):] {
			if z-a > 23 { // 19 digits and 4 separators at most; ends are sorted
				break
			}
			if z <= a || z-a <= best {
				continue
			}
			digits := digitsOf(x[a:z])
			if len(digits) >= 13 && cardShaped(x[a:z], digits) && luhnValid(digits) && !consecutiveGroups(x[a:z]) {
				best, bs, be = z-a, a, z
			}
		}
	}
	return bs, be
}

// consecutiveGroups: three or more groups of four digits that count up by
// one (ports 5000 5001 5002 5003): a list, not a card (cpicli#76).
func consecutiveGroups(x string) bool {
	groups := strings.FieldsFunc(x, func(r rune) bool { return r == ' ' || r == '-' })
	if len(groups) < 3 {
		return false
	}
	prev := -1
	for i, g := range groups {
		if len(g) != 4 {
			return false
		}
		n, err := strconv.Atoi(g)
		if err != nil || i > 0 && n != prev+1 {
			return false
		}
		prev = n
	}
	return true
}

var (
	reGUID  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	reHexID = regexp.MustCompile(`^[0-9a-fA-F]+$`)
	// hash lengths in hex: MD5, SHA-1, SHA-224, SHA-256, SHA-384, SHA-512
	hexIDLengths = []int{32, 40, 56, 64, 96, 128}
)

// maxHexID is the longest hex ID (SHA-512 in hex): a longer word is no hex
// ID, and the scan for one stops there, so that masking stays linear
// (cpicli#76).
const maxHexID = 128

// inHexID: s[a:b] lies inside a hex ID, a GUID (8-4-4-4-12) or a hash (hex
// digits of a hash's length), not inside text where a number may touch a
// letter (4111…JOHN SMITH in an IDoc, call+49…) or a card glued to more hex.
// It looks at most maxHexID characters around the match: a longer word is no
// hex ID (masked, the safe side).
func inHexID(s string, a, b int) bool {
	if b-a > maxHexID {
		return false
	}
	word := func(c byte) bool { return isLetter(c) || isDigit(c) || c == '-' }
	for a > 0 && word(s[a-1]) {
		if a--; b-a > maxHexID {
			return false
		}
	}
	for b < len(s) && word(s[b]) {
		if b++; b-a > maxHexID {
			return false
		}
	}
	w := s[a:b]
	return reGUID.MatchString(w) || slices.Contains(hexIDLengths, len(w)) && reHexID.MatchString(w)
}

// digitsOf returns the ASCII digits of s.
func digitsOf(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if isDigit(s[i]) {
			b = append(b, s[i])
		}
	}
	return string(b)
}

// basicCredentials: Basic credentials that decode to user:password.
func basicCredentials(v string) bool {
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		b, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(v, "="))
	}
	return err == nil && bytes.IndexByte(b, ':') > 0
}

// credentialLike: a token, not a word (it has a digit or a base64 sign, or
// mixes cases at length 20 and more).
func credentialLike(v string) bool {
	if strings.ContainsAny(v, "0123456789+/=") {
		return true
	}
	return len(v) >= 20 && strings.ToLower(v) != v && strings.ToUpper(v) != v
}

// cardShaped: a card number as cards are written: 13 to 19 digits from a
// card network (first digit 2 to 6), in one run or in groups of four (the
// last shorter), or 4-6-4/4-6-5 (American Express, Diners). Dates, times
// and IDs with other groups are not cards.
func cardShaped(x, digits string) bool {
	if len(digits) < 13 || len(digits) > 19 || !cardNetwork(digits) {
		return false
	}
	groups := strings.FieldsFunc(x, func(r rune) bool { return r == ' ' || r == '-' })
	if len(groups) == 1 {
		return true
	}
	var sizes []int
	for _, g := range groups {
		sizes = append(sizes, len(g))
	}
	if slices.Equal(sizes, []int{4, 6, 4}) || slices.Equal(sizes, []int{4, 6, 5}) {
		return true
	}
	for i, n := range sizes {
		if n != 4 && !(i == len(sizes)-1 && n >= 1 && n < 4) {
			return false
		}
	}
	return true
}

// ibanLengths are the IBAN lengths per country (ISO 13616 registry).
var ibanLengths = map[string]int{
	"AD": 24, "AE": 23, "AL": 28, "AT": 20, "AZ": 28, "BA": 20, "BE": 16, "BG": 22, "BH": 22, "BI": 27, "BR": 29,
	"BY": 28, "CH": 21, "CR": 22, "CY": 28, "CZ": 24, "DE": 22, "DJ": 27, "DK": 18, "DO": 28, "EE": 20, "EG": 29,
	"ES": 24, "FI": 18, "FK": 18, "FO": 18, "FR": 27, "GB": 22, "GE": 22, "GI": 23, "GL": 18, "GR": 27, "GT": 28,
	"HR": 21, "HU": 28, "IE": 22, "IL": 23, "IQ": 23, "IS": 26, "IT": 27, "JO": 30, "KW": 30, "KZ": 20, "LB": 28,
	"LC": 32, "LI": 21, "LT": 20, "LU": 20, "LV": 21, "LY": 25, "MC": 27, "MD": 24, "ME": 22, "MK": 19, "MN": 20,
	"MR": 27, "MT": 31, "MU": 30, "NI": 28, "NL": 18, "NO": 15, "OM": 23, "PK": 24, "PL": 28, "PS": 29, "PT": 25,
	"QA": 29, "RO": 24, "RS": 22, "RU": 33, "SA": 24, "SC": 31, "SD": 18, "SE": 24, "SI": 19, "SK": 24, "SM": 27,
	"SO": 23, "ST": 25, "SV": 28, "TL": 23, "TN": 24, "TR": 26, "UA": 29, "VA": 22, "VG": 24, "XK": 20, "YE": 30,
}

func ibanValid(s string) bool {
	c := strings.ToUpper(strings.ReplaceAll(s, " ", ""))
	// the country's exact length: a hash or an ID passes mod-97 one time in
	// 97, but rarely also has a country code and its length (cpicli#75)
	if n, ok := ibanLengths[c[:min(2, len(c))]]; !ok || len(c) != n {
		return false
	}
	rest := 0
	for _, ch := range c[4:] + c[:4] {
		v := string(ch)
		if ch >= 'A' && ch <= 'Z' {
			v = strconv.Itoa(int(ch) - 55)
		}
		for _, d := range v {
			rest = (rest*10 + int(d-'0')) % 97
		}
	}
	return rest == 1
}

func luhnValid(digits string) bool {
	sum := 0
	for i := 0; i < len(digits); i++ {
		d := int(digits[len(digits)-1-i] - '0')
		if i%2 == 1 {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum%10 == 0
}

// --- XML ---------------------------------------------------------------

// xmlToken is one piece of markup found by nextMarkup: a tag (name set),
// or a comment, CDATA section or processing instruction (name "").
type xmlToken struct {
	start, end                  int // the markup is s[start:end]
	name                        string
	attrStart, attrEnd          int // the attributes of a tag
	closing, selfClosing, cdata bool
}

// nextMarkup finds the next markup at or after i (ok false: none). It is a
// plain scan: large documents (flow models) pass in linear time.
func nextMarkup(s string, i int) (xmlToken, bool) {
	for {
		j := strings.IndexByte(s[i:], '<')
		if j < 0 {
			return xmlToken{}, false
		}
		at := i + j
		rest := s[at:]
		var endMark string
		switch {
		case strings.HasPrefix(rest, "<!--"):
			endMark = "-->"
		case strings.HasPrefix(rest, "<![CDATA["):
			endMark = "]]>"
		case strings.HasPrefix(rest, "<?"):
			endMark = "?>"
		}
		if endMark != "" {
			k := strings.Index(rest, endMark)
			if k < 0 {
				return xmlToken{}, false
			}
			return xmlToken{start: at, end: at + k + len(endMark), cdata: endMark == "]]>"}, true
		}
		t := xmlToken{start: at}
		p := at + 1
		if p < len(s) && s[p] == '/' {
			t.closing = true
			p++
		}
		n := p
		for n < len(s) && (isNameChar(s[n]) || n > p && (s[n] == ':' || s[n] == '.' || s[n] == '-' || isDigit(s[n]))) {
			n++
		}
		if n == p {
			i = at + 1 // a '<' that starts no tag
			continue
		}
		// the end of the tag, outside quoted attribute values
		q, k := byte(0), n
		for ; k < len(s); k++ {
			c := s[k]
			if q != 0 {
				if c == q {
					q = 0
				}
				continue
			}
			if c == '"' || c == '\'' {
				q = c
			} else if c == '>' {
				break
			}
		}
		if k >= len(s) {
			return xmlToken{}, false
		}
		t.name, t.attrStart, t.attrEnd, t.end = s[p:n], n, k, k+1
		if k > n && s[k-1] == '/' {
			t.selfClosing, t.attrEnd = true, k-1
		}
		return t, true
	}
}

func isNameChar(c byte) bool { return c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' }

var attrRE = regexp.MustCompile(`([A-Za-z_][\w:.-]*)(\s*=\s*)("[^"]*"|'[^']*')`)

func localName(n string) string {
	if i := strings.LastIndex(n, ":"); i >= 0 {
		return n[i+1:]
	}
	return n
}

// xml masks element text under a sensitive name or a path rule, and
// sensitive attributes; the detectors run over the result afterwards.
func (m *Masker) xml(s string, st Stats) string {
	var stack []string
	var out strings.Builder
	last := 0
	hidden := func() bool {
		return len(stack) > 0 && m.Sensitive(stack[len(stack)-1]) || m.underPath(stack)
	}
	text := func(t string) {
		if strings.TrimSpace(t) != "" && hidden() {
			if m.underPath(stack) {
				st[CountPath]++
			} else {
				st[CountName]++
			}
			out.WriteString(Dots)
			return
		}
		out.WriteString(t)
	}
	for pos := 0; ; {
		t, ok := nextMarkup(s, pos)
		if !ok {
			break
		}
		text(s[last:t.start])
		whole := s[t.start:t.end]
		switch {
		case t.name == "": // comment, CDATA or processing instruction
			if t.cdata && hidden() {
				st[CountName]++
				out.WriteString("<![CDATA[" + Dots + "]]>")
			} else {
				out.WriteString(whole)
			}
		default:
			name := localName(t.name)
			attrs := s[t.attrStart:t.attrEnd]
			if !t.closing && strings.Contains(attrs, "=") && (len(m.paths) > 0 || len(m.names) > 0 || SensitiveName(attrs)) {
				attrs = attrRE.ReplaceAllStringFunc(attrs, func(a string) string {
					p := attrRE.FindStringSubmatch(a)
					v := p[3][1 : len(p[3])-1]
					if v == "" || v == Dots || !m.Sensitive(localName(p[1])) && !m.underPath(append(slices.Clone(stack), name, localName(p[1]))) {
						return a
					}
					st[CountName]++
					return p[1] + p[2] + p[3][:1] + Dots + p[3][:1]
				})
				whole = s[t.start:t.attrStart] + attrs + s[t.attrEnd:t.end]
			}
			switch {
			case t.closing:
				for i := len(stack) - 1; i >= 0; i-- {
					if stack[i] == name {
						stack = stack[:i]
						break
					}
				}
			case !t.selfClosing:
				stack = append(stack, name)
			}
			out.WriteString(whole)
		}
		last, pos = t.end, t.end
	}
	text(s[last:])
	return out.String()
}

func (m *Masker) underPath(stack []string) bool {
	for _, segs := range m.paths {
		if len(segs) > len(stack) {
			continue
		}
		if slices.Equal(stack[len(stack)-len(segs):], segs) {
			return true
		}
	}
	return false
}

// --- JSON --------------------------------------------------------------

type kv struct {
	k string
	v any
}
type object []kv

func decodeOrdered(s string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err == nil {
		return nil, fmt.Errorf("trailing data")
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch d {
	case '{':
		o := object{}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			v, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			o = append(o, kv{kt.(string), v})
		}
		_, err := dec.Token()
		return o, err
	default: // '['
		a := []any{}
		for dec.More() {
			v, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		_, err := dec.Token()
		return a, err
	}
}

// writeJSON writes v with masking at the leaves (2-space indent when
// indent). hide masks every leaf (below a sensitive key).
func (m *Masker) writeJSON(b *bytes.Buffer, v any, stack []string, indent bool, depth int, hide bool, st Stats) {
	m.writeJSONKey(b, v, stack, indent, depth, hide, false, false, st)
}

// writeJSONKey is writeJSON; ident: v is an identifier field
// (IdentifierKey), masked by identifierDetectors only; cred: v is inside a
// credential container (InCredential), where only metadata stays readable.
func (m *Masker) writeJSONKey(b *bytes.Buffer, v any, stack []string, indent bool, depth int, hide, ident, cred bool, st Stats) {
	nl := func(d int) {
		if indent {
			b.WriteByte('\n')
			b.WriteString(strings.Repeat("  ", d))
		}
	}
	leafHidden := func() string {
		switch {
		case hide:
			return CountName
		case m.underPath(stack):
			return CountPath
		}
		return ""
	}
	switch t := v.(type) {
	case object:
		if len(t) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteByte('{')
		keys := make([]string, len(t))
		for i, e := range t {
			keys[i] = e.k
		}
		pair := m.SensitivePair(keys, func(k string) (string, bool) {
			for _, e := range t {
				if e.k == k {
					s, ok := e.v.(string)
					return s, ok
				}
			}
			return "", false
		})
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			nl(depth + 1)
			writeString(b, e.k)
			b.WriteByte(':')
			if indent {
				b.WriteByte(' ')
			}
			// inside a credential container its metadata wins over a sensitive
			// word in the key (secretEnv names a variable, cpicli#76)
			meta := cred && CredentialMetaKey(e.k) && !m.userSensitive(e.k) && envNameOK(e.k, e.v)
			hideChild := hide || pair && IsPairValue(e.k) || !meta && m.HidesWhole(e.k, e.v) || cred && isScalar(e.v) && !meta
			childCred := cred || !hideChild && isContainer(e.v) && CredentialContainer(e.k)
			m.writeJSONKey(b, e.v, append(slices.Clone(stack), e.k), indent, depth+1, hideChild, !hideChild && IdentifierKey(e.k), childCred, st)
		}
		nl(depth)
		b.WriteByte('}')
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			nl(depth + 1)
			// a bare value in a credential list is a secret
			m.writeJSONKey(b, e, stack, indent, depth+1, hide || cred && isScalar(e), false, cred, st)
		}
		nl(depth)
		b.WriteByte(']')
	case string:
		if c := leafHidden(); c != "" && t != "" && t != Dots {
			st[c]++
			writeString(b, Dots)
			return
		}
		if ident {
			writeString(b, m.Identifier(t, st))
			return
		}
		writeString(b, m.text(t, stack, st))
	case json.Number:
		if c := leafHidden(); c != "" {
			st[c]++
			writeString(b, Dots)
			return
		}
		b.WriteString(string(t)) // as written: never rounded
	case bool:
		if c := leafHidden(); c != "" {
			st[c]++
			writeString(b, Dots)
			return
		}
		b.WriteString(strconv.FormatBool(t))
	default: // null
		b.WriteString("null")
	}
}

func writeString(b *bytes.Buffer, s string) {
	enc := json.NewEncoder(b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	b.Truncate(b.Len() - 1) // Encode adds a newline
}

// --- keys --------------------------------------------------------------

// IdentifierKey: a field that holds an identifier a caller passes back (an
// artifact or message ID, a loop or task ID, a hash, a version). Its value
// is masked by the identifier detectors only (Identifier): an ID with an
// e-mail address or a token in it is still masked, a digit run is not.
func IdentifierKey(k string) bool {
	l := strings.ToLower(k)
	switch l {
	case "id", "guid", "fingerprint", "sha256", "hash", "etag", "ref", "version", "pid":
		return true
	}
	return strings.HasSuffix(l, "id") && !strings.HasSuffix(l, "paid") || strings.HasSuffix(l, "guid") ||
		strings.HasSuffix(l, "ids") || strings.HasSuffix(l, "sha256") || strings.HasSuffix(l, "version")
}

// ReferenceKey: a field that names security material instead of holding it
// (credentialName, keyAlias, tokenServiceUrl, passwordType): a sensitive
// name does not mask it whole; the detectors still run.
func ReferenceKey(k string) bool {
	l := strings.ToLower(k)
	for _, suffix := range []string{"name", "alias", "url", "uri", "type", "kind", "count", "expiry", "expires", "length"} {
		if strings.HasSuffix(l, suffix) {
			return true
		}
	}
	return false
}

// pairNames and pairValues are the keys of name/value objects: {"key":
// "SFTP_Password", "value": …}, {"Name": …, "Value": …}, OData parameters.
var (
	pairNames  = []string{"name", "key", "id", "parameterkey"}
	pairValues = []string{"value", "parametervalue"}
)

// SensitivePair reports whether an object is a name/value pair of a
// sensitive name (keys compared in any case): any of its name keys (name,
// key, id, parameterKey) holds a sensitive name. Its value keys
// (IsPairValue) are then masked whole, whatever their type.
func (m *Masker) SensitivePair(keys []string, str func(k string) (string, bool)) bool {
	hasValue, sensitive := false, false
	for _, k := range keys {
		l := strings.ToLower(k)
		switch {
		case slices.Contains(pairNames, l):
			if n, ok := str(k); ok && m.Sensitive(n) {
				sensitive = true
			}
		case slices.Contains(pairValues, l):
			hasValue = true
		}
	}
	return hasValue && sensitive
}

// IsPairValue: the value key of a name/value object (value, parameterValue,
// in any case).
func IsPairValue(k string) bool { return slices.Contains(pairValues, strings.ToLower(k)) }

// Identifier masks an identifier field's value with the identifier
// detectors (not card and phone, which take the digit runs of IDs).
func (m *Masker) Identifier(s string, st Stats) string {
	if len(s) < 6 && len(m.regexes) == 0 {
		return s
	}
	digits := 0
	for i := 0; i < len(s); i++ {
		if isDigit(s[i]) {
			digits++
		}
	}
	for _, d := range m.detectors {
		if slices.Contains(identifierDetectors, d) && may(d, s, digits) {
			s = detect(d, s, st)
		}
	}
	// an IBAN only as the whole value or a whole word: hashes and GUIDs hold
	// IBAN-like runs
	if slices.Contains(m.detectors, DetectIBAN) && len(s) >= 15 {
		if ibanValid(strings.TrimSpace(s)) {
			st[DetectIBAN]++
			c := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
			s = c[:4] + " " + Dots + " " + Dots + " " + Dots + " " + c[len(c)-4:]
		} else if strings.Contains(s, " ") {
			words := strings.Split(s, " ")
			for i, w := range words {
				if len(w) >= 15 && ibanValid(w) {
					st[DetectIBAN]++
					c := strings.ToUpper(w)
					words[i] = c[:4] + " " + Dots + " " + Dots + " " + Dots + " " + c[len(c)-4:]
				}
			}
			s = strings.Join(words, " ")
		}
	}
	// the caller's own rules apply everywhere
	for i, re := range m.regexes {
		if p := m.prefixes[i]; p != "" && !strings.Contains(s, p) {
			continue
		}
		s = re.ReplaceAllStringFunc(s, func(string) string { st[CountRegex]++; return Dots })
	}
	return s
}

// HidesWhole reports whether the value v under key k is masked whole: k is
// a sensitive name that is no reference (credentialName), and v is a scalar
// or k names the secret itself (password, token, secret). A list or object
// under a collection key like userCredentials is walked instead: its own
// keys decide (credential metadata stays readable, its secrets do not).
func (m *Masker) HidesWhole(k string, v any) bool {
	if !m.Sensitive(k) || ReferenceKey(k) || flagKey(k) && isFlag(k, v) {
		return false
	}
	switch v.(type) {
	case object, []any, map[string]any:
		return !strings.Contains(strings.ToLower(k), "credential")
	}
	return true
}

// flagKey: a field that says something about security material with a
// number or a boolean (tokenExpiresIn: 3600, passwordRequired: true). Only
// such values stay readable; a string under it is masked (cpicli#78).
func flagKey(k string) bool {
	l := strings.ToLower(k)
	return strings.HasSuffix(l, "required") || strings.HasSuffix(l, "expiresin")
}

// isFlag: v is what a flagKey k may show (cpicli#79): a boolean under
// …Required, a whole number of at most ten digits under …ExpiresIn (no card
// number or PIN).
func isFlag(k string, v any) bool {
	if strings.HasSuffix(strings.ToLower(k), "required") {
		_, ok := v.(bool)
		return ok
	}
	switch n := v.(type) {
	case json.Number:
		return reSeconds.MatchString(string(n))
	case int:
		return n >= 0 && n < 1e10
	case int64:
		return n >= 0 && n < 1e10
	case float64:
		return n >= 0 && n < 1e10 && n == float64(int64(n))
	}
	return false
}

var (
	reSeconds    = regexp.MustCompile(`^\d{1,10}$`)
	reJSONNumber = regexp.MustCompile(`^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$`)
)

// scalar reads a bare token of JSON-like text: a boolean, a number, null,
// or else a string (broken text is no flag).
func scalar(t string) any {
	switch {
	case t == "true":
		return true
	case t == "false":
		return false
	case t == "null":
		return nil
	case reJSONNumber.MatchString(t):
		return json.Number(t)
	}
	return t
}

// CredentialContainer: a key whose list or object holds credentials
// (credentials, userCredentials, credential). Inside it only metadata
// (CredentialMetaKey) stays readable; every other value, also under keys
// no rule knows, is masked (cpicli#75).
func CredentialContainer(k string) bool { return strings.Contains(strings.ToLower(k), "credential") }

// credentialMeta are the readable fields of a credential (cpi.UserCredential,
// cpi.OAuth2Credential and the like).
var credentialMeta = []string{"name", "kind", "type", "description", "user", "username", "companyid", "clientid",
	"clientauthentication", "scope", "resource", "audience", "status", "deployedby", "deployedon", "modifiedby",
	"modifiedon", "createdby", "createdon", "lastmodified", "lastmodifiedby", "lastmodifiedtime", "expiry", "expires",
	"validuntil", "alias", "secretenv"}

// CredentialMetaKey: a field of a credential that is metadata, not the
// secret: its name, kind, user, client ID, token URL and the like, and
// reference or identifier fields (…Name, …Url, …Type, …Id).
func CredentialMetaKey(k string) bool {
	return slices.Contains(credentialMeta, strings.ToLower(k)) || ReferenceKey(k) || IdentifierKey(k) && !SensitiveName(k)
}

func isScalar(v any) bool {
	switch v.(type) {
	case object, []any, map[string]any:
		return false
	}
	return true
}

func isContainer(v any) bool { return !isScalar(v) }

// userSensitive: the caller's own sensitive names (Config.Names) match k.
// They always apply, also to credential metadata (cpicli#76).
func (m *Masker) userSensitive(k string) bool {
	for _, re := range m.names {
		if re.MatchString(k) {
			return true
		}
	}
	return false
}

// reEnvName is cpictl's name for a secret's variable (credentials.yml,
// backup: CPI_SECRET_<KIND>_<NAME>); an upper-case value alone could be
// the secret itself (HUNTER2).
var reEnvName = regexp.MustCompile(`^CPI_SECRET_[A-Z0-9_]+$`)

// envNameOK: secretEnv is metadata only while it holds cpictl's variable
// name for the secret; any other value is treated as the secret
// (cpicli#76).
func envNameOK(k string, v any) bool {
	if !strings.EqualFold(k, "secretEnv") {
		return true
	}
	s, ok := v.(string)
	return ok && reEnvName.MatchString(s)
}

// CredentialMetaValue reports whether the value v of key k inside a
// credential container stays readable: k is metadata, no caller name
// matches it, and secretEnv holds a variable name.
func (m *Masker) CredentialMetaValue(k string, v any) bool {
	return CredentialMetaKey(k) && !m.userSensitive(k) && envNameOK(k, v)
}
