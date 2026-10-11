package cmd

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/snapshot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, _ = w.Write([]byte(content))
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// tenantFlow is an integration flow as the tenant exports it: CRLF
// manifest and model, a timestamped unsorted parameters.prop, a binary.
func tenantFlow(t *testing.T, id, script, host string) []byte {
	return zipOf(t, map[string]string{
		"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\r\nBundle-SymbolicName: " + id + "; singleton:=true\r\nBundle-Name: " + id + "\r\nBundle-Version: 1.0.0\r\nSAP-BundleType: IntegrationFlow\r\n\r\n",
		"metainfo.prop":        "#Thu Oct 08 14:11:02 UTC 2026\r\ndescription=" + id + "\r\n",
		".project":             "<projectDescription><name>" + id + "</name></projectDescription>\r\n",
		"src/main/resources/scenarioflows/integrationflow/" + id + ".iflw": "<bpmn2:definitions>\r\n</bpmn2:definitions>\r\n",
		"src/main/resources/script/s.groovy":                               script,
		"src/main/resources/parameters.prop":                               "#Thu Oct 08 14:11:02 UTC 2026\r\nUser=u\r\nHost=" + host + "\r\nPort=443\r\n",
		"src/main/resources/lib/x.jar":                                     "PK\r\n\x00binary\r\n",
	})
}

// treeOf maps every file below dir to the hash of its bytes.
func treeOf(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		sum := sha256.Sum256(data)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	}))
	return out
}

type layoutRepo struct {
	t       *testing.T
	mock    *cpitest.Tenant
	repo    string // dir-git-repo: packages/ with .cpi/snapshot-state.json
	deploys string
}

func (l layoutRepo) snapshot(extra ...string) (cliRun, snapshotResult) {
	l.t.Helper()
	args := append([]string{"snapshot", "--dir-git-repo", l.repo, "--dir-work", l.t.TempDir(), "--git-skip-commit",
		"--deploy-config", l.deploys, "--output", "json"}, extra...)
	r := runMain(l.t, append(args, basicAuth(l.mock)...)...)
	var env struct {
		Result snapshotResult `json:"result"`
	}
	if r.stdout != "" {
		require.NoError(l.t, json.Unmarshal([]byte(r.stdout), &env), r.stdout)
	}
	return r, env.Result
}

func statuses(res snapshotResult) map[string]snapshot.Item {
	out := map[string]snapshot.Item{}
	for _, it := range res.Artifacts {
		out[it.Artifact] = it
	}
	return out
}

func newLayoutRepo(t *testing.T) layoutRepo {
	modified := time.Now().Add(-time.Hour).Truncate(time.Second)
	flow := func(id, script, host string) *cpitest.Artifact {
		return &cpitest.Artifact{Type: "Integration", DesignVersion: "1.0.3", Package: "EDM", Name: id, ModifiedAt: modified,
			Zip: tenantFlow(t, id, script, host), Parameters: map[string]string{"Host": host}}
	}
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		// deployed from the folder Outbound as itself (Host overridden to a) and as a copy (Host h)
		"Outbound":            flow("Outbound", "v1", "a.example"),
		"Herrenberg_Outbound": flow("Herrenberg_Outbound", "v1", "h.example"),
		"Other":               flow("Other", "v1", "o.example"),
		"Gone":                flow("Gone", "v1", "g.example"),
		"Elsewhere":           {Type: "Integration", DesignVersion: "1.0.0", Package: "Unrelated", Name: "Elsewhere", ModifiedAt: modified, Zip: tenantFlow(t, "Elsewhere", "v1", "e")},
	})
	mock.Packages = []cpitest.Package{{ID: "EDM", Version: "1.0.0"}, {ID: "Unrelated", Version: "1.0.0"}}
	root := t.TempDir()
	deploys := filepath.Join(root, "deployments")
	require.NoError(t, os.MkdirAll(deploys, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(deploys, "edm.yaml"), []byte(`packages:
  - integrationSuiteId: EDM
    packageDir: EDM
    artifacts:
      - artifactId: Outbound
        artifactDir: Outbound
        type: IntegrationFlow
        configOverrides: {Host: a.example}
      - artifactId: Herrenberg_Outbound
        artifactDir: Outbound
        type: IntegrationFlow
        configOverrides: {Host: h.example}
      - {artifactId: Other, artifactDir: Other, type: IntegrationFlow}
`), 0o644))
	return layoutRepo{t: t, mock: mock, repo: filepath.Join(root, "packages"), deploys: deploys}
}

