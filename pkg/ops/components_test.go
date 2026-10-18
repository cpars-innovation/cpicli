package ops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cpars-innovation/cpicli/pkg/catalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLearnComponentsFromDiscovery(t *testing.T) {
	d, err := DiscoverDir(context.Background(), filepath.Join("..", "..", "internal", "cpitest", "landscapes", "demo", "packages"))
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "components.json")
	c, err := LearnComponents(d, file, "demo")
	require.NoError(t, err)
	assert.Equal(t, []string{catalog.SourceLearned}, c.Sources)
	assert.Equal(t, "demo", c.Tenant)
	v, ok := c.Latest("ctype::FlowstepVariant/cname::GroovyScript")
	assert.True(t, ok)
	assert.Equal(t, "2.0.0", v, "Billing_Post's version 2 script")
	res := FilterComponents(c, "sftp", "adapter", "receiver")
	require.Len(t, res.Adapters, 1)
	assert.Equal(t, "SFTP", res.Adapters[0].Name)
	assert.Empty(t, res.Steps)

	back, err := catalog.Load(file)
	require.NoError(t, err)
	assert.Equal(t, c.Adapters, back.Adapters)
}

func TestImportComponentsKeepsLearned(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "catalog", "testdata", "editor-metadata.json"))
	require.NoError(t, err)
	old := &catalog.Catalog{Tenant: "old"}
	old.Learn([]catalog.Use{{URI: "ctype::FlowstepVariant/cname::XmlModifier/version::1.1.0"}})
	learnedAt := old.SourceDates[catalog.SourceLearned]
	require.False(t, learnedAt.IsZero())

	c, err := ImportComponents(old, data, "")
	require.NoError(t, err)
	assert.Equal(t, "old", c.Tenant)
	m, ok := c.Find("ctype::FlowstepVariant/cname::XmlModifier")
	require.True(t, ok)
	assert.Equal(t, "1.1.0", m.Version())
	assert.Equal(t, learnedAt, c.SourceDates[catalog.SourceLearned], "re-adding does not make learned entries newer")
	assert.Contains(t, c.SourceDates, catalog.SourceEditor)

	c, err = ImportComponents(nil, data, "t1")
	require.NoError(t, err)
	assert.Equal(t, "t1", c.Tenant)
	_, ok = c.Find("ctype::FlowstepVariant/cname::XmlModifier")
	assert.False(t, ok)
	assert.NotContains(t, c.SourceDates, catalog.SourceLearned)

	_, err = ImportComponents(old, []byte(`{}`), "")
	assert.Error(t, err)
}
