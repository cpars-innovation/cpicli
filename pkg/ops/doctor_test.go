package ops

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctorProbesAndErrorText(t *testing.T) {
	var mu sync.Mutex // the probes run in parallel
	queries := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries[r.URL.Path] = r.URL.RawQuery
		mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/DataStores":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"Bad Request","message":{"lang":"en","value":"Data store service not enabled"}}}`))
		case "/api/v1/AuditLogs":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"Forbidden","message":{"lang":"en","value":null}}}`))
		case "/api/v1/LogFiles":
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = w.Write([]byte("Not supported\n"))
		default:
			_, _ = w.Write([]byte(`{"d":{"results":[]}}`))
		}
	}))
	defer srv.Close()
	host, port := httpclnt.GetHostPort(srv.URL)
	res := Doctor(context.Background(), httpclnt.New("", "", "", "", "u", "p", host, "http", port, false))

	byName := map[string]Check{}
	for _, c := range res.Checks {
		byName[c.Name] = c
	}
	assert.Equal(t, "keystoreName=system", queries["/api/v1/KeystoreEntries"], "same request as keystore list")
	assert.Empty(t, queries["/api/v1/UserCredentials"], "no $top: not every collection accepts it")
	assert.Empty(t, queries["/api/v1/DataStores"])
	assert.Equal(t, CheckOK, byName["keystore"].Status)
	assert.Equal(t, CheckWarn, byName["data stores"].Status)
	assert.Equal(t, "HTTP 400: Data store service not enabled", byName["data stores"].Detail, "the tenant's own message")
	assert.Equal(t, CheckMissing, byName["log files"].Status)
	assert.Contains(t, byName["log files"].Detail, "501")
	assert.Contains(t, byName["log files"].Detail, ": Not supported")
	assert.Equal(t, CheckForbidden, byName["audit log"].Status, "as on a trial tenant")

	// categories per check, for UIs that classify without parsing Detail
	assert.Equal(t, "", byName["keystore"].Category)
	assert.Equal(t, 200, byName["keystore"].HTTPStatus)
	assert.Equal(t, CategoryAuth, byName["audit log"].Category)
	assert.Equal(t, 403, byName["audit log"].HTTPStatus)
	assert.Equal(t, CategoryTenantHTTP, byName["log files"].Category)
	assert.Equal(t, CategoryTenantHTTP, byName["data stores"].Category)

	// no connection at all: the first check fails with tenant_http
	res = Doctor(context.Background(), httpclnt.New("", "", "", "", "u", "p", "127.0.0.1", "http", 1, false))
	require.Len(t, res.Checks, 1)
	assert.Equal(t, CategoryTenantHTTP, res.Checks[0].Category)
	assert.Equal(t, CategoryTenantHTTP, ErrorCategory(res.Err))
}

func TestAPIErrorMessage(t *testing.T) {
	assert.Equal(t, "x y", apiErrorMessage([]byte(`{"error":{"message":"x   y"}}`)))
	assert.Empty(t, apiErrorMessage([]byte("<html><body>error</body></html>")))
	assert.Len(t, []rune(apiErrorMessage([]byte(strings.Repeat("a", 500)))), 201)
}
