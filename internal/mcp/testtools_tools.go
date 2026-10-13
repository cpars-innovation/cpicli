package mcp

import (
	"context"
	"encoding/json"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/cpars-innovation/cpicli/pkg/testtools"
)

// testToolingTools install the tenant-side test tooling (test harness,
// receiver stub) and configure the stub. Both refuse on a production tier
// unless the server was started with AllowProduction.
func testToolingTools(cfg Config) []Tool {
	return []Tool{
		{
			Name: "install_test_tools", Title: "Install the test harness and the receiver stub",
			Description: "Upload the test tooling that ships with cpictl into its package (created when missing; default " + testtools.PackageID + ") and, with deploy, deploy it: " +
				testtools.HarnessID + " (HTTPS /cpictl/test -> ProcessDirect ${header.CpictlTargetAddress}; send_test_message process_direct_address uses it) and " +
				testtools.StubID + " (HTTPS /cpictl/stub/*: records each request in its message log, attachment " + testtools.StubAttachment +
				", and answers per rule from the Partner Directory, see set_stub_response). Point a receiver of the flow under test at <stubUrl>/<rule>/... " +
				"Development tenants only; never transport the package. Refused on a production tier.",
			InputSchema: object(props{
				"package_id":            str("Package of the test tooling, default " + testtools.PackageID + " (letters and digits only)"),
				"only":                  enum("Install only one tool (default both)", testtools.ToolHarness, testtools.ToolStub),
				"deploy":                boolean("Deploy the artifacts and wait for them (default false)"),
				"poll_interval_seconds": integer("Seconds between deployment status checks"),
				"max_checks":            integer("Maximum number of status checks per artifact"),
			}),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					PackageID    string `json:"package_id"`
					Only         string `json:"only"`
					Deploy       bool   `json:"deploy"`
					PollInterval *int   `json:"poll_interval_seconds"`
					MaxChecks    *int   `json:"max_checks"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if err := ops.GuardProduction(cfg.Tier, cfg.AllowProduction, "install_test_tools"); err != nil {
					return nil, err
				}
				opts := pollOptions(cfg, a.PollInterval, a.MaxChecks)
				opts.Progress = progressFrom(ctx)
				res, err := ops.InstallTestTools(ctx, cfg.Exe, ops.TestToolsOptions{PackageID: a.PackageID, Only: a.Only, Deploy: a.Deploy, DeployOptions: opts})
				if res == nil {
					return nil, err
				}
				return res, err
			},
		},
		{
			Name: "set_stub_response", Title: "Set the receiver stub's answer for a rule",
			Description: "Configure what the receiver stub (" + testtools.StubID + ", install_test_tools) answers to requests to <stubUrl>/<rule>/...: " +
				"HTTP status, content type, body and headers, stored as Partner Directory string parameter " + testtools.StubPID + "/<rule>. " +
				"Without one the stub answers 200 with {}; clear removes the rule's answer. Use it to simulate receiver answers and errors (e.g. status 500) in tests. " +
				"Refused on a production tier.",
			InputSchema: object(props{
				"rule":         str("Rule: the first path segment after /cpictl/stub/ (letters, digits, '_', '.', '-')"),
				"status":       map[string]any{"type": "integer", "minimum": 100, "maximum": 599, "description": "HTTP status, default 200"},
				"content_type": str("Content-Type of the answer, default application/json"),
				"body":         str("Body of the answer (text)"),
				"headers":      map[string]any{"type": "object", "description": "Additional response headers", "additionalProperties": map[string]any{"type": "string"}},
				"clear":        boolean("Remove the rule's answer instead (the stub then answers 200 with {})"),
			}, "rule"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Rule        string            `json:"rule"`
					Status      int               `json:"status"`
					ContentType string            `json:"content_type"`
					Body        string            `json:"body"`
					Headers     map[string]string `json:"headers"`
					Clear       bool              `json:"clear"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if err := ops.GuardProduction(cfg.Tier, cfg.AllowProduction, "set_stub_response"); err != nil {
					return nil, err
				}
				if a.Clear {
					if a.Status != 0 || a.ContentType != "" || a.Body != "" || len(a.Headers) > 0 {
						return nil, output.Usagef("clear takes no status, content_type, body or headers")
					}
					if err := ops.ClearStubResponse(cfg.Exe, a.Rule); err != nil {
						return nil, err
					}
					return map[string]any{"rule": a.Rule, "pid": testtools.StubPID, "action": "CLEARED"}, nil
				}
				r := ops.StubResponse{Status: a.Status, ContentType: a.ContentType, Body: a.Body, Headers: a.Headers}
				if err := ops.SetStubResponse(cfg.Exe, a.Rule, r); err != nil {
					return nil, err
				}
				return map[string]any{"rule": a.Rule, "pid": testtools.StubPID, "action": "SET"}, nil
			},
		},
	}
}
