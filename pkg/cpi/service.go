package cpi

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/rs/zerolog/log"
	"golang.org/x/oauth2"
)

type ServiceDetails struct {
	Host              string
	Userid            string
	Password          string
	OauthHost         string
	OauthPath         string
	OauthClientId     string
	OauthClientSecret string
	// HTTPClient is the client requests go through (timeouts, transport,
	// proxy); nil: one with httpclnt.DefaultTimeout.
	HTTPClient *http.Client
	// Gateway is the URL of a cpictl gateway including the SAP tenant
	// (https://gw.example.com/t/dev): requests go there with GatewayToken,
	// the user's token, instead of to the tenant with a service key.
	Gateway      string
	GatewayToken string
	// GatewayTokenSource, when set, supplies the gateway bearer per request
	// and is preferred over GatewayToken: it refreshes the token silently so a
	// long-running session does not fail when the first token expires. Set by
	// gateway mode from a cpictl auth login token.
	GatewayTokenSource oauth2.TokenSource
}

// ReadRetries and ReadBackoff apply to every executer created by
// InitHTTPExecuter: GET and HEAD requests answered with 429, 502, 503 or 504
// are retried (see httpclnt.RetryReads). Writes are never retried.
var (
	ReadRetries = 3
	ReadBackoff = 2 * time.Second
)

func InitHTTPExecuter(serviceDetails *ServiceDetails) *httpclnt.HTTPExecuter {
	if serviceDetails.Gateway != "" {
		scheme, host, port, path := ParseGateway(serviceDetails.Gateway)
		exe := httpclnt.NewWithOptions(httpclnt.Options{Host: host, Scheme: scheme, Port: port, BasePath: path,
			BearerToken: serviceDetails.GatewayToken, BearerTokenSource: serviceDetails.GatewayTokenSource,
			HTTPClient: serviceDetails.HTTPClient, ShowLogs: true})
		return exe.RetryReads(ReadRetries, ReadBackoff)
	}
	scheme, host, port := ParseHost(serviceDetails.Host)
	oauthHost := serviceDetails.OauthHost
	if oauthHost != "" {
		// The token server shares scheme and port with the tenant host
		_, oauthHost, _ = ParseHost(oauthHost)
	}
	exe := httpclnt.NewWithOptions(httpclnt.Options{Host: host, Scheme: scheme, Port: port,
		OAuthHost: oauthHost, OAuthPath: serviceDetails.OauthPath, ClientID: serviceDetails.OauthClientId, ClientSecret: serviceDetails.OauthClientSecret,
		BasicUserID: serviceDetails.Userid, BasicPassword: serviceDetails.Password, HTTPClient: serviceDetails.HTTPClient, ShowLogs: true})
	return exe.RetryReads(ReadRetries, ReadBackoff)
}

// TenantID identifies a tenant by its host (and port when not 443), e.g. in
// state files.
func TenantID(hostFlag string) string {
	_, host, port := ParseHost(hostFlag)
	if port != 443 {
		return host + ":" + strconv.Itoa(port)
	}
	return host
}

// ParseHost splits a host flag value into scheme, host and port. The value is
// normally a bare host name (https on port 443 is used); an explicit
// "https://" prefix and a ":port" suffix are accepted. Plain http is only
// honoured for loopback hosts (local mocks), so credentials are never sent
// unencrypted to a remote tenant.
func ParseHost(value string) (scheme string, host string, port int) {
	scheme, port = "https", 443
	rest := strings.TrimSuffix(strings.TrimSpace(value), "/")
	insecure := false
	switch {
	case strings.HasPrefix(rest, "https://"):
		rest = strings.TrimPrefix(rest, "https://")
	case strings.HasPrefix(rest, "http://"):
		rest = strings.TrimPrefix(rest, "http://")
		insecure = true
	}
	host = rest
	if h, p, err := net.SplitHostPort(rest); err == nil {
		if n, err := strconv.Atoi(p); err == nil {
			host, port = h, n
		}
	}
	if insecure && (host == "localhost" || net.ParseIP(host).IsLoopback()) {
		scheme = "http"
	}
	return scheme, host, port
}

// ParseGateway splits a gateway URL into scheme, host, port and path (the
// SAP tenant, /t/<name>). As in ParseHost, plain http is only honoured for
// loopback hosts, so a token is never sent unencrypted to a remote gateway.
func ParseGateway(value string) (scheme string, host string, port int, path string) {
	rest := strings.TrimSpace(value)
	prefix := ""
	for _, p := range []string{"https://", "http://"} {
		if strings.HasPrefix(rest, p) {
			prefix, rest = p, strings.TrimPrefix(rest, p)
		}
	}
	hostPart, path, _ := strings.Cut(rest, "/")
	scheme, host, port = ParseHost(prefix + hostPart)
	if path != "" {
		path = "/" + strings.TrimSuffix(path, "/")
	}
	return scheme, host, port, path
}

func modifyingCall(method string, urlPath string, content []byte, successCode int, callType string, exe *httpclnt.HTTPExecuter) error {
	return modifyingCallWithContentType(method, urlPath, content, "application/json", successCode, callType, exe)
}

func modifyingCallWithContentType(method string, urlPath string, content []byte, contentType string, successCode int, callType string, exe *httpclnt.HTTPExecuter) error {
	headers := map[string]string{}

	headers["Accept"] = "application/json"
	var body io.Reader
	if len(content) > 0 {
		headers["Content-Type"] = contentType
		log.Debug().Msgf("Request body: %d bytes", len(content)) // never log content: may contain secrets
		body = bytes.NewReader(content)
	} else {
		body = http.NoBody
	}

	resp, err := exe.Exec(method, urlPath, body, headers)
	if err != nil {
		return err
	}
	if resp.StatusCode != successCode {
		_, err = exe.LogError(resp, callType)
		return err
	}
	return nil
}

func readOnlyCall(urlPath string, callType string, exe *httpclnt.HTTPExecuter) (*http.Response, error) {
	return readOnlyCallWithBodyAndAcceptType(urlPath, nil, callType, "application/json", exe)
}

func readOnlyCallWithBody(urlPath string, content []byte, callType string, exe *httpclnt.HTTPExecuter) (*http.Response, error) {
	return readOnlyCallWithBodyAndAcceptType(urlPath, content, callType, "", exe)
}

func readOnlyCallWithBodyAndAcceptType(urlPath string, content []byte, callType string, acceptType string, exe *httpclnt.HTTPExecuter) (*http.Response, error) {
	headers := map[string]string{}
	if acceptType != "" {
		headers["Accept"] = acceptType
	}
	var body io.Reader
	if len(content) > 0 {
		log.Debug().Msgf("Request body: %d bytes", len(content)) // never log content: may contain secrets
		body = bytes.NewReader(content)
	} else {
		body = http.NoBody
	}

	resp, err := exe.Exec(http.MethodGet, urlPath, body, headers)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		resBody, err := exe.LogError(resp, callType)
		resp.Body = io.NopCloser(bytes.NewReader(resBody))
		return resp, err
	}
	return resp, nil
}
