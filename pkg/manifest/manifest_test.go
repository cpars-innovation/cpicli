package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseContinuationLines(t *testing.T) {
	h := Parse([]byte("Manifest-Version: 1.0\r\nImport-Package: a,\r\n b\r\nBundle-Version: 1.0.3\r\n"))
	assert.Equal(t, "1.0", h["Manifest-Version"])
	assert.Equal(t, "a,b", h["Import-Package"])
	assert.Equal(t, "1.0.3", h["Bundle-Version"])
}

func TestSetHeadersKeepsOtherLinesAndWraps(t *testing.T) {
	in := []byte("Manifest-Version: 1.0\r\nBundle-Version: 1.0.0\r\nOrigin: x\r\n")
	long := strings.Repeat("x", 100)
	out := SetHeaders(in, map[string]string{"Bundle-Version": "1.0.1", "Z-Long": long})
	assert.Equal(t, "Manifest-Version: 1.0\r\nBundle-Version: 1.0.1\r\nOrigin: x\r\n"+
		strings.Join(WrapLine("Z-Long: "+long), "\r\n")+"\r\n", string(out))
	assert.Equal(t, long, Parse(out)["Z-Long"])
	for _, l := range WrapLine("Z-Long: " + long) {
		assert.LessOrEqual(t, len(l), 72)
	}
}

func TestVersionAndSetVersion(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "META-INF"), 0o755))
	require.NoError(t, os.WriteFile(Path(dir), []byte("Manifest-Version: 1.0\nBundle-Version: 1.0.0\n"), 0o644))
	require.NoError(t, SetVersion(dir, "2.1.0"))
	v, err := Version(dir)
	require.NoError(t, err)
	assert.Equal(t, "2.1.0", v)
	_, err = Version(t.TempDir())
	assert.Error(t, err)
}
