package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func NewScriptDocCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "script-doc [file...]",
		Short: "Check the comments of Groovy scripts against the script doc template",
		Long: `Checks what the comments of Groovy scripts say, offline: a first sentence that
says what the script does, and for scripts of a script collection (found by the
MANIFEST.MF of the artifact folder) a description per public function, every
header and property the code reads or writes named with @reads / @writes, and
@keywords. These are the facts discover and the graph use to find scripts for
reuse; lint reports the same as script-doc, collection-script-doc,
collection-script-doc-quality and collection-script-keywords
(docs/script-docs.md).

The template (one comment per public function, or one at the top of a flow
script):

` + ops.ScriptDocTemplate + `

Tags: @reads and @writes ("header X", "property Y", or "body" with what is
expected), @body (what happens to the body), @param and @return for helper
functions, @throws, @keywords (comma-separated words someone would search
for), @example (how to call it), @deprecated (what to use instead).

With --hook it runs as a Claude Code PostToolUse hook: it reads the hook's
JSON from standard input, checks the file the tool wrote when it is a Groovy
script and, when something is missing, writes it to standard error with exit
code 2, which Claude Code hands to the agent. Other files and complete
comments end silently with 0. The cpi plugin installs this hook.`,
		Example: `  cpictl script-doc packages/Common/Common_Scripts/src/main/resources/script/DateUtils.groovy
  cpictl script-doc --output json script/*.groovy
  cpictl script-doc --hook < hook-input.json`,
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true", annotationNoStats: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if hook, _ := cmd.Flags().GetBool("hook"); hook {
				if msg := scriptDocHook(cmd.InOrStdin()); msg != "" {
					fmt.Fprint(cmd.ErrOrStderr(), msg)
					os.Exit(2) // Claude Code shows stderr of exit code 2 to the agent
				}
				return nil
			}
			if len(args) == 0 {
				return output.Usagef("name one or more script files")
			}
			var reports []*ops.ScriptDocReport
			incomplete := 0
			for _, f := range args {
				r, err := ops.CheckScriptFile(f)
				if err != nil {
					return err
				}
				reports = append(reports, r)
				kind := "flow script"
				if r.Collection {
					kind = "script collection"
				}
				log.Info().Msgf("%s (%s): %s", f, kind, r.Status)
				for _, i := range r.Issues {
					log.Warn().Msgf("  %s", i.Message)
				}
				if r.Status != ops.DocOK {
					incomplete++
				}
			}
			output.SetResult(cmd.Context(), map[string]any{"scripts": reports})
			if fail, _ := cmd.Flags().GetBool("fail"); fail && incomplete > 0 {
				return output.Failed(fmt.Errorf("%d of %d scripts have incomplete comments", incomplete, len(reports)))
			}
			return nil
		},
	}
	c.Flags().Bool("hook", false, "Run as a Claude Code PostToolUse hook (JSON on standard input)")
	c.Flags().Bool("fail", false, "Exit with code 5 when a script's comments are incomplete")
	return c
}

// scriptDocHook reads one PostToolUse event and returns what the agent
// should be told about the Groovy script it wrote ("" when nothing).
func scriptDocHook(in io.Reader) string {
	var ev struct {
		Cwd       string `json:"cwd"`
		ToolInput struct {
			FilePath string `json:"file_path"`
		} `json:"tool_input"`
	}
	if err := json.NewDecoder(io.LimitReader(in, 1<<20)).Decode(&ev); err != nil {
		return ""
	}
	file := ev.ToolInput.FilePath
	if file == "" || !ops.IsGroovyFile(file) {
		return ""
	}
	if !filepath.IsAbs(file) && ev.Cwd != "" {
		file = filepath.Join(ev.Cwd, file)
	}
	r, err := ops.CheckScriptFile(file)
	if err != nil || len(r.Issues) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "cpictl script-doc: the comments of %s are incomplete (template: cpictl help script-doc, docs/script-docs.md):\n", filepath.Base(file))
	for _, i := range r.Issues {
		fmt.Fprintf(&b, "- %s\n", i.Message)
	}
	return b.String()
}
