package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/scope"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// activeScope is the scope of this process (nil: unrestricted).
var activeScope *scope.Scope

func addScopeFlags(c *cobra.Command) {
	f := c.PersistentFlags()
	f.String("scope-file", "", "Access scope (JSON or YAML): packages, artifacts, partners, tenant areas and actions this process may use; everything else is refused or hidden. Fixed for the process; not read from config files")
	f.StringSlice("scope-packages", nil, "Access scope without a file: package patterns (with --scope-actions)")
	f.StringSlice("scope-artifacts", nil, "Access scope without a file: artifact patterns (!X excludes)")
	f.StringSlice("scope-partners", nil, "Access scope without a file: Partner Directory partner patterns")
	f.StringSlice("scope-tenant", nil, "Access scope without a file: tenant-wide grants (<area>:read|write, e.g. variables:read)")
	f.StringSlice("scope-actions", nil, "Access scope without a file: actions (read, download, upload, deploy, configure, test, log-level, delete, pd-write)")
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
		OnRefuse: func(r *scope.Refusal) {
			log.Warn().Str("action", r.Action).Str("resource", r.Resource.String()).Msg(r.Error())
			output.Emit(ctx, "scope.refused", map[string]any{"action": r.Action, "kind": r.Resource.Kind, "id": r.Resource.ID, "reason": r.Reason})
		},
	}
	httpclnt.SetGuard(g)
	log.Debug().Msgf("Access scope active (sha256 %s)", s.Hash())
	return nil
}
