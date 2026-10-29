package cmd

import (
	"os"

	"encoding/json"
	"fmt"
	"github.com/cpars-innovation/cpicli/internal/auth"
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
	// TokenStore is where cpictl took the gateway token from (filled by
	// cpictl, not the gateway): never the token itself.
	TokenStore *TokenStore `json:"tokenStore,omitempty"`
}

// TokenStore says where the gateway token comes from: the sign-in of a
// profile (its file, and where the refresh token is), or CPICTL_GATEWAY_TOKEN.
type TokenStore struct {
	Source  string `json:"source"` // "login" or "environment"
	Profile string `json:"profile,omitempty"`
	*auth.Location
}

// WhoAmIScope is the caller's scope on one SAP tenant.
type WhoAmIScope struct {
	ScopeHash string       `json:"scopeHash"`
	Scope     *scope.Scope `json:"scope"`
	// Credential names the SAP key the gateway uses for this caller on this
	// tenant: SAP's own audit names that key, not the user.
	Credential string `json:"credential,omitempty"`
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
			who, err := fetchWhoAmI(cmd, gw)
			if err != nil {
				return err
			}
			who.TokenStore = tokenStore(cmd)
			output.SetResult(cmd.Context(), who)
			log.Info().Msgf("%s (%s) in %s, roles %s; token valid until %s", who.Name, who.User, who.Org,
				strings.Join(who.Roles, ", "), who.Expires.Local().Format(time.RFC3339))
			if ts := who.TokenStore; ts != nil {
				log.Info().Msg(ts.describe())
			}
			names := make([]string, 0, len(who.Tenants))
			for n := range who.Tenants {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				t := who.Tenants[n]
				via := ""
				if t.Credential != "" {
					via = " (via SAP key " + t.Credential + ")"
				}
				log.Info().Msgf("SAP tenant %s%s: %s", n, via, describeScope(t.Scope))
			}
			if len(names) == 0 {
				log.Warn().Msg("no SAP tenant grants you anything")
			}
			return nil
		},
	}
}

// tokenStore says where serviceDetails takes the gateway token from: the
// profile's sign-in (as gatewayTokenSource reads it), else the environment.
func tokenStore(cmd *cobra.Command) *TokenStore {
	if gatewayTokenSource(cmd) != nil {
		_, profile, _ := resolveConfigSource(config.GetString(cmd, "config"), config.GetString(cmd, "profile"))
		if store, err := auth.DefaultStore(); err == nil {
			if loc, err := store.Location(profile); err == nil {
				return &TokenStore{Source: "login", Profile: profile, Location: loc}
			}
		}
		return &TokenStore{Source: "login", Profile: profile}
	}
	if os.Getenv(gatewayTokenEnv) != "" {
		return &TokenStore{Source: "environment"}
	}
	return nil
}

func (ts *TokenStore) describe() string {
	if ts.Source == "environment" {
		return "token from " + gatewayTokenEnv
	}
	s := "token from the sign-in of profile " + ts.Profile
	if ts.Location != nil {
		s += ", in " + ts.File
		switch ts.Refresh {
		case "OS keychain":
			s += "; refresh token in the OS keychain (" + auth.KeyringService + " / " + ts.KeychainEntry + ")"
		case "file":
			s += "; refresh token in that file (no OS keychain, or CPICTL_TOKEN_STORE=file)"
		}
	}
	return s
}

// fetchWhoAmI asks the gateway who it takes the caller for. /whoami is at
// the gateway's root, not below /t/<tenant>.
func fetchWhoAmI(cmd *cobra.Command, gw string) (*WhoAmI, error) {
	scheme, host, port, _ := cpi.ParseGateway(gw)
	sd := serviceDetails(cmd)
	if sd.GatewayToken == "" && sd.GatewayTokenSource == nil {
		return nil, output.Usagef("not signed in to the gateway: run cpictl auth login (or set %s)", gatewayTokenEnv)
	}
	sd.Gateway = fmt.Sprintf("%s://%s:%d", scheme, host, port)
	exe := cpi.InitHTTPExecuter(sd).WithContext(cmd.Context())
	resp, err := exe.Exec("GET", "/whoami", nil, map[string]string{"Accept": "application/json"})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		_, err := exe.LogError(resp, "Who am I")
		return nil, err
	}
	body, err := exe.ReadRespBody(resp)
	if err != nil {
		return nil, err
	}
	var who WhoAmI
	if err := json.Unmarshal(body, &who); err != nil {
		return nil, fmt.Errorf("the gateway's /whoami answer is not readable: %w", err)
	}
	return &who, nil
}

// gatewayTenant is the SAP tenant name of a gateway URL (.../t/<tenant>).
func gatewayTenant(gw string) string {
	_, _, _, path := cpi.ParseGateway(gw)
	name, ok := strings.CutPrefix(path, "/t/")
	if !ok || strings.Contains(name, "/") {
		return ""
	}
	return name
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
