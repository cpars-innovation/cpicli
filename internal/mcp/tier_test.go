package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// On a production tier, send_test_message is refused; only the server's
// start option lifts it, never a tool argument.
func TestSendRefusedOnProduction(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": {}})
	var send Tool
	for _, tl := range Tools(Config{Exe: mock.Executer(), Tier: "prod"}) {
		if tl.Name == "send_test_message" {
			send = tl
		}
	}
	require.NotNil(t, send.Handler)
	raw, _ := json.Marshal(map[string]any{"artifact_id": "A"})
	before := len(mock.Requests())
	_, err := send.Handler(context.Background(), raw)
	require.Error(t, err)
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
	assert.Contains(t, err.Error(), "production tenant")
	assert.Len(t, mock.Requests(), before, "nothing was sent or read")
}
