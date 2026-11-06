package mask

import (
	"testing"

	"github.com/cpars-innovation/cpicli/internal/depcheck"
)

// pkg/mask runs in the cpictl gateway and CPI Studio: standard library only
// (cpicli#70).
func TestDependencies(t *testing.T) {
	depcheck.Allow(t, ".", "github.com/cpars-innovation/cpicli/pkg/mask")
}
