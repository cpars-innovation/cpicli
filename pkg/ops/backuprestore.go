package ops

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/pdrepo"
	"github.com/cpars-innovation/cpicli/pkg/versioning"
)

// Restore areas, in the order a restore runs them: packages before their
// artifacts, artifacts before their parameters, everything before deploy.
const (
	RestorePackages         = "packages"
	RestoreArtifacts        = "artifacts"
	RestoreParameters       = "parameters"
	RestorePartnerDirectory = "partner-directory"
	RestoreCredentials      = "credentials"
	RestoreCertificates     = "certificates"
	RestoreAccessPolicies   = "access-policies"
	RestoreNumberRanges     = "number-ranges"
	RestoreDeploy           = "deploy"
)

// RestoreAreas are the areas a restore covers, in order.
var RestoreAreas = []string{RestorePackages, RestoreArtifacts, RestoreParameters, RestorePartnerDirectory, RestoreCredentials,
	RestoreCertificates, RestoreAccessPolicies, RestoreNumberRanges, RestoreDeploy}

// Restore step actions. In a dry run they say what would happen.
const (
	RestoreCreate    = "create"
	RestoreUpdate    = "update"
	RestoreUnchanged = "unchanged"
	RestoreDeployIt  = "deploy"
	RestoreSkip      = "skip"   // deliberately left alone (detail says why)
	RestoreManual    = "manual" // needs a person (detail says what to do)
	RestoreFailed    = "failed"
	RestoreInfo      = "info"
)

// RestoreOptions control RestoreBackup.
type RestoreOptions struct {
	DryRun bool
	// Areas limits the restore (empty: all of RestoreAreas).
	Areas []string
	// Packages limits packages, artifacts, parameters and deploy to these
	// package IDs (patterns with *; empty: all).
	Packages []string
	// Parallel is the number of artifacts uploaded at a time.
	Parallel int
	// Deploy are the wait settings of the deploy area.
	Deploy Options
	// Getenv reads the secrets of credentials.yml (nil: os.Getenv).
	Getenv func(string) string
	// TargetHost is the tenant restored into (for the plan).
	TargetHost string
}

// RestoreStep is one thing a restore does (or would do).
type RestoreStep struct {
	Area   string `json:"area"`
	Object string `json:"object"`
	Action string `json:"action"`
	Detail string `json:"detail,omitempty"`
	Error  string `json:"error,omitempty"`
}

// RestoreResult is the plan (dry run) or the outcome of a restore.
type RestoreResult struct {
	Archive         string         `json:"archive"`
	SourceTenant    string         `json:"sourceTenant"`
	SourceHost      string         `json:"sourceHost,omitempty"`
	TargetHost      string         `json:"targetHost,omitempty"`
	BackupCreatedAt time.Time      `json:"backupCreatedAt"`
	DryRun          bool           `json:"dryRun"`
	Areas           []string       `json:"areas"`
	Steps           []RestoreStep  `json:"steps"`
	Counts          map[string]int `json:"counts"`
}

// Manual returns the steps that need a person.
func (r *RestoreResult) Manual() []RestoreStep {
	var out []RestoreStep
	for _, s := range r.Steps {
		if s.Action == RestoreManual {
			out = append(out, s)
		}
	}
	return out
}

type restorer struct {
	exe  *httpclnt.HTTPExecuter
	opts RestoreOptions
	dir  string // the extracted archive
	man  *BackupManifest
	idx  *BackupIndex
	res  *RestoreResult
	mu   sync.Mutex
	// changed are artifacts created or updated by this restore (deploy them
	// even when the running version is the same)
	changed map[string]bool
	// created are artifacts this restore creates (a dry run cannot read
	// their parameters yet)
	created map[string]bool
	// skippedPackages are packages whose artifacts the restore leaves out
	// (Discover content not on the tenant)
	skippedPackages map[string]bool
}

func (r *restorer) step(area, object, action, detail string, err error) {
	s := RestoreStep{Area: area, Object: object, Action: action, Detail: detail}
	if err != nil {
		s.Action, s.Error = RestoreFailed, err.Error()
	}
	r.mu.Lock()
	r.res.Steps = append(r.res.Steps, s)
	r.res.Counts[s.Action]++
	r.mu.Unlock()
}

