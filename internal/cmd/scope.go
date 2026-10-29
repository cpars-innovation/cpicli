package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"

	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/cpars-innovation/cpicli/pkg/scope"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// activeScope is the scope of this process (nil: unrestricted).
var activeScope *scope.Scope

// lastRefusal is the last scope refusal of the current command (nil: none).
var lastRefusal atomic.Pointer[scope.Refusal]

// withScopeRefusal makes a failed command whose failures came from the scope
// end with exit code 9 (errorCategory scope): commands that report per-item
// failures (configure, orchestrator) otherwise turn them into "failed".
// Partial results stay partial.
func withScopeRefusal(err error) error {
	r := lastRefusal.Load()
	if err == nil || r == nil || output.ExitCode(err) != exitcode.DeployFailed {
		return err
	}
	return &scopeFailure{err: err, refusal: r}
}

type scopeFailure struct {
	err     error
	refusal *scope.Refusal
}

func (e *scopeFailure) Error() string { return e.err.Error() + " (" + e.refusal.Error() + ")" }
func (e *scopeFailure) Unwrap() error { return e.err }
func (e *scopeFailure) ExitCode() int { return exitcode.Scope }

func addScopeFlags(c *cobra.Command) {
	f := c.PersistentFlags()
	f.String("scope-file", "", "Access scope (JSON or YAML): packages, artifacts, partners, tenant areas and actions this process may use; everything else is refused or hidden. Fixed for the process; not read from config files")
	f.StringSlice("scope-packages", nil, "Access scope without a file: package patterns (with --scope-actions)")
	f.StringSlice("scope-artifacts", nil, "Access scope without a file: artifact patterns (!X excludes)")
	f.StringSlice("scope-partners", nil, "Access scope without a file: Partner Directory partner patterns")
	f.StringSlice("scope-tenant", nil, "Access scope without a file: tenant-wide grants (<area>:read|write, e.g. variables:read)")
	f.StringSlice("scope-actions", nil, "Access scope without a file: actions (read, download, upload, deploy, configure, test, log-level, delete, pd-write)")
	f.Bool("scope-harness", false, "Under a scope, allow test messages through the test harness to ProcessDirect addresses of flows in the scope (off by default until checked on a tenant, docs/access-scope.md)")
}

// loadScope builds the scope from the flags only: a config file (which an
// agent might be able to edit) cannot widen or remove it.
func loadScope(cmd *cobra.Command) (*scope.Scope, error) {
	flag := func(name string) []string {
		v, _ := cmd.Flags().GetStringSlice(name)
		return v
	}
	file, _ := cmd.Flags().GetString("scope-file")
	short := map[string][]string{"packages": flag("scope-packages"), "artifacts": flag("scope-artifacts"),
		"partners": flag("scope-partners"), "tenant": flag("scope-tenant"), "actions": flag("scope-actions")}
	anyShort := false
	for _, v := range short {
		anyShort = anyShort || len(v) > 0
	}
	switch {
	case file != "" && anyShort:
		return nil, output.Usagef("use --scope-file or the --scope-* flags, not both")
	case file != "":
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, output.Usage(err)
		}
		s, err := scope.Parse(data)
		if err != nil {
			return nil, output.Usage(err)
		}
		return s, nil
	case anyShort:
		var b strings.Builder
		b.WriteString("version: 1\n")
		for _, k := range []string{"packages", "artifacts", "partners", "tenant", "actions"} {
			if len(short[k]) > 0 {
				fmt.Fprintf(&b, "%s: [%s]\n", k, quoteAll(short[k]))
			}
		}
		s, err := scope.Parse([]byte(b.String()))
		if err != nil {
			return nil, output.Usage(err)
		}
		return s, nil
	}
	return nil, nil
}

func quoteAll(v []string) string {
	q := make([]string, len(v))
	for i, s := range v {
		q[i] = `"` + strings.ReplaceAll(s, `"`, "") + `"`
	}
	return strings.Join(q, ", ")
}

// installScope activates the scope for every tenant request of the process.
func installScope(cmd *cobra.Command) error {
	s, err := loadScope(cmd)
	if err != nil {
		return err
	}
	activeScope = s
	lastRefusal.Store(nil)
	if s == nil {
		httpclnt.SetGuard(nil)
		return nil
	}
	ctx := cmd.Context()
	g := &scope.Guard{Scope: s,
		Fetch: func(fctx context.Context, path string) ([]byte, error) {
			exe := cpi.InitHTTPExecuter(serviceDetails(cmd)).WithContext(fctx)
			resp, err := exe.Exec(http.MethodGet, path, http.NoBody, map[string]string{"Accept": "application/json"})
			if err != nil {
				return nil, err
			}
			body, err := exe.ReadRespBody(resp)
			if err == nil && resp.StatusCode != http.StatusOK {
				err = fmt.Errorf("HTTP %d", resp.StatusCode)
			}
			return body, err
		},
		// the harness decision stays off unless --scope-harness: without
		// ProcessDirectAddresses the guard refuses every harness call

		OnRefuse: func(r *scope.Refusal) {
			lastRefusal.Store(r)
			log.Warn().Str("action", r.Action).Str("resource", r.Resource.String()).Msg(r.Error())
			output.Emit(ctx, "scope.refused", map[string]any{"action": r.Action, "kind": r.Resource.Kind, "id": r.Resource.ID, "reason": r.Reason})
		},
	}
	if on, _ := cmd.Flags().GetBool("scope-harness"); on {
		g.ProcessDirectAddresses = func(fctx context.Context, id string) ([]string, error) {
			return ops.ProcessDirectAddresses(cpi.InitHTTPExecuter(serviceDetails(cmd)).WithContext(fctx), id)
		}
	}
	httpclnt.SetGuard(g)
	log.Debug().Msgf("Access scope active (sha256 %s)", s.Hash())
	return nil
}
