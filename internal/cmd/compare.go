package cmd

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/internal/str"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func NewCompareCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "compare <side A> <side B>",
		Short: "Compare two tiers, Git refs or content trees per artifact (content, version, parameters, files)",
		Long: `Compare two sides per artifact. A side is:

  <directory>          a content tree (<package>/<artifact>, as snapshot writes it)
  git:<ref>[:<path>]   a Git ref of the repository (--repo), e.g. git:main:packages
  tenant               the configured tenant (--profile, config, environment)
  tenant:<profile>     the tenant of a profile (~/.cpictl/<profile>.yaml)

Tenants are only read: their artifacts are downloaded and normalized as snapshot
writes them, so only real differences show. Per artifact the status is same,
only_a, only_b, content_differs (as an upload compares: without Bundle-Version and
parameters.prop), version_differs (same content) or parameters_differ (same content
and version); per file added / removed / changed (--diff: unified diffs); per
parameters.prop key differs / only_a / only_b (--show-values: the values). --model
compares changed .iflw models per element and per property instead of per line:
steps, events, gateways, adapters (address, authentication, versions, ...),
sequence flows (source, target, condition) and the flow's own properties; a shape
that only moved is marked moved; sensitive values are masked. For a tenant side
the designtime and running versions, the draft flag and, where the tenant reports
it, who changed the artifact last.`,
		Example: `  cpictl compare tenant:test tenant:prod --package Orders
  cpictl compare packages tenant --diff                 # repository vs tenant (drift)
  cpictl compare tenant:test tenant:prod --artifact Orders_Route --model
  cpictl compare git:release/2026-10:packages git:main:packages
  cpictl compare tenant:dev tenant:test --fail-on-diff --output json`,
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			work, err := os.MkdirTemp("", "cpictl-compare-*")
			if err != nil {
				return err
			}
			defer os.RemoveAll(work)
			sides := make([]ops.CompareSide, 2)
			for i, spec := range args {
				if sides[i], err = compareSide(cmd, spec, filepath.Join(work, fmt.Sprintf("git%d", i))); err != nil {
					return err
				}
			}
			diff, _ := cmd.Flags().GetBool("diff")
			values, _ := cmd.Flags().GetBool("show-values")
			model, _ := cmd.Flags().GetBool("model")
			o := ops.CompareOptions{
				Packages:  str.TrimSlice(config.GetStringSlice(cmd, "package")),
				Artifacts: str.TrimSlice(config.GetStringSlice(cmd, "artifact")),
				Diff:      diff, Values: values, Model: model,
				Parallel: config.GetIntWithFallback(cmd, "parallel", "compare.parallel"),
				WorkDir:  filepath.Join(work, "tenants"),
			}
			res, err := ops.Compare(cmd.Context(), sides[0], sides[1], o)
			if res != nil {
				output.SetResult(cmd.Context(), res)
				all, _ := cmd.Flags().GetBool("all")
				logCompare(res, all)
			}
			if err != nil {
				return err
			}
			if failOnDiff, _ := cmd.Flags().GetBool("fail-on-diff"); failOnDiff {
				if n := len(res.Items) - res.Summary[ops.CompareSame]; n > 0 {
					return output.Failed(fmt.Errorf("%d artifact(s) differ", n))
				}
			}
			return nil
		},
	}
	c.Flags().StringSlice("package", nil, "Only these packages (IDs or patterns)")
	c.Flags().StringSlice("artifact", nil, "Only these artifacts (IDs or patterns)")
	c.Flags().Bool("diff", false, "Unified diffs of changed text files")
	c.Flags().Bool("model", false, "Per element and per property diff of changed .iflw models (steps, adapters, sequence flows, flow properties)")
	c.Flags().Bool("show-values", false, "Parameter values (default: only which keys differ)")
	c.Flags().Bool("all", false, "Also list artifacts that are the same (text output)")
	c.Flags().Bool("fail-on-diff", false, "Exit with code 5 when any artifact differs")
	c.Flags().String("repo", ".", "Git repository for git: sides")
	c.Flags().Int("parallel", 8, "Downloads at the same time per tenant (config: compare.parallel)")
	return c
}