func (r *restorer) area(name string) bool {
	return len(r.opts.Areas) == 0 || slices.Contains(r.opts.Areas, name)
}

func (r *restorer) inPackages(pkg string) bool { return matchesAny(r.opts.Packages, pkg) }

// RestoreBackup restores a backup archive into the tenant: what the tenant
// API can take back (packages, artifacts, parameters, Partner Directory,
// credentials whose secret is given, certificates, access policies, number
// ranges, deployments) is restored, everything else is listed as a manual
// step. With DryRun nothing is written and the result is the plan.
//
// It never deletes anything on the tenant: content the backup does not
// have stays. Existing objects are updated only where they differ.
func RestoreBackup(ctx context.Context, exe *httpclnt.HTTPExecuter, archive string, opts RestoreOptions) (*RestoreResult, error) {
	for _, a := range opts.Areas {
		if !slices.Contains(RestoreAreas, a) {
			return nil, output.Usagef("unknown restore area %q (%s)", a, strings.Join(RestoreAreas, ", "))
		}
	}
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	ver, err := VerifyBackup(archive)
	if err != nil {
		return nil, err
	}
	var damaged []string
	for _, p := range ver.Problems {
		if !strings.HasPrefix(p, "the backup is incomplete") {
			damaged = append(damaged, p)
		}
	}
	if len(damaged) > 0 {
		return nil, output.Failed(fmt.Errorf("%s is damaged, nothing restored: %s", archive, strings.Join(damaged, "; ")))
	}
	dir, err := os.MkdirTemp("", "cpictl-restore-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if err := extractBackup(archive, dir); err != nil {
		return nil, err
	}
	r := &restorer{exe: exe, opts: opts, dir: dir, changed: map[string]bool{}, created: map[string]bool{}, skippedPackages: map[string]bool{}}
	r.man, r.idx = &BackupManifest{}, &BackupIndex{}
	if err := readJSONFile(filepath.Join(dir, "manifest.json"), r.man); err != nil {
		return nil, err
	}
	if err := readJSONFile(filepath.Join(dir, "index.json"), r.idx); err != nil {
		return nil, err
	}
	r.res = &RestoreResult{Archive: archive, SourceTenant: r.man.Tenant, SourceHost: r.man.Host, TargetHost: opts.TargetHost,
		BackupCreatedAt: r.man.CreatedAt, DryRun: opts.DryRun, Areas: opts.Areas, Steps: []RestoreStep{}, Counts: map[string]int{}}
	if len(r.res.Areas) == 0 {
		r.res.Areas = RestoreAreas
	}
	if !r.man.Complete {
		r.step("backup", archive, RestoreInfo, fmt.Sprintf("the backup is incomplete (%d warning(s) at creation): %s", len(r.man.Warnings), strings.Join(r.man.Warnings, "; ")), nil)
	}
	if opts.TargetHost != "" && r.man.Host != "" && !strings.EqualFold(cpi.TenantID(opts.TargetHost), cpi.TenantID(r.man.Host)) {
		r.step("backup", archive, RestoreInfo, fmt.Sprintf("backup of tenant %s restored into %s", r.man.Tenant, cpi.TenantID(opts.TargetHost)), nil)
	}

	steps := []struct {
		area string
		run  func(context.Context) error
	}{
		{RestorePackages, r.packages}, {RestoreArtifacts, r.artifacts}, {RestoreParameters, r.parameters},
		{RestorePartnerDirectory, r.partnerDirectory}, {RestoreCredentials, r.credentials}, {RestoreCertificates, r.certificates},
		{RestoreAccessPolicies, r.accessPolicies}, {RestoreNumberRanges, r.numberRanges}, {RestoreDeploy, r.deploy},
	}
	for _, s := range steps {
		if ctx.Err() != nil {
			return r.res, ctx.Err()
		}
		if !r.area(s.area) {
			continue
		}
		if err := s.run(ctx); err != nil {
			r.step(s.area, "", RestoreFailed, "", err)
		}
	}
	r.runtimeDataNotes()
	if r.res.Counts[RestoreFailed] > 0 {
		return r.res, output.Partial(fmt.Errorf("%d restore step(s) failed", r.res.Counts[RestoreFailed]))
	}
	return r.res, nil
}

