package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"gopkg.in/yaml.v3"
)

// BranchPolicy makes the server read-only while a repository is on a
// protected branch (cpictl mcp --branch-policy): the agent may change the
// tenant from a feature branch, not from main or a release branch. It is
// judged on every call of a tool that changes the tenant, so a checkout
// during the session counts. Without a policy nothing is checked.
type BranchPolicy struct {
	// Default "readonly" makes the server read-only whatever the branch;
	// "readwrite" (or empty) leaves it to the branches.
	Default string `yaml:"default" json:"default"`
	// ReadonlyBranches are branch name patterns ('*' matches any
	// characters, also '/').
	ReadonlyBranches []string `yaml:"readonlyBranches" json:"readonlyBranches"`
	// Repos are the checkouts to judge, relative to the server root
	// (default "."). The root checkout on a detached HEAD is read-only;
	// other repos on a detached HEAD (submodules at their pinned commit) and
	// missing ones are not judged.
	Repos []string `yaml:"repos" json:"repos"`
	// WritableEnvironments are the tiers (--tier) the server may ever
	// change; a server with another tier is read-only. Not judged without
	// a tier.
	WritableEnvironments []string `yaml:"writableEnvironments" json:"writableEnvironments"`

	// File is where the policy was read from.
	File       string `yaml:"-" json:"file"`
	root, tier string
	patterns   []*regexp.Regexp
	// branchOf returns the branch of a checkout ("HEAD" when detached).
	branchOf func(dir string) (string, error)
}

// LoadBranchPolicy reads a policy file (YAML or JSON). root is the server
// root the repos are relative to, tier the server's tier.
func LoadBranchPolicy(file, root, tier string) (*BranchPolicy, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, output.Usagef("branch policy: %v", err)
	}
	p := &BranchPolicy{}
	if err := yaml.Unmarshal(data, p); err != nil {
		return nil, output.Usagef("branch policy %s: %v", file, err)
	}
	if p.Default != "" && p.Default != "readonly" && p.Default != "readwrite" {
		return nil, output.Usagef("branch policy %s: default must be readonly or readwrite, not %q", file, p.Default)
	}
	p.File, p.root, p.tier = file, root, tier
	if len(p.Repos) == 0 {
		p.Repos = []string{"."}
	}
	for _, b := range p.ReadonlyBranches {
		p.patterns = append(p.patterns, regexp.MustCompile("^"+strings.ReplaceAll(regexp.QuoteMeta(b), `\*`, ".*")+"$"))
	}
	p.branchOf = gitBranch
	return p, nil
}

func gitBranch(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// BranchDecision is the policy's verdict now.
type BranchDecision struct {
	ReadOnly bool              `json:"readOnly"`
	Reasons  []string          `json:"reasons,omitempty"`
	Branches map[string]string `json:"branches"`
}

// Decide judges the current branches.
func (p *BranchPolicy) Decide() BranchDecision {
	d := BranchDecision{Branches: map[string]string{}}
	for _, repo := range p.Repos {
		dir := filepath.Join(p.root, repo)
		branch, err := p.branchOf(dir)
		switch {
		case err != nil && repo == ".":
			// the policy cannot be judged: fail closed
			d.Reasons = append(d.Reasons, fmt.Sprintf("the branch of %s cannot be read (%v)", p.root, err))
			continue
		case err != nil:
			continue
		}
		d.Branches[repo] = branch
		switch {
		case branch == "HEAD" && repo == ".":
			d.Reasons = append(d.Reasons, repo+" is on a detached HEAD")
		case branch != "HEAD" && slices.ContainsFunc(p.patterns, func(re *regexp.Regexp) bool { return re.MatchString(branch) }):
			d.Reasons = append(d.Reasons, fmt.Sprintf("%s is on the read-only branch %s", repo, branch))
		}
	}
	if p.tier != "" && len(p.WritableEnvironments) > 0 && !slices.Contains(p.WritableEnvironments, p.tier) {
		d.Reasons = append(d.Reasons, "tier "+p.tier+" is never writable (writableEnvironments)")
	}
	if p.Default == "readonly" {
		d.Reasons = append(d.Reasons, "the policy default is read-only")
	}
	d.ReadOnly = len(d.Reasons) > 0
	return d
}

// BranchReadOnlyError refuses a tenant change under the branch policy.
// Exit code usage, errorCategory branch_readonly.
type BranchReadOnlyError struct {
	Tool    string
	Reasons []string
}

func (e *BranchReadOnlyError) Error() string {
	return "read-only by branch policy: " + e.Tool + " changes the tenant, but " + strings.Join(e.Reasons, "; ") +
		". Switch to a branch that allows changes, or ask the user"
}

// ExitCode implements output.ExitCoder.
func (e *BranchReadOnlyError) ExitCode() int { return exitcode.Usage }

// ErrorCategory is the errorCategory of the tool result.
func (e *BranchReadOnlyError) ErrorCategory() string { return "branch_readonly" }

// Wrap refuses the tools that change the tenant while the policy says
// read-only.
func (p *BranchPolicy) Wrap(tools []Tool) []Tool {
	out := make([]Tool, 0, len(tools))
	for _, t := range tools {
		t := t
		if EffectOf(t.Name) == EffectTenant {
			inner := t.Handler
			t.Handler = func(ctx context.Context, raw json.RawMessage) (any, error) {
				if d := p.Decide(); d.ReadOnly {
					return nil, &BranchReadOnlyError{Tool: t.Name, Reasons: d.Reasons}
				}
				return inner(ctx, raw)
			}
		}
		out = append(out, t)
	}
	return out
}

// Instructions describes the policy for the server instructions.
func (p *BranchPolicy) Instructions() string {
	d := p.Decide()
	state := "tenant changes are allowed now"
	if d.ReadOnly {
		state = "read-only now: " + strings.Join(d.Reasons, "; ")
	}
	return "\n\nBranch policy: tools that change the tenant are refused (errorCategory branch_readonly) while a repository is on a read-only branch (" +
		strings.Join(p.ReadonlyBranches, ", ") + "); it is checked on every call. " + state + "."
}
