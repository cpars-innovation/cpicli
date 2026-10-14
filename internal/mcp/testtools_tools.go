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
				"only":                  enum("Install only one tool (default all)", testtools.ToolHarness, testtools.ToolStub, testtools.ToolXSLT),
				"deploy":                boolean("Deploy the artifacts and wait for them (default false)"),
				"role":                  str("User role a runtime client needs to call the tools (their HTTPS senders' externalised role, set before the deploy). Default: keep the configured one (" + testtools.DefaultRole + " on a first install). A tenant-specific role keeps arbitrary ESBMessaging.send clients off the harness"),
				"poll_interval_seconds": integer("Seconds between deployment status checks"),
				"max_checks":            integer("Maximum number of status checks per artifact"),
			}),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					PackageID    string `json:"package_id"`
					Only         string `json:"only"`
					Deploy       bool   `json:"deploy"`
					Role         string `json:"role"`
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
				res, err := ops.InstallTestTools(ctx, cfg.Exe, ops.TestToolsOptions{PackageID: a.PackageID, Only: a.Only, Deploy: a.Deploy, Role: a.Role, DeployOptions: opts})
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
				"delay_ms":     map[string]any{"type": "integer", "minimum": 0, "maximum": ops.MaxStubDelayMs, "description": "Delay the answer (milliseconds), e.g. to test receiver timeouts"},
				"when": map[string]any{"type": "array", "description": "Conditional answers: the first entry whose conditions all hold answers instead; the answer fields it sets override the rule's. The recorded call names it (matched).",
					"items": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
						"name":          str("Name reported as matched (default when[<index>])"),
						"path_contains": str("Condition: the path after /cpictl/stub/ contains this"),
						"header":        map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Condition: request headers with these values (names case-insensitive)"},
						"body_contains": str("Condition: the request body contains this"),
						"status":        map[string]any{"type": "integer", "minimum": 100, "maximum": 599},
						"content_type":  str("Content-Type of this answer"),
						"body":          str("Body of this answer"),
						"headers":       map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
						"delay_ms":      map[string]any{"type": "integer", "minimum": 0, "maximum": ops.MaxStubDelayMs},
					}}},
				"clear": boolean("Remove the rule's answer instead (the stub then answers 200 with {})"),
			}, "rule"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Rule        string            `json:"rule"`
					Status      int               `json:"status"`
					ContentType string            `json:"content_type"`
					Body        string            `json:"body"`
					Headers     map[string]string `json:"headers"`
					DelayMs     int               `json:"delay_ms"`
					When        []struct {
						Name         string            `json:"name"`
						PathContains string            `json:"path_contains"`
						Header       map[string]string `json:"header"`
						BodyContains string            `json:"body_contains"`
						Status       int               `json:"status"`
						ContentType  string            `json:"content_type"`
						Body         *string           `json:"body"`
						Headers      map[string]string `json:"headers"`
						DelayMs      int               `json:"delay_ms"`
					} `json:"when"`
					Clear bool `json:"clear"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if err := ops.GuardProduction(cfg.Tier, cfg.AllowProduction, "set_stub_response"); err != nil {
					return nil, err
				}
				if a.Clear {
					if a.Status != 0 || a.ContentType != "" || a.Body != "" || len(a.Headers) > 0 || a.DelayMs != 0 || len(a.When) > 0 {
						return nil, output.Usagef("clear takes no status, content_type, body, headers, delay_ms or when")
					}
					if err := ops.ClearStubResponse(cfg.Exe, a.Rule); err != nil {
						return nil, err
					}
					return map[string]any{"rule": a.Rule, "pid": testtools.StubPID, "action": "CLEARED"}, nil
				}
				r := ops.StubResponse{Status: a.Status, ContentType: a.ContentType, Body: a.Body, Headers: a.Headers, DelayMs: a.DelayMs}
				for _, w := range a.When {
					r.When = append(r.When, ops.StubWhen{Name: w.Name, PathContains: w.PathContains, Header: w.Header, BodyContains: w.BodyContains,
						Status: w.Status, ContentType: w.ContentType, Body: w.Body, Headers: w.Headers, DelayMs: w.DelayMs})
				}
				if err := ops.SetStubResponse(cfg.Exe, a.Rule, r); err != nil {
					return nil, err
				}
				return map[string]any{"rule": a.Rule, "pid": testtools.StubPID, "action": "SET"}, nil
			},
		},
	}
}