// extractBackup unpacks the archive's regular files below dir; names that
// would leave dir are refused.
func extractBackup(archive, dir string) error {
	return readBackupEntries(archive, func(name string, rd io.Reader) error {
		clean := path.Clean(name)
		if clean == "." || path.IsAbs(clean) || strings.HasPrefix(clean, "../") || clean == ".." || strings.Contains(clean, "\\") {
			return fmt.Errorf("%s: entry %q leaves the archive", archive, name)
		}
		target := filepath.Join(dir, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, rd)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return err
	})
}

func readJSONFile(file string, v any) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(file), err)
	}
	return nil
}

func (r *restorer) file(rel string) string { return filepath.Join(r.dir, filepath.FromSlash(rel)) }

func (r *restorer) exists(rel string) bool {
	_, err := os.Stat(r.file(rel))
	return err == nil
}

// --- packages and artifacts ---------------------------------------------------

func (r *restorer) packages(ctx context.Context) error {
	ip := cpi.NewIntegrationPackage(r.exe)
	have, err := ip.GetPackagesList()
	if err != nil {
		return err
	}
	for _, p := range r.idx.Packages {
		if !r.inPackages(p.ID) {
			continue
		}
		switch {
		case slices.Contains(have, p.ID):
			r.step(RestorePackages, p.ID, RestoreUnchanged, "exists", nil)
		case strings.EqualFold(p.Mode, "READ_ONLY"):
			r.skippedPackages[p.ID] = true
			r.step(RestorePackages, p.ID, RestoreManual, fmt.Sprintf("standard content (%s): copy %q from Discover in the Web UI, then run the restore again for its parameters and deployments", p.Vendor, p.Name), nil)
		default:
			var f cpi.PackageFields
			if err := readJSONFile(r.file("packages/"+safeName(p.ID)+"/package.json"), &f); err != nil {
				r.step(RestorePackages, p.ID, RestoreFailed, "", err)
				continue
			}
			f.Mode = ""
			if !r.opts.DryRun {
				err = ip.Create(&cpi.PackageSingleData{Root: f})
			}
			r.step(RestorePackages, p.ID, RestoreCreate, fmt.Sprintf("%q, version %s", p.Name, p.Version), err)
		}
	}
	return nil
}

func (r *restorer) artifacts(ctx context.Context) error {
	type job struct {
		pkg BackupPackage
		a   BackupArtifact
	}
	var jobs []job
	for _, p := range r.idx.Packages {
		if !r.inPackages(p.ID) {
			continue
		}
		if r.skippedPackages[p.ID] || (strings.EqualFold(p.Mode, "READ_ONLY") && !r.area(RestorePackages)) {
			for _, a := range p.Artifacts {
				r.step(RestoreArtifacts, a.ID, RestoreSkip, "in standard package "+p.ID+" (copied from Discover, not uploaded)", nil)
			}
			continue
		}
		if strings.EqualFold(p.Mode, "READ_ONLY") {
			continue // on the tenant from Discover: its artifacts are SAP's
		}
		for _, a := range p.Artifacts {
			jobs = append(jobs, job{p, a})
		}
	}
	sem := make(chan struct{}, max(r.opts.Parallel, 1))
	var wg sync.WaitGroup
	for _, j := range jobs {
		if ctx.Err() != nil {
			break
		}
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			r.artifact(j.pkg.ID, j.a)
		})
	}
	wg.Wait()
	return ctx.Err()
}

