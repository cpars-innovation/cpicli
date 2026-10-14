package testrun

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func strp(s string) *string { return &s }

func TestCheckScript(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "F/scripts/s.groovy/expected.json", `{"order": {"no": "4711"}}`)
	c := ScriptCase{Kind: ScriptKindFlow, Owner: "F", Script: "s.groovy", Name: "c", File: dir + "/F/scripts/s.groovy/c.yaml",
		Expect: ScriptExpect{
			BodyEqualsFile: "expected.json",
			BodyContains:   []string{"4711"},
			BodyJSON:       map[string]any{"order.no": "4711"},
			Headers:        map[string]*string{"orderno": strp("4711"), "Temp": nil},
			Properties:     map[string]*string{"Checked": strp("true")},
			Log:            map[string]string{"OrderNo": "4711"},
			MaxDuration:    "1s",
		}}
	o := ScriptOutcome{Body: strp(`{ "order":{"no":"4711"} }`), Headers: map[string]*string{"OrderNo": strp("4711"), "Temp": nil},
		Properties: map[string]*string{"Checked": strp("true")}, DurationMs: 12}
	o.Log.CustomHeaders = map[string][]string{"OrderNo": {"4711"}}
	assert.Empty(t, CheckScript(c, o), "headers are case-insensitive, null counts as absent, JSON compares structurally")
	assert.Equal(t, StatusPassed, ScriptCaseResult(c, o).Status)

	o.Properties = map[string]*string{"checked": strp("true")} // properties are case-sensitive
	o.Headers["Temp"] = strp("x")
	o.DurationMs = 1500
	failures := CheckScript(c, o)
	var exps []string
	for _, f := range failures {
		exps = append(exps, f.Expectation)
	}
	assert.ElementsMatch(t, []string{"properties[Checked]", "headers[Temp]", "max_duration"}, exps)
	cr := ScriptCaseResult(c, o)
	assert.Equal(t, StatusFailed, cr.Status)
	require.NotNil(t, cr.Response)
}

func TestCheckScriptThrowsAndReturns(t *testing.T) {
	thrown := ScriptOutcome{}
	thrown.Throws = &struct {
		Class   string `json:"class"`
		Message string `json:"message"`
	}{"java.lang.IllegalStateException", "OrderNo missing"}

	c := ScriptCase{Expect: ScriptExpect{Throws: &ScriptThrows{Class: "IllegalStateException", MessageContains: "missing"}}}
	assert.Empty(t, CheckScript(c, thrown))
	assert.Len(t, CheckScript(c, ScriptOutcome{}), 1, "expected to fail, did not")

	c = ScriptCase{Expect: ScriptExpect{BodyContains: []string{"x"}}}
	f := CheckScript(c, thrown)
	require.Len(t, f, 1)
	assert.Equal(t, "throws", f[0].Expectation)

	c = ScriptCase{Expect: ScriptExpect{Returns: "00004711", HasReturns: true}}
	assert.Empty(t, CheckScript(c, ScriptOutcome{Returns: "00004711", HasReturns: true}))
	assert.Len(t, CheckScript(c, ScriptOutcome{Returns: "4711", HasReturns: true}), 1)
	c = ScriptCase{Expect: ScriptExpect{Returns: 8, HasReturns: true}}
	assert.Empty(t, CheckScript(c, ScriptOutcome{Returns: float64(8), HasReturns: true}), "numbers by value")
	c = ScriptCase{Expect: ScriptExpect{HasReturns: true}}
	assert.Empty(t, CheckScript(c, ScriptOutcome{HasReturns: true}), "returns: null")
	assert.Len(t, CheckScript(c, ScriptOutcome{}), 1, "the function returned the message")

	unsupported := thrown
	unsupported.Unsupported = true
	cr := ScriptCaseResult(ScriptCase{Expect: ScriptExpect{BodyContains: []string{"x"}}}, unsupported)
	assert.Equal(t, StatusError, cr.Status)
	assert.Equal(t, "unsupported", cr.ErrorCategory)
	assert.Equal(t, StatusError, ScriptCaseResult(ScriptCase{}, ScriptOutcome{Error: "no function"}).Status)
}
