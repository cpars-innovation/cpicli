package cmd

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/logger"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/internal/stats"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

func NewCmdRoot(version string) *cobra.Command {
	// rootCmd represents the base command when called without any subcommands
	rootCmd := &cobra.Command{
		Use:     "cpictl",
		Version: version,
		Short:   "Build, deploy and operate SAP Cloud Integration content",
		Long: `cpictl builds, deploys and operates SAP Cloud Integration (CPI) content from
the command line, CI/CD pipelines and AI agents (cpictl mcp).

Tenant connection: --tmn-host plus OAuth (--oauth-host, --oauth-clientid,
--oauth-clientsecret) or Basic Auth (--tmn-userid, --tmn-password). Every flag
can also be set as CPICTL_<FLAG> environment variable or in $HOME/cpictl.yaml.

Use --output json for one machine-readable result document on stdout.
Exit codes: 0 ok, 2 usage, 3 auth, 4 tenant HTTP error, 5 failed, 6 timeout,
7 partial failure.`,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// You can bind cobra and viper in a few locations, but PersistencePreRunE on the root command works well
			if err := initializeConfig(cmd); err != nil {
				return err
			}
			if n, err := cmd.Flags().GetInt("read-retries"); err == nil {
				if n < 0 {
					return output.Usagef("--read-retries must not be negative")
				}
				cpi.ReadRetries = n
			}
			return nil
		},
	}

	rootCmd.PersistentFlags().String("config", "", "config file (default: the profile, else $CPICTL_CONFIG, else $HOME/cpictl.yaml plus ./cpictl.yaml of the repository)")
	addScopeFlags(rootCmd)
	rootCmd.PersistentFlags().String("profile", "", "Profile to use: $HOME/.cpictl/<name>.yaml (default: $CPICTL_PROFILE, else the one chosen with 'cpictl profile use')")

	// Define cobra flags, the default value has the lowest (least significant) precedence
	rootCmd.PersistentFlags().String("tmn-host", "", "Tenant host of Cloud Integration (or API portal host for API Management)")
	rootCmd.PersistentFlags().String("tmn-userid", "", "User ID for Basic Auth")
	rootCmd.PersistentFlags().String("tmn-password", "", "Password for Basic Auth")
	rootCmd.PersistentFlags().String("oauth-host", "", "OAuth token server host")
	rootCmd.PersistentFlags().String("oauth-clientid", "", "Client ID for using OAuth")
	rootCmd.PersistentFlags().String("oauth-clientsecret", "", "Client Secret for using OAuth")
	rootCmd.PersistentFlags().String("oauth-path", "/oauth/token", "Path for OAuth token server")
	rootCmd.PersistentFlags().String("gateway", "", "URL of a cpictl gateway including the SAP tenant (https://gw.example.com/t/dev), instead of --tmn-host and a service key; the token comes from $CPICTL_GATEWAY_TOKEN")

	rootCmd.PersistentFlags().String("tier", "", "Tier of the tenant (dev, test, prod, ...), usually set in the profile; on prod, sending test messages and test runs are refused unless --allow-production")
	rootCmd.PersistentFlags().Bool("allow-production", false, "Allow test messages and test runs on a tenant whose tier is prod")
	rootCmd.PersistentFlags().Int("read-retries", cpi.ReadRetries, "Retries of a tenant read (GET) answered with 429, 502, 503 or 504, with backoff 2s, 4s, 8s ... (0: none). Writes are never retried")
	rootCmd.PersistentFlags().String("summary", "", "Append a markdown job summary of orchestrator, configure, deploy, undeploy and snapshot to this file (default: $GITHUB_STEP_SUMMARY when set; \"off\": none)")
	rootCmd.PersistentFlags().Bool("debug", false, "Show debug logs")
	rootCmd.PersistentFlags().String("events", "", "Write progress events as JSON lines (start, one per artifact, end) to this file, or to stderr with \"-\" (see the README, Output)")
	rootCmd.PersistentFlags().String("output", output.FormatText, "Output format: text or json. With json the result is written to stdout as one JSON document and logs are written to stderr as JSON lines")

	rootCmd.MarkFlagsRequiredTogether("tmn-userid", "tmn-password")
	rootCmd.MarkFlagsRequiredTogether("oauth-host", "oauth-clientid", "oauth-clientsecret")

	return rootCmd
}

