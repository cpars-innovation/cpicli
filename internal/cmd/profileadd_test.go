package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/auth"
	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

type memKeyring map[string]string

func (m memKeyring) Get(s, u string) (string, error) {
	if v, ok := m[s+"/"+u]; ok {
		return v, nil
	}
	return "", keyring.ErrNotFound
}
func (m memKeyring) Set(s, u, v string) error { m[s+"/"+u] = v; return nil }
func (m memKeyring) Delete(s, u string) error {
	if _, ok := m[s+"/"+u]; !ok {
		return keyring.ErrNotFound
	}
	delete(m, s+"/"+u)
	return nil
}

// withKeyring makes profile commands use kr (nil: file only) instead of the
// developer's real keychain.
func withKeyring(t *testing.T, kr auth.Keyring) {
	old := profileKeyring
	profileKeyring = func() auth.Keyring { return kr }
	t.Cleanup(func() { profileKeyring = old })
}

func runWithStdin(t *testing.T, home, stdin string, args ...string) (int, string) {
	t.Helper()
	in, err := os.CreateTemp(t.TempDir(), "stdin")
	require.NoError(t, err)
	defer in.Close()
	_, _ = in.WriteString(stdin)
	_, _ = in.Seek(0, 0)
	old := os.Stdin
	os.Stdin = in
	defer func() { os.Stdin = old }()
	cpitest.SetHome(t, home)
	t.Chdir(t.TempDir())
	viper.Reset()
	t.Cleanup(viper.Reset)
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), args, &stdout, &stderr, "test", "test")
	return code, stdout.String() + stderr.String()
}

func clearTenantEnv(t *testing.T) {
	for _, k := range append(append(credentialConfigKeys, hostConfigKeys...), "profile", "config", "gateway", "token-store") {
		t.Setenv("CPICTL_"+upperEnv(k), "")
	}
}

func upperEnv(k string) string {
	b := []byte(k)
	for i, c := range b {
		switch {
		case c == '-':
			b[i] = '_'
		case c >= 'a' && c <= 'z':
			b[i] = c - 32
		}
	}
	return string(b)
}

const profileTestSecret = "s3cret-client-value"

// profile add from a service key: checked against the tenant before it is
// stored, the secret in the keychain and never in the file or the output,
// usable at once, shown without the secret, removed with it.
func TestProfileAddFromServiceKey(t *testing.T) {
	clearTenantEnv(t)
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": {}})
	mock.OAuth = true
	kr := memKeyring{}
	withKeyring(t, kr)
	home := t.TempDir()
	key := filepath.Join(t.TempDir(), "dev-api.json")
	require.NoError(t, os.WriteFile(key, []byte(fmt.Sprintf(`{"oauth":{"clientid":"sb-dev!b1|it!b2","clientsecret":"%s","url":"%s","tokenurl":"%s/oauth/token"}}`,
		profileTestSecret, mock.URL(), mock.URL())), 0o600))

	code, out := runWithStdin(t, home, "", "profile", "add", "dev", "--service-key", key, "--use")
	require.Equal(t, 0, code, out)
	assert.NotContains(t, out, profileTestSecret)
	assert.Contains(t, out, "secrets in the OS keychain")
	file := filepath.Join(home, ".cpictl", "dev.yaml")
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.NotContains(t, string(data), profileTestSecret, "the secret is not in the file")
	assert.Contains(t, string(data), "keychain-secrets: [oauth-clientsecret]")
	assert.Equal(t, profileTestSecret, kr[auth.KeyringService+"/profile:dev:oauth-clientsecret"])
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(file)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}

	// usable at once (the active profile), the secret read from the keychain
	code, out = runWithStdin(t, home, "", "packages")
	assert.Equal(t, 0, code, out)

	code, out = runWithStdin(t, home, "", "profile", "show", "dev")
	assert.Equal(t, 0, code, out)
	assert.Contains(t, out, "(in the OS keychain)")
	assert.NotContains(t, out, profileTestSecret)

	code, out = runWithStdin(t, home, "", "profile", "add", "dev", "--service-key", key)
	assert.Equal(t, 2, code)
	assert.Contains(t, out, "use --force")

	code, out = runWithStdin(t, home, "", "profile", "remove", "dev", "--confirm")
	assert.Equal(t, 0, code, out)
	assert.NoFileExists(t, file)
	assert.Empty(t, kr, "the keychain entry is gone")
}

