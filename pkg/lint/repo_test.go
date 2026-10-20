package lint

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepositoryRules(t *testing.T) {
	root := t.TempDir()
	tree := filepath.Join(root, "packages")
	for _, f := range []tFlow{
		{pkg: "Orders", id: "Orders_In", params: "Host=dev.example\nUser=dev\nApi_Token=\n"},
		{pkg: "Orders", id: "Orders_Out", params: "Target=dev.example\n"},
		{pkg: "Billing", id: "Billing_Post", params: "Host=dev.example\n"},
	} {
		f.write(t, tree)
	}
	write := func(name, content string) string {
		p := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
		return p
	}
	write("configuration/prod.yml", `packages:
  - integrationSuiteId: Orders
    artifacts:
      - artifactId: Orders_In
        type: Integration
        parameters:
          - {key: Host, value: prod.example}
          - {key: Password, value: x}
          - {key: Api_Token, value: tok-5f3a9c2e}
`)
	write("deploy.yml", `packages:
  - integrationSuiteId: Orders
    packageDir: Orders
    artifacts:
      - {artifactId: Orders_In, artifactDir: Orders_In, type: Integration}
`)
	cfg := DefaultConfig()
	cfg.baseDir = root
	cfg.Repository.Configure, cfg.Repository.DeployConfig = "configuration", "deploy.yml"
	o := Options{Dir: tree, Config: cfg}
	require.NoError(t, LoadRepositoryFiles(&o, "", ""))
	res, err := Run(context.Background(), o)
	require.NoError(t, err)
	has := func(rule, artifact string, n int) []Finding {
		t.Helper()
		got := findingsOf(res, rule, artifact)
		assert.Len(t, got, n, "%s in %s: %v", rule, artifact, got)
		return got
	}
	f := has("configure-unknown-parameter", "Orders_In", 1)
	assert.Contains(t, f[0].Message, "prod.yml sets a parameter the flow does not have: Password")
	assert.Equal(t, SevError, f[0].Severity)
	f = has("configure-missing-parameter", "Orders_In", 1)
	assert.Contains(t, f[0].Message, "no value for the parameter User")
	f = has("configure-missing-parameter", "Orders_Out", 1)
	assert.Contains(t, f[0].Message, "covers package Orders but has no entry for this flow")
	has("configure-missing-parameter", "Billing_Post", 0) // the file does not cover Billing
	f = has("hardcoded-secret", "Orders_In", 1)
	assert.Equal(t, "prod.yml sets the parameter Api_Token to a value that looks like a secret", f[0].Message)

	f = has("flow-without-deployment", "Orders_Out", 1)
	assert.Contains(t, f[0].Message, "package folder Orders")
	has("flow-without-deployment", "Orders_In", 0)
	has("flow-without-deployment", "Billing_Post", 0) // the deploy config does not cover Billing

	// without the files nothing is checked
	res, err = Run(context.Background(), Options{Dir: tree})
	require.NoError(t, err)
	for _, r := range []string{"configure-unknown-parameter", "configure-missing-parameter", "flow-without-deployment"} {
		assert.Zero(t, res.ByRule[r])
	}
	assert.ErrorContains(t, LoadRepositoryFiles(&Options{}, filepath.Join(root, "missing.yml"), ""), "configure files")
}
