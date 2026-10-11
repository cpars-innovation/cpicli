package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	gosync "sync"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/content"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// State remembers per artifact what the last snapshot saw on the tenant and
// wrote locally, so that an incremental snapshot can skip the download of
// artifacts that did not change, and an upload can compare with it instead
// of downloading (ops.UploadRequest.Baseline). Its methods are safe for
// concurrent use; read Artifacts directly only when no snapshot runs.
type State struct {
	mu     gosync.Mutex
	Format int `json:"format"`
	// Tenant is the host the snapshot was taken from: the state is used as
	// an upload baseline only for the same tenant.
	Tenant    string                   `json:"tenant,omitempty"`
	Artifacts map[string]ArtifactState `json:"artifacts"` // key "<package>/<artifact>"
	byID      map[string]ArtifactState
}

// ArtifactState is the signature of one artifact.
type ArtifactState struct {
	Type    string `json:"type"`
	Version string `json:"version"`
	// ModifiedAt is the tenant's last change (zero: not reported, the
	// artifact is never skipped).
	ModifiedAt time.Time `json:"modifiedAt"`
	// ConfigHash is the SHA-256 of the configured parameters (integration
	// flows): Configure changes need not change version or ModifiedAt.
	ConfigHash string `json:"configHash,omitempty"`
	// ContentHash is the SHA-256 of the local directory after the snapshot
	// (META-INF and src/main/resources): a changed or missing local copy is
	// downloaded again.
	ContentHash string `json:"contentHash"`
	// UploadHash is the content.UploadHash of the tenant's content as
	// downloaded (no Bundle-Version): an upload whose content has the same
	// hash would change nothing (see ops.UploadRequest.Baseline).
	UploadHash string `json:"uploadHash,omitempty"`
	// FilesHash is the content.TreeHash of the local folder as the snapshot
	// left it (every file, exact bytes): a different hash means local edits.
	FilesHash string `json:"filesHash,omitempty"`
	// Derived is set for a deployment copy of another artifact
	// ("<package>/<artifactDir>"): the snapshot does not write it.
	Derived string `json:"derived,omitempty"`
	// Draft is set when the artifact was in draft on the tenant at the last
	// snapshot: written as a draft (DraftHandling ADD), or skipped (SKIP; the
	// other fields are then those of the last saved version the snapshot
	// saw, without hashes when it never saw one).
	Draft bool `json:"draft,omitempty"`
}

// NewState returns an empty state.
func NewState() *State {
	return &State{Format: 1, Artifacts: map[string]ArtifactState{}}
}

// LoadState reads a state file; a missing file is an empty state.
func LoadState(path string) (*State, error) {
	st := &State{Format: 1, Artifacts: map[string]ArtifactState{}}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, st); err != nil {
		return nil, err
	}
	if st.Artifacts == nil {
		st.Artifacts = map[string]ArtifactState{}
	}
	return st, nil
}

// Save writes the state file (sorted, indented, for readable diffs).
func (st *State) Save(path string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ") // map keys are sorted
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func (st *State) get(key string) (ArtifactState, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	a, ok := st.Artifacts[key]
	return a, ok
}

func (st *State) remove(key string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.Artifacts, key)
	st.byID = nil
}

// keys returns the keys of one package ("<package>/...").
func (st *State) keys(packageID string) []string {
	st.mu.Lock()
	defer st.mu.Unlock()
	var out []string
	for k := range st.Artifacts {
		if strings.HasPrefix(k, packageID+"/") {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func (st *State) set(key string, a ArtifactState) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.Artifacts[key] = a
	st.byID = nil
}

// Artifact returns the state of an artifact by its ID (unique on a tenant,
// whatever the package).
func (st *State) Artifact(id string) (ArtifactState, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.byID == nil {
		st.byID = make(map[string]ArtifactState, len(st.Artifacts))
		for key, a := range st.Artifacts {
			_, artifactID, _ := strings.Cut(key, "/")
			st.byID[artifactID] = a
		}
	}
	a, ok := st.byID[id]
	return a, ok
}

// tenantSignature is what the tenant says about an artifact without a
// download: version and ModifiedAt from the listing, plus the hash of the
// configured parameters for integration flows.
func tenantSignature(exe *httpclnt.HTTPExecuter, a *cpi.ArtifactDetails) (ArtifactState, error) {
	sig := ArtifactState{Type: a.ArtifactType, Version: a.Version, ModifiedAt: a.ModifiedAt.UTC()}
	if a.ArtifactType != "Integration" {
		return sig, nil
	}
	params, err := cpi.NewConfiguration(exe).Get(a.Id, "active")
	if err != nil {
		return sig, err
	}
	lines := make([]string, 0, len(params.Root.Results))
	for _, p := range params.Root.Results {
		lines = append(lines, p.ParameterKey+"\x00"+p.ParameterValue+"\x00"+p.DataType)
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l))
		h.Write([]byte{'\n'})
	}
	sig.ConfigHash = hex.EncodeToString(h.Sum(nil))
	return sig, nil
}

func localContentHash(dir string) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, "META-INF", "MANIFEST.MF")); err != nil {
		return "", err
	}
	return content.ContentHash(os.DirFS(dir))
}
