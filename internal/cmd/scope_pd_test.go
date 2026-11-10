package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Partner grants as the cpictl gateway builds them: pd snapshot and pd deploy
// --full-sync on the granted partners, refused on others before the tenant
// is asked.
func TestScopePartnerGrantsSnapshotAndFullSync(t *testing.T) {
	mock := scopeTenant(t)
	mock.PDStrings = map[string]string{"ONE_ROUTING/target": "erp", "ONE_ROUTING/old": "x", "SAP_TPM_X/key": "s", "HR_X/flag": "on"}
	dir := t.TempDir()
	scope := filepath.Join(dir, "scope.json")
	require.NoError(t, os.WriteFile(scope, []byte(`{"version":1,"grants":[
		{"packages":["SD_*"],"actions":["read"]},
		{"partners":["ONE_*"],"actions":["read","pd-write"]}],
		"deny":[{"partners":["SAP_*"]}]}`), 0o600))
	pd := filepath.Join(dir, "pd")

	doc, code := runScoped(t, mock, "pd", "snapshot", "--pids", "ONE_ROUTING", "--resources-path", pd, "--scope-file", scope)
	require.Equal(t, exitcode.OK, code, doc)
	_, err := os.Stat(filepath.Join(pd, "ONE_ROUTING"))
	require.NoError(t, err, "snapshot wrote the granted partner")

	// a full snapshot only sees the granted partners
	all := filepath.Join(dir, "all")
	doc, code = runScoped(t, mock, "pd", "snapshot", "--resources-path", all, "--scope-file", scope)
	require.Equal(t, exitcode.OK, code, doc)
	entries, _ := os.ReadDir(all)
	var pids []string
	for _, e := range entries {
		pids = append(pids, e.Name())
	}
	assert.Equal(t, []string{"ONE_ROUTING"}, pids)

	// pd partners lists only the granted partners
	doc, code = runScoped(t, mock, "pd", "list", "--scope-file", scope)
	require.Equal(t, exitcode.OK, code, doc)
	assert.Contains(t, fmt.Sprint(doc["result"]), "ONE_ROUTING")
	assert.NotContains(t, fmt.Sprint(doc["result"]), "SAP_TPM_X")
	assert.NotContains(t, fmt.Sprint(doc["result"]), "HR_X")

	// full sync deletes the granted partner's parameter missing locally
	require.NoError(t, os.WriteFile(filepath.Join(pd, "ONE_ROUTING", "String.properties"), []byte("target=erp\n"), 0o600))
	doc, code = runScoped(t, mock, "pd", "deploy", "--pids", "ONE_ROUTING", "--full-sync", "--resources-path", pd, "--scope-file", scope)
	require.Equal(t, exitcode.OK, code, doc)
	_, hasOld := mock.PDStrings["ONE_ROUTING/old"]
	assert.False(t, hasOld, "full sync deleted ONE_ROUTING/old")
	assert.Contains(t, mock.PDStrings, "SAP_TPM_X/key")
	assert.Contains(t, mock.PDStrings, "HR_X/flag")

	// another partner: refused before any write or delete
	require.NoError(t, os.MkdirAll(filepath.Join(pd, "SAP_TPM_X"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pd, "SAP_TPM_X", "String.properties"), []byte("key=t\n"), 0o600))
	before := mock.Count("DELETE /api/v1/StringParameters") + mock.Count("PUT /api/v1/StringParameters") + mock.Count("POST /api/v1/StringParameters")
	doc, code = runScoped(t, mock, "pd", "deploy", "--pids", "SAP_TPM_X", "--full-sync", "--resources-path", pd, "--scope-file", scope)
	assert.Equal(t, exitcode.Partial, code, doc)
	assert.Contains(t, fmt.Sprint(doc["error"]), "scope: ")
	assert.Contains(t, fmt.Sprint(doc), "SAP_TPM_X")
	assert.Equal(t, before, mock.Count("DELETE /api/v1/StringParameters")+mock.Count("PUT /api/v1/StringParameters")+mock.Count("POST /api/v1/StringParameters"))
	assert.Equal(t, "s", mock.PDStrings["SAP_TPM_X/key"])

	// read only: pd-write refused
	ro := filepath.Join(dir, "ro.json")
	require.NoError(t, os.WriteFile(ro, []byte(`{"version":1,"grants":[{"partners":["ONE_*"],"actions":["read"]}]}`), 0o600))
	mock.PDStrings["ONE_ROUTING/new"] = "remote only"
	doc, code = runScoped(t, mock, "pd", "deploy", "--pids", "ONE_ROUTING", "--full-sync", "--resources-path", pd, "--scope-file", ro)
	assert.Equal(t, exitcode.Partial, code, doc)
	assert.Contains(t, fmt.Sprint(doc["error"]), "scope: ")
	assert.Contains(t, mock.PDStrings, "ONE_ROUTING/new", "no delete without pd-write")
}

// Behind the scope's filter, pages of hidden partners in front of the
// granted ones neither stop nor repeat the listing (cpicli#82).
func TestScopePartnerListsPageBehindTheFilter(t *testing.T) {
	mock := scopeTenant(t)
	mock.PDStrings = map[string]string{"ONE_ROUTING/target": "erp"}
	mock.PDBinaries = map[string]cpitest.PDBinary{}
	for i := range 1200 {
		mock.PDStrings[fmt.Sprintf("AAA_%04d/k", i)] = "v"
	}
	for i := range 70 {
		mock.PDBinaries[fmt.Sprintf("AAA_%04d/b", i)] = cpitest.PDBinary{ContentType: "txt", Content: []byte("x")}
	}
	mock.PDBinaries["ONE_ROUTING/map"] = cpitest.PDBinary{ContentType: "txt", Content: []byte("m")}
	dir := t.TempDir()
	scope := filepath.Join(dir, "scope.json")
	require.NoError(t, os.WriteFile(scope, []byte(`{"version":1,"grants":[{"partners":["ONE_*"],"actions":["read"]}]}`), 0o600))

	for _, args := range [][]string{{"--pids", "ONE_ROUTING"}, nil} {
		out := filepath.Join(dir, fmt.Sprint(len(args)))
		doc, code := runScoped(t, mock, append([]string{"pd", "snapshot", "--resources-path", out, "--scope-file", scope}, args...)...)
		require.Equal(t, exitcode.OK, code, doc)
		entries, _ := os.ReadDir(out)
		require.Len(t, entries, 1, args)
		assert.Equal(t, "ONE_ROUTING", entries[0].Name())
		bins, _ := filepath.Glob(filepath.Join(out, "ONE_ROUTING", "Binary", "*"))
		assert.NotEmpty(t, bins, "the binary behind 70 hidden ones (pages of 30)")
		props, err := os.ReadFile(filepath.Join(out, "ONE_ROUTING", "String.properties"))
		require.NoError(t, err)
		assert.Contains(t, string(props), "target=erp")
	}

	doc, code := runScoped(t, mock, "pd", "list", "--scope-file", scope)
	require.Equal(t, exitcode.OK, code, doc)
	res, _ := doc["result"].(map[string]any)
	partners, _ := res["partners"].([]any)
	require.Len(t, partners, 1, "one row, no repeats")
	row, _ := partners[0].(map[string]any)
	assert.EqualValues(t, 1, row["strings"])
	assert.EqualValues(t, 1, row["binaries"])
}