func (r *restorer) artifact(pkg string, a BackupArtifact) {
	if a.File == "" {
		r.step(RestoreArtifacts, a.ID, RestoreManual, "not in the backup (its download failed at backup time): restore it from Git or another backup", nil)
		return
	}
	if !cpi.IsValidArtifactType(a.Type) {
		r.step(RestoreArtifacts, a.ID, RestoreManual, "type "+a.Type+" cannot be uploaded through the API", nil)
		return
	}
	dir, err := os.MkdirTemp(r.dir, "artifact-*")
	if err != nil {
		r.step(RestoreArtifacts, a.ID, RestoreFailed, "", err)
		return
	}
	defer os.RemoveAll(dir)
	if err := unzipTo(r.file(a.File), dir); err != nil {
		r.step(RestoreArtifacts, a.ID, RestoreFailed, "", err)
		return
	}
	// keep: the content goes back exactly as backed up; the version the
	// tenant has stays (a new artifact gets the backup's Bundle-Version), and
	// running artifacts keep running until the deploy area
	res, err := UploadArtifact(r.exe, UploadRequest{ID: a.ID, Name: a.Name, Type: a.Type, PackageID: pkg, Dir: dir,
		Versioning: versioning.Keep, DryRun: r.opts.DryRun, SkipDraft: true, KeepRuntime: true})
	if err != nil {
		r.step(RestoreArtifacts, a.ID, RestoreFailed, "", err)
		return
	}
	if res.Skipped != "" {
		r.step(RestoreArtifacts, a.ID, RestoreManual, res.Reason, nil)
		return
	}
	action := map[string]string{"CREATED": RestoreCreate, "UPDATED": RestoreUpdate}[res.Action]
	if action == "" {
		action = RestoreUnchanged
	}
	r.mu.Lock()
	if action != RestoreUnchanged || res.Redeploy {
		r.changed[a.ID] = true
	}
	if action == RestoreCreate {
		r.created[a.ID] = true
	}
	r.mu.Unlock()
	detail := fmt.Sprintf("%s in %s, backup version %s", a.Type, pkg, a.Version)
	if res.Version != "" && res.Version != a.Version {
		detail += ", tenant version " + res.Version + " stays"
	}
	r.step(RestoreArtifacts, a.ID, action, detail, nil)
}

// unzipTo extracts an artifact zip below dir (names leaving dir refused).
func unzipTo(file, dir string) error {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		clean := path.Clean(strings.ReplaceAll(f.Name, "\\", "/"))
		if clean == "." || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("%s: entry %q leaves the zip", filepath.Base(file), f.Name)
		}
		target := filepath.Join(dir, filepath.FromSlash(clean))
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxBackupEntry))
		rc.Close()
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// --- parameters ----------------------------------------------------------------

func (r *restorer) parameters(ctx context.Context) error {
	for _, p := range r.idx.Packages {
		if !r.inPackages(p.ID) || r.skippedPackages[p.ID] {
			continue
		}
		for _, a := range p.Artifacts {
			if a.Parameters == 0 {
				continue
			}
			rel := "packages/" + safeName(p.ID) + "/" + safeName(a.ID) + ".parameters.json"
			var list []struct {
				ParameterKey   string
				ParameterValue string
			}
			if err := readJSONFile(r.file(rel), &list); err != nil {
				r.step(RestoreParameters, a.ID, RestoreFailed, "", err)
				continue
			}
			values := map[string]string{}
			for _, kv := range list {
				values[kv.ParameterKey] = kv.ParameterValue
			}
			if r.opts.DryRun && r.created[a.ID] {
				r.step(RestoreParameters, a.ID, RestoreUpdate, fmt.Sprintf("%d parameter(s), set after the artifact is created", len(values)), nil)
				continue
			}
			up, err := UpdateConfiguration(r.exe, a.ID, "active", values, r.opts.DryRun)
			if err != nil {
				r.step(RestoreParameters, a.ID, RestoreFailed, "", err)
				continue
			}
			if len(up.Updated) == 0 {
				r.step(RestoreParameters, a.ID, RestoreUnchanged, fmt.Sprintf("%d parameter(s)", len(values)), nil)
				continue
			}
			sort.Strings(up.Updated)
			r.mu.Lock()
			r.changed[a.ID] = true // a configuration change needs a deployment
			r.mu.Unlock()
			r.step(RestoreParameters, a.ID, RestoreUpdate, "set "+strings.Join(up.Updated, ", "), nil)
		}
	}
	return nil
}

// --- Partner Directory --------------------------------------------------------

