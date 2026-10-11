package ops

import (
	"context"
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
