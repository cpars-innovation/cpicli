package ops

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// MatrixTier is one tenant of a version matrix.
type MatrixTier struct {
	Name string
	Exe  *httpclnt.HTTPExecuter
}

// MatrixCell is an artifact on one tier.
type MatrixCell struct {
	Designtime string     `json:"designtime,omitempty"`
	Draft      bool       `json:"draft,omitempty"`
	Running    string     `json:"running,omitempty"`
	Status     string     `json:"status,omitempty"`
	ModifiedAt *time.Time `json:"modifiedAt,omitempty"`
	ModifiedBy string     `json:"modifiedBy,omitempty"`
	DeployedBy string     `json:"deployedBy,omitempty"`
	DeployedOn *time.Time `json:"deployedOn,omitempty"`
}

// MatrixTierCell is a MatrixCell with its tier, in tier order.
type MatrixTierCell struct {
	Tier    string `json:"tier"`
	Present bool   `json:"present"`
	MatrixCell
}

// MatrixRow is one artifact across Git and the tiers.
type MatrixRow struct {
	Package  string `json:"package"`
	Artifact string `json:"artifact"`
	Type     string `json:"type,omitempty"`
	// Git is the Bundle-Version in the content tree (empty: not in it).
	Git   string                `json:"git,omitempty"`
	Tiers map[string]MatrixCell `json:"tiers"`
	// Cells are the tiers in promotion order (VersionMatrix.Tiers), also
	// those without the artifact (Present false).
	Cells []MatrixTierCell `json:"cells"`
	// Differs is true when the designtime versions (and Git) are not all
	// equal, Behind lists the tiers whose designtime version is lower than
	// the tier (or Git) before them, in tier order: the next promotion.
	Differs bool     `json:"differs"`
	Behind  []string `json:"behind,omitempty"`
}

// VersionMatrix is the result of BuildVersionMatrix.
type VersionMatrix struct {
	Tiers []string    `json:"tiers"`
	Rows  []MatrixRow `json:"rows"`
	// Errors of tiers that could not be read completely.
	Errors []string `json:"errors,omitempty"`
}

// MatrixOptions select artifacts.
type MatrixOptions struct {
	// Dir is the content tree with the Git versions (optional).
	Dir                 string
	Packages, Artifacts []string
}

// BuildVersionMatrix reads, per tier and in one pass per tenant, the
// designtime version, draft flag and last change of every selected artifact
// and the runtime version and status, next to the Bundle-Version of the
// content tree. Tiers are in promotion order.
func BuildVersionMatrix(ctx context.Context, tiers []MatrixTier, o MatrixOptions) (*VersionMatrix, error) {
	m := &VersionMatrix{Tiers: []string{}, Rows: []MatrixRow{}}
	rows := map[string]*MatrixRow{}
	row := func(pkg, id, typ string) *MatrixRow {
		r := rows[id]
		if r == nil {
			r = &MatrixRow{Package: pkg, Artifact: id, Type: typ, Tiers: map[string]MatrixCell{}}
			rows[id] = r
		}
		r.Package, r.Type = cmpOr(r.Package, pkg), cmpOr(r.Type, typ)
		return r
	}
	if o.Dir != "" {
		err := WalkLocalArtifacts(ctx, o.Dir, func(la LocalArtifact) {
			if la.PackageID == "" || !MatchAny(o.Packages, la.PackageID) || !MatchAny(o.Artifacts, la.ID) {
				return
			}
			v, _ := manifestVersionOf(la.Dir)
			row(la.PackageID, la.ID, la.Type).Git = v
		}, func(error) {})
		if err != nil {
			return nil, err
		}
	}
	type tierData struct {
		arts    map[string]*cpi.ArtifactDetails
		pkgOf   map[string]string
		runtime map[string]cpi.RuntimeArtifact
		err     error
	}
	data := make([]tierData, len(tiers))
	var wg sync.WaitGroup
	for i, t := range tiers {
		m.Tiers = append(m.Tiers, t.Name)
		wg.Add(1)
		go func() {
			defer wg.Done()
			d := tierData{arts: map[string]*cpi.ArtifactDetails{}, pkgOf: map[string]string{}, runtime: map[string]cpi.RuntimeArtifact{}}
			d.err = func() error {
				ip := cpi.NewIntegrationPackage(t.Exe)
				packages, err := ip.GetPackagesList()
				if err != nil {
					return err
				}
				for _, p := range packages {
					if !MatchAny(o.Packages, p) {
						continue
					}
					arts, err := ip.GetAllArtifacts(p)
					if err != nil {
						return err
					}
					for _, a := range arts {
						if MatchAny(o.Artifacts, a.Id) {
							d.arts[a.Id], d.pkgOf[a.Id] = a, p
						}
					}
				}
				running, err := cpi.NewContent(t.Exe).RuntimeArtifacts(nil)
				if err != nil {
					return err
				}
				for _, r := range running {
					d.runtime[r.Id] = r
				}
				return nil
			}()
			data[i] = d
		}()
	}
	wg.Wait()
	var errs []error
	for i, t := range tiers {
		d := data[i]
		if d.err != nil {
			m.Errors = append(m.Errors, t.Name+": "+d.err.Error())
			errs = append(errs, d.err)
			continue
		}
		for id, a := range d.arts {
			c := MatrixCell{Designtime: a.Version, Draft: a.IsDraft, ModifiedBy: a.ModifiedBy}
			if !a.ModifiedAt.IsZero() {
				at := a.ModifiedAt.UTC()
				c.ModifiedAt = &at
			}
			if r, ok := d.runtime[id]; ok {
				c.Running, c.Status, c.DeployedBy = r.Version, r.Status, r.DeployedBy
				if !r.DeployedOn.IsZero() {
					on := r.DeployedOn.UTC()
					c.DeployedOn = &on
				}
			}
			row(d.pkgOf[id], id, a.ArtifactType).Tiers[t.Name] = c
		}
	}
	if len(errs) == len(tiers) && len(tiers) > 0 {
		return nil, errors.Join(errs...)
	}
	for _, r := range rows {
		prev := r.Git
		versions := map[string]bool{}
		if r.Git != "" {
			versions[r.Git] = true
		}
		r.Cells = make([]MatrixTierCell, 0, len(m.Tiers))
		for _, t := range m.Tiers {
			c, ok := r.Tiers[t]
			r.Cells = append(r.Cells, MatrixTierCell{Tier: t, Present: ok, MatrixCell: c})
			v := c.Designtime
			if c.Draft {
				v = "draft"
			}
			versions[v] = true
			if ok && prev != "" && !c.Draft && CompareVersions(c.Designtime, prev) < 0 {
				r.Behind = append(r.Behind, t)
			} else if !ok && prev != "" {
				r.Behind = append(r.Behind, t) // not on this tier yet
			}
			if ok && !c.Draft {
				prev = c.Designtime
			}
		}
		r.Differs = len(versions) > 1
		m.Rows = append(m.Rows, *r)
	}
	sort.Slice(m.Rows, func(i, j int) bool {
		if m.Rows[i].Package != m.Rows[j].Package {
			return m.Rows[i].Package < m.Rows[j].Package
		}
		return m.Rows[i].Artifact < m.Rows[j].Artifact
	})
	return m, nil
}
