package ops

import (
	"testing"

	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/stretchr/testify/assert"
)

func TestGuardProduction(t *testing.T) {
	for _, tier := range []string{"prod", "PROD", "production", "prd", " p "} {
		err := GuardProduction(tier, false, "sending a test message")
		assert.Equal(t, exitcode.Usage, output.ExitCode(err), tier)
		assert.NoError(t, GuardProduction(tier, true, "x"), tier)
	}
	for _, tier := range []string{"", "dev", "test", "qa", "preprod"} {
		assert.NoError(t, GuardProduction(tier, false, "x"), tier)
	}
}
