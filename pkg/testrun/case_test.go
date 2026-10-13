package testrun

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	zerolog.SetGlobalLevel(zerolog.Disabled) // deploys and sends log a lot
	os.Exit(m.Run())
}

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

func TestLoadFixtures(t *testing.T) {
	cases, err := Load(fixtures, Filter{})
	require.NoError(t, err)
	var ids []string
	for _, c := range cases {
		ids = append(ids, c.ID())
	}
	assert.Equal(t, []string{"Orders_IDoc_In/orders05", "Orders_In/later", "Orders_In/valid-order", "Orders_In/response-checks", "Orders_Route/via-harness"}, ids,
		"by flow, order, name")

	valid := cases[2]
	assert.Equal(t, "Orders_In", valid.Flow, "the folder name")
	assert.Equal(t, filepath.Join(fixtures, "Orders_In", "valid-order.yaml"), valid.File)
	assert.Equal(t, StringList{"<OrderNo>4500017001</OrderNo>"}, valid.Expect.ResponseContains)
	assert.Equal(t, map[string]string{"OrderNo": "4500017001"}, valid.Expect.CustomHeaders)
	assert.Equal(t, "valid-order.xml", valid.Request.BodyFile)

	checks := cases[3]
	assert.Equal(t, StringList{"<Order>"}, checks.Expect.ResponseContains, "a single string")
	assert.Equal(t, Duration(30*time.Second), checks.Request.Wait)
	assert.Equal(t, Duration(time.Minute), checks.Expect.MaxDuration)
	assert.Equal(t, 2, checks.Order)

	idoc := cases[0]
	assert.Equal(t, Duration(30*time.Second), idoc.Request.Wait, "seconds as a number")
	assert.Equal(t, map[string]string{"ORDER_API_URL": "https://orders-api-test.example.com/v1/orders"}, idoc.Config)
	assert.Nil(t, valid.Config)
	assert.Equal(t, "the partner sends JSON from November", cases[1].Skip)
}

func TestLoadFilters(t *testing.T) {
	ids := func(f Filter) []string {
		cases, err := Load(fixtures, f)
		require.NoError(t, err)
		out := []string{}
		for _, c := range cases {
			out = append(out, c.ID())
		}
		return out
	}
	assert.Equal(t, []string{"Orders_IDoc_In/orders05"}, ids(Filter{Flows: []string{"Orders_IDoc_In"}}))
	assert.Equal(t, []string{"Orders_IDoc_In/orders05", "Orders_In/later", "Orders_In/valid-order", "Orders_In/response-checks"}, ids(Filter{Flows: []string{"Orders_I*"}}))
	assert.Equal(t, []string{"Orders_In/valid-order"}, ids(Filter{Cases: []string{"valid-order"}}))
	assert.Equal(t, []string{"Orders_In/valid-order"}, ids(Filter{Cases: []string{"Orders_In/valid-*"}}))
	assert.Equal(t, []string{"Orders_In/valid-order", "Orders_In/response-checks"}, ids(Filter{Tags: []string{"smoke"}}))
	assert.Equal(t, []string{"Orders_IDoc_In/orders05", "Orders_In/response-checks"}, ids(Filter{Tags: []string{"xml", "idoc"}}))
	assert.Equal(t, []string{}, ids(Filter{Flows: []string{"Orders_In"}, Tags: []string{"idoc"}}))
}

func TestLoadStrict(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, want string }{
		"unknown key":       {"request:\n  body: x\nexpect:\n  http_statuss: 200\n", "c.yaml:4: field http_statuss not found in Expect"},
		"unknown top key":   {"descripton: x\n", "c.yaml:1: field descripton not found in Case"},
		"invalid yaml":      {"request:\n  body: [x\n", "c.yaml:"},
		"wrong type":        {"expect:\n  http_status: ok\n", "c.yaml:2: cannot unmarshal !!str `ok` into int"},
		"bad duration":      {"request:\n  wait: soon\n", `c.yaml:2: invalid duration "soon"`},
		"body and file":     {"request: {body: x, body_file: b.xml}\n", "use either body or body_file"},
		"missing body file": {"request: {body_file: nope.xml}\n", "nope.xml not found next to the case file"},
		"url and endpoint":  {"request: {url: https://a/x, endpoint: https://a/y}\n", "url and endpoint differ"},
		"bad status":        {"expect: {http_status: 42}\n", "not an HTTP status"},
		"negative count":    {"expect: {stub_calls: [{rule: a, count: -1}]}\n", "count must not be negative"},
		"stub unknown key":  {"expect: {stub_calls: [{rule: a, bodycontains: x}]}\n", "field bodycontains not found in StubMatcher"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "F/c.yaml", tc.yaml)
			_, err := Load(dir, Filter{})
			require.Error(t, err)
			assert.Equal(t, exitcode.Usage, output.ExitCode(err))
			assert.Contains(t, err.Error(), tc.want)
		})
	}

	dir := t.TempDir()
	write(t, dir, "F/config.yaml", "A: {nested: 1}\n")
	_, err := Load(dir, Filter{})
	assert.ErrorContains(t, err, "parameter values are scalars")

	_, err = Load(filepath.Join(t.TempDir(), "missing"), Filter{})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
	assert.ErrorContains(t, err, "does not exist")
}

func TestLoadEdgeCases(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "F/empty.yaml", "")
	write(t, dir, "F/flow.yaml", "flow: Other\nrequest: {endpoint: https://h/http/x}\n")
	write(t, dir, "F/config.yaml", "N: 5\nB: true\nE:\n")
	write(t, dir, ".hidden/x.yaml", "nope: 1\n")
	write(t, dir, "F/notes.txt", "ignored")
	cases, err := Load(dir, Filter{})
	require.NoError(t, err)
	require.Len(t, cases, 2)
	assert.Equal(t, "F", cases[0].Flow)
	assert.Equal(t, "Other", cases[1].Flow, "flow overrides the folder")
	assert.Equal(t, "https://h/http/x", cases[1].Request.url(), "endpoint is the old name of url")
	assert.Equal(t, map[string]string{"N": "5", "B": "true", "E": ""}, cases[0].Config)

	l := List(cases)
	assert.Equal(t, 2, l.Cases)
	require.Len(t, l.Flows, 2)
	assert.Equal(t, []string{"B", "E", "N"}, l.Flows[0].ConfigKeys)
	assert.Equal(t, "http", l.Flows[0].Cases[0].Trigger)
}

func TestTrigger(t *testing.T) {
	assert.Equal(t, "idoc", Request{IDocFile: "x"}.trigger())
	assert.Equal(t, "odata", Request{OData: &ODataRequest{}}.trigger())
	assert.Equal(t, "process_direct", Request{ProcessDirectAddress: "/x"}.trigger())
}
