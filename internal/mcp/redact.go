package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/cpars-innovation/cpicli/pkg/mask"
	"github.com/cpars-innovation/cpicli/pkg/secretscan"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Every tool result passes the server's Redactor before it is sent, so that a
// new tool is covered without its own masking code (cpicli#70, #71). The
// result is rewritten as JSON:
//
//   - name/value lists (objects with string "name" and "value": custom header
//     properties, adapter attributes, step properties, trace headers and
//     exchange properties): the value is replaced by Masked when the name
//     looks sensitive (mask.SensitiveName) or matches a --mask-pattern or a
//     name of --mask-config;
//   - name → value maps under "keys", "headers" and "properties" (the key
//     headers of get_trace_tree): the same by key;
//   - the "custom header <name> = "<value>"" filter text of
//     find_messages_by_header: the value, by the same rule;
//   - with RedactPayloads (the default; --send-payloads turns it off):
//     message bodies (send_test_message response, attachments, message store
//     entries, trace payloads, data store entries, variables) become a
//     placeholder with their size; message error texts (errorText, a step's
//     error, an error group's sample) too; and business keys (the values of
//     customHeaderProperties and of a trace tree's keys) read Masked;
//   - every other string passes the value masker (pkg/mask: personal data,
//     secret shapes, --mask-config rules);
//   - strings that are absolute paths inside the server root become
//     root-relative, slash-separated paths.

// Masked replaces a masked value (mask.Dots). A tool argument that carries
// it is refused by write tools: a masked view must not be written back.
const Masked = mask.Dots

// RedactedPayload starts a withheld payload's placeholder.
const RedactedPayload = "[payload withheld"

// HintMCP says how to get withheld data in cpictl mcp.
const HintMCP = "start cpictl mcp with --send-payloads to include it"

// withheld is the placeholder of a payload or error text of n bytes.
func withheld(what string, n int, hint string) string {
	if hint == "" {
		hint = HintMCP
	}
	return fmt.Sprintf("[%s withheld: %s bytes; %s]", what, groupDigits(n), hint)
}

func groupDigits(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// payloadTools return message bodies (redacted with RedactPayloads).
var payloadTools = []string{"send_test_message", "get_message_attachment", "get_message_store_entry", "get_trace_message",
	"get_data_store_entry", "get_variable", "run_tests", "run_script_cases_online",
	// the tools that cover deprecated payload tools (consolidate)
	"get_message_content", "get_message_trace", "list_variables"}

// redactFailure blanks what a run_tests failure quotes from the message
// (response excerpts, stub call bodies); the expectation and the expected
// value stay.
func redactFailure(n map[string]any, hint string) bool {
	exp, ok := n["expectation"].(string)
	if !ok {
		return false
	}
	if !strings.HasPrefix(exp, "response") && !strings.HasPrefix(exp, "stub_calls") && exp != "http_status" && exp != "error_contains" {
		return false
	}
	changed := false
	for _, k := range []string{"actual", "message"} {
		if s, ok := n[k].(string); ok && s != "" && !strings.HasPrefix(s, RedactedPayload) {
			n[k], changed = withheld("payload", len(s), hint), true
		}
	}
	return changed
}

// nameValueMaps are object fields whose value is a name → value map.
var nameValueMaps = []string{"keys", "headers", "properties"}

var headerFilter = regexp.MustCompile(`^(custom header )(.+?)( = )(".*")$`)

// Redactor rewrites tool results before they are sent.
type Redactor struct {
	// MaskPatterns mask the values of matching names in addition to the
	// built-in sensitive names (authorization, cookies, tokens, passwords,
	// secrets, credentials, API keys).
	MaskPatterns []*regexp.Regexp
	// RedactPayloads withholds message bodies and error texts, and masks
	// business keys. Read it with Withholding: set_masking changes it.
	RedactPayloads bool
	// Masker masks every other string (nil: mask.Default()); Config is
	// what it was built from (get_masking shows it). Read them with
	// Policy: set_masking changes them.
	Masker *mask.Masker
	Config mask.Config
	// Root makes absolute paths inside it root-relative ("" : unchanged).
	Root string
	// Hint ends a withheld placeholder: how to get the data (HintMCP).
	Hint string

	mu    sync.RWMutex // RedactPayloads, Masker, Config
	once  sync.Once
	roots []string // Root, absolute and with symlinks resolved
}

// Withholding reports whether payloads and error texts are withheld now.
func (r *Redactor) Withholding() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.RedactPayloads
}

