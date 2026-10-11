package lint

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const v2Script = "// Groovy Flowstep Version 2.x, Groovy runtime 4\npackage script.v2\n\nimport com.sap.it.script.v2.api.Message\n\ndef Message processData(Message message) {\n    message.setProperty('checked', 'yes')\n    return message\n}\n"

func TestScriptV2(t *testing.T) {
	root := t.TempDir()
	v2 := tStep{id: "S2", name: "Check v2", kind: "script", props: map[string]string{"script": "v2/Check.groovy",
		"cmdVariantUri": "ctype::FlowstepVariant/cname::GroovyScript/version::2.0.0", "componentVersion": "2.0"}}
	flow := tFlow{pkg: "P", id: "FlowV2", steps: []tStep{v2, tScript("S1", "Legacy", "Legacy.groovy"), {id: "R", name: "Route", kind: "router", conditions: []string{"${property.checked} = 'yes'"}}},
		scripts: map[string]string{"v2/Check.groovy": v2Script, "Legacy.groovy": "def x = 1\n"},
		params:  "API_KEY_REF=APIM_BusinessPartner_Key\nAPI_TOKEN=c2VjcmV0LXRva2VuLXZhbHVlLTEyMw==\n"}
	flow.write(t, root)
	// a gateway's condition and a loop's characteristics carry ids but are not steps
	iflw := filepath.Join(root, "P", "FlowV2", "src/main/resources/scenarioflows/integrationflow/FlowV2.iflw")
	data, err := os.ReadFile(iflw)
	require.NoError(t, err)
	require.Contains(t, string(data), `<bpmn2:conditionExpression xsi:type`)
	model := strings.Replace(string(data), `<bpmn2:conditionExpression xsi:type`, `<bpmn2:conditionExpression id="FormalExpression_1" xsi:type`, 1)
	model = strings.Replace(model, `</bpmn2:callActivity>`, `<bpmn2:standardLoopCharacteristics id="LoopCharacteristics_1"><bpmn2:loopCondition id="FormalExpression_2">x</bpmn2:loopCondition></bpmn2:standardLoopCharacteristics></bpmn2:callActivity>`, 1)
	require.NoError(t, os.WriteFile(iflw, []byte(model), 0o644))

	res, err := Run(context.Background(), Options{Dir: root})
	require.NoError(t, err)
	assert.Empty(t, findingsOf(res, "unused-script", "FlowV2"), "v2/Check.groovy is used by its step: %v", findingsOf(res, "unused-script", "FlowV2"))
	assert.Empty(t, findingsOf(res, "unconnected-step", "FlowV2"))
	pref := findingsOf(res, "prefer-script-v2", "FlowV2")
	require.Len(t, pref, 1, "only the version 1 step")
	assert.Equal(t, "S1", pref[0].Element)
	assert.Contains(t, pref[0].Suggestion, "com.sap.it.script.v2.api.Message")
	secrets := findingsOf(res, "hardcoded-secret", "FlowV2")
	require.Len(t, secrets, 1, "a reference name is not a secret, a token value is: %v", secrets)
	assert.Contains(t, secrets[0].Message, "API_TOKEN")

	// fix never deletes a v2 script
	_, err = Fix(context.Background(), Options{Dir: root}, FixOptions{})
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(root, "P", "FlowV2", "src/main/resources/script/v2/Check.groovy"))
}