func (r *restorer) partnerDirectory(ctx context.Context) error {
	if !r.exists("partner-directory") {
		if len(r.idx.PartnerDirectory) > 0 {
			r.step(RestorePartnerDirectory, "", RestoreManual, "the index lists parameters but the archive has no partner-directory/ folder", nil)
		}
		return nil
	}
	res, err := PDDeploy(cpi.NewPartnerDirectory(r.exe), pdrepo.New(r.file("partner-directory")), PDDeployOptions{Replace: true, DryRun: r.opts.DryRun})
	if res == nil {
		return err
	}
	for kind, b := range map[string]*cpi.BatchResult{"string": res.String, "binary": res.Binary} {
		if b == nil {
			continue
		}
		for _, k := range b.Created {
			r.step(RestorePartnerDirectory, k, RestoreCreate, kind+" parameter", nil)
		}
		for _, k := range b.Updated {
			r.step(RestorePartnerDirectory, k, RestoreUpdate, kind+" parameter", nil)
		}
		for _, k := range b.Unchanged {
			r.step(RestorePartnerDirectory, k, RestoreUnchanged, kind+" parameter", nil)
		}
		for _, e := range b.Errors {
			r.step(RestorePartnerDirectory, "", RestoreFailed, kind+" parameter", fmt.Errorf("%s", e))
		}
	}
	return nil
}

// --- credentials, certificates, access policies --------------------------------

func (r *restorer) credentials(ctx context.Context) error {
	rel := "security/credentials.yml"
	if !r.exists(rel) {
		return nil
	}
	f, err := LoadCredentialsFile(r.file(rel))
	if err != nil {
		return err
	}
	s := cpi.NewSecurity(r.exe)
	have := map[string]bool{}
	if list, err := s.UserCredentials(); err == nil {
		for _, c := range list {
			have[cpi.KindUser+"/"+c.Name] = true
		}
	}
	if list, err := s.OAuth2Credentials(); err == nil {
		for _, c := range list {
			have[cpi.KindOAuth2+"/"+c.Name] = true
		}
	}
	if list, err := s.SecureParameters(); err == nil {
		for _, c := range list {
			have[cpi.KindSecureParam+"/"+c.Name] = true
		}
	}
	type cred struct {
		spec       any
		name, env  string
		kind, what string
	}
	var creds []cred
	for _, c := range f.UserCredentials {
		creds = append(creds, cred{c, c.Name, c.Password.Env, cpi.KindUser, "user credential"})
	}
	for _, c := range f.OAuth2Credentials {
		creds = append(creds, cred{c, c.Name, c.ClientSecret.Env, cpi.KindOAuth2, "OAuth2 credential"})
	}
	for _, c := range f.SecureParameters {
		creds = append(creds, cred{c, c.Name, c.Value.Env, cpi.KindSecureParam, "secure parameter"})
	}
	for _, c := range creds {
		exists := have[c.kind+"/"+c.name]
		if c.env == "" || r.opts.Getenv(c.env) == "" {
			if exists {
				r.step(RestoreCredentials, c.name, RestoreUnchanged, c.what+": kept (set "+c.env+" to replace its secret)", nil)
			} else {
				r.step(RestoreCredentials, c.name, RestoreManual, c.what+": set "+c.env+" from your vault and run the restore again (or cpictl credentials apply)", nil)
			}
			continue
		}
		action := map[bool]string{true: RestoreUpdate, false: RestoreCreate}[exists]
		var derr error
		if !r.opts.DryRun {
			_, derr = DeployCredential(r.exe, withSecretFrom(c.spec, r.opts.Getenv(c.env)), false)
		}
		r.step(RestoreCredentials, c.name, action, c.what+", secret from "+c.env, derr)
	}
	return nil
}

// withSecretFrom gives a credential spec its secret directly (the restore's
// Getenv may not be the process environment).
func withSecretFrom(spec any, secret string) any {
	src := SecretSource{Stdin: strings.NewReader(secret)}
	switch c := spec.(type) {
	case UserCredentialSpec:
		c.Password = src
		return c
	case OAuth2CredentialSpec:
		c.ClientSecret = src
		return c
	case SecureParameterSpec:
		c.Value = src
		return c
	}
	return spec
}

