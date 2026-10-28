package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// handlerSized are tools whose handler applies max_bytes to content that is
// not a payload of its own (Partner Directory values in an inspection):
// they get the agent's max_bytes (default 64 KiB) instead of the whole
// content.
var handlerSized = map[string]bool{"get_pd_parameters": true}

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
		p["max_bytes"] = map[string]any{"type": "integer", "minimum": 1, "maximum": maxInlineBytes,
			"description": fmt.Sprintf("Bytes inline, default %d; the rest by ref (get_content)", inlineDefault(t.Name))}
		p[saveToProp] = str("Write the whole content to this file under the root instead")
		if t.Name == "get_message_content" {
			p[responseOfProp] = str("Message GUID of a send_test_message: its whole stored response, without sending again")
		}
	}
}

func inlineDefault(tool string) int {
	if fullInline[tool] || handlerSized[tool] {
		return 64 * 1024
	}
	return inlinePayloadBytes
}

// payloadCall is how the server handles the payloads of one call.
type payloadCall struct {
	saveTo string // resolved path
	inline int    // bytes inline
	// responseOf is the message GUID of get_message_content response_of
	responseOf string
}

// payloadArgs takes save_to, response_of and max_bytes from the arguments
// of a tool with max_bytes: the handler reads the whole content (the view
// step stores it and shows max_bytes inline), except for handlerSized tools.
func (s *Server) payloadArgs(t Tool, args json.RawMessage) (json.RawMessage, payloadCall, error) {
	pc := payloadCall{inline: inlineDefault(t.Name)}
	if !hasMaxBytes(t) || t.Name == "get_content" {
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
	if raw, ok := m["max_bytes"]; ok {
		if err := json.Unmarshal(raw, &pc.inline); err != nil || pc.inline < 1 || pc.inline > maxInlineBytes {
			return nil, pc, output.Usagef("max_bytes must be between 1 and %d (save_to or get_content for more)", maxInlineBytes)
		}
	}
	if pc.saveTo != "" {
		if s.redacting() {
			return nil, pc, output.Usagef("save_to is not available: this server redacts message bodies (--redact-payloads)")
		}
		path, err := resolvePath(cmpRoot(s.Root), pc.saveTo)
		if err != nil {
			return nil, pc, err
		}
		pc.saveTo = path
	}
	if handlerSized[t.Name] {
		m["max_bytes"] = json.RawMessage(fmt.Sprint(pc.inline))
	} else {
		m["max_bytes"] = json.RawMessage(fmt.Sprint(fetchBytes))
	}
	out, err := json.Marshal(m)
	return out, pc, err
}

func (s *Server) redacting() bool { return s.Redactor != nil && s.Redactor.RedactPayloads }

// storedResponse answers get_message_content response_of from the store.
func (s *Server) storedResponse(guid string) (any, error) {
	if s.redacting() {
		return nil, output.Usagef("response_of is not available: this server redacts message bodies (--redact-payloads)")
	}
	ref, err := s.contentStore().responseOf(guid)
	if err != nil {
		return nil, err
	}
	b, err := s.contentStore().Get(ref)
	if err != nil {
		return nil, err
	}
	return map[string]any{"messageGuid": guid, "response": ops.NewContent(b, -1)}, nil
}

// contentBytes returns the bytes of an ops.Content ({size, text | base64,
// truncated}), also inside a larger object (such as a message store entry
// with its id).
func contentBytes(v *jv) ([]byte, bool) {
	if !v.isObject() {
		return nil, false
	}
	if _, ok := v.get("size").number(); !ok {
		return nil, false
	}
	if t := v.get("text"); t != nil && t.kind == 's' {
		return []byte(t.text()), true
	}
	if b := v.get("base64"); b != nil && b.kind == 's' {
		d, err := base64.StdEncoding.DecodeString(b.text())
		return d, err == nil
	}
	for _, k := range v.keys {
		if k != "size" && k != "truncated" {
			return nil, false
		}
	}
	return []byte{}, true // empty content
}

// setPayload replaces the payload fields of a content object (text, base64,
// truncated) with fields, after its other fields.
func setPayload(v *jv, fields *jv) {
	for _, k := range []string{"text", "base64", "truncated"} {
		v.del(k)
	}
	for i, k := range fields.keys {
		v.set(k, fields.vals[i])
	}
}

// contents applies the payload rules to every content of a result: with
// save_to it is written to the file; above the inline limit it is stored
// and shown as {ref, size, cut, first bytes}. A send_test_message response
// is always stored (get_message_content response_of).
func (s *Server) contents(tool string, pc payloadCall, v *jv) (*jv, error) {
	if tool == "send_test_message" && !s.redacting() {
		if resp := v.get("response"); resp != nil {
			if b, ok := contentBytes(resp); ok {
				if ref, _, err := s.contentStore().Put(b); err == nil {
					s.contentStore().name(v.get("messageGuid").text(), ref)
				}
			}
		}
	}
	n := 0
	var walk func(v *jv) error
	walk = func(v *jv) error {
		b, ok := contentBytes(v)
		if !ok {
			for _, child := range v.vals {
				if child.isObject() || child.isArray() {
					if err := walk(child); err != nil {
						return err
					}
				}
			}
			return nil
		}
		size, _ := v.get("size").number()
		fetchCut := v.get("truncated").isTrue() // the server could not read it all
		if pc.saveTo != "" {
			n++
			file := pc.saveTo
			if n > 1 {
				ext := filepath.Ext(file)
				file = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(file, ext), n, ext)
			}
			saved, err := saveContent(s.Root, file, "", b)
			if err != nil {
				return err
			}
			fields := jvObject()
			fields.set("savedTo", jvString(saved["savedTo"].(string)))
			fields.set("sha256", jvString(saved["sha256"].(string)))
			if fetchCut {
				fields.set("cut", jvString(fmt.Sprintf("only the first %d of %d bytes could be read", len(b), int(size))))
			}
			setPayload(v, fields)
			return nil
		}
		if len(b) <= pc.inline && !fetchCut {
			return nil
		}
		if handlerSized[tool] || s.redacting() {
			hint := "call again with a larger max_bytes"
			if s.redacting() {
				hint = "this server redacts payloads"
			}
			v.del("truncated")
			v.setFirst("cut", jvString(fmt.Sprintf("%d of %d bytes shown; %s", len(b), int(size), hint)))
			return nil
		}
		ref, _, err := s.contentStore().Put(b)
		if err != nil {
			return err
		}
		c := ops.NewContent(b, pc.inline)
		fields := jvObject()
		fields.set("ref", jvString(ref))
		cut := cutText(min(pc.inline, len(b)), int(size), ref)
		if fetchCut {
			cut = fmt.Sprintf("the server could read only the first %d of %d bytes; %s", len(b), int(size), cutText(min(pc.inline, len(b)), len(b), ref))
		}
		fields.set("cut", jvString(cut))
		if c.Base64 != "" {
			fields.set("base64", jvString(c.Base64))
		} else {
			fields.set("text", jvString(c.Text))
		}
		setPayload(v, fields)
		return nil
	}
	return v, walk(v)
}
