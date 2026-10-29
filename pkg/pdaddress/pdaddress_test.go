package pdaddress

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/depcheck"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sender(adapter, addr string) string {
	return `<bpmn2:messageFlow id="MessageFlow_1"><bpmn2:extensionElements>
<ifl:property><key>ComponentType</key><value>` + adapter + `</value></ifl:property>
<ifl:property><key>direction</key><value>Sender</value></ifl:property>
<ifl:property><key>address</key><value>` + addr + `</value></ifl:property>
</bpmn2:extensionElements></bpmn2:messageFlow>`
}

func archive(t *testing.T, model string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, err := zw.Create("src/main/resources/scenarioflows/integrationflow/F.iflw")
	require.NoError(t, err)
	_, _ = w.Write([]byte(model))
	require.NoError(t, zw.Close())
	return b.Bytes()
}

// Reader reads the active archive through a Fetch, so the gateway can set
// Guard.ProcessDirectAddresses without the API client. Addresses set by a
// parameter are left out: the running flow may use another value (#65).
func TestReader(t *testing.T) {
	zipped := archive(t, sender("ProcessDirect", "/sd/in")+sender("ProcessDirect", "{{target}}")+sender("HTTPS", "/http/x")+
		sender("ProcessDirect", "&amp;x")+sender("ProcessDirect", "/sd/{{part}}"))
	var paths []string
	fetch := func(_ context.Context, p string) ([]byte, error) {
		paths = append(paths, p)
		if p == "/api/v1/IntegrationDesigntimeArtifacts(Id='SD_A',Version='active')/$value" {
			return zipped, nil
		}
		return nil, errors.New("HTTP 404")
	}
	got, err := Reader(fetch)(context.Background(), "SD_A")
	require.NoError(t, err)
	assert.Equal(t, []string{"/sd/in", "&x"}, got)
	assert.Len(t, paths, 1, "no configuration read")

	_, err = Reader(fetch)(context.Background(), "O'Brien")
	assert.Error(t, err)
	assert.Equal(t, "/api/v1/IntegrationDesigntimeArtifacts(Id='O%27%27Brien',Version='active')/$value", paths[1])
}

// The model is read as XML: an address in a comment is no address, and a
// sender whose address is repeated or ambiguous is refused (#65).
func TestProcessDirectSendersAsXML(t *testing.T) {
	model := func(body string) []byte {
		return []byte(`<?xml version="1.0"?><bpmn2:definitions xmlns:bpmn2="b" xmlns:ifl="i">` + body + `</bpmn2:definitions>`)
	}
	got, err := ProcessDirectSenders(model(`<!-- ` + sender("ProcessDirect", "/fi/other") + ` -->` + sender("ProcessDirect", "/sd/in")))
	require.NoError(t, err)
	assert.Equal(t, []string{"/sd/in"}, got)

	twice := strings.Replace(sender("ProcessDirect", "/sd/in"), "</bpmn2:extensionElements>",
		`<ifl:property><key>address</key><value>/fi/other</value></ifl:property></bpmn2:extensionElements>`, 1)
	_, err = ProcessDirectSenders(model(twice))
	assert.ErrorContains(t, err, "repeated")

	two := strings.Replace(sender("ProcessDirect", "/sd/in"), "</bpmn2:extensionElements>",
		`<ifl:property><key>path</key><value>/fi/other</value></ifl:property></bpmn2:extensionElements>`, 1)
	_, err = ProcessDirectSenders(model(two))
	assert.ErrorContains(t, err, "address properties")

	_, err = ProcessDirectSenders(model(`<bpmn2:messageFlow>`))
	assert.Error(t, err, "not well-formed")
}

// The package is for embedders of pkg/scope: the standard library only.
func TestDependencies(t *testing.T) {
	depcheck.Allow(t, ".", "github.com/cpars-innovation/cpicli/pkg/pdaddress")
}
