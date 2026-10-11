package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// addSecretFlags adds --<name>-env, --<name>-file and --<name>-stdin. There is
// deliberately no flag that takes the secret itself.
func addSecretFlags(c *cobra.Command, name, what string) {
	c.Flags().String(name+"-env", "", "Read the "+what+" from this environment variable")
	c.Flags().String(name+"-file", "", "Read the "+what+" from this file")
	c.Flags().Bool(name+"-stdin", false, "Read the "+what+" from stdin")
}

func secretSource(cmd *cobra.Command, name string) ops.SecretSource {
	s := ops.SecretSource{Env: config.GetString(cmd, name+"-env"), File: config.GetString(cmd, name+"-file")}
	if config.GetBool(cmd, name+"-stdin") {
		s.Stdin = cmd.InOrStdin()
	}
	return s
}

func logCredentialResult(r *ops.CredentialResult) {
	log.Info().Msgf("%s %s: %s%s", r.Kind, r.Name, r.Action, errSuffix(r.Error))
}

func NewCredentialsCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "credentials",
		Short: "List and deploy user credentials, OAuth2 client credentials and secure parameters",
		Long: `Manage security material referenced by integration flows.

Secrets are never accepted as flag values (they would end up in shell history
and process lists): use --*-env, --*-file or --*-stdin, or 'credentials apply'
with a YAML file that references environment variables or files.
Listing never returns secrets.`,
	}

	list := &cobra.Command{
		Use:          "list",
		Short:        "List credentials (names and metadata, never secrets)",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := ops.ListCredentials(tenantExecuter(cmd), config.GetString(cmd, "kind"))
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), o)
			for _, u := range o.UserCredentials {
				log.Info().Msgf("user          %-30s %s (%s)", u.Name, u.User, u.Status)
			}
			for _, a := range o.OAuth2Credentials {
				log.Info().Msgf("oauth2        %-30s %s %s (%s)", a.Name, a.ClientID, a.TokenServiceURL, a.Status)
			}
			for _, a := range o.OAuth2AuthorizationCodes {
				token := "no refresh token"
				if a.RefreshTokenExists {
					token = "refresh token"
				}
				log.Info().Msgf("oauth2-authcode %-28s %s %s (%s, %s)", a.Name, a.Provider, a.ClientID, a.UserName, token)
			}
			for _, p := range o.SecureParameters {
				log.Info().Msgf("secure-param  %-30s (%s)", p.Name, p.Status)
			}
			for _, w := range o.Warnings {
				log.Warn().Msg(w)
			}
			return nil
		},
	}
	list.Flags().String("kind", "", "Only this kind: "+strings.Join(cpi.ListCredentialKinds, ", "))

	setUser := &cobra.Command{
		Use:          "set-user",
		Short:        "Create or update a user credential",
		SilenceUsage: true,
		Example:      `  ERP_PASSWORD=... cpictl credentials set-user --name ERP_User --user svc_erp --password-env ERP_PASSWORD`,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ops.DeployCredential(tenantExecuter(cmd), ops.UserCredentialSpec{
				Name: config.GetString(cmd, "name"), Kind: config.GetString(cmd, "kind"), Description: config.GetString(cmd, "description"),
				User: config.GetString(cmd, "user"), CompanyID: config.GetString(cmd, "company-id"), Password: secretSource(cmd, "password"),
			}, config.GetBool(cmd, "dry-run"))
			return credentialOutcome(cmd, res, err)
		},
	}
	setUser.Flags().String("name", "", "Credential name (as referenced in the iFlow)")
	setUser.Flags().String("user", "", "User name")
	setUser.Flags().String("kind", "default", "default, successfactors or openconnectors")
	setUser.Flags().String("company-id", "", "Company ID (SuccessFactors)")
	setUser.Flags().String("description", "", "Description")
	setUser.Flags().Bool("dry-run", false, "Check the input without writing")
	addSecretFlags(setUser, "password", "password")

	setOAuth2 := &cobra.Command{
		Use:          "set-oauth2",
		Short:        "Create or update an OAuth2 client credential",
		SilenceUsage: true,
		Example:      `  cpictl credentials set-oauth2 --name Graph --token-url https://login/token --client-id app --secret-file ./graph.secret`,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ops.DeployCredential(tenantExecuter(cmd), ops.OAuth2CredentialSpec{
				Name: config.GetString(cmd, "name"), Description: config.GetString(cmd, "description"),
				TokenServiceURL: config.GetString(cmd, "token-url"), ClientID: config.GetString(cmd, "client-id"),
				ClientSecret: secretSource(cmd, "secret"), ClientAuthentication: config.GetString(cmd, "client-auth"),
				Scope: config.GetString(cmd, "scope"), ScopeContentType: config.GetString(cmd, "scope-content-type"),
				Resource: config.GetString(cmd, "resource"), Audience: config.GetString(cmd, "audience"),
			}, config.GetBool(cmd, "dry-run"))
			return credentialOutcome(cmd, res, err)
		},
	}
	setOAuth2.Flags().String("name", "", "Credential name")
	setOAuth2.Flags().String("token-url", "", "Token service URL (https)")
	setOAuth2.Flags().String("client-id", "", "Client ID")
	setOAuth2.Flags().String("client-auth", "body", "Client authentication: body or header")
	setOAuth2.Flags().String("scope", "", "Scope")
	setOAuth2.Flags().String("scope-content-type", "urlencoded", "urlencoded or json")
	setOAuth2.Flags().String("resource", "", "Resource")
	setOAuth2.Flags().String("audience", "", "Audience")
	setOAuth2.Flags().String("description", "", "Description")
	setOAuth2.Flags().Bool("dry-run", false, "Check the input without writing")
	addSecretFlags(setOAuth2, "secret", "client secret")

	setParam := &cobra.Command{
		Use:          "set-secure-param",
		Short:        "Create or update a secure parameter (Neo environment)",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ops.DeployCredential(tenantExecuter(cmd), ops.SecureParameterSpec{
				Name: config.GetString(cmd, "name"), Description: config.GetString(cmd, "description"), Value: secretSource(cmd, "value"),
			}, config.GetBool(cmd, "dry-run"))
			return credentialOutcome(cmd, res, err)
		},
	}
	setParam.Flags().String("name", "", "Parameter name")
	setParam.Flags().String("description", "", "Description")
	setParam.Flags().Bool("dry-run", false, "Check the input without writing")
	addSecretFlags(setParam, "value", "value")

	apply := &cobra.Command{
		Use:          "apply",
		Short:        "Create or update all credentials of a YAML file",
		SilenceUsage: true,
		Long: `Deploy every credential listed in a YAML file. Secrets are references only:

  userCredentials:
    - name: ERP_User
      user: svc_erp
      password: {env: ERP_PASSWORD}
  oauth2Credentials:
    - name: Graph
      tokenServiceUrl: https://login.example.com/oauth/token
      clientId: my-app
      clientSecret: {file: secrets/graph.txt}   # relative to the YAML file
  secureParameters:
    - name: ApiKey
      value: {env: API_KEY}

All secrets are resolved before anything is written. Inline secrets and
unknown keys are rejected.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := ops.LoadCredentialsFile(config.GetString(cmd, "file"))
			if err != nil {
				return err
			}
			res, err := ops.ApplyCredentials(tenantExecuter(cmd), f, config.GetBool(cmd, "dry-run"))
			if res != nil {
				output.SetResult(cmd.Context(), res)
				for i := range res.Results {
					logCredentialResult(&res.Results[i])
				}
			}
			return err
		},
	}
	apply.Flags().String("file", "", "Credentials YAML file")
	apply.Flags().Bool("dry-run", false, "Resolve all secrets and validate, without writing")

	del := &cobra.Command{
		Use:          "delete",
		Short:        "Delete a credential (requires --confirm)",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, name := config.GetString(cmd, "kind"), config.GetString(cmd, "name")
			if !config.GetBool(cmd, "confirm") {
				return output.Usagef("deleting %s credential %q breaks integration flows that use it; repeat with --confirm", kind, name)
			}
			res, err := ops.DeleteCredential(tenantExecuter(cmd), kind, name)
			return credentialOutcome(cmd, res, err)
		},
	}
	del.Flags().String("kind", "", "Kind: "+strings.Join(cpi.CredentialKinds, ", "))
	del.Flags().String("name", "", "Credential name")
	del.Flags().Bool("confirm", false, "Confirm the deletion")

	c.AddCommand(list, setUser, setOAuth2, setParam, apply, del)
	return c
}

func credentialOutcome(cmd *cobra.Command, res *ops.CredentialResult, err error) error {
	if res != nil {
		output.SetResult(cmd.Context(), res)
		logCredentialResult(res)
	}
	return err
}

func NewKeystoreCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "keystore",
		Short: "Keystore: list and check expiry, certificates, key pairs, SSH keys, CSRs, chains, history",
	}

	list := &cobra.Command{
		Use:          "list",
		Short:        "List keystore entries with remaining validity",
		SilenceUsage: true,
		Example: `  cpictl keystore list --expiring-within 30d
  cpictl keystore list --expiring-within 30d --fail-on-expiry   # exit 5 in CI`,
		RunE: func(cmd *cobra.Command, args []string) error {
			within, err := parseDays(config.GetString(cmd, "expiring-within"))
			if err != nil {
				return output.Usagef("invalid --expiring-within: %v", err)
			}
			report, err := ops.ListKeystore(tenantExecuter(cmd), config.GetString(cmd, "keystore"), within, config.GetBool(cmd, "fail-on-expiry"), time.Now())
			if report != nil {
				output.SetResult(cmd.Context(), report)
				for _, e := range report.Entries {
					event := log.Info()
					if e.Expired || e.ExpiringSoon {
						event = log.Warn()
					}
					event.Msgf("%-30s %-12s %s  %4d days  %s", e.Alias, e.Type, e.ValidNotAfter.Format(time.DateOnly), e.DaysLeft, e.SubjectDN)
				}
			}
			return err
		},
	}
	list.Flags().String("keystore", "system", "Keystore: "+strings.Join(cpi.Keystores, ", "))
	list.Flags().String("expiring-within", "", "Flag entries expiring within this period (e.g. 30d, 720h)")
	list.Flags().Bool("fail-on-expiry", false, "Exit with code 5 if an entry is expired or expiring")

	export := &cobra.Command{
		Use:          "export-cert",
		Short:        "Export the certificate of a keystore entry as PEM",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			info, err := ops.ExportCertificate(tenantExecuter(cmd), config.GetString(cmd, "alias"), config.GetString(cmd, "keystore"))
			if err != nil {
				return err
			}
			if out := config.GetString(cmd, "out"); out != "" {
				if err := os.WriteFile(out, []byte(info.PEM), 0644); err != nil {
					return err
				}
				log.Info().Msgf("%s written to %s (%s, valid until %s)", info.Alias, out, info.Subject, info.NotAfter.Format(time.DateOnly))
				info.PEM = ""
				output.SetResult(cmd.Context(), map[string]any{"file": out, "certificate": info})
				return nil
			}
			if outputFormat(cmd) == output.FormatJSON {
				output.SetResult(cmd.Context(), info)
				return nil
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), info.PEM)
			return err
		},
	}
	export.Flags().String("alias", "", "Keystore alias")
	export.Flags().String("keystore", "system", "Keystore: "+strings.Join(cpi.Keystores, ", "))
	export.Flags().String("out", "", "Write the PEM to this file instead of stdout")

	imp := &cobra.Command{
		Use:          "import-cert",
		Short:        "Import a certificate (PEM or DER) into the tenant keystore",
		SilenceUsage: true,
		Example:      `  cpictl keystore import-cert --alias partner_acme --file acme.pem`,
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(config.GetString(cmd, "file"))
			if err != nil {
				return output.Usagef("%v", err)
			}
			info, err := ops.ImportCertificate(tenantExecuter(cmd), config.GetString(cmd, "alias"), data, config.GetBool(cmd, "update"))
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), info)
			log.Info().Msgf("%s imported: %s, valid until %s", info.Alias, info.Subject, info.NotAfter.Format(time.DateOnly))
			return nil
		},
	}
	imp.Flags().String("alias", "", "Keystore alias")
	imp.Flags().String("file", "", "Certificate file (PEM or DER)")
	imp.Flags().Bool("update", false, "Replace an existing entry with the same alias")

	c.AddCommand(list, export, imp)
	c.AddCommand(keystoreMaterialCommands()...)
	return c
}

// parseDays accepts "", "30d" or a Go duration.
func parseDays(v string) (time.Duration, error) {
	if v == "" {
		return 0, nil
	}
	if days, ok := strings.CutSuffix(v, "d"); ok {
		d, err := time.ParseDuration(days + "h")
		return 24 * d, err
	}
	return time.ParseDuration(v)
}
