package content

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// textExtensions are written with LF line endings by snapshot; every other
// file is kept byte for byte.
var textExtensions = map[string]bool{
	".iflw": true, ".groovy": true, ".gsh": true, ".js": true, ".xml": true, ".xsd": true, ".xsl": true, ".xslt": true,
	".prop": true, ".propdef": true, ".properties": true, ".mf": true, ".json": true, ".wsdl": true, ".edmx": true,
	".mmap": true, ".project": true, ".txt": true,
}

// IsTextFile reports whether name is normalized to LF line endings.
func IsTextFile(name string) bool {
	return textExtensions[strings.ToLower(path.Ext(name))]
}

// NormalizeTree makes an extracted artifact stable between downloads: text
// files get LF line endings, parameters.prop and metainfo.prop are
// normalized (NormalizeProperties: no timestamp comment, sorted keys). Binary files are not touched.
func NormalizeTree(dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if rel != ParametersFile && rel != MetainfoFile && !IsTextFile(rel) {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		norm := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
		if rel == ParametersFile || rel == MetainfoFile {
			norm = NormalizeProperties(norm)
		}
		if bytes.Equal(norm, data) {
			return nil
		}
		return os.WriteFile(p, norm, 0o644)
	})
}

// ignoredTreeFiles are never part of an artifact (OS metadata).
var ignoredTreeFiles = map[string]bool{".DS_Store": true, "Thumbs.db": true}

// TreeFiles returns the files below dir (relative, slash-separated, sorted).
func TreeFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || ignoredTreeFiles[d.Name()] {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(files)
	return files, err
}

// TreeHash is the SHA-256 of every file below dir: paths and exact bytes.
// Equal hashes mean equal folders (git sees no change).
func TreeHash(dir string) (string, error) {
	files, err := TreeFiles(dir)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(data)
		h.Write([]byte(f))
		h.Write([]byte{0})
		h.Write(sum[:])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// TreeDiff lists the files that differ between two folders (missing on one
// side or other bytes), except those for which skip returns true. Text
// files are compared without regard to line endings.
func TreeDiff(a, b string, skip func(rel string) bool) ([]string, error) {
	fa, err := TreeFiles(a)
	if err != nil {
		return nil, err
	}
	fb, err := TreeFiles(b)
	if err != nil {
		return nil, err
	}
	all := map[string]bool{}
	for _, f := range append(fa, fb...) {
		all[f] = true
	}
	var diff []string
	for f := range all {
		if skip != nil && skip(f) {
			continue
		}
		da, errA := os.ReadFile(filepath.Join(a, filepath.FromSlash(f)))
		db, errB := os.ReadFile(filepath.Join(b, filepath.FromSlash(f)))
		if errA != nil || errB != nil {
			diff = append(diff, f)
			continue
		}
		if IsTextFile(f) {
			da = bytes.ReplaceAll(da, []byte("\r\n"), []byte("\n"))
			db = bytes.ReplaceAll(db, []byte("\r\n"), []byte("\n"))
		}
		if !bytes.Equal(da, db) {
			diff = append(diff, f)
		}
	}
	sort.Strings(diff)
	return diff, nil
}
