package ops

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cpars-innovation/cpicli/pkg/pdrepo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPDSnapshot(t *testing.T) {
	_, api := pdTenant(t)
	dir := t.TempDir()
	res, err := PDSnapshot(api, pdrepo.New(dir), true, nil)
	require.NoError(t, err)
	assert.Equal(t, &PDSnapshotResult{StringParameters: 3, BinaryParameters: 2, PIDs: []string{"ONE_OMS", "OTHER"}}, res)
	props, err := os.ReadFile(filepath.Join(dir, "ONE_OMS", "String.properties"))
	require.NoError(t, err)
	assert.Contains(t, string(props), "endpoint=https://oms")
	assert.FileExists(t, filepath.Join(dir, "ONE_OMS", "Binary", "now_email.xsl"))

	// the snapshot is what PDDiff compares with: no differences
	diff, err := PDDiff(api, pdrepo.New(dir), nil)
	require.NoError(t, err)
	for _, it := range diff.Items {
		assert.Equal(t, PDUnchanged, it.Change, it.Kind+":"+it.ID)
	}

	// add only keeps local values; a PID filter limits the snapshot
	writeFile(t, filepath.Join(dir, "ONE_OMS", "String.properties"), "endpoint=local\n")
	res, err = PDSnapshot(api, pdrepo.New(dir), false, []string{"ONE_OMS"})
	require.NoError(t, err)
	assert.Equal(t, []string{"ONE_OMS"}, res.PIDs)
	params, err := pdrepo.New(dir).ReadStringParameters("ONE_OMS")
	require.NoError(t, err)
	values := map[string]string{}
	for _, p := range params {
		values[p.ID] = p.Value
	}
	assert.Equal(t, map[string]string{"endpoint": "local", "old": "x"}, values)
}
