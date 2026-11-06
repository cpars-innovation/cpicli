package ops

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/cpars-innovation/cpicli/pkg/mask"
)

// PDSecureMarksFile is where a repository marks Partner Directory entries
// as secure (relative to its root; the place CPI Studio uses too):
//
//	{"entries": [{"pid": "FI_Bank", "id": "ApiToken"}],
//	 "patterns": [{"pid": "*", "id": "*Token*"}]}
//
// A marked entry's value is never returned by cpictl (tool and command
// output): its name, kind and existence stay visible, marked secure
// (cpicli#73). pd snapshot keeps writing real values into the repository.
const PDSecureMarksFile = ".cpi/secrets/partner-directory.json"

// PDSecureMarks are the secure Partner Directory entries of a repository.
type PDSecureMarks struct {
	Entries  []PDMark `json:"entries"`
	Patterns []PDMark `json:"patterns"`
	// All marks every entry: the marks file could not be read (fail
	// closed).
	All bool `json:"-"`
	// Problem says why All is set.
	Problem string `json:"-"`
}

// PDMark is a partner ID and a parameter ID; in patterns both may hold *.
type PDMark struct {
	Pid string `json:"pid"`
	ID  string `json:"id"`
}

// LoadPDSecureMarks reads PDSecureMarksFile below root. No file: no marks.
// A file that cannot be read or parsed, or a mark without pid or id, marks
// every entry: a damaged file never means "nothing is secure".
func LoadPDSecureMarks(root string) *PDSecureMarks {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(PDSecureMarksFile)))
	if errors.Is(err, fs.ErrNotExist) {
		return &PDSecureMarks{}
	}
	closed := func(why string) *PDSecureMarks {
		return &PDSecureMarks{All: true, Problem: PDSecureMarksFile + " " + why + ": every Partner Directory value is withheld"}
	}
	if err != nil {
		return closed("cannot be read")
	}
	var m PDSecureMarks
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return closed("is not valid (" + err.Error() + ")")
	}
	for _, e := range append(append([]PDMark{}, m.Entries...), m.Patterns...) {
		if e.Pid == "" || e.ID == "" {
			return closed("has a mark without pid or id")
		}
		if _, err := path.Match(e.Pid, ""); err != nil {
			return closed("has an invalid pattern")
		}
		if _, err := path.Match(e.ID, ""); err != nil {
			return closed("has an invalid pattern")
		}
	}
	return &m
}

// FindPDSecureMarks loads the marks of the repository dir is in: the first
// directory from dir upwards that holds .cpi/ or .git.
func FindPDSecureMarks(dir string) *PDSecureMarks {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return LoadPDSecureMarks(dir)
	}
	for d := abs; ; d = filepath.Dir(d) {
		for _, marker := range []string{".cpi", ".git"} {
			if _, err := os.Stat(filepath.Join(d, marker)); err == nil {
				return LoadPDSecureMarks(d)
			}
		}
		if filepath.Dir(d) == d {
			return LoadPDSecureMarks(abs)
		}
	}
}

// Secure reports whether the entry's value is withheld.
func (m *PDSecureMarks) Secure(pid, id string) bool {
	if m == nil {
		return false
	}
	if m.All {
		return true
	}
	// in any case: a mark that misses by case would leak (cpicli#75)
	pid, id = strings.ToLower(pid), strings.ToLower(id)
	for _, e := range m.Entries {
		if strings.ToLower(e.Pid) == pid && strings.ToLower(e.ID) == id {
			return true
		}
	}
	for _, e := range m.Patterns {
		p, _ := path.Match(strings.ToLower(e.Pid), pid)
		i, _ := path.Match(strings.ToLower(e.ID), id)
		if p && i {
			return true
		}
	}
	return false
}

// withhold replaces the values of secure entries in res.
func (m *PDSecureMarks) withhold(res *PDParameters) {
	if m == nil {
		return
	}
	for i, s := range res.Strings {
		if m.Secure(res.Pid, s.ID) {
			res.Strings[i].Value, res.Strings[i].Secure = mask.Dots, true
		}
	}
	for i, b := range res.Binaries {
		if m.Secure(res.Pid, b.ID) {
			res.Binaries[i] = PDBinary{ID: b.ID, ContentType: b.ContentType, Size: b.Size, Secure: true}
		}
	}
}
