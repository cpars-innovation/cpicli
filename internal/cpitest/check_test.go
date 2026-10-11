package cpitest

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The demo flows pass the Check (they pass it on a real tenant too).
func TestCheckDemoFlowsPass(t *testing.T) {
	for _, tier := range DemoTiers {
		m := NewTenant(t, nil)
		require.NoError(t, SeedDemo(m, tier, time.Now()))
		n := 0
		for id, a := range m.Artifacts {
			if a.Type != "Integration" || len(a.Zip) == 0 {
				continue
			}
			c, err := readArtifactZip(a.Zip)
			require.NoError(t, err)
			assert.Empty(t, checkFlow(c), "%s on %s", id, tier)
			n++
		}
		assert.Positive(t, n)
	}
}

// rewriteZip replaces text in the .iflw of an archive and drops files.
func rewriteZip(t *testing.T, data []byte, replace map[string]string, drop string) []byte {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		if drop != "" && strings.HasSuffix(f.Name, drop) {
			continue
		}
		rc, err := f.Open()
		require.NoError(t, err)
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		if strings.HasSuffix(f.Name, ".iflw") {
			s := string(b)
			for old, nw := range replace {
				require.Contains(t, s, old)
				s = strings.ReplaceAll(s, old, nw)
			}
			b = []byte(s)
		}
		w, err := zw.Create(f.Name)
		require.NoError(t, err)
		_, _ = w.Write(b)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestCheckFindsStructuralProblems(t *testing.T) {
	m := NewTenant(t, nil)
	require.NoError(t, SeedDemo(m, "dev", time.Now()))

	notify := rewriteZip(t, m.Artifacts["Partner_Notify"].Zip, map[string]string{
		"<key>host</key><value>sftp.partner.example.com</value>": "<key>host</key><value></value>",
		"<key>credential_name</key><value>Partner_SFTP</value>":  "<key>credential_name</key><value></value>",
	}, "")
	c, err := readArtifactZip(notify)
	require.NoError(t, err)
	issues := checkFlow(c)
	require.Len(t, issues, 2)
	assert.Equal(t, "Enter a value for the 'host' attribute", issues[0].Message)
	assert.Equal(t, "ComponentMetadataEditor/eid:MessageFlow_R0/etype:MessageFlow/location:SFTP/pid:host", issues[0].SourceObject)
	assert.Equal(t, "src/main/resources/scenarioflows/integrationflow", issues[0].ResourcePath)
	assert.Equal(t, "Partner_Notify.iflw", issues[0].ResourceName)
	assert.Equal(t, "Enter a value for the 'Credential Name' attribute", issues[1].Message)

	post := rewriteZip(t, m.Artifacts["Billing_Post"].Zip, nil, "script/v2/toInvoice.groovy")
	c, err = readArtifactZip(post)
	require.NoError(t, err)
	issues = checkFlow(c)
	require.Len(t, issues, 1)
	assert.Equal(t, "The script file 'v2/toInvoice.groovy' does not exist", issues[0].Message)

	text := checkResult(issues)
	assert.True(t, strings.HasPrefix(text, "Check execution result: Failed\n[\n  {\n    \"severity\": \"Error\","), text)
	assert.Equal(t, "Check execution result: Passed", checkResult(nil))
}
