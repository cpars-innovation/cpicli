package schemagen

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBreaking(t *testing.T) {
	old := Files{
		"a.json": []byte(`{"type":"object","required":["id","n"],"properties":{"id":{"type":"string"},"n":{"type":"integer"},"tags":{"type":"array","items":{"$ref":"#/$defs/T"}}},
			"$defs":{"T":{"type":"object","properties":{"k":{"type":"string"}}}}}`),
		"gone.json":  []byte(`{"type":"null"}`),
		"index.json": []byte(`{}`),
	}
	new := Files{
		"a.json": []byte(`{"type":"object","required":["id"],"properties":{"id":{"type":"string"},"n":{"anyOf":[{"type":"string"},{"type":"null"}]},"tags":{"type":"array","items":{"$ref":"#/$defs/T"}},"added":{"type":"string"}},
			"$defs":{"T":{"type":"object","properties":{}}}}`),
	}
	assert.Equal(t, []string{
		"a.n: no longer always present",
		"a.n: type integer -> string",
		"a.tags[].k: removed",
		"gone.json: removed",
	}, Breaking(old, new))
	assert.Empty(t, Breaking(new, new))
}

func TestBreakingRecursive(t *testing.T) {
	f := Files{"t.json": []byte(`{"$ref":"#/$defs/N","$defs":{"N":{"type":"object","properties":{"children":{"type":"array","items":{"$ref":"#/$defs/N"}}}}}}`)}
	assert.Empty(t, Breaking(f, f))
}

// A command without a result that gets one is not a breaking change; the
// reverse is.
func TestBreakingResultAdded(t *testing.T) {
	none := []byte(`{"type":"null","description":"The command has no result."}`)
	obj := []byte(`{"type":"object","properties":{"files":{"type":"array","items":{"type":"string"}}},"required":["files"]}`)
	if got := Breaking(Files{"c.json": none}, Files{"c.json": obj}); len(got) != 0 {
		t.Errorf("result added: %v", got)
	}
	if got := Breaking(Files{"c.json": obj}, Files{"c.json": none}); len(got) == 0 {
		t.Errorf("result removed must be breaking")
	}
}
