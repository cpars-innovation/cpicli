package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

// Every command works in gateway mode (cpicli#86): none asks for a tenant
// host or a SAP key, and every request it sends goes to the gateway (the
// SAP tenant's path, or /whoami). Commands run without arguments, so most
// stop at a usage error of their own; what must never come is a complaint
// about a missing tmn-host or service key, or a request elsewhere. A new
// command is covered without being listed here.
func TestEveryCommandWorksInGatewayMode(t *testing.T) {
	t.Setenv("CPICTL_TOKEN_STORE", "file")
	for _, k := range append(credentialConfigKeys, hostConfigKeys...) {
		t.Setenv(envKey(k), "")
	}
	t.Setenv("CPICTL_GATEWAY", "")
	t.Setenv(gatewayTokenEnv, "user-token")
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": {}})
	gw, seen := fakeGateway(t, mock)
	home := t.TempDir()
	writeModeFile(t, filepath.Join(home, ".cpictl", "gw.yaml"), "gateway: "+gw.URL+"/t/dev\n", 0o600)
	cpitest.SetHome(t, home)

	// interactive or long-running by design; checked elsewhere
	skip := map[string]bool{
		"auth login": true, "auth logout": true, "completion": true, "help": true, "mcp": true, "mcp serve": true,
		"update": true, "profile add": true, "logs follow": true, "runtimes watch": true, "watch": true,
	}
	var paths [][]string
	walkCommands(NewCLI("test"), func(c *cobra.Command) {
		if !c.Runnable() || c.Hidden || !c.HasParent() {
			return
		}
		path := strings.Fields(c.CommandPath())[1:]
		if skip[strings.Join(path, " ")] || skip[path[0]] {
			return
		}
		paths = append(paths, path)
	})
	if len(paths) < 50 {
		t.Fatalf("only %d commands found: the walk is broken", len(paths))
	}

	for _, path := range paths {
		name := strings.Join(path, " ")
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			viper.Reset()
			t.Cleanup(viper.Reset)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			before := len(seen())
			var stdout, stderr bytes.Buffer
			args := append(append([]string{"--profile", "gw"}, path...), "--output", "json")
			Run(ctx, args, &stdout, &stderr, "test", "test")
			out := stderr.String() + stdout.String()
			for _, bad := range []string{"tmn-host", "no tenant configured", "oauth-clientid", "tmn-userid", "service key is required"} {
				assert.NotContains(t, out, bad, "%s asks for a tenant host or SAP key in gateway mode", name)
			}
			if len(seen()) > before {
				t.Logf("REQ %s", name)
			} else {
				t.Logf("NOREQ %s: %s", name, firstErrorLine(stderr.String()))
			}
			for _, s := range seen()[before:] {
				assert.True(t, strings.Contains(s, " /t/dev/") || strings.HasSuffix(s, " /whoami"), "%s sent %s outside the gateway's tenant path", name, s)
			}
		})
	}
	_ = os.Remove(filepath.Join(home, ".cpictl", "gw.yaml"))
}

func firstErrorLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, `"level":"error"`) || strings.Contains(l, "ERR") {
			if len(l) > 200 {
				return l[:200]
			}
			return l
		}
	}
	return ""
}

// No command decides on the tenant host itself (cpicli#86): the connection
// comes from serviceDetails and the tenant's name from tenantTarget, which
// both know gateway mode. A command that reads tmn-host or checks .Host
// would fail through a gateway again; the run above cannot see it when the
// command needs arguments first. The places listed here handle both modes.
func TestNoCommandReadsTheTenantHostDirectly(t *testing.T) {
	re := regexp.MustCompile(`"tmn-host"\)|(\b(sd|serviceDetails|details|d|o|opts)|\))\.Host\b`)
	allowed := map[string]int{
		"service_details.go": 3, // builds the connection; tenantTarget
		"root.go":            3, // checks the connection settings, after gateway mode
		"compare.go":         2, // another profile without a gateway
		"profile.go":         1, // shows a profile
		"doctor.go":          0,
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		n := len(re.FindAll(data, -1))
		if n > allowed[f] {
			for _, m := range re.FindAllIndex(data, -1) {
				line := 1 + strings.Count(string(data[:m[0]]), "\n")
				t.Errorf("%s:%d reads the tenant host directly: use serviceDetails(cmd).Target() or tenantTarget(cmd), which know gateway mode", f, line)
			}
		}
	}
}

// compare tenant:<profile> takes a gateway profile too, with that profile's
// own sign-in, never the environment's token (cpicli#86).
func TestCompareGatewayProfile(t *testing.T) {
	t.Setenv("CPICTL_TOKEN_STORE", "file")
	t.Setenv(gatewayTokenEnv, "env-token")
	home := t.TempDir()
	cpitest.SetHome(t, home)
	writeModeFile(t, filepath.Join(home, ".cpictl", "gwprod.yaml"), "gateway: https://gw.example.com/t/prod\n", 0o600)
	_, err := profileServiceDetails("gwprod")
	assert.ErrorContains(t, err, "gateway profile without a sign-in")
	writeModeFile(t, filepath.Join(home, ".cpictl", "gwprod.yaml"),
		"gateway: https://gw.example.com/t/prod\nlogin:\n  tenantId: tid\n  clientId: cid\n", 0o600)
	_, err = profileServiceDetails("gwprod")
	assert.ErrorContains(t, err, "cpictl --profile gwprod auth login", "not signed in: the environment's token is not used")
}
