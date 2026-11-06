package cmd

import (
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/mask"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// NewMaskCommand masks files or stdin with the masker cpictl mcp and
// --redact use (pkg/mask), so that pipelines, the cpictl gateway and CPI
// Studio can mask with the same rules.
func NewMaskCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "mask [file...]",
		Short: "Mask personal data and secrets in files or stdin (the masker of cpictl mcp and --redact)",
		Long: `Writes the files (or stdin) to stdout with personal data and secrets masked as ••••:
IBAN, e-mail, phone, payment card, JWT, Bearer/Basic credentials, private keys,
credentials and signatures in URLs, values under sensitive names, and the rules of
--mask-config (docs/ai-agents.md). JSON stays valid JSON and XML is masked element by
element; --lines keeps every line and leaves unmasked lines byte for byte (for files a
patch is made against). stderr gets the counts per detector, never a value.`,
		Example: `  cpictl mask payload.xml
  cat error.txt | cpictl mask --mask-config .cpi/mask.yaml
  cpictl mask --lines --detector email --detector iban --name '^customerNo$' report.csv
  cpictl mask config --mask-config .cpi/mask.yaml        # check rules, show the policy`,
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true", annotationNoEnvelope: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			m, _, err := maskerFromFlags(cmd)
			if err != nil {
				return err
			}
			lines, _ := cmd.Flags().GetBool("lines")
			inputs := args
			if len(inputs) == 0 {
				inputs = []string{"-"}
			}
			total := mask.Stats{}
			for _, in := range inputs {
				var data []byte
				if in == "-" {
					data, err = io.ReadAll(cmd.InOrStdin())
				} else {
					data, err = os.ReadFile(in)
				}
				if err != nil {
					return err
				}
				var out string
				var st mask.Stats
				if lines {
					out, st = m.InPlace(string(data))
				} else {
					out, st = m.Text(string(data))
				}
				total.Add(st)
				if _, err := io.WriteString(cmd.OutOrStdout(), out); err != nil {
					return err
				}
			}
			if total.Total() > 0 {
				log.Info().Msgf("Masked %d values (%s)", total.Total(), total)
			} else {
				log.Info().Msg("Nothing to mask")
			}
			return nil
		},
	}
	addMaskRuleFlags(c)
	c.Flags().Bool("lines", false, "Mask line by line and keep the layout (unmasked lines stay byte for byte)")

	cfg := &cobra.Command{
		Use:   "config",
		Short: "Check masking rules and show the policy they give",
		Long: `Reads --mask-config (and --detector, --name, --rule) like cpictl mcp and --redact do,
and prints the resulting policy: the detectors, sensitive names and rules. An invalid
rule, an unknown detector or a regex that matches empty text is a usage error (exit 2).`,
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			_, c, err := maskerFromFlags(cmd)
			if err != nil {
				return err
			}
			detectors := c.BuiltIns
			if detectors == nil {
				detectors = mask.BuiltIns
			}
			res := map[string]any{"detectors": detectors, "names": nonNil(c.Names), "rules": nonNil(c.Rules), "available": mask.BuiltIns}
			output.SetResult(cmd.Context(), res)
			log.Info().Msgf("Detectors: %v", detectors)
			log.Info().Msgf("Names: %v (and the built-in sensitive names)", c.Names)
			for _, r := range c.Rules {
				log.Info().Msgf("Rule %s %s", r.Kind, r.Pattern)
			}
			return nil
		},
	}
	addMaskRuleFlags(cfg)
	c.AddCommand(cfg)
	return c
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func addMaskRuleFlags(c *cobra.Command) {
	c.Flags().StringArray("detector", nil, "Only these built-in detectors (repeatable; default all, or those of --mask-config)")
	c.Flags().StringArray("name", nil, "Also mask values under names matching this regex (repeatable)")
	c.Flags().StringArray("rule", nil, "A rule: path://Customer/Name or regex:<expression> (repeatable)")
}

// maskerFromFlags builds the masker of --mask-config plus --detector,
// --name and --rule.
func maskerFromFlags(cmd *cobra.Command) (*mask.Masker, mask.Config, error) {
	c, _, err := loadMaskConfig(config.GetString(cmd, "mask-config"))
	if err != nil {
		return nil, c, err
	}
	if d, _ := cmd.Flags().GetStringArray("detector"); len(d) > 0 {
		c.BuiltIns = d
	}
	names, _ := cmd.Flags().GetStringArray("name")
	for _, n := range names {
		if !slices.Contains(c.Names, n) {
			c.Names = append(c.Names, n)
		}
	}
	rules, _ := cmd.Flags().GetStringArray("rule")
	for _, r := range rules {
		kind, pattern, ok := cutRule(r)
		if !ok {
			return nil, c, output.Usagef("--rule %q: write path://Element/Path or regex:<expression>", r)
		}
		c.Rules = append(c.Rules, mask.Rule{Kind: kind, Pattern: pattern})
	}
	m, err := mask.New(c)
	if err != nil {
		return nil, c, output.Usage(fmt.Errorf("masking rules: %w", err))
	}
	return m, c, nil
}

func cutRule(r string) (kind, pattern string, ok bool) {
	switch {
	case len(r) > 5 && r[:5] == "path:":
		return "path", r[5:], true
	case len(r) > 6 && r[:6] == "regex:":
		return "regex", r[6:], true
	}
	return "", "", false
}
