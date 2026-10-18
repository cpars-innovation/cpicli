package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// discover --dir works offline: no tenant settings needed.
func TestDiscoverDirOffline(t *testing.T) {
	dir := t.TempDir()
	flow := filepath.Join(dir, "Pkg", "Flow_A")
	require.NoError(t, os.MkdirAll(filepath.Join(flow, "META-INF"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(flow, "src/main/resources/scenarioflows/integrationflow"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(flow, "META-INF/MANIFEST.MF"), []byte("Bundle-SymbolicName: Flow_A\nSAP-BundleType: IntegrationFlow\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(flow, "src/main/resources/scenarioflows/integrationflow/Flow_A.iflw"), []byte("<definitions/>"), 0o644))
	out := filepath.Join(dir, "out", "discovery.json")

	r := runMain(t, "discover", "--dir", dir, "--output-file", out, "--output", "json")
	require.Equal(t, 0, r.code, r.stderr)
	var env struct {
		Ok     bool
		Result struct{ File string }
	}
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &env))
	assert.True(t, env.Ok)
	assert.Equal(t, out, env.Result.File)
	_, err := os.Stat(out)
	require.NoError(t, err)

	// without --dir a tenant is required
	r = runMain(t, "discover", "--output-file", out)
	assert.Equal(t, 2, r.code)
	assert.Contains(t, r.stderr, "no tenant configured")

	// graph.json is written next to it and queried offline
	graph := filepath.Join(dir, "out", "graph.json")
	require.FileExists(t, graph)
	r = runMain(t, "graph", "neighbors", "Flow_A", "--file", graph, "--output", "json")
	require.Equal(t, 0, r.code, r.stderr)
	var sub struct {
		Result struct {
			Edges []struct{ From, To, Type string }
		}
	}
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &sub))
	require.Len(t, sub.Result.Edges, 1)
	assert.Equal(t, "package:Pkg", sub.Result.Edges[0].From)
	r = runMain(t, "graph", "search", "missing", "--file", filepath.Join(dir, "nothing", "graph.json"))
	assert.Equal(t, 2, r.code)
	assert.Contains(t, r.stderr, "run discover")

	// graph build from an existing discovery file
	require.NoError(t, os.Remove(graph))
	r = runMain(t, "graph", "build", "--discovery-file", out)
	require.Equal(t, 0, r.code, r.stderr)
	require.FileExists(t, graph)
}

func TestPackagesCreateAndSend(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"Orders": {Type: "Integration", DesignVersion: "1"}})
	host, port := mock.HostPort()
	mock.Artifacts["Orders"].EndpointURL = fmt.Sprintf("http://%s:%d/http/orders", host, port)
	mock.Inbound = map[string]*cpitest.Inbound{"/http/orders": {MessageGuid: "G1", Status: 500}}

	_, _, err := runCLI(t, mock, "packages", "create", "--package-id", "Orders")
	require.NoError(t, err)
	assert.Equal(t, 1, mock.Count("POST /api/v1/IntegrationPackages"))

	r := runMain(t, append([]string{"send", "--artifact-id", "Orders", "--body", "x", "--header", "X-A=1", "--output", "json"}, basicAuth(mock)...)...)
	var env struct {
		ExitCode int
		Result   struct{ MessageGuid string }
	}
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &env))
	assert.Equal(t, 5, r.code)
	assert.Equal(t, 5, env.ExitCode)
	assert.Equal(t, "G1", env.Result.MessageGuid)
	require.Len(t, mock.Received, 1)
	assert.Equal(t, "1", mock.Received[0].Header.Get("X-A"))
	// without runtime credentials the API credentials are used
	assert.Contains(t, mock.Received[0].Header.Get("Authorization"), "Basic ")
}

// Tool filter settings are validated before the server starts, from flags
// and from the environment.
func TestMCPToolFilterSettings(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	r := runMain(t, append([]string{"mcp", "--tools", "deplyo"}, basicAuth(mock)...)...)
	assert.Equal(t, 2, r.code)
	assert.Contains(t, r.stderr, "matches no tool")
	r = runMain(t, append([]string{"mcp", "--mode", "admin"}, basicAuth(mock)...)...)
	assert.Equal(t, 2, r.code)
	assert.Contains(t, r.stderr, "invalid mode")

	// runMain clears CPICTL_* variables, so call Run directly
	cpitest.SetHome(t, t.TempDir())
	t.Setenv("CPICTL_DISABLE_TOOLS", "undeploy,nope_*")
	viper.Reset()
	t.Cleanup(viper.Reset)
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), append([]string{"mcp"}, basicAuth(mock)...), &stdout, &stderr, "test", "test")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "nope_*")
}

// iflow copy --from-dir works offline.
func TestIFlowCopyOffline(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Templates", "Tpl")
	model := `<definitions><collaboration><messageFlow><extensionElements>` +
		`<property><key>ComponentType</key><value>ProcessDirect</value></property>` +
		`<property><key>address</key><value>/tpl</value></property>` +
		`<property><key>direction</key><value>Sender</value></property>` +
		`</extensionElements></messageFlow></collaboration></definitions>`
	for name, content := range map[string]string{
		"META-INF/MANIFEST.MF": "Bundle-SymbolicName: Tpl\nBundle-Name: Tpl\nSAP-BundleType: IntegrationFlow\n",
		"src/main/resources/scenarioflows/integrationflow/Tpl.iflw": model,
	} {
		p := filepath.Join(src, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}

	r := runMain(t, "iflow", "copy", "--from-dir", src, "--id", "New_Flow", "--name", "New flow", "--output", "json")
	assert.Equal(t, 2, r.code, "the ProcessDirect address must change")
	assert.Contains(t, r.stderr, "/tpl")

	r = runMain(t, "iflow", "copy", "--from-dir", src, "--id", "New_Flow", "--name", "New flow", "--address", "/new", "--output", "json")
	require.Equal(t, 0, r.code, r.stderr)
	var env struct {
		Result struct {
			Dir       string
			Addresses []struct{ Old, New, Where string }
		}
	}
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &env))
	assert.Equal(t, filepath.Join(dir, "Templates", "New_Flow"), env.Result.Dir, "default: next to the source")
	require.Len(t, env.Result.Addresses, 1)
	assert.Equal(t, "/new", env.Result.Addresses[0].New)
	assert.FileExists(t, filepath.Join(dir, "Templates", "New_Flow", "src/main/resources/scenarioflows/integrationflow/New_Flow.iflw"))

	// --from needs a tenant
	r = runMain(t, "iflow", "copy", "--from", "Tpl", "--id", "X")
	assert.Equal(t, 2, r.code)
	assert.Contains(t, r.stderr, "no tenant configured")
}
