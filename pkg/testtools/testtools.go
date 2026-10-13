// Package testtools holds the tenant-side test tooling of cpictl, embedded in
// the binary: the test harness flow (HTTPS -> ProcessDirect to any address)
// and the receiver stub flow (records requests, answers per rule from the
// Partner Directory). `cpictl test tools install` uploads them.
//
// The models copy the property sets and component versions of the demo
// landscape flows (internal/cpitest/landscapes/demo), which pass a real
// tenant's Check.
package testtools

import (
	"archive/zip"
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
)

//go:embed all:artifacts
var content embed.FS

const (
	// PackageID is the default package of the tooling. Package IDs take
	// letters and digits only (the tenant refuses '_').
	PackageID = "CPICTLTesting"
	// PackageName and PackageShortText describe the package (a tenant
	// requires a short text).
	PackageName      = "cpictl test tooling"
	PackageShortText = "cpictl test tooling: test harness and receiver stub (do not transport)"

	// HarnessID is the test harness flow: HTTPS sender HarnessPath ->
	// Request Reply -> ProcessDirect receiver ${header.CpictlTargetAddress}.
	HarnessID = "CPICTL_Test_Harness"
	// HarnessPath is the harness sender's address.
	HarnessPath = "/cpictl/test"
	// HarnessAddressHeader carries the ProcessDirect target address.
	HarnessAddressHeader = "CpictlTargetAddress"

	// StubID is the receiver stub flow: HTTPS sender StubPath + "/*".
	StubID = "CPICTL_Test_Stub"
	// StubPath is the stub sender's address without the wildcard; a request
	// to <runtime>/http/cpictl/stub/<rule>/... is answered per rule.
	StubPath = "/cpictl/stub"
	// StubPID is the Partner Directory partner that holds the responses:
	// one string parameter per rule (ID = rule, value = a StubResponse as
	// JSON).
	StubPID = "CPICTL_Stub"
	// StubAttachment is the message processing log attachment with the
	// recorded request (JSON).
	StubAttachment = "cpictl-stub-request"
	// StubRuleHeader and StubPathHeader are the custom header properties
	// the stub sets: the rule (first path segment) and the path after
	// /cpictl/stub/.
	StubRuleHeader = "CpictlStubRule"
	StubPathHeader = "CpictlStubPath"
	// StubMaxBody is the most of a request body the stub records (characters).
	StubMaxBody = 64 * 1024
)

// Tool names for Only.
const (
	ToolHarness = "harness"
	ToolStub    = "stub"
)

// Artifact is one embedded flow.
type Artifact struct {
	ID      string `json:"id"`
	Tool    string `json:"tool"` // harness or stub
	Name    string `json:"name"`
	Version string `json:"version"`
}

var reManifest = regexp.MustCompile(`(?m)^(Bundle-Name|Bundle-Version): *(.+?)\r?$`)

// Artifacts returns the embedded flows: the harness, then the stub.
func Artifacts() []Artifact {
	out := []Artifact{{ID: HarnessID, Tool: ToolHarness}, {ID: StubID, Tool: ToolStub}}
	for i := range out {
		mf, err := content.ReadFile("artifacts/" + out[i].ID + "/META-INF/MANIFEST.MF")
		if err != nil {
			panic(err) // embedded and tested
		}
		for _, m := range reManifest.FindAllStringSubmatch(string(mf), -1) {
			if m[1] == "Bundle-Name" {
				out[i].Name = m[2]
			} else {
				out[i].Version = m[2]
			}
		}
	}
	return out
}

// Select returns the artifacts of one tool ("" or "all": both).
func Select(only string) ([]Artifact, error) {
	all := Artifacts()
	switch only {
	case "", "all":
		return all, nil
	case ToolHarness, ToolStub:
		return slices.DeleteFunc(all, func(a Artifact) bool { return a.Tool != only }), nil
	}
	return nil, fmt.Errorf("invalid tool %q (harness or stub)", only)
}

// FS returns the files of an embedded artifact (META-INF/MANIFEST.MF,
// metainfo.prop, src/main/resources/...).
func FS(id string) (fs.FS, error) {
	return fs.Sub(content, "artifacts/"+id)
}

// WriteDir writes an embedded artifact into dir (the layout UploadArtifact
// reads).
func WriteDir(id, dir string) error {
	sub, err := FS(id)
	if err != nil {
		return err
	}
	if _, err := fs.Stat(sub, "META-INF/MANIFEST.MF"); err != nil {
		return fmt.Errorf("no embedded artifact %s", id)
	}
	return fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(sub, p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// Zip returns an embedded artifact as the archive a tenant stores.
func Zip(id string) ([]byte, error) {
	sub, err := FS(id)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(sub, p)
		if err != nil {
			return err
		}
		w, err := zw.Create(path.Clean(p))
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
