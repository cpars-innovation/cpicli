package pdaddress

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
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

// Reader reads the active archive and the configuration through a Fetch, so
// the gateway can set Guard.ProcessDirectAddresses without the API client.
func TestReader(t *testing.T) {
	zipped := archive(t, sender("ProcessDirect", "/sd/in")+sender("ProcessDirect", "{{target}}")+sender("HTTPS", "/http/x")+sender("ProcessDirect", "&amp;x"))
	var paths []string
	fetch := func(_ context.Context, p string) ([]byte, error) {
		paths = append(paths, p)
		switch p {
		case "/api/v1/IntegrationDesigntimeArtifacts(Id='SD_A',Version='active')/$value":
			return zipped, nil
		case "/api/v1/IntegrationDesigntimeArtifacts(Id='SD_A',Version='active')/Configurations":
			return []byte(`{"d":{"results":[{"ParameterKey":"target","ParameterValue":"/sd/configured"}]}}`), nil
		}
		return nil, errors.New("HTTP 404")
	}
	got, err := Reader(fetch)(context.Background(), "SD_A")
	require.NoError(t, err)
	assert.Equal(t, []string{"/sd/in", "/sd/configured", "&x"}, got)
	assert.Len(t, paths, 2)

	_, err = Reader(fetch)(context.Background(), "O'Brien")
	assert.Error(t, err)
	assert.Equal(t, "/api/v1/IntegrationDesigntimeArtifacts(Id='O%27%27Brien',Version='active')/$value", paths[2])
}

// The package is for embedders of pkg/scope: the standard library only.
func TestDependencies(t *testing.T) {
	depcheck.Allow(t, ".", "github.com/cpars-innovation/cpicli/pkg/pdaddress")
}
