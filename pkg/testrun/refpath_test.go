package testrun

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// With a root, a referenced file must be inside it once links are
// resolved; without one (the CLI) the reference is relative to the case.
func TestRefPathStaysInRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	dir := filepath.Join(root, "tests", "Flow")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "body.xml"), []byte("<in/>"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(parent, "secret"), []byte("x"), 0o600))
	caseFile := filepath.Join(dir, "c.yaml")

	b, err := readRef(caseFile, "body.xml", root)
	require.NoError(t, err)
	assert.Equal(t, "<in/>", string(b))
	_, err = readRef(caseFile, "../../../secret", root)
	assert.ErrorContains(t, err, "outside the server root")
	_, err = readRef(caseFile, filepath.Join(parent, "secret"), root)
	assert.ErrorContains(t, err, "relative to the case file")
	if os.Symlink(filepath.Join(parent, "secret"), filepath.Join(dir, "link")) == nil {
		_, err = readRef(caseFile, "link", root)
		assert.ErrorContains(t, err, "outside the server root")
	}
	b, err = readRef(caseFile, "../../../secret", "")
	require.NoError(t, err, "the CLI reads what its user can read")
	assert.Equal(t, "x", string(b))
}
