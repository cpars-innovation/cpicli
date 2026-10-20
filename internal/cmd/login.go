package cmd

import (
	"fmt"

	"github.com/cpars-innovation/cpicli/internal/auth"
	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/oauth2"
)

// loginConfig reads the active profile's login block:
//
//	login:
//	  tenantId: <entra directory id>
//	  clientId: <public-client app registration cpictl signs in as>
//	  scope: api://<gateway app>/access   # string or list
//
// ok is false when no login is configured (then the env token is used).
func loginConfig() (auth.Config, bool) {
	tenant := viper.GetString("login.tenantId")
	client := viper.GetString("login.clientId")
	if tenant == "" || client == "" {
		return auth.Config{}, false
	}
	scopes := viper.GetStringSlice("login.scope")
	if len(scopes) == 0 {
		if s := viper.GetString("login.scope"); s != "" {
			scopes = []string{s}
		}
	}
	return auth.Config{TenantID: tenant, ClientID: client, Scopes: scopes, Authority: viper.GetString("login.authority")}, true
}

// NewLoginCommand signs the user in to the gateway's Entra ID and stores the
// tokens for the active profile.
func NewLoginCommand() *cobra.Command {
	var deviceCode bool
	cmd := &cobra.Command{
		// no tenant call: signing in only talks to Entra
		Annotations: map[string]string{annotationOffline: "true"},
		Use:         "login",
		Short:       "Sign in to the cpictl gateway with Entra ID",
		Long: `Sign in to the cpictl gateway with Entra ID and store the token for the
active profile, so cpictl (and cpictl mcp) talk to the gateway as you without a
CPI service key. The token is refreshed silently; run again if sign-in expires.

The profile needs a gateway and a login block:

  gateway: https://cpi-gateway.example.com/t/dev
  login:
    tenantId: <entra directory id>
    clientId: <public-client app registration>
    scope: api://<gateway app>/access

By default a browser opens for sign-in; use --device-code on a headless host.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ok := loginConfig()
			if !ok {
				return output.Usagef("no login configured: add a login block (tenantId, clientId, scope) to the profile")
			}
			_, profile, err := resolveConfigSource(config.GetString(cmd, "config"), config.GetString(cmd, "profile"))
			if err != nil {
				return output.Usage(err)
			}
			if profile == "" {
				return output.Usagef("login needs a profile: pass --profile or select one with 'cpictl profile use'")
			}
			store, err := auth.DefaultStore()
			if err != nil {
				return err
			}

			var tok *oauth2.Token
			if deviceCode {
				tok, err = auth.LoginDeviceCode(cmd.Context(), c, func(d *oauth2.DeviceAuthResponse) {
					fmt.Fprintf(cmd.ErrOrStderr(), "To sign in, open %s and enter code %s\n", d.VerificationURI, d.UserCode)
				})
			} else {
				tok, err = auth.LoginInteractive(cmd.Context(), c, func(authURL string) error {
					fmt.Fprintf(cmd.ErrOrStderr(), "Opening your browser to sign in. If it does not open, visit:\n%s\n", authURL)
					if err := auth.Browse(authURL); err != nil {
						log.Debug().Err(err).Msg("could not open a browser; use the URL above or --device-code")
					}
					return nil
				})
			}
			if err != nil {
				return err
			}
			if err := store.Save(profile, tok); err != nil {
				return err
			}
			log.Info().Msgf("Signed in; token stored for profile %s", profile)
			return nil
		},
	}
	cmd.Flags().BoolVar(&deviceCode, "device-code", false, "Sign in with a device code (headless/SSH) instead of opening a browser")
	return cmd
}

// NewLogoutCommand clears the stored login token for the active profile.
func NewLogoutCommand() *cobra.Command {
	return &cobra.Command{
		Annotations:  map[string]string{annotationOffline: "true"},
		Use:          "logout",
		Short:        "Clear the stored cpictl gateway login token for the active profile",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, profile, err := resolveConfigSource(config.GetString(cmd, "config"), config.GetString(cmd, "profile"))
			if err != nil {
				return output.Usage(err)
			}
			if profile == "" {
				return output.Usagef("logout needs a profile: pass --profile or select one with 'cpictl profile use'")
			}
			store, err := auth.DefaultStore()
			if err != nil {
				return err
			}
			if err := store.Delete(profile); err != nil {
				return err
			}
			log.Info().Msgf("Signed out; token cleared for profile %s", profile)
			return nil
		},
	}
}
