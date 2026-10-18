package cpitest_test

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Partner Directory parameters carry who created and last changed them and
// when, like a real tenant, and $select returns only the selected
// properties (#34).
func TestPDChangeMetadataAndSelect(t *testing.T) {
	m := cpitest.NewTenant(t, nil)
	m.PDStrings = map[string]string{"Seeded/Url": "https://x"}
	exe := m.Executer()
	post := func(body string) int {
		resp, err := exe.Exec(http.MethodPost, "/api/v1/StringParameters", bytes.NewBufferString(body), map[string]string{"Content-Type": "application/json"})
		require.NoError(t, err)
		resp.Body.Close()
		return resp.StatusCode
	}
	require.Equal(t, http.StatusCreated, post(`{"Pid":"ACME","Id":"Mode","Value":"test"}`))

	type row map[string]string
	var res struct {
		D struct{ Results []row } `json:"d"`
	}
	require.Equal(t, http.StatusOK, get(t, exe, "/api/v1/StringParameters", &res))
	require.Len(t, res.D.Results, 2)
	created := res.D.Results[0]
	assert.Equal(t, "ACME", created["Pid"])
	assert.NotEmpty(t, created["LastModifiedBy"])
	assert.Regexp(t, `^/Date\(\d+\)/$`, created["LastModifiedTime"])
	assert.Equal(t, created["CreatedTime"], created["LastModifiedTime"])
	assert.Equal(t, "mock-seed", res.D.Results[1]["LastModifiedBy"], "seeded parameters report the seed user")

	res.D.Results = nil
	require.Equal(t, http.StatusOK, get(t, exe, "/api/v1/StringParameters?$select=Pid,Id,LastModifiedTime", &res))
	assert.Equal(t, row{"Pid": "ACME", "Id": "Mode", "LastModifiedTime": created["LastModifiedTime"]}, res.D.Results[0])
}