// NewCLI returns the root command with all subcommands attached.
func NewCLI(version string) *cobra.Command {
	rootCmd := NewCmdRoot(version)
	rootCmd.AddCommand(NewDeployCommand())
	rootCmd.AddCommand(NewUndeployCommand())
	rootCmd.AddCommand(NewStatusCommand())
	logsCmd := NewLogsCommand()
	logsCmd.AddCommand(renamed(NewLogLevelCommand(), "level"), renamed(NewLogFilesCommand(), "files"))
	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(NewValidateCommand())
	rootCmd.AddCommand(NewScriptDocCommand())
	rootCmd.AddCommand(NewBackupCommand())
	rootCmd.AddCommand(NewGuidelinesCommand())
	rootCmd.AddCommand(NewEndpointsCommand())
	rootCmd.AddCommand(NewResourcesCommand())
	rootCmd.AddCommand(NewDownloadCommand())
	rootCmd.AddCommand(NewCredentialsCommand())
	rootCmd.AddCommand(NewKeystoreCommand())
	rootCmd.AddCommand(NewPackagesCommand())
	rootCmd.AddCommand(NewArtifactsCommand())
	rootCmd.AddCommand(NewParamsCommand())
	rootCmd.AddCommand(NewSendCommand())
	rootCmd.AddCommand(NewTestCommand())
	rootCmd.AddCommand(NewIDocCommand())
	rootCmd.AddCommand(legacyName(NewLogLevelCommand(), "logs level"))
	rootCmd.AddCommand(NewDiscoverCommand())
	rootCmd.AddCommand(NewGraphCommand())
	rootCmd.AddCommand(NewIFlowCommand())
	rootCmd.AddCommand(NewSkillsCommand())
	rootCmd.AddCommand(NewVersionCommand())
	rootCmd.AddCommand(NewAuthCommand())
	rootCmd.AddCommand(whoAmIAlias(NewAuthStatusCommand()))
	rootCmd.AddCommand(NewDriftCommand())
	rootCmd.AddCommand(NewDataStoreCommand())
	rootCmd.AddCommand(NewLocksCommand())
	rootCmd.AddCommand(NewRuntimesCommand())
	rootCmd.AddCommand(NewChangedSinceCommand())
	rootCmd.AddCommand(NewComponentsCommand())
	rootCmd.AddCommand(NewConnectionsCommand())
	rootCmd.AddCommand(NewTenantSettingsCommand())
	rootCmd.AddCommand(NewDeleteCommand())
	rootCmd.AddCommand(NewValMapCommand())
	rootCmd.AddCommand(NewPGPCommand())
	rootCmd.AddCommand(NewAccessPoliciesCommand())
	rootCmd.AddCommand(NewAuditLogCommand())
	rootCmd.AddCommand(NewVariablesCommand())
	rootCmd.AddCommand(NewJMSCommand())
	rootCmd.AddCommand(NewNumberRangesCommand())
	rootCmd.AddCommand(legacyName(NewLogFilesCommand(), "logs files"))
	rootCmd.AddCommand(NewIdempotentCommand())
	rootCmd.AddCommand(NewIDMappingsCommand())
	rootCmd.AddCommand(NewProfileCommand())
	rootCmd.AddCommand(NewStatsCommand())
	rootCmd.AddCommand(NewDoctorCommand())
	rootCmd.AddCommand(NewLintCommand())
	rootCmd.AddCommand(NewSecretScanCommand())
	rootCmd.AddCommand(NewCompareCommand())
	rootCmd.AddCommand(NewTransportCommand())
	rootCmd.AddCommand(NewMatrixCommand())
	rootCmd.AddCommand(NewMockTenantCommand())
	rootCmd.AddCommand(NewMCPCommand(version))
	syncCmd := NewSyncCommand()
	syncCmd.AddCommand(NewAPIProxyCommand())
	syncCmd.AddCommand(NewAPIProductCommand())
	rootCmd.AddCommand(syncCmd)
	updateCmd := NewUpdateCommand()
	updateCmd.AddCommand(NewArtifactCommand())
	updateCmd.AddCommand(NewPackageCommand())
	rootCmd.AddCommand(updateCmd)
	snapshotCmd := NewSnapshotCommand()
	snapshotCmd.AddCommand(NewRestoreCommand())
	rootCmd.AddCommand(snapshotCmd)
	rootCmd.AddCommand(legacyName(NewPDSnapshotCommand(), "pd snapshot"))
	rootCmd.AddCommand(legacyName(NewPDDeployCommand(), "pd deploy"))
	pdCmd := NewPDCommand()
	pdCmd.AddCommand(renamed(NewPDSnapshotCommand(), "snapshot"), renamed(NewPDDeployCommand(), "deploy"))
	rootCmd.AddCommand(pdCmd)
	rootCmd.AddCommand(NewConfigGenerateCommand())
	rootCmd.AddCommand(NewOrchestratorCommand())
	rootCmd.AddCommand(NewConfigureCommand())
	return rootCmd
}

