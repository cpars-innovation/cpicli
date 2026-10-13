package ops

import (
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
)

// IsProduction reports whether a tier (the profile's tier setting) names a
// production tenant: prod, production, prd or p (any case).
func IsProduction(tier string) bool {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "prod", "production", "prd", "p":
		return true
	}
	return false
}

// GuardProduction refuses an action that triggers processing (test
// messages, test runs, test tooling) on a production tier unless allow is
// set; the error is a usage error naming the action.
func GuardProduction(tier string, allow bool, action string) error {
	if !IsProduction(tier) || allow {
		return nil
	}
	return output.Usagef("%s on a production tenant (tier %q) is refused; start with --allow-production if this is really intended", action, tier)
}
