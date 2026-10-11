package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// An autonomous build loop needs limits the model cannot talk its way past.
// The ledger counts the tenant calls of an open loop on the server and refuses
// further changes once a limit is reached; read tools keep working, so the
// agent can still report.

// LoopLimits are the stop conditions of a loop.
type LoopLimits struct {
	MaxIterations    int `json:"maxIterations"`
	SameErrorLimit   int `json:"sameErrorLimit"`
	WallClockMinutes int `json:"wallClockMinutes"`
	MaxDeploys       int `json:"maxDeploys"`
}

// DefaultLoopLimits apply to omitted limits.
var DefaultLoopLimits = LoopLimits{MaxIterations: 5, SameErrorLimit: 2, WallClockMinutes: 60, MaxDeploys: 15}

// LoopPolicy is the server's loop policy (cpictl mcp --loop-*): the model
// cannot change it.
type LoopPolicy struct {
	// Required refuses tenant tools while no loop is open (errorCategory
	// loop_required).
	Required bool
	// Caps are the highest limits loop_start accepts (0: no cap). Omitted
	// limits get the default, lowered to the cap; a higher value is a usage
	// error naming the cap.
	Caps LoopLimits
}

// LoopRequiredError refuses a tenant call because no loop is open
// (LoopPolicy.Required). Exit code usage, errorCategory loop_required.
type LoopRequiredError struct{ Tool string }

func (e *LoopRequiredError) Error() string {
	return "loop required: " + e.Tool + " changes the tenant; this server only allows that inside a loop: call loop_start first"
}

// ExitCode implements output.ExitCoder.
func (e *LoopRequiredError) ExitCode() int { return exitcode.Usage }

// ErrorCategory is the errorCategory of the tool result.
func (e *LoopRequiredError) ErrorCategory() string { return "loop_required" }

// LoopPolicyInstructions describes the loop policy for the server
// instructions.
func LoopPolicyInstructions(p LoopPolicy) string {
	var b strings.Builder
	if p.Required {
		b.WriteString("\n\nLoop required: tools that change the tenant or trigger processing are refused (errorCategory loop_required) until loop_start opened a loop.")
	}
	var caps []string
	for _, c := range []struct {
		name string
		cap  int
	}{{"max_iterations", p.Caps.MaxIterations}, {"same_error_limit", p.Caps.SameErrorLimit}, {"wall_clock_minutes", p.Caps.WallClockMinutes}, {"max_deploys", p.Caps.MaxDeploys}} {
		if c.cap > 0 {
			caps = append(caps, fmt.Sprintf("%s %d", c.name, c.cap))
		}
	}
	if len(caps) > 0 {
		b.WriteString("\n\nLoop limits are capped by the server: " + strings.Join(caps, ", ") + "; loop_start refuses higher values.")
	}
	return b.String()
}

// LoopCall is one tool call recorded in a loop.
type LoopCall struct {
	Time     time.Time `json:"time"`
	Tool     string    `json:"tool"`
	OK       bool      `json:"ok"`
	Category string    `json:"errorCategory,omitempty"`
	Summary  string    `json:"summary,omitempty"`
}

// LoopStatus is the state of a loop.
type LoopStatus struct {
	ID         string     `json:"loopId"`
	Goal       string     `json:"goal"`
	Limits     LoopLimits `json:"limits"`
	Started    time.Time  `json:"started"`
	Steps      int        `json:"steps"`
	Iterations int        `json:"iterations"`
	Deploys    int        `json:"deploys"`
	// Errors counts each error fingerprint seen in failed test results.
	Errors  map[string]int `json:"errors"`
	Stopped string         `json:"stopped,omitempty"`
	Ended   bool           `json:"ended"`
	Outcome string         `json:"outcome,omitempty"`
}

type loop struct {
	LoopStatus
	calls []LoopCall
	// failedSinceDeploy is set by a failed test result; the next deploy is
	// then a new iteration.
	failedSinceDeploy bool
	// touched are the artifacts uploaded, deployed or sent to in the loop:
	// their failures count toward same_error_limit whatever their time.
	touched map[string]bool
}