// Execute runs the CLI with the process arguments and exits with the
// contract exit code. This is called by main.main().
func Execute(version, buildTime string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := Run(ctx, os.Args[1:], os.Stdout, os.Stderr, version, buildTime)
	stop()
	os.Exit(code)
}

// Run executes the CLI and returns the exit code (see internal/exitcode).
// Results go to stdout, logs to stderr.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer, version, buildTime string) int {
	begin := time.Now()
	// logs and --events - share stderr: one lock for every writer
	stderr = zerolog.SyncWriter(stderr)
	rootCmd := NewCLI(version)
	rootCmd.SetVersionTemplate(fmt.Sprintf("cpictl version {{.Version}} (built %s)\n", buildTime))
	rootCmd.SetArgs(args)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return output.Usage(err) })

	// The format is needed before cobra has parsed the flags (e.g. to report
	// a flag parsing error as JSON)
	format := scanOutputFormat(args)
	logger.Init(stderr, format == output.FormatJSON, false)

	// Errors raised before a command's RunE starts are usage/config errors
	started := false
	walkCommands(rootCmd, func(c *cobra.Command) {
		if format == output.FormatJSON {
			c.SilenceUsage = true // usage text would break the JSON-lines stderr contract
		}
		if run := c.RunE; run != nil {
			c.RunE = func(cmd *cobra.Command, args []string) error {
				started = true
				if err := startEvents(cmd, stderr); err != nil {
					return err
				}
				return run(cmd, args)
			}
		}
	})

	ctx = output.WithResultHolder(ctx)
	ctx = output.WithEvents(ctx)
	cmd, err := rootCmd.ExecuteContextC(ctx)
	err = withScopeRefusal(err)
	if cmd != nil {
		if f := cmd.Flags().Lookup("output"); f != nil && f.Changed {
			format = outputFormat(cmd)
		}
	}

	code := output.ExitCode(err)
	if err != nil && !started && code == exitcode.Error {
		code = exitcode.Usage
	}
	if err != nil {
		log.Error().Int("exitCode", code).Msg(logger.GetErrorDetails(err))
	}

	// Every command that ran reports how long it took (not --help/--version)
	elapsed := time.Since(begin)
	if started {
		ev := log.Info()
		if err != nil {
			ev = log.Warn()
		}
		ev.Int64("durationMs", elapsed.Milliseconds()).Msgf("⏱ %s %s in %s", commandName(cmd), finishedVerb(err), formatElapsed(elapsed))
		if cmd.Annotations[annotationNoStats] != "true" {
			stats.Record(commandName(cmd), stats.SourceCLI, code, elapsed)
		}
		writeSummary(cmd, output.Result(ctx), code, elapsed)
		output.Emit(ctx, output.EventEnd, output.EndData{ExitCode: code, ErrorCategory: output.Category(code), DurationMs: elapsed.Milliseconds()})
	}
	output.CloseEvents(ctx)

	// --help/--version and commands without RunE produce no result document
	ownsStdout := cmd != nil && cmd.Annotations[annotationNoEnvelope] == "true" && started
	if format == output.FormatJSON && (started || err != nil) && !ownsStdout {
		env := output.Envelope{Command: commandName(cmd), OK: code == exitcode.OK, ExitCode: code, ErrorCategory: output.Category(code),
			SchemaVersion: output.SchemaVersion, DurationMs: elapsed.Milliseconds(), Result: output.Result(ctx)}
		if err != nil {
			env.Error = err.Error()
		}
		if werr := output.WriteEnvelope(stdout, env); werr != nil && code == exitcode.OK {
			code = exitcode.Error
		}
	}
	return code
}

