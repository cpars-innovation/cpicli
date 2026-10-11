package snapshot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/rs/zerolog/log"
)

// Default commit author: the GitHub Actions bot.
const (
	DefaultAuthorName  = "github-actions[bot]"
	DefaultAuthorEmail = "41898282+github-actions[bot]@users.noreply.github.com"
)

// CommitOptions control Commit.
type CommitOptions struct {
	// Message of the commit (empty: "Tenant snapshot of <current time>").
	Message string
	// AuthorName and AuthorEmail of the commit (empty: the GitHub Actions
	// bot); the author is the committer too.
	AuthorName, AuthorEmail string
	// Paths limits the commit to these files and folders (relative to the
	// directory given to Commit, or absolute; inside it), e.g. Result.Paths
	// and the state file. Nil: every change below the directory.
	Paths []string
}

// CommitResult is the outcome of Commit.
type CommitResult struct {
	// Hash of the new commit; empty when there was nothing to commit.
	Hash string `json:"hash,omitempty"`
	// Root is the working tree of the repository (dir or a parent of it).
	Root string `json:"root"`
	// Files is the number of files added, changed or removed.
	Files int `json:"files"`
}

// Commit commits the changes below dir, which is the root of a Git working
// tree or a folder inside one (like `git rev-parse --show-toplevel`, the
// repository is looked up in dir and its parents). Only changes below dir
// (or below CommitOptions.Paths) are staged; changes elsewhere in the
// working tree are left alone, and Commit refuses when the index already
// holds staged changes outside them. Nothing is pushed.
func Commit(dir string, o CommitOptions) (*CommitResult, error) {
	absDir, err := realPath(dir)
	if err != nil {
		return nil, err
	}
	log.Info().Msgf("Opening Git repository at %v", dir)
	repo, err := git.PlainOpenWithOptions(absDir, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	w, err := repo.Worktree()
	if err != nil {
		return nil, err
	}
	root, err := realPath(w.Filesystem.Root())
	if err != nil {
		return nil, err
	}
	res := &CommitResult{Root: root}

	// the scopes, relative to the root of the working tree
	inside := func(path string) (string, error) {
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return "", fmt.Errorf("%s is outside the Git working tree %s", path, root)
		}
		return filepath.ToSlash(rel), nil
	}
	dirRel, err := inside(absDir)
	if err != nil {
		return nil, err
	}
	scopes := []string{dirRel}
	if o.Paths != nil {
		scopes = nil
		for _, p := range o.Paths {
			if !filepath.IsAbs(p) {
				p = filepath.Join(absDir, filepath.FromSlash(p))
			}
			abs, err := realPath(p)
			if err != nil {
				return nil, err
			}
			rel, err := inside(abs)
			if err != nil {
				return nil, err
			}
			if !inScope(rel, []string{dirRel}) {
				return nil, fmt.Errorf("commit path %s is outside %s", p, dir)
			}
			scopes = append(scopes, rel)
		}
	}

	log.Info().Msg("Checking status of working tree")
	status, err := w.Status()
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(status))
	for f := range status {
		files = append(files, f)
	}
	sort.Strings(files)
	var outside []string
	var add []string
	for _, f := range files {
		st := status[f]
		switch {
		case !inScope(f, scopes):
			if st.Staging != git.Unmodified && st.Staging != git.Untracked {
				outside = append(outside, f)
			}
		case st.Worktree != git.Unmodified || st.Staging != git.Unmodified:
			add = append(add, f)
		}
	}
	if len(outside) > 0 {
		if len(outside) > 5 {
			outside = append(outside[:5], fmt.Sprintf("and %d more", len(outside)-5))
		}
		return nil, fmt.Errorf("the Git index has staged changes outside the snapshot (%s): commit or unstage them first", strings.Join(outside, ", "))
	}
	if len(add) == 0 {
		log.Info().Msg("🏆 No changes to commit")
		return res, nil
	}
	log.Info().Msgf("Adding %d changed file(s) below %s for Git tracking", len(add), dirRel)
	for _, f := range add {
		if status[f].Worktree == git.Unmodified {
			continue // staged already
		}
		if _, err := w.Add(f); err != nil {
			return nil, fmt.Errorf("git add %s: %w", f, err)
		}
	}
	res.Files = len(add)

	msg := o.Message
	if msg == "" {
		msg = "Tenant snapshot of " + time.Now().Format(time.UnixDate)
	}
	name, email := o.AuthorName, o.AuthorEmail
	if name == "" {
		name = DefaultAuthorName
	}
	if email == "" {
		email = DefaultAuthorEmail
	}
	log.Info().Msg("Trying to commit changes")
	hash, err := w.Commit(msg, &git.CommitOptions{Author: &object.Signature{Name: name, Email: email, When: time.Now()}})
	if errors.Is(err, git.ErrEmptyCommit) {
		log.Info().Msg("🏆 No changes to commit")
		return res, nil
	}
	if err != nil {
		return nil, err
	}
	res.Hash = hash.String()
	log.Info().Msgf("🏆 Changes committed: %s (%d file(s))", res.Hash, res.Files)
	return res, nil
}

// inScope reports whether path (slash-separated, relative to the root) is
// one of scopes or below one ("." is everything).
func inScope(path string, scopes []string) bool {
	for _, s := range scopes {
		if s == "." || path == s || strings.HasPrefix(path, s+"/") {
			return true
		}
	}
	return false
}

// realPath is the absolute path with symbolic links resolved (as far as
// the path exists), so that paths compare with the repository's root.
func realPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	rest := ""
	for cur := abs; ; cur = filepath.Dir(cur) {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(resolved, rest), nil
		}
		if filepath.Dir(cur) == cur {
			return abs, nil
		}
		rest = filepath.Join(filepath.Base(cur), rest)
	}
}
