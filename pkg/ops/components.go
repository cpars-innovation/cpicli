package ops

import (
	"errors"
	"io/fs"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/catalog"
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

// ComponentList is a filtered view of the catalogue.
type ComponentList struct {
	Tenant      string            `json:"tenant,omitempty"`
	GeneratedAt time.Time         `json:"generatedAt"`
	Sources     []string          `json:"sources"`
	Adapters    []catalog.Adapter `json:"adapters"`
	Steps       []catalog.Step    `json:"steps"`
}

// FilterComponents filters the catalogue by name (substring, case-insensitive,
// also matching the display name), kind (adapter, step) and direction.
func FilterComponents(c *catalog.Catalog, name, kind, direction string) *ComponentList {
	res := &ComponentList{Tenant: c.Tenant, GeneratedAt: c.GeneratedAt, Sources: c.Sources, Adapters: []catalog.Adapter{}, Steps: []catalog.Step{}}
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