// startEvents activates the --events stream for cmd (Run writes the end
// event and closes a file).
func startEvents(cmd *cobra.Command, stderr io.Writer) error {
	switch dest, _ := cmd.Flags().GetString("events"); dest {
	case "":
	case "-":
		output.StartEvents(cmd.Context(), nopCloser{stderr}, commandName(cmd))
	default:
		f, err := os.Create(dest)
		if err != nil {
			return output.Usagef("--events: %v", err)
		}
		output.StartEvents(cmd.Context(), f, commandName(cmd))
	}
	return nil
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

// annotationNoStats marks commands that are not recorded in the usage statistics.
const annotationNoStats = "cpicli/no-stats"

func finishedVerb(err error) string {
	if err != nil {
		return "failed"
	}
	return "finished"
}

// formatElapsed prints 350ms, 12.3s or 4m5s.
func formatElapsed(d time.Duration) string {
	switch {
	case d < time.Second:
		return d.Round(time.Millisecond).String()
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return d.Round(time.Second).String()
	}
}

func walkCommands(c *cobra.Command, fn func(*cobra.Command)) {
	fn(c)
	for _, sub := range c.Commands() {
		walkCommands(sub, fn)
	}
}

func commandName(cmd *cobra.Command) string {
	if cmd == nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()))
}

// scanOutputFormat finds --output in raw arguments (or CPICTL_OUTPUT).
func scanOutputFormat(args []string) string {
	value := os.Getenv(envPrefix + "_OUTPUT")
	for i, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--output" && i+1 < len(args) {
			value = args[i+1]
		} else if v, ok := strings.CutPrefix(arg, "--output="); ok {
			value = v
		}
	}
	if value == output.FormatJSON {
		return output.FormatJSON
	}
	return output.FormatText
}

// outputFormat returns the effective output format of a parsed command.
func outputFormat(cmd *cobra.Command) string {
	if config.GetString(cmd, "output") == output.FormatJSON {
		return output.FormatJSON
	}
	return output.FormatText
}

// envPrefix is the prefix of the environment variables bound to flags.
const envPrefix = "CPICTL"

// legacyEnvPrefix is the FlashPipe prefix, only used to warn about old settings.
const legacyEnvPrefix = "FLASHPIPE"

// renamed gives a command (built by its constructor, so with the same flags
// and config keys) a new name below another command.
func renamed(c *cobra.Command, name string) *cobra.Command {
	_, args, _ := strings.Cut(c.Use, " ")
	c.Use = strings.TrimSpace(name + " " + args)
	c.Example = renameExamples(c.Example, c.Name(), name)
	return c
}

// legacyName keeps a command under its old top-level name: it works as
// before (flags, config keys, environment variables, output), but is no
// longer listed. No notice is printed: stderr carries JSON log lines with
// --output json, and pipelines keep running unchanged.
func legacyName(c *cobra.Command, now string) *cobra.Command {
	c.Hidden = true
	c.Short = "Old name of '" + now + "'"
	return c
}

// whoAmIAlias keeps cpictl whoami as a short, unlisted way to cpictl auth
// status: it is what people type.
func whoAmIAlias(c *cobra.Command) *cobra.Command {
	c.Use, c.Aliases, c.Hidden = "whoami", nil, true
	c.Short = "Same as 'auth status'"
	return c
}

// renameExamples rewrites "cpictl <old>" in examples.
func renameExamples(examples, old, now string) string {
	return strings.ReplaceAll(examples, "cpictl "+old, "cpictl "+now)
}

// annotationOffline marks commands that do not talk to a tenant: "true", the
// name of a flag that makes the command offline when set (e.g. discover --dir),
// or "!" and the name of a flag that makes it talk to one (e.g. "!online").
const annotationOffline = "cpicli/offline"

// isOffline reports whether cmd runs without a tenant (annotationOffline).
func isOffline(cmd *cobra.Command) bool {
	a := cmd.Annotations[annotationOffline]
	switch {
	case a == "true":
		return true
	case a == "":
		return false
	case strings.HasPrefix(a, "!"):
		return !config.GetBool(cmd, a[1:])
	}
	return config.GetString(cmd, a) != ""
}

