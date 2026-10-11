package lint

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cpars-innovation/cpicli/pkg/catalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The demo landscape's flows against a catalogue: HTTP receiver 1.4.1 where
// the tenant offers 1.22.0 is outdated; a Groovy script 2.0.0 on a tenant
// that offers 1.1.2 cannot be checked or deployed there.
func TestComponentRules(t *testing.T) {
	dir := filepath.Join("..", "..", "internal", "cpitest", "landscapes", "demo", "packages")
	cat := &catalog.Catalog{Schema: catalog.Schema,
		Adapters: []catalog.Adapter{{Name: "HTTP", Direction: "Receiver", Version: "1.22.0", Source: catalog.SourceEditor,
			VariantURI: "ctype::AdapterVariant/cname::sap:HTTP/tp::HTTP/mp::None/direction::Receiver/version::1.22.0"}},
		Steps: []catalog.Step{{Name: "GroovyScript", Version: "1.1.2", Source: catalog.SourceEditor,
			VariantURI: "ctype::FlowstepVariant/cname::GroovyScript/version::1.1.2"}}}
	res, err := Run(context.Background(), Options{Dir: dir, Components: cat})
	require.NoError(t, err)
	byRule := map[string][]Finding{}
	for _, f := range res.Findings {
		byRule[f.Rule] = append(byRule[f.Rule], f)
	}
	newer := byRule["component-newer-than-tenant"]
	require.Len(t, newer, 1)
	assert.Equal(t, "Billing_Post", newer[0].Artifact)
	assert.Equal(t, SevError, newer[0].Severity)
	assert.Contains(t, newer[0].Message, "uses version 2.0.0; the tenant offers 1.1.2")

	var http []string
	for _, f := range byRule["outdated-component"] {
		if f.ElementName == "ERP" || f.ElementName == "Finance" || f.ElementName == "HTTP" {
			http = append(http, f.Message)
		}
	}
	require.NotEmpty(t, http, "%v", byRule["outdated-component"])
	assert.Contains(t, http[0], "the tenant offers 1.22.0")

	// without a catalogue neither rule reports the tenant
	res, err = Run(context.Background(), Options{Dir: dir})
	require.NoError(t, err)
	for _, f := range res.Findings {
		assert.NotEqual(t, "component-newer-than-tenant", f.Rule)
		assert.NotContains(t, f.Message, "the tenant offers")
	}
}
