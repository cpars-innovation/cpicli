package content

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hashFixture is an integration flow as a tenant exports it: CRLF, a
// timestamp in the .prop files, an Origin header, a binary.
var hashFixture = map[string]string{
	"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\r\nBundle-SymbolicName: Orders; singleton:=true\r\nBundle-Name: Orders\r\nBundle-Version: 1.0.7\r\nOrigin-Bundle-Name: x\r\nSAP-BundleType: IntegrationFlow\r\n\r\n",
	"metainfo.prop":        "#Thu Oct 08 14:11:02 UTC 2026\r\ndescription=Orders\r\n",
	".project":             "<projectDescription><name>Orders</name></projectDescription>\n",
	"src/main/resources/scenarioflows/integrationflow/Orders.iflw": "<bpmn2:definitions>\r\n  <bpmn2:process id=\"p\"/>\r\n</bpmn2:definitions>\r\n",
	"src/main/resources/script/s.groovy":                           "def x = 1\n\n// comment\n",
	"src/main/resources/parameters.prop":                           "#Thu Oct 08 14:11:02 UTC 2026\r\nUser=u\r\nHost=h\r\n",
	"src/main/resources/lib/x.jar":                                 "PK\r\n\x00binary\r\n",
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, data := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(data), 0o644))
	}
	return dir
}

// The hashes are stored in snapshot states and compared across versions:
// they must never change. The values were computed by cpictl 0.9.1
// (internal/file) before the functions moved here.
func TestHashesAreStable(t *testing.T) {
	dir := writeTree(t, hashFixture)
	fsys := os.DirFS(dir)
	h, err := UploadHash(fsys, "Integration")
	require.NoError(t, err)
	assert.Equal(t, "0410d7f6f98d5077db04593cd3201107", h)
	h, err = UploadHash(fsys, "ValueMapping")
	require.NoError(t, err)
	assert.Equal(t, "cbb7c7ddf2d8e9c68e2b9a1532776023", h)
	h, err = ContentHash(fsys)
	require.NoError(t, err)
	assert.Equal(t, "a0ea1838b78b7a7486ce0936a547c058", h)
	h, err = TreeHash(dir)
	require.NoError(t, err)
	assert.Equal(t, "0d68ef89453e0058338c44e3b94501f5900b88a43e4ab56251b99243a08d6f55", h)

	// normalized as a snapshot writes it: the upload and content hashes do
	// not see line endings and timestamps, the tree hash sees every byte
	require.NoError(t, NormalizeTree(dir))
	h, _ = UploadHash(fsys, "Integration")
	assert.Equal(t, "0410d7f6f98d5077db04593cd3201107", h)
	h, _ = ContentHash(fsys)
	assert.Equal(t, "a0ea1838b78b7a7486ce0936a547c058", h)
	h, _ = TreeHash(dir)
	assert.Equal(t, "66ce5942998f81d1436299e771442c7ce95842be7b2f138279afe5759349c388", h)
	jar, _ := os.ReadFile(filepath.Join(dir, "src", "main", "resources", "lib", "x.jar"))
	assert.Equal(t, hashFixture["src/main/resources/lib/x.jar"], string(jar), "binaries byte for byte")
	params, _ := os.ReadFile(filepath.Join(dir, "src", "main", "resources", "parameters.prop"))
	assert.Equal(t, "Host=h\nUser=u\n", string(params))
}

// UploadHash ignores Bundle-Version (the versioning modes set it), the
// ContentHash does not.
func TestUploadHashIgnoresVersion(t *testing.T) {
	a := writeTree(t, hashFixture)
	files := map[string]string{}
	for k, v := range hashFixture {
		files[k] = v
	}
	files["META-INF/MANIFEST.MF"] = "Manifest-Version: 1.0\nBundle-SymbolicName: Orders; singleton:=true\nBundle-Name: Orders\nBundle-Version: 2.0.0\nSAP-BundleType: IntegrationFlow\n"
	b := writeTree(t, files)
	ha, _ := UploadHash(os.DirFS(a), "Integration")
	hb, _ := UploadHash(os.DirFS(b), "Integration")
	assert.Equal(t, ha, hb)
	ca, _ := ContentHash(os.DirFS(a))
	cb, _ := ContentHash(os.DirFS(b))
	assert.NotEqual(t, ca, cb)
	diff, err := TreeDiff(a, b, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"META-INF/MANIFEST.MF"}, diff)
}
