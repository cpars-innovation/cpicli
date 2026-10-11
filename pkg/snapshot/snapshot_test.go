package snapshot

import (
	"context"
	"os"
	"path/filepath"
	gosync "sync"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// demoTenant is the dev tier of the demo landscape: several packages, a
// draft (Billing_Post).
func demoTenant(t *testing.T) *cpitest.Tenant {
	t.Helper()
	m := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(m, "dev", time.Now()))
	return m
}

type progressLog struct {
	mu    gosync.Mutex
	items map[string]Item
	calls int
}

func (p *progressLog) add(it Item) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.items == nil {
		p.items = map[string]Item{}
	}
	p.items[it.Package+"/"+it.Artifact] = it
	p.calls++
}

func TestTakeDemoLandscape(t *testing.T) {
	m := demoTenant(t)
	dir := filepath.Join(t.TempDir(), "packages")
	work := t.TempDir()
	var progress progressLog
	res, err := Take(context.Background(), m.Executer(), dir, Options{WorkDir: work, Tenant: "demo", Progress: progress.add})
	require.NoError(t, err)
	require.NotEmpty(t, res.Items)
	assert.Equal(t, res.Packages, res.Succeeded)
	assert.Equal(t, len(res.Items), progress.calls, "one progress call per artifact")
	assert.Equal(t, int64(len(res.Items)-res.Counts[StatusDraft]), res.Downloaded)

	// C4: the draft is reported as such, kept in the state, not written
	draft, ok := progress.items["Billing/Billing_Post"]
	require.True(t, ok, "%v", res.Items)
	assert.Equal(t, StatusDraft, draft.Status)
	assert.Equal(t, ActionSkip, draft.Action)
	assert.True(t, draft.Draft)
	assert.Equal(t, "draft", draft.Label())
	assert.Equal(t, 1, res.Counts[StatusDraft])
	assert.NoDirExists(t, filepath.Join(dir, "Billing", "Billing_Post"))
	assert.True(t, res.State.Artifacts["Billing/Billing_Post"].Draft)

	// the state on disk is the result's; the work folder is gone
	state, err := LoadState(filepath.Join(dir, ".cpi", "snapshot-state.json"))
	require.NoError(t, err)
	assert.Equal(t, "demo", state.Tenant)
	assert.Equal(t, res.State.Artifacts, state.Artifacts)
	left, _ := os.ReadDir(work)
	assert.Empty(t, left, "the work folder is removed after the run")
	assert.Contains(t, res.Paths, ".cpi/snapshot-state.json")
	assert.Contains(t, res.Paths, "Billing/Billing.json")

	// G9: the public hashes of a written folder are what the state stores
	for _, it := range res.Items {
		if it.Status == StatusDraft {
			continue
		}
		assert.Equal(t, StatusNew, it.Status, it.Artifact)
		assert.Contains(t, res.Paths, it.Package+"/"+it.Artifact)
		folder := filepath.Join(dir, it.Package, it.Artifact)
		st := state.Artifacts[it.Package+"/"+it.Artifact]
		upload, err := content.UploadHash(os.DirFS(folder), st.Type)
		require.NoError(t, err)
		assert.Equal(t, st.UploadHash, upload, it.Artifact)
		files, err := content.TreeHash(folder)
		require.NoError(t, err)
		assert.Equal(t, st.FilesHash, files, it.Artifact)
		contentHash, err := content.ContentHash(os.DirFS(folder))
		require.NoError(t, err)
		assert.Equal(t, st.ContentHash, contentHash, it.Artifact)
	}

	// incremental: nothing is downloaded again, nothing written
	progress = progressLog{}
	res, err = Take(context.Background(), m.Executer(), dir, Options{Incremental: true, Tenant: "demo", Progress: progress.add})
	require.NoError(t, err)
	assert.Zero(t, res.Downloaded)
	assert.Equal(t, int64(len(res.Items)-1), res.Skipped)
	assert.Equal(t, map[string]int{StatusUnchanged: len(res.Items) - 1, StatusDraft: 1}, res.Counts)
	assert.Equal(t, []string{".cpi/snapshot-state.json"}, res.Paths, "only the state is rewritten (unchanged)")
	assert.Equal(t, len(res.Items), progress.calls)

	// the draft is saved: written like any new artifact, the flag cleared
	m.Artifacts["Billing_Post"].DesignVersion = "1.0.1"
	res, err = Take(context.Background(), m.Executer(), dir, Options{Packages: []string{"Billing"}, Incremental: true})
	require.NoError(t, err)
	assert.Equal(t, StatusNew, items(res)["Billing_Post"].Status)
	assert.DirExists(t, filepath.Join(dir, "Billing", "Billing_Post"))
	assert.False(t, res.State.Artifacts["Billing/Billing_Post"].Draft)
}

func items(res *Result) map[string]Item {
	out := map[string]Item{}
	for _, it := range res.Items {
		out[it.Artifact] = it
	}
	return out
}

func TestTakeOptions(t *testing.T) {
	m := demoTenant(t)
	dir := t.TempDir()
	ctx := context.Background()

	_, err := Take(ctx, m.Executer(), dir, Options{DraftHandling: "MAYBE"})
	assert.ErrorContains(t, err, "draft handling")
	_, err = Take(ctx, m.Executer(), dir, Options{Packages: []string{"a"}, Exclude: []string{"b"}})
	assert.ErrorContains(t, err, "mutually exclusive")
	_, err = Take(ctx, m.Executer(), "", Options{})
	assert.Error(t, err)

	// a dry run writes nothing, no state either
	res, err := Take(ctx, m.Executer(), dir, Options{DryRun: true, Packages: []string{"Billing"}})
	require.NoError(t, err)
	assert.True(t, res.DryRun)
	assert.NotEmpty(t, res.Items)
	assert.Empty(t, res.Paths)
	entries, _ := os.ReadDir(dir)
	assert.Empty(t, entries)

	// an in-memory state: nothing is read or written
	state := NewState()
	res, err = Take(ctx, m.Executer(), dir, Options{State: state, Packages: []string{"Billing"}, DraftHandling: "ADD", KeepWorkDir: true, WorkDir: t.TempDir()})
	require.NoError(t, err)
	assert.Same(t, state, res.State)
	assert.NoFileExists(t, filepath.Join(dir, ".cpi", "snapshot-state.json"))
	assert.Equal(t, "new (draft)", items(res)["Billing_Post"].Label(), "ADD writes the draft")
	assert.True(t, state.Artifacts["Billing/Billing_Post"].Draft)
	assert.NotContains(t, res.Paths, ".cpi/snapshot-state.json")

	// a state of another tenant is not used
	state.Tenant = "demo"
	res, err = Take(ctx, m.Executer(), dir, Options{State: state, Packages: []string{"Billing"}, Tenant: "other", DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, "other", state.Tenant)
	assert.Empty(t, state.Artifacts, "replaced by an empty state")
	assert.Equal(t, StatusDraft, items(res)["Billing_Post"].Status)

	// cancelled: nothing is downloaded
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	before := m.Count("GET /api/v1/IntegrationDesigntimeArtifacts(Id='")
	res, err = Take(cancelled, m.Executer(), t.TempDir(), Options{})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, res.Succeeded)
	assert.Equal(t, before, m.Count("GET /api/v1/IntegrationDesigntimeArtifacts(Id='"))
}
