package content

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDiffDirectories_SameIgnoringOrigin(t *testing.T) {
	contentDiffer := DiffDirectories("../../test/testdata/DiffComparison/Dir1/", "../../test/testdata/DiffComparison/Dir2/")

	assert.False(t, contentDiffer, "Directory contents differ")
}

func TestDiffDirectories_Different(t *testing.T) {
	contentDiffer := DiffDirectories("../../test/testdata/DiffComparison/Dir1/", "../../test/testdata/DiffComparison/Dir3/")

	assert.True(t, contentDiffer, "Directory contents do not differ")
}

func TestDiffFile_Different(t *testing.T) {
	fileDiffer := DiffFile("../../test/testdata/DiffComparison/Dir1/MANIFEST.MF", "../../test/testdata/DiffComparison/Dir3/MANIFEST.MF")

	assert.True(t, fileDiffer, "File contents do not differ")
}

func TestDiffNormalization(t *testing.T) {
	dir := t.TempDir()
	write := func(p, content string) {
		full := dir + "/" + p
		assert.NoError(t, os.MkdirAll(full[:strings.LastIndex(full, "/")], 0o755))
		assert.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}
	write("a/META-INF/MANIFEST.MF", "Bundle-Version: 1.0.0\r\nOrigin-Bundle-Name: X\r\n")
	write("b/META-INF/MANIFEST.MF", "Bundle-Version:  1.0.0\n\n")
	write("a/src/main/resources/parameters.prop", "x=1")
	write("b/src/main/resources/parameters.prop", "x=2")
	assert.False(t, DiffDirectories(dir+"/a", dir+"/b"), "CR, spaces, blank lines, Origin and parameters.prop are ignored")

	write("b/src/main/resources/script/new.groovy", "println 1")
	assert.True(t, DiffDirectories(dir+"/a", dir+"/b"), "a file only on one side")
	assert.False(t, DiffDirectories(dir+"/none1", dir+"/none2"), "both missing")

	write("p1.prop", "#Mon Oct 05 2026\nk=v\n")
	write("p2.prop", "#Tue Oct 06 2026\nk = v\n")
	assert.False(t, DiffFile(dir+"/p1.prop", dir+"/p2.prop"))
	write("p3.prop", "k=w\n")
	assert.True(t, DiffFile(dir+"/p1.prop", dir+"/p3.prop"))

	h1, err := ContentHash(os.DirFS(dir + "/a"))
	assert.NoError(t, err)
	write("a/src/main/resources/parameters.prop", "x=3")
	h2, _ := ContentHash(os.DirFS(dir + "/a"))
	assert.Equal(t, h1, h2, "parameters are not content")
	write("a/src/main/resources/script/s.groovy", "x")
	h3, _ := ContentHash(os.DirFS(dir + "/a"))
	assert.NotEqual(t, h1, h3)
}
