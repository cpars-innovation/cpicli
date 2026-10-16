package ops

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/cpars-innovation/cpicli/pkg/manifest"
)

// maxBackupEntry limits one file read from a backup into memory.
const maxBackupEntry = 1 << 30

// readBackupEntries calls fn for every file of a backup archive in order.
func readBackupEntries(file string, fn func(name string, r io.Reader) error) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("%s is not a cpictl backup (gzip): %w", file, err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if err := fn(hdr.Name, io.LimitReader(tr, maxBackupEntry)); err != nil {
			return err
		}
	}
}

// ReadBackup returns the manifest and index of a backup archive.
func ReadBackup(file string) (*BackupManifest, *BackupIndex, error) {
	var man BackupManifest
	var idx BackupIndex
	found := 0
	err := readBackupEntries(file, func(name string, r io.Reader) error {
		switch name {
		case "manifest.json":
			found++
			return json.NewDecoder(r).Decode(&man)
		case "index.json":
			found++
			return json.NewDecoder(r).Decode(&idx)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if found < 2 || man.Schema != BackupSchema {
		return nil, nil, fmt.Errorf("%s is not a cpictl backup (%s): manifest.json or index.json missing", file, BackupSchema)
	}
	return &man, &idx, nil
}

// BackupVerifyResult is the outcome of VerifyBackup.
type BackupVerifyResult struct {
	File     string          `json:"file"`
	OK       bool            `json:"ok"`
	Files    int             `json:"files"`
	Problems []string        `json:"problems"`
	Manifest *BackupManifest `json:"manifest,omitempty"`
}

// VerifyBackup checks a backup archive: every file has the checksum the
// manifest lists and nothing is missing or extra, every artifact zip is a
// readable zip whose MANIFEST.MF names the artifact, and the index points
// only to files that exist. An incomplete backup (warnings at creation) is
// reported as a problem too.
func VerifyBackup(file string) (*BackupVerifyResult, error) {
	man, idx, err := ReadBackup(file)
	if err != nil {
		return nil, err
	}
	res := &BackupVerifyResult{File: file, Problems: []string{}, Manifest: man}
	seen := map[string]bool{}
	zips := map[string]string{} // file -> artifact ID
	for _, p := range idx.Packages {
		for _, a := range p.Artifacts {
			if a.File != "" {
				zips[a.File] = a.ID
			}
		}
	}
	err = readBackupEntries(file, func(name string, r io.Reader) error {
		if name == "manifest.json" {
			return nil
		}
		res.Files++
		seen[name] = true
		data, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		want, listed := man.Files[name]
		h := sha256.Sum256(data)
		switch {
		case !listed:
			res.Problems = append(res.Problems, name+": not in the manifest")
		case hex.EncodeToString(h[:]) != want.SHA256 || int64(len(data)) != want.Size:
			res.Problems = append(res.Problems, name+": checksum differs from the manifest")
		}
		if id, ok := zips[name]; ok {
			if problem := checkArtifactZip(data, id); problem != "" {
				res.Problems = append(res.Problems, name+": "+problem)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var missing []string
	for name := range man.Files {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	for _, name := range missing {
		res.Problems = append(res.Problems, name+": listed in the manifest, missing in the archive")
	}
	if !man.Complete {
		res.Problems = append(res.Problems, fmt.Sprintf("the backup is incomplete: %d warning(s) at creation (%s)", len(man.Warnings), strings.Join(man.Warnings, "; ")))
	}
	res.OK = len(res.Problems) == 0
	return res, nil
}

// checkArtifactZip returns what is wrong with an artifact's zip ("" if
// nothing).
func checkArtifactZip(data []byte, id string) string {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "not a readable zip: " + err.Error()
	}
	for _, f := range zr.File {
		if f.Name != "META-INF/MANIFEST.MF" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "MANIFEST.MF not readable: " + err.Error()
		}
		mf, err := io.ReadAll(io.LimitReader(rc, 1<<20))
		rc.Close()
		if err != nil {
			return "MANIFEST.MF not readable: " + err.Error()
		}
		sym, _, _ := strings.Cut(manifest.Parse(mf)["Bundle-SymbolicName"], ";")
		if sym = strings.TrimSpace(sym); sym != "" && sym != id {
			return fmt.Sprintf("MANIFEST.MF names %s, not %s", sym, id)
		}
		return ""
	}
	return "no META-INF/MANIFEST.MF"
}

// BackupChange is one difference between two backups, or a backup and the
// tenant.
type BackupChange struct {
	Area   string `json:"area"` // package, artifact, runtime, credential, keystore, partner-directory, number-range, variable, data-store, jms-queue
	Key    string `json:"key"`
	Change string `json:"change"` // added, removed, changed
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// DiffBackupIndexes compares two indexes (a: older or the backup, b: newer
// or the tenant). Artifacts are compared by content checksum when both
// sides have one, else by version and change time. Number range counters
// are not compared (they change with every message).
func DiffBackupIndexes(a, b *BackupIndex) []BackupChange {
	var out []BackupChange
	cmpMaps := func(area string, x, y map[string]string) {
		keys := map[string]bool{}
		for k := range x {
			keys[k] = true
		}
		for k := range y {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			before, inA := x[k]
			after, inB := y[k]
			switch {
			case !inA:
				out = append(out, BackupChange{Area: area, Key: k, Change: "added", After: after})
			case !inB:
				out = append(out, BackupChange{Area: area, Key: k, Change: "removed", Before: before})
			case before != after:
				out = append(out, BackupChange{Area: area, Key: k, Change: "changed", Before: before, After: after})
			}
		}
	}
	packages := func(i *BackupIndex) map[string]string {
		m := map[string]string{}
		for _, p := range i.Packages {
			m[p.ID] = "version " + p.Version
		}
		return m
	}
	cmpMaps("package", packages(a), packages(b))

	withContent := func(i *BackupIndex) bool {
		for _, p := range i.Packages {
			for _, x := range p.Artifacts {
				if x.SHA256 != "" {
					return true
				}
			}
		}
		return false
	}
	content := withContent(a) && withContent(b)
	artifacts := func(i *BackupIndex) map[string]string {
		m := map[string]string{}
		for _, p := range i.Packages {
			for _, x := range p.Artifacts {
				v := x.Type + " " + x.Version
				if x.Draft {
					v += " (draft)"
				}
				switch {
				case content && x.SHA256 != "":
					v += " sha256:" + x.SHA256[:12]
					if x.ParametersSHA256 != "" {
						v += " parameters:" + x.ParametersSHA256[:12]
					}
				case x.ModifiedAt != nil:
					v += " modified " + x.ModifiedAt.UTC().Format("2006-01-02T15:04:05Z")
				}
				m[p.ID+"/"+x.ID] = v
			}
		}
		return m
	}
	cmpMaps("artifact", artifacts(a), artifacts(b))

	runtime := func(i *BackupIndex) map[string]string {
		m := map[string]string{}
		for _, r := range i.Runtime {
			k := r.ID
			if r.Runtime != "" {
				k += "@" + r.Runtime
			}
			v := r.Version + " " + r.Status
			if !r.DeployedOn.IsZero() {
				v += " deployed " + r.DeployedOn.UTC().Format("2006-01-02T15:04:05Z")
			}
			m[k] = v
		}
		return m
	}
	cmpMaps("runtime", runtime(a), runtime(b))

	creds := func(i *BackupIndex) map[string]string {
		m := map[string]string{}
		for _, c := range i.Credentials {
			m[c.Kind+":"+c.Name] = strings.TrimSpace(c.User + " " + c.ClientID + " " + c.TokenURL)
		}
		return m
	}
	cmpMaps("credential", creds(a), creds(b))

	keys := func(i *BackupIndex) map[string]string {
		m := map[string]string{}
		for _, k := range i.Keystore {
			m[k.Alias] = k.Type + " serial " + k.SerialNumber + " valid until " + k.ValidNotAfter.UTC().Format("2006-01-02")
		}
		return m
	}
	cmpMaps("keystore", keys(a), keys(b))

	pd := func(i *BackupIndex) map[string]string {
		m := map[string]string{}
		for _, p := range i.PartnerDirectory {
			m[p.PID+":"+p.ID] = p.Kind + " sha256:" + p.SHA256[:12]
		}
		return m
	}
	cmpMaps("partner-directory", pd(a), pd(b))

	ranges := func(i *BackupIndex) map[string]string {
		m := map[string]string{}
		for _, r := range i.NumberRanges {
			m[r.Name] = "min " + r.MinValue + " max " + r.MaxValue + " rotate " + r.Rotate
		}
		return m
	}
	cmpMaps("number-range", ranges(a), ranges(b))

	names := func(area string, list func(*BackupIndex) []string) {
		toMap := func(i *BackupIndex) map[string]string {
			m := map[string]string{}
			for _, n := range list(i) {
				m[n] = ""
			}
			return m
		}
		cmpMaps(area, toMap(a), toMap(b))
	}
	names("variable", func(i *BackupIndex) []string {
		var l []string
		for _, v := range i.Variables {
			l = append(l, strings.TrimPrefix(v.IntegrationFlow+"/"+v.Name, "/"))
		}
		return l
	})
	names("data-store", func(i *BackupIndex) []string {
		var l []string
		for _, d := range i.DataStores {
			l = append(l, strings.TrimPrefix(d.IntegrationFlow+"/"+d.Name, "/"))
		}
		return l
	})
	names("jms-queue", func(i *BackupIndex) []string {
		var l []string
		for _, q := range i.JMSQueues {
			l = append(l, q.Name)
		}
		return l
	})
	if a.AccessPolicies != b.AccessPolicies {
		out = append(out, BackupChange{Area: "access-policies", Key: "count", Change: "changed",
			Before: fmt.Sprint(a.AccessPolicies), After: fmt.Sprint(b.AccessPolicies)})
	}
	return out
}
