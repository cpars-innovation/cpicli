package conformance_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/conformance"
	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockAnswers are the mock's answers to the probes: what the access scope
// assumes of a runtime, until a tenant run records the real answers in
// testdata/tenant.json.
var mockAnswers = map[string]bool{
	"case":                  false,
	"encoded":               true,
	"param":                 true,
	"odata-case":            false,
	"odata-zero":            false,
	"odata-extra":           false,
	"modified-on-configure": true,
}

// The probes run against the mock tenant: it answers like the tenant (the
// recorded answers, where there are some) and as the guard assumes
// (cpicli#59).
func TestMockConformance(t *testing.T) {
	m := cpitest.NewTenant(t, nil)
	m.Live = true
	require.NoError(t, cpitest.SeedDemo(m, "dev", time.Now()))
	exe := m.Executer()
	facts, err := conformance.Run(context.Background(), conformance.Target{
		API: exe,
		Send: func(_ context.Context, method, path string) (int, error) {
			resp, err := exe.Exec(method, path, http.NoBody, nil)
			if err != nil {
				return 0, err
			}
			_, _ = exe.ReadRespBody(resp)
			return resp.StatusCode, nil
		},
		HTTPFlow: "Orders_In", ODataFlow: "Orders_OData_Service", ConfigKey: "ERP_IDOC_URL",
	})
	require.NoError(t, err)
	require.Len(t, facts, len(mockAnswers))

	want := mockAnswers
	if b, err := os.ReadFile("testdata/tenant.json"); err == nil {
		var rec struct{ Facts []conformance.Fact }
		require.NoError(t, json.Unmarshal(b, &rec))
		want = map[string]bool{}
		for _, f := range rec.Facts {
			want[f.ID] = f.Got
		}
	}
	for _, f := range facts {
		assert.True(t, f.Holds(), "%s: %s", f.ID, f.Note)
		if w, ok := want[f.ID]; ok {
			assert.Equal(t, w, f.Got, "the mock answers %q differently from the tenant: %s", f.ID, f.Text)
		}
	}
}
