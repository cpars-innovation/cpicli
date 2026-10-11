package ops

import (
	"errors"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/catalog"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// ComponentsFile is the default location of the component catalogue.
const ComponentsFile = ".cpi/components.json"

// LearnComponents adds the variants the discovered flows use to the
// catalogue at file (created when missing; entries from the Web UI's editor
// metadata are kept). tenant names the source tenant.
func LearnComponents(d *Discovery, file, tenant string) (*catalog.Catalog, error) {
	c, err := catalog.Load(file)
	if errors.Is(err, fs.ErrNotExist) {
		c, err = &catalog.Catalog{Schema: catalog.Schema}, nil
	}
	if err != nil {
		return nil, err
	}
	var uses []catalog.Use
	for _, f := range d.IFlows {
		for _, uri := range f.Components {
			uses = append(uses, catalog.Use{URI: uri})
		}
	}
	c.Learn(uses)
	if tenant != "" && c.Tenant == "" {
		c.Tenant = tenant
	}
	c.GeneratedAt = time.Now().UTC().Truncate(time.Second)
	return c, c.Save(file)
}

// DeployedAdapters lists the custom adapters deployed on the tenant: runtime
// artifacts whose type names an adapter (the API has no list of SAP's own
// adapter versions; those come from the editor metadata).
func DeployedAdapters(exe *httpclnt.HTTPExecuter) ([]catalog.DeployedAdapter, error) {
	artifacts, err := cpi.NewContent(exe).RuntimeArtifacts(nil)
	if err != nil {
		return nil, err
	}
	out := []catalog.DeployedAdapter{}
	for _, a := range artifacts {
		if strings.Contains(strings.ToUpper(a.Type), "ADAPTER") {
			out = append(out, catalog.DeployedAdapter{ID: a.Id, Name: a.Name, Version: a.Version, Type: a.Type, Status: a.Status})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ComponentList is a filtered view of the catalogue.
type ComponentList struct {
	Tenant      string            `json:"tenant,omitempty"`
	GeneratedAt time.Time         `json:"generatedAt"`
	Sources     []string          `json:"sources"`
	Adapters    []catalog.Adapter `json:"adapters"`
	Steps       []catalog.Step    `json:"steps"`
	// DeployedAdapters are custom adapters deployed on the tenant.
	DeployedAdapters []catalog.DeployedAdapter `json:"deployedAdapters,omitempty"`
}

// FilterComponents filters the catalogue by name (substring, case-insensitive,
// also matching the display name), kind (adapter, step) and direction.
func FilterComponents(c *catalog.Catalog, name, kind, direction string) *ComponentList {
	res := &ComponentList{Tenant: c.Tenant, GeneratedAt: c.GeneratedAt, Sources: c.Sources, Adapters: []catalog.Adapter{}, Steps: []catalog.Step{}}
	if kind == "" || strings.EqualFold(kind, "adapter") {
		for _, d := range c.DeployedAdapters {
			if name == "" || strings.Contains(strings.ToLower(d.ID+" "+d.Name), strings.ToLower(name)) {
				res.DeployedAdapters = append(res.DeployedAdapters, d)
			}
		}
	}
	match := func(names ...string) bool {
		if name == "" {
			return true
		}
		for _, n := range names {
			if strings.Contains(strings.ToLower(n), strings.ToLower(name)) {
				return true
			}
		}
		return false
	}
	if kind == "" || strings.EqualFold(kind, "adapter") {
		for _, a := range c.Adapters {
			if match(a.Name, a.DisplayName) && (direction == "" || strings.EqualFold(direction, a.Direction)) {
				res.Adapters = append(res.Adapters, a)
			}
		}
	}
	if (kind == "" || strings.EqualFold(kind, "step")) && direction == "" {
		for _, s := range c.Steps {
			if match(s.Name, s.DisplayName, s.Flavor) {
				res.Steps = append(res.Steps, s)
			}
		}
	}
	return res
}
