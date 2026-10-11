package cpitest

import (
	"archive/zip"
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func zipWith(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, c := range files {
		w, err := zw.Create(n)
		require.NoError(t, err)
		_, _ = w.Write([]byte(c))
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestUploadProblemPropdefWithoutConstraint(t *testing.T) {
	bad := zipWith(t, map[string]string{"src/main/resources/parameters.propdef": `<parameters><parameter><name>A</name><type>xsd:string</type></parameter></parameters>`})
	good := zipWith(t, map[string]string{"src/main/resources/parameters.propdef": `<parameters><parameter><key/><name>A</name><type>xsd:string</type><isRequired>false</isRequired><constraint/><description/></parameter></parameters>`})
	assert.Contains(t, uploadProblem(bad), `"constraint" is null`)
	assert.Empty(t, uploadProblem(good))
	assert.Empty(t, uploadProblem([]byte("not a zip")))
}

func TestDemoContentIsImportable(t *testing.T) {
	m := NewTenant(t, nil)
	require.NoError(t, SeedDemo(m, "dev", time.Now()))
	for id, a := range m.Artifacts {
		if len(a.Zip) > 0 {
			assert.Empty(t, uploadProblem(a.Zip), id)
		}
	}
	for _, p := range m.Packages {
		assert.NotEmpty(t, p.ShortText, p.ID)
	}
}
