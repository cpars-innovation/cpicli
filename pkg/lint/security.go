package lint

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/cpars-innovation/cpicli/pkg/iflow"
)

// Security material (passwords, tokens, keys a flow reads from the secure
// store or keystore) may only live in the headers and properties the
// project names for it (Config.Security). Those names are then checked
// where messages leave the flow: receivers that pass headers on, the
// message processing log, the message body, and the senders that may set
// headers.

// SecurityConfig names where a flow may keep security material. The names
// of .cpi/lint.yaml add to the defaults: a project declares its own
// authentication headers (X-Partner-Token) without repeating Authorization.
type SecurityConfig struct {
	// SecretHeaders are the headers a flow may put security material into
	// (case-insensitive, * matches any characters).
	SecretHeaders []string `yaml:"secretHeaders" json:"secretHeaders"`
	// SecretProperties are the exchange properties a flow may put security
	// material into. Prefer properties: no channel sends them.
	SecretProperties []string `yaml:"secretProperties" json:"secretProperties"`
}

// defaultSecurity: the headers that carry credentials by definition, and
// properties starting with secret.
func defaultSecurity() SecurityConfig {
	return SecurityConfig{
		SecretHeaders:    []string{"Authorization", "Proxy-Authorization", "X-API-Key", "APIKey"},
		SecretProperties: []string{"secret*"},
	}
}

// withDefaults adds the default names to the configured ones.
func (c SecurityConfig) withDefaults() SecurityConfig {
	d := defaultSecurity()
	merge := func(defaults, own []string) []string {
		out := slices.Clone(defaults)
		for _, n := range own {
			if n = strings.TrimSpace(n); n != "" && !slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, n) }) {
				out = append(out, n)
			}
		}
		return out
	}
	return SecurityConfig{SecretHeaders: merge(d.SecretHeaders, c.SecretHeaders), SecretProperties: merge(d.SecretProperties, c.SecretProperties)}
}

func nameMatches(patterns []string, name string) bool {
	for _, p := range patterns {
		if globMatch(strings.ToLower(p), strings.ToLower(name)) {
			return true
		}
	}
	return false
}

// globMatch matches * against any characters (header names contain
// characters path.Match treats specially, so it is not used).
func globMatch(pattern, name string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == name
	}
	if !strings.HasPrefix(name, parts[0]) {
		return false
	}
	name = name[len(parts[0]):]
	for i, p := range parts[1:] {
		if i == len(parts)-2 {
			return strings.HasSuffix(name, p)
		}
		j := strings.Index(name, p)
		if j < 0 {
			return false
		}
		name = name[j+len(p):]
	}
	return true
}

func (c SecurityConfig) secretHeader(name string) bool { return nameMatches(c.SecretHeaders, name) }
func (c SecurityConfig) secretProperty(name string) bool {
	return nameMatches(c.SecretProperties, name)
}

// --- scripts -----------------------------------------------------------------

// secretUse is where a script puts security material.
type secretUse struct {
	file string
	line int
	// sink: header, property, log, body; name: the header or property
	// (empty when computed at runtime) or the log call.
	sink, name string
}

