package cmd

import (
	"archive/zip"
	"bytes"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecretScan(t *testing.T) {
	var zipped bytes.Buffer
	zw := zip.NewWriter(&zipped)
	w, err := zw.Create("conn.xml")
	require.NoError(t, err)
	_, _ = w.Write([]byte("<conn><password>Pa55word!</password></conn>"))
	require.NoError(t, zw.Close())

	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"Orders": {Type: "Integration", DesignVersion: "1.0.0", Package: "Sales", Name: "Orders", Zip: flowZip(t, "Orders", "v1"),
			Parameters: map[string]string{"Receiver_Password": "Welcome1", "Receiver_Credential": "ERP_Basic", "Receiver_URL": "https://erp/api"}},
	})
	mock.Packages = []cpitest.Package{{ID: "Sales"}}
	mock.PDStrings = map[string]string{"ACME/Password": "Hunter2xyz", "ACME/CredentialName": "ACME_Basic", "ACME/Region": "eu10"}
	mock.PDBinaries = map[string]cpitest.PDBinary{"ACME/bundle": {ContentType: "zip", Content: zipped.Bytes()}}

	r := runMain(t, append([]string{"secret-scan", "--output", "json"}, basicAuth(mock)...)...)
	assert.Equal(t, 5, r.code, r.stderr)
	for _, s := range []string{`"pid": "ACME"`, `"key": "Password"`, `"entry": "conn.xml"`, `"artifact": "Orders"`, `"key": "Receiver_Password"`,
		`"stringParameters": 3`, `"binaryParameters": 1`, `"configurationParameters": 3`} {
		assert.Contains(t, r.stdout, s)
	}
	for _, secret := range []string{"Hunter2xyz", "Welcome1", "Pa55word"} {
		assert.NotContains(t, r.stdout+r.stderr, secret, "values are never reported")
	}
	assert.NotContains(t, r.stdout, "CredentialName")
	assert.NotContains(t, r.stdout, "Receiver_Credential")

	r = runMain(t, append([]string{"secret-scan", "--only", "configuration", "--packages", "Other", "--output", "json"}, basicAuth(mock)...)...)
	assert.Equal(t, 0, r.code, r.stderr)

	r = runMain(t, "secret-scan", "--only", "nothing")
	assert.Equal(t, 2, r.code)
}