func TestSnapshotSingleLayout(t *testing.T) {
	l := newLayoutRepo(t)
	edm := func(parts ...string) string { return filepath.Join(append([]string{l.repo, "EDM"}, parts...)...) }

	// dry run on an empty repository: everything new, the copy derived, nothing written
	r, res := l.snapshot("--dry-run", "--ids-include", "EDM")
	require.Equal(t, 0, r.code, r.stderr)
	st := statuses(res)
	assert.True(t, res.DryRun)
	assert.Equal(t, snapshot.StatusNew, st["Outbound"].Status)
	assert.Equal(t, snapshot.StatusDerived, st["Herrenberg_Outbound"].Status)
	assert.Equal(t, "EDM/Outbound", st["Herrenberg_Outbound"].Source)
	assert.NotContains(t, st, "Elsewhere", "--ids-include")
	assert.NoDirExists(t, l.repo, "a dry run writes nothing, no state either")

	// first snapshot
	r, res = l.snapshot()
	require.Equal(t, 0, r.code, r.stderr)
	assert.DirExists(t, edm("Outbound"))
	assert.NoDirExists(t, edm("Herrenberg_Outbound"), "deployment copies are not written")
	params, _ := os.ReadFile(edm("Outbound", "src", "main", "resources", "parameters.prop"))
	assert.Equal(t, "Host=a.example\nPort=443\nUser=u\n", string(params), "no timestamp, sorted, LF")
	mf, _ := os.ReadFile(edm("Outbound", "META-INF", "MANIFEST.MF"))
	assert.NotContains(t, string(mf), "\r")
	assert.Contains(t, string(mf), "Bundle-Version: 1.0.3")
	jar, _ := os.ReadFile(edm("Outbound", "src", "main", "resources", "lib", "x.jar"))
	assert.Equal(t, "PK\r\n\x00binary\r\n", string(jar), "binaries byte for byte")
	meta, _ := os.ReadFile(edm("Outbound", "metainfo.prop"))
	assert.Equal(t, "description=Outbound\n", string(meta), "metainfo.prop normalized like parameters.prop: no timestamp")
	assert.Empty(t, statuses(res)["Herrenberg_Outbound"].Warning, "same as its source")
	assert.Contains(t, st["Herrenberg_Outbound"].Warning, "not found locally", "dry run on an empty repository: no source to compare")

	// the repository holds the base value of overridden keys: the next
	// snapshot keeps it although the tenant has the deployed value
	require.NoError(t, os.WriteFile(edm("Outbound", "src", "main", "resources", "parameters.prop"), []byte("Host=base.example\nPort=443\nUser=u\n"), 0o644))
	r, _ = l.snapshot("--overwrite-local") // accept the edit as the new baseline
	require.Equal(t, 0, r.code, r.stderr)
	params, _ = os.ReadFile(edm("Outbound", "src", "main", "resources", "parameters.prop"))
	assert.Equal(t, "Host=base.example\nPort=443\nUser=u\n", string(params), "configOverrides keys stay as in the repository")

	// second run: no changes at all, the state file included
	before := treeOf(t, l.repo)
	r, res = l.snapshot()
	require.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, before, treeOf(t, l.repo), "snapshot twice: empty git status")
	assert.Equal(t, 0, res.Counts[snapshot.StatusChanged])
	r, _ = l.snapshot("--incremental")
	require.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, before, treeOf(t, l.repo))

	// a local edit is not overwritten
	script := edm("Other", "src", "main", "resources", "script", "s.groovy")
	require.NoError(t, os.WriteFile(script, []byte("local edit"), 0o644))
	r, res = l.snapshot()
	require.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, snapshot.StatusLocalModified, statuses(res)["Other"].Status)
	data, _ := os.ReadFile(script)
	assert.Equal(t, "local edit", string(data))
	r, _ = l.snapshot("--fail-on-local-modified")
	assert.Equal(t, 5, r.code, "CI gate")

	// the tenant changes too: still kept; --overwrite-local takes the tenant's
	l.mock.Artifacts["Other"].Zip = tenantFlow(t, "Other", "v2", "o.example")
	l.mock.Artifacts["Other"].ModifiedAt = time.Now()
	r, res = l.snapshot()
	require.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, snapshot.StatusLocalModified, statuses(res)["Other"].Status)
	r, res = l.snapshot("--overwrite-local")
	require.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, snapshot.StatusChanged, statuses(res)["Other"].Status)
	data, _ = os.ReadFile(script)
	assert.Equal(t, "v2", string(data))

	// the local edit deployed: tenant and repository agree, nothing written
	require.NoError(t, os.WriteFile(script, []byte("v3"), 0o644))
	l.mock.Artifacts["Other"].Zip = tenantFlow(t, "Other", "v3", "o.example")
	l.mock.Artifacts["Other"].ModifiedAt = time.Now().Add(time.Second)
	r, res = l.snapshot()
	require.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, snapshot.StatusUnchanged, statuses(res)["Other"].Status, statuses(res)["Other"].Note)

	// tenant changes: a file removed inside an artifact, a new artifact, a
	// deleted artifact, an edited deployment copy; plus a new local artifact
	l.mock.Artifacts["Outbound"].Zip = func() []byte {
		files := map[string]string{
			"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\r\nBundle-SymbolicName: Outbound; singleton:=true\r\nBundle-Name: Outbound\r\nBundle-Version: 1.0.0\r\n\r\n",
			"src/main/resources/scenarioflows/integrationflow/Outbound.iflw": "<bpmn2:definitions>\r\n</bpmn2:definitions>\r\n",
			"src/main/resources/parameters.prop":                             "Host=a.example\nPort=443\nUser=u\n",
		}
		return zipOf(t, files)
	}()
	l.mock.Artifacts["Outbound"].ModifiedAt = time.Now().Add(2 * time.Second)
	l.mock.Artifacts["New"] = &cpitest.Artifact{Type: "Integration", DesignVersion: "1.0.0", Package: "EDM", Name: "New", ModifiedAt: time.Now(), Zip: tenantFlow(t, "New", "v1", "n")}
	delete(l.mock.Artifacts, "Gone")
	l.mock.Artifacts["Herrenberg_Outbound"].Zip = tenantFlow(t, "Herrenberg_Outbound", "edited on the tenant", "h.example")
	l.mock.Artifacts["Herrenberg_Outbound"].ModifiedAt = time.Now()
	require.NoError(t, os.MkdirAll(edm("Draft", "META-INF"), 0o755))
	require.NoError(t, os.WriteFile(edm("Draft", "META-INF", "MANIFEST.MF"), []byte("Manifest-Version: 1.0\n"), 0o644))
	require.NoError(t, os.WriteFile(script, []byte("v4 local"), 0o644))

	before = treeOf(t, l.repo)
	r, res = l.snapshot("--dry-run", "--ids-include", "EDM")
	require.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, before, treeOf(t, l.repo), "dry run: no files, no state")
	st = statuses(res)
	assert.Equal(t, snapshot.StatusChanged, st["Outbound"].Status)
	assert.Equal(t, snapshot.StatusNew, st["New"].Status)
	assert.Equal(t, snapshot.StatusDeleted, st["Gone"].Status)
	assert.Equal(t, snapshot.StatusLocalOnly, st["Draft"].Status)
	assert.Equal(t, snapshot.StatusLocalModified, st["Other"].Status)
	assert.Equal(t, snapshot.StatusDerived, st["Herrenberg_Outbound"].Status)
	assert.Contains(t, st["Herrenberg_Outbound"].Warning, "src/main/resources/script/s.groovy", "edited on the tenant")
	assert.Equal(t, 1, res.Warnings)

	// text output names the statuses too
	r = runMain(t, append([]string{"snapshot", "--dir-git-repo", l.repo, "--dir-work", t.TempDir(), "--git-skip-commit",
		"--deploy-config", l.deploys, "--dry-run"}, basicAuth(l.mock)...)...)
	require.Equal(t, 0, r.code, r.stderr)
	for _, s := range []string{"new", "changed", "deleted", "local-modified", "local-only", "derived"} {
		assert.Contains(t, r.stderr, s)
	}

	// the real run: files deleted on the tenant go, deleted artifacts stay without --prune
	r, _ = l.snapshot()
	require.Equal(t, 0, r.code, r.stderr)
	assert.NoFileExists(t, edm("Outbound", "src", "main", "resources", "script", "s.groovy"), "the folder matches the tenant exactly")
	assert.NoFileExists(t, edm("Outbound", "metainfo.prop"))
	assert.DirExists(t, edm("New"))
	assert.DirExists(t, edm("Gone"), "not removed without --prune")
	r, res = l.snapshot("--prune")
	require.Equal(t, 0, r.code, r.stderr)
	assert.NoDirExists(t, edm("Gone"))
	assert.DirExists(t, edm("Draft"), "never snapshotted: kept")
	assert.DirExists(t, edm("Other"), "edited locally: kept")
	state, _ := os.ReadFile(filepath.Join(l.repo, ".cpi", "snapshot-state.json"))
	assert.NotContains(t, string(state), `"EDM/Gone"`)

	// a package gone from the tenant
	l.mock.Packages = []cpitest.Package{{ID: "EDM", Version: "1.0.0"}}
	delete(l.mock.Artifacts, "Elsewhere")
	r, res = l.snapshot("--dry-run")
	require.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, snapshot.StatusDeleted, statuses(res)["Elsewhere"].Status)
	r, _ = l.snapshot("--prune")
	require.Equal(t, 0, r.code, r.stderr)
	assert.NoDirExists(t, filepath.Join(l.repo, "Unrelated"))

	// --include-derived writes the copy like any other artifact
	r, _ = l.snapshot("--include-derived", "--overwrite-local")
	require.Equal(t, 0, r.code, r.stderr)
	assert.DirExists(t, edm("Herrenberg_Outbound"))
}