var (
	// reads of security material: SecureStoreService (user credentials,
	// secure parameters, OAuth2 tokens), KeystoreService, AccessTokenAndUser,
	// and the token fields of a token endpoint's response
	reSecretSource = regexp.MustCompile(`\b(getUserCredential|getPassword|getAccessToken\w*|getAcces+TokenFor\w*|getOAuth2Token\w*|getClientSecret|getPrivateKey|getSecret|getSecretKey|getSecureParameter)\s*\(` +
		`|\b(getKey|getKeyPair|getEntry)\s*\(\s*[^)\s]` + // KeyStore.getKey(alias, ...), not Map.Entry.getKey()
		`|\.(password|accessToken|access_token|refreshToken|refresh_token|id_token|clientSecret|client_secret|privateKey)\b`)
	// reads of a header or property by name
	reNamedRead = regexp.MustCompile(`\b(getHeader|getProperty)\s*\(\s*["']([^"']+)["']` +
		`|\b(headers|properties|getHeaders\(\)|getProperties\(\))\s*(?:\.get\s*\(\s*|\[\s*)["']([^"']+)["']`)
	reAssign  = regexp.MustCompile(`^\s*(?:(?:def|final|var|static|[A-Z]\w*(?:<[^>]*>)?(?:\[\])?|char\[\]|byte\[\])\s+)*([a-zA-Z_]\w*)\s*=([^=~].*)$`)
	reSetCall = regexp.MustCompile(`\b(setHeader|setProperty)\s*\(\s*(?:(["'])([^"']*)["']|([^,]+?))\s*,(.*)$`)
	reLogCall = regexp.MustCompile(`\b(addAttachmentAsString|addAttachmentAsBinary|setStringProperty|setIntegerProperty|setBooleanProperty|addCustomHeaderProperty|println|printf|print)\s*\(` +
		`|\b(log|logger|LOG|LOGGER)\s*\.\s*(info|debug|warn|error|trace)\s*\(|\bthrow\s+new\s+\w+\s*\(`)
	reBodyCall = regexp.MustCompile(`\bsetBody\s*\(`)
	reIdent    = regexp.MustCompile(`[A-Za-z_]\w*`)
	// the parts of a credential or key pair that are not secret: the user
	// name, the public key, the certificate (cred.username,
	// getUserCredential("x").getUsername())
	reNonSecretAccess = regexp.MustCompile(`(?:[A-Za-z_]\w*(?:\s*\([^()]*\))?\s*\.\s*)+(?:(?:getUsername|getUser|getPublic|getCertificate\w*|getKind|getAlias|getCredentialType)\s*\(\s*\)|(?:username|user|public|certificate|certificateChain|kind|alias|credentialType)\b)`)
)

// statements joins lines while parentheses are open, so a call spanning
// lines is one statement; each keeps the number of its first line.
func statements(src string) (stmts []string, lines []int) {
	var cur strings.Builder
	depth, first := 0, 0
	for i, l := range strings.Split(src, "\n") {
		if cur.Len() == 0 {
			first = i + 1
		}
		cur.WriteString(l + " ")
		depth += strings.Count(l, "(") - strings.Count(l, ")")
		if depth <= 0 || cur.Len() > 4000 {
			stmts, lines = append(stmts, cur.String()), append(lines, first)
			cur.Reset()
			depth = 0
		}
	}
	if cur.Len() > 0 {
		stmts, lines = append(stmts, cur.String()), append(lines, first)
	}
	return stmts, lines
}

// secretUses follows security material through a script: values read from
// the secure store, keystore or a secret header/property, and variables
// assigned from them, into headers, properties, logs and the body.
func secretUses(file, src string, sec SecurityConfig) []secretUse {
	stmts, lines := statements(string(blankComments([]byte(src))))
	tainted := map[string]bool{}
	carries := func(expr string) bool {
		expr = reNonSecretAccess.ReplaceAllString(expr, " ")
		if reSecretSource.MatchString(expr) {
			return true
		}
		for _, m := range reNamedRead.FindAllStringSubmatch(expr, -1) {
			name, kind := m[2], m[1]
			if name == "" {
				name, kind = m[4], m[3]
			}
			if strings.Contains(strings.ToLower(kind), "header") && sec.secretHeader(name) ||
				strings.Contains(strings.ToLower(kind), "propert") && sec.secretProperty(name) {
				return true
			}
		}
		for _, id := range reIdent.FindAllString(stringsBlanked(expr), -1) {
			if tainted[id] {
				return true
			}
		}
		return false
	}
	// assignments until nothing changes (a variable may be assigned from
	// one declared further down in a closure)
	for changed, round := true, 0; changed && round < 10; round++ {
		changed = false
		for _, s := range stmts {
			if m := reAssign.FindStringSubmatch(s); m != nil && !tainted[m[1]] && carries(m[2]) {
				tainted[m[1]] = true
				changed = true
			}
		}
	}
	var out []secretUse
	for i, s := range stmts {
		if m := reSetCall.FindStringSubmatch(s); m != nil && carries(m[5]) {
			sink := "header"
			if m[1] == "setProperty" {
				sink = "property"
			}
			out = append(out, secretUse{file: file, line: lines[i], sink: sink, name: m[3]})
			continue
		}
		if loc := reLogCall.FindStringIndex(s); loc != nil && carries(s[loc[1]:]) {
			out = append(out, secretUse{file: file, line: lines[i], sink: "log", name: strings.TrimRight(strings.TrimSpace(s[loc[0]:loc[1]]), "(")})
			continue
		}
		if loc := reBodyCall.FindStringIndex(s); loc != nil && carries(s[loc[1]:]) {
			out = append(out, secretUse{file: file, line: lines[i], sink: "body", name: "setBody"})
		}
	}
	return out
}