// Policy returns the masking policy in force.
func (r *Redactor) Policy() (cfg mask.Config, withhold bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.Config, r.RedactPayloads
}

// SetPolicy replaces the masking policy; an invalid config changes nothing.
func (r *Redactor) SetPolicy(cfg mask.Config, withhold bool) error {
	return r.UpdatePolicy(func(mask.Config, bool) (mask.Config, bool, error) { return cfg, withhold, nil })
}

// UpdatePolicy changes the policy atomically: change gets the policy in
// force and returns the next one, all under one lock, so that concurrent
// set_masking calls cannot lose a change (cpicli#75). An error or an
// invalid config changes nothing.
func (r *Redactor) UpdatePolicy(change func(cfg mask.Config, withhold bool) (mask.Config, bool, error)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cfg, withhold, err := change(r.Config, r.RedactPayloads)
	if err != nil {
		return err
	}
	m, err := mask.New(cfg)
	if err != nil {
		return err
	}
	r.Masker, r.Config, r.RedactPayloads = m, cfg, withhold
	return nil
}

// NewRedactor compiles the mask patterns (Go regular expressions, matched
// case-insensitively against names). masker nil masks with every built-in
// detector.
func NewRedactor(root string, maskPatterns []string, redactPayloads bool, masker *mask.Masker) (*Redactor, error) {
	r := &Redactor{Root: root, RedactPayloads: redactPayloads, Masker: masker}
	for _, p := range maskPatterns {
		re, err := regexp.Compile("(?i)" + p)
		if err != nil {
			return nil, err
		}
		r.MaskPatterns = append(r.MaskPatterns, re)
	}
	return r, nil
}

func (r *Redactor) masker() *mask.Masker {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.Masker == nil {
		return defaultMasker
	}
	return r.Masker
}

var defaultMasker = mask.Default()

// sensitive reports whether the value of name is masked.
func (r *Redactor) sensitive(name string) bool { return r.sensitiveWith(r.masker(), name) }

func (r *Redactor) sensitiveWith(m *mask.Masker, name string) bool {
	return m.Sensitive(name) || slices.ContainsFunc(r.MaskPatterns, func(re *regexp.Regexp) bool { return re.MatchString(name) })
}

// Apply returns v with sensitive values masked, payloads redacted (if
// enabled) and paths made root-relative. v itself is not changed; it is
// returned as is when nothing needs to change.
func (r *Redactor) Apply(tool string, v any) any {
	out, _ := r.ApplyStats(tool, v)
	return out
}

