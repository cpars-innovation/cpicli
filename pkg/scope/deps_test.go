package scope

import (
	"testing"

	"github.com/cpars-innovation/cpicli/internal/depcheck"
)

// pkg/scope runs in the gateway: its dependencies are listed here, and a new
// one (like pkg/cpi in cpicli#54) fails until it is reviewed (cpicli#62).
func TestDependencies(t *testing.T) {
	depcheck.Allow(t, ".",
		"github.com/cpars-innovation/cpicli/pkg/scope",
		"github.com/cpars-innovation/cpicli/pkg/httpclnt",
		"github.com/cpars-innovation/cpicli/pkg/odatatime",
		"github.com/cpars-innovation/cpicli/pkg/testtools",
		"github.com/cpars-innovation/cpicli/internal/exitcode",
		"gopkg.in/yaml.v3",
		// through pkg/httpclnt
		"golang.org/x/oauth2", "github.com/rs/zerolog",
		"github.com/mattn/go-colorable", "github.com/mattn/go-isatty", "golang.org/x/sys",
	)
}
