package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/scope"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// WhoAmI is the cpictl gateway's answer to /whoami: who it takes the caller
// for and the caller's scope on each SAP tenant it serves them.
type WhoAmI struct {
	Org     string                 `json:"org"`
	User    string                 `json:"user"`
	Name    string                 `json:"name,omitempty"`
	Roles   []string               `json:"roles,omitempty"`
	Expires time.Time              `json:"expires"`
	Tenants map[string]WhoAmIScope `json:"sapTenants"`
}

// WhoAmIScope is the caller's scope on one SAP tenant.
type WhoAmIScope struct {
	ScopeHash string       `json:"scopeHash"`
	Scope     *scope.Scope `json:"scope"`
}

// NewAuthStatusCommand is cpictl auth status (alias whoami).
func NewAuthStatusCommand() *cobra.Command {
	return &cobra.Command{
		// checks gateway mode and the token itself (a clearer message than
		// the tenant check outside gateway mode)
		Annotations: map[string]string{annotationOffline: "true"},
		Use:         "status",
		Aliases:     []string{"whoami"},
		Short:       "Show who the cpictl gateway takes you for, and your scope per SAP tenant (gateway mode)",
		Long: `In gateway mode, asks the gateway who it takes you for (organisation, user,
roles, when your token expires) and which packages and actions you may use on
each SAP tenant it serves you. Use it to check you are signed in as the right
user (cpictl auth login) and what an agent working for you can do.`,
		Example: `  cpictl --profile dev auth status
  cpictl --profile dev auth status --output json
  cpictl --profile dev whoami`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			gw := config.GetString(cmd, "gateway")
			if gw == "" {
				return output.Usagef("auth status needs gateway mode: set gateway in the profile (docs/configuration.md#gateway-mode)")
			}
			// /whoami is at the gateway's root, not below /t/<tenant>
			scheme, host, port, _ := cpi.ParseGateway(gw)
			sd := serviceDetails(cmd)
			if sd.GatewayToken == "" && sd.GatewayTokenSource == nil {
				return output.Usagef("not signed in to the gateway: run cpictl auth login (or set %s)", gatewayTokenEnv)
			}
			sd.Gateway = fmt.Sprintf("%s://%s:%d", scheme, host, port)
			exe := cpi.InitHTTPExecuter(sd).WithContext(cmd.Context())
			resp, err := exe.Exec("GET", "/whoami", nil, map[string]string{"Accept": "application/json"})
			if err != nil {
				return err
			}
			if resp.StatusCode != 200 {
				_, err := exe.LogError(resp, "Who am I")
				return err
			}
			body, err := exe.ReadRespBody(resp)
			if err != nil {
				return err
			}
			var who WhoAmI
			if err := json.Unmarshal(body, &who); err != nil {
				return fmt.Errorf("the gateway's /whoami answer is not readable: %w", err)
			}
			output.SetResult(cmd.Context(), &who)
			log.Info().Msgf("%s (%s) in %s, roles %s; token valid until %s", who.Name, who.User, who.Org,
				strings.Join(who.Roles, ", "), who.Expires.Local().Format(time.RFC3339))
			names := make([]string, 0, len(who.Tenants))
			for n := range who.Tenants {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				log.Info().Msgf("SAP tenant %s: %s", n, describeScope(who.Tenants[n].Scope))
			}
			if len(names) == 0 {
				log.Warn().Msg("no SAP tenant grants you anything")
			}
			return nil
		},
	}
}

// describeScope summarises a scope in one line: grants and denies.
func describeScope(s *scope.Scope) string {
	if s == nil {
		return "nothing"
	}
	var parts []string
	if len(s.Packages) > 0 && len(s.Actions) > 0 {
		parts = append(parts, fmt.Sprintf("%s on %s", strings.Join(s.Actions, ","), strings.Join(s.Packages, ",")))
	}
	for _, g := range s.PackageGrants {
		parts = append(parts, fmt.Sprintf("%s on %s", strings.Join(g.Actions, ","), strings.Join(g.Packages, ",")))
	}
	for _, d := range s.Deny {
		parts = append(parts, fmt.Sprintf("never %s", strings.Join(append(append([]string{}, d.Packages...), d.Artifacts...), ",")))
	}
	if len(s.Tenant) > 0 {
		parts = append(parts, "tenant "+strings.Join(s.Tenant, ","))
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, "; ")
}
