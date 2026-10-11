package cmd

import (
	"fmt"
	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/file"
	"github.com/cpars-innovation/cpicli/internal/str"
	"github.com/cpars-innovation/cpicli/internal/sync"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/versioning"
	"github.com/go-errors/errors"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"os"
	"path/filepath"
	"strings"
)

func NewRestoreCommand() *cobra.Command {

	restoreCmd := &cobra.Command{
		Use:          "restore",
		Short:        "Create or update integration packages on the tenant from a Git repository",
		SilenceUsage: true,
		Long: `Restore all editable integration packages from a Git repository to SAP Integration Suite tenant.

Configuration:
  Settings can be loaded from the global config file (--config) under the
  'restore' section. CLI flags override config file settings.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {

			// If artifacts directory is provided, validate that is it a subdirectory of Git repo
			gitRepoDir, err := config.GetStringWithEnvExpandAndFallback(cmd, "dir-git-repo", "restore.dirGitRepo")
			if err != nil {
				return fmt.Errorf("security alert for --dir-git-repo: %w", err)
			}

			if gitRepoDir != "" {
				artifactsDir, err := config.GetStringWithEnvExpandAndFallback(cmd, "dir-artifacts", "restore.dirArtifacts")
				if err != nil {
					return fmt.Errorf("security alert for --dir-artifacts: %w", err)
				}
				gitRepoDirClean := filepath.Clean(gitRepoDir) + string(os.PathSeparator)
				if artifactsDir != "" && !strings.HasPrefix(artifactsDir, gitRepoDirClean) {
					return fmt.Errorf("--dir-artifacts [%v] should be a subdirectory of --dir-git-repo [%v]", artifactsDir, gitRepoDirClean)
				}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			if err = runRestore(cmd); err != nil {
				cmd.SilenceUsage = true
			}
			return
		},
	}
	addVersioningFlag(restoreCmd)

	return restoreCmd
}

func runRestore(cmd *cobra.Command) error {
	log.Info().Msg("Executing snapshot restore command")

	// Support reading from config file under 'restore' key
	gitRepoDir, err := config.GetStringWithEnvExpandAndFallback(cmd, "dir-git-repo", "restore.dirGitRepo")
	if err != nil {
		return fmt.Errorf("security alert for --dir-git-repo: %w", err)
	}
	artifactsBaseDir := config.GetStringWithFallback(cmd, "dir-artifacts", "restore.dirArtifacts")
	if artifactsBaseDir == "" {
		artifactsBaseDir = gitRepoDir
	}
	workDir, err := config.GetStringWithEnvExpandAndFallback(cmd, "dir-work", "restore.dirWork")
	if err != nil {
		return fmt.Errorf("security alert for --dir-work: %w", err)
	}
	includedIds := str.TrimSlice(config.GetStringSliceWithFallback(cmd, "ids-include", "restore.idsInclude"))
	excludedIds := str.TrimSlice(config.GetStringSliceWithFallback(cmd, "ids-exclude", "restore.idsExclude"))

	serviceDetails := serviceDetails(cmd)
	mode, err := versioningMode(cmd)
	if err != nil {
		return err
	}
	err = restoreSnapshot(serviceDetails, artifactsBaseDir, workDir, includedIds, excludedIds, mode)
	if err != nil {
		return err
	}

	return nil
}

func restoreSnapshot(serviceDetails *cpi.ServiceDetails, artifactsBaseDir string, workDir string, includedIds []string, excludedIds []string, mode versioning.Mode) error {
	log.Info().Msg("---------------------------------------------------------------------------------")
	log.Info().Msg("📢 Begin restoring snapshot to the tenant")

	// Get directory list
	baseSourceDir := filepath.Clean(artifactsBaseDir)
	entries, err := os.ReadDir(baseSourceDir)
	if err != nil {
		return errors.Wrap(err, 0)
	}

	// Initialise HTTP executer
	exe := cpi.InitHTTPExecuter(serviceDetails)
	packageSynchroniser := sync.NewSyncer("tenant", "CPIPackage", exe)
	artifactsSynchroniser := sync.New(exe)
	artifactsSynchroniser.Versioning = mode

	// Go through each directory and check if there is an integration package details in it, if yes, then proceed to restore integration package and artifacts
	for _, entry := range entries {
		packageId := entry.Name()
		packageDir := fmt.Sprintf("%v/%v", baseSourceDir, packageId)
		packageFile := fmt.Sprintf("%v/%v.json", packageDir, packageId)
		if entry.IsDir() {
			log.Info().Msg("---------------------------------------------------------------------------------")
			log.Info().Msgf("Processing directory %v", packageDir)
			if file.Exists(packageFile) {
				// Filter in/out packages
				if str.FilterIDs(packageId, includedIds, excludedIds) {
					continue
				}

				// 1 - Sync CPI Integration Package
				err = packageSynchroniser.Exec(sync.Request{ArtifactsDir: packageDir})
				if err != nil {
					return err
				}

				// 2 - Sync CPI Artifacts
				err = artifactsSynchroniser.ArtifactsToTenant(packageId, workDir, packageDir, nil, nil)
				if err != nil {
					return err
				}
			} else {
				log.Warn().Msgf("Skipping directory as integration package file %v is not found", packageFile)
			}
		}
	}

	log.Info().Msg("---------------------------------------------------------------------------------")
	log.Info().Msg("🏆 Completed restoring snapshot to the tenant")
	return nil
}
