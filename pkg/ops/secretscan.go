package ops

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/x509"
	"encoding/base64"
	"io"
	"sort"
	"sync"

	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/pdrepo"
	"github.com/cpars-innovation/cpicli/pkg/secretscan"
)

// Areas a secret scan covers.
const (
	AreaPartnerDirectory = "partner-directory"
	AreaConfiguration    = "configuration"
)

// SecretFinding is security material outside the secure store and
// keystore. It never carries the value.
type SecretFinding struct {
	Area string `json:"area"`
	// PID and Key locate a Partner Directory parameter (Binary: a binary
	// parameter; Entry: the file inside a ZIP).
	PID    string `json:"pid,omitempty"`
	Binary bool   `json:"binary,omitempty"`
	Entry  string `json:"entry,omitempty"`
	// Package and Artifact locate a configuration parameter (Key).
	Package  string `json:"package,omitempty"`
	Artifact string `json:"artifact,omitempty"`
	Key      string `json:"key"`
	secretscan.Hit
	LastModifiedBy string `json:"lastModifiedBy,omitempty"`
}

// SecretScanResult is the outcome of a secret scan.
type SecretScanResult struct {
	Source   string          `json:"source"` // tenant or the local folder
	Findings []SecretFinding `json:"findings"`
	Scanned  struct {
		StringParameters        int `json:"stringParameters"`
		BinaryParameters        int `json:"binaryParameters"`
		Artifacts               int `json:"artifacts"`
		ConfigurationParameters int `json:"configurationParameters"`
	} `json:"scanned"`
	// Errors are the artifacts whose configuration could not be read.
	Errors []string `json:"errors,omitempty"`
}

// maxScanBytes bounds what is read of one binary parameter or ZIP entry.
const maxScanBytes = 4 << 20

// ScanPDParameters checks Partner Directory parameters: string values by
// name and content, binary contents (text, ZIP and gzip entries, DER
// private keys) by content.
func ScanPDParameters(strs []cpi.StringParameter, bins []cpi.BinaryParameter) []SecretFinding {
	var out []SecretFinding
	for _, p := range strs {
		if h := secretscan.Value(p.ID, p.Value); h != nil {
			out = append(out, SecretFinding{Area: AreaPartnerDirectory, PID: p.Pid, Key: p.ID, Hit: *h, LastModifiedBy: p.LastModifiedBy})
		}
	}
	for _, p := range bins {
		data, err := base64.StdEncoding.DecodeString(p.Value)
		if err != nil {
			continue
		}
		for _, h := range scanBinary(data, "", 0) {
			out = append(out, SecretFinding{Area: AreaPartnerDirectory, PID: p.Pid, Key: p.ID, Binary: true, Entry: h.entry, Hit: h.Hit, LastModifiedBy: p.LastModifiedBy})
		}
	}
	return out
}

type binaryHit struct {
	secretscan.Hit
	entry string
}

func scanBinary(data []byte, entry string, depth int) []binaryHit {
	var out []binaryHit
	switch detectPDType(data) {
	case "zip":
		if depth > 1 {
			return nil
		}
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil
		}
		for i, f := range zr.File {
			if i >= 200 || f.FileInfo().IsDir() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				continue
			}
			b, _ := io.ReadAll(io.LimitReader(rc, maxScanBytes))
			rc.Close()
			out = append(out, scanBinary(b, joinEntry(entry, f.Name), depth+1)...)
		}
	case "gz":
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil
		}
		b, _ := io.ReadAll(io.LimitReader(zr, maxScanBytes))
		return scanBinary(b, entry, depth+1)
	case "binary":
		if derPrivateKey(data) {
			out = append(out, binaryHit{Hit: secretscan.Hit{Kind: secretscan.KindPrivateKey, Reason: "a private key (DER)", Fingerprint: secretscan.Fingerprint(string(data))}, entry: entry})
		}
	case "crt":
		// certificates are public
	default:
		for _, h := range secretscan.Text(string(data[:min(len(data), maxScanBytes)])) {
			out = append(out, binaryHit{Hit: h, entry: entry})
		}
	}
	return out
}

