package cmd

import (
	"encoding/base64"
	"fmt"
	"github.com/cpars-innovation/cpicli/pkg/mask"
	"github.com/cpars-innovation/cpicli/pkg/secretscan"
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

func NewValidateCommand() *cobra.Command {
	c := &cobra.Command{
		Use:          "validate",
		Short:        "Validate an integration flow on the tenant (like Check in the Web UI)",
		SilenceUsage: true,
		Example:      `  cpictl validate --artifact-id OrderIntake`,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ops.ValidateArtifact(tenantExecuter(cmd), config.GetString(cmd, "artifact-id"), config.GetString(cmd, "version"))
			if res != nil {
				output.SetResult(cmd.Context(), res)
				log.Info().Msgf("%s: %s", res.ArtifactID, res.Status)
				if res.Details != "" {
					log.Info().Msg(res.Details)
				}
			}
			return err
		},
	}
	c.Flags().String("artifact-id", "", "Integration flow ID")
	c.Flags().String("version", "active", "Designtime version")
	return c
}

func NewGuidelinesCommand() *cobra.Command {
	c := &cobra.Command{
		Use:          "guidelines",
		Short:        "Check an integration flow against the design guidelines activated on the tenant",
		SilenceUsage: true,
		Long: `Run the design guidelines that the tenant administrator activated against an
integration flow and wait for the result. Violations (not compliant, not
skipped) give exit code 5.`,
		Example: `  cpictl guidelines --artifact-id OrderIntake --output json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			timeout, _ := cmd.Flags().GetDuration("timeout")
			report, err := ops.CheckGuidelines(cmd.Context(), tenantExecuter(cmd), config.GetString(cmd, "artifact-id"),
				config.GetString(cmd, "version"), timeout, 3*time.Second)
			if report != nil {
				output.SetResult(cmd.Context(), report)
				log.Info().Msgf("%s: %s, %d guideline(s), %d violation(s)", report.ArtifactID, report.Status, report.Total, len(report.Violations))
				for _, v := range report.Violations {
					log.Warn().Msgf("%s [%s] %s%s", v.ID, v.Severity, v.Name, errSuffix(v.ViolatedComponents))
				}
			}
			return err
		},
	}
	c.Flags().String("artifact-id", "", "Integration flow ID")
	c.Flags().String("version", "active", "Designtime version")
	c.Flags().Duration("timeout", 2*time.Minute, "Maximum time to wait for the result")
	return c
}

func NewEndpointsCommand() *cobra.Command {
	c := &cobra.Command{
		Use:          "endpoints",
		Short:        "List the URLs of deployed integration flows",
		SilenceUsage: true,
		Example:      `  cpictl endpoints --artifact-id OrderIntake`,
		RunE: func(cmd *cobra.Command, args []string) error {
			eps, err := ops.ListServiceEndpoints(tenantExecuter(cmd), config.GetString(cmd, "artifact-id"))
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), map[string]any{"endpoints": eps})
			for _, e := range eps {
				for _, p := range e.EntryPoints {
					log.Info().Msgf("%s  %s  %s", e.ArtifactID, e.Protocol, p.URL)
				}
			}
			return nil
		},
	}
	c.Flags().String("artifact-id", "", "Only endpoints of this integration flow")
	return c
}

func NewResourcesCommand() *cobra.Command {
	c := &cobra.Command{
		Use:          "resources",
		Short:        "List the resources (scripts, mappings, schemas, ...) of an integration flow",
		SilenceUsage: true,
		Example: `  cpictl resources --artifact-id OrderIntake
  cpictl resources get --artifact-id OrderIntake --name script1.groovy --type groovy`,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := config.GetString(cmd, "artifact-id")
			list, err := ops.ListResources(tenantExecuter(cmd), id, config.GetString(cmd, "version"))
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), map[string]any{"artifactId": id, "resources": list})
			for _, r := range list {
				log.Info().Msgf("%-10s %s", r.Type, r.Name)
			}
			return nil
		},
	}
	c.Flags().String("artifact-id", "", "Integration flow ID")
	c.Flags().String("version", "active", "Designtime version")

	get := &cobra.Command{
		Use:          "get",
		Short:        "Download one resource of an integration flow",
		SilenceUsage: true,
		Long: `Download one resource. In text mode the content is written to stdout (or
--out); with --output json it is part of the result document.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ops.GetResource(tenantExecuter(cmd), config.GetString(cmd, "artifact-id"), config.GetString(cmd, "version"),
				config.GetString(cmd, "name"), config.GetString(cmd, "type"), contentLimit(cmd))
			if err != nil {
				return err
			}
			return emitContent(cmd, res, res.Content)
		},
	}
	get.Flags().String("artifact-id", "", "Integration flow ID")
	get.Flags().String("version", "active", "Designtime version")
	get.Flags().String("name", "", "Resource name, e.g. script1.groovy")
	get.Flags().String("type", "", "Resource type, e.g. groovy, xslt, mmap, xsd, wsdl, jar")
	addContentFlags(get)
	c.AddCommand(get)
	return c
}

