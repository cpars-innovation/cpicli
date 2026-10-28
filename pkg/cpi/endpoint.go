package cpi

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// NewEndpointExecuter returns an executer for a runtime endpoint URL of a
// deployed integration flow (as listed by ServiceEndpoints) and the request
// path including the query. auth supplies the credentials; its Host is ignored.
// Plain http is only accepted for loopback hosts (local mocks).
func NewEndpointExecuter(auth *ServiceDetails, endpointURL string) (*httpclnt.HTTPExecuter, string, error) {
	u, err := url.Parse(endpointURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, "", fmt.Errorf("invalid endpoint URL %q", endpointURL)
	}
	details := *auth
	details.Host = u.Scheme + "://" + u.Host
	if details.Gateway != "" {
		// the gateway serves the tenant's runtime endpoints below
		// <gateway>/t/<tenant>/rt, with its runtime key
		details.Gateway = strings.TrimSuffix(details.Gateway, "/") + "/rt"
	}
	scheme, _, _ := ParseHost(details.Host)
	if scheme != u.Scheme {
		return nil, "", fmt.Errorf("endpoint URL %q must use https", endpointURL)
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	return InitHTTPExecuter(&details).ForEndpoint(u.EscapedPath()), path, nil
}
