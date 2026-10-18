package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/cpars-innovation/cpicli/pkg/testrun"
)

// testTools are list_tests and run_tests: the test cases under
// .cpi/tests/<FlowId>/ (see the cpi-test skill).
func testTools(cfg Config, endpoints ops.EndpointExecuterFunc) []Tool {
	dirSchema := str(`Directory of the test cases, relative to the server root, default "` + testrun.DefaultDir + `"`)
	// load reads the cases under the root and makes their files root-relative.
	load := func(dir string, f testrun.Filter) ([]testrun.Case, error) {
		if dir == "" {
			dir = testrun.DefaultDir
		}
		abs, err := resolvePath(cfg.Root, dir)
		if err != nil {
			return nil, err
		}
		cases, err := testrun.Load(abs, f)
		if err != nil {
			return nil, err
		}
		return cases, nil
	}
	rel := func(file string) string {
		root, err := resolvePath(cfg.Root, ".") // as the case files: links resolved
		if err != nil {
			return file
		}
		if r, err := filepath.Rel(root, file); err == nil && !strings.HasPrefix(r, "..") {
			return filepath.ToSlash(r)
		}
		return file
	}
	return []Tool{
		{
			Name: "list_tests", Title: "List test cases",
			Description: "List the test cases in .cpi/tests/<FlowId>/<case>.yaml per flow: trigger (http, idoc, odata, process_direct), tags, skip reasons and the parameter keys of each flow's config.yaml. " +
				"Checks the files strictly (an unknown key or invalid YAML is a usage error with file:line). Local, nothing is sent. Run them with run_tests.",
			InputSchema: object(props{
				"dir":  dirSchema,
				"flow": str("Only the cases of this flow (name or pattern such as Orders_*)"),
				"tags": strArray("Only cases with one of these tags"),
			}),
			Annotations: map[string]any{"readOnlyHint": true, "openWorldHint": false},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Dir  string   `json:"dir"`
					Flow string   `json:"flow"`
					Tags []string `json:"tags"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				cases, err := load(a.Dir, filterOf(a.Flow, "", a.Tags))
				if err != nil {
					return nil, err
				}
				l := testrun.List(cases)
				for i := range l.Flows {
					for j := range l.Flows[i].Cases {
						l.Flows[i].Cases[j].File = rel(l.Flows[i].Cases[j].File)
					}
				}
				return l, nil
			},
		},
		{
			Name: "run_tests", Title: "Run test cases",
			Description: "Run the test cases of .cpi/tests against the deployed flows: each case sends its message (as send_test_message), waits for the processing log and checks every expectation " +
				"(http_status, message_status, response_contains / not_contains / xpath / json, error_contains, custom_headers, steps_include, max_duration, stub_calls). " +
				"Returns passed/failed/errors/skipped and per case the failures with expected and actual value; a failed case has diagnostics (messageGuid, lastError, failedStep, errorInformation, traceRoot). " +
				"apply_config sets each flow's config.yaml values (e.g. receivers pointing at the stub), redeploys, runs the cases and restores the previous values (keys reported, never values). " +
				"Triggers real processing, including receiver calls: use a development tenant. Write cases instead of sending by hand; on a failure read the diagnostics, then get_message_steps / get_trace_tree.",
			InputSchema: object(props{
				"dir":          dirSchema,
				"flow":         str("Only the cases of this flow (name or pattern)"),
				"case":         str("Only this case (file name without .yaml, or Flow/case; pattern allowed)"),
				"tags":         strArray("Only cases with one of these tags"),
				"apply_config": boolean("Apply each flow's config.yaml for the run (redeploys the flow twice: before and after its cases), default false"),
				"parallel":     map[string]any{"type": "integer", "minimum": 1, "maximum": 8, "description": "Cases run at the same time, default 1 (cases with stub_calls always run alone)"},
			}),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Dir         string   `json:"dir"`
					Flow        string   `json:"flow"`
					Case        string   `json:"case"`
					Tags        []string `json:"tags"`
					ApplyConfig bool     `json:"apply_config"`
					Parallel    int      `json:"parallel"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if err := ops.GuardProduction(cfg.Tier, cfg.AllowProduction, "run_tests"); err != nil {
					return nil, err
				}
				if a.Parallel < 0 || a.Parallel > 8 {
					return nil, output.Usagef("parallel must be between 1 and 8")
				}
				if endpoints == nil {
					return nil, output.Usagef("sending test messages is not configured for this server")
				}
				cases, err := load(a.Dir, filterOf(a.Flow, a.Case, a.Tags))
				if err != nil {
					return nil, err
				}
				if len(cases) == 0 {
					return nil, output.Usagef("no test case matches (see list_tests)")
				}
				progress := progressFrom(ctx)
				done := 0
				res, err := testrun.Run(ctx, cfg.Exe, endpoints, cases, testrun.Options{
					Parallel: a.Parallel, ApplyConfig: a.ApplyConfig, Tier: cfg.Tier, AllowProduction: cfg.AllowProduction,
					PollInterval: cfg.LogPollInterval, DeployInterval: cfg.PollInterval, DeployMaxChecks: cfg.MaxChecks,
					Progress: func(c testrun.CaseResult) {
						done++
						if progress != nil {
							progress(ops.Progress{Step: done, Total: len(cases), Message: fmt.Sprintf("%s/%s: %s", c.Flow, c.Case, c.Status)})
						}
					},
				})
				if err != nil {
					return nil, err
				}
				for i := range res.Cases {
					res.Cases[i].File = rel(res.Cases[i].File)
				}
				return res, res.Err()
			},
		},
	}
}

func filterOf(flow, c string, tags []string) testrun.Filter {
	var f testrun.Filter
	if flow != "" {
		f.Flows = []string{flow}
	}
	if c != "" {
		f.Cases = []string{c}
	}
	f.Tags = tags
	return f
}