func NewDownloadCommand() *cobra.Command {
	c := &cobra.Command{
		Use:          "download",
		Short:        "Download a designtime artifact and extract it into a directory",
		SilenceUsage: true,
		Example: `  cpictl download --artifact-id OrderIntake --dir ./OrderIntake
  cpictl download --artifact-id OrderMapping --artifact-type MessageMapping --dir ./OrderMapping --overwrite`,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ops.DownloadArtifactToDir(tenantExecuter(cmd), config.GetString(cmd, "artifact-type"),
				config.GetString(cmd, "artifact-id"), config.GetString(cmd, "version"), config.GetString(cmd, "dir"), config.GetBool(cmd, "overwrite"))
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), res)
			log.Info().Msgf("%s extracted to %s (%d files)", res.ArtifactID, res.Dir, res.Files)
			return nil
		},
	}
	c.Flags().String("artifact-id", "", "Artifact ID")
	c.Flags().String("artifact-type", "Integration", "Artifact type: "+strings.Join(cpi.ArtifactTypes, ", "))
	c.Flags().String("version", "active", "Designtime version")
	c.Flags().String("dir", "", "Target directory (must be empty unless --overwrite)")
	c.Flags().Bool("overwrite", false, "Replace the content of a non-empty target directory")
	return c
}

// addContentFlags adds --out and --max-bytes to content download commands.
func addContentFlags(c *cobra.Command) {
	c.Flags().String("out", "", "Write the content to this file instead of stdout / the JSON result")
	c.Flags().Int("max-bytes", 0, "Maximum bytes returned in the JSON result (default 65536; 0 with --out: unlimited)")
}

func contentLimit(cmd *cobra.Command) int {
	if config.GetString(cmd, "out") != "" || outputFormat(cmd) == output.FormatText {
		return -1 // full content when it goes to a file or stdout
	}
	return config.GetInt(cmd, "max-bytes")
}

// emitContent writes downloaded content to --out, to stdout (text mode) or
// into the JSON result (result is the full result value).
func emitContent(cmd *cobra.Command, result any, c ops.Content) error {
	raw := []byte(c.Text)
	if c.Base64 != "" {
		var err error
		if raw, err = base64.StdEncoding.DecodeString(c.Base64); err != nil {
			return err
		}
	}
	if out := config.GetString(cmd, "out"); out != "" {
		if err := os.WriteFile(out, raw, 0644); err != nil {
			return err
		}
		output.SetResult(cmd.Context(), map[string]any{"file": out, "size": c.Size})
		log.Info().Msgf("%d bytes written to %s", c.Size, out)
		return nil
	}
	if outputFormat(cmd) == output.FormatJSON {
		output.SetResult(cmd.Context(), result)
		return nil
	}
	if rc, ok := result.(*ops.ResourceContent); ok && redactionOf(cmd.Context()).on {
		masked, _ := secretscan.MaskLines(rc.Name, string(raw), mask.Dots)
		raw = []byte(masked)
	}
	if redactionOf(cmd.Context()).on && cmd.Annotations[annotationPayload] == "true" {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "[payload withheld: %d bytes; %s, or write it to a file with --out]\n", c.Size, hintCLI)
		return err
	}
	_, err := fmt.Fprint(cmd.OutOrStdout(), string(raw))
	return err
}