// legacySettingsHint warns about FlashPipe-era settings that are no longer read.
func legacySettingsHint(cfgFile string) string {
	var found []string
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, legacyEnvPrefix+"_") {
			found = append(found, name)
		}
	}
	var hints []string
	if len(found) > 0 {
		sort.Strings(found)
		hints = append(hints, fmt.Sprintf("FLASHPIPE_* environment variables are not read, rename them to CPICTL_* (found: %s)", strings.Join(found, ", ")))
	}
	if cfgFile == "" {
		if home, err := os.UserHomeDir(); err == nil {
			_, errOld := os.Stat(filepath.Join(home, "flashpipe.yaml"))
			_, errNew := os.Stat(filepath.Join(home, "cpictl.yaml"))
			if errOld == nil && errNew != nil {
				hints = append(hints, "$HOME/flashpipe.yaml is not read, rename it to $HOME/cpictl.yaml")
			}
		}
	}
	if len(hints) == 0 {
		return ""
	}
	return strings.Join(hints, "; ") + " (see docs/migrating-from-flashpipe.md)"
}

func hintSuffix(hint string) string {
	if hint == "" {
		return ""
	}
	return ". Note: " + hint
}

// configFile is a loaded config file.
// loadedConfig are the config files (profile, home, project) of the current
// command, for doctor.
var loadedConfig []string

type configFile struct {
	path    string
	v       *viper.Viper
	project bool
}

// secretConfigKeys are settings that hold secrets.
var secretConfigKeys = []string{"tmn-password", "oauth-clientsecret", "runtime-oauth-clientsecret", "runtime-password"}

// credentialConfigKeys identify an account (secrets included).
var credentialConfigKeys = append([]string{"tmn-userid", "oauth-clientid", "runtime-oauth-clientid", "runtime-userid"}, secretConfigKeys...)

// hostConfigKeys are the hosts that credentials are sent to.
var hostConfigKeys = []string{"tmn-host", "oauth-host", "runtime-oauth-host"}

// loadConfigFiles reads the config files and merges them into viper: the
// explicit file (--config or CPICTL_CONFIG) alone, or else $HOME/cpictl.yaml
// overlaid by the project file (cpictl.yaml in the current directory or a
// parent up to the repository root). Default files are opened by their full
// name: a search by base name would also pick up an extension-less
// $HOME/cpictl (for example the binary itself).
func loadConfigFiles(explicit, profile string, offline bool) ([]configFile, error) {
	var paths []configFile
	if explicit != "" {
		paths = append(paths, configFile{path: explicit})
	} else {
		home, _ := os.UserHomeDir()
		if profile != "" {
			p, err := profilePath(profile)
			if err != nil {
				return nil, err
			}
			if !fileExists(p) {
				return nil, fmt.Errorf("profile %q not found (%s); available: %s", profile, p, strings.Join(listProfileNames(), ", "))
			}
			paths = append(paths, configFile{path: p})
		} else if home != "" {
			if p := filepath.Join(home, "cpictl.yaml"); fileExists(p) {
				paths = append(paths, configFile{path: p})
			}
		}
		if p := findProjectConfig(home); p != "" {
			paths = append(paths, configFile{path: p, project: true})
		}
	}
	files := make([]configFile, 0, len(paths))
	for _, f := range paths {
		f.v = viper.New()
		f.v.SetConfigFile(f.path)
		if err := f.v.ReadInConfig(); err != nil {
			return nil, err
		}
		if profile != "" && !f.project {
			if err := profileSecretsFromKeychain(f.v, profile, offline); err != nil {
				return nil, err
			}
		}
		files = append(files, f)
	}
	if err := checkProjectConfig(files); err != nil {
		return nil, err
	}
	for _, f := range files {
		if err := viper.MergeConfigMap(f.v.AllSettings()); err != nil {
			return nil, err
		}
	}
	return files, nil
}

// connectionKeys choose where a command connects to.
var connectionKeys = []string{"tmn-host", "gateway"}

// fromFlagOrEnv reports whether a setting is given as a flag or as an
// environment variable (not by a config file).
func fromFlagOrEnv(cmd *cobra.Command, key string) bool {
	return flagOrEnv(cmd, key) != ""
}

// flagOrEnv is the value of a setting given as a flag, else as an
// environment variable ("" if neither).
func flagOrEnv(cmd *cobra.Command, key string) string {
	if f := cmd.Flags().Lookup(key); f != nil && f.Changed {
		return f.Value.String()
	}
	return os.Getenv(envPrefix + "_" + strings.ToUpper(strings.ReplaceAll(key, "-", "_")))
}

