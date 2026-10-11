package cmd

import (
	"cmp"
	"context"
	"fmt"
	"github.com/cpars-innovation/cpicli/internal/deploy"
	"github.com/spf13/viper"
	"slices"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/internal/str"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/snapshot"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func NewSnapshotCommand() *cobra.Command {

	snapshotCmd := &cobra.Command{
		Use:          "snapshot",
		Short:        "Save all integration packages of the tenant to a Git repository",
		SilenceUsage: true,
		Long: `Snapshot all editable integration packages from SAP Integration Suite
tenant to a Git repository.

Configuration:
  Settings can be loaded from the global config file (--config) under the
  'snapshot' section. CLI flags override config file settings.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			// Validate Draft Handling
			draftHandling := config.GetStringWithFallback(cmd, "draft-handling", "snapshot.draftHandling")
			switch draftHandling {
			case "SKIP", "ADD", "ERROR":
			default:
				return fmt.Errorf("invalid value for --draft-handling = %v", draftHandling)
			}
			// If artifacts directory is provided, validate that is it a subdirectory of Git repo
			gitRepoDir, err := config.GetStringWithEnvExpandAndFallback(cmd, "dir-git-repo", "snapshot.dirGitRepo")
			if err != nil {
				return fmt.Errorf("security alert for --dir-git-repo: %w", err)
			}

			// required here: MarkFlagRequired does not apply to persistent flags, and
			// the value may come from the config file; without it the artifacts
			// were written relative to the file system root
			if strings.TrimSpace(gitRepoDir) == "" {
				return output.Usagef("required flag \"dir-git-repo\" not set (or snapshot.dirGitRepo in the config file)")
			}
			artifactsDir, err := config.GetStringWithEnvExpandAndFallback(cmd, "dir-artifacts", "snapshot.dirArtifacts")
			if err != nil {
				return fmt.Errorf("security alert for --dir-artifacts: %w", err)
			}
			if artifactsDir != "" {
				repoAbs, err1 := filepath.Abs(gitRepoDir)
				artAbs, err2 := filepath.Abs(artifactsDir)
				if err1 != nil || err2 != nil || (artAbs != repoAbs && !strings.HasPrefix(artAbs, repoAbs+string(os.PathSeparator))) {
					return output.Usagef("--dir-artifacts [%v] should be a subdirectory of --dir-git-repo [%v]", artifactsDir, gitRepoDir)
				}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			if err = runSnapshot(cmd); err != nil {
				cmd.SilenceUsage = true
			}
			return
		},
	}

	// Define cobra flags, the default value has the lowest (least significant) precedence
	// Note: These can be set in config file under 'snapshot' key
	snapshotCmd.PersistentFlags().String("dir-git-repo", "", "Directory of the Git repository, or a folder inside one: only changes below it are committed (config: snapshot.dirGitRepo)")
	snapshotCmd.PersistentFlags().String("dir-artifacts", "", "Directory containing contents of artifacts (grouped into packages) (config: snapshot.dirArtifacts)")
	snapshotCmd.PersistentFlags().String("dir-work", "", "Directory in which a temporary folder for in-transit files is created and removed after the run (config: snapshot.dirWork) (default: the system's temporary directory)")
	snapshotCmd.PersistentFlags().Bool("keep-work-dir", false, "Keep the temporary folder in --dir-work after the run, for debugging (config: snapshot.keepWorkDir)")
	snapshotCmd.Flags().String("draft-handling", "SKIP", "Handling when artifact is in draft version. Allowed values: SKIP, ADD, ERROR (config: snapshot.draftHandling)")
	snapshotCmd.PersistentFlags().StringSlice("ids-include", nil, "List of included package IDs (config: snapshot.idsInclude)")
	snapshotCmd.PersistentFlags().StringSlice("ids-exclude", nil, "List of excluded package IDs (config: snapshot.idsExclude)")

	snapshotCmd.Flags().String("git-commit-msg", "", "Message used in commit (config: snapshot.gitCommitMsg) (default \"Tenant snapshot of <current time>\")")
	snapshotCmd.Flags().String("git-commit-user", snapshot.DefaultAuthorName, "Author name of the commit (config: snapshot.gitCommitUser)")
	snapshotCmd.Flags().String("git-commit-email", snapshot.DefaultAuthorEmail, "Author email of the commit (config: snapshot.gitCommitEmail)")
	snapshotCmd.Flags().Bool("git-skip-commit", false, "Skip committing changes to Git repository (config: snapshot.gitSkipCommit)")
	snapshotCmd.Flags().Bool("git-commit-changed-only", false, "Commit only the artifact folders, package files and state file this snapshot changed (default: every change below --dir-git-repo) (config: snapshot.gitCommitChangedOnly)")
	snapshotCmd.Flags().Bool("sync-package-details", true, "Sync details of Integration Packages (config: snapshot.syncPackageDetails)")
	snapshotCmd.Flags().Bool("incremental", false, "Skip the download of artifacts whose version, ModifiedAt, configured parameters and local copy did not change since the last snapshot (config: snapshot.incremental)")
	snapshotCmd.Flags().Int("parallel", 8, "Artifacts downloaded at the same time, across all packages (config: snapshot.parallel)")
	snapshotCmd.Flags().Bool("dry-run", false, "Only report per artifact what the snapshot would do (new, changed, deleted, unchanged, local-modified, local-only, derived, draft); writes no files and no state")
	snapshotCmd.Flags().Bool("overwrite-local", false, "Overwrite artifacts with local edits since the last snapshot (default: skip them as local-modified)")
	snapshotCmd.Flags().Bool("fail-on-local-modified", false, "Exit with code 5 when an artifact is local-modified (for CI)")
	snapshotCmd.Flags().Bool("keep-orphan-parameters", false, "Write parameters.prop keys that parameters.propdef no longer declares (values the tenant keeps after a parameter was renamed or removed); they are reported either way (config: snapshot.keepOrphanParameters)")
	snapshotCmd.Flags().Bool("prune", false, "Remove local artifact folders of artifacts and packages deleted on the tenant, and local folders of derived copies (never when edited locally)")
	snapshotCmd.Flags().String("deploy-config", "", "Deploy config file or folder of the orchestrator: its deployment copies are not written (config: snapshot.deployConfig, else orchestrator.deployConfig)")
	snapshotCmd.Flags().StringSlice("deployment-prefix", nil, "Additional deployment prefixes whose copies are not written (the deploy config's deploymentPrefix always counts)")
	snapshotCmd.Flags().Bool("include-derived", false, "Write deployment copies like any other artifact (ignore the deploy config)")
	snapshotCmd.Flags().String("state-file", "", "State of the last snapshot (default: <dir-git-repo>/.cpi/snapshot-state.json, committed with the snapshot) (config: snapshot.stateFile)")

	snapshotCmd.MarkFlagsMutuallyExclusive("ids-include", "ids-exclude")

	return snapshotCmd
}

func runSnapshot(cmd *cobra.Command) error {
	log.Info().Msg("Executing snapshot command")

	// Support reading from config file under 'snapshot' key
	gitRepoDir, err := config.GetStringWithEnvExpandAndFallback(cmd, "dir-git-repo", "snapshot.dirGitRepo")
	if err != nil {
		return fmt.Errorf("security alert for --dir-git-repo: %w", err)
	}
	artifactsBaseDir := config.GetStringWithFallback(cmd, "dir-artifacts", "snapshot.dirArtifacts")
	if artifactsBaseDir == "" {
		artifactsBaseDir = gitRepoDir
	}
	workDir, err := config.GetStringWithEnvExpandAndFallback(cmd, "dir-work", "snapshot.dirWork")
	if err != nil {
		return fmt.Errorf("security alert for --dir-work: %w", err)
	}
	commit := snapshot.CommitOptions{
		Message:     config.GetStringWithFallback(cmd, "git-commit-msg", "snapshot.gitCommitMsg"),
		AuthorName:  config.GetStringWithFallback(cmd, "git-commit-user", "snapshot.gitCommitUser"),
		AuthorEmail: config.GetStringWithFallback(cmd, "git-commit-email", "snapshot.gitCommitEmail"),
	}
	if commit.Message == "" {
		commit.Message = "Tenant snapshot of " + time.Now().Format(time.UnixDate)
	}
	skipCommit := config.GetBoolWithFallback(cmd, "git-skip-commit", "snapshot.gitSkipCommit")
	changedOnly := config.GetBoolWithFallback(cmd, "git-commit-changed-only", "snapshot.gitCommitChangedOnly")
	stateFile := config.GetStringWithFallback(cmd, "state-file", "snapshot.stateFile")
	if stateFile == "" {
		stateFile = filepath.Join(gitRepoDir, ".cpi", "snapshot-state.json")
	}

	o := snapshot.Options{
		Packages:             str.TrimSlice(config.GetStringSliceWithFallback(cmd, "ids-include", "snapshot.idsInclude")),
		Exclude:              str.TrimSlice(config.GetStringSliceWithFallback(cmd, "ids-exclude", "snapshot.idsExclude")),
		DraftHandling:        config.GetStringWithFallback(cmd, "draft-handling", "snapshot.draftHandling"),
		Incremental:          config.GetBoolWithFallback(cmd, "incremental", "snapshot.incremental"),
		DryRun:               config.GetBoolWithFallback(cmd, "dry-run", "snapshot.dryRun"),
		OverwriteLocal:       config.GetBoolWithFallback(cmd, "overwrite-local", "snapshot.overwriteLocal"),
		Prune:                config.GetBoolWithFallback(cmd, "prune", "snapshot.prune"),
		KeepOrphanParameters: config.GetBoolWithFallback(cmd, "keep-orphan-parameters", "snapshot.keepOrphanParameters"),
		SkipPackageDetails:   !config.GetBoolWithFallback(cmd, "sync-package-details", "snapshot.syncPackageDetails"),
		Parallel:             max(1, config.GetIntWithFallback(cmd, "parallel", "snapshot.parallel")),
		StatePath:            stateFile,
		WorkDir:              workDir,
		KeepWorkDir:          config.GetBoolWithFallback(cmd, "keep-work-dir", "snapshot.keepWorkDir"),
	}
	if o.Derived, err = derivedIndex(cmd, artifactsBaseDir); err != nil {
		return err
	}
	failOnLocal := config.GetBoolWithFallback(cmd, "fail-on-local-modified", "snapshot.failOnLocalModified")

	serviceDetails := serviceDetails(cmd)
	o.Tenant = cpi.TenantID(serviceDetails.Host)
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	// many parallel reads: the executer retries when the tenant throttles
	// (429) or a gateway fails
	res, snapErr := snapshot.Take(ctx, cpi.InitHTTPExecuter(serviceDetails), artifactsBaseDir, o)
	if res != nil {
		output.SetResult(cmd.Context(), newSnapshotResult(res))
	}
	if snapErr != nil && (res == nil || res.Succeeded == 0) {
		return snapErr
	}
	localErr := error(nil)
	if failOnLocal && res.Counts[snapshot.StatusLocalModified] > 0 {
		localErr = output.Failed(fmt.Errorf("%d artifact(s) local-modified", res.Counts[snapshot.StatusLocalModified]))
	}
	if o.DryRun {
		if snapErr != nil {
			return snapErr
		}
		return localErr
	}
	// the state is saved (what succeeded is kept, also when some packages failed)
	if !skipCommit {
		if changedOnly {
			commit.Paths = commitPaths(gitRepoDir, artifactsBaseDir, stateFile, res.Paths)
		}
		if _, err := snapshot.Commit(gitRepoDir, commit); err != nil {
			return err
		}
	}
	if snapErr != nil {
		return snapErr
	}
	return localErr
}

// commitPaths are the paths a snapshot changed (relative to the artifacts
// folder) and the state file when it is inside the repository folder.
func commitPaths(gitRepoDir, artifactsBaseDir, stateFile string, changed []string) []string {
	paths := []string{}
	for _, p := range changed {
		paths = append(paths, filepath.Join(artifactsBaseDir, filepath.FromSlash(p)))
	}
	repoAbs, err1 := filepath.Abs(gitRepoDir)
	stateAbs, err2 := filepath.Abs(stateFile)
	if err1 == nil && err2 == nil && strings.HasPrefix(stateAbs, repoAbs+string(os.PathSeparator)) {
		paths = append(paths, stateAbs)
	}
	return paths
}

// snapshotResult is the JSON result of snapshot.
type snapshotResult struct {
	Packages   int      `json:"packages"`
	Succeeded  int      `json:"succeeded"`
	Downloaded int64    `json:"artifactsDownloaded"`
	Skipped    int64    `json:"artifactsSkipped"`
	Seconds    float64  `json:"seconds"`
	Failed     []string `json:"failed,omitempty"`
	DryRun     bool     `json:"dryRun,omitempty"`
	// Counts per status, Artifacts per artifact (sorted), Warnings: derived
	// copies edited on the tenant and similar.
	Counts    map[string]int  `json:"counts"`
	Warnings  int             `json:"warnings"`
	Artifacts []snapshot.Item `json:"artifacts"`
}

func newSnapshotResult(r *snapshot.Result) *snapshotResult {
	return &snapshotResult{Packages: r.Packages, Succeeded: r.Succeeded, Downloaded: r.Downloaded, Skipped: r.Skipped, Seconds: r.Seconds,
		Failed: r.Failed, DryRun: r.DryRun, Counts: r.Counts, Warnings: r.Warnings, Artifacts: r.Items}
}

// derivedIndex reads the deploy config (--deploy-config, snapshot.deployConfig
// or orchestrator.deployConfig): the deployment copies it creates on the
// tenant, and the configOverrides keys of the artifacts deployed as they are.
func derivedIndex(cmd *cobra.Command, base string) (*snapshot.DerivedIndex, error) {
	path := config.GetStringWithFallback(cmd, "deploy-config", "snapshot.deployConfig")
	if path == "" {
		path = viper.GetString("orchestrator.deployConfig")
	}
	if path == "" {
		return nil, nil
	}
	loader := deploy.NewConfigLoader()
	if err := loader.DetectSource(path); err != nil {
		return nil, output.Usagef("deploy config %s: %v", path, err)
	}
	files, err := loader.LoadConfigs()
	if err != nil {
		return nil, output.Usagef("deploy config %s: %v", path, err)
	}
	extra := str.TrimSlice(config.GetStringSliceWithFallback(cmd, "deployment-prefix", "snapshot.deploymentPrefix"))
	includeDerived := config.GetBoolWithFallback(cmd, "include-derived", "snapshot.includeDerived")
	idx := &snapshot.DerivedIndex{Artifacts: map[string]snapshot.DerivedSource{}, Packages: map[string]string{}, Overrides: map[string]map[string]bool{}}
	sources := map[string]bool{}
	for _, f := range files {
		prefixes := slices.Clone(extra)
		if p := f.Config.DeploymentPrefix; p != "" && !slices.Contains(prefixes, p) {
			prefixes = append(prefixes, p)
		}
		for _, pkg := range f.Config.Packages {
			pkgDir := cmp.Or(pkg.PackageDir, pkg.ID)
			for _, p := range prefixes {
				idx.Packages[p+pkg.ID] = pkg.ID
			}
			for _, a := range pkg.Artifacts {
				artDir := filepath.ToSlash(filepath.Clean(cmp.Or(a.ArtifactDir, a.Id)))
				src := snapshot.DerivedSource{Label: pkgDir + "/" + artDir, Dir: filepath.Join(base, filepath.FromSlash(pkgDir), filepath.FromSlash(artDir))}
				if artDir != a.Id {
					idx.Artifacts[a.Id] = snapshot.DerivedSource{Label: src.Label, Dir: src.Dir, Reason: "artifactDir"}
				} else if f.Config.DeploymentPrefix == "" {
					sources[a.Id] = true
					if len(a.ConfigOverrides) > 0 {
						if idx.Overrides[a.Id] == nil {
							idx.Overrides[a.Id] = map[string]bool{}
						}
						for k := range a.ConfigOverrides {
							idx.Overrides[a.Id][k] = true
						}
					}
				}
				for _, p := range prefixes {
					idx.Artifacts[p+"_"+a.Id] = snapshot.DerivedSource{Label: src.Label, Dir: src.Dir, Reason: "deploymentPrefix " + p}
				}
			}
		}
	}
	// an ID that is deployed from its own folder is no copy
	for id := range sources {
		delete(idx.Artifacts, id)
	}
	if includeDerived {
		idx.Artifacts, idx.Packages = nil, nil
	}
	log.Info().Msgf("Deploy config %s: %d deployment copies and %d prefixed package(s) are not written%s",
		path, len(idx.Artifacts), len(idx.Packages), map[bool]string{true: " (--include-derived: all are written)", false: ""}[includeDerived])
	return idx, nil
}
