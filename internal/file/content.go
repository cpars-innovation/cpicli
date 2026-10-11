package file

import (
	"io/fs"

	"github.com/cpars-innovation/cpicli/pkg/content"
)

// The content comparison, hashing and normalisation live in pkg/content
// (public, so that library users hash and diff exactly as cpictl does).
// These wrappers keep the internal callers unchanged.

const (
	ParametersFile = content.ParametersFile
	MetainfoFile   = content.MetainfoFile
	PropdefFile    = content.PropdefFile
)

func IsTextFile(name string) bool              { return content.IsTextFile(name) }
func NormalizeTree(dir string) error           { return content.NormalizeTree(dir) }
func TreeFiles(dir string) ([]string, error)   { return content.TreeFiles(dir) }
func TreeHash(dir string) (string, error)      { return content.TreeHash(dir) }
func ContentHash(fsys fs.FS) (string, error)   { return content.ContentHash(fsys) }
func NormalizeProperties(data []byte) []byte   { return content.NormalizeProperties(data) }
func PropertiesEqual(a, b []byte) bool         { return content.PropertiesEqual(a, b) }
func NormalizeParametersFile(dir string) error { return content.NormalizeParametersFile(dir) }
func DiffDirectories(firstDir, secondDir string) bool {
	return content.DiffDirectories(firstDir, secondDir)
}
func DiffFile(firstFile, secondFile string) bool { return content.DiffFile(firstFile, secondFile) }

func UploadHash(fsys fs.FS, artifactType string) (string, error) {
	return content.UploadHash(fsys, artifactType)
}

func TreeDiff(a, b string, skip func(rel string) bool) ([]string, error) {
	return content.TreeDiff(a, b, skip)
}

func UnifiedDiff(a, b, nameA, nameB string, context int) (string, bool) {
	return content.UnifiedDiff(a, b, nameA, nameB, context)
}

func RestoreProperties(tenant, local []byte, keys map[string]bool) []byte {
	return content.RestoreProperties(tenant, local, keys)
}

func PropdefNames(dir string) (map[string]bool, bool, error) { return content.PropdefNames(dir) }

func DropOrphanParameters(dir string, keep bool) ([]string, error) {
	return content.DropOrphanParameters(dir, keep)
}
