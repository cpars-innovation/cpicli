//go:build e2e

package cmd

// Runtime conformance against a development tenant (cpicli#59): how the
// runtime routes URLs and how the API reports changes, the assumptions of
// the access scope and the mock tenant. Compiled only with the build tag e2e
// and run only with CPICTL_CONFORMANCE=1, so `go test ./...` never reaches a
// tenant. See docs/e2e.md.
//
//	CPICTL_CONFORMANCE=1 CPICTL_TMN_HOST=... CPICTL_OAUTH_HOST=... \
//	CPICTL_OAUTH_CLIENTID=... CPICTL_OAUTH_CLIENTSECRET=... \
//	CPICTL_RUNTIME_OAUTH_CLIENTID=... CPICTL_RUNTIME_OAUTH_CLIENTSECRET=... \
//	  go test -tags e2e -run TestConformance -v -timeout 30m ./internal/cmd
//
// It deploys two copies of demo flows into the package
// CPICTL_CONFORMANCE_PACKAGE (default CpictlConformance): CpictlConf_HTTP (an
// HTTPS sender at /http/cpictlconf/billing) and CpictlConf_OData (an OData
// sender with an externalised parameter), probes them, and undeploys them
// (CPICTL_E2E_KEEP=1 keeps them). It writes the answers to
// CPICTL_CONFORMANCE_REPORT (default conformance-report.json in the working
// directory); copy that file to internal/conformance/testdata/tenant.json
// and the mock tenant's test checks that the mock answers the same.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/conformance"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/stretchr/testify/require"
)

func TestConformance(t *testing.T) {
	if os.Getenv("CPICTL_CONFORMANCE") != "1" {
		t.Skip("set CPICTL_CONFORMANCE=1 and the tenant variables to probe a development tenant (docs/e2e.md)")
	}
	require.NotEmpty(t, os.Getenv("CPICTL_TMN_HOST"), "CPICTL_TMN_HOST")
	pkg := envOr("CPICTL_CONFORMANCE_PACKAGE", "CpictlConformance")
	const httpFlow, odataFlow = "CpictlConf_HTTP", "CpictlConf_OData"
	demo := filepath.Join("..", "cpitest", "landscapes", "demo", "packages", "Orders")
	packages := filepath.Join(t.TempDir(), "packages")

	e2eJSON(t, 0, nil, "packages", "create", "--package-id", pkg, "--name", "cpictl runtime conformance",
		"--description", "Created by cpictl's conformance test (go test -tags e2e). Safe to delete.")
	version := fmt.Sprintf("1.0.%d", time.Now().Unix())
	e2eJSON(t, 0, nil, "iflow", "copy", "--from-dir", filepath.Join(demo, "Orders_In"), "--id", httpFlow, "--name", httpFlow,
		"--dir", filepath.Join(packages, pkg, httpFlow), "--version", version, "--address", "/orders/in=/cpictlconf/billing")
	e2eJSON(t, 0, nil, "iflow", "copy", "--from-dir", filepath.Join(demo, "Orders_OData_Service"), "--id", odataFlow, "--name", odataFlow,
		"--dir", filepath.Join(packages, pkg, odataFlow), "--version", version, "--keep-addresses")
	cfg := filepath.Join(t.TempDir(), "deploy.yml")
	require.NoError(t, os.WriteFile(cfg, []byte(fmt.Sprintf(`packages:
  - integrationSuiteId: %s
    packageDir: %s
    artifacts:
      - {artifactId: %s, artifactDir: %s, type: IntegrationFlow}
      - {artifactId: %s, artifactDir: %s, type: IntegrationFlow}
`, pkg, pkg, httpFlow, httpFlow, odataFlow, odataFlow)), 0o644))
	if os.Getenv("CPICTL_E2E_KEEP") != "1" {
		t.Cleanup(func() {
			r := e2eRun(t, "undeploy", "--artifact-ids", httpFlow+","+odataFlow)
			t.Logf("undeploy: exit %d", r.code)
		})
	}
	var res orchestratorResult
	e2eJSON(t, 0, &res, "orchestrator", "--packages-dir", packages, "--deploy-config", cfg, "--snapshot-state", "off")
	deployed := time.Now()

	api := cpi.InitHTTPExecuter(&cpi.ServiceDetails{Host: os.Getenv("CPICTL_TMN_HOST"), OauthHost: os.Getenv("CPICTL_OAUTH_HOST"),
		OauthPath: envOr("CPICTL_OAUTH_PATH", "/oauth/token"), OauthClientId: os.Getenv("CPICTL_OAUTH_CLIENTID"),
		OauthClientSecret: os.Getenv("CPICTL_OAUTH_CLIENTSECRET"), Userid: os.Getenv("CPICTL_TMN_USERID"), Password: os.Getenv("CPICTL_TMN_PASSWORD")})
	runtime := &cpi.ServiceDetails{OauthHost: envOr("CPICTL_RUNTIME_OAUTH_HOST", os.Getenv("CPICTL_OAUTH_HOST")), OauthPath: envOr("CPICTL_OAUTH_PATH", "/oauth/token"),
		OauthClientId:     envOr("CPICTL_RUNTIME_OAUTH_CLIENTID", os.Getenv("CPICTL_OAUTH_CLIENTID")),
		OauthClientSecret: envOr("CPICTL_RUNTIME_OAUTH_CLIENTSECRET", os.Getenv("CPICTL_OAUTH_CLIENTSECRET"))}

	// how long until a new deployment is listed (the access scope decides on
	// the list, docs/access-scope.md)
	var runtimeBase string
	for {
		eps, err := conformance.EntryPoints(api)
		require.NoError(t, err)
		if len(eps[httpFlow]) > 0 && len(eps[odataFlow]) > 0 {
			u, err := url.Parse(eps[httpFlow][0])
			require.NoError(t, err)
			runtimeBase = u.Scheme + "://" + u.Host
			break
		}
		require.Less(t, time.Since(deployed), 10*time.Minute, "the flows are not listed in ServiceEndpoints")
		time.Sleep(2 * time.Second)
	}
	listed := time.Since(deployed)

	facts, err := conformance.Run(context.Background(), conformance.Target{
		API: api,
		Send: func(_ context.Context, method, path string) (int, error) {
			exe, p, err := cpi.NewEndpointExecuter(runtime, runtimeBase+path)
			if err != nil {
				return 0, err
			}
			resp, err := exe.Exec(method, p, http.NoBody, map[string]string{"Content-Type": "application/xml"})
			if err != nil {
				return 0, err
			}
			_, _ = exe.ReadRespBody(resp)
			return resp.StatusCode, nil
		},
		HTTPFlow: httpFlow, ODataFlow: odataFlow, ConfigKey: "ERP_IDOC_URL",
	})
	require.NoError(t, err)

	report := struct {
		Date               string             `json:"date"`
		ListedAfterSeconds float64            `json:"listedAfterSeconds"`
		Facts              []conformance.Fact `json:"facts"`
	}{time.Now().UTC().Format(time.RFC3339), listed.Seconds(), facts}
	b, _ := json.MarshalIndent(report, "", "  ")
	out := envOr("CPICTL_CONFORMANCE_REPORT", "conformance-report.json")
	require.NoError(t, os.WriteFile(out, append(b, '\n'), 0o644))
	t.Logf("listed %.0f s after the deployment; report: %s\n%s", listed.Seconds(), out, b)
	for _, f := range facts {
		if !f.Holds() {
			t.Errorf("%s: %s is %v on this tenant; the access scope relies on %v: %s", f.ID, f.Text, f.Got, f.Want, f.Note)
		}
	}
}
