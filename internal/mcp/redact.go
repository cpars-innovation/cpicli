package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// Every tool result passes the server's Redactor before it is sent, so that a
// new tool is covered without its own masking code. The result is rewritten
// as JSON:
//
//   - name/value lists (objects with string "name" and "value": custom header
//     properties, adapter attributes, step properties, trace headers and
//     exchange properties): the value is replaced by "***" when
//     the name looks sensitive (ops.SensitiveName) or matches a --mask-pattern;
//   - name → value maps under "keys", "headers" and "properties" (the key
//     headers of get_trace_tree): the same by key;
//   - the "custom header <name> = "<value>"" filter text of
//     find_messages_by_header: the value, by the same rule;
//   - with RedactPayloads, message bodies (send_test_message response,
//     attachments, message store entries, trace payloads, data store entries,
//     variables): text/base64 are replaced by a placeholder with size and
//     sha256;
//   - strings that are absolute paths inside the server root become
//     root-relative, slash-separated paths.

// Masked replaces a masked value.
const Masked = "***"

// RedactedPayload replaces a redacted payload.
const RedactedPayload = "[redacted]"

// payloadTools return message bodies (redacted with RedactPayloads).
var payloadTools = []string{"send_test_message", "get_message_attachment", "get_message_store_entry", "get_trace_message",
	"get_data_store_entry", "get_variable"}

// nameValueMaps are object fields whose value is a name → value map.
var nameValueMaps = []string{"keys", "headers", "properties"}

var headerFilter = regexp.MustCompile(`^(custom header )(.+?)( = )(".*")$`)

// Redactor rewrites tool results before they are sent.
type Redactor struct {
	// MaskPatterns mask the values of matching names in addition to the
	// built-in sensitive names (authorization, cookies, tokens, passwords,
	// secrets, credentials, API keys).
	MaskPatterns []*regexp.Regexp
	// RedactPayloads replaces message bodies with a placeholder, the size
	// and the sha256 of the returned bytes.
	RedactPayloads bool
	// Root makes absolute paths inside it root-relative ("" : unchanged).
	Root string

	once  sync.Once
	roots []string // Root, absolute and with symlinks resolved
}

// NewRedactor compiles the mask patterns (Go regular expressions, matched
// case-insensitively against names).
func NewRedactor(root string, maskPatterns []string, redactPayloads bool) (*Redactor, error) {
	r := &Redactor{Root: root, RedactPayloads: redactPayloads}
	for _, p := range maskPatterns {
		re, err := regexp.Compile("(?i)" + p)
		if err != nil {
			return nil, err
		}
		r.MaskPatterns = append(r.MaskPatterns, re)
	}
	return r, nil
}

// sensitive reports whether the value of name is masked.
func (r *Redactor) sensitive(name string) bool {
	return ops.SensitiveName(name) || slices.ContainsFunc(r.MaskPatterns, func(re *regexp.Regexp) bool { return re.MatchString(name) })
}

// Apply returns v with sensitive values masked, payloads redacted (if
// enabled) and paths made root-relative. v itself is not changed; it is
// returned as is when nothing needs to change.
func (r *Redactor) Apply(tool string, v any) any {
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return v
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var g any
	if dec.Decode(&g) != nil {
		return v
	}
	out, changed := r.walk(g, r.RedactPayloads && slices.Contains(payloadTools, tool))
	if !changed {
		return v
	}
	return out
}

func (r *Redactor) walk(node any, payloads bool) (any, bool) {
	switch n := node.(type) {
	case map[string]any:
		changed := false
		if name, ok := n["name"].(string); ok {
			if value, ok := n["value"].(string); ok && value != "" && value != Masked && r.sensitive(name) {
				n["value"], changed = Masked, true
			}
		}
		if payloads && redactContent(n) {
			changed = true
		}
		for k, child := range n {
			if m, ok := child.(map[string]any); ok && slices.Contains(nameValueMaps, k) {
				for name, value := range m {
					if s, ok := value.(string); ok && s != "" && s != Masked && r.sensitive(name) {
						m[name], changed = Masked, true
					}
				}
			}
			if s, ok := child.(string); ok && k == "filter" {
				if m := headerFilter.FindStringSubmatch(s); m != nil && r.sensitive(m[2]) {
					n[k], changed = m[1]+m[2]+m[3]+`"`+Masked+`"`, true
					continue
				}
			}
			if c, ok := r.walk(child, payloads); ok {
				n[k], changed = c, true
			}
		}
		return n, changed
	case []any:
		changed := false
		for i, child := range n {
			if c, ok := r.walk(child, payloads); ok {
				n[i], changed = c, true
			}
		}
		return n, changed
	case string:
		if rel, ok := r.relative(n); ok {
			return rel, true
		}
	}
	return node, false
}

// redactContent replaces the body of an ops.Content object ("size" and
// "text" or "base64") with the placeholder, and adds its sha256.
func redactContent(n map[string]any) bool {
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
	sum := sha256.Sum256(body)
	n["text"], n["sha256"], n["redacted"] = RedactedPayload, hex.EncodeToString(sum[:]), true
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
	}
	return s
}