func (r *restorer) certificates(ctx context.Context) error {
	var items []BackupKeystoreItem
	if err := readJSONFile(r.file("security/keystore.json"), &items); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	entries, err := cpi.NewSecurity(r.exe).KeystoreEntries("system")
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, e := range entries {
		have[e.Alias] = true
	}
	for _, it := range items {
		switch {
		case it.Restore == "sap":
			continue
		case have[it.Alias]:
			r.step(RestoreCertificates, it.Alias, RestoreUnchanged, "exists", nil)
		case it.Restore == "certificate" && it.File != "":
			data, err := os.ReadFile(r.file(it.File))
			if err == nil && !r.opts.DryRun {
				_, err = ImportCertificate(r.exe, it.Alias, data, false)
			}
			r.step(RestoreCertificates, it.Alias, RestoreCreate, "certificate "+it.SubjectDN, err)
		case it.Restore == "certificate":
			r.step(RestoreCertificates, it.Alias, RestoreManual, "certificate "+it.SubjectDN+": not in the archive, import it again (cpictl keystore import-cert)", nil)
		default:
			r.step(RestoreCertificates, it.Alias, RestoreManual, fmt.Sprintf("%s %s: its private key never left the tenant. Generate it again and have it signed (cpictl keystore generate-key-pair, csr, upload-chain), or upload the original key; partners that trust the old certificate need the new one",
				strings.ToLower(cmpOrStr(it.Type, "key pair")), it.SubjectDN), nil)
		}
	}
	return nil
}

func cmpOrStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func refKey(x cpi.ArtifactReference) string {
	return strings.Join([]string{x.Type, x.ConditionAttribute, x.ConditionValue, x.ConditionType}, "\x00")
}

func (r *restorer) accessPolicies(ctx context.Context) error {
	var want []cpi.AccessPolicyDetail
	if err := readJSONFile(r.file("security/access-policies.json"), &want); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	have, err := ListAccessPolicies(r.exe)
	if err != nil {
		return err
	}
	for _, p := range want {
		i := slices.IndexFunc(have, func(h cpi.AccessPolicyDetail) bool { return h.RoleName == p.RoleName })
		refs := make([]cpi.ArtifactReference, 0, len(p.ArtifactReferences))
		for _, x := range p.ArtifactReferences {
			x.ID = ""
			refs = append(refs, x)
		}
		if i < 0 {
			var err error
			if !r.opts.DryRun {
				_, err = CreateAccessPolicy(r.exe, p.RoleName, p.Description, refs)
			}
			r.step(RestoreAccessPolicies, p.RoleName, RestoreCreate, fmt.Sprintf("%d artifact reference(s)", len(refs)), err)
			continue
		}
		existing := map[string]bool{}
		for _, x := range have[i].ArtifactReferences {
			existing[refKey(x)] = true
		}
		added := 0
		var firstErr error
		for _, x := range refs {
			if existing[refKey(x)] {
				continue
			}
			added++
			if !r.opts.DryRun {
				if _, err := AddArtifactReference(r.exe, have[i].ID, x); err != nil && firstErr == nil {
					firstErr = err
				}
			}
		}
		if added == 0 {
			r.step(RestoreAccessPolicies, p.RoleName, RestoreUnchanged, "exists with all references", nil)
		} else {
			r.step(RestoreAccessPolicies, p.RoleName, RestoreUpdate, fmt.Sprintf("add %d artifact reference(s); references the backup does not have stay", added), firstErr)
		}
	}
	return nil
}

// --- number ranges --------------------------------------------------------------

func (r *restorer) numberRanges(ctx context.Context) error {
	var want []cpi.NumberRange
	if err := readJSONFile(r.file("tenant/number-ranges.json"), &want); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	st := cpi.NewStores(r.exe)
	have, err := st.NumberRanges()
	if err != nil {
		return err
	}
	for _, w := range want {
		i := slices.IndexFunc(have, func(h cpi.NumberRange) bool { return h.Name == w.Name })
		if i < 0 {
			var err error
			if !r.opts.DryRun {
				err = st.SetNumberRange(w, true)
			}
			r.step(RestoreNumberRanges, w.Name, RestoreCreate, "current value "+w.CurrentValue, err)
			continue
		}
		h := have[i]
		// never lower a counter: numbers already issued would repeat
		next := w
		next.CurrentValue = h.CurrentValue
		detail := ""
		if numLess(h.CurrentValue, w.CurrentValue) {
			next.CurrentValue = w.CurrentValue
			detail = "current value " + h.CurrentValue + " -> " + w.CurrentValue
		}
		settings := h.Description != w.Description || h.MinValue != w.MinValue || h.MaxValue != w.MaxValue || h.FieldLength != w.FieldLength || h.Rotate != w.Rotate
		if detail == "" && !settings {
			r.step(RestoreNumberRanges, w.Name, RestoreUnchanged, "current value "+h.CurrentValue+" (backup "+w.CurrentValue+")", nil)
			continue
		}
		if settings {
			detail = strings.TrimPrefix(detail+"; settings as in the backup", "; ")
		}
		if h.CurrentValue != next.CurrentValue || settings {
			var err error
			if !r.opts.DryRun {
				err = st.SetNumberRange(next, false)
			}
			r.step(RestoreNumberRanges, w.Name, RestoreUpdate, detail, err)
		}
	}
	return nil
}