// StoppedError refuses a tenant call because a loop limit was reached.
type StoppedError struct{ Reason string }

func (e *StoppedError) Error() string {
	return "loop stopped: " + e.Reason + " (read tools still work; call loop_end and report to the user)"
}

// ExitCode implements output.ExitCoder.
func (e *StoppedError) ExitCode() int { return exitcode.Stopped }

// Ledger tracks the open loop of a server (at most one at a time).
type Ledger struct {
	// Policy is the server's loop policy; set it before Wrap (the loop_start
	// schema shows the caps).
	Policy LoopPolicy

	mu    sync.Mutex
	root  string
	now   func() time.Time
	loops map[string]*loop
	open  *loop
}

// NewLedger returns a ledger writing to <root>/.cpi/loops.
func NewLedger(root string) *Ledger {
	return &Ledger{root: root, now: time.Now, loops: map[string]*loop{}}
}

func (l *Ledger) dir() string { return filepath.Join(l.root, ".cpi", "loops") }

func (l *Ledger) start(goal string, limits LoopLimits) (*LoopStatus, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.open != nil {
		return nil, output.Usagef("loop %s is still open: call loop_end first", l.open.ID)
	}
	if strings.TrimSpace(goal) == "" {
		return nil, output.Usagef("goal is required")
	}
	d := DefaultLoopLimits
	caps := l.Policy.Caps
	for _, f := range []struct {
		value     *int
		requested int
		cap       int
		param     string
		flag      string
	}{
		{&d.MaxIterations, limits.MaxIterations, caps.MaxIterations, "max_iterations", "--loop-max-iterations"},
		{&d.SameErrorLimit, limits.SameErrorLimit, caps.SameErrorLimit, "same_error_limit", "--loop-max-same-errors"},
		{&d.WallClockMinutes, limits.WallClockMinutes, caps.WallClockMinutes, "wall_clock_minutes", "--loop-max-minutes"},
		{&d.MaxDeploys, limits.MaxDeploys, caps.MaxDeploys, "max_deploys", "--loop-max-deploys"},
	} {
		if f.requested > 0 {
			if f.cap > 0 && f.requested > f.cap {
				return nil, output.Usagef("%s %d exceeds this server's cap of %d (%s); use at most %d", f.param, f.requested, f.cap, f.flag, f.cap)
			}
			*f.value = f.requested
		} else if f.cap > 0 && *f.value > f.cap {
			*f.value = f.cap
		}
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	now := l.now().UTC()
	lp := &loop{LoopStatus: LoopStatus{ID: now.Format("20060102-150405") + "-" + hex.EncodeToString(b), Goal: goal, Limits: d, Started: now, Errors: map[string]int{}}, touched: map[string]bool{}}
	if err := os.MkdirAll(l.dir(), 0o755); err != nil {
		return nil, err
	}
	l.loops[lp.ID], l.open = lp, lp
	st := lp.LoopStatus
	return &st, nil
}

func (l *Ledger) status(id string) (*LoopStatus, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	lp, ok := l.loops[id]
	if !ok {
		return nil, output.Usagef("unknown loop %q", id)
	}
	l.checkClock(lp)
	st := lp.LoopStatus
	st.Errors = copyMap(lp.Errors)
	return &st, nil
}

func copyMap(m map[string]int) map[string]int {
	c := make(map[string]int, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func (l *Ledger) checkClock(lp *loop) {
	if lp.Stopped == "" && !lp.Ended && l.now().Sub(lp.Started) > time.Duration(lp.Limits.WallClockMinutes)*time.Minute {
		lp.Stopped = fmt.Sprintf("wall clock limit of %d minutes reached", lp.Limits.WallClockMinutes)
	}
}

// before is called before a tool runs; it refuses tenant calls of a stopped
// loop and deployments over the limits.
func (l *Ledger) before(tool string, args json.RawMessage) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	lp := l.open
	if EffectOf(tool) != EffectTenant {
		return nil
	}
	if lp == nil {
		if l.Policy.Required {
			return &LoopRequiredError{Tool: tool}
		}
		return nil
	}
	l.checkClock(lp)
	if lp.Stopped != "" {
		return &StoppedError{Reason: lp.Stopped}
	}
	if isDeployCall(tool, args) {
		if lp.Deploys >= lp.Limits.MaxDeploys {
			lp.Stopped = fmt.Sprintf("max_deploys (%d) reached", lp.Limits.MaxDeploys)
			return &StoppedError{Reason: lp.Stopped}
		}
		if lp.failedSinceDeploy && lp.Iterations >= lp.Limits.MaxIterations {
			lp.Stopped = fmt.Sprintf("max_iterations (%d) reached", lp.Limits.MaxIterations)
			return &StoppedError{Reason: lp.Stopped}
		}
	}
	return nil
}

// isDeployCall reports deployments that count against the limits: deploy
// unless it is a dry run, and pd_deploy unless it is a dry run (its
// default).
func isDeployCall(tool string, args json.RawMessage) bool {
	return (tool == "deploy" || tool == "pd_deploy") && !isDryRun(tool, args)
}

// isDryRun reports a call that only predicts and changes nothing: dry_run
// true, or for pd_deploy dry_run omitted.
func isDryRun(tool string, args json.RawMessage) bool {
	var a struct {
		DryRun *bool `json:"dry_run"`
	}
	_ = json.Unmarshal(args, &a)
	if a.DryRun == nil {
		return tool == "pd_deploy"
	}
	return *a.DryRun
}

// touchedArtifacts returns the artifacts a tenant call works on: artifact_id,
// artifact_ids and artifacts[].artifact_id of its arguments.
func touchedArtifacts(args json.RawMessage) []string {
	var a struct {
		ArtifactID  string   `json:"artifact_id"`
		ArtifactIDs []string `json:"artifact_ids"`
		Artifacts   []struct {
			ArtifactID string `json:"artifact_id"`
		} `json:"artifacts"`
	}
	_ = json.Unmarshal(args, &a)
	ids := append([]string{a.ArtifactID}, a.ArtifactIDs...)
	for _, x := range a.Artifacts {
		ids = append(ids, x.ArtifactID)
	}
	return slices.DeleteFunc(ids, func(id string) bool { return id == "" })
}

// after records a tool call of the open loop.
func (l *Ledger) after(tool string, args json.RawMessage, value any, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	lp := l.open
	if lp == nil || strings.HasPrefix(tool, "loop_") {
		return
	}
	var stopped *StoppedError
	refused := false
	if e, ok := err.(*StoppedError); ok {
		stopped, refused = e, true
	}
	call := LoopCall{Time: l.now().UTC(), Tool: tool, OK: err == nil}
	if err != nil {
		call.Category = errorCategory(err)
		call.Summary = firstLine(err.Error())
	}
	if !refused && EffectOf(tool) == EffectTenant {
		if !isDryRun(tool, args) {
			for _, id := range touchedArtifacts(args) {
				lp.touched[id] = true
			}
		}
		lp.Steps++
		if isDeployCall(tool, args) {
			lp.Deploys++
			if lp.failedSinceDeploy {
				lp.Iterations++
				lp.failedSinceDeploy = false
			}
		}
	}
	if fp, failed := lp.failureFingerprint(value, err); failed && stopped == nil {
		lp.failedSinceDeploy = true
		if fp != "" {
			lp.Errors[fp]++
			if lp.Errors[fp] >= lp.Limits.SameErrorLimit && lp.Stopped == "" {
				lp.Stopped = fmt.Sprintf("the same error occurred %d times (same_error_limit)", lp.Errors[fp])
			}
		}
	}
	lp.calls = append(lp.calls, call)
	l.appendJSONL(lp, call)
}

func (l *Ledger) appendJSONL(lp *loop, call LoopCall) {
	f, err := os.OpenFile(filepath.Join(l.dir(), lp.ID+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_ = json.NewEncoder(f).Encode(call)
}

func fingerprint(artifactID, modelStepID, errorText string) string {
	return ops.ErrorFingerprint(artifactID, modelStepID, errorText)
}

// failureFingerprint reports whether a test or diagnosis result shows a
// failed message that belongs to the loop, and its fingerprint. A message
// belongs to the loop when it started or ended after loop_start, or when its
// artifact was uploaded, deployed or sent to in the loop; older failures of
// other artifacts (a diagnosis reading history) do not count.
func (lp *loop) failureFingerprint(value any, err error) (string, bool) {
	switch v := value.(type) {
	case *ops.SentMessage:
		if err == nil || v == nil {
			return "", false
		}
		lp.touched[v.ArtifactID] = true // sent in the loop
		text := err.Error()
		if v.Log != nil && v.Log.ErrorText != "" {
			text = v.Log.ErrorText
		}
		return fingerprint(v.ArtifactID, "", text), true
	case *ops.TraceTree:
		if v == nil || v.FirstFailure == nil {
			return "", false
		}
		f := v.FirstFailure
		start, end := traceNodeTimes(v.Roots, f.MessageGuid)
		if !lp.belongs(f.ArtifactID, start, end) {
			return "", false
		}
		return fingerprint(f.ArtifactID, "", f.ErrorText), true
	case *ops.MessageLogList:
		if v == nil {
			return "", false
		}
		for _, m := range v.Logs {
			if (m.Status == "FAILED" || m.Status == "ESCALATED") && lp.belongs(m.ArtifactID, m.LogStart, m.LogEnd) {
				return fingerprint(m.ArtifactID, "", m.ErrorText), true
			}
		}
	}
	return "", false
}

// belongs reports whether a message belongs to the loop (see
// failureFingerprint).
func (lp *loop) belongs(artifactID string, start, end *time.Time) bool {
	if lp.touched[artifactID] {
		return true
	}
	for _, t := range []*time.Time{end, start} {
		if t != nil && !t.Before(lp.Started) {
			return true
		}
	}
	return false
}

// traceNodeTimes returns the start and end of a message of a trace tree.
func traceNodeTimes(nodes []*ops.TraceNode, guid string) (start, end *time.Time) {
	for _, n := range nodes {
		if n.MessageGuid == guid {
			return n.LogStart, n.LogEnd
		}
		if s, e := traceNodeTimes(n.Children, guid); s != nil || e != nil {
			return s, e
		}
	}
	return nil, nil
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func (l *Ledger) end(id, outcome string) (map[string]any, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	lp, ok := l.loops[id]
	if !ok {
		return nil, output.Usagef("unknown loop %q", id)
	}
	lp.Ended, lp.Outcome = true, outcome
	if l.open == lp {
		l.open = nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Loop %s\n\n**Goal:** %s\n\n**Outcome:** %s\n\n", lp.ID, lp.Goal, outcome)
	if lp.Stopped != "" {
		fmt.Fprintf(&b, "**Stopped:** %s\n\n", lp.Stopped)
	}
	fmt.Fprintf(&b, "| Counter | Value | Limit |\n|---|---|---|\n| Steps | %d | |\n| Iterations | %d | %d |\n| Deploys | %d | %d |\n| Duration | %s | %d min |\n\n",
		lp.Steps, lp.Iterations, lp.Limits.MaxIterations, lp.Deploys, lp.Limits.MaxDeploys,
		l.now().Sub(lp.Started).Round(time.Second), lp.Limits.WallClockMinutes)
	b.WriteString("| Time | Tool | Result | Detail |\n|---|---|---|---|\n")
	for _, c := range lp.calls {
		result := "ok"
		if !c.OK {
			result = c.Category
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", c.Time.Format(time.TimeOnly), c.Tool, result, strings.ReplaceAll(c.Summary, "|", "\\|"))
	}
	file := filepath.Join(l.dir(), lp.ID+".md")
	if err := os.MkdirAll(l.dir(), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(file, []byte(b.String()), 0o644); err != nil {
		return nil, err
	}
	rel, _ := filepath.Rel(l.root, file)
	st := lp.LoopStatus
	return map[string]any{"status": st, "summaryFile": filepath.ToSlash(rel)}, nil
}

// Wrap returns the tools with ledger accounting and adds the loop tools.
func (l *Ledger) Wrap(tools []Tool) []Tool {
	out := make([]Tool, 0, len(tools)+3)
	for _, t := range tools {
		t := t
		inner := t.Handler
		t.Handler = func(ctx context.Context, raw json.RawMessage) (any, error) {
			if err := l.before(t.Name, raw); err != nil {
				l.after(t.Name, raw, nil, err)
				return nil, err
			}
			v, err := inner(ctx, raw)
			l.after(t.Name, raw, v, err)
			return v, err
		}
		out = append(out, t)
	}
	return append(out, l.tools()...)
}

// limitSchema is the schema of a loop_start limit: at most the server's cap.
func limitSchema(max, def, cap int, desc string) map[string]any {
	if cap > 0 {
		max, def = min(max, cap), min(def, cap)
		desc += fmt.Sprintf(", default %d, at most %d on this server", def, max)
	} else {
		desc += fmt.Sprintf(", default %d", def)
	}
	return map[string]any{"type": "integer", "minimum": 1, "maximum": max, "description": desc}
}

func (l *Ledger) tools() []Tool {
	caps, d := l.Policy.Caps, DefaultLoopLimits
	description := "Open a build/test/fix loop before changing anything on the tenant. While it is open, the server counts tenant calls and refuses further changes " +
		"(errorCategory stopped) when max_iterations (deploys after a failed test), same_error_limit (the same failure of this loop seen again), wall_clock_minutes or max_deploys is reached. " +
		"Read tools keep working. Returns loop_id; finish with loop_end."
	if l.Policy.Required {
		description += " This server refuses tenant tools without an open loop (errorCategory loop_required)."
	}
	return []Tool{
		{
			Name: "loop_start", Title: "Start a build loop with limits",
			Description: description,
			InputSchema: object(props{
				"goal":               str("What the loop should achieve"),
				"max_iterations":     limitSchema(50, d.MaxIterations, caps.MaxIterations, "Deploys after a failed test"),
				"same_error_limit":   limitSchema(20, d.SameErrorLimit, caps.SameErrorLimit, "Stop when the same error of this loop was seen this often"),
				"wall_clock_minutes": limitSchema(480, d.WallClockMinutes, caps.WallClockMinutes, "Maximum duration in minutes"),
				"max_deploys":        limitSchema(100, d.MaxDeploys, caps.MaxDeploys, "Maximum deploy / pd_deploy calls (dry runs do not count)"),
			}, "goal"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": false},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Goal             string `json:"goal"`
					MaxIterations    int    `json:"max_iterations"`
					SameErrorLimit   int    `json:"same_error_limit"`
					WallClockMinutes int    `json:"wall_clock_minutes"`
					MaxDeploys       int    `json:"max_deploys"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				return l.start(a.Goal, LoopLimits{MaxIterations: a.MaxIterations, SameErrorLimit: a.SameErrorLimit, WallClockMinutes: a.WallClockMinutes, MaxDeploys: a.MaxDeploys})
			},
		},
		{
			Name: "loop_status", Title: "Get the counters of a loop",
			Description: "Counters and limits of a loop: steps, iterations, deploys, error fingerprints and why it stopped, if it did.",
			InputSchema: object(props{"loop_id": str("Loop ID from loop_start")}, "loop_id"),
			Annotations: map[string]any{"readOnlyHint": true, "openWorldHint": false},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					LoopID string `json:"loop_id"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				return l.status(a.LoopID)
			},
		},
		{
			Name: "loop_end", Title: "End a loop and write its summary",
			Description: "Close a loop (always allowed, also after a stop) and write .cpi/loops/<loop_id>.md: goal, outcome, counters and every tool call with its result. Give the summary to the user.",
			InputSchema: object(props{
				"loop_id": str("Loop ID from loop_start"),
				"outcome": str("What was achieved, or why the loop ended"),
			}, "loop_id", "outcome"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					LoopID  string `json:"loop_id"`
					Outcome string `json:"outcome"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				return l.end(a.LoopID, a.Outcome)
			},
		},
	}
}
