package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/internal/stats"
	"github.com/cpars-innovation/cpicli/internal/sync"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// doctorResult is the JSON result of doctor.
type doctorResult struct {
	Version string            `json:"version"`
	Local   []ops.Check       `json:"local"`
	Tenant  *ops.DoctorResult `json:"tenant,omitempty"`
}

func NewDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the setup: configuration, connection and which API areas the credentials can use",
		Long: `Check the setup and report what works:

  local   config file / profile, tenant host, authentication method, runtime
          credentials for test messages, git, snapshot state and pending
          deployments in .cpi, usage statistics
  tenant  connection and authentication, then one small read (GET, $top=1) per
          API area: designtime, runtime, message logs, security material,
          keystore, Partner Directory, data stores, log files. 403 means the
          credentials lack the role for that area, 404 that the tenant does not
          offer the API.

Nothing is changed. Exit code 3 when authentication fails, 4 without a
connection, 2 without a tenant host; otherwise 0, also when optional areas are
forbidden (see the result).`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			res := doctorResult{Version: cmd.Root().Version, Local: localChecks(cmd)}
			details := serviceDetails(cmd)
			var err error
			if details.Host == "" {
				err = output.Usagef("no tenant host: set --tmn-host, CPICTL_TMN_HOST, a profile (cpictl profile use) or cpictl.yaml")
			} else {
				res.Tenant = ops.Doctor(cmd.Context(), cpi.InitHTTPExecuter(details))
				err = res.Tenant.Err
			}
			output.SetResult(cmd.Context(), res)
			logChecks("local", res.Local)
			if res.Tenant != nil {
				logChecks("tenant "+cpi.TenantID(details.Host), res.Tenant.Checks)
			}
			return err
		},
	}
}

func logChecks(title string, checks []ops.Check) {
	icons := map[string]string{ops.CheckOK: "✅", ops.CheckWarn: "⚠️ ", ops.CheckForbidden: "🔒", ops.CheckMissing: "➖", ops.CheckFailed: "❌"}
	log.Info().Msgf("── %s", title)
	for _, c := range checks {
		ev := log.Info()
		if c.Status == ops.CheckFailed {
			ev = log.Error()
		} else if c.Status != ops.CheckOK {
			ev = log.Warn()
		}
		line := fmt.Sprintf("%s %-20s %s", icons[c.Status], c.Name, c.Detail)
		if c.Needed != "" && c.Status != ops.CheckOK {
			line += " (needed for: " + c.Needed + ")"
		}
		ev.Str("check", c.Name).Str("status", c.Status).Msg(line)
	}
}

func localChecks(cmd *cobra.Command) []ops.Check {
	var checks []ops.Check
	add := func(name, status, detail, needed string) {
		checks = append(checks, ops.Check{Name: name, Status: status, Detail: detail, Needed: needed})
	}
	if len(loadedConfig) > 0 {
		add("config", ops.CheckOK, strings.Join(loadedConfig, ", "), "")
	} else if f := viper.ConfigFileUsed(); f != "" {
		add("config", ops.CheckOK, f, "")
	} else {
		add("config", ops.CheckWarn, "no config file or profile: flags and CPICTL_* variables only", "")
	}
	d := serviceDetails(cmd)
	switch {
	case d.Host == "":
		add("tenant host", ops.CheckFailed, "not set", "every tenant command")
	default:
		add("tenant host", ops.CheckOK, cpi.TenantID(d.Host), "")
	}
	switch {
	case d.OauthHost != "" && d.OauthClientId != "" && d.OauthClientSecret != "":
		add("authentication", ops.CheckOK, "OAuth client credentials ("+d.OauthHost+")", "")
	case d.OauthHost == "" && d.Userid != "" && d.Password != "":
		add("authentication", ops.CheckOK, "Basic Auth (user "+d.Userid+")", "")
	default:
		add("authentication", ops.CheckFailed, "incomplete: OAuth host, client ID and secret, or user and password", "every tenant command")
	}
	if viper.GetString("runtime-oauth-clientid") != "" || viper.GetString("runtime-userid") != "" {
		add("runtime credentials", ops.CheckOK, "set", "")
	} else {
		add("runtime credentials", ops.CheckWarn, "not set", "send / send_test_message")
	}
	if p, err := exec.LookPath("git"); err == nil {
		add("git", ops.CheckOK, p, "")
	} else {
		add("git", ops.CheckWarn, "not found", "snapshot commits, version bump --changed")
	}
	statePath := filepath.Join(".cpi", "snapshot-state.json")
	if st, err := sync.LoadSnapshotState(statePath); err != nil {
		add("snapshot state", ops.CheckWarn, fmt.Sprintf("%s unreadable: %v", statePath, err), "orchestrator comparison without downloads")
	} else if len(st.Artifacts) == 0 {
		add("snapshot state", ops.CheckWarn, "none in .cpi (orchestrator downloads every artifact for the comparison)", "")
	} else if d.Host != "" && st.Tenant != cpi.TenantID(d.Host) {
		add("snapshot state", ops.CheckWarn, fmt.Sprintf("%d artifacts of tenant %q, not this one", len(st.Artifacts), st.Tenant), "")
	} else {
		add("snapshot state", ops.CheckOK, fmt.Sprintf("%d artifacts", len(st.Artifacts)), "")
	}
	if p, err := loadPending(defaultPendingFile); err == nil && len(p.Artifacts) > 0 {
		add("pending deployments", ops.CheckWarn, fmt.Sprintf("%d in %s for %s: cpictl deploy --pending", len(p.Artifacts), defaultPendingFile, p.Tenant), "")
	}
	if path := stats.Path(); path != "" {
		add("usage statistics", ops.CheckOK, path+" (local only)", "")
	} else if os.Getenv("CPICTL_STATS") != "" {
		add("usage statistics", ops.CheckOK, "off", "")
	}
	return checks
}