func numLess(a, b string) bool {
	x, err1 := strconv.ParseInt(strings.TrimSpace(a), 10, 64)
	y, err2 := strconv.ParseInt(strings.TrimSpace(b), 10, 64)
	return err1 == nil && err2 == nil && x < y
}

// --- deploy ---------------------------------------------------------------------

func (r *restorer) deploy(ctx context.Context) error {
	types, pkgOf, designVersion := map[string]string{}, map[string]string{}, map[string]string{}
	for _, p := range r.idx.Packages {
		for _, a := range p.Artifacts {
			types[a.ID], pkgOf[a.ID], designVersion[a.ID] = a.Type, p.ID, a.Version
		}
	}
	// the backup holds designtime content only: when an older version was
	// running, the restore deploys the designtime one
	note := map[string]string{}
	running := map[string]cpi.RuntimeArtifact{}
	if list, err := cpi.NewContent(r.exe).RuntimeArtifacts(nil); err == nil {
		for _, a := range list {
			running[a.Id] = a
		}
	} else {
		return err
	}
	var first, second []Artifact
	for _, s := range r.idx.Runtime {
		pkg, known := pkgOf[s.ID]
		if known && !r.inPackages(pkg) {
			continue
		}
		switch {
		case s.Runtime != "":
			r.step(RestoreDeploy, s.ID, RestoreManual, "was deployed on integration cell "+s.Runtime+": deploy it there", nil)
			continue
		case !known:
			if len(r.opts.Packages) == 0 {
				r.step(RestoreDeploy, s.ID, RestoreManual, "was deployed, but its designtime artifact is not in the backup", nil)
			}
			continue
		case r.skippedPackages[pkg]:
			r.step(RestoreDeploy, s.ID, RestoreManual, "in standard package "+pkg+": deploy it after copying the package from Discover", nil)
			continue
		case strings.EqualFold(s.Status, "ERROR"):
			r.step(RestoreDeploy, s.ID, RestoreSkip, "was in error at backup time ("+s.Version+"): not deployed", nil)
			continue
		}
		rt, isRunning := running[s.ID]
		if isRunning && strings.EqualFold(rt.Status, "STARTED") && !r.changed[s.ID] {
			r.step(RestoreDeploy, s.ID, RestoreUnchanged, "running version "+rt.Version, nil)
			continue
		}
		if dv := designVersion[s.ID]; dv != "" && dv != s.Version {
			note[s.ID] = fmt.Sprintf("; version %s was running at backup time, the backup holds designtime %s, which is deployed", s.Version, dv)
		}
		a := Artifact{ID: s.ID, Type: types[s.ID], PackageID: pkg, Force: true, AllowDowngrade: true, Versioning: versioning.Keep}
		if types[s.ID] == "Integration" {
			second = append(second, a)
		} else {
			first = append(first, a) // script collections and mappings before the flows that use them
		}
	}
	if r.opts.DryRun {
		for _, a := range append(first, second...) {
			r.step(RestoreDeploy, a.ID, RestoreDeployIt, a.Type+" (was running at backup time)"+note[a.ID], nil)
		}
		return nil
	}
	opts := r.opts.Deploy
	for _, batch := range [][]Artifact{first, second} {
		if len(batch) == 0 {
			continue
		}
		for _, res := range Deploy(ctx, NewTenant(r.exe), batch, opts) {
			var err error
			if res.Status != StatusDeployed && res.Status != StatusSkipped {
				err = fmt.Errorf("%s: %s", res.Status, cmpOrStr(res.Error, "not deployed"))
			}
			r.step(RestoreDeploy, res.ID, RestoreDeployIt, res.Type+" version "+res.Version+note[res.ID], err)
		}
	}
	return nil
}

