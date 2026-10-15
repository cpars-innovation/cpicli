package cpitest

import (
	"archive/zip"
	"bytes"
	"strings"
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

// What a real tenant refused at the CPI Studio rehearsal (#22).
func TestRehearsalRefusals(t *testing.T) {
	mf := "Manifest-Version: 1.0\nBundle-SymbolicName: A\nImport-Package: com.sap.esb.security,javax.jms,ja\n vax.wsdl\nImport-Service:\n"
	assert.Equal(t, "invalid header field (line 5)", uploadProblem(zipWith(t, map[string]string{"META-INF/MANIFEST.MF": mf})))
	assert.Empty(t, uploadProblem(zipWith(t, map[string]string{"META-INF/MANIFEST.MF": strings.Replace(mf, "Import-Service:\n", "Import-Service: \n", 1)})),
		"with the space after the colon; continuation lines start with a space")

	vm := `<vm version="2.0"><group id="g1"><entry><agency>A</agency><schema>S</schema><value></value></entry><entry><agency>B</agency><schema>T</schema><value/></entry></group></vm>`
	status, msg := deployProblem("ValueMapping", "VM1", zipWith(t, map[string]string{"value_mapping.xml": vm}))
	assert.Equal(t, 500, status)
	assert.Equal(t, `"Failed to deploy Value Mapping id: VM1"`, msg, "as a tenant answers (quoted)")
	status, msg = deployProblem("ValueMapping", "VM1", zipWith(t, map[string]string{"value_mapping.xml": `<vm version="2.0"></vm>`}))
	assert.Zero(t, status)
	assert.Empty(t, msg, "an empty value mapping deploys")

	status, msg = deployProblem("MessageMapping", "MM1", zipWith(t, map[string]string{"src/main/resources/mapping/MM1.mmap": "<xiObj><transformation/></xiObj>"}))
	assert.Zero(t, status)
	assert.Contains(t, msg, "Generation and build failed for MM1 as validation of resource is failed\nSource element is not assigned\nTarget element is not assigned")
	_, msg = deployProblem("MessageMapping", "MM1", zipWith(t, map[string]string{"src/main/resources/mapping/MM1.mmap": `<xiObj><transformation><brick path="/ns0:Out/No"/></transformation></xiObj>`}))
	assert.Empty(t, msg)
}
