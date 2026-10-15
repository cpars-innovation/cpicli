package iflow

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddFlowReferences(t *testing.T) {
	src := `<bpmn2:definitions xmlns:bpmn2="http://www.omg.org/spec/BPMN/20100524/MODEL"><bpmn2:process id="P">` +
		`<bpmn2:startEvent id="S"><bpmn2:extensionElements/><bpmn2:messageEventDefinition/></bpmn2:startEvent>` +
		`<bpmn2:callActivity id="A"><bpmn2:incoming>F0</bpmn2:incoming></bpmn2:callActivity>` +
		`<bpmn2:endEvent id="E"/>` +
		`<bpmn2:sequenceFlow id="F0" sourceRef="S" targetRef="A"/><bpmn2:sequenceFlow id="F1" sourceRef="A" targetRef="E"/>` +
		`</bpmn2:process></bpmn2:definitions>`
	m, err := Parse([]byte(src))
	require.NoError(t, err)
	assert.Equal(t, []FlowRef{{Element: "S", Flow: "F0"}, {Element: "A", Flow: "F1"}, {Element: "E", Flow: "F1", Incoming: true}}, m.MissingRefs)

	doc := NewDocument()
	require.NoError(t, doc.ReadFromString(src))
	require.True(t, AddFlowReferences(doc))
	out, err := doc.WriteToString()
	require.NoError(t, err)
	assert.Contains(t, out, `<bpmn2:startEvent id="S"><bpmn2:extensionElements/><bpmn2:outgoing>F0</bpmn2:outgoing><bpmn2:messageEventDefinition/>`)
	assert.Contains(t, out, `<bpmn2:callActivity id="A"><bpmn2:incoming>F0</bpmn2:incoming><bpmn2:outgoing>F1</bpmn2:outgoing></bpmn2:callActivity>`)
	assert.Contains(t, out, `<bpmn2:endEvent id="E"><bpmn2:incoming>F1</bpmn2:incoming></bpmn2:endEvent>`)
	m, err = Parse([]byte(out))
	require.NoError(t, err)
	assert.Empty(t, m.MissingRefs)
	assert.False(t, AddFlowReferences(doc), "nothing left to add")
}

// The flows cpictl ships (test tools, mock tenant demo content) must run on a
// tenant: SAP's runtime skips a process whose steps lack the references.
func TestShippedFlowsHaveFlowReferences(t *testing.T) {
	n := 0
	for _, root := range []string{"../testtools/artifacts", "../../internal/cpitest/landscapes"} {
		require.NoError(t, filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".iflw") {
				return err
			}
			data, err := os.ReadFile(p)
			require.NoError(t, err)
			m, err := Parse(data)
			require.NoError(t, err, p)
			assert.Empty(t, m.MissingRefs, p)
			n++
			return nil
		}))
	}
	assert.GreaterOrEqual(t, n, 11)
}