// A key that does not work stores nothing; a runtime key given as the API key
// is named as such; the interactive form reads answers (here from a pipe);
// without a keychain the secret goes into the 0600 file.
func TestProfileAddChecksAndAsks(t *testing.T) {
	clearTenantEnv(t)
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": {}})
	mock.OAuth = true
	mock.Auth = &cpitest.Auth{APIClient: cpitest.Client{ID: "sb-dev", Secret: profileTestSecret}}
	withKeyring(t, nil)
	home := t.TempDir()
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(bad, []byte(fmt.Sprintf(`{"oauth":{"clientid":"sb-dev","clientsecret":"wrong","url":"%s","tokenurl":"%s/oauth/token"}}`, mock.URL(), mock.URL())), 0o600))
	code, out := runWithStdin(t, home, "", "profile", "add", "dev", "--service-key", bad)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, out, "nothing was stored")
	assert.NoFileExists(t, filepath.Join(home, ".cpictl", "dev.yaml"))

	rt := filepath.Join(dir, "rt.json")
	require.NoError(t, os.WriteFile(rt, []byte(`{"oauth":{"clientid":"x","clientsecret":"y","url":"https://sub.it-cpi018-rt.cfapps.eu10-003.hana.ondemand.com","tokenurl":"https://sub.authentication.eu10.hana.ondemand.com/oauth/token"}}`), 0o600))
	code, out = runWithStdin(t, home, "", "profile", "add", "dev", "--service-key", rt)
	assert.Equal(t, 2, code)
	assert.Contains(t, out, "runtime key")

	answers := fmt.Sprintf("%s\n%s/oauth/token\nsb-dev\n%s\n", mock.URL(), mock.URL(), profileTestSecret)
	code, out = runWithStdin(t, home, answers, "profile", "add", "qa")
	require.Equal(t, 0, code, out)
	assert.NotContains(t, out, profileTestSecret)
	data, err := os.ReadFile(filepath.Join(home, ".cpictl", "qa.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), profileTestSecret, "no keychain: the secret is in the file")
	assert.NotContains(t, string(data), "keychain-secrets")
	code, out = runWithStdin(t, home, "", "--profile", "qa", "packages")
	assert.Equal(t, 0, code, out)
}

func TestParseServiceKey(t *testing.T) {
	for _, j := range []string{
		`{"oauth":{"clientid":"a","clientsecret":"b","url":"https://t.it-cpi018.cfapps.eu10.hana.ondemand.com","tokenurl":"https://s.authentication.eu10.hana.ondemand.com/oauth/token"}}`,
		`{"credentials":{"oauth":{"clientid":"a","clientsecret":"b","url":"https://t.it-cpi018.cfapps.eu10.hana.ondemand.com","tokenurl":"https://s.authentication.eu10.hana.ondemand.com/oauth/token"}}}`,
		`{"clientid":"a","clientsecret":"b","url":"https://t.it-cpi018.cfapps.eu10.hana.ondemand.com","tokenurl":"https://s.authentication.eu10.hana.ondemand.com/oauth/token"}`,
	} {
		k, err := parseServiceKey([]byte(j))
		require.NoError(t, err, j)
		assert.Equal(t, "a", k.ClientID)
		assert.False(t, k.isRuntimeKey())
	}
	_, err := parseServiceKey([]byte(`{"oauth":{"clientid":"a","certificate":"-----BEGIN","key":"x","url":"u","tokenurl":"t"}}`))
	assert.ErrorContains(t, err, "certificate-based")
	s := &profileSettings{}
	k, _ := parseServiceKey([]byte(`{"oauth":{"clientid":"a","clientsecret":"b","url":"https://t.it-cpi018.cfapps.eu10.hana.ondemand.com","tokenurl":"https://s.authentication.eu10.hana.ondemand.com/oauth/token"}}`))
	require.NoError(t, s.fromAPIKey(k))
	assert.Equal(t, "t.it-cpi018.cfapps.eu10.hana.ondemand.com", s.values["tmn-host"])
	assert.Equal(t, "s.authentication.eu10.hana.ondemand.com", s.values["oauth-host"])
}

// A gateway profile: no SAP key, the sign-in settings as a login block.
func TestProfileAddGateway(t *testing.T) {
	clearTenantEnv(t)
	withKeyring(t, memKeyring{})
	home := t.TempDir()
	code, out := runWithStdin(t, home, "", "profile", "add", "gw", "--gateway", "https://gw.example.com/t/dev",
		"--login-tenant-id", "tid", "--login-client-id", "cid", "--login-scope", "api://gw/access")
	require.Equal(t, 0, code, out)
	assert.Contains(t, out, "auth login")
	v := viper.New()
	v.SetConfigFile(filepath.Join(home, ".cpictl", "gw.yaml"))
	require.NoError(t, v.ReadInConfig())
	assert.Equal(t, "https://gw.example.com/t/dev", v.GetString("gateway"))
	assert.Equal(t, "cid", v.GetString("login.clientId"))
	assert.Equal(t, "api://gw/access", v.GetString("login.scope"))
	assert.Empty(t, v.GetString("oauth-clientsecret"))

	code, out = runWithStdin(t, home, "", "profile", "add", "gw2", "--gateway", "http://gw.example.com/t/dev")
	assert.Equal(t, 2, code, out)
}
