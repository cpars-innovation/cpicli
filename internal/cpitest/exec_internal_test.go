package cpitest

import (
	"os"
	"regexp"
	"testing"

	"github.com/cpars-innovation/cpicli/pkg/iflow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Like SAP's runtime, the mock runs no step of a process whose elements do
// not declare their outgoing sequence flows (the message completes as sent).
func TestMainPathNeedsDeclaredReferences(t *testing.T) {
	data, err := os.ReadFile("landscapes/demo/packages/Billing/Billing_Post/src/main/resources/scenarioflows/integrationflow/Billing_Post.iflw")
	require.NoError(t, err)
	m, err := iflow.Parse(data)
	require.NoError(t, err)
	require.NotEmpty(t, mainPath(m))

	bare := regexp.MustCompile(`<bpmn2:(incoming|outgoing)>[^<]*</bpmn2:(incoming|outgoing)>`).ReplaceAll(data, nil)
	m, err = iflow.Parse(bare)
	require.NoError(t, err)
	assert.Empty(t, mainPath(m))
}
