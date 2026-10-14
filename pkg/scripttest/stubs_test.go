package scripttest

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The stand-ins are written with the template, in SAP's package paths, and the
// build has the libraries they import.
func TestInitWritesStandIns(t *testing.T) {
	dir := t.TempDir()
	res, err := Init(dir, false)
	require.NoError(t, err)
	for _, f := range []string{
		"tests/stubs/README.md",
		"tests/stubs/com/sap/gateway/ip/core/customdev/util/Message.java",
		"tests/stubs/com/sap/gateway/ip/core/customdev/processor/SoapFaultFactory.java",
		"tests/stubs/com/sap/it/script/v2/api/Message.java",
		"tests/stubs/com/sap/it/script/v2/api/Exchange.java",
		"tests/stubs/com/sap/it/api/ITApiFactory.java",
		"tests/stubs/com/sap/it/api/securestore/SecureStoreService.java",
		"tests/stubs/com/sap/it/api/keystore/KeystoreService.java",
		"tests/stubs/com/sap/it/api/pd/PartnerDirectoryService.java",
		"tests/stubs/com/sap/it/api/mapping/ValueMappingApi.java",
		"tests/stubs/com/sap/it/api/asdk/datastore/DataStoreService.java",
		"tests/stubs/com/sap/it/api/msglog/MessageLogFactory.java",
		"tests/stubs/cpictl/standin/CaseContext.java",
		"tests/stubs/cpictl/standin/MessageV1.java",
		"tests/stubs/cpictl/standin/MessageV2.java",
	} {
		assert.Contains(t, res.Written, f)
		assert.FileExists(t, filepath.Join(dir, filepath.FromSlash(f)))
	}
	for _, w := range res.Written {
		if strings.HasSuffix(w, ".java") {
			assert.True(t, strings.HasPrefix(w, "tests/stubs/"), "stand-ins only below tests/stubs: %s", w)
		}
	}
	assert.NotContains(t, strings.Join(res.Written, "\n"), "conformance", "the conformance tests are not part of the template")

	gradle, err := os.ReadFile(filepath.Join(dir, "build.gradle"))
	require.NoError(t, err)
	for _, dep := range []string{"camel-attachments:3.14.10", "javax.activation:1.2.0", "bcprov-jdk18on:1.78.1", "bcpkix-jdk18on:1.78.1", "bcutil-jdk18on:1.78.1"} {
		assert.Contains(t, string(gradle), dep)
	}
}

// Every Java file of the template declares the package its path says (Gradle
// and javac need that to find the classes).
func TestStandInPackagesMatchPaths(t *testing.T) {
	n := 0
	err := fs.WalkDir(template, "template/tests/stubs", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".java") {
			return err
		}
		n++
		data, err := template.ReadFile(p)
		require.NoError(t, err)
		want := "package " + strings.ReplaceAll(strings.TrimPrefix(filepath.ToSlash(filepath.Dir(p)), "template/tests/stubs/"), "/", ".") + ";"
		assert.Contains(t, string(data), want, p)
		return nil
	})
	require.NoError(t, err)
	assert.Greater(t, n, 50)
}

// TestStandInsBuild writes a project from the template, adds the conformance
// tests (testdata/conformance) and an example script with its test
// (testdata/example) and runs the real Gradle build. It needs a JDK 17+,
// Gradle and Maven Central, so it runs only with CPICTL_GRADLE_TESTS=1.
func TestStandInsBuild(t *testing.T) {
	if os.Getenv("CPICTL_GRADLE_TESTS") == "" {
		t.Skip("set CPICTL_GRADLE_TESTS=1 to run the Gradle build of the stand-ins (needs JDK 17+, gradle, network)")
	}
	if _, err := exec.LookPath("gradle"); err != nil {
		t.Skip("gradle not on PATH")
	}
	dir := t.TempDir()
	_, err := Init(dir, false)
	require.NoError(t, err)
	copyTree(t, "testdata/conformance", filepath.Join(dir, "tests", "scripts", "conformance"))
	copyTree(t, "testdata/example", dir)

	// --no-daemon: a daemon would keep the output pipe open after the build ends
	res, err := Run(context.Background(), Options{Project: dir, Command: []string{"gradle", "test", "--no-daemon", "--console=plain"}, Output: testingWriter{t}})
	require.NoError(t, err)
	assert.Zero(t, res.ExitCode, "gradle test")
	assert.Zero(t, res.Failed)
	assert.Zero(t, res.Errors)
	assert.Greater(t, res.Passed, 20, "conformance tests and the example ran")
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	require.NoError(t, filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		data, err := os.ReadFile(p)
		require.NoError(t, err)
		dst := filepath.Join(to, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o755))
		return os.WriteFile(dst, data, 0o644)
	}))
}

type testingWriter struct{ t *testing.T }

func (w testingWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
