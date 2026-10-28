package mcp

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// Payloads (message bodies, attachments, store entries, variables) cost an
// agent many tokens and are often only searched. Tools with max_bytes
// return them inline up to inlinePayloadBytes by default (the result says
// truncated and the full size), and take save_to: the payloads are written
// to that file under the server root and the result names the file, its
// size and SHA-256 instead of the content.
const (
	inlinePayloadBytes = 16 * 1024
	maxPayloadBytes    = 1 << 20
	saveToProp         = "save_to"
	responseOfProp     = "response_of"
	// keptResponses is how many send_test_message responses the server
	// keeps for get_message_content response_of
	keptResponses = 20
)

// responseCache keeps the full responses of the last send_test_message
// calls by message GUID, so that a response cut inline can be read or saved
// without sending the message again.
type responseCache struct {
	mu    sync.Mutex
	guids []string
	body  map[string][]byte
}

func (c *responseCache) put(guid string, b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.body == nil {
		c.body = map[string][]byte{}
	}
	if _, ok := c.body[guid]; !ok {
		c.guids = append(c.guids, guid)
	}
	c.body[guid] = b
	for len(c.guids) > keptResponses {
		delete(c.body, c.guids[0])
		c.guids = c.guids[1:]
	}
}

func (c *responseCache) get(guid string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.body[guid]
	return b, ok
}

// fullInline are tools with max_bytes whose content an agent usually needs
// whole (scripts, mappings, parameters): they keep the default of the Go
// API (64 KiB).
var fullInline = map[string]bool{"get_resource": true, "get_pd_parameters": true}

func hasMaxBytes(t Tool) bool {
	p, ok := t.InputSchema["properties"].(props)
	if !ok {
		return false
	}
	_, ok = p["max_bytes"]
	return ok
}

// withSaveTo adds save_to to the tools with max_bytes and states the inline
// default.
func withSaveTo(tools []Tool) {
	for _, t := range tools {
		if !hasMaxBytes(t) {
			continue
		}
		p := t.InputSchema["properties"].(props)
		def := inlinePayloadBytes
		if fullInline[t.Name] {
			def = 64 * 1024
		}
		p["max_bytes"] = map[string]any{"type": "integer", "minimum": 1, "maximum": maxPayloadBytes,
			"description": fmt.Sprintf("Maximum bytes inline, default %d (cut: truncated, size)", def)}
		p[saveToProp] = str("Write the content (up to 1 MiB) to this file under the root instead; returns path, size, sha256")
		if t.Name == "get_message_content" {
			p[responseOfProp] = str(fmt.Sprintf("Message GUID of a send_test_message of this session (the last %d): its whole response, without sending again", keptResponses))
		}
	}
}

// payloadCall is how the server handles the payloads of one call.
type payloadCall struct {
	saveTo string
	// inline is the limit the server cuts payloads to itself (0: the tool
	// cuts them)
	inline int
	// responseOf is the message GUID of get_message_content response_of
	responseOf string
}

// payloadArgs removes save_to and response_of from the arguments of a tool
// with max_bytes and sets max_bytes: the inline default, or with save_to
// the maximum. send_test_message always gets the whole response (kept for
// response_of); the server cuts it to the inline limit.
func (s *Server) payloadArgs(t Tool, args json.RawMessage) (json.RawMessage, payloadCall, error) {
	var pc payloadCall
	if !hasMaxBytes(t) {
		return args, pc, nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(args, &m) != nil {
		return args, pc, nil
	}
	for _, f := range []struct {
		key string
		to  *string
	}{{saveToProp, &pc.saveTo}, {responseOfProp, &pc.responseOf}} {
		if raw, ok := m[f.key]; ok {
			if err := json.Unmarshal(raw, f.to); err != nil {
				return nil, pc, output.Usagef("invalid arguments: %s must be a string", f.key)
			}
			delete(m, f.key)
		}
	}
	if pc.saveTo != "" {
		if s.Redactor != nil && s.Redactor.RedactPayloads {
			return nil, pc, output.Usagef("save_to is not available: this server redacts message bodies (--redact-payloads)")
		}
		path, err := resolvePath(cmpRoot(s.Root), pc.saveTo)
		if err != nil {
			return nil, pc, err
		}
		pc.saveTo = path
		if _, ok := m["max_bytes"]; !ok {
			m["max_bytes"] = json.RawMessage(fmt.Sprint(maxPayloadBytes))
		}
	} else if t.Name == "send_test_message" {
		pc.inline = inlinePayloadBytes
		if raw, ok := m["max_bytes"]; ok {
			_ = json.Unmarshal(raw, &pc.inline)
		}
		m["max_bytes"] = json.RawMessage(fmt.Sprint(maxPayloadBytes))
	} else if _, ok := m["max_bytes"]; !ok && !fullInline[t.Name] {
		m["max_bytes"] = json.RawMessage(fmt.Sprint(inlinePayloadBytes))
	}
	if pc.responseOf != "" {
		var mb int
		if raw, ok := m["max_bytes"]; ok {
			_ = json.Unmarshal(raw, &mb)
		}
		pc.inline = mb
	}
	out, err := json.Marshal(m)
	return out, pc, err
}

// storedResponse answers get_message_content response_of from the kept
// responses.
func (s *Server) storedResponse(pc payloadCall) (any, error) {
	b, ok := s.responses.get(pc.responseOf)
	if !ok {
		return nil, output.Usagef("no response of message %s: only the responses of the last %d send_test_message calls of this server session are kept", pc.responseOf, keptResponses)
	}
	return map[string]any{"messageGuid": pc.responseOf, "response": ops.NewContent(b, pc.inline)}, nil
}

// keepResponse keeps the whole response of a send_test_message result.
func (s *Server) keepResponse(value any) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	var r struct {
		MessageGuid string         `json:"messageGuid"`
		Response    map[string]any `json:"response"`
	}
	if json.Unmarshal(data, &r) != nil || r.MessageGuid == "" || r.Response == nil {
		return
	}
	if b, ok := contentBytes(r.Response); ok && r.Response["truncated"] != true {
		s.responses.put(r.MessageGuid, b)
	}
}

