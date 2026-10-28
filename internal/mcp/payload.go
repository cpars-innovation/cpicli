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

	"github.com/cpars-innovation/cpicli/internal/output"
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
)

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
	}
}

// payloadArgs removes save_to from the arguments of a tool with max_bytes
// and sets max_bytes: the inline default, or with save_to the maximum.
func (s *Server) payloadArgs(t Tool, args json.RawMessage) (json.RawMessage, string, error) {
	if !hasMaxBytes(t) {
		return args, "", nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(args, &m) != nil {
		return args, "", nil
	}
	var saveTo string
	if raw, ok := m[saveToProp]; ok {
		if err := json.Unmarshal(raw, &saveTo); err != nil {
			return nil, "", output.Usagef("invalid arguments: save_to must be a string")
		}
		delete(m, saveToProp)
	}
	if saveTo != "" {
		if s.Redactor != nil && s.Redactor.RedactPayloads {
			return nil, "", output.Usagef("save_to is not available: this server redacts message bodies (--redact-payloads)")
		}
		path, err := resolvePath(cmpRoot(s.Root), saveTo)
		if err != nil {
			return nil, "", err
		}
		saveTo = path
		if _, ok := m["max_bytes"]; !ok {
			m["max_bytes"] = json.RawMessage(fmt.Sprint(maxPayloadBytes))
		}
	} else if _, ok := m["max_bytes"]; !ok && !fullInline[t.Name] {
		m["max_bytes"] = json.RawMessage(fmt.Sprint(inlinePayloadBytes))
	}
	out, err := json.Marshal(m)
	return out, saveTo, err
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
	if _, ok := m["size"].(float64); !ok || len(m) > 4 {
		return nil, false
	}
	for k := range m {
		if k != "size" && k != "text" && k != "base64" && k != "truncated" {
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
