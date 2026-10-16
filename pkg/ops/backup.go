package ops

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/pdrepo"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

// BackupSchema identifies the backup format (manifest.json).
const BackupSchema = "cpictl.backup/v1"

// BackupManifest is manifest.json: what the archive is and the checksum of
// every other file in it.
type BackupManifest struct {
	Schema        string    `json:"schema"`
	Tenant        string    `json:"tenant"`
	Host          string    `json:"host"`
	CreatedAt     time.Time `json:"createdAt"`
	CpictlVersion string    `json:"cpictlVersion,omitempty"`
	// Complete is false when an area could not be read (Warnings).
	Complete bool `json:"complete"`
	// Warnings are areas or artifacts that could not be backed up.
	Warnings []string `json:"warnings"`
	// Unavailable are areas this tenant does not offer (e.g. secure
	// parameters on Cloud Foundry).
	Unavailable []string              `json:"unavailable,omitempty"`
	Counts      map[string]int        `json:"counts"`
	Files       map[string]BackupFile `json:"files"`
}

// BackupFile is one file of the archive.
type BackupFile struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// BackupIndex is index.json: everything a comparison needs, without the
// content (versions, hashes, states, names).
type BackupIndex struct {
	Packages         []BackupPackage      `json:"packages"`
	Runtime          []RuntimeState       `json:"runtime"`
	Credentials      []BackupCredential   `json:"credentials"`
	Keystore         []BackupKeystoreItem `json:"keystore"`
	PartnerDirectory []BackupPDParameter  `json:"partnerDirectory"`
	Variables        []cpi.Variable       `json:"variables"`
	NumberRanges     []cpi.NumberRange    `json:"numberRanges"`
	DataStores       []cpi.DataStore      `json:"dataStores"`
	JMSQueues        []BackupQueue        `json:"jmsQueues"`
	AccessPolicies   int                  `json:"accessPolicies"`
}

// BackupPackage is an integration package and its artifacts.
type BackupPackage struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Vendor  string `json:"vendor,omitempty"`
	// Mode is the tenant's package mode (EDIT_ALLOWED, READ_ONLY for
	// standard content from Discover, which is copied again rather than
	// uploaded on restore).
	Mode      string           `json:"mode,omitempty"`
	Artifacts []BackupArtifact `json:"artifacts"`
}

