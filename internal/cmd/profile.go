package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Profiles are personal config files $HOME/.cpictl/<name>.yaml, one per
// tenant. The active one is stored in $HOME/.cpictl/current.

var profileNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func profilesDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cpictl"), nil
}

func profilePath(name string) (string, error) {
	if !profileNamePattern.MatchString(name) || strings.HasSuffix(name, ".yaml") {
		return "", fmt.Errorf("invalid profile name %q (letters, digits, '_', '-', '.')", name)
	}
	dir, err := profilesDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".yaml"), nil
}

func listProfileNames() []string {
	dir, err := profilesDir()
	if err != nil {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.yaml"))
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, strings.TrimSuffix(filepath.Base(m), ".yaml"))
	}
	sort.Strings(names)
	return names
}

func activeProfile() string {
	dir, err := profilesDir()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(dir, "current"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// resolveConfigSource picks the config: --config, --profile, CPICTL_PROFILE,
// CPICTL_CONFIG, the active profile, else the default files (both empty).
func resolveConfigSource(configFlag, profileFlag string) (file, profile string, err error) {
	switch {
	case configFlag != "" && profileFlag != "":
		return "", "", fmt.Errorf("use either --config or --profile")
	case configFlag != "":
		return configFlag, "", nil
	case profileFlag != "":
		return "", profileFlag, nil
	case os.Getenv(envPrefix+"_PROFILE") != "":
		return "", os.Getenv(envPrefix + "_PROFILE"), nil
	case os.Getenv(envPrefix+"_CONFIG") != "":
		return os.Getenv(envPrefix + "_CONFIG"), "", nil
	}
	return "", activeProfile(), nil
}

// ProfileInfo describes a profile (never its credentials).
type ProfileInfo struct {
	Name    string `json:"name"`
	TmnHost string `json:"tmnHost,omitempty"`
	Active  bool   `json:"active"`
}

func profileInfo(name, active string) ProfileInfo {
	info := ProfileInfo{Name: name, Active: name == active}
	if p, err := profilePath(name); err == nil {
		v := viper.New()
		v.SetConfigFile(p)
		if v.ReadInConfig() == nil {
			info.TmnHost = v.GetString("tmn-host")
		}
	}
	return info
}

func NewProfileCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "profile",
		Short: "Switch between tenants (profiles in $HOME/.cpictl)",
		Long: `A profile is a personal config file $HOME/.cpictl/<name>.yaml with the connection
of one tenant (same keys as cpictl.yaml, credentials included; keep it chmod 600).

  cpictl profile use qa          make qa the default for every following command
  cpictl --profile dev deploy …  one command against another tenant
  CPICTL_PROFILE=dev             per shell (overrides 'profile use')
  cpictl mcp --profile dev       one MCP server per tenant

Precedence: --config, --profile, CPICTL_PROFILE, CPICTL_CONFIG, 'profile use',
$HOME/cpictl.yaml. A repository's cpictl.yaml is still overlaid; its hosts must
match the profile's.`,
		Annotations: map[string]string{annotationOffline: "true"},
	}
	list := &cobra.Command{
		Use:          "list",
		Short:        "List profiles and mark the active one",
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			_, active, _ := resolveConfigSource("", "")
			profiles := []ProfileInfo{}
			for _, n := range listProfileNames() {
				profiles = append(profiles, profileInfo(n, active))
			}
			output.SetResult(cmd.Context(), map[string]any{"active": active, "profiles": profiles})
			if len(profiles) == 0 {
				dir, _ := profilesDir()
				log.Info().Msgf("No profiles yet: cpictl profile add <name> --service-key <key.json> (or create %s/<name>.yaml, chmod 600)", dir)
			}
			for _, p := range profiles {
				mark := " "
				if p.Active {
					mark = "*"
				}
				log.Info().Msgf("%s %-15s %s", mark, p.Name, p.TmnHost)
			}
			return nil
		},
	}
	use := &cobra.Command{
		Use:          "use <name>",
		Short:        "Make a profile the default (stored in $HOME/.cpictl/current); 'use -' clears it",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := profilesDir()
			if err != nil {
				return err
			}
			current := filepath.Join(dir, "current")
			if args[0] == "-" {
				if err := os.Remove(current); err != nil && !os.IsNotExist(err) {
					return err
				}
				output.SetResult(cmd.Context(), map[string]any{"active": ""})
				log.Info().Msg("No active profile: $HOME/cpictl.yaml is used")
				return nil
			}
			p, err := profilePath(args[0])
			if err != nil {
				return output.Usage(err)
			}
			if !fileExists(p) {
				return output.Usagef("profile %q not found (%s); available: %s", args[0], p, strings.Join(listProfileNames(), ", "))
			}
			if err := os.WriteFile(current, []byte(args[0]+"\n"), 0o600); err != nil {
				return err
			}
			info := profileInfo(args[0], args[0])
			output.SetResult(cmd.Context(), info)
			log.Info().Msgf("Active profile: %s (%s)", info.Name, info.TmnHost)
			if env := os.Getenv(envPrefix + "_PROFILE"); env != "" && env != args[0] {
				log.Warn().Msgf("CPICTL_PROFILE=%s is set in this shell and takes precedence", env)
			}
			return nil
		},
	}
	current := &cobra.Command{
		Use:          "current",
		Short:        "Show the profile that commands use now",
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			file, profile, err := resolveConfigSource("", "")
			if err != nil {
				return output.Usage(err)
			}
			switch {
			case profile != "":
				info := profileInfo(profile, profile)
				output.SetResult(cmd.Context(), info)
				log.Info().Msgf("%s (%s)", info.Name, info.TmnHost)
			case file != "":
				output.SetResult(cmd.Context(), map[string]any{"config": file})
				log.Info().Msgf("No profile, CPICTL_CONFIG=%s", file)
			default:
				output.SetResult(cmd.Context(), map[string]any{"active": ""})
				log.Info().Msg("No profile: $HOME/cpictl.yaml (and the repository's cpictl.yaml) are used")
			}
			return nil
		},
	}
	c.AddCommand(list, use, current, newProfileAddCommand(), newProfileShowCommand(), newProfileRemoveCommand())
	return c
}
