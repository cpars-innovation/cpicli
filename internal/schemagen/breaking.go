package schemagen

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Breaking lists the changes from old to new schema files that can break a
// consumer: a removed command or property, a changed type, a property that
// is no longer always present. Added properties and commands are not
// breaking.
func Breaking(old, new Files) []string {
	var out []string
	names := make([]string, 0, len(old))
	for n := range old {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		if name == "index.json" {
			continue
		}
		nd, ok := new[name]
		if !ok {
			out = append(out, name+": removed")
			continue
		}
		var o, n map[string]any
		if json.Unmarshal(old[name], &o) != nil || json.Unmarshal(nd, &n) != nil {
			out = append(out, name+": not readable")
			continue
		}
		c := &comparer{oldRoot: o, newRoot: n, seen: map[string]bool{}}
		c.compare(o, n, strings.TrimSuffix(name, ".json"))
		out = append(out, c.out...)
	}
	return out
}

type comparer struct {
	oldRoot, newRoot map[string]any
	seen             map[string]bool
	out              []string
}

func resolve(root, s map[string]any) map[string]any {
	for i := 0; i < 20; i++ {
		ref, ok := s["$ref"].(string)
		if !ok {
			return s
		}
		defs, _ := root["$defs"].(map[string]any)
		next, _ := defs[strings.TrimPrefix(ref, "#/$defs/")].(map[string]any)
		if next == nil {
			return s
		}
		s = next
	}
	return s
}

// nonNull drops the null variant of a nullable anyOf.
func nonNull(s map[string]any) map[string]any {
	any_, ok := s["anyOf"].([]any)
	if !ok || len(any_) != 2 {
		return s
	}
	for i, v := range any_ {
		if m, ok := v.(map[string]any); ok && m["type"] == "null" {
			if other, ok := any_[1-i].(map[string]any); ok {
				return other
			}
		}
	}
	return s
}

func (c *comparer) compare(o, n map[string]any, at string) {
	if o["$ref"] != nil || n["$ref"] != nil {
		// each pair of definitions once: recursive types end here
		key := fmt.Sprint(o["$ref"], "|", n["$ref"])
		if c.seen[key] {
			return
		}
		c.seen[key] = true
	}
	o, n = nonNull(resolve(c.oldRoot, o)), nonNull(resolve(c.newRoot, n))
	if o["anyOf"] != nil || n["anyOf"] != nil || len(o) == 0 {
		return // unions and free-form values are not compared
	}
	if o["type"] != n["type"] {
		c.out = append(c.out, fmt.Sprintf("%s: type %v -> %v", at, o["type"], n["type"]))
		return
	}
	switch o["type"] {
	case "array":
		oi, _ := o["items"].(map[string]any)
		ni, _ := n["items"].(map[string]any)
		if oi != nil && ni != nil {
			c.compare(oi, ni, at+"[]")
		}
	case "object":
		op, _ := o["properties"].(map[string]any)
		np, _ := n["properties"].(map[string]any)
		newReq := map[string]bool{}
		for _, r := range asList(n["required"]) {
			newReq[r] = true
		}
		for _, r := range asList(o["required"]) {
			if _, still := np[r]; still && !newReq[r] {
				c.out = append(c.out, at+"."+r+": no longer always present")
			}
		}
		keys := make([]string, 0, len(op))
		for k := range op {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			nv, ok := np[k].(map[string]any)
			if !ok {
				c.out = append(c.out, at+"."+k+": removed")
				continue
			}
			c.compare(op[k].(map[string]any), nv, at+"."+k)
		}
		oa, _ := o["additionalProperties"].(map[string]any)
		na, _ := n["additionalProperties"].(map[string]any)
		if oa != nil && na != nil {
			c.compare(oa, na, at+".*")
		}
	}
}

func asList(v any) []string {
	var out []string
	l, _ := v.([]any)
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
