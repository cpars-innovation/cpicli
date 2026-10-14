package lint

import (
	"cmp"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/models"
	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// Rules that compare the flows with the repository's configure and deploy
// files (offline): what the pipeline would refuse or silently leave at the
// design-time value.

// ConfigureFile is a configure YAML file (cpictl configure) lint compares
// the flows' parameters with.
type ConfigureFile struct {
	Name   string
	Config *models.ConfigureConfig
}

// DeployEntriesOf returns the artifact folders a deploy config lists
// ("<packageDir>/<artifactDir>", as the deployment-copies rule keys them).
func DeployEntriesOf(cfg *models.DeployConfig) map[string]bool {
	out := map[string]bool{}
	if cfg == nil {
		return out
	}
	for _, pkg := range cfg.Packages {
		for _, a := range pkg.Artifacts {
			out[filepath.ToSlash(filepath.Join(cmp.Or(pkg.PackageDir, pkg.ID), cmp.Or(a.ArtifactDir, a.Id)))] = true
		}
	}
	return out
}

// ruleFlowWithoutDeployment: a flow in a package folder the deploy config
// covers, without an entry of its own, is never uploaded or deployed by
// the orchestrator.
func ruleFlowWithoutDeployment(lc *lintContext, a *lintArtifact) []Finding {
	entries := lc.opts.DeployEntries
	if entries == nil || a.Type != "Integration" || entries[a.Rel] {
		return nil
	}
	pkgDir, _, ok := strings.Cut(a.Rel, "/")
	if !ok || !slices.ContainsFunc(sortedKeys(entries), func(k string) bool { return strings.HasPrefix(k, pkgDir+"/") }) {
		return nil
	}
	return []Finding{a.finding("flow-without-deployment", "", "", "", a.Rel,
		fmt.Sprintf("the deploy config lists other artifacts of package folder %s, but not %s: the orchestrator never uploads or deploys it", pkgDir, path.Base(a.Rel)),
		"add an artifacts entry (artifactId, artifactDir, type) to the deploy config, or remove the folder if the flow is retired")}
}

// configureEntries returns the configure entries of an artifact per file,
// and whether a file covers its package at all.
func configureEntries(lc *lintContext, a *lintArtifact) (entries map[string]*models.ConfigureArtifact, covered []string) {
	entries = map[string]*models.ConfigureArtifact{}
	for _, f := range lc.opts.Configure {
		for _, p := range f.Config.Packages {
			if p.ID != a.PackageID {
				continue
			}
			covered = append(covered, f.Name)
			for i := range p.Artifacts {
				if p.Artifacts[i].ID == a.ID {
					entries[f.Name] = &p.Artifacts[i]
				}
			}
		}
	}
	return entries, covered
}

// ruleConfigureMissingParameter: a configure file covers the flow's package
// but sets no value for one of its parameters: that tier runs with the
// design-time value (usually dev's).
func ruleConfigureMissingParameter(lc *lintContext, a *lintArtifact) []Finding {
	if len(lc.opts.Configure) == 0 || a.Type != "Integration" || len(a.Params) == 0 {
		return nil
	}
	entries, covered := configureEntries(lc, a)
	var out []Finding
	for _, file := range covered {
		set := map[string]bool{}
		if e := entries[file]; e != nil {
			for _, p := range e.Parameters {
				set[p.Key] = true
			}
		}
		var missing []string
		for _, k := range sortedKeys(a.Params) {
			if !set[k] {
				missing = append(missing, k)
			}
		}
		if len(missing) == 0 {
			continue
		}
		msg := fmt.Sprintf("%s sets no value for the %s %s", file, plural(len(missing), "parameter", "parameters"), listIDs(missing, 8))
		if entries[file] == nil {
			msg = fmt.Sprintf("%s covers package %s but has no entry for this flow (%d %s)", file, a.PackageID, len(missing), plural(len(missing), "parameter", "parameters"))
		}
		out = append(out, a.finding("configure-missing-parameter", "src/main/resources/parameters.prop", "", "", file, msg,
			"add the keys with this tier's values to "+file+" (the design-time value otherwise stays, usually dev's)"))
	}
	return out
}

// ruleConfigureUnknownParameter: a configure file sets a key the flow does
// not have: configure fails on the tenant.
func ruleConfigureUnknownParameter(lc *lintContext, a *lintArtifact) []Finding {
	if len(lc.opts.Configure) == 0 || a.Type != "Integration" {
		return nil
	}
	entries, _ := configureEntries(lc, a)
	var out []Finding
	for _, file := range sortedKeys(entries) {
		var unknown []string
		for _, p := range entries[file].Parameters {
			if _, ok := a.Params[p.Key]; !ok {
				unknown = append(unknown, p.Key)
			}
		}
		if len(unknown) == 0 {
			continue
		}
		out = append(out, a.finding("configure-unknown-parameter", "src/main/resources/parameters.prop", "", "", file,
			fmt.Sprintf("%s sets %s the flow does not have: %s", file, plural(len(unknown), "a parameter", "parameters"), listIDs(unknown, 8)),
			"remove the keys from "+file+", or externalise them in the flow"))
	}
	return out
}

// LoadRepositoryFiles fills o.Configure and o.DeployEntries from a
// configure file or folder and a deploy config (empty: the paths of
// o.Config.Repository; still empty: not checked).
func LoadRepositoryFiles(o *Options, configurePath, deployPath string) error {
	if configurePath == "" && o.Config != nil {
		configurePath = o.Config.path(o.Config.Repository.Configure)
	}
	if deployPath == "" && o.Config != nil {
		deployPath = o.Config.path(o.Config.Repository.DeployConfig)
	}
	if configurePath != "" {
		files, err := ops.LoadConfigureFiles(configurePath)
		if err != nil {
			return output.Usagef("configure files %s: %v", configurePath, err)
		}
		for _, f := range files {
			o.Configure = append(o.Configure, ConfigureFile{Name: f.FileName, Config: f.Config})
		}
	}
	if deployPath != "" {
		cfg, err := ops.LoadDeployConfig(deployPath)
		if err != nil {
			return output.Usagef("%v", err)
		}
		o.DeployEntries = DeployEntriesOf(cfg)
	}
	return nil
}
