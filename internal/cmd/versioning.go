package cmd

import (
	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/versioning"
	"github.com/spf13/cobra"
)

const versioningFlagHelp = "Versions: manifest (Bundle-Version of the repository, downgrade guard on), keep (the tenant's versions, guard off) or tenant-bump (max(designtime, runtime)+1); env CPICTL_VERSIONING, set it per pipeline/branch (docs/versioning.md)"

func addVersioningFlag(c *cobra.Command) {
	c.Flags().String("versioning", "", versioningFlagHelp)
}

// versioningMode reads --versioning (or CPICTL_VERSIONING).
func versioningMode(cmd *cobra.Command) (versioning.Mode, error) {
	m, err := versioning.Parse(config.GetString(cmd, "versioning"))
	if err != nil {
		return versioning.Unset, output.Usage(err)
	}
	return m, nil
}

// resolveVersioning validates and resolves the versioning of an artifact
// and its package in a deployment or configure file.
func resolveVersioning(artifact, pkg string, global versioning.Mode) (versioning.Mode, error) {
	for _, v := range []string{artifact, pkg} {
		if _, err := versioning.Parse(v); err != nil {
			return versioning.Unset, output.Usage(err)
		}
	}
	return versioning.Resolve(artifact, pkg, global), nil
}