// runtimeDataNotes lists what a backup holds only by name.
func (r *restorer) runtimeDataNotes() {
	if n := len(r.idx.Variables); n > 0 {
		r.step("runtime-data", "variables", RestoreInfo, fmt.Sprintf("%d variable(s): runtime data, recreated when the flows run", n), nil)
	}
	if n := len(r.idx.DataStores); n > 0 {
		r.step("runtime-data", "data stores", RestoreInfo, fmt.Sprintf("%d data store(s): their entries are runtime data and not in the backup", n), nil)
	}
	if n := len(r.idx.JMSQueues); n > 0 {
		r.step("runtime-data", "JMS queues", RestoreInfo, fmt.Sprintf("%d JMS queue(s): created when the flows using them are deployed; their messages are not in the backup", n), nil)
	}
}

// --- the plan as a file -------------------------------------------------------------

// WriteRestorePlan writes the result to file: JSON for .json, else
// Markdown (the plan to review before the restore, or its report).
func WriteRestorePlan(res *RestoreResult, file string) error {
	var data []byte
	if strings.EqualFold(filepath.Ext(file), ".json") {
		b, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return err
		}
		data = append(b, '\n')
	} else {
		data = RestorePlanMarkdown(res)
	}
	if dir := filepath.Dir(file); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(file, data, 0o644)
}

// RestorePlanMarkdown renders the result as Markdown.
func RestorePlanMarkdown(res *RestoreResult) []byte {
	var b bytes.Buffer
	title := "Restore report"
	if res.DryRun {
		title = "Restore plan (dry run: nothing was changed)"
	}
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "- Backup: `%s`, tenant %s, created %s\n", res.Archive, res.SourceTenant, res.BackupCreatedAt.UTC().Format(time.RFC3339))
	if res.TargetHost != "" {
		fmt.Fprintf(&b, "- Target: %s\n", res.TargetHost)
	}
	fmt.Fprintf(&b, "- Areas: %s\n", strings.Join(res.Areas, ", "))
	var counts []string
	for _, a := range []string{RestoreCreate, RestoreUpdate, RestoreDeployIt, RestoreUnchanged, RestoreSkip, RestoreManual, RestoreFailed} {
		if n := res.Counts[a]; n > 0 {
			counts = append(counts, fmt.Sprintf("%s %d", a, n))
		}
	}
	fmt.Fprintf(&b, "- Steps: %s\n", strings.Join(counts, ", "))
	if res.DryRun {
		b.WriteString("\nThe actions say what the restore would do. Nothing is deleted: content the backup does not have stays on the tenant.\n")
	}
	if manual := res.Manual(); len(manual) > 0 {
		b.WriteString("\n## Manual steps\n\n")
		for _, s := range manual {
			fmt.Fprintf(&b, "- [ ] **%s** %s: %s\n", s.Area, mdCode(s.Object), s.Detail)
		}
	}
	areas := append([]string{"backup"}, RestoreAreas...)
	areas = append(areas, "runtime-data")
	for _, area := range areas {
		var rows []RestoreStep
		for _, s := range res.Steps {
			if s.Area == area {
				rows = append(rows, s)
			}
		}
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n\n| Object | Action | Detail |\n|---|---|---|\n", area)
		for _, s := range rows {
			detail := s.Detail
			if s.Error != "" {
				detail = strings.TrimPrefix(detail+"; error: "+s.Error, "; ")
			}
			fmt.Fprintf(&b, "| %s | %s | %s |\n", mdCode(s.Object), s.Action, mdCell(detail))
		}
	}
	return b.Bytes()
}

func mdCode(s string) string {
	if s == "" {
		return ""
	}
	return "`" + strings.ReplaceAll(s, "`", "'") + "`"
}

func mdCell(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ").Replace(s)
}
