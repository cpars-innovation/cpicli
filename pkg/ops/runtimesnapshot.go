package ops

import (
	"sync"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/cpi"
)

// runtimeLister is implemented by tenants that list every deployed artifact
// in one call (the CPI API does: IntegrationRuntimeArtifacts).
type runtimeLister interface {
	RuntimeArtifacts() ([]cpi.RuntimeArtifact, error)
}

// RuntimeArtifacts lists every artifact on the Cloud Integration runtime.
func (t *apiTenant) RuntimeArtifacts() ([]cpi.RuntimeArtifact, error) {
	return cpi.NewContent(t.exe).RuntimeArtifacts(nil)
}

// runtimeSnapshot answers runtime status lookups of many artifacts from one
// list of the runtime, fetched at most once per maxAge: watching 200
// deployments costs one request per interval instead of 200. Tenants that
// cannot list are asked per artifact.
type runtimeSnapshot struct {
	tenant Tenant
	lister runtimeLister
	maxAge time.Duration

	mu   sync.Mutex
	at   time.Time
	arts map[string]cpi.RuntimeArtifact
}

func newRuntimeSnapshot(tenant Tenant, interval time.Duration) *runtimeSnapshot {
	s := &runtimeSnapshot{tenant: tenant, maxAge: interval / 2}
	s.lister, _ = tenant.(runtimeLister)
	return s
}

// get returns the runtime state of id (nil when it is not deployed).
func (s *runtimeSnapshot) get(id string) (*cpi.RuntimeArtifact, error) {
	if s == nil || s.lister == nil {
		return s.tenantOf().RuntimeArtifact(id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.arts == nil || s.maxAge <= 0 || time.Since(s.at) > s.maxAge { // no interval: never reuse (a coarse clock reads 0 between calls)
		list, err := s.lister.RuntimeArtifacts()
		if err != nil {
			return nil, err
		}
		s.arts = make(map[string]cpi.RuntimeArtifact, len(list))
		for _, a := range list {
			s.arts[a.Id] = a
		}
		s.at = time.Now()
	}
	a, ok := s.arts[id]
	if !ok {
		return nil, nil
	}
	return &a, nil
}

func (s *runtimeSnapshot) tenantOf() Tenant { return s.tenant }
