package ops

import (
	"os"
	"path/filepath"
	"strings"
)

// ScriptDocReport is what CheckScriptFile finds in one script file.
type ScriptDocReport struct {
	File string `json:"file"`
	// Collection is true for a script of a script collection (found by the
	// MANIFEST.MF of the artifact folder the file is in).
	Collection bool             `json:"collection"`
	Summary    string           `json:"summary,omitempty"`
	Keywords   []string         `json:"keywords,omitempty"`
	Reads      []string         `json:"reads,omitempty"`
	Writes     []string         `json:"writes,omitempty"`
	Functions  []ScriptFunction `json:"functions,omitempty"`
	Status     string           `json:"docStatus"`
	Issues     []ScriptDocIssue `json:"issues"`
}

// CheckScriptFile checks the comments of a Groovy script file
// (CheckScriptDoc), as a script collection's when the artifact folder the
// file is in is one.
func CheckScriptFile(file string) (*ScriptDocReport, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	r := &ScriptDocReport{File: file, Collection: inScriptCollection(file)}
	doc := ParseScriptDoc(data)
	r.Summary, r.Keywords, r.Functions = doc.Summary, doc.Keywords(), doc.Functions
	r.Reads, r.Writes = ScriptAccess(data)
	r.Issues = CheckScriptDoc(data, r.Collection)
	if r.Issues == nil {
		r.Issues = []ScriptDocIssue{}
	}
	public := 1
	if r.Collection {
		public = 0
		for _, f := range doc.Functions {
			if !f.Private {
				public++
			}
		}
	}
	r.Status = DocStatusOf(r.Issues, public)
	return r, nil
}

// IsGroovyFile tells whether a file is a Groovy script by its extension.
func IsGroovyFile(file string) bool {
	ext := strings.ToLower(filepath.Ext(file))
	return ext == ".groovy" || ext == ".gsh"
}

// inScriptCollection looks for the artifact folder above the file (the one
// with META-INF/MANIFEST.MF, at most 8 levels up) and tells whether it is a
// script collection.
func inScriptCollection(file string) bool {
	dir, err := filepath.Abs(filepath.Dir(file))
	if err != nil {
		return false
	}
	for range 8 {
		if mf, err := os.ReadFile(filepath.Join(dir, "META-INF", "MANIFEST.MF")); err == nil {
			return parseManifest(mf)["SAP-BundleType"] == "ScriptCollection"
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return false
}