// The state file is deterministic: sorted keys, no run timestamps.
func TestSnapshotStateIsStable(t *testing.T) {
	l := newLayoutRepo(t)
	r, _ := l.snapshot()
	require.Equal(t, 0, r.code, r.stderr)
	state := filepath.Join(l.repo, ".cpi", "snapshot-state.json")
	first, err := os.ReadFile(state)
	require.NoError(t, err)
	assert.Contains(t, string(first), `"filesHash"`)
	assert.Contains(t, string(first), `"derived": "EDM/Outbound"`)
	time.Sleep(1100 * time.Millisecond)
	r, _ = l.snapshot()
	require.Equal(t, 0, r.code, r.stderr)
	second, _ := os.ReadFile(state)
	assert.Equal(t, string(first), string(second))
	assert.True(t, strings.Index(string(first), `"EDM/Herrenberg_Outbound"`) < strings.Index(string(first), `"EDM/Other"`), "sorted")
}

// deploymentPrefix variants (package DEVEDM, artifact DEV_Outbound) are not
// written; --deployment-prefix adds prefixes the config does not name.
func TestSnapshotSkipsPrefixedCopies(t *testing.T) {
	modified := time.Now().Add(-time.Hour).Truncate(time.Second)
	flow := func(pkg, id string) *cpitest.Artifact {
		return &cpitest.Artifact{Type: "Integration", DesignVersion: "1.0.0", Package: pkg, Name: id, ModifiedAt: modified, Zip: tenantFlow(t, id, "v1", "x")}
	}
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"Outbound": flow("EDM", "Outbound"), "DEV_Outbound": flow("DEVEDM", "DEV_Outbound"), "QA_Outbound": flow("QAEDM", "QA_Outbound"),
	})
	mock.Packages = []cpitest.Package{{ID: "EDM"}, {ID: "DEVEDM"}, {ID: "QAEDM"}}
	root := t.TempDir()
	cfg := filepath.Join(root, "dev.yaml")
	require.NoError(t, os.WriteFile(cfg, []byte("deploymentPrefix: DEV\npackages:\n  - integrationSuiteId: EDM\n    packageDir: EDM\n    artifacts:\n      - {artifactId: Outbound, artifactDir: Outbound, type: IntegrationFlow}\n"), 0o644))
	l := layoutRepo{t: t, mock: mock, repo: filepath.Join(root, "packages"), deploys: cfg}
	r, res := l.snapshot("--deployment-prefix", "QA")
	require.Equal(t, 0, r.code, r.stderr)
	st := statuses(res)
	assert.Equal(t, snapshot.StatusNew, st["Outbound"].Status, "the config is prefixed: the plain ID is the source")
	assert.Equal(t, snapshot.StatusDerived, st["DEV_Outbound"].Status)
	assert.Equal(t, "EDM/Outbound", st["DEV_Outbound"].Source)
	assert.Equal(t, snapshot.StatusDerived, st["QA_Outbound"].Status)
	assert.NoDirExists(t, filepath.Join(l.repo, "DEVEDM"), "no package folder, no package file")
	assert.NoDirExists(t, filepath.Join(l.repo, "QAEDM"))
	assert.FileExists(t, filepath.Join(l.repo, "EDM", "EDM.json"))
}

