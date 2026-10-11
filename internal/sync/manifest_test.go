package sync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManifestFromTenantWithoutBlankLine(t *testing.T) {
	// as downloaded from a tenant: CRLF, continuation lines, no closing blank line
	mf := "Manifest-Version: 1.0\r\nBundle-SymbolicName: X; singleton:=true\r\nImport-Package: a,b\r\n ,c\r\nBundle-Version: 1.0.1\r\nSAP-BundleType: IntegrationFlow\r\n"
	p := filepath.Join(t.TempDir(), "MANIFEST.MF")
	require.NoError(t, os.WriteFile(p, []byte(mf), 0o644))
	h, err := GetManifestHeaders(p)
	require.NoError(t, err)
	assert.Equal(t, "1.0.1", h.Get("Bundle-Version"))
	assert.Equal(t, "IntegrationFlow", h.Get("SAP-BundleType"))
}
