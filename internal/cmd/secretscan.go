package cmd

import (
	"fmt"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/internal/str"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func NewSecretScanCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "secret-scan",
		Short: "Find passwords, tokens and keys in the Partner Directory and in flow configuration parameters",
		Long: `Security material belongs in the secure store (user credentials, secure
parameters, OAuth2 credentials) and the keystore, where flows read it by name.
secret-scan reads the tenant's Partner Directory (string and binary
parameters, including files inside ZIPs) and the configuration parameters of
every integration flow, and reports values that are security material:
private keys, JSON web tokens, access keys of known providers, HTTP
authorization values, URLs with credentials, and values of parameters named
like a password, secret, token or key that are not references (a credential
or alias name).

Findings never carry the value: the kind, the reason and a short fingerprint
(the first 12 hex digits of its SHA-256), so the report can be shared and the
same value recognised across runs.

--pd-dir scans a Partner Directory folder written by pd snapshot instead of
the tenant. Externalised parameters in the content repository and configure
files are checked by lint (rule hardcoded-secret).

Exits with code 5 (failed) when it finds something, so a pipeline stops.`,
		Example: `  cpictl secret-scan
  cpictl secret-scan --only partner-directory
  cpictl secret-scan --only configuration --packages 'Finance*'
  cpictl secret-scan --pd-dir partner-directory`,
		Annotations:  map[string]string{annotationOffline: "pd-dir"},
		SilenceUsage: true,
		RunE:         runSecretScan,
	}
	c.Flags().String("only", "", "Scan only this area: partner-directory or configuration")
	c.Flags().StringSlice("packages", nil, "Configuration: only these packages (IDs or patterns)")
	c.Flags().Int("parallel", 8, "Configuration: artifacts read at a time")
	c.Flags().String("pd-dir", "", "Scan this local Partner Directory folder (pd snapshot) instead of the tenant")
	return c
}

func runSecretScan(cmd *cobra.Command, args []string) error {
	only := config.GetString(cmd, "only")
	if only != "" && only != ops.AreaPartnerDirectory && only != ops.AreaConfiguration {
		return output.Usagef("--only %q: partner-directory or configuration", only)
	}
	res := &ops.SecretScanResult{Source: "tenant", Findings: []ops.SecretFinding{}}
	if dir := config.GetString(cmd, "pd-dir"); dir != "" {
		if only == ops.AreaConfiguration {
			return output.Usagef("--pd-dir scans a Partner Directory folder; configuration is read from the tenant")
		}
		res.Source = dir
		if err := ops.ScanLocalPartnerDirectory(dir, res); err != nil {
			return err
		}
	} else {
		exe := tenantExecuter(cmd)
		if only != ops.AreaConfiguration {
			if err := ops.ScanTenantPartnerDirectory(exe, res); err != nil {
				return err
			}
		}
		if only != ops.AreaPartnerDirectory {
			parallel, _ := cmd.Flags().GetInt("parallel")
			if err := ops.ScanTenantConfigurations(cmd.Context(), exe, str.TrimSlice(config.GetStringSlice(cmd, "packages")), parallel, res); err != nil {
				return err
			}
		}
	}
	ops.SortSecretFindings(res.Findings)
	output.SetResult(cmd.Context(), res)
	for _, f := range res.Findings {
		log.Warn().Str("kind", f.Kind).Str("fingerprint", f.Fingerprint).Msgf("%s: %s", secretPlace(f), f.Reason)
	}
	for _, e := range res.Errors {
		log.Warn().Msgf("configuration not read: %s", e)
	}
	s := res.Scanned
	log.Info().Msgf("Scanned %d string and %d binary Partner Directory parameters, %d configuration parameters of %d artifacts: %d finding(s)",
		s.StringParameters, s.BinaryParameters, s.ConfigurationParameters, s.Artifacts, len(res.Findings))
	if len(res.Findings) > 0 {
		return output.Failed(fmt.Errorf("%d value(s) look like security material: move them to the secure store or keystore", len(res.Findings)))
	}
	return nil
}

func secretPlace(f ops.SecretFinding) string {
	if f.Area == ops.AreaConfiguration {
		return fmt.Sprintf("configuration %s/%s %s", f.Package, f.Artifact, f.Key)
	}
	kind := "string"
	if f.Binary {
		kind = "binary"
	}
	place := fmt.Sprintf("Partner Directory %s parameter %s/%s", kind, f.PID, f.Key)
	if f.Entry != "" {
		place += " (" + f.Entry + ")"
	}
	return place
}
