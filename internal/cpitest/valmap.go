package cpitest

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// Value mappings: the mock reads the schemas and value pairs from the
// artifact's value_mapping.xml (groups of two entries: source and target
// agency/identifier/value) and UpsertValMaps writes them back into the
// archive, so a download shows the change.

var (
	reValMapSchema = regexp.MustCompile(`^/api/v1/ValueMappingDesigntimeArtifacts\(Id='([^']+)',Version='active'\)/ValMapSchema$`)
	reValMaps      = regexp.MustCompile(`^/api/v1/ValueMappingDesigntimeArtifacts\(Id='([^']+)',Version='active'\)/ValMapSchema\(SrcAgency='([^']*)',SrcId='([^']*)',TgtAgency='([^']*)',TgtId='([^']*)'\)/(ValMaps|DefaultValMaps)$`)
)

type vmFile struct {
	XMLName xml.Name  `xml:"vm"`
	Version string    `xml:"version,attr"`
	Groups  []vmGroup `xml:"group"`
}

type vmGroup struct {
	ID      string    `xml:"id,attr"`
	Entries []vmEntry `xml:"entry"`
}

type vmEntry struct {
	Agency string `xml:"agency"`
	Schema string `xml:"schema"`
	Value  string `xml:"value"`
}

type vmSchema struct{ SrcAgency, SrcID, TgtAgency, TgtID string }

func (g vmGroup) schema() (vmSchema, bool) {
	if len(g.Entries) < 2 {
		return vmSchema{}, false
	}
	return vmSchema{g.Entries[0].Agency, g.Entries[0].Schema, g.Entries[1].Agency, g.Entries[1].Schema}, true
}

const valueMappingFile = "value_mapping.xml"

// valueMapping reads value_mapping.xml of a value mapping artifact.
func (a *Artifact) valueMapping() (*vmFile, error) {
	c, err := readArtifactZip(a.Zip)
	if err != nil {
		return nil, err
	}
	vm := &vmFile{}
	if data, ok := c.Files[valueMappingFile]; ok {
		if err := xml.Unmarshal(data, vm); err != nil {
			return nil, err
		}
	}
	return vm, nil
}

// setValueMapping replaces value_mapping.xml in the artifact's archive.
func (a *Artifact) setValueMapping(vm *vmFile) error {
	data, err := xml.MarshalIndent(vm, "", "    ")
	if err != nil {
		return err
	}
	data = append([]byte(xml.Header), data...)
	zr, err := zip.NewReader(bytes.NewReader(a.Zip), int64(len(a.Zip)))
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	written := false
	for _, f := range zr.File {
		w, err := zw.Create(f.Name)
		if err != nil {
			return err
		}
		if strings.TrimPrefix(f.Name, "/") == valueMappingFile {
			_, err = w.Write(data)
			written = true
		} else {
			var rc io.ReadCloser
			if rc, err = f.Open(); err == nil {
				_, err = io.Copy(w, rc)
				_ = rc.Close()
			}
		}
		if err != nil {
			return err
		}
	}
	if !written {
		w, err := zw.Create(valueMappingFile)
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	a.applyContent(buf.Bytes())
	return nil
}

// valMapError is the tenant's answer for an unknown value mapping: 500
// without a message.
func valMapError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte(`{"error":{"code":"Internal Server Error","message":{"lang":"en","value":null}}}`))
}

// unquote reads an OData string literal (in quotes, a quote inside doubled).
func unquote(v string) string {
	if len(v) >= 2 && strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'") {
		v = v[1 : len(v)-1]
	}
	return strings.ReplaceAll(v, "''", "'")
}

