package scripttest

import (
	"embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// template is what Init writes into a project: build.gradle and the SAP
// script API stand-ins below tests/stubs (test code, never uploaded).
//
//go:embed all:template
var template embed.FS

// InitResult lists the files Init wrote and the existing ones it left alone
// (relative to the project, slash-separated).
type InitResult struct {
	Written []string `json:"written"`
	Skipped []string `json:"skipped"`
}

// Init writes the build and the stand-ins of SAP's script API that
// `cpictl test scripts` runs with into project. Existing files are kept
// unless force.
func Init(project string, force bool) (*InitResult, error) {
	res := &InitResult{Written: []string{}, Skipped: []string{}}
	err := fs.WalkDir(template, "template", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel("template", filepath.FromSlash(p))
		target := filepath.Join(project, rel)
		if _, err := os.Stat(target); err == nil && !force {
			res.Skipped = append(res.Skipped, filepath.ToSlash(rel))
			return nil
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		data, err := template.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
		res.Written = append(res.Written, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(res.Written)
	sort.Strings(res.Skipped)
	return res, err
}
