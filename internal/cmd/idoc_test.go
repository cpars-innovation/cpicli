package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// idoc template writes a skeleton that send --idoc accepts; the demo's IDoc
// and OData flows take test messages from the CLI.
func TestIDocTemplateAndSAPSend(t *testing.T) {
	file := filepath.Join(t.TempDir(), "orders.xml")
	r := runMain(t, "idoc", "template", "--type", "ORDERS05", "--mestyp", "ORDERS", "--sndprn", "S4HCLNT100", "--rcvprn", "CPI",
		"--docnum", "0000000000000042", "--out", file)
	require.Equal(t, 0, r.code, r.stderr)
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Contains(t, string(data), "<EDI_DC40 SEGMENT=\"1\">")
	assert.Contains(t, string(data), "<SNDPRN>S4HCLNT100</SNDPRN>")

	r = runMain(t, "idoc", "template", "--type", "ORDERS05", "--mestyp", "ORDERS")
	require.Equal(t, 0, r.code, r.stderr)
	assert.Contains(t, r.stdout, "<IDOCTYP>ORDERS05</IDOCTYP>", "stdout without --out")
	assert.Equal(t, 2, runMain(t, "idoc", "template", "--type", "ORDERS05").code, "--mestyp is required")

	mock := cpitest.NewTenant(t, nil)
	require.NoError(t, cpitest.SeedDemo(mock, "dev", time.Now()))
	r = runMain(t, append([]string{"send", "--artifact-id", "Orders_IDoc_In", "--idoc", file, "--output", "json"}, basicAuth(mock)...)...)
	require.Equal(t, 0, r.code, r.stdout+r.stderr)
	var env struct {
		Result struct {
			HTTPStatus int
			IDocs      []struct{ DOCNUM, IDOCTYP string }
		}
	}
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &env), r.stdout)
	assert.Equal(t, 200, env.Result.HTTPStatus)
	require.Len(t, env.Result.IDocs, 1)
	assert.Equal(t, "0000000000000042", env.Result.IDocs[0].DOCNUM)
	got := mock.Received[len(mock.Received)-1]
	assert.Equal(t, "/cxf/orders/idoc", got.Path)
	assert.Equal(t, "ORDERS05", got.Header.Get("SapIDocType"))

	bad := filepath.Join(t.TempDir(), "bad.xml")
	require.NoError(t, os.WriteFile(bad, []byte("<Order/>"), 0o644))
	r = runMain(t, append([]string{"send", "--artifact-id", "Orders_IDoc_In", "--idoc", bad}, basicAuth(mock)...)...)
	assert.Equal(t, 2, r.code)
	assert.Contains(t, r.stdout+r.stderr, "EDI_DC40")

	r = runMain(t, append([]string{"send", "--artifact-id", "Orders_OData_Service", "--odata-method", "POST", "--entity-set", "SalesOrders",
		"--body", `{"OrderNo":"4711"}`, "--output", "json"}, basicAuth(mock)...)...)
	require.Equal(t, 0, r.code, r.stdout+r.stderr)
	got = mock.Received[len(mock.Received)-1]
	assert.Equal(t, "/gw/odata/Demo/Orders;v=1/SalesOrders", got.Path)
	assert.Equal(t, "application/json", got.Header.Get("Content-Type"))

	r = runMain(t, append([]string{"send", "--artifact-id", "Orders_OData_Service", "--entity-set", "SalesOrders", "--key", "'4711'",
		"--query", "$select=OrderNo", "--output", "json"}, basicAuth(mock)...)...)
	require.Equal(t, 0, r.code, r.stdout+r.stderr)
	got = mock.Received[len(mock.Received)-1]
	assert.Equal(t, "GET", got.Method)
	assert.Equal(t, "/gw/odata/Demo/Orders;v=1/SalesOrders('4711')", got.Path)

	r = runMain(t, append([]string{"send", "--artifact-id", "Orders_OData_Service", "--entity-set", "SalesOrders", "--method", "GET"}, basicAuth(mock)...)...)
	assert.Equal(t, 2, r.code, "--method with OData flags")
}
