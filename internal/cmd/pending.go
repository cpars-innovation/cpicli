package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/versioning"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// defaultPendingFile collects the deployments of --defer-deploy runs.
var defaultPendingFile = filepath.Join(".cpi", "pending-deploy.json")

// pendingDeploy is the list of deployments that orchestrator and configure
// (--defer-deploy) leave for one `deploy --pending` at the end of a pipeline:
// every artifact is deployed once, with force when any step needs it.
type pendingDeploy struct {
	Format    int                         `json:"format"`
	Tenant    string                      `json:"tenant"`
	Artifacts map[string]*pendingArtifact `json:"artifacts"`
}

type pendingArtifact struct {
	Type    string `json:"type"`
	Package string `json:"package"`
	// Force deploys even when the runtime has the designtime version
	// (configuration or same-version content changes).
	Force           bool       `json:"force,omitempty"`
	Reasons         []string   `json:"reasons"`
	Versioning      string     `json:"versioning,omitempty"`
	ExpectedVersion string     `json:"expectedVersion,omitempty"`
	AllowDowngrade  bool       `json:"allowDowngrade,omitempty"`
	ModifiedAt      *time.Time `json:"modifiedAt,omitempty"`
	// Seq keeps the order in which artifacts were added (packages are
	// deployed in that order).
	Seq int `json:"seq"`
}

func addDeferFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("defer-deploy", false, "Do not deploy: add the deployments to the pending file for one 'cpictl deploy --pending' at the end")
	cmd.Flags().String("pending-file", "", "Pending deployments file (default: .cpi/pending-deploy.json)")
}

func pendingFile(cmd *cobra.Command) string {
	if p, _ := cmd.Flags().GetString("pending-file"); p != "" {
		return p
	}
	return defaultPendingFile
}

func loadPending(path string) (*pendingDeploy, error) {
	p := &pendingDeploy{Format: 1, Artifacts: map[string]*pendingArtifact{}}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, p); err != nil {
		return nil, output.Usagef("cannot read the pending deployments %s: %v", path, err)
	}
	if p.Artifacts == nil {
		p.Artifacts = map[string]*pendingArtifact{}
	}
	return p, nil
}

func (p *pendingDeploy) save(path string) error {
	if len(p.Artifacts) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// deferDeployments merges tasks into the pending file of the tenant.
// reason(t) explains each task.
func deferDeployments(path, host string, tasks []DeploymentTask, reason func(DeploymentTask) string) error {
	p, err := loadPending(path)
	if err != nil {
		return err
	}
	tenant := cpi.TenantID(host)
	if p.Tenant != "" && p.Tenant != tenant && len(p.Artifacts) > 0 {
		return output.Usagef("%s holds pending deployments for tenant %s, not %s: deploy or delete them first", path, p.Tenant, tenant)
	}
	p.Tenant = tenant
	next := 0
	for _, a := range p.Artifacts {
		next = max(next, a.Seq+1)
	}
	for _, t := range tasks {
		a, ok := p.Artifacts[t.ArtifactID]
		if !ok {
			a = &pendingArtifact{Type: t.ArtifactType, Package: t.PackageID, Seq: next}
			next++
			p.Artifacts[t.ArtifactID] = a
		}
		a.Force = a.Force || t.Force
		a.AllowDowngrade = a.AllowDowngrade || t.AllowDowngrade
		if a.Versioning == "" {
			a.Versioning = string(t.Versioning)
		}
		if a.ExpectedVersion == "" {
			a.ExpectedVersion = t.ExpectedVersion
		}
		if a.ModifiedAt == nil {
			a.ModifiedAt = t.ModifiedAt
		}
		if r := reason(t); !slices.Contains(a.Reasons, r) {
			a.Reasons = append(a.Reasons, r)
		}
	}
	if err := p.save(path); err != nil {
		return err
	}
	log.Info().Msgf("📋 %d deployment(s) deferred to %s (%d pending): run 'cpictl deploy --pending'", len(tasks), path, len(p.Artifacts))
	return nil
}

// tasks returns the pending deployments in the order they were added.
func (p *pendingDeploy) tasks() []DeploymentTask {
	ids := make([]string, 0, len(p.Artifacts))
	for id := range p.Artifacts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return p.Artifacts[ids[i]].Seq < p.Artifacts[ids[j]].Seq })
	out := make([]DeploymentTask, 0, len(ids))
	for _, id := range ids {
		a := p.Artifacts[id]
		out = append(out, DeploymentTask{ArtifactID: id, ArtifactType: a.Type, PackageID: a.Package, Force: a.Force,
			AllowDowngrade: a.AllowDowngrade, ModifiedAt: a.ModifiedAt, Versioning: versioning.Mode(a.Versioning), ExpectedVersion: a.ExpectedVersion})
	}
	return out
}