// connectionOutsideFiles: the tenant or gateway is given as a flag or in the
// environment.
func connectionOutsideFiles(cmd *cobra.Command) bool {
	return slices.ContainsFunc(connectionKeys, func(k string) bool { return fromFlagOrEnv(cmd, k) })
}

// explicitProfile: --profile, CPICTL_PROFILE, --config or CPICTL_CONFIG
// chose the configuration (not the active profile).
func explicitProfile(cmd *cobra.Command) bool {
	return config.GetString(cmd, "profile") != "" || config.GetString(cmd, "config") != "" ||
		os.Getenv(envPrefix+"_PROFILE") != "" || os.Getenv(envPrefix+"_CONFIG") != ""
}

// checkCredentialMix refuses a host from a flag or the environment that
// differs from the one of a config file whose credentials would be used:
// the file's secrets would go to a host they do not belong to.
func checkCredentialMix(cmd *cobra.Command, files []configFile) error {
	for _, f := range files {
		if f.project {
			continue
		}
		var secrets []string
		for _, k := range secretConfigKeys {
			if f.v.IsSet(k) && !fromFlagOrEnv(cmd, k) {
				secrets = append(secrets, k)
			}
		}
		if len(secrets) == 0 {
			continue
		}
		for _, k := range append(slices.Clone(hostConfigKeys), "gateway") {
			if !f.v.IsSet(k) || !fromFlagOrEnv(cmd, k) {
				continue
			}
			if given := flagOrEnv(cmd, k); given != f.v.GetString(k) {
				return mixError(k, given, f.path, f.v.GetString(k), secrets)
			}
		}
		// the host each secret is sent to, with its fallback: a profile
		// usually has no runtime-oauth-host (the runtime key's token server is
		// oauth-host), so CPICTL_RUNTIME_OAUTH_HOST alone must not redirect
		// its runtime secret
		for _, s := range secrets {
			chain := secretHosts[s]
			fileHost, effective, from := "", "", ""
			for _, k := range chain {
				if fileHost == "" {
					fileHost = f.v.GetString(k)
				}
				v := f.v.GetString(k)
				if fromFlagOrEnv(cmd, k) {
					v = flagOrEnv(cmd, k)
				}
				if effective == "" && v != "" {
					effective, from = v, k
				}
			}
			if effective != fileHost {
				return mixError(from, effective, f.path, fileHost, []string{s})
			}
		}
	}
	return nil
}

// secretHosts are the settings, in order of precedence, that name the host
// a secret is sent to.
var secretHosts = map[string][]string{
	"tmn-password":               {"tmn-host"},
	"oauth-clientsecret":         {"oauth-host"},
	"runtime-oauth-clientsecret": {"runtime-oauth-host", "oauth-host"},
	"runtime-password":           {"tmn-host"},
}

func mixError(key, given, file, fileHost string, secrets []string) error {
	return fmt.Errorf("%s is %s from a flag or the environment, but %s holds the credentials for %s (%s): "+
		"give the credentials too, or use another profile, so they are not sent to another host",
		key, given, file, cmp.Or(fileHost, "its own host"), strings.Join(secrets, ", "))
}

