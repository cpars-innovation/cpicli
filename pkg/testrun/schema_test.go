package testrun

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// schemaFile is the published JSON Schema of the case files.
const schemaFile = "../../docs/test-case.schema.json"

func loadSchema(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(schemaFile)
	require.NoError(t, err)
	var s map[string]any
	require.NoError(t, json.Unmarshal(data, &s))
	return s
}

// resolve follows a local $ref.
func resolve(root, node map[string]any) map[string]any {
	ref, ok := node["$ref"].(string)
	if !ok {
		return node
	}
	cur := any(root)
	for _, p := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		cur = cur.(map[string]any)[p]
	}
	return cur.(map[string]any)
}

// yamlFields returns the YAML keys of a struct type.
func yamlFields(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "-" || !f.IsExported() {
			continue
		}
		out[name] = f.Type
	}
	return out
}

// nestedStruct is the case-file struct behind a field type (pointer, slice), if any.
func nestedStruct(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() == reflect.Struct && t.PkgPath() == reflect.TypeFor[Case]().PkgPath() {
		return t
	}
	return nil
}

// Every field of the case structs is in the schema and the schema has no
// other properties; every object is closed (unknown keys are errors, as in
// Load).
func TestSchemaMatchesStructs(t *testing.T) {
	root := loadSchema(t)
	var walk func(typ reflect.Type, node map[string]any, at string)
	walk = func(typ reflect.Type, node map[string]any, at string) {
		node = resolve(root, node)
		assert.Equal(t, false, node["additionalProperties"], "%s: additionalProperties must be false", at)
		props, _ := node["properties"].(map[string]any)
		fields := yamlFields(typ)
		var want, got []string
		for k := range fields {
			want = append(want, k)
		}
		for k := range props {
			got = append(got, k)
		}
		sort.Strings(want)
		sort.Strings(got)
		assert.Equal(t, want, got, "%s: schema properties and struct fields differ", at)
		for name, ft := range fields {
			p, ok := props[name].(map[string]any)
			if !ok {
				continue
			}
			assert.NotEmpty(t, resolve(root, p)["description"], "%s.%s has no description", at, name)
			if nested := nestedStruct(ft); nested != nil {
				child := resolve(root, p)
				if items, ok := child["items"].(map[string]any); ok {
					child = items
				}
				walk(nested, child, at+"."+name)
			}
		}
	}
	walk(reflect.TypeFor[Case](), root, "case")
}

// validate checks a YAML node against a schema node: closed objects, known
// keys and the basic types (string, integer, number, boolean, object,
// array, oneOf).
func validate(root, schema map[string]any, n *yaml.Node, at string) []string {
	schema = resolve(root, schema)
	if oneOf, ok := schema["oneOf"].([]any); ok {
		for _, alt := range oneOf {
			if len(validate(root, alt.(map[string]any), n, at)) == 0 {
				return nil
			}
		}
		return []string{fmt.Sprintf("%s (line %d): matches no alternative", at, n.Line)}
	}
	typ, _ := schema["type"].(string)
	scalarTag := func(tags ...string) []string {
		if n.Kind != yaml.ScalarNode || !slices.Contains(tags, n.Tag) {
			return []string{fmt.Sprintf("%s (line %d): expected %s, got %s", at, n.Line, typ, n.Tag)}
		}
		return nil
	}
	switch typ {
	case "string":
		return scalarTag("!!str", "!!int", "!!float", "!!bool", "!!null")
	case "integer":
		return scalarTag("!!int")
	case "number":
		return scalarTag("!!int", "!!float")
	case "array":
		if n.Kind != yaml.SequenceNode {
			return []string{fmt.Sprintf("%s (line %d): expected an array", at, n.Line)}
		}
		var errs []string
		if items, ok := schema["items"].(map[string]any); ok {
			for i, c := range n.Content {
				errs = append(errs, validate(root, items, c, fmt.Sprintf("%s[%d]", at, i))...)
			}
		}
		return errs
	case "object":
		if n.Kind != yaml.MappingNode {
			return []string{fmt.Sprintf("%s (line %d): expected an object", at, n.Line)}
		}
		props, _ := schema["properties"].(map[string]any)
		var errs []string
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i].Value, n.Content[i+1]
			if p, ok := props[key].(map[string]any); ok {
				errs = append(errs, validate(root, p, val, at+"."+key)...)
				continue
			}
			switch ap := schema["additionalProperties"].(type) {
			case bool:
				if !ap {
					errs = append(errs, fmt.Sprintf("%s (line %d): unknown key %s", at, n.Content[i].Line, key))
				}
			case map[string]any:
				errs = append(errs, validate(root, ap, val, at+"."+key)...)
			}
		}
		return errs
	}
	return nil
}

// The fixtures (and the example of the cpi-test skill) are valid for the
// schema and for the strict loader; a broken file is invalid for both.
func TestFixturesValidateAgainstSchema(t *testing.T) {
	root := loadSchema(t)
	files, err := filepath.Glob(filepath.Join(fixtures, "*", "*.yaml"))
	require.NoError(t, err)
	files = slices.DeleteFunc(files, func(f string) bool { return filepath.Base(f) == ConfigFile })
	require.NotEmpty(t, files)
	for _, f := range files {
		data, err := os.ReadFile(f)
		require.NoError(t, err)
		var doc yaml.Node
		require.NoError(t, yaml.Unmarshal(data, &doc), f)
		assert.Empty(t, validate(root, root, doc.Content[0], "case"), f)
		_, err = LoadCase(f)
		assert.NoError(t, err, f)
	}

	broken := "request:\n  bodyfile: x.xml\nexpect:\n  http_status: \"200\"\n  response_contains: [1, [2]]\n  stub_calls: [{rule: a, count: many}]\n"
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(broken), &doc))
	errs := validate(root, root, doc.Content[0], "case")
	assert.Len(t, errs, 4, "%v", errs)
	dir := t.TempDir()
	write(t, dir, "F/b.yaml", broken)
	_, err = Load(dir, Filter{})
	assert.Error(t, err)
}

// Every field has a title and examples for editors that build a form from the
// schema (CPI Studio); request and expect are objects with a title only.
func TestSchemaTitlesAndExamples(t *testing.T) {
	root := loadSchema(t)
	var check func(path string, props map[string]any)
	check = func(path string, props map[string]any) {
		for name, raw := range props {
			p := raw.(map[string]any)
			field := path + name
			assert.NotEmpty(t, p["title"], "%s has no title", field)
			node := resolve(root, p)
			if _, isRef := p["$ref"]; isRef && node["properties"] != nil {
				check(field+".", node["properties"].(map[string]any))
				continue
			}
			assert.NotEmpty(t, p["examples"], "%s has no examples", field)
			if sub, ok := p["properties"].(map[string]any); ok {
				check(field+".", sub)
			}
			if items, ok := p["items"].(map[string]any); ok {
				if sub, ok := items["properties"].(map[string]any); ok {
					check(field+"[].", sub)
				}
			}
		}
	}
	check("", root["properties"].(map[string]any))
}
