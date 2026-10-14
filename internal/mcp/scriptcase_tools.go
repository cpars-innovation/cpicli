package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/scripttest"
	"github.com/cpars-innovation/cpicli/pkg/testrun"
)

// scriptCaseTools is run_script_cases: the script cases of .cpi/tests run
// on the SAP script API stand-ins (local; nothing is sent to a tenant).
func scriptCaseTools(cfg Config) []Tool {
	return []Tool{{
		Name: "run_script_cases", Title: "Run script cases on the stand-ins",
		Description: "Run script cases locally: each calls one function of a script with a message and checks body, headers, properties, return value, exception and message log. " +
			"Cases: .cpi/tests/<FlowId>/scripts/<script file>/<case>.yaml (a flow's script step) and .cpi/tests/collections/<CollectionId>/<case>.yaml (a collection function); " +
			"format docs/script-case.schema.json (shared with CPI Studio). pd, value_mappings, secure and keystore set what SAP's services answer (test values only). " +
			"Runs the Gradle task cpictlScriptCases of the project's build.gradle (cpictl test scripts init writes it and the stand-ins); needs a JDK and Gradle. " +
			"Nothing is sent to a tenant. A case in error with errorCategory unsupported uses a part of SAP's API the stand-ins lack: test that on the tenant.",
		InputSchema: object(props{
			"dir":     str(`Directory of the test cases, relative to the server root, default "` + testrun.DefaultDir + `"`),
			"content": str(`Content tree with the flows and script collections, relative to the server root, default "packages"`),
			"flow":    str("Only these cases: a flow, <FlowId>/<script> or a collection (pattern allowed)"),
			"case":    str("Only this case (file name without .yaml; pattern allowed)"),
			"tags":    strArray("Only cases with one of these tags"),
		}),
		Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "openWorldHint": false},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a struct {
				Dir     string   `json:"dir"`
				Content string   `json:"content"`
				Flow    string   `json:"flow"`
				Case    string   `json:"case"`
				Tags    []string `json:"tags"`
			}
			if err := decode(raw, &a); err != nil {
				return nil, err
			}
			root, err := resolvePath(cfg.Root, ".")
			if err != nil {
				return nil, err
			}
			dir, err := resolvePath(cfg.Root, cmpOr(a.Dir, testrun.DefaultDir))
			if err != nil {
				return nil, err
			}
			content, err := resolvePath(cfg.Root, cmpOr(a.Content, "packages"))
			if err != nil {
				return nil, err
			}
			cases, err := testrun.LoadScriptCases(dir, filterOf(a.Flow, a.Case, a.Tags))
			if err != nil {
				return nil, err
			}
			if len(cases) == 0 {
				return nil, output.Usagef("no script case matches (write them as %s/<FlowId>/scripts/<script>/<case>.yaml or %s/collections/<CollectionId>/<case>.yaml)", testrun.DefaultDir, testrun.DefaultDir)
			}
			res, err := scripttest.RunCases(ctx, cases, scripttest.CaseOptions{Project: root, Content: content})
			if err != nil {
				return nil, err
			}
			for i := range res.Cases {
				if r, err := filepath.Rel(root, res.Cases[i].File); err == nil && !strings.HasPrefix(r, "..") {
					res.Cases[i].File = filepath.ToSlash(r)
				}
			}
			return res, nil
		},
	}}
}

func cmpOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
