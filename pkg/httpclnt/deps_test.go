package httpclnt

import (
	"testing"

	"github.com/cpars-innovation/cpicli/internal/depcheck"
)

// pkg/httpclnt carries every request and credential: its dependencies are
// listed here, and a new one fails until it is reviewed (cpicli#62).
func TestDependencies(t *testing.T) {
	depcheck.Allow(t, ".",
		"github.com/cpars-innovation/cpicli/pkg/httpclnt",
		"golang.org/x/oauth2",
		"github.com/rs/zerolog",
		"github.com/mattn/go-colorable", "github.com/mattn/go-isatty", "golang.org/x/sys", // zerolog's console
	)
}
