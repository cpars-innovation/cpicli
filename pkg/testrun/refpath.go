package testrun

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
)

// refPath returns the file a case references (body_file, idoc_file,
// body_equals_file, input_file, ...), relative to the case file. With root
// set (an MCP server sets its root) the file must be inside root once
// symbolic links are resolved: a case in a repository cannot make the
// server read and send files from elsewhere.
func refPath(caseFile, ref, root string) (string, error) {
	if filepath.IsAbs(ref) || filepath.VolumeName(ref) != "" {
		return "", output.Usagef("%s must be relative to the case file", ref)
	}
	p := filepath.Join(filepath.Dir(caseFile), ref)
	if root == "" {
		return p, nil
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", output.Usagef("%s: %v", ref, err)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = r
	}
	rel, err := filepath.Rel(absRoot, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", output.Usagef("%s is outside the server root", ref)
	}
	return resolved, nil
}

// readRef reads a referenced file (see refPath).
func readRef(caseFile, ref, root string) ([]byte, error) {
	p, err := refPath(caseFile, ref, root)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, output.Usage(err)
	}
	return b, nil
}

// InputBody reads the case's input.body_file (inside Root when set).
func (c ScriptCase) InputBody() ([]byte, error) {
	return readRef(c.File, c.Input.BodyFile, c.Root)
}