// scriptSecretUses returns secretUses of each script of a, computed once.
func scriptSecretUses(a *lintArtifact, sec SecurityConfig) map[string][]secretUse {
	if a.secretUses == nil {
		a.secretUses = map[string][]secretUse{}
		for file, data := range a.Scripts {
			a.secretUses[file] = secretUses(file, string(data), sec)
		}
	}
	return a.secretUses
}

var reStringLiteral = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'`)

// stringsBlanked removes string literals, so words inside them are not
// taken for variables (GString ${x} references stay visible).
func stringsBlanked(expr string) string {
	return reStringLiteral.ReplaceAllStringFunc(expr, func(lit string) string {
		var refs []string
		for _, m := range reGStringRef.FindAllStringSubmatch(lit, -1) {
			refs = append(refs, m[1])
		}
		return " " + strings.Join(refs, " ") + " "
	})
}

var reGStringRef = regexp.MustCompile(`\$\{?\s*([A-Za-z_]\w*)`)

// --- content modifiers -------------------------------------------------------

// reExprRef: ${header.X}, ${property.X}, ${in.header.X}, ${exchange.property.X}
var reExprRef = regexp.MustCompile(`\$\{\s*(?:in\.)?(?:exchange\.)?(header|headers|property|properties)\.([^}\s]+)\s*\}`)

// modifierCarries reports whether a content modifier row (type and value)
// copies security material: a secret header or property.
func modifierCarries(sec SecurityConfig, typ, value string) bool {
	switch strings.ToLower(typ) {
	case "header":
		return sec.secretHeader(value)
	case "property":
		return sec.secretProperty(value)
	case "expression":
		return exprCarries(sec, value)
	}
	return false
}

func exprCarries(sec SecurityConfig, expr string) bool {
	for _, m := range reExprRef.FindAllStringSubmatch(expr, -1) {
		if strings.HasPrefix(m[1], "header") && sec.secretHeader(m[2]) || strings.HasPrefix(m[1], "propert") && sec.secretProperty(m[2]) {
			return true
		}
	}
	return false
}

// --- rules -------------------------------------------------------------------

