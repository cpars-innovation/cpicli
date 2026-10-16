package cpi_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/stretchr/testify/assert"
)

var reQuotedKey = regexp.MustCompile(`'((?:[^']|'')*)'`)

// An ID can never change which entity a URL addresses: quotes, slashes,
// parentheses and query characters in IDs arrive escaped inside the key.
func TestHostileIDsAreEscaped(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	host, port := httpclnt.GetHostPort(srv.URL)
	exe := httpclnt.New("", "", "", "", "u", "p", host, "http", port, false)

	hostile := []string{
		"X')/IntegrationPackages('Y",
		"X'&Id='Secret",
		"A/../B",
		"X?$filter=Id eq 'Z'",
	}
	for _, id := range hostile {
		seen = nil
		_, _, _, _ = cpi.NewIntegrationPackage(exe).Get(id)
		_ = cpi.NewIntegrationPackage(exe).Delete(id)
		_, _, _, _ = cpi.NewDesigntimeArtifact("Integration", exe).Get(id, "active")
		_ = cpi.NewDesigntimeArtifact("Integration", exe).Delete(id)
		_ = cpi.NewDesigntimeArtifact("Integration", exe).Deploy(id)
		_, _, _ = cpi.NewRuntime(exe).Get(id)
		_, _ = cpi.NewPartnerDirectory(exe).GetStringParameter(id, id)
		_, _ = cpi.NewConfiguration(exe).Get(id, "active")
		_ = cpi.NewConfiguration(exe).Update(id, "active", id, "v")
		assert.GreaterOrEqual(t, len(seen), 9, id)
		for _, s := range seen {
			path, query, _ := strings.Cut(s, "?")
			// every quoted key holds only escaped characters: no quote,
			// slash, parenthesis, space or query character of the ID survives
			for _, m := range reQuotedKey.FindAllStringSubmatch(path, -1) {
				// HTTP level: nothing that ends a segment or starts the query
				assert.NotRegexp(t, `['/?# ]`, m[1], "%s: %s", id, s)
				// OData level (after the server decodes): quotes stay doubled,
				// so the literal cannot be closed early
				decoded, err := url.PathUnescape(m[1])
				assert.NoError(t, err)
				assert.NotContains(t, strings.ReplaceAll(decoded, "''", ""), "'", "%s: %s", id, s)
			}
			assert.Equal(t, 0, strings.Count(path, "/..")+strings.Count(path, "('Y"), "%s: %s", id, s)
			if strings.Contains(path, "Deploy") {
				assert.NotContains(t, query, "&Id=", "%s: %s", id, s)
			}
		}
	}
}