// findProjectConfig looks for cpictl.yaml from the working directory up to
// the first directory with a .git entry; home itself is not a project.
func findProjectConfig(home string) string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if dir == home {
			return ""
		}
		if p := filepath.Join(dir, "cpictl.yaml"); fileExists(p) {
			return p
		}
		parent := filepath.Dir(dir)
		if fileExists(filepath.Join(dir, ".git")) || parent == dir {
			return ""
		}
		dir = parent
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// checkProjectConfig enforces the rules for project files, which are usually
// committed and come with the repository: they hold no secrets, and they
// cannot send the credentials of the home file to other hosts.
func checkProjectConfig(files []configFile) error {
	var home, project *configFile
	for i := range files {
		if files[i].project {
			project = &files[i]
		} else {
			home = &files[i]
		}
	}
	if project == nil {
		return nil
	}
	for _, k := range secretConfigKeys {
		if project.v.InConfig(k) {
			return fmt.Errorf("%s contains %s: a project config file is shared with the repository, keep secrets in $HOME/cpictl.yaml, CPICTL_CONFIG or the environment", project.path, k)
		}
	}
	// the tier belongs to the tenant (profile), and a repository file must
	// not lift the production guard
	for _, k := range []string{"tier", "allow-production"} {
		if project.v.InConfig(k) {
			return fmt.Errorf("%s contains %s: set it in the profile or $HOME/cpictl.yaml (a repository file must not change the production guard)", project.path, k)
		}
	}
	// the user's gateway token is sent to the gateway: a repository file
	// must not choose where
	if project.v.InConfig("gateway") {
		return fmt.Errorf("%s contains gateway: set it in the profile or $HOME/cpictl.yaml (a repository file must not choose where your token is sent)", project.path)
	}
	// login decides which Entra tenant and app the user authenticates to: a
	// repository file must not choose where sign-in goes
	if project.v.InConfig("login") {
		return fmt.Errorf("%s contains login: set it in the profile or $HOME/cpictl.yaml (a repository file must not choose where you sign in)", project.path)
	}
	if home == nil {
		return nil
	}
	var homeCreds []string
	for _, k := range credentialConfigKeys {
		if home.v.InConfig(k) && os.Getenv(envKey(k)) == "" {
			homeCreds = append(homeCreds, k)
		}
	}
	if len(homeCreds) == 0 {
		return nil
	}
	for _, k := range hostConfigKeys {
		if project.v.InConfig(k) && project.v.GetString(k) != home.v.GetString(k) {
			return fmt.Errorf("%s sets %s to %q, but the credentials (%s) come from %s, which is configured for %q. "+
				"Credentials from your home config are only sent to its own hosts: put this tenant's connection into its own file (CPICTL_CONFIG / --config) or the environment, or remove %s from %s",
				project.path, k, project.v.GetString(k), strings.Join(homeCreds, ", "), home.path, home.v.GetString(k), k, project.path)
		}
	}
	return nil
}

func envKey(key string) string {
	return envPrefix + "_" + strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
}

// configPermissionError refuses a config file that every user may write: it
// decides which hosts credentials are sent to (Unix permissions; not checked
// on Windows). Group-writable files are accepted (user-private groups).
func configPermissionError(files []configFile) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	for _, f := range files {
		if info, err := os.Stat(f.path); err == nil && info.Mode().Perm()&0o002 != 0 {
			return output.Usagef("%s can be changed by every user, so it could redirect your credentials; run: chmod o-w %s", f.path, f.path)
		}
	}
	return nil
}

// configPermissionWarning warns when a config file holds secrets but can be
// read by other users (Unix permissions; not checked on Windows).
func configPermissionWarning(files []configFile) string {
	if runtime.GOOS == "windows" {
		return ""
	}
	for _, f := range files {
		info, err := os.Stat(f.path)
		if err != nil || info.Mode().Perm()&0o077 == 0 {
			continue
		}
		for _, k := range secretConfigKeys {
			if f.v.InConfig(k) {
				return fmt.Sprintf("%s contains %s but can be read by other users; run: chmod 600 %s", f.path, k, f.path)
			}
		}
	}
	return ""
}