func ruleSecretTarget(lc *lintContext, a *lintArtifact) []Finding {
	sec := lc.cfg.Security
	var out []Finding
	allowed := "allowed names (.cpi/lint.yaml security): headers " + strings.Join(sec.SecretHeaders, ", ") + "; properties " + strings.Join(sec.SecretProperties, ", ")
	for _, file := range sortedKeys(a.Scripts) {
		for _, u := range scriptSecretUses(a, sec)[file] {
			ok := u.sink == "header" && sec.secretHeader(u.name) || u.sink == "property" && sec.secretProperty(u.name)
			if ok || (u.sink != "header" && u.sink != "property") {
				continue
			}
			what := fmt.Sprintf("the %s %s", u.sink, u.name)
			if u.name == "" {
				what = "a " + u.sink + " whose name is computed at runtime"
			}
			out = append(out, a.finding("secret-target-name", file, "", "", fmt.Sprintf("%d", u.line),
				fmt.Sprintf("%s line %d puts security material into %s", path.Base(file), u.line, what),
				declareHint(u.sink, u.name, allowed)))
		}
	}
	eachElement(a, func(file string, m *iflow.Model, el *iflow.Element) {
		if !isContentModifier(el) {
			return
		}
		for _, t := range []struct{ table, sink string }{{"headerTable", "header"}, {"propertyTable", "property"}} {
			for _, r := range tableRows(el.Props[t.table]) {
				if r["Name"] == "" || strings.EqualFold(r["Action"], "Delete") || !modifierCarries(sec, r["Type"], r["Value"]) {
					continue
				}
				if t.sink == "header" && sec.secretHeader(r["Name"]) || t.sink == "property" && sec.secretProperty(r["Name"]) {
					continue
				}
				out = append(out, a.finding("secret-target-name", file, el.ID, el.Name, t.sink+":"+r["Name"],
					fmt.Sprintf("content modifier %q copies security material into the %s %s", el.Name, t.sink, r["Name"]),
					declareHint(t.sink, r["Name"], allowed)))
			}
		}
	})
	return out
}

// declareHint: rename, or declare the name when it is the project's own
// authentication header or secret property.
func declareHint(sink, name, allowed string) string {
	if name == "" {
		return "use a fixed name, one of the " + allowed
	}
	key := "secretHeaders"
	if sink == "property" {
		key = "secretProperties"
	}
	return fmt.Sprintf("use one of the %s; if %s is an authentication %s of this project (a receiver expects it), add it to security.%s in .cpi/lint.yaml", allowed, name, sink, key)
}

// reAuthName: header names that say they carry authentication.
var reAuthName = regexp.MustCompile(`(?i)(auth|token|api[-_]?key|secret|passw|pwd|subscription[-_]?key|signature|session|cookie|credential|bearer)`)

// authLooking reports a header name that reads like an authentication
// header (CSRF tokens are fetched by the adapters, not secrets of the flow).
func authLooking(name string) bool {
	return reAuthName.MatchString(name) && !strings.Contains(strings.ToLower(name), "csrf")
}

// ruleUndeclaredAuthHeader: a header that reads like an authentication
// header (set by the flow, or sent by a receiver by name) is not declared
// in security.secretHeaders, so the secret-* rules do not protect it.
func ruleUndeclaredAuthHeader(lc *lintContext, a *lintArtifact) []Finding {
	sec := lc.cfg.Security
	seen := map[string]string{} // lower name -> name
	where := map[string]string{}
	add := func(name, at string) {
		if name == "" || sec.secretHeader(name) || !authLooking(name) || strings.Contains(name, "*") {
			return
		}
		if _, ok := seen[strings.ToLower(name)]; !ok {
			seen[strings.ToLower(name)], where[strings.ToLower(name)] = name, at
		}
	}
	for _, file := range sortedKeys(a.Scripts) {
		for _, m := range reSetHeaderName.FindAllStringSubmatch(string(blankComments(a.Scripts[file])), -1) {
			add(m[1], "script "+scriptRef(file))
		}
	}
	eachElement(a, func(file string, m *iflow.Model, el *iflow.Element) {
		if !isContentModifier(el) {
			return
		}
		for _, r := range tableRows(el.Props["headerTable"]) {
			if !strings.EqualFold(r["Action"], "Delete") {
				add(r["Name"], fmt.Sprintf("content modifier %q", el.Name))
			}
		}
	})
	for _, file := range sortedModelFiles(a) {
		for _, ad := range a.Models[file].Adapters {
			if ad.Props["direction"] != "Receiver" {
				continue
			}
			lists := headerLists(ad)
			for _, k := range sortedKeys(lists) {
				for _, e := range lists[k] {
					add(e, fmt.Sprintf("receiver %q", channelName(ad)))
				}
			}
		}
	}
	var out []Finding
	for _, k := range sortedKeys(seen) {
		out = append(out, a.finding("undeclared-auth-header", "", "", "", k,
			fmt.Sprintf("the header %s (%s) reads like an authentication header but is not in security.secretHeaders", seen[k], where[k]),
			"if it carries credentials, add it to security.secretHeaders in .cpi/lint.yaml so the secret-* rules protect it; otherwise add an ignore entry"))
	}
	return out
}

