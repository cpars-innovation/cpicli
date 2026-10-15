package testrun

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const flowScriptCase = `description: trims the order number
tags: [smoke]
input:
  body: "<Order><No> 4711 </No></Order>"
  headers: {OrderNo: " 4711 "}
expect:
  headers: {OrderNo: "4711", Temp: null}
  body_contains: ["4711"]
  max_duration: 2s
`

func TestLoadScriptCases(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Orders_In/valid.yaml", "request: {body: x}\nexpect: {http_status: 200}\n") // a flow case: not a script case
	write(t, dir, "Orders_In/scripts/Headers.groovy/trims.yaml", flowScriptCase)
	write(t, dir, "collections/Orders_Scripts/format.yaml", "call: {function: formatNo, args: [\"4711\", 8]}\nexpect: {returns: \"00004711\"}\n")
	write(t, dir, "collections/Orders_Scripts/nothing.yaml", "call: {function: noop}\nexpect: {returns: null}\npd: {PID1/Target: ERP}\n")
	write(t, dir, "pd/PID1/Map/x.yaml", "expect: {output: x}\n") // XSLT cases are not script cases

	cases, err := LoadScriptCases(dir, Filter{})
	require.NoError(t, err)
	require.Len(t, cases, 3)
	trims, format, nothing := cases[0], cases[1], cases[2]
	assert.Equal(t, "Orders_Scripts", format.Key())
	assert.Equal(t, ScriptKindCollection, format.Kind)
	assert.Equal(t, "formatNo", format.Function())
	assert.Equal(t, []any{"4711", 8}, format.Call.Args)
	assert.Equal(t, "00004711", format.Expect.Returns)
	assert.True(t, nothing.Expect.HasReturns, "returns: null is an expectation, not an absent key")
	assert.Nil(t, nothing.Expect.Returns)
	assert.Equal(t, []string{"pd"}, nothing.InjectsValues())

	assert.Equal(t, "Orders_In/Headers.groovy", trims.Key())
	assert.Equal(t, "script:Orders_In/Headers.groovy", trims.Suite())
	assert.Equal(t, "processData", trims.Function())
	assert.Nil(t, trims.Expect.Headers["Temp"], "null: must be absent")
	assert.Equal(t, "4711", *trims.Expect.Headers["OrderNo"])
	assert.Empty(t, trims.InjectsValues())

	only, err := LoadScriptCases(dir, Filter{Flows: []string{"Orders_In"}})
	require.NoError(t, err)
	assert.Len(t, only, 1)

	// the flow cases are unchanged: the collections folder is not a flow
	flows, err := Load(dir, Filter{})
	require.NoError(t, err)
	require.Len(t, flows, 1)
	assert.Equal(t, "Orders_In", flows[0].Flow)
}

func TestLoadScriptCasesRefusesInvalid(t *testing.T) {
	for name, content := range map[string]string{
		"unknown key":       "expect: {returns: 1}\nrequest: {}\n",
		"no expectation":    "input: {body: x}\nexpect: {}\n",
		"unknown in expect": "expect: {returns: 1, output: x}\n",
		"body twice":        "input: {body: x, body_file: y.xml}\nexpect: {body_contains: [x]}\n",
		"params":            "params: {a: b}\nexpect: {body_contains: [x]}\n",
		"duration":          "expect: {max_duration: 1.5s}\n",
		"secure":            "secure: {a: {user: u, value: v}}\nexpect: {body_contains: [x]}\n",
		"keystore":          "keystore: {a: something}\nexpect: {body_contains: [x]}\n",
		"missing file":      "expect: {body_equals_file: none.xml}\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "F/scripts/s.groovy/c.yaml", content)
			_, err := LoadScriptCases(dir, Filter{})
			assert.ErrorContains(t, err, "invalid script case file")
		})
	}
}

// The schema lists exactly the fields of the structs, every field has a
// title and examples, and a case written from the examples loads.
func TestScriptCaseSchema(t *testing.T) {
	data, err := os.ReadFile("../../docs/script-case.schema.json")
	require.NoError(t, err)
	var s map[string]any
	require.NoError(t, json.Unmarshal(data, &s))
	keys := func(m map[string]any) []string {
		out := []string{}
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	fields := func(t reflect.Type) []string {
		out := []string{}
		for name := range yamlFields(t) {
			out = append(out, name)
		}
		sort.Strings(out)
		return out
	}
	defs := s["$defs"].(map[string]any)
	props := func(def string) map[string]any {
		return defs[def].(map[string]any)["properties"].(map[string]any)
	}
	assert.Equal(t, fields(reflect.TypeOf(ScriptCase{})), keys(s["properties"].(map[string]any)))
	for def, typ := range map[string]any{"call": ScriptCall{}, "input": ScriptInput{}, "secure": SecureEntry{}, "throws": ScriptThrows{}, "expect": ScriptExpect{}} {
		assert.Equal(t, fields(reflect.TypeOf(typ)), keys(props(def)), def)
		assert.Equal(t, false, defs[def].(map[string]any)["additionalProperties"], def)
	}
	for _, m := range []map[string]any{s["properties"].(map[string]any), props("call"), props("input"), props("secure"), props("throws"), props("expect")} {
		for k, v := range m {
			p := v.(map[string]any)
			assert.NotEmpty(t, p["title"], k)
			assert.NotEmpty(t, p["description"], k)
			if k != "expect" {
				assert.NotEmpty(t, p["examples"], k)
			}
		}
	}
	// a collection case from the examples
	example := map[string]any{"call": props("call")["args"].(map[string]any)["examples"].([]any)[0]}
	example = map[string]any{"call": map[string]any{"function": "f", "args": example["call"]}, "expect": map[string]any{"returns": "x"}}
	doc, err := yaml.Marshal(example)
	require.NoError(t, err)
	dir := t.TempDir()
	write(t, dir, "collections/C/e.yaml", string(doc))
	_, err = LoadScriptCases(dir, Filter{})
	assert.NoError(t, err)
}