// handleValMap serves the value mapping API; false: not a value mapping path.
func (m *Tenant) handleValMap(w http.ResponseWriter, r *http.Request, path string) bool {
	switch {
	case r.Method == http.MethodGet && path == "/api/v1/ValueMappingDesigntimeArtifacts":
		rows := []map[string]string{}
		for _, id := range sortedKeys(m.Artifacts) {
			if a := m.Artifacts[id]; a.Type == "ValueMapping" && a.DesignVersion != "" {
				rows = append(rows, map[string]string{"Id": id, "Version": a.DesignVersion, "PackageId": a.Package, "Name": cmpOr(a.Name, id), "Description": ""})
			}
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})

	case r.Method == http.MethodGet && reValMapSchema.MatchString(path):
		vm, err := m.valueMappingArtifact(reValMapSchema.FindStringSubmatch(path)[1]).valueMappingOrNil()
		if err != nil {
			valMapError(w)
			return true
		}
		rows := []map[string]string{}
		seen := map[vmSchema]bool{}
		for _, g := range vm.Groups {
			if s, ok := g.schema(); ok && !seen[s] {
				seen[s] = true
				rows = append(rows, map[string]string{"SrcAgency": s.SrcAgency, "SrcId": s.SrcID, "TgtAgency": s.TgtAgency, "TgtId": s.TgtID, "State": "Configured"})
			}
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})

	case r.Method == http.MethodGet && reValMaps.MatchString(path):
		mm := reValMaps.FindStringSubmatch(path)
		vm, err := m.valueMappingArtifact(mm[1]).valueMappingOrNil()
		if err != nil {
			valMapError(w)
			return true
		}
		want := vmSchema{mm[2], mm[3], mm[4], mm[5]}
		rows := []map[string]any{}
		if mm[6] == "ValMaps" {
			for _, g := range vm.Groups {
				if s, ok := g.schema(); ok && s == want {
					rows = append(rows, map[string]any{"Id": g.ID, "Value": map[string]string{"SrcValue": g.Entries[0].Value, "TgtValue": g.Entries[1].Value}})
				}
			}
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})

	case r.Method == http.MethodPost && path == "/api/v1/UpsertValMaps":
		q := r.URL.Query()
		for _, p := range []string{"Id", "Version", "SrcAgency", "SrcId", "TgtAgency", "TgtId", "SrcValue", "TgtValue", "IsConfigured"} {
			if !q.Has(p) {
				odataError(w, http.StatusBadRequest, "Bad Request", "Missing parameter "+p)
				return true
			}
		}
		a := m.valueMappingArtifact(unquote(q.Get("Id")))
		vm, err := a.valueMappingOrNil()
		if err != nil {
			notFound(w)
			return true
		}
		want := vmSchema{unquote(q.Get("SrcAgency")), unquote(q.Get("SrcId")), unquote(q.Get("TgtAgency")), unquote(q.Get("TgtId"))}
		src, tgt, id := unquote(q.Get("SrcValue")), unquote(q.Get("TgtValue")), unquote(q.Get("ValMapId"))
		known, target := false, -1
		for i, g := range vm.Groups {
			s, ok := g.schema()
			if !ok || s != want {
				continue
			}
			known = true
			switch {
			case id != "" && g.ID == id:
				target = i
			case g.Entries[0].Value == src && g.ID != id:
				odataError(w, http.StatusBadRequest, "Bad Request", fmt.Sprintf("Source value %s already exists", src))
				return true
			}
		}
		if !known || (id != "" && target < 0) {
			notFound(w) // as the tenant answers for an unknown agency/identifier pair
			return true
		}
		if target >= 0 {
			vm.Groups[target].Entries[0].Value, vm.Groups[target].Entries[1].Value = src, tgt
		} else {
			b := make([]byte, 16)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
			vm.Groups = append(vm.Groups, vmGroup{ID: id, Entries: []vmEntry{{want.SrcAgency, want.SrcID, src}, {want.TgtAgency, want.TgtID, tgt}}})
		}
		if err := a.setValueMapping(vm); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		}
		a.Uploads++
		w.WriteHeader(http.StatusAccepted)

	default:
		return false
	}
	return true
}

// valueMappingArtifact returns the value mapping artifact id (nil if there is none).
func (m *Tenant) valueMappingArtifact(id string) *Artifact {
	if a := m.Artifacts[id]; a != nil && a.Type == "ValueMapping" && a.DesignVersion != "" {
		return a
	}
	return nil
}

func (a *Artifact) valueMappingOrNil() (*vmFile, error) {
	if a == nil {
		return nil, fmt.Errorf("no value mapping")
	}
	return a.valueMapping()
}