// cutPayloads cuts the contents of a result to limit bytes (limit > 0) and
// says, before the content, that it was cut and how to get the rest.
func cutPayloads(value any, limit int, hint string) any {
	data, err := json.Marshal(value)
	if err != nil || !strings.Contains(string(data), `"size"`) {
		return value
	}
	var v any
	if json.Unmarshal(data, &v) != nil {
		return value
	}
	changed := false
	var walk func(any) any
	walk = func(x any) any {
		switch t := x.(type) {
		case map[string]any:
			if b, ok := contentBytes(t); ok {
				size, _ := t["size"].(float64)
				if limit > 0 && len(b) > limit {
					c := ops.NewContent(b, limit)
					c.Size = int(size)
					raw, _ := json.Marshal(c)
					t = map[string]any{}
					_ = json.Unmarshal(raw, &t)
				}
				if t["truncated"] == true {
					shown := len(b)
					if limit > 0 && limit < shown {
						shown = limit
					}
					t["cut"] = fmt.Sprintf("%d of %d bytes shown; %s", shown, int(size), hint)
					changed = true
				}
				return t
			}
			for k, e := range t {
				t[k] = walk(e)
			}
		case []any:
			for i, e := range t {
				t[i] = walk(e)
			}
		}
		return x
	}
	v = walk(v)
	if !changed {
		return value
	}
	return v
}

func cmpRoot(root string) string {
	if root == "" {
		return "."
	}
	return root
}

// rootDir is the server root, absolute and with symbolic links resolved
// (as resolvePath returns paths).
func (s *Server) rootDir() string {
	abs, err := filepath.Abs(cmpRoot(s.Root))
	if err != nil {
		return cmpRoot(s.Root)
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	return abs
}

// savePayloads writes the contents ({size, text|base64, truncated}) of a
// result to path (path-2, path-3, ... for further ones) and replaces each
// with {size, savedTo, sha256, truncated}.
func (s *Server) savePayloads(value any, path string) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return value, err
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return value, err
	}
	n := 0
	var walkErr error
	var walk func(any) any
	walk = func(x any) any {
		switch t := x.(type) {
		case map[string]any:
			if b, ok := contentBytes(t); ok {
				n++
				file := path
				if n > 1 {
					ext := filepath.Ext(path)
					file = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(path, ext), n, ext)
				}
				if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil && walkErr == nil {
					walkErr = err
				}
				if err := os.WriteFile(file, b, 0o644); err != nil && walkErr == nil {
					walkErr = err
				}
				sum := sha256.Sum256(b)
				shown := file
				if rel, err := filepath.Rel(s.rootDir(), file); err == nil {
					shown = filepath.ToSlash(rel)
				}
				saved := map[string]any{"size": t["size"], "savedTo": shown, "savedBytes": len(b), "sha256": hex.EncodeToString(sum[:])}
				if t["truncated"] == true {
					saved["truncated"] = true
				}
				return saved
			}
			for k, e := range t {
				t[k] = walk(e)
			}
		case []any:
			for i, e := range t {
				t[i] = walk(e)
			}
		}
		return x
	}
	v = walk(v)
	if walkErr != nil {
		return value, walkErr
	}
	if n == 0 {
		return value, nil
	}
	return v, nil
}

// contentBytes returns the bytes of an ops.Content in a decoded result.
func contentBytes(m map[string]any) ([]byte, bool) {
	if _, ok := m["size"].(float64); !ok || len(m) > 5 {
		return nil, false
	}
	for k := range m {
		if k != "size" && k != "text" && k != "base64" && k != "truncated" && k != "cut" {
			return nil, false
		}
	}
	if t, ok := m["text"].(string); ok {
		return []byte(t), true
	}
	if b, ok := m["base64"].(string); ok {
		d, err := base64.StdEncoding.DecodeString(b)
		return d, err == nil
	}
	return []byte{}, true // empty content
}

// messageGuidOf returns the messageGuid of a result.
func messageGuidOf(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	var r struct {
		MessageGuid string `json:"messageGuid"`
	}
	_ = json.Unmarshal(data, &r)
	return r.MessageGuid
}
