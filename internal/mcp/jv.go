package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
)

// jv is a JSON value that keeps the order of object keys, so that the view
// step (view.go) can reshape a result and still write it in the order the
// tool's Go type defines.
type jv struct {
	kind byte // 'o' object, 'a' array, 's' string, 'v' other scalar (number, bool, null)
	keys []string
	vals []*jv // object values (with keys) or array items
	str  string
	raw  json.RawMessage
}

func jvString(s string) *jv { return &jv{kind: 's', str: s} }

func jvRaw(raw string) *jv { return &jv{kind: 'v', raw: json.RawMessage(raw)} }

func jvObject() *jv { return &jv{kind: 'o'} }

func jvArray(items ...*jv) *jv { return &jv{kind: 'a', vals: items} }

// toJV converts a Go value through its JSON encoding.
func toJV(v any) (*jv, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return parseJV(data)
}

func parseJV(data []byte) (*jv, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeJV(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing data after JSON value")
	}
	return v, nil
}

func decodeJV(dec *json.Decoder) (*jv, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := jvObject()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				val, err := decodeJV(dec)
				if err != nil {
					return nil, err
				}
				o.keys, o.vals = append(o.keys, kt.(string)), append(o.vals, val)
			}
			_, err := dec.Token()
			return o, err
		case '[':
			a := jvArray()
			for dec.More() {
				val, err := decodeJV(dec)
				if err != nil {
					return nil, err
				}
				a.vals = append(a.vals, val)
			}
			_, err := dec.Token()
			return a, err
		}
		return nil, fmt.Errorf("unexpected %v", t)
	case string:
		return jvString(t), nil
	case json.Number:
		return jvRaw(t.String()), nil
	case bool:
		return jvRaw(strconv.FormatBool(t)), nil
	case nil:
		return jvRaw("null"), nil
	}
	return nil, fmt.Errorf("unexpected token %v", tok)
}

func (v *jv) isObject() bool { return v != nil && v.kind == 'o' }

func (v *jv) isArray() bool { return v != nil && v.kind == 'a' }

// get returns an object's value for key (nil if absent).
func (v *jv) get(key string) *jv {
	if !v.isObject() {
		return nil
	}
	for i, k := range v.keys {
		if k == key {
			return v.vals[i]
		}
	}
	return nil
}

// set replaces or appends a key's value.
func (v *jv) set(key string, val *jv) {
	for i, k := range v.keys {
		if k == key {
			v.vals[i] = val
			return
		}
	}
	v.keys, v.vals = append(v.keys, key), append(v.vals, val)
}

// setFirst sets a key and moves it to the front.
func (v *jv) setFirst(key string, val *jv) {
	v.del(key)
	v.keys, v.vals = append([]string{key}, v.keys...), append([]*jv{val}, v.vals...)
}

func (v *jv) del(key string) {
	for i, k := range v.keys {
		if k == key {
			v.keys, v.vals = append(v.keys[:i:i], v.keys[i+1:]...), append(v.vals[:i:i], v.vals[i+1:]...)
			return
		}
	}
}

// keep removes every key not in keys.
func (v *jv) keep(keys ...string) {
	for i := len(v.keys) - 1; i >= 0; i-- {
		found := false
		for _, k := range keys {
			if v.keys[i] == k {
				found = true
				break
			}
		}
		if !found {
			v.del(v.keys[i])
		}
	}
}

// text returns a string value ("" for anything else).
func (v *jv) text() string {
	if v == nil || v.kind != 's' {
		return ""
	}
	return v.str
}

// number returns a numeric value.
func (v *jv) number() (float64, bool) {
	if v == nil || v.kind != 'v' {
		return 0, false
	}
	f, err := strconv.ParseFloat(string(v.raw), 64)
	return f, err == nil
}

func (v *jv) isTrue() bool { return v != nil && v.kind == 'v' && string(v.raw) == "true" }

// equal compares two scalar values.
func (v *jv) equal(o *jv) bool {
	if v == nil || o == nil || v.kind != o.kind {
		return false
	}
	switch v.kind {
	case 's':
		return v.str == o.str
	case 'v':
		return string(v.raw) == string(o.raw)
	}
	return false
}

// empty: null, "", [] or {}.
func (v *jv) empty() bool {
	switch v.kind {
	case 's':
		return v.str == ""
	case 'v':
		return string(v.raw) == "null"
	default:
		return len(v.vals) == 0
	}
}

// encode writes the value as compact JSON, without HTML escaping (XML
// payloads stay readable).
func (v *jv) encode(b *bytes.Buffer) {
	switch v.kind {
	case 'o':
		b.WriteByte('{')
		for i, k := range v.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSONString(b, k)
			b.WriteByte(':')
			v.vals[i].encode(b)
		}
		b.WriteByte('}')
	case 'a':
		b.WriteByte('[')
		for i, item := range v.vals {
			if i > 0 {
				b.WriteByte(',')
			}
			item.encode(b)
		}
		b.WriteByte(']')
	case 's':
		writeJSONString(b, v.str)
	default:
		b.Write(v.raw)
	}
}

func (v *jv) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	v.encode(&b)
	return b.Bytes(), nil
}

func writeJSONString(b *bytes.Buffer, s string) {
	var tmp bytes.Buffer
	enc := json.NewEncoder(&tmp)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	b.Write(bytes.TrimRight(tmp.Bytes(), "\n"))
}
