package cmd

import (
	"bytes"
	"strconv"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/spf13/viper"
)

// runCLI executes the CLI against a mock tenant with an isolated HOME (no
// cpictl.yaml) and returns stdout, stderr and the command error.
func runCLI(t *testing.T, mock *cpitest.Tenant, args ...string) (string, string, error) {
	t.Helper()
	cpitest.SetHome(t, t.TempDir())
	t.Setenv("CPICTL_PROFILE", "")
	t.Setenv("CPICTL_CONFIG", "")
	viper.Reset()
	t.Cleanup(viper.Reset)

	if mock != nil {
		host, port := mock.HostPort()
		args = append(args, "--tmn-host", "http://"+host+":"+strconv.Itoa(port), "--tmn-userid", "user", "--tmn-password", "secret")
	}
	root := NewCLI("test")
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}