func initializeConfig(cmd *cobra.Command) error {
	if err := installScope(cmd); err != nil {
		return err
	}
	cfgFile, profile, err := resolveConfigSource(config.GetString(cmd, "config"), config.GetString(cmd, "profile"))
	if err != nil {
		return output.Usage(err)
	}
	// a tenant given in the environment or as a flag is a connection of its
	// own: the active profile (chosen earlier, for another tenant) is not
	// mixed into it
	skippedProfile := ""
	if profile != "" && !explicitProfile(cmd) && connectionOutsideFiles(cmd) {
		skippedProfile, profile = profile, ""
	}
	files, err := loadConfigFiles(cfgFile, profile, isOffline(cmd))
	if err != nil {
		return output.Usage(err)
	}
	if err := checkCredentialMix(cmd, files); err != nil {
		return output.Usage(err)
	}
	loadedConfig = loadedConfig[:0]
	for _, f := range files {
		loadedConfig = append(loadedConfig, f.path)
	}

	viper.SetEnvPrefix(envPrefix)

	// Environment variables can't have dashes in them, so bind them to their equivalent
	// keys with underscores, e.g. --artifact-id to CPICTL_ARTIFACT_ID
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))

	// Bind to environment variables
	viper.AutomaticEnv()

	// Bind the current command's flags to viper
	bindFlags(cmd)

	// Set debug flag from command line to viper
	if !viper.IsSet("debug") {
		viper.Set("debug", config.GetBool(cmd, "debug"))
	}

	format := config.GetString(cmd, "output")
	if format != output.FormatText && format != output.FormatJSON {
		return output.Usagef("invalid value for --output = %v (allowed: text, json)", format)
	}

	logger.Init(cmd.ErrOrStderr(), format == output.FormatJSON, viper.GetBool("debug"))
	for _, f := range files {
		log.Debug().Msgf("Config file %s", f.path)
	}
	if skippedProfile != "" {
		log.Debug().Msgf("Active profile %s not used: the tenant comes from the environment or a flag", skippedProfile)
	}
	if profile != "" && !isOffline(cmd) {
		log.Info().Msgf("Profile %s (%s)", profile, cmp.Or(viper.GetString("gateway"), viper.GetString("tmn-host")))
	}
	if err := configPermissionError(files); err != nil {
		return err
	}
	if w := configPermissionWarning(files); w != "" {
		log.Warn().Msg(w)
	}
	legacy := legacySettingsHint(cfgFile)
	if legacy != "" {
		log.Warn().Msg(legacy)
	}

	if gw := config.GetString(cmd, "gateway"); gw != "" && !isOffline(cmd) {
		// gateway mode: the gateway holds the SAP key; a key here would
		// be a way around it
		hasAuth := config.GetString(cmd, "oauth-host") != "" || config.GetString(cmd, "tmn-userid") != ""
		// runtime credentials would send test messages straight to the
		// runtime, past the gateway and the user's scope there
		hasRuntimeAuth := config.GetString(cmd, "runtime-oauth-clientid") != "" || config.GetString(cmd, "runtime-userid") != ""
		switch {
		case hasAuth:
			return output.Usagef("--gateway and SAP credentials (--oauth-host, --tmn-userid) exclude each other: in gateway mode the gateway holds the key")
		case hasRuntimeAuth:
			return output.Usagef("--gateway and runtime credentials (--runtime-oauth-clientid, --runtime-userid) exclude each other: in gateway mode test messages go through the gateway")
		case os.Getenv(gatewayTokenEnv) == "" && gatewayTokenSource(cmd) == nil:
			if _, ok := loginConfig(); ok {
				return output.Usagef("gateway mode: not signed in, run cpictl auth login (or set %s)", gatewayTokenEnv)
			}
			return output.Usagef("gateway mode: set %s to your token for %s, or configure login and run cpictl auth login", gatewayTokenEnv, gw)
		}
		// http is only honoured on loopback (ParseHost): never a token in clear text
		if _, host, _, path := cpi.ParseGateway(gw); host == "" || path == "" {
			return output.Usagef("--gateway %q: the gateway's URL with the SAP tenant, e.g. https://gw.example.com/t/dev", gw)
		}
		return nil
	}
	if !isOffline(cmd) {
		hasAuth := config.GetString(cmd, "oauth-host") != "" || config.GetString(cmd, "tmn-userid") != ""
		switch {
		case config.GetString(cmd, "tmn-host") == "" && !hasAuth:
			return output.Usagef("no tenant configured: set --tmn-host and OAuth (--oauth-host, --oauth-clientid, --oauth-clientsecret) or Basic Auth (--tmn-userid, --tmn-password)%s", hintSuffix(legacy))
		case config.GetString(cmd, "tmn-host") == "":
			return output.Usagef("required flag \"tmn-host\" not set%s", hintSuffix(legacy))
		case !hasAuth:
			return output.Usagef("required flag \"tmn-userid\" (Basic Auth) or \"oauth-host\" (OAuth) not set%s", hintSuffix(legacy))
		}
	}

	return nil
}

// Bind each cobra flag to its associated viper configuration (config file and environment variable)
func bindFlags(cmd *cobra.Command) {
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		configName := f.Name
		// Apply the viper config value to the flag when the flag is not set and viper has a value
		if !f.Changed && viper.IsSet(configName) {
			val := viper.Get(configName)
			cmd.Flags().Set(configName, fmt.Sprintf("%v", val))
		}
	})
}
