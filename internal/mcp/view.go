package mcp

import "bytes"

// The view step turns a tool's result (the Go value the CLI also prints)
// into what the agent reads, once per call:
//
//  1. the tool's lean spec (lean.go), unless detail "full" or --full-results;
//  2. payloads: stored whole and shown by reference with the first bytes,
//     or written to save_to (payload.go, content.go);
//  3. compact (unless detail "full" or --full-results): empty values (null,
//     "", [], {}) are left out, and lists of at least minColumnRows objects
//     become {"columns": [...], "rows": [[...], ...]}.
//
// The loop ledger, stats and logs see the result before this step.

// minColumnRows is the shortest list written as columns.
const minColumnRows = 5

func (s *Server) view(tool string, full bool, pc payloadCall, value any) (*jv, error) {
	v, err := toJV(value)
	if err != nil {
		return nil, err
	}
	full = full || s.FullResults
	if spec := leanSpecs[tool]; spec != nil && !full {
		spec(v)
	}
	if tool != "get_content" { // it reads the store itself
		if v, err = s.contents(tool, pc, v); err != nil {
			return nil, err
		}
	}
	if !full {
		compact(v)
	}
	return v, nil
}

// compact leaves out empty values and writes long lists of objects as
// columns. The top-level value itself stays.
func compact(v *jv) {
	switch v.kind {
	case 'o':
		for i := len(v.keys) - 1; i >= 0; i-- {
			compact(v.vals[i])
			if v.vals[i].empty() {
				v.del(v.keys[i])
			}
		}
	case 'a':
		for _, item := range v.vals {
			compact(item)
		}
		columns(v)
	}
}

// columns rewrites an array of at least minColumnRows objects as
// {columns, rows} (a missing value is null). Lists of trees (objects with
// lists of objects) stay as they are.
func columns(a *jv) {
	if len(a.vals) < minColumnRows {
		return
	}
	var cols []string
	index := map[string]int{}
	for _, item := range a.vals {
		if !item.isObject() {
			return
		}
		for i, k := range item.keys {
			if val := item.vals[i]; val.isArray() && len(val.vals) > 0 && val.vals[0].isObject() {
				return
			}
			if _, ok := index[k]; !ok {
				index[k] = len(cols)
				cols = append(cols, k)
			}
		}
	}
	header := jvArray()
	for _, c := range cols {
		header.vals = append(header.vals, jvString(c))
	}
	rows := jvArray()
	for _, item := range a.vals {
		row := make([]*jv, len(cols))
		for i := range row {
			row[i] = jvRaw("null")
		}
		for i, k := range item.keys {
			row[index[k]] = item.vals[i]
		}
		rows.vals = append(rows.vals, jvArray(row...))
	}
	*a = jv{kind: 'o', keys: []string{"columns", "rows"}, vals: []*jv{header, rows}}
}

// encodeJV writes a value as compact JSON.
func encodeJV(v *jv) []byte {
	var b bytes.Buffer
	v.encode(&b)
	return b.Bytes()
}