func ruleSecretLogged(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	for _, file := range sortedKeys(a.Scripts) {
		for _, u := range scriptSecretUses(a, lc.cfg.Security)[file] {
			if u.sink != "log" {
				continue
			}
			out = append(out, a.finding("secret-logged", file, "", "", fmt.Sprintf("%d", u.line),
				fmt.Sprintf("%s line %d passes security material to %s (message processing log, error text or standard output)", path.Base(file), u.line, u.name),
				"log that the value was read, never the value; error texts end up in the message processing log too"))
		}
	}
	return out
}

func ruleSecretInBody(lc *lintContext, a *lintArtifact) []Finding {
	sec := lc.cfg.Security
	var out []Finding
	for _, file := range sortedKeys(a.Scripts) {
		for _, u := range scriptSecretUses(a, sec)[file] {
			if u.sink != "body" {
				continue
			}
			out = append(out, a.finding("secret-in-body", file, "", "", fmt.Sprintf("%d", u.line),
				fmt.Sprintf("%s line %d writes security material into the message body", path.Base(file), u.line),
				"only for a call that needs it (a token request): replace the body right after that call, and keep it out of Persist steps, data stores, JMS queues and log attachments"))
		}
	}
	eachElement(a, func(file string, m *iflow.Model, el *iflow.Element) {
		if !isContentModifier(el) {
			return
		}
		for _, k := range sortedKeys(el.Props) {
			if k == "headerTable" || k == "propertyTable" || !exprCarries(sec, el.Props[k]) {
				continue
			}
			out = append(out, a.finding("secret-in-body", file, el.ID, el.Name, k,
				fmt.Sprintf("content modifier %q writes a secret header or property into the message body", el.Name),
				"only for a call that needs it (a token request): replace the body right after that call"))
			break
		}
	})
	return out
}

// secretHeadersOf lists the secret headers a flow sets (scripts and
// content modifiers), and those it removes again.
func secretHeadersOf(a *lintArtifact, sec SecurityConfig) (set, removed map[string]bool) {
	set, removed = map[string]bool{}, map[string]bool{}
	for _, file := range sortedKeys(a.Scripts) {
		src := string(blankComments(a.Scripts[file]))
		for _, u := range scriptSecretUses(a, sec)[file] {
			if u.sink == "header" && u.name != "" {
				set[strings.ToLower(u.name)] = true
			}
		}
		for _, m := range reSetHeaderName.FindAllStringSubmatch(src, -1) {
			if sec.secretHeader(m[1]) {
				set[strings.ToLower(m[1])] = true
			}
		}
		for _, m := range reRemoveHeader.FindAllStringSubmatch(src, -1) {
			removed[strings.ToLower(firstNonEmpty(m[1], m[2]))] = true
		}
	}
	eachElement(a, func(file string, m *iflow.Model, el *iflow.Element) {
		if !isContentModifier(el) {
			return
		}
		for _, r := range tableRows(el.Props["headerTable"]) {
			name := strings.ToLower(r["Name"])
			switch {
			case name == "":
			case strings.EqualFold(r["Action"], "Delete"):
				removed[name] = true
			case sec.secretHeader(r["Name"]) || modifierCarries(sec, r["Type"], r["Value"]):
				set[name] = true
			}
		}
	})
	return set, removed
}

var (
	reSetHeaderName = regexp.MustCompile(`\bsetHeader\s*\(\s*["']([^"']+)["']`)
	reRemoveHeader  = regexp.MustCompile(`\bremoveHeader\s*\(\s*["']([^"']+)["']|\bsetHeader\s*\(\s*["']([^"']+)["']\s*,\s*null\s*\)`)
	// a list of header names: Name|Name2, *, x-*
	reHeaderList = regexp.MustCompile(`^\s*[A-Za-z0-9_.*\-]+(\s*\|\s*[A-Za-z0-9_.*\-]+)*\s*$`)
)