// ApplyStats is Apply, also returning how many values were masked per
// detector (never the values).
func (r *Redactor) ApplyStats(tool string, v any) (any, mask.Stats) {
	st := mask.Stats{}
	if v == nil {
		return nil, st
	}
	data, err := json.Marshal(v)
	if err != nil {
		return v, st
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var g any
	if dec.Decode(&g) != nil {
		return v, st
	}
	return r.apply(v, g, slices.Contains(payloadTools, tool), st)
}

// ApplyPayloads is ApplyStats for a result that is message data (payloads
// holds whether bodies are withheld), whatever the tool: the CLI's
// commands.
func (r *Redactor) ApplyPayloads(v any, payloads bool) (any, mask.Stats) {
	st := mask.Stats{}
	if v == nil {
		return nil, st
	}
	data, err := json.Marshal(v)
	if err != nil {
		return v, st
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var g any
	if dec.Decode(&g) != nil {
		return v, st
	}
	return r.apply(v, g, payloads, st)
}

func (r *Redactor) apply(v, g any, payloads bool, st mask.Stats) (any, mask.Stats) {
	// one policy for the whole result, also when set_masking runs meanwhile
	withhold, m := r.Withholding(), r.masker()
	w := walker{r: r, m: m, payloads: withhold && payloads, messageData: withhold, st: st}
	out, changed := w.walk(g, "")
	if !changed {
		return v, st
	}
	return out, st
}

type walker struct {
	r           *Redactor
	m           *mask.Masker
	payloads    bool // withhold message bodies (payload tools)
	messageData bool // withhold error texts, mask business keys (every tool)
	st          mask.Stats
}

func (w walker) walk(node any, key string) (any, bool) {
	r := w.r
	switch n := node.(type) {
	case map[string]any:
		changed := false
		// name/value objects in any spelling: {"name","value"}, {"key","value"}
		// (parameters), {"id","value"} (Partner Directory), {"Name","Value"}
		var nameKey, valueKey string
		for k := range n {
			switch l := strings.ToLower(k); l {
			case "name", "key", "id", "parameterkey":
				if nameKey == "" || l == "name" || l == "key" {
					nameKey = k
				}
			case "value", "parametervalue":
				valueKey = k
			}
		}
		if name, ok := n[nameKey].(string); ok && valueKey != "" {
			if value, ok := n[valueKey].(string); ok && value != "" && value != Masked && r.sensitiveWith(w.m, name) {
				n[valueKey], changed = Masked, true
				w.st[mask.CountName]++
			}
		}
		if w.payloads && (redactContent(n, r.Hint) || redactFailure(n, r.Hint)) {
			changed = true
		}
		if w.messageData && w.withholdMessageData(n) {
			changed = true
		}
		if maskScriptLines(n, w.st) {
			changed = true
		}
		for k, child := range n {
			if m, ok := child.(map[string]any); ok && slices.Contains(nameValueMaps, k) {
				for name, value := range m {
					if s, ok := value.(string); ok && s != "" && s != Masked && (r.sensitiveWith(w.m, name) || w.messageData && k == "keys") {
						m[name], changed = Masked, true
						w.st[mask.CountName]++
					}
				}
			}
			if s, ok := child.(string); ok && k == "filter" {
				if m := headerFilter.FindStringSubmatch(s); m != nil && (r.sensitiveWith(w.m, m[2]) || w.messageData) {
					n[k], changed = m[1]+m[2]+m[3]+`"`+Masked+`"`, true
					continue
				}
			}
			if v, isString := child.(string); isString && v != "" && v != Masked && !mask.ReferenceKey(k) && r.sensitiveWith(w.m, k) {
				n[k], changed = Masked, true
				w.st[mask.CountName]++
				continue
			}
			if v, isString := child.(string); isString && mask.IdentifierKey(k) {
				// round-trip values: only the identifier detectors and the
				// caller's rules (an e-mail or a token in an ID is masked)
				if rel, ok := r.relative(v); ok {
					n[k], changed = rel, true
				} else if out := w.m.Identifier(v, w.st); out != v {
					n[k], changed = out, true
				}
				continue
			}
			if c, ok := w.walk(child, k); ok {
				n[k], changed = c, true
			}
		}
		return n, changed
	case []any:
		changed := false
		for i, child := range n {
			if w.messageData && key == "customHeaderProperties" {
				if m, ok := child.(map[string]any); ok {
					if s, ok := m["value"].(string); ok && s != "" && s != Masked {
						m["value"], changed = Masked, true
						w.st[mask.CountName]++
					}
				}
			}
			if c, ok := w.walk(child, key); ok {
				n[i], changed = c, true
			}
		}
		return n, changed
	case string:
		if rel, ok := r.relative(n); ok {
			return rel, true
		}
		if strings.HasPrefix(n, "[") && strings.Contains(n, " withheld: ") {
			return n, false
		}
		out, st := w.m.Text(n)
		if st.Total() > 0 {
			w.st.Add(st)
			return out, true
		}
	}
	return node, false
}

// withholdMessageData replaces message error texts in n: errorText (message
// logs, trace trees), a step's error (an object with stepId) and an error
// group's sample (an object with fingerprint).
func (w walker) withholdMessageData(n map[string]any) bool {
	changed := false
	hold := func(k string) {
		if s, ok := n[k].(string); ok && s != "" && !strings.HasPrefix(s, "[error text withheld") {
			n[k], changed = withheld("error text", len(s), w.r.Hint), true
			w.st["error-text"]++
		}
	}
	hold("errorText")
	if _, ok := n["stepId"]; ok {
		hold("error")
	}
	if _, ok := n["fingerprint"]; ok {
		hold("sample")
	}
	return changed
}

// redactContent replaces the body of an ops.Content object ("size" and
// "text" or "base64") with the placeholder, and adds its sha256.
func redactContent(n map[string]any, hint string) bool {
	if _, ok := n["size"].(json.Number); !ok {
		return false
	}
	text, isText := n["text"].(string)
	b64, isB64 := n["base64"].(string)
	if !isText && !isB64 {
		return false
	}
	body := []byte(text)
	if isB64 {
		if b, err := base64.StdEncoding.DecodeString(b64); err == nil {
			body = b
		} else {
			body = []byte(b64)
		}
		delete(n, "base64")
	}
	n["text"], n["redacted"] = withheld("payload", len(body), hint), true
	return true
}

// rootPaths returns the root as given, absolute and with symlinks resolved
// (resolvePath returns resolved paths).
func (r *Redactor) rootPaths() []string {
	r.once.Do(func() {
		if r.Root == "" {
			return
		}
		abs, err := filepath.Abs(r.Root)
		if err != nil || abs == string(os.PathSeparator) {
			return
		}
		r.roots = []string{abs}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil && resolved != abs {
			r.roots = append(r.roots, resolved)
		}
	})
	return r.roots
}

// relative returns s as a root-relative, slash-separated path if s is an
// absolute path inside the root.
func (r *Redactor) relative(s string) (string, bool) {
	for _, root := range r.rootPaths() {
		if s == root {
			return ".", true
		}
		if rest, ok := strings.CutPrefix(s, root+string(os.PathSeparator)); ok {
			return filepath.ToSlash(rest), true
		}
	}
	return s, false
}

// relativeText makes the paths inside the root in a text (an error message)
// root-relative; the quoted root itself becomes ".".
func (r *Redactor) relativeText(s string) string {
	for _, root := range r.rootPaths() {
		s = strings.ReplaceAll(s, root+string(os.PathSeparator), "")
		s = strings.ReplaceAll(s, `"`+root+`"`, `"."`)
		// %q doubles the backslashes of Windows paths
		if quoted := strconv.Quote(root); quoted != `"`+root+`"` {
			s = strings.ReplaceAll(s, strings.TrimSuffix(quoted, `"`)+strings.Trim(strconv.Quote(string(os.PathSeparator)), `"`), `"`)
			s = strings.ReplaceAll(s, quoted, `"."`)
		}
	}
	return s
}

// maskScriptLines masks the lines of a script's text that hold a
// hard-coded secret (secretscan.LineSecret, cpicli#74): an object with a
// script file name under name, path or file and its text under text.
func maskScriptLines(n map[string]any, st mask.Stats) bool {
	text, ok := n["text"].(string)
	if !ok || text == "" {
		return false
	}
	for _, k := range []string{"name", "path", "file"} {
		if p, ok := n[k].(string); ok && secretscan.IsScript(p) {
			out, lines := secretscan.MaskLines(p, text, Masked)
			if lines == 0 {
				return false
			}
			n["text"] = out
			st["script-secret"] += lines
			return true
		}
	}
	return false
}
