package cmd

import (
	"os"

	"github.com/cpars-innovation/cpicli/internal/auth"
	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
)

// serviceDetails reads the tenant connection settings from the (config/env
// bound) persistent flags.
func serviceDetails(cmd *cobra.Command) *cpi.ServiceDetails {
	if gw := config.GetString(cmd, "gateway"); gw != "" {
		sd := &cpi.ServiceDetails{Gateway: gw}
		// Prefer a cpictl auth login token (refreshed silently); fall back to the
		// environment. The token is never read from a flag or a config file.
		if src := gatewayTokenSource(cmd); src != nil {
			sd.GatewayTokenSource = src
		} else {
			sd.GatewayToken = os.Getenv(gatewayTokenEnv)
		}
		return sd
	}
	oauthHost := config.GetString(cmd, "oauth-host")
	if oauthHost == "" {
		return &cpi.ServiceDetails{
			Host:     config.GetString(cmd, "tmn-host"),
			Userid:   config.GetString(cmd, "tmn-userid"),
			Password: config.GetString(cmd, "tmn-password"),
		}
	}
	return &cpi.ServiceDetails{
		Host:              config.GetString(cmd, "tmn-host"),
		OauthHost:         oauthHost,
		OauthClientId:     config.GetString(cmd, "oauth-clientid"),
		OauthClientSecret: config.GetString(cmd, "oauth-clientsecret"),
		OauthPath:         config.GetString(cmd, "oauth-path"),
	}
}

// gatewayTokenEnv holds the user's token for a cpictl gateway (gateway mode),
// used when the profile has no cpictl auth login session.
const gatewayTokenEnv = "CPICTL_GATEWAY_TOKEN"

// gatewayTokenSource returns a refreshing token source from the stored cpictl
// login session for the active profile, or nil if the profile has no login
// configured or no session yet (then the caller falls back to the env token).
func gatewayTokenSource(cmd *cobra.Command) oauth2.TokenSource {
	c, ok := loginConfig()
	if !ok {
		return nil
	}
	_, profile, err := resolveConfigSource(config.GetString(cmd, "config"), config.GetString(cmd, "profile"))
	if err != nil || profile == "" {
		return nil
	}
	store, err := auth.DefaultStore()
	if err != nil {
		return nil
	}
	src, err := store.TokenSource(cmd.Context(), c, profile)
	if err != nil {
		if err != auth.ErrNotLoggedIn {
			log.Debug().Err(err).Msg("reading the stored gateway login token")
		}
		return nil
	}
	return src
}
