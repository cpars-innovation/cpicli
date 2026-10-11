package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cpars-innovation/cpicli/pkg/iflow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIFlowLayout(t *testing.T) {
	src := filepath.Join("..", "..", "test", "testdata", "artifacts", "update", "Integration_Test_IFlow")
	dir := filepath.Join(t.TempDir(), "Integration_Test_IFlow")
	require.NoError(t, os.CopyFS(dir, os.DirFS(src)))
	model := filepath.Join(dir, "src", "main", "resources", "scenarioflows", "integrationflow", "Integration Test IFlow.iflw")
	// squeeze the end event onto the step
	data, err := os.ReadFile(model)
	require.NoError(t, err)
	before := string(data)
	i := strings.Index(before, `bpmnElement="EndEvent_2"`)
	require.Positive(t, i)
	j := i + strings.Index(before[i:], `x="`)
	k := j + 3 + strings.Index(before[j+3:], `"`)
	squeezed := before[:j] + `x="400.0` + before[k:]
	require.NoError(t, os.WriteFile(model, []byte(squeezed), 0o644))

	layout := func(code int, extra ...string) iflow.LayoutReport {
		t.Helper()
		r := runMain(t, append([]string{"iflow", "layout", dir, "--output", "json"}, extra...)...)
		require.Equal(t, code, r.code, r.stderr)
		var env struct {
			Result iflow.LayoutReport `json:"result"`
		}
		require.NoError(t, json.Unmarshal([]byte(r.stdout), &env), r.stdout)
		return env.Result
	}
	rep := layout(5, "--check")
	require.Len(t, rep.Files, 1)
	assert.NotEmpty(t, rep.Files[0].Issues)
	same, _ := os.ReadFile(model)
	assert.Equal(t, squeezed, string(same), "--check writes nothing")

	layout(0, "--dry-run")
	same, _ = os.ReadFile(model)
	assert.Equal(t, squeezed, string(same), "--dry-run writes nothing")

	rep = layout(0)
	assert.Equal(t, 1, rep.Changed)
	rep = layout(0, "--check")
	assert.Equal(t, 0, rep.Issues)
	rep = layout(0)
	assert.Equal(t, 0, rep.Changed, "a second run changes nothing")

	r := runMain(t, "iflow", "layout", dir, "--mode", "pretty")
	assert.Equal(t, 2, r.code)
}

func TestIFlowDiagram(t *testing.T) {
	dir := filepath.Join("..", "..", "internal", "cpitest", "landscapes", "demo", "packages", "Orders", "Orders_Route")
	r := runMain(t, "iflow", "diagram", dir, "--output", "json")
	require.Equal(t, 0, r.code, r.stderr)
	var env struct {
		Result iflow.Diagram `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &env), r.stdout)
	assert.Equal(t, iflow.Rect{X: 362, Y: 110, Width: 100, Height: 60}, env.Result.Shapes["ServiceTask_0"])
	assert.Len(t, env.Result.Pools, 4)
	assert.Len(t, env.Result.MessageFlows, 3)

	bad := filepath.Join(t.TempDir(), "bad.iflw")
	require.NoError(t, os.WriteFile(bad, []byte("<bpmn2:definitions>"), 0o644))
	assert.Equal(t, 2, runMain(t, "iflow", "diagram", bad).code)
	assert.Equal(t, 2, runMain(t, "iflow", "diagram", t.TempDir()).code)
}