func zipFile(t *testing.T, data []byte, name string) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			require.NoError(t, err)
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(rc)
			rc.Close()
			return buf.String()
		}
	}
	return ""
}

// The orchestrator reads --packages-dir (the Git working tree) and never
// writes into it; one artifactDir deploys under every configured ID with its
// own configOverrides, and --plan lists every ID.
func TestOrchestratorReadOnlyOnRepository(t *testing.T) {
	l := newLayoutRepo(t)
	r, _ := l.snapshot()
	require.Equal(t, 0, r.code, r.stderr)
	// a change in the repository, so that both IDs need an upload
	require.NoError(t, os.WriteFile(filepath.Join(l.repo, "EDM", "Outbound", "src", "main", "resources", "script", "s.groovy"), []byte("v2"), 0o644))
	before := treeOf(t, l.repo)

	orchestrate := func(extra ...string) cliRun {
		args := append([]string{"orchestrator", "--packages-dir", l.repo, "--deploy-config", l.deploys, "--output", "json"}, extra...)
		return runMain(t, append(args, basicAuth(l.mock)...)...)
	}
	r = orchestrate("--plan")
	require.Equal(t, 0, r.code, r.stderr)
	var env struct {
		Result orchestratorResult `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &env))
	plan := map[string]PlanItem{}
	for _, it := range env.Result.Plan {
		plan[it.Artifact] = it
	}
	assert.Equal(t, "update", plan["Outbound"].Upload)
	assert.Equal(t, "update", plan["Herrenberg_Outbound"].Upload)
	assert.Contains(t, plan, "Other")
	assert.Equal(t, before, treeOf(t, l.repo), "--plan: repository unchanged")

	r = orchestrate("--update-only")
	require.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, before, treeOf(t, l.repo), "update: repository unchanged")
	for id, host := range map[string]string{"Outbound": "a.example", "Herrenberg_Outbound": "h.example"} {
		zipData := l.mock.Artifacts[id].Zip
		assert.Equal(t, 1, l.mock.Artifacts[id].Uploads, id)
		assert.Contains(t, zipFile(t, zipData, "src/main/resources/parameters.prop"), "Host="+host, "%s: its own configOverrides", id)
		assert.Contains(t, zipFile(t, zipData, "META-INF/MANIFEST.MF"), "Bundle-SymbolicName: "+id, id)
		assert.Equal(t, "v2", zipFile(t, zipData, "src/main/resources/script/s.groovy"), id)
	}
	r = orchestrate("--defer-deploy", "--pending-file", filepath.Join(t.TempDir(), "pending.json"))
	require.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, before, treeOf(t, l.repo), "defer: repository unchanged")
}
