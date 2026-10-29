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

// model wraps message flows into a model with the real namespaces.
func model(flows string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><bpmn2:definitions xmlns:bpmn2="http://www.omg.org/spec/BPMN/20100524/MODEL" ` +
		`xmlns:ifl="http:///com.sap.ifl.model/Ifl.xsd"><bpmn2:collaboration>` + flows + `</bpmn2:collaboration></bpmn2:definitions>`
}

func archive(t *testing.T, flows string) []byte {
	t.Helper()
	return zipOf(t, map[string]string{"src/main/resources/scenarioflows/integrationflow/F.iflw": model(flows)})
}

func zipOf(t *testing.T, files map[string]string, extra ...string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for name, content := range files {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, _ = w.Write([]byte(content))
	}
	for _, name := range extra { // a repeated entry
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, _ = w.Write([]byte(model(sender("ProcessDirect", "/fi/repeated"))))
	}
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
	read := func(m string) ([]string, error) { return ProcessDirectSenders([]byte(m)) }
	got, err := read(model(`<!-- ` + sender("ProcessDirect", "/fi/other") + ` -->` + sender("ProcessDirect", "/sd/in")))
	require.NoError(t, err)
	assert.Equal(t, []string{"/sd/in"}, got)

	twice := strings.Replace(sender("ProcessDirect", "/sd/in"), "</bpmn2:extensionElements>",
		`<ifl:property><key>address</key><value>/fi/other</value></ifl:property></bpmn2:extensionElements>`, 1)
	_, err = read(model(twice))
	assert.ErrorContains(t, err, "repeated")

	two := strings.Replace(sender("ProcessDirect", "/sd/in"), "</bpmn2:extensionElements>",
		`<ifl:property><key>path</key><value>/fi/other</value></ifl:property></bpmn2:extensionElements>`, 1)
	_, err = read(model(two))
	assert.ErrorContains(t, err, "address properties")

	_, err = read(model(`<bpmn2:messageFlow>`))
	assert.Error(t, err, "not well-formed")

	_, err = read(model(strings.Replace(sender("ProcessDirect", "/sd/in"), "<value>/sd/in</value>", "<value>/sd/<b/>in</value>", 1)))
	assert.ErrorContains(t, err, "markup")
	_, err = read(model(strings.Replace(sender("ProcessDirect", "/sd/in"), "<value>/sd/in</value>", "<value>/sd/<!-- x -->in</value>", 1)))
	assert.ErrorContains(t, err, "markup")
}

// Only properties at their place, in the model's namespaces, count (#65,
// re-check): a message flow in another namespace, outside the
// collaboration, or a property under another element is no sender.
func TestProcessDirectSendersOnlyAtTheirPlace(t *testing.T) {
	read := func(m string) []string {
		got, err := ProcessDirectSenders([]byte(m))
		require.NoError(t, err)
		return got
	}
	foreign := `<x:messageFlow xmlns:x="urn:other"><bpmn2:extensionElements><ifl:property><key>ComponentType</key><value>ProcessDirect</value></ifl:property>` +
		`<ifl:property><key>direction</key><value>Sender</value></ifl:property><ifl:property><key>address</key><value>/fi/x</value></ifl:property>` +
		`</bpmn2:extensionElements></x:messageFlow>`
	assert.Empty(t, read(model(foreign)))
	outside := `<?xml version="1.0"?><bpmn2:definitions xmlns:bpmn2="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:ifl="http:///com.sap.ifl.model/Ifl.xsd">` +
		sender("ProcessDirect", "/fi/outside") + `<bpmn2:collaboration>` + sender("ProcessDirect", "/sd/in") + `</bpmn2:collaboration></bpmn2:definitions>`
	assert.Equal(t, []string{"/sd/in"}, read(outside))
	spoofedIfl := strings.ReplaceAll(sender("ProcessDirect", "/fi/spoof"), "ifl:property", "f:property")
	spoofedIfl = strings.Replace(spoofedIfl, "<bpmn2:messageFlow ", `<bpmn2:messageFlow xmlns:f="urn:fake" `, 1)
	assert.Empty(t, read(model(spoofedIfl)))
}

// Only the flow's model counts: a decoy model elsewhere in the archive, two
// models, a repeated entry or no model are refused or ignored (#65, re-check).
func TestFromArchiveReadsOnlyTheModel(t *testing.T) {
	const dir = "src/main/resources/scenarioflows/integrationflow/"
	got, err := FromArchive(zipOf(t, map[string]string{
		dir + "F.iflw":                  model(sender("ProcessDirect", "/sd/in")),
		"src/main/resources/decoy.iflw": model(sender("ProcessDirect", "/fi/decoy")),
		dir + "sub/G.iflw":              model(sender("ProcessDirect", "/fi/sub")),
	}))
	require.NoError(t, err)
	assert.Equal(t, []string{"/sd/in"}, got)

	_, err = FromArchive(zipOf(t, map[string]string{dir + "F.iflw": model(""), dir + "G.iflw": model("")}))
	assert.ErrorContains(t, err, "more than one model")
	_, err = FromArchive(zipOf(t, map[string]string{dir + "F.iflw": model(sender("ProcessDirect", "/sd/in"))}, dir+"F.iflw"))
	assert.ErrorContains(t, err, "repeated")
	_, err = FromArchive(zipOf(t, map[string]string{"src/main/resources/x.iflw": model(sender("ProcessDirect", "/fi/x"))}))
	assert.ErrorContains(t, err, "without a model")
}

// The package is for embedders of pkg/scope: the standard library only.
func TestDependencies(t *testing.T) {
	depcheck.Allow(t, ".", "github.com/cpars-innovation/cpicli/pkg/pdaddress")
}
