package content

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/rs/zerolog/log"
)

// The comparisons below decide whether content changed (upload, sync,
// drift). They are pure Go (no external diff program, so they work on every
// platform) and ignore what the tenant rewrites on its own:
//   - carriage returns, whitespace inside lines and blank lines;
//   - in directories: lines starting with "Origin" (MANIFEST.MF headers the
//     tenant adds) and the files parameters.prop and .DS_Store;
//   - in single files: lines starting with "#" (comments and timestamps of
//     .prop files).

var excludedNames = map[string]bool{"parameters.prop": true, ".DS_Store": true}

// normalizeLines returns the significant lines of data.
func normalizeLines(data []byte, ignorePrefix string) []string {
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.Join(strings.Fields(line), "")
		if line == "" || (ignorePrefix != "" && strings.HasPrefix(line, ignorePrefix)) {
			continue
		}
		out = append(out, line)
	}
	return out
}

// normalizedTree reads all files of fsys below root (excluding
// parameters.prop and .DS_Store) as relative path -> significant lines.
func normalizedTree(fsys fs.FS, root string) (map[string][]string, error) {
	tree := map[string][]string{}
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || excludedNames[d.Name()] {
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, root), "/")
		tree[rel] = normalizeLines(data, "Origin")
		return nil
	})
	return tree, err
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// DiffDirectories reports whether two directory trees differ (see above for
// what is ignored). A missing directory differs from an existing one.
func DiffDirectories(firstDir string, secondDir string) bool {
	a, errA := normalizedTree(os.DirFS(firstDir), ".")
	b, errB := normalizedTree(os.DirFS(secondDir), ".")
	if errA != nil || errB != nil {
		if errA != nil && errB != nil && os.IsNotExist(errA) && os.IsNotExist(errB) {
			return false
		}
		log.Info().Msgf("Comparing %v and %v: %v %v", firstDir, secondDir, errA, errB)
		return true
	}
	var differing []string
	for name, lines := range a {
		if other, ok := b[name]; !ok || !equalLines(lines, other) {
			differing = append(differing, name)
		}
	}
	for name := range b {
		if _, ok := a[name]; !ok {
			differing = append(differing, name)
		}
	}
	if len(differing) > 0 {
		sort.Strings(differing)
		log.Info().Msgf("Content differs: %s", strings.Join(differing, ", "))
		return true
	}
	return false
}

// DiffFile reports whether two files differ, ignoring comment lines.
func DiffFile(firstFile string, secondFile string) bool {
	a, errA := os.ReadFile(firstFile)
	b, errB := os.ReadFile(secondFile)
	if errA != nil || errB != nil {
		return true
	}
	if !equalLines(normalizeLines(a, "#"), normalizeLines(b, "#")) {
		log.Info().Msgf("File differs: %v", path.Base(firstFile))
		return true
	}
	return false
}

// ContentHash is a hash of the significant content of an artifact in fsys
// (the root holds META-INF): META-INF, src/main/resources, metainfo.prop and
// value_mapping.xml, normalized as in DiffDirectories / DiffFile. Equal hashes
// mean upload would report UNCHANGED.
func ContentHash(fsys fs.FS) (string, error) {
	return contentHash(fsys, false)
}

// UploadHash covers what an upload compares (cpi CompareContent): like
// ContentHash, but without the Bundle-Version header of
// META-INF/MANIFEST.MF (the version is set separately, see the versioning
// modes); for value mappings only META-INF and value_mapping.xml.
func UploadHash(fsys fs.FS, artifactType string) (string, error) {
	if artifactType == "ValueMapping" {
		return hashOf(fsys, true, []string{"META-INF"}, []string{"value_mapping.xml"})
	}
	return contentHash(fsys, true)
}

func contentHash(fsys fs.FS, skipVersion bool) (string, error) {
	return hashOf(fsys, skipVersion, []string{"META-INF", "src/main/resources"}, []string{"metainfo.prop", "value_mapping.xml"})
}

func hashOf(fsys fs.FS, skipVersion bool, dirs, files []string) (string, error) {
	h := sha256.New()
	add := func(name string, lines []string) {
		h.Write([]byte(name))
		h.Write([]byte{0})
		for _, l := range lines {
			h.Write([]byte(l))
			h.Write([]byte{'\n'})
		}
		h.Write([]byte{0})
	}
	for _, dir := range dirs {
		if _, err := fs.Stat(fsys, dir); err != nil {
			continue
		}
		tree, err := normalizedTree(fsys, dir)
		if err != nil {
			return "", err
		}
		names := make([]string, 0, len(tree))
		for n := range tree {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			lines := tree[n]
			if skipVersion && dir == "META-INF" && n == "MANIFEST.MF" {
				lines = slices.DeleteFunc(slices.Clone(lines), func(l string) bool { return strings.HasPrefix(l, "Bundle-Version:") })
			}
			add(dir+"/"+n, lines)
		}
	}
	for _, f := range files {
		if data, err := fs.ReadFile(fsys, f); err == nil {
			add(f, normalizeLines(bytes.TrimSpace(data), "#"))
		}
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:16]), nil
}
