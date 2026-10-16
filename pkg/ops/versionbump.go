package ops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/manifest"
	"github.com/cpars-innovation/cpicli/pkg/versioning"
)

// BumpOptions select the artifacts whose Bundle-Version is raised.
type BumpOptions struct {
	// Dir is the content tree (layout <package>/<artifact>, as sync writes it).
	Dir string
	// Changed bumps only artifacts whose directory changed since the commit
	// that last set their Bundle-Version (needs Git).
	Changed bool
	// Level is patch (default), minor or major.
	Level string
	// Packages and Artifacts filter by package folder and artifact ID
	// (names or path.Match patterns); empty selects all.
	Packages, Artifacts []string
	// DryRun reports without writing.
	DryRun bool
}

// Bump statuses.
const (
	BumpBumped        = "bumped"
	BumpUnchanged     = "unchanged"      // no change since the version was set
	BumpAlreadyBumped = "already_bumped" // the version differs from the one set in Since
	BumpNew           = "new"            // never committed: its version is its first one
	BumpFailed        = "failed"
)

// BumpItem is the outcome for one artifact.
type BumpItem struct {
	Artifact string `json:"artifact"`
	Package  string `json:"package,omitempty"`
	Path     string `json:"path"`
	Old      string `json:"old"`
	New      string `json:"new,omitempty"`
	Status   string `json:"status"`
	// Since is the commit that last set the version (Changed only).
	Since string `json:"since,omitempty"`
	Error string `json:"error,omitempty"`
}

// BumpResult lists every selected artifact.
type BumpResult struct {
	Items  []BumpItem     `json:"items"`
	Counts map[string]int `json:"counts"`
	DryRun bool           `json:"dryRun,omitempty"`
}

// BumpVersions raises Bundle-Version in the META-INF/MANIFEST.MF of the
// selected artifacts. Nothing is read from or written to a tenant.
func BumpVersions(ctx context.Context, o BumpOptions) (*BumpResult, error) {
	if o.Level == "" {
		o.Level = "patch"
	}
	if !slices.Contains(versioning.Levels, o.Level) {
		return nil, output.Usagef("invalid level %q (patch, minor, major)", o.Level)
	}
	for _, p := range append(slices.Clone(o.Packages), o.Artifacts...) {
		if _, err := path.Match(p, ""); err != nil {
			return nil, output.Usagef("invalid pattern %q", p)
		}
	}
	root := ""
	if o.Changed {
		top, err := Git(ctx, o.Dir, "rev-parse", "--show-toplevel")
		if err != nil {
			return nil, output.Usagef("--changed needs a Git repository: %v", err)
		}
		root = strings.TrimSpace(top)
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			root = resolved
		}
	}

	res := &BumpResult{Items: []BumpItem{}, Counts: map[string]int{}, DryRun: o.DryRun}
	var walkErr error
	err := WalkLocalArtifacts(ctx, o.Dir, func(a LocalArtifact) {
		if !MatchAny(o.Packages, a.PackageID) || !MatchAny(o.Artifacts, a.ID) {
			return
		}
		it := BumpItem{Artifact: a.ID, Package: a.PackageID, Path: a.Rel}
		it.Status, it.Error = bumpOne(ctx, root, a, o, &it)
		res.Items = append(res.Items, it)
		res.Counts[it.Status]++
	}, func(err error) { walkErr = err })
	if err != nil {
		return nil, err
	}
	if walkErr != nil {
		return nil, walkErr
	}
	if len(res.Items) == 0 && (len(o.Packages) > 0 || len(o.Artifacts) > 0) {
		return res, output.Usagef("no artifact in %s matches the filters", o.Dir)
	}
	if n := res.Counts[BumpFailed]; n > 0 {
		return res, output.Partial(fmt.Errorf("%d artifact(s) could not be bumped", n))
	}
	return res, nil
}

func bumpOne(ctx context.Context, root string, a LocalArtifact, o BumpOptions, it *BumpItem) (status, errText string) {
	current, err := manifest.Version(a.Dir)
	if err != nil {
		return BumpFailed, err.Error()
	}
	it.Old = current
	if o.Changed {
		abs, err := filepath.Abs(a.Dir)
		if err != nil {
			return BumpFailed, err.Error()
		}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			return BumpFailed, err.Error()
		}
		rel = filepath.ToSlash(rel)
		mf := rel + "/META-INF/MANIFEST.MF"
		since, err := Git(ctx, root, "log", "-1", "--format=%H", "-G", "^Bundle-Version:", "--", mf)
		if err != nil {
			return BumpFailed, err.Error()
		}
		since = strings.TrimSpace(since)
		if since == "" {
			return BumpNew, ""
		}
		it.Since = since
		committed, err := Git(ctx, root, "show", since+":"+mf)
		if err != nil {
			return BumpFailed, err.Error()
		}
		if v := strings.TrimSpace(manifest.Parse([]byte(committed))["Bundle-Version"]); versioning.Compare(v, current) != 0 {
			return BumpAlreadyBumped, "" // e.g. bumped before, not committed yet
		}
		changed, err := gitChanged(ctx, root, since, rel)
		if err != nil {
			return BumpFailed, err.Error()
		}
		if !changed {
			return BumpUnchanged, ""
		}
	}
	next, err := versioning.Bump(current, o.Level)
	if err != nil {
		return BumpFailed, err.Error()
	}
	it.New = next
	if !o.DryRun {
		if err := manifest.SetVersion(a.Dir, next); err != nil {
			return BumpFailed, err.Error()
		}
	}
	return BumpBumped, ""
}

// gitChanged reports whether dir (relative to the repository root) differs
// from commit: committed, staged, unstaged or untracked changes.
func gitChanged(ctx context.Context, root, commit, dir string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "diff", "--quiet", commit, "--", dir)
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return true, nil
		}
		return false, fmt.Errorf("git diff %s -- %s: %w", commit, dir, err)
	}
	untracked, err := Git(ctx, root, "ls-files", "--others", "--exclude-standard", "--", dir)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(untracked) != "", nil
}

// CheckGitRef refuses a Git revision that git would read as an option
// (--output=..., --remote=...) or that holds control characters: refs come
// from tool arguments.
func CheckGitRef(ref string) error {
	if ref == "" || strings.HasPrefix(ref, "-") || strings.ContainsFunc(ref, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return output.Usagef("invalid Git revision %q", ref)
	}
	return nil
}

func Git(ctx context.Context, dir string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return stdout.String(), nil
}

func MatchAny(patterns []string, name string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		if ok, _ := path.Match(p, name); ok {
			return true
		}
	}
	return false
}
