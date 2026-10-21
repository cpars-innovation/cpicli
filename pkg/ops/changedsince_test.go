package ops

import (
	"context"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The freshness probe lists what changed after a time; the fingerprint also
// shows a deletion, which no change time can (#29).
func TestDesigntimeChangedSince(t *testing.T) {
	m := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"Old": {Type: "Integration", Package: "P", DesignVersion: "1.0.0", ModifiedAt: t0.Add(-48 * time.Hour)},
		"New": {Type: "Integration", Package: "P", DesignVersion: "1.0.3", ModifiedAt: t0, ModifiedBy: "jane.doe@example.com"},
		"Map": {Type: "ValueMapping", Package: "Q", DesignVersion: "1.0.0", ModifiedAt: t0.Add(-time.Hour)},
	})
	m.Packages = []cpitest.Package{{ID: "P", Version: "1.0.0"}, {ID: "Q", Version: "1.0.0"}}

	res, err := DesigntimeChangedSince(context.Background(), m.Executer(), ChangedSinceOptions{Since: t0.Add(-2 * time.Hour)})
	require.NoError(t, err)
	assert.True(t, res.Changed)
	assert.Equal(t, 2, res.Packages)
	assert.Equal(t, 3, res.Artifacts)
	require.Len(t, res.ChangedArtifacts, 2)
	assert.Equal(t, ChangedArtifact{Package: "P", ID: "New", Type: "Integration", Version: "1.0.3", ModifiedAt: t0, ModifiedBy: "jane.doe@example.com"},
		res.ChangedArtifacts[0], "newest first")
	assert.Equal(t, "Map", res.ChangedArtifacts[1].ID)
	require.NotNil(t, res.Latest)
	assert.True(t, res.Latest.Equal(t0))
	require.NotEmpty(t, res.Fingerprint)

	// nothing new since then, same fingerprint: unchanged
	again, err := DesigntimeChangedSince(context.Background(), m.Executer(), ChangedSinceOptions{Since: t0, Fingerprint: res.Fingerprint})
	require.NoError(t, err)
	assert.False(t, again.Changed)
	assert.Empty(t, again.ChangedArtifacts)
	assert.Equal(t, res.Fingerprint, again.Fingerprint)

	// a deletion changes no change time, only the fingerprint
	delete(m.Artifacts, "Old")
	gone, err := DesigntimeChangedSince(context.Background(), m.Executer(), ChangedSinceOptions{Since: t0, Fingerprint: res.Fingerprint})
	require.NoError(t, err)
	assert.True(t, gone.Changed)
	assert.Empty(t, gone.ChangedArtifacts)

	only, err := DesigntimeChangedSince(context.Background(), m.Executer(), ChangedSinceOptions{Since: t0.Add(-2 * time.Hour), Packages: []string{"Q"}})
	require.NoError(t, err)
	assert.Equal(t, 1, only.Packages)
	require.Len(t, only.ChangedArtifacts, 1)
	assert.Equal(t, "Map", only.ChangedArtifacts[0].ID)
}
