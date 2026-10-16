package ops

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func backupTenant(t *testing.T) *cpitest.Tenant {
	t.Helper()
	m := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", Package: "P", DesignVersion: "1.0.0", Runtime: &cpitest.Runtime{Version: "1.0.0", Status: "STARTED", DeployedOn: t0},
			Zip: zipOf(t, map[string]string{"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\nBundle-SymbolicName: A\n", "src/main/resources/parameters.prop": "a=b\n"})},
		"Lib": {Type: "ScriptCollection", Package: "P", DesignVersion: "1.0.0",
			Zip: zipOf(t, map[string]string{"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\nBundle-SymbolicName: Lib\n"})},
	})
	m.Packages = []cpitest.Package{{ID: "P", Name: "P", Version: "1.0.0", ShortText: "p"}}
	return m
}

// A backup holds the artifacts and the tenant's state, verifies, and a
// second backup after a change shows exactly that change.
func TestBackupCreateVerifyDiff(t *testing.T) {
	m := backupTenant(t)
	dir := t.TempDir()
	first := filepath.Join(dir, "first.tar.gz")
	res, err := CreateBackup(context.Background(), m.Executer(), "mytenant.it-cpi018.cfapps.eu10.hana.ondemand.com", BackupOptions{Output: first, CpictlVersion: "test"})
	require.NoError(t, err)
	assert.True(t, res.Manifest.Complete, "warnings: %v", res.Manifest.Warnings)
	assert.Equal(t, 1, res.Manifest.Counts["packages"])
	assert.Equal(t, 2, res.Manifest.Counts["artifacts"])
	assert.Equal(t, 1, res.Manifest.Counts["deployed"])
	assert.Contains(t, res.Manifest.Files, "packages/P/A.zip")
	assert.Contains(t, res.Manifest.Files, "packages/P/package.json")
	assert.Contains(t, res.Manifest.Files, "runtime/deployed.json")
	assert.NoFileExists(t, first+".partial")

	v, err := VerifyBackup(first)
	require.NoError(t, err)
	assert.True(t, v.OK, "%v", v.Problems)

	_, idx, err := ReadBackup(first)
	require.NoError(t, err)
	tenant, warnings, err := BuildTenantIndex(context.Background(), m.Executer(), "mytenant", BackupOptions{})
	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Empty(t, DiffBackupIndexes(idx, tenant), "nothing changed since the backup")

	m.Artifacts["A"].DesignVersion = "1.0.1"
	m.SetRuntime("A", &cpitest.Runtime{Version: "1.0.1", Status: "STARTED", DeployedOn: t1})
	second := filepath.Join(dir, "second.tar.gz")
	_, err = CreateBackup(context.Background(), m.Executer(), "mytenant", BackupOptions{Output: second})
	require.NoError(t, err)
	_, idx2, err := ReadBackup(second)
	require.NoError(t, err)
	var got []string
	for _, c := range DiffBackupIndexes(idx, idx2) {
		got = append(got, c.Change+" "+c.Area+" "+c.Key)
	}
	assert.Equal(t, []string{"changed artifact P/A", "changed runtime A"}, got)
}

// A changed file, or an archive that is not a backup, is found.
func TestBackupVerifyTampered(t *testing.T) {
	m := backupTenant(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "b.tar.gz")
	_, err := CreateBackup(context.Background(), m.Executer(), "mytenant", BackupOptions{Output: file})
	require.NoError(t, err)

	// unpack, change one artifact, pack again with the old manifest
	work := filepath.Join(dir, "work")
	tampered := filepath.Join(dir, "tampered.tar.gz")
	require.NoError(t, os.MkdirAll(work, 0o700))
	require.NoError(t, readBackupEntriesTo(file, work))
	require.NoError(t, os.WriteFile(filepath.Join(work, "packages", "P", "A.zip"), []byte("not a zip"), 0o600))
	require.NoError(t, writeTarGz(work, tampered))
	v, err := VerifyBackup(tampered)
	require.NoError(t, err)
	assert.False(t, v.OK)
	assert.Contains(t, strings.Join(v.Problems, "\n"), "packages/P/A.zip: checksum differs")
	assert.Contains(t, strings.Join(v.Problems, "\n"), "not a readable zip")

	plain := filepath.Join(dir, "plain.tar.gz")
	require.NoError(t, os.WriteFile(plain, []byte("hello"), 0o600))
	_, err = VerifyBackup(plain)
	assert.Error(t, err)
}

// readBackupEntriesTo unpacks a backup into dir.
func readBackupEntriesTo(file, dir string) error {
	return readBackupEntries(file, func(name string, r io.Reader) error {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		data, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		return os.WriteFile(p, data, 0o600)
	})
}

func TestBackupNames(t *testing.T) {
	at := time.Date(2026, 10, 18, 2, 0, 0, 0, time.UTC) // a Sunday
	assert.Equal(t, "cpi-backup_mytenant_2026-10-18T02-00-00Z.tar.gz", BackupFileName("mytenant.it-cpi018.cfapps.eu10.hana.ondemand.com", at))
	assert.Equal(t, "weekly", BackupScheduleFolder(at))
	assert.Equal(t, "monthly", BackupScheduleFolder(time.Date(2026, 11, 1, 2, 0, 0, 0, time.UTC)))
	assert.Equal(t, "daily", BackupScheduleFolder(time.Date(2026, 10, 19, 2, 0, 0, 0, time.UTC)))
	assert.Equal(t, "CPI_SECRET_OAUTH2_GRAPH_API", secretEnv("oauth2", "Graph-API"))
}