// passAllHeaders are receiver adapters that hand every header on: to a
// queue (stored), to another flow, to a broker.
var passAllHeaders = map[string]string{"JMS": "stores the headers with the message in the JMS queue", "ProcessDirect": "passes every header to the called flow",
	"AMQP": "sends headers as message properties to the broker", "Kafka": "sends headers as record headers to the broker"}

// headerLists are the receiver channel's lists of headers it sends
// (Request Headers): every property whose name speaks of headers and whose
// value is a list of header names. SAP names the key differently per
// adapter, so it is recognised by its shape.
func headerLists(ad *iflow.Element) map[string][]string {
	out := map[string][]string{}
	for k, v := range ad.Props {
		lk := strings.ToLower(k)
		if !strings.Contains(lk, "header") || strings.Contains(lk, "response") || !reHeaderList.MatchString(v) {
			continue
		}
		for _, e := range strings.Split(v, "|") {
			out[k] = append(out[k], strings.TrimSpace(e))
		}
	}
	return out
}

func ruleSecretHeaderSent(lc *lintContext, a *lintArtifact) []Finding {
	sec := lc.cfg.Security
	set, removed := secretHeadersOf(a, sec)
	var kept []string
	for h := range set {
		if !removed[h] {
			kept = append(kept, h)
		}
	}
	sort.Strings(kept)
	if len(kept) == 0 {
		return nil
	}
	var out []Finding
	for _, file := range sortedModelFiles(a) {
		for _, ad := range a.Models[file].Adapters {
			if ad.Props["direction"] != "Receiver" {
				continue
			}
			typ := ad.Props["ComponentType"]
			if why, ok := passAllHeaders[typ]; ok {
				out = append(out, a.finding("secret-header-sent", file, ad.ID, channelName(ad), "all",
					fmt.Sprintf("the %s receiver %q %s, including %s, which the flow sets and does not remove", typ, channelName(ad), why, strings.Join(kept, ", ")),
					"remove the header before this channel (content modifier, Delete), or keep the material in a property"))
				continue
			}
			lists := headerLists(ad)
			for _, k := range sortedKeys(lists) {
				for _, e := range lists[k] {
					if !strings.Contains(e, "*") {
						continue // a header named explicitly is meant for this receiver
					}
					var hit []string
					for _, h := range kept {
						if globMatch(strings.ToLower(e), h) {
							hit = append(hit, h)
						}
					}
					if len(hit) == 0 {
						continue
					}
					out = append(out, a.finding("secret-header-sent", file, ad.ID, channelName(ad), k+":"+e,
						fmt.Sprintf("the %s receiver %q sends the headers %q (%s), which includes %s", typ, channelName(ad), e, k, strings.Join(hit, ", ")),
						"list the headers this receiver needs by name; name a secret header only on the channel it is meant for"))
				}
			}
		}
	}
	return out
}

func ruleSecretHeaderInbound(lc *lintContext, a *lintArtifact) []Finding {
	sec := lc.cfg.Security
	var out []Finding
	for _, file := range sortedModelFiles(a) {
		m := a.Models[file]
		if m.Collaboration == nil {
			continue
		}
		for _, e := range strings.Split(m.Collaboration.Props["allowedHeaderList"], "|") {
			e = strings.TrimSpace(e)
			if e == "" || e == "*" {
				continue
			}
			var hit []string
			for _, p := range sec.SecretHeaders {
				if strings.EqualFold(e, p) || strings.Contains(e, "*") && globMatch(strings.ToLower(e), strings.ToLower(p)) {
					hit = append(hit, p)
				}
			}
			if len(hit) == 0 {
				continue
			}
			out = append(out, a.finding("secret-header-inbound", file, "", "", e,
				fmt.Sprintf("Allowed Header(s) of the flow accept %s from senders (entry %q)", strings.Join(hit, ", "), e),
				"senders could set or replace security material the flow relies on: read it from the secure store instead"))
		}
	}
	return out
}