func joinEntry(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "!" + name
}

func derPrivateKey(data []byte) bool {
	if _, err := x509.ParsePKCS8PrivateKey(data); err == nil {
		return true
	}
	if _, err := x509.ParsePKCS1PrivateKey(data); err == nil {
		return true
	}
	_, err := x509.ParseECPrivateKey(data)
	return err == nil
}

// ScanTenantPartnerDirectory reads every Partner Directory parameter of the
// tenant and checks it.
func ScanTenantPartnerDirectory(exe *httpclnt.HTTPExecuter, res *SecretScanResult) error {
	api := cpi.NewPartnerDirectory(exe)
	strs, err := api.GetStringParameters("")
	if err != nil {
		return err
	}
	bins, err := api.GetBinaryParameters("")
	if err != nil {
		return err
	}
	res.Scanned.StringParameters += len(strs)
	res.Scanned.BinaryParameters += len(bins)
	res.Findings = append(res.Findings, ScanPDParameters(strs, bins)...)
	return nil
}

// ScanLocalPartnerDirectory checks a Partner Directory folder as pd
// snapshot writes it.
func ScanLocalPartnerDirectory(dir string, res *SecretScanResult) error {
	repo := pdrepo.New(dir)
	pids, err := repo.GetLocalPIDs()
	if err != nil {
		return err
	}
	for _, pid := range pids {
		strs, err := repo.ReadStringParameters(pid)
		if err != nil {
			return err
		}
		bins, err := repo.ReadBinaryParameters(pid)
		if err != nil {
			return err
		}
		res.Scanned.StringParameters += len(strs)
		res.Scanned.BinaryParameters += len(bins)
		res.Findings = append(res.Findings, ScanPDParameters(strs, bins)...)
	}
	return nil
}

// ScanTenantConfigurations checks the externalised parameters of every
// integration flow of the given packages (all: none given), reading
// parallel configurations at a time.
func ScanTenantConfigurations(ctx context.Context, exe *httpclnt.HTTPExecuter, packages []string, parallel int, res *SecretScanResult) error {
	pkgs, err := ListPackages(exe)
	if err != nil {
		return err
	}
	type job struct{ pkg, id string }
	var jobs []job
	for _, p := range pkgs {
		if !MatchAny(packages, p.ID) {
			continue
		}
		arts, err := ListArtifacts(exe, p.ID)
		if err != nil {
			return err
		}
		for _, a := range arts {
			if a.Type == "IntegrationFlow" || a.Type == "Integration" {
				jobs = append(jobs, job{p.ID, a.ID})
			}
		}
	}
	res.Scanned.Artifacts += len(jobs)
	var mu sync.Mutex
	sem := make(chan struct{}, max(parallel, 1))
	var wg sync.WaitGroup
	for _, j := range jobs {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			params, err := GetConfiguration(exe, j.id, "active")
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				res.Errors = append(res.Errors, j.id+": "+err.Error())
				return
			}
			res.Scanned.ConfigurationParameters += len(params)
			for _, p := range params {
				if h := secretscan.Value(p.Key, p.Value); h != nil {
					res.Findings = append(res.Findings, SecretFinding{Area: AreaConfiguration, Package: j.pkg, Artifact: j.id, Key: p.Key, Hit: *h})
				}
			}
		}()
	}
	wg.Wait()
	sort.Strings(res.Errors)
	return ctx.Err()
}

// SortSecretFindings orders findings by area, place and key.
func SortSecretFindings(f []SecretFinding) {
	sort.SliceStable(f, func(i, j int) bool {
		a, b := f[i], f[j]
		if a.Area != b.Area {
			return a.Area < b.Area
		}
		if a.PID+a.Package != b.PID+b.Package {
			return a.PID+a.Package < b.PID+b.Package
		}
		if a.Artifact != b.Artifact {
			return a.Artifact < b.Artifact
		}
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		return a.Entry < b.Entry
	})
}
