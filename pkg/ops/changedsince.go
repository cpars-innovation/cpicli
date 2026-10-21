package ops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// ChangedSinceOptions selects what DesigntimeChangedSince reads.
type ChangedSinceOptions struct {
	// Since is the time to compare with: artifacts changed after it are
	// listed.
	Since time.Time
	// Fingerprint is the Fingerprint of an earlier call. A different one
	// means something changed, also a deletion, which ModifiedAt cannot
	// show.
	Fingerprint string
	// Packages limits the packages (glob patterns); empty: all.
	Packages []string
	// Parallel is the number of packages listed at a time (default 8).
	Parallel int
}

// ChangedArtifact is a designtime artifact changed after Since.
type ChangedArtifact struct {
	Package    string    `json:"package"`
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Version    string    `json:"version"`
	ModifiedAt time.Time `json:"modifiedAt"`
	ModifiedBy string    `json:"modifiedBy,omitempty"`
}

// DesigntimeChanges answers whether designtime content changed since a time.
type DesigntimeChanges struct {
	Since time.Time `json:"since"`
	// Changed: an artifact changed after Since, or the fingerprint differs
	// from the one given.
	Changed bool `json:"changed"`
	// Latest is the newest ModifiedAt of all artifacts.
	Latest *time.Time `json:"latest,omitempty"`
	// Fingerprint is a hash of every package and artifact with its version
	// and change time. Keep it for the next call: it also changes when an
	// artifact or package is deleted.
	Fingerprint      string            `json:"fingerprint"`
	Packages         int               `json:"packages"`
	Artifacts        int               `json:"artifacts"`
	ChangedArtifacts []ChangedArtifact `json:"changedArtifacts"`
	Warnings         []string          `json:"warnings,omitempty"`
}

// DesigntimeChangedSince tells whether designtime content changed since a
// time, and what. SAP has no cheap call for it: listing artifacts without a
// package and $expand on packages are not implemented, a package's
// ModifiedDate does not change with its artifacts, and AuditLogs needs a
// role an API key usually lacks. So it lists the packages, then the
// artifacts of the packages in parallel (four small calls per package).
// A package whose artifacts cannot be listed is a warning, and the
// fingerprint is then left empty: it would not be comparable.
func DesigntimeChangedSince(ctx context.Context, exe *httpclnt.HTTPExecuter, o ChangedSinceOptions) (*DesigntimeChanges, error) {
	ip := cpi.NewIntegrationPackage(exe.WithContext(ctx))
	list, err := ip.GetPackagesData()
	if err != nil {
		return nil, fmt.Errorf("packages could not be listed: %w", err)
	}
	var pkgs []string
	for _, p := range list {
		if matchesAny(o.Packages, p.Root.Id) {
			pkgs = append(pkgs, p.Root.Id)
		}
	}
	sort.Strings(pkgs)
	arts := make([][]*cpi.ArtifactDetails, len(pkgs))
	errs := make([]error, len(pkgs))
	parallel := o.Parallel
	if parallel <= 0 {
		parallel = 8
	}
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for i, id := range pkgs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				errs[i] = ctx.Err()
				return
			}
			defer func() { <-sem }()
			arts[i], errs[i] = ip.GetAllArtifacts(id)
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	res := &DesigntimeChanges{Since: o.Since, Packages: len(pkgs), ChangedArtifacts: []ChangedArtifact{}}
	h := sha256.New()
	complete := true
	for i, pkg := range pkgs {
		if errs[i] != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("artifacts of package %s could not be listed: %v", pkg, errs[i]))
			complete = false
			continue
		}
		fmt.Fprintf(h, "P %s\n", pkg)
		sort.Slice(arts[i], func(a, b int) bool {
			return arts[i][a].ArtifactType+"/"+arts[i][a].Id < arts[i][b].ArtifactType+"/"+arts[i][b].Id
		})
		for _, a := range arts[i] {
			res.Artifacts++
			fmt.Fprintf(h, "A %s %s %s %d\n", a.ArtifactType, a.Id, a.Version, a.ModifiedAt.UnixMilli())
			if !a.ModifiedAt.IsZero() && (res.Latest == nil || a.ModifiedAt.After(*res.Latest)) {
				t := a.ModifiedAt
				res.Latest = &t
			}
			if a.ModifiedAt.After(o.Since) {
				res.ChangedArtifacts = append(res.ChangedArtifacts, ChangedArtifact{Package: pkg, ID: a.Id, Type: a.ArtifactType,
					Version: a.Version, ModifiedAt: a.ModifiedAt, ModifiedBy: a.ModifiedBy})
			}
		}
	}
	if complete {
		res.Fingerprint = hex.EncodeToString(h.Sum(nil))[:16]
	}
	sort.Slice(res.ChangedArtifacts, func(a, b int) bool {
		return res.ChangedArtifacts[a].ModifiedAt.After(res.ChangedArtifacts[b].ModifiedAt)
	})
	res.Changed = len(res.ChangedArtifacts) > 0 || (o.Fingerprint != "" && res.Fingerprint != o.Fingerprint)
	return res, nil
}
