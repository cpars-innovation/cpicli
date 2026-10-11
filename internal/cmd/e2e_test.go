//go:build e2e

package cmd

// End-to-end test against a development tenant. It is compiled only with
// the build tag e2e and runs only with CPICTL_E2E=1, so `go test ./...`
// never reaches a tenant. See docs/e2e.md.
//
//	CPICTL_E2E=1 CPICTL_TMN_HOST=... CPICTL_OAUTH_HOST=... \
//	CPICTL_OAUTH_CLIENTID=... CPICTL_OAUTH_CLIENTSECRET=... \
//	  go test -tags e2e -run TestE2E -v -timeout 30m ./internal/cmd
//
// It creates (or reuses) the package CPICTL_E2E_PACKAGE (default CpictlE2E)
// with one flow CPICTL_E2E_FLOW (default CpictlE2E_Flow), deploys it, and
// undeploys it at the end (CPICTL_E2E_KEEP=1 keeps it running). The
// designtime artifact and the package stay on the tenant.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/iflow"
	"github.com/cpars-innovation/cpicli/pkg/snapshot"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// e2eRun runs the CLI in-process with the caller's environment (tenant
// credentials from CPICTL_*), unlike runMain.
func e2eRun(t *testing.T, args ...string) cliRun {
	t.Helper()
	viper.Reset()
	var stdout, stderr bytes.Buffer
	t.Logf("$ cpictl %s", strings.Join(args, " "))
	code := Run(context.Background(), args, &stdout, &stderr, "e2e", "e2e")
	return cliRun{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// e2eJSON runs a command with --output json, requires the exit code and
// decodes the result into v.
func e2eJSON(t *testing.T, want int, v any, args ...string) cliRun {
	t.Helper()
	r := e2eRun(t, append(args, "--output", "json")...)
	require.Equal(t, want, r.code, "exit code of %s\n%s", strings.Join(args, " "), r.stderr)
	if v != nil {
		var env struct {
			Result json.RawMessage `json:"result"`
		}
		require.NoError(t, json.Unmarshal([]byte(r.stdout), &env), r.stdout)
		require.NoError(t, json.Unmarshal(env.Result, v), string(env.Result))
	}
	return r
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func TestE2E(t *testing.T) {
	if os.Getenv("CPICTL_E2E") != "1" {
		t.Skip("set CPICTL_E2E=1 and the tenant variables to run against a development tenant (docs/e2e.md)")
	}
	require.NotEmpty(t, os.Getenv("CPICTL_TMN_HOST"), "CPICTL_TMN_HOST")
	pkg := envOr("CPICTL_E2E_PACKAGE", "CpictlE2E")
	flow := envOr("CPICTL_E2E_FLOW", "CpictlE2E_Flow")
	root := t.TempDir()
	packages := filepath.Join(root, "packages")
	flowDir := filepath.Join(packages, pkg, flow)
	work := t.TempDir()

	// 0. package and flow
	e2eJSON(t, 0, nil, "packages", "create", "--package-id", pkg, "--name", "cpictl end-to-end test",
		"--description", "Created by cpictl's end-to-end test (go test -tags e2e). Safe to delete.")
	// a version per run, so that manifest versioning always has a new one
	version := fmt.Sprintf("1.0.%d", time.Now().Unix())
	e2eJSON(t, 0, nil, "iflow", "copy", "--from-dir", filepath.Join("..", "..", "test", "testdata", "artifacts", "create", "Integration_Test_IFlow"),
		"--id", flow, "--name", flow, "--dir", flowDir, "--version", version, "--keep-addresses")
	cfg := filepath.Join(root, "deploy.yml")
	require.NoError(t, os.WriteFile(cfg, []byte(fmt.Sprintf(`packages:
  - integrationSuiteId: %s
    packageDir: %s
    artifacts:
      - {artifactId: %s, artifactDir: %s, type: IntegrationFlow}
`, pkg, pkg, flow, flow)), 0o644))
	orch := []string{"orchestrator", "--packages-dir", packages, "--deploy-config", cfg, "--versioning", "manifest"}
	if os.Getenv("CPICTL_E2E_KEEP") != "1" {
		t.Cleanup(func() {
			r := e2eRun(t, "undeploy", "--artifact-ids", flow)
			t.Logf("undeploy %s: exit %d", flow, r.code)
		})
	}
	planItem := func(res orchestratorResult) PlanItem {
		for _, it := range res.Plan {
			if it.Artifact == flow {
				return it
			}
		}
		t.Fatalf("%s not in the plan", flow)
		return PlanItem{}
	}

	// 1. plan, then upload and deploy
	var res orchestratorResult
	e2eJSON(t, 0, &res, append(orch, "--plan", "--snapshot-state", "off")...)
	it := planItem(res)
	assert.Contains(t, []string{"create", "update"}, it.Upload, "a new version is uploaded")
	assert.True(t, it.Deploy, it.Reason)
	e2eJSON(t, 0, &res, append(orch, "--snapshot-state", "off")...)
	assert.Equal(t, "DEPLOYED", statusOf(res, flow).Deploy, "%+v", res.Artifacts)

	// 2. snapshot twice: the second one writes nothing
	snap := []string{"snapshot", "--dir-git-repo", packages, "--dir-work", work, "--git-skip-commit", "--ids-include", pkg,
		"--deploy-config", cfg, "--sync-package-details=false"}
	var sres snapshotResult
	e2eJSON(t, 0, &sres, snap...)
	t.Logf("first snapshot: %v", sres.Counts)
	tree := treeOf(t, filepath.Join(packages, pkg))
	e2eJSON(t, 0, &sres, snap...)
	assert.Equal(t, tree, treeOf(t, filepath.Join(packages, pkg)), "a second snapshot changes no file")
	for _, a := range sres.Artifacts {
		assert.Contains(t, []string{snapshot.StatusUnchanged, snapshot.StatusDraft}, a.Status, "%s: %s %s", a.Artifact, a.Note, a.Warning)
	}

	// 3. nothing changed: compared with the snapshot state, no upload, no deploy
	e2eJSON(t, 0, &res, append(orch, "--plan")...)
	it = planItem(res)
	assert.Equal(t, "unchanged", it.Upload)
	assert.Equal(t, "snapshot", it.Compared, "compared without a download")
	assert.False(t, it.Deploy, it.Reason)

	// 4. a change: new content and version, uploaded and deployed once
	model := filepath.Join(flowDir, "src", "main", "resources", "scenarioflows", "integrationflow", flow+".iflw")
	if _, err := os.Stat(model); err != nil {
		matches, _ := filepath.Glob(filepath.Join(flowDir, "src", "main", "resources", "scenarioflows", "integrationflow", "*.iflw"))
		require.Len(t, matches, 1)
		model = matches[0]
	}
	data, err := os.ReadFile(model)
	require.NoError(t, err)
	changed := strings.Replace(string(data), `name="End"`, `name="End `+version+`"`, 1)
	require.NotEqual(t, string(data), changed, "the model has an end event named End")
	require.NoError(t, os.WriteFile(model, []byte(changed), 0o644))
	e2eJSON(t, 0, nil, "version", "bump", "--dir", packages, "--artifact", flow)
	e2eJSON(t, 0, &res, append(orch, "--plan")...)
	it = planItem(res)
	assert.Equal(t, "update", it.Upload)
	assert.True(t, it.Deploy, it.Reason)
	e2eJSON(t, 0, &res, orch...)
	assert.Equal(t, StatusUpdated, statusOf(res, flow).Status)
	assert.Equal(t, "DEPLOYED", statusOf(res, flow).Deploy)

	// 5. the snapshot after the deployment gives no diff
	tree = treeOf(t, filepath.Join(packages, pkg))
	e2eJSON(t, 0, &sres, snap...)
	assert.Equal(t, tree, treeOf(t, filepath.Join(packages, pkg)), "the deployed content equals the repository")

	// 6. the diagram: lay it out, upload again, the tenant accepts it
	var lres iflow.LayoutReport
	e2eJSON(t, 0, &lres, "iflow", "layout", flowDir)
	if lres.Changed > 0 {
		e2eJSON(t, 0, nil, "version", "bump", "--dir", packages, "--artifact", flow)
		e2eJSON(t, 0, &res, orch...)
		assert.Equal(t, "DEPLOYED", statusOf(res, flow).Deploy)
		e2eJSON(t, 0, nil, "validate", "--artifact-id", flow)
		t.Logf("laid out and deployed: open %s in the Web UI and check the diagram", flow)
	}
}
