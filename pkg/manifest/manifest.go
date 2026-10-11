// Package manifest reads and writes META-INF/MANIFEST.MF of CPI artifacts.
package manifest

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// Path returns the manifest of an artifact directory.
func Path(artifactDir string) string {
	return filepath.Join(artifactDir, "META-INF", "MANIFEST.MF")
}

// Version returns Bundle-Version of an artifact directory ("" without one).
func Version(artifactDir string) (string, error) {
	data, err := os.ReadFile(Path(artifactDir))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(Parse(data)["Bundle-Version"]), nil
}

// SetVersion writes Bundle-Version into an artifact directory's manifest,
// keeping all other lines.
func SetVersion(artifactDir, version string) error {
	p := Path(artifactDir)
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	if strings.TrimSpace(Parse(data)["Bundle-Version"]) == version {
		return nil
	}
	info, err := os.Stat(p)
	if err != nil {
		return err
	}
	return os.WriteFile(p, SetHeaders(data, map[string]string{"Bundle-Version": version}), info.Mode().Perm())
}

// Parse reads MANIFEST.MF headers (continuation lines start with a space).
func Parse(data []byte) map[string]string {
	h := map[string]string{}
	var last string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.HasPrefix(line, " ") && last != "" {
			h[last] += line[1:]
			continue
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			last = strings.TrimSpace(k)
			h[last] = strings.TrimSpace(v)
		}
	}
	return h
}

// SetHeaders replaces (or appends) manifest headers, wrapping them at
// 72 bytes; all other lines are kept as they are.
func SetHeaders(mf []byte, headers map[string]string) []byte {
	eol := "\n"
	if bytes.Contains(mf, []byte("\r\n")) {
		eol = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(string(mf), "\r\n", "\n"), "\n")
	var out []string
	done := map[string]bool{}
	skipping := false
	for _, line := range lines {
		if strings.HasPrefix(line, " ") {
			if !skipping {
				out = append(out, line)
			}
			continue
		}
		skipping = false
		key, _, ok := strings.Cut(line, ":")
		if ok {
			if v, set := headers[strings.TrimSpace(key)]; set {
				out = append(out, WrapLine(strings.TrimSpace(key)+": "+v)...)
				done[strings.TrimSpace(key)] = true
				skipping = true
				continue
			}
		}
		out = append(out, line)
	}
	// drop trailing empty lines, append missing headers, end with one EOL
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !done[k] {
			out = append(out, WrapLine(k+": "+headers[k])...)
		}
	}
	return []byte(strings.Join(out, eol) + eol)
}

// WrapLine splits a header into lines of at most 72 bytes;
// continuation lines start with a space. UTF-8 characters are not split.
func WrapLine(s string) []string {
	var lines []string
	limit := 72
	for len(s) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		lines = append(lines, s[:cut])
		s = " " + s[cut:]
		limit = 72
	}
	return append(lines, s)
}