// BackupArtifact is one designtime artifact.
type BackupArtifact struct {
	ID         string     `json:"id"`
	Type       string     `json:"type"`
	Name       string     `json:"name,omitempty"`
	Version    string     `json:"version"`
	Draft      bool       `json:"draft,omitempty"`
	ModifiedAt *time.Time `json:"modifiedAt,omitempty"`
	ModifiedBy string     `json:"modifiedBy,omitempty"`
	// File is the zip in the archive, SHA256 its checksum (empty when the
	// download failed, see the manifest's warnings).
	File   string `json:"file,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	// Parameters is the number of externalised parameters, ParametersSHA256
	// the checksum of their values.
	Parameters       int    `json:"parameters,omitempty"`
	ParametersSHA256 string `json:"parametersSha256,omitempty"`
}

// BackupCredential is a credential's metadata; the secret is never readable.
type BackupCredential struct {
	Kind     string `json:"kind"` // user, oauth2, secure-param
	Name     string `json:"name"`
	User     string `json:"user,omitempty"`
	ClientID string `json:"clientId,omitempty"`
	TokenURL string `json:"tokenServiceUrl,omitempty"`
	// SecretEnv is the environment variable credentials.yml expects the
	// secret in on restore.
	SecretEnv string `json:"secretEnv"`
}

// BackupKeystoreItem is a keystore entry and how a restore gets it back.
type BackupKeystoreItem struct {
	cpi.KeystoreEntry
	// Restore: certificate (imported from certs/), regenerate (a key pair
	// or SSH key: its private key never leaves the tenant), sap (managed by
	// SAP).
	Restore string `json:"restore"`
	File    string `json:"file,omitempty"`
}

// BackupPDParameter is one Partner Directory parameter.
type BackupPDParameter struct {
	PID         string `json:"pid"`
	ID          string `json:"id"`
	Kind        string `json:"kind"` // string, binary
	ContentType string `json:"contentType,omitempty"`
	SHA256      string `json:"sha256"`
}

// BackupQueue is a JMS queue (its messages are not backed up).
type BackupQueue struct {
	Name      string `json:"name"`
	Exclusive bool   `json:"exclusive,omitempty"`
	Messages  int64  `json:"messages"`
}

// BackupOptions configure CreateBackup.
type BackupOptions struct {
	// Output is the archive to write (.tar.gz).
	Output string
	// Packages limits the packages (patterns with *; empty: all).
	Packages []string
	// Cells also records the deployment state of integration cells.
	Cells bool
	// Parallel is the number of artifacts downloaded at a time (default 8).
	Parallel      int
	CpictlVersion string
	Progress      ProgressFunc
}

// BackupResult is the outcome of CreateBackup.
type BackupResult struct {
	File     string          `json:"file"`
	Size     int64           `json:"size"`
	SHA256   string          `json:"sha256"`
	Manifest *BackupManifest `json:"manifest"`
}

// backupBuilder collects the files of a backup in a work directory.
type backupBuilder struct {
	dir  string
	man  *BackupManifest
	idx  *BackupIndex
	mu   sync.Mutex
	exe  *httpclnt.HTTPExecuter
	prog *progress
	// content false: metadata only (BuildTenantIndex), no downloads
	content bool
}

func (b *backupBuilder) warn(format string, args ...any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	msg := fmt.Sprintf(format, args...)
	log.Warn().Msg(msg)
	b.man.Warnings = append(b.man.Warnings, msg)
}

// area records an area's error: a 404 means the tenant does not offer it,
// anything else is a warning (the backup is incomplete).
func (b *backupBuilder) area(name string, err error) bool {
	if err == nil {
		return true
	}
	if httpclnt.StatusCode(err) == 404 {
		b.mu.Lock()
		b.man.Unavailable = append(b.man.Unavailable, name)
		b.mu.Unlock()
		log.Info().Msgf("%s: not available on this tenant", name)
		return false
	}
	b.warn("%s could not be read: %v", name, err)
	return false
}

func (b *backupBuilder) write(rel string, data []byte) error {
	p := filepath.Join(b.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}

func (b *backupBuilder) writeJSON(rel string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return b.write(rel, append(data, '\n'))
}

// CreateBackup writes a backup of the tenant into one archive: every
// package with its artifacts (zip as downloaded), their externalised
// parameters, the Partner Directory, the deployment state, credentials
// (metadata, plus credentials.yml with a placeholder per secret: secrets are
// not readable), the keystore (metadata and certificates; private keys never
// leave the tenant), number ranges, access policies, and the names of
// variables, data stores and JMS queues (their content is runtime data and
// not backed up). manifest.json lists every file with its checksum,
// index.json what a comparison needs. An area or artifact that cannot be
// read is a warning (Manifest.Complete false); the archive is still written.
func CreateBackup(ctx context.Context, exe *httpclnt.HTTPExecuter, host string, opts BackupOptions) (*BackupResult, error) {
	if opts.Output == "" {
		return nil, errors.New("no output file")
	}
	if opts.Parallel <= 0 {
		opts.Parallel = 8
	}
	work, err := os.MkdirTemp(filepath.Dir(opts.Output), ".cpictl-backup-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	b := newBackupBuilder(work, exe, host, opts, true)
	if err := b.collect(ctx, opts); err != nil {
		return nil, err
	}

	b.man.Complete = len(b.man.Warnings) == 0
	b.man.Counts["packages"] = len(b.idx.Packages)
	for _, p := range b.idx.Packages {
		b.man.Counts["artifacts"] += len(p.Artifacts)
	}
	b.man.Counts["deployed"] = len(b.idx.Runtime)
	b.man.Counts["credentials"] = len(b.idx.Credentials)
	b.man.Counts["keystoreEntries"] = len(b.idx.Keystore)
	b.man.Counts["partnerDirectoryParameters"] = len(b.idx.PartnerDirectory)
	if err := b.writeJSON("index.json", b.idx); err != nil {
		return nil, err
	}
	// checksums of everything but the manifest itself
	err = filepath.WalkDir(work, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(work, p)
		sum, size, err := fileSHA256(p)
		if err != nil {
			return err
		}
		b.man.Files[filepath.ToSlash(rel)] = BackupFile{Size: size, SHA256: sum}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := b.writeJSON("manifest.json", b.man); err != nil {
		return nil, err
	}
	if err := writeTarGz(work, opts.Output); err != nil {
		return nil, err
	}
	sum, size, err := fileSHA256(opts.Output)
	if err != nil {
		return nil, err
	}
	return &BackupResult{File: opts.Output, Size: size, SHA256: sum, Manifest: b.man}, nil
}

func newBackupBuilder(dir string, exe *httpclnt.HTTPExecuter, host string, opts BackupOptions, content bool) *backupBuilder {
	return &backupBuilder{dir: dir, exe: exe, prog: newProgress(opts.Progress), content: content,
		man: &BackupManifest{Schema: BackupSchema, Tenant: cpi.TenantID(host), Host: host, CreatedAt: time.Now().UTC(),
			CpictlVersion: opts.CpictlVersion, Warnings: []string{}, Counts: map[string]int{}, Files: map[string]BackupFile{}},
		idx: &BackupIndex{Packages: []BackupPackage{}, Runtime: []RuntimeState{}, Credentials: []BackupCredential{},
			Keystore: []BackupKeystoreItem{}, PartnerDirectory: []BackupPDParameter{}, Variables: []cpi.Variable{},
			NumberRanges: []cpi.NumberRange{}, DataStores: []cpi.DataStore{}, JMSQueues: []BackupQueue{}}}
}

func (b *backupBuilder) collect(ctx context.Context, opts BackupOptions) error {
	if err := b.packages(ctx, opts); err != nil {
		return err
	}
	b.partnerDirectory()
	b.runtime(opts.Cells)
	b.security()
	b.tenantData()
	return ctx.Err()
}

// BuildTenantIndex reads what index.json of a backup would hold from the
// tenant, without downloading artifacts or parameters (artifacts are
// compared by version and change time), for a quick comparison with a
// backup. Warnings are areas that could not be read.
func BuildTenantIndex(ctx context.Context, exe *httpclnt.HTTPExecuter, host string, opts BackupOptions) (*BackupIndex, []string, error) {
	work, err := os.MkdirTemp("", ".cpictl-index-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(work)
	b := newBackupBuilder(work, exe, host, opts, false)
	if err := b.collect(ctx, opts); err != nil {
		return nil, nil, err
	}
	return b.idx, b.man.Warnings, nil
}

// packages backs up the packages, their artifacts and parameters.
func (b *backupBuilder) packages(ctx context.Context, opts BackupOptions) error {
	ip := cpi.NewIntegrationPackage(b.exe)
	list, err := ip.GetPackagesData()
	if err != nil {
		return fmt.Errorf("packages could not be listed: %w", err) // without packages there is no backup
	}
	type job struct {
		pkg int
		a   *cpi.ArtifactDetails
	}
	var jobs []job
	for _, p := range list {
		f := p.Root
		if !matchesAny(opts.Packages, f.Id) {
			continue
		}
		bp := BackupPackage{ID: f.Id, Name: f.Name, Version: f.Version, Vendor: f.Vendor, Mode: f.Mode, Artifacts: []BackupArtifact{}}
		if err := b.writeJSON("packages/"+safeName(f.Id)+"/package.json", f); err != nil {
			return err
		}
		arts, err := ip.GetAllArtifacts(f.Id)
		if err != nil {
			b.warn("artifacts of package %s could not be listed: %v", f.Id, err)
		}
		b.idx.Packages = append(b.idx.Packages, bp)
		for _, a := range arts {
			jobs = append(jobs, job{len(b.idx.Packages) - 1, a})
		}
	}
	results := make([]BackupArtifact, len(jobs))
	sem := make(chan struct{}, max(opts.Parallel, 1))
	var wg sync.WaitGroup
	for i, j := range jobs {
		if ctx.Err() != nil {
			break
		}
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			pkg := b.idx.Packages[j.pkg].ID
			results[i] = b.artifact(pkg, j.a)
			b.prog.report(len(jobs), "artifact %s/%s", pkg, j.a.Id)
		})
	}
	wg.Wait()
	for i, j := range jobs {
		b.idx.Packages[j.pkg].Artifacts = append(b.idx.Packages[j.pkg].Artifacts, results[i])
	}
	return ctx.Err()
}

// artifact downloads one artifact's zip and its externalised parameters.
func (b *backupBuilder) artifact(pkg string, a *cpi.ArtifactDetails) BackupArtifact {
	r := BackupArtifact{ID: a.Id, Type: a.ArtifactType, Name: a.Name, Version: a.Version, Draft: a.IsDraft, ModifiedBy: a.ModifiedBy}
	if !a.ModifiedAt.IsZero() {
		at := a.ModifiedAt
		r.ModifiedAt = &at
	}
	if !b.content {
		return r
	}
	dt := cpi.NewDesigntimeArtifact(a.ArtifactType, b.exe)
	if dt == nil {
		b.warn("artifact %s: type %s cannot be downloaded", a.Id, a.ArtifactType)
		return r
	}
	rel := "packages/" + safeName(pkg) + "/" + safeName(a.Id) + ".zip"
	target := filepath.Join(b.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		b.warn("artifact %s: %v", a.Id, err)
		return r
	}
	if err := dt.Download(target, a.Id); err != nil {
		b.warn("artifact %s of package %s could not be downloaded: %v", a.Id, pkg, err)
		return r
	}
	sum, _, err := fileSHA256(target)
	if err != nil {
		b.warn("artifact %s: %v", a.Id, err)
		return r
	}
	r.File, r.SHA256 = rel, sum
	if a.ArtifactType != "Integration" {
		return r
	}
	params, err := cpi.NewConfiguration(b.exe).Get(a.Id, "active")
	if err != nil {
		b.warn("parameters of %s could not be read: %v", a.Id, err)
		return r
	}
	if params != nil && len(params.Root.Results) > 0 {
		list := params.Root.Results
		sort.Slice(list, func(i, j int) bool { return list[i].ParameterKey < list[j].ParameterKey })
		data, _ := json.MarshalIndent(list, "", "  ")
		if err := b.write("packages/"+safeName(pkg)+"/"+safeName(a.Id)+".parameters.json", append(data, '\n')); err != nil {
			b.warn("parameters of %s: %v", a.Id, err)
			return r
		}
		h := sha256.Sum256(data)
		r.Parameters, r.ParametersSHA256 = len(list), hex.EncodeToString(h[:])
	}
	return r
}

// partnerDirectory backs up every Partner Directory parameter (the files
// pd-snapshot writes) and indexes them by checksum.
func (b *backupBuilder) partnerDirectory() {
	api := cpi.NewPartnerDirectory(b.exe)
	if b.content {
		repo := pdrepo.New(filepath.Join(b.dir, "partner-directory"))
		if _, err := PDSnapshot(api, repo, true, nil); !b.area("Partner Directory", err) {
			return
		}
	}
	strs, err := api.GetStringParameters("Pid,Id,Value")
	if !b.area("Partner Directory string parameters", err) {
		return
	}
	for _, p := range strs {
		h := sha256.Sum256([]byte(p.Value))
		b.idx.PartnerDirectory = append(b.idx.PartnerDirectory, BackupPDParameter{PID: p.Pid, ID: p.ID, Kind: "string", SHA256: hex.EncodeToString(h[:])})
	}
	bins, err := api.GetBinaryParameters("")
	if !b.area("Partner Directory binary parameters", err) {
		return
	}
	for _, p := range bins {
		h := sha256.Sum256([]byte(p.Value))
		b.idx.PartnerDirectory = append(b.idx.PartnerDirectory, BackupPDParameter{PID: p.Pid, ID: p.ID, Kind: "binary", ContentType: p.ContentType, SHA256: hex.EncodeToString(h[:])})
	}
	sort.Slice(b.idx.PartnerDirectory, func(i, j int) bool {
		x, y := b.idx.PartnerDirectory[i], b.idx.PartnerDirectory[j]
		return x.PID+"\x00"+x.ID < y.PID+"\x00"+y.ID
	})
}

// runtime records what is deployed (the state a restore deploys again).
func (b *backupBuilder) runtime(cells bool) {
	states, err := readRuntimeStates(b.exe, cells)
	if !b.area("runtime", err) {
		return
	}
	b.idx.Runtime = states
	_ = b.writeJSON("runtime/deployed.json", states)
}

// readRuntimeStates lists the deployed artifacts (and with cells those on
// integration cells), sorted.
func readRuntimeStates(exe *httpclnt.HTTPExecuter, cells bool) ([]RuntimeState, error) {
	list, err := cpi.NewContent(exe).RuntimeArtifacts(nil)
	if err != nil {
		return nil, err
	}
	out := []RuntimeState{}
	for _, a := range list {
		out = append(out, RuntimeState{ID: a.Id, Version: a.Version, Status: a.Status, DeployedBy: a.DeployedBy, DeployedOn: a.DeployedOn})
	}
	if cells {
		rt := cpi.NewRuntime(exe)
		locs, err := rt.Locations()
		if err != nil {
			return nil, err
		}
		for _, l := range locs {
			if l.ID == cpi.CloudIntegrationLocation {
				continue
			}
			arts, err := rt.LocationArtifacts(l.ID)
			if err != nil {
				return nil, err
			}
			for _, a := range arts {
				out = append(out, RuntimeState{ID: a.Id, Runtime: l.ID, Version: a.Version, Status: a.Status, DeployedBy: a.DeployedBy, DeployedOn: a.DeployedOn})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
	return out, nil
}

var reEnvUnsafe = regexp.MustCompile(`[^A-Z0-9]+`)

// secretEnv is the environment variable a restore reads a secret from.
func secretEnv(kind, name string) string {
	return "CPI_SECRET_" + strings.Trim(reEnvUnsafe.ReplaceAllString(strings.ToUpper(kind+"_"+name), "_"), "_")
}

// security backs up credential metadata with credentials.yml, the keystore
// (certificates; key pairs only as metadata) and access policies.
func (b *backupBuilder) security() {
	s := cpi.NewSecurity(b.exe)
	var file CredentialsFile
	if users, err := s.UserCredentials(); b.area("user credentials", err) {
		for _, c := range users {
			env := secretEnv("user", c.Name)
			b.idx.Credentials = append(b.idx.Credentials, BackupCredential{Kind: "user", Name: c.Name, User: c.User, SecretEnv: env})
			file.UserCredentials = append(file.UserCredentials, UserCredentialSpec{Name: c.Name, Kind: c.Kind, Description: c.Description,
				User: c.User, CompanyID: c.CompanyID, Password: SecretSource{Env: env}})
		}
	}
	if oauth, err := s.OAuth2Credentials(); b.area("OAuth2 credentials", err) {
		for _, c := range oauth {
			env := secretEnv("oauth2", c.Name)
			b.idx.Credentials = append(b.idx.Credentials, BackupCredential{Kind: "oauth2", Name: c.Name, ClientID: c.ClientID, TokenURL: c.TokenServiceURL, SecretEnv: env})
			file.OAuth2Credentials = append(file.OAuth2Credentials, OAuth2CredentialSpec{Name: c.Name, Description: c.Description,
				TokenServiceURL: c.TokenServiceURL, ClientID: c.ClientID, ClientSecret: SecretSource{Env: env},
				ClientAuthentication: c.ClientAuthentication, Scope: c.Scope, Resource: c.Resource, Audience: c.Audience})
		}
	}
	if secure, err := s.SecureParameters(); b.area("secure parameters", err) {
		for _, c := range secure {
			env := secretEnv("secure", c.Name)
			b.idx.Credentials = append(b.idx.Credentials, BackupCredential{Kind: "secure-param", Name: c.Name, SecretEnv: env})
			file.SecureParameters = append(file.SecureParameters, SecureParameterSpec{Name: c.Name, Description: c.Description, Value: SecretSource{Env: env}})
		}
	}
	if len(b.idx.Credentials) > 0 {
		data, err := yaml.Marshal(file)
		if err == nil {
			header := "# Credentials of tenant " + b.man.Tenant + " (" + b.man.CreatedAt.Format(time.RFC3339) + ").\n" +
				"# Secrets are not readable from the tenant: set each environment variable from your vault, then\n" +
				"#   cpictl credentials apply --file credentials.yml\n"
			_ = b.write("security/credentials.yml", append([]byte(header), data...))
		}
	}

	entries, err := s.KeystoreEntries("system")
	if b.area("keystore", err) {
		for _, e := range entries {
			item := BackupKeystoreItem{KeystoreEntry: e}
			switch {
			case strings.EqualFold(e.Owner, "SAP") || strings.HasPrefix(e.Alias, "sap_"):
				item.Restore = "sap"
			case strings.EqualFold(e.Type, "Certificate") || strings.EqualFold(e.Type, "TrustedCertificate"):
				item.Restore = "certificate"
			default:
				item.Restore = "regenerate"
			}
			if item.Restore != "sap" && b.content {
				// the public certificate of key pairs too: it tells which
				// key pair to regenerate or re-upload
				if der, err := s.ExportCertificate(e.Alias, "system"); err == nil && len(der) > 0 {
					item.File = "security/certs/" + safeName(e.Alias) + ".pem"
					_ = b.write(item.File, pemCertificate(der))
				} else if err != nil {
					b.warn("certificate of %s could not be exported: %v", e.Alias, err)
				}
			}
			b.idx.Keystore = append(b.idx.Keystore, item)
		}
		_ = b.writeJSON("security/keystore.json", b.idx.Keystore)
	}
	if policies, err := s.AccessPoliciesWithReferences(); b.area("access policies", err) {
		b.idx.AccessPolicies = len(policies)
		_ = b.writeJSON("security/access-policies.json", policies)
	}
}

// pemCertificate encodes DER as PEM (a PEM answer stays as it is).
func pemCertificate(data []byte) []byte {
	if strings.HasPrefix(strings.TrimSpace(string(data)), "-----BEGIN") {
		return data
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: data})
}

// tenantData backs up number ranges and the names of variables, data stores
// and JMS queues.
func (b *backupBuilder) tenantData() {
	st := cpi.NewStores(b.exe)
	if ranges, err := st.NumberRanges(); b.area("number ranges", err) {
		b.idx.NumberRanges = ranges
		_ = b.writeJSON("tenant/number-ranges.json", ranges)
	}
	if vars, err := st.Variables(); b.area("variables", err) {
		b.idx.Variables = vars
		_ = b.writeJSON("tenant/variables.json", vars)
	}
	if stores, err := st.DataStores(false); b.area("data stores", err) {
		b.idx.DataStores = stores
		_ = b.writeJSON("tenant/data-stores.json", stores)
	}
	if queues, err := st.Queues(); b.area("JMS queues", err) {
		for _, q := range queues {
			b.idx.JMSQueues = append(b.idx.JMSQueues, BackupQueue{Name: q.Name, Exclusive: q.Exclusive, Messages: q.Messages})
		}
		_ = b.writeJSON("tenant/jms-queues.json", b.idx.JMSQueues)
	}
}

var reUnsafeName = regexp.MustCompile(`[^A-Za-z0-9_.\-]`)

// safeName is an ID as a file name.
func safeName(id string) string {
	s := reUnsafeName.ReplaceAllString(id, "_")
	if s == "" || s == "." || s == ".." {
		return "_"
	}
	return s
}

func matchesAny(patterns []string, id string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		if ok, _ := path.Match(p, id); ok {
			return true
		}
	}
	return false
}

func fileSHA256(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// writeTarGz writes dir as a gzipped tar (sorted, fixed modes) to out,
// through a temporary file renamed at the end.
func writeTarGz(dir, out string) error {
	tmp := out + ".partial"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	var files []string
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return err
	})
	sort.Strings(files)
	for _, p := range files {
		if err != nil {
			break
		}
		err = func() error {
			rel, _ := filepath.Rel(dir, p)
			fi, err := os.Stat(p)
			if err != nil {
				return err
			}
			hdr := &tar.Header{Name: filepath.ToSlash(rel), Mode: 0o600, Size: fi.Size(), ModTime: fi.ModTime().UTC(), Typeflag: tar.TypeReg}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			src, err := os.Open(p)
			if err != nil {
				return err
			}
			defer src.Close()
			_, err = io.Copy(tw, src)
			return err
		}()
	}
	for _, c := range []io.Closer{tw, gz, f} {
		if cerr := c.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, out)
}

// BackupFileName is the archive name of a backup taken at t.
func BackupFileName(host string, t time.Time) string {
	tenant, _, _ := strings.Cut(cpi.TenantID(host), ".")
	return "cpi-backup_" + safeName(tenant) + "_" + t.UTC().Format("2006-01-02T15-04-05Z") + ".tar.gz"
}

// BackupScheduleFolder is where a backup taken at t goes for rotation by
// storage lifecycle rules: monthly on the 1st, weekly on Sundays, else daily.
func BackupScheduleFolder(t time.Time) string {
	t = t.UTC()
	switch {
	case t.Day() == 1:
		return "monthly"
	case t.Weekday() == time.Sunday:
		return "weekly"
	}
	return "daily"
}
