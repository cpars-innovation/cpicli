package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// Doctor check statuses.
const (
	CheckOK        = "ok"
	CheckWarn      = "warn"      // works partly or is optional
	CheckForbidden = "forbidden" // 403: the credentials lack a role
	CheckMissing   = "missing"   // 404: not available on this tenant
	CheckFailed    = "failed"
)

// Check is the outcome of one doctor check.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	// Needed lists what does not work without it.
	Needed string `json:"needed,omitempty"`
	Ms     int64  `json:"ms,omitempty"`
	// Category classifies a check that is not ok, with the names of
	// ErrorCategory: auth (401, 403, token) or tenant_http (no connection,
	// API missing, other HTTP errors).
	Category string `json:"category,omitempty"`
	// HTTPStatus is the tenant's answer (0: no answer).
	HTTPStatus int `json:"httpStatus,omitempty"`
}

// DoctorResult lists the checks; OK is false when the tenant cannot be used
// at all (no connection, authentication or designtime access).
type DoctorResult struct {
	OK     bool    `json:"ok"`
	Checks []Check `json:"checks"`
	// Err says why the tenant cannot be used (exit code: auth, tenant HTTP
	// or failed); nil when OK.
	Err error `json:"-"`
}

// apiProbe is one API area: a cheap read that needs the area's role.
type apiProbe struct {
	name, path, needed string
	core               bool
}

// The probes send the same requests as the commands that need the area: some
// collections reject query options such as $top (HTTP 400 / 501), and the
// keystore needs keystoreName.
var apiProbes = []apiProbe{
	{"designtime", "/api/v1/IntegrationPackages?$top=1", "packages, artifacts, upload, snapshot, orchestrator", true},
	{"runtime", "/api/v1/IntegrationRuntimeArtifacts?$top=1", "deploy, undeploy, status, plan", false},
	{"message logs", "/api/v1/MessageProcessingLogs?$top=1", "logs, send (wait), test loops", false},
	{"security material", "/api/v1/UserCredentials", "credentials list/apply", false},
	{"keystore", "/api/v1/KeystoreEntries?keystoreName=system", "keystore list, expiring certificates", false},
	{"partner directory", "/api/v1/StringParameters?$top=1", "pd-snapshot, pd-deploy, pd_diff", false},
	{"data stores", "/api/v1/DataStores", "datastore commands", false},
	{"log files", "/api/v1/LogFiles", "log-files", false},
	{"audit log", "/api/v1/AuditLogs?$top=1", "audit-log (who changed what)", false},
}

// Doctor checks the connection and which API areas the credentials can
// read. Only GET requests are sent.
func Doctor(ctx context.Context, exe *httpclnt.HTTPExecuter) *DoctorResult {
	exe = exe.WithContext(ctx)
	res := &DoctorResult{Checks: make([]Check, len(apiProbes))}

	// the first probe also proves authentication and connectivity; run it
	// alone so that a token problem is reported once
	var err error
	res.Checks[0], err = probe(exe, apiProbes[0])
	if res.Checks[0].Status == CheckFailed {
		res.Checks = res.Checks[:1]
		res.Err = err
		return res
	}
	var wg sync.WaitGroup
	for i := 1; i < len(apiProbes); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ctx.Err() == nil {
				res.Checks[i], _ = probe(exe, apiProbes[i])
			}
		}()
	}
	wg.Wait()
	res.OK = res.Checks[0].Status == CheckOK
	return res
}

func probe(exe *httpclnt.HTTPExecuter, p apiProbe) (c Check, err error) {
	c = Check{Name: p.name, Needed: p.needed}
	started := time.Now()
	resp, err := exe.ExecGetRequest(p.path, map[string]string{"Accept": "application/json"})
	c.Ms = time.Since(started).Milliseconds()
	if err != nil {
		c.Status, c.HTTPStatus = CheckFailed, httpclnt.StatusCode(err)
		switch output.TransportExitCode(err, 0) {
		case exitcode.Auth:
			c.Detail, c.Category = "authentication failed: "+err.Error(), CategoryAuth
			return c, output.WithExitCode(err, exitcode.Auth)
		default:
			c.Detail, c.Category = "no connection: "+err.Error(), CategoryTenantHTTP
		}
		return c, output.WithExitCode(err, exitcode.TenantHTTP)
	}
	c.HTTPStatus = resp.StatusCode
	body, _ := exe.ReadRespBody(resp)
	switch {
	case resp.StatusCode < 300:
		c.Status = CheckOK
		c.Needed = ""
	case resp.StatusCode == http.StatusUnauthorized:
		c.Status, c.Detail = CheckFailed, "401: the credentials are not accepted"
	case resp.StatusCode == http.StatusForbidden:
		c.Status, c.Detail = CheckForbidden, "403: the credentials lack the role for this area"
	case resp.StatusCode == http.StatusNotFound:
		c.Status, c.Detail = CheckMissing, "404: this API is not available on the tenant"
	case resp.StatusCode == http.StatusNotImplemented:
		c.Status, c.Detail = CheckMissing, "501: the tenant does not offer this API (e.g. a trial or another environment)"
	default:
		c.Status, c.Detail = CheckWarn, fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	if c.Status != CheckOK {
		c.Category = CategoryTenantHTTP
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			c.Category = CategoryAuth
		}
	}
	if msg := apiErrorMessage(body); msg != "" && c.Status != CheckOK {
		c.Detail += ": " + msg
	}
	if p.core && c.Status != CheckOK {
		c.Status = CheckFailed
		code := exitcode.DeployFailed
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			code = exitcode.Auth
		}
		return c, output.WithExitCode(fmt.Errorf("%s: %s", p.name, c.Detail), code)
	}
	return c, nil
}

// apiErrorMessage returns the tenant's error text from a response body: the
// OData error message, else the start of a plain text body (HTML pages are
// ignored).
func apiErrorMessage(body []byte) string {
	var odata struct {
		Error struct {
			Message any `json:"message"`
		} `json:"error"`
	}
	msg := ""
	if json.Unmarshal(body, &odata) == nil {
		switch m := odata.Error.Message.(type) {
		case string:
			msg = m
		case map[string]any:
			msg, _ = m["value"].(string)
		}
	} else if t := strings.TrimSpace(string(body)); t != "" && !strings.HasPrefix(t, "<") {
		msg = t
	}
	msg = strings.Join(strings.Fields(msg), " ")
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	return msg
}
