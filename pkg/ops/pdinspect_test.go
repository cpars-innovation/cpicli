package ops

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func inspect(t *testing.T, data []byte, ct string, o PDInspectOptions) *PDInspection {
	t.Helper()
	in, err := InspectPDBinary(data, ct, o)
	require.NoError(t, err)
	return in
}

func TestInspectPDBinaryText(t *testing.T) {
	cases := []struct{ data, declared, detected string }{
		{`<?xml version="1.0"?><a/>`, "xml", "xml"},
		{"\xef\xbb\xbf<xs:schema xmlns:xs=\"http://www.w3.org/2001/XMLSchema\"/>", "xsd", "xsd"},
		{`<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="1.0"/>`, "xml", "xsl"},
		{` {"a": [1, 2]}`, "json", "json"},
		{"x^ plain text", "txt", "txt"},
		{`{"a": 1}`, "txt", "json"},
	}
	for _, c := range cases {
		in := inspect(t, []byte(c.data), c.declared, PDInspectOptions{})
		assert.Equal(t, c.detected, in.DetectedType, c.data)
		assert.False(t, in.Mismatch, c.data)
		assert.NotEmpty(t, in.Text, c.data)
		assert.Equal(t, int64(len(c.data)), in.Size)
		assert.Equal(t, sha256Hex([]byte(c.data)), in.SHA256)
	}
	in := inspect(t, []byte(`{"a":1}`), "application/xml; charset=UTF-8", PDInspectOptions{})
	assert.Equal(t, "xml", in.DeclaredType)
	assert.True(t, in.Mismatch, "json declared as xml")

	in = inspect(t, []byte(strings.Repeat("ä", 10)), "txt", PDInspectOptions{TextBytes: 5})
	assert.Equal(t, "ää", in.Text, "cut on a rune boundary")
	assert.True(t, in.TextTruncated)

	in = inspect(t, []byte{0x00, 0x01, 0xff, 0xfe}, "txt", PDInspectOptions{})
	assert.Equal(t, "binary", in.DetectedType)
	assert.True(t, in.Mismatch)
	assert.Empty(t, in.Text)
}

func TestInspectPDBinaryCompressed(t *testing.T) {
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	w.Write([]byte("<a>gz</a>"))
	w.Close()
	in := inspect(t, gz.Bytes(), "gz", PDInspectOptions{})
	assert.Equal(t, "gz", in.DetectedType)
	require.NotNil(t, in.Inner)
	assert.Equal(t, "xml", in.Inner.DetectedType)
	assert.Equal(t, "<a>gz</a>", in.Inner.Text)

	var zl bytes.Buffer
	zw := zlib.NewWriter(&zl)
	zw.Write([]byte(strings.Repeat("0", 1<<20))) // 1 MiB of zeros: a small bomb
	zw.Close()
	in = inspect(t, zl.Bytes(), "zlib", PDInspectOptions{MaxBytes: 1000})
	assert.Equal(t, "zlib", in.DetectedType)
	require.NotNil(t, in.Inner)
	assert.Equal(t, int64(1000), in.Inner.Size)
	assert.True(t, in.Inner.Truncated)
	assert.Len(t, in.Warnings, 1)

	// gz declared as zip
	in = inspect(t, gz.Bytes(), "zip", PDInspectOptions{})
	assert.True(t, in.Mismatch)

	// nesting is bounded
	inner := gz.Bytes()
	for range 3 {
		var b bytes.Buffer
		w := gzip.NewWriter(&b)
		w.Write(inner)
		w.Close()
		inner = b.Bytes()
	}
	in = inspect(t, inner, "gz", PDInspectOptions{MaxDepth: 2})
	require.NotNil(t, in.Inner)
	assert.Nil(t, in.Inner.Inner)
	assert.Contains(t, in.Inner.Warnings[0], "nested deeper")
}

func TestInspectPDBinaryZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"a.xml", "dir/", "dir/b.json", "c.txt"} {
		f, _ := zw.Create(name)
		if !strings.HasSuffix(name, "/") {
			f.Write([]byte(map[string]string{"a.xml": "<a/>", "dir/b.json": `{"b":1}`, "c.txt": "hello"}[name]))
		}
	}
	zw.Close()
	data := buf.Bytes()

	in := inspect(t, data, "zip", PDInspectOptions{})
	assert.Equal(t, "zip", in.DetectedType)
	require.Len(t, in.Entries, 4)
	assert.Equal(t, "a.xml", in.Entries[0].Name)
	assert.Equal(t, int64(4), in.Entries[0].Size)
	assert.True(t, in.Entries[1].Dir)
	assert.Nil(t, in.Entry)

	in = inspect(t, data, "zip", PDInspectOptions{ZipEntry: "dir/b.json", MaxEntries: 2})
	assert.Len(t, in.Entries, 2)
	assert.True(t, in.EntriesTruncated)
	require.NotNil(t, in.Entry)
	assert.Equal(t, "json", in.Entry.DeclaredType, "from the extension")
	assert.Equal(t, "json", in.Entry.DetectedType)
	assert.Equal(t, `{"b":1}`, in.Entry.Text)

	in = inspect(t, data, "zip", PDInspectOptions{ZipEntry: "c.txt", MaxBytes: 3})
	assert.Equal(t, "hel", in.Entry.Text)
	assert.True(t, in.Entry.Truncated)

	in = inspect(t, data, "zip", PDInspectOptions{ZipEntry: "missing"})
	assert.Nil(t, in.Entry)
	assert.Equal(t, []string{"no zip entry missing"}, in.Warnings)

	in = inspect(t, data[:len(data)-10], "zip", PDInspectOptions{})
	assert.Equal(t, "zip", in.DetectedType)
	assert.Contains(t, in.Warnings[0], "not a readable zip")
}

func testCert(t *testing.T, cn string, notAfter time.Time, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(0xBEEF), Subject: pkix.Name{CommonName: cn},
		NotBefore: notAfter.AddDate(-1, 0, 0), NotAfter: notAfter, DNSNames: []string{cn + ".example.com"},
		IsCA: parent == nil, BasicConstraintsValid: true}
	if parent == nil {
		parent, parentKey = tmpl, key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, parentKey)
	require.NoError(t, err)
	c, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return c, key, der
}

func TestInspectPDBinaryCertificate(t *testing.T) {
	now := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	defer func(f func() time.Time) { pdNow = f }(pdNow)
	pdNow = func() time.Time { return now }

	ca, caKey, caDER := testCert(t, "ca", now.AddDate(5, 0, 0), nil, nil)
	_, _, leafDER := testCert(t, "partner", now.AddDate(0, 0, 20), ca, caKey)

	in := inspect(t, leafDER, "crt", PDInspectOptions{})
	assert.Equal(t, "crt", in.DetectedType, "DER")
	assert.False(t, in.Mismatch)
	require.Len(t, in.Certificates, 1)
	c := in.Certificates[0]
	assert.Equal(t, "CN=partner", c.Subject)
	assert.Equal(t, "CN=ca", c.Issuer)
	assert.Equal(t, "BEEF", c.SerialNumber)
	assert.Equal(t, 20, c.DaysLeft)
	assert.False(t, c.Expired)
	assert.False(t, c.SelfSigned)
	assert.Equal(t, "ECDSA", c.KeyAlgorithm)
	assert.Equal(t, 256, c.KeyBits)
	assert.Equal(t, []string{"partner.example.com"}, c.DNSNames)
	assert.Len(t, c.SHA256, 64)
	assert.Len(t, c.SHA1, 40)

	chain := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})...)
	in = inspect(t, chain, "crt", PDInspectOptions{})
	assert.Equal(t, "crt", in.DetectedType, "PEM")
	require.Len(t, in.Certificates, 2)
	assert.True(t, in.Certificates[1].IsCA)
	assert.True(t, in.Certificates[1].SelfSigned)

	pdNow = func() time.Time { return now.AddDate(0, 1, 0) }
	in = inspect(t, leafDER, "crt", PDInspectOptions{})
	assert.True(t, in.Certificates[0].Expired)
	assert.Negative(t, in.Certificates[0].DaysLeft)

	in = inspect(t, []byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"), "crt", PDInspectOptions{})
	assert.Equal(t, "crt", in.DetectedType)
	assert.Contains(t, in.Warnings[0], "certificate not readable")
}
