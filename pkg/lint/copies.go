package lint

import (
	"cmp"
	"path/filepath"
	"slices"

	"github.com/cpars-innovation/cpicli/pkg/models"
)

// DeploymentCopiesOf maps each artifact folder of a deploy config (key
// "<packageDir>/<artifactDir>") to the IDs it is deployed as, for
// Options.DeploymentCopies; the deployment-copies rule reports folders with
// several. Configs and merged packages with a deploymentPrefix are
// environment copies, not partner copies, and are left out.
func DeploymentCopiesOf(cfg *models.DeployConfig) map[string][]DeploymentCopy {
	out := map[string][]DeploymentCopy{}
	if cfg == nil || cfg.DeploymentPrefix != "" {
		return out
	}
	for _, pkg := range cfg.Packages {
		if pkg.MergedPrefix != "" {
			continue
		}
		for _, a := range pkg.Artifacts {
			key := filepath.ToSlash(filepath.Join(cmp.Or(pkg.PackageDir, pkg.ID), cmp.Or(a.ArtifactDir, a.Id)))
			c := DeploymentCopy{ID: a.Id}
			for k := range a.ConfigOverrides {
				c.Overrides = append(c.Overrides, k)
			}
			slices.Sort(c.Overrides)
			out[key] = append(out[key], c)
		}
	}
	return out
}