// compareSide turns a side argument into a CompareSide; git refs are
// extracted below gitDir.
func compareSide(cmd *cobra.Command, spec, gitDir string) (ops.CompareSide, error) {
	switch {
	case spec == "tenant":
		sd := serviceDetails(cmd)
		if sd.Host == "" {
			return ops.CompareSide{}, output.Usagef("side tenant: no tenant configured (tmn-host); use tenant:<profile> or configure one")
		}
		return ops.CompareSide{Label: cmp.Or(activeProfileLabel(cmd), "tenant"), Exe: cpi.InitHTTPExecuter(sd)}, nil
	case strings.HasPrefix(spec, "tenant:"):
		name := strings.TrimPrefix(spec, "tenant:")
		sd, err := profileServiceDetails(name)
		if err != nil {
			return ops.CompareSide{}, output.Usage(err)
		}
		return ops.CompareSide{Label: name, Exe: cpi.InitHTTPExecuter(sd)}, nil
	case strings.HasPrefix(spec, "git:"):
		ref, sub, _ := strings.Cut(strings.TrimPrefix(spec, "git:"), ":")
		if ref == "" {
			return ops.CompareSide{}, output.Usagef("side %s: git:<ref>[:<path>]", spec)
		}
		repo, _ := cmd.Flags().GetString("repo")
		if err := ops.GitTree(cmd.Context(), repo, ref, sub, gitDir); err != nil {
			return ops.CompareSide{}, err
		}
		return ops.CompareSide{Label: "git:" + ref, Dir: gitDir}, nil
	default:
		return ops.CompareSide{Label: spec, Dir: spec}, nil
	}
}

// activeProfileLabel names the configured tenant by its profile, if any.
func activeProfileLabel(cmd *cobra.Command) string {
	_, profile, _ := resolveConfigSource(config.GetString(cmd, "config"), config.GetString(cmd, "profile"))
	return profile
}

// profileServiceDetails reads the tenant connection of a profile without
// touching the active configuration.
func profileServiceDetails(name string) (*cpi.ServiceDetails, error) {
	p, err := profilePath(name)
	if err != nil {
		return nil, err
	}
	if !fileExists(p) {
		return nil, fmt.Errorf("profile %q not found (%s); available: %s", name, p, strings.Join(listProfileNames(), ", "))
	}
	v := viper.New()
	v.SetConfigFile(p)
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("profile %s: %w", name, err)
	}
	sd := &cpi.ServiceDetails{
		Host:              v.GetString("tmn-host"),
		Userid:            v.GetString("tmn-userid"),
		Password:          v.GetString("tmn-password"),
		OauthHost:         v.GetString("oauth-host"),
		OauthClientId:     v.GetString("oauth-clientid"),
		OauthClientSecret: v.GetString("oauth-clientsecret"),
		OauthPath:         cmp.Or(v.GetString("oauth-path"), "/oauth/token"), // the default of --oauth-path
	}
	if sd.Host == "" {
		return nil, fmt.Errorf("profile %s has no tmn-host", name)
	}
	if sd.OauthHost != "" {
		sd.Userid, sd.Password = "", ""
	}
	return sd, nil
}

func logCompare(res *ops.CompareResult, all bool) {
	for _, it := range res.Items {
		if it.Status == ops.CompareSame && !all {
			continue
		}
		versions := ""
		if it.VersionA != "" || it.VersionB != "" {
			versions = fmt.Sprintf(" (%s: %s, %s: %s)", res.A, cmp.Or(it.VersionA, "-"), res.B, cmp.Or(it.VersionB, "-"))
		}
		ev := log.Info()
		if it.Status != ops.CompareSame {
			ev = log.Warn()
		}
		extra := ""
		for _, side := range []*ops.CompareSideInfo{it.A, it.B} {
			if side != nil && side.Draft {
				extra += " [draft]"
			}
		}
		if it.B != nil && it.B.ModifiedBy != "" {
			extra += " changed by " + it.B.ModifiedBy
		}
		ev.Str("status", it.Status).Msgf("%-17s %s/%s%s%s", it.Status, it.Package, it.Artifact, versions, extra)
		for _, f := range it.Files {
			log.Info().Msgf("    %-8s %s", f.Change, f.Path)
			if f.ModelError != "" {
				log.Warn().Msgf("      model: %s", f.ModelError)
			}
			if f.Model != nil {
				lines := f.Model.Summary()
				if len(lines) == 0 {
					lines = []string{"(model unchanged: formatting only)"}
				}
				for _, line := range lines {
					log.Info().Msg("      " + line)
				}
			}
			if f.Diff != "" {
				for _, line := range strings.Split(strings.TrimSuffix(f.Diff, "\n"), "\n") {
					log.Info().Msg("      " + line)
				}
			}
		}
		for _, p := range it.Parameters {
			v := ""
			if p.A != "" || p.B != "" {
				v = fmt.Sprintf(": %q -> %q", p.A, p.B)
			}
			log.Info().Msgf("    param %-7s %s%s", p.Change, p.Key, v)
		}
	}
	var parts []string
	for _, s := range []string{ops.CompareSame, ops.CompareContentDiffers, ops.CompareVersionDiffers, ops.CompareParametersDiffer, ops.CompareOnlyA, ops.CompareOnlyB} {
		if n := res.Summary[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s))
		}
	}
	log.Info().Msgf("%s vs %s: %d artifact(s): %s", res.A, res.B, len(res.Items), cmp.Or(strings.Join(parts, ", "), "none"))
	for _, e := range res.Errors {
		log.Error().Msg(e)
	}
}
