package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/auth"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/zalando/go-keyring"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// profileKeyring is the keychain profile secrets are kept in; nil keeps them
// in the profile file (CPICTL_TOKEN_STORE=file, the same switch as for the
// gateway sign-in). Tests replace it.
var profileKeyring = func() auth.Keyring {
	if os.Getenv("CPICTL_TOKEN_STORE") == "file" {
		return nil
	}
	return auth.OSKeyring()
}

// keychainSecretsKey lists, in a profile file, the secrets kept in the OS
// keychain instead of the file.
const keychainSecretsKey = "keychain-secrets"

func profileKeychainUser(profile, key string) string { return "profile:" + profile + ":" + key }

// profileSecretsFromKeychain fills the secrets a profile keeps in the OS
// keychain into its settings, like values of the file (flags and
// environment variables still win). Offline commands need none.
func profileSecretsFromKeychain(v *viper.Viper, profile string, offline bool) error {
	keys := v.GetStringSlice(keychainSecretsKey)
	if len(keys) == 0 {
		return nil
	}
	if offline {
		// a command that talks to no tenant needs no secret: no keychain
		// access (which may ask the user); the placeholder only completes
		// the settings that belong together
		for _, k := range keys {
			if !v.IsSet(k) {
				v.Set(k, "(in the OS keychain)")
			}
		}
		return nil
	}
	kr := profileKeyring()
	if kr == nil {
		return fmt.Errorf("profile %s keeps its secrets in the OS keychain, which CPICTL_TOKEN_STORE=file switches off", profile)
	}
	for _, k := range keys {
		if !slices.Contains(secretConfigKeys, k) {
			return fmt.Errorf("profile %s: %s lists %q, which is not a secret setting (%s)", profile, keychainSecretsKey, k, strings.Join(secretConfigKeys, ", "))
		}
		if v.IsSet(k) {
			continue
		}
		val, err := kr.Get(auth.KeyringService, profileKeychainUser(profile, k))
		if err != nil {
			return fmt.Errorf("the %s of profile %s is not in the OS keychain (%v): run cpictl profile add %s --force", k, profile, err, profile)
		}
		v.Set(k, val)
	}
	return nil
}

// serviceKey is what cpictl needs from a BTP service key of Cloud Integration.
type serviceKey struct {
	URL, TokenURL, ClientID, ClientSecret string
}

// parseServiceKey reads a service key as the BTP cockpit downloads it
// ({"oauth": {...}}), as `cf service-key` prints it ({"credentials":
// {"oauth": ...}}), or flat.
func parseServiceKey(data []byte) (*serviceKey, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("not a service key (JSON): %v", err)
	}
	if c, ok := raw["credentials"].(map[string]any); ok {
		raw = c
	}
	if o, ok := raw["oauth"].(map[string]any); ok {
		raw = o
	}
	str := func(k string) string { s, _ := raw[k].(string); return strings.TrimSpace(s) }
	k := &serviceKey{URL: str("url"), TokenURL: str("tokenurl"), ClientID: str("clientid"), ClientSecret: str("clientsecret")}
	if k.ClientSecret == "" && (str("certificate") != "" || str("key") != "") {
		return nil, fmt.Errorf("this is a certificate-based service key; create one with a client secret (credential type binding-secret)")
	}
	if k.URL == "" || k.TokenURL == "" || k.ClientID == "" || k.ClientSecret == "" {
		return nil, fmt.Errorf("the service key lacks url, tokenurl, clientid or clientsecret")
	}
	return k, nil
}

// isRuntimeKey reports a key of plan integration-flow: its url is the
// runtime host (…-rt.…).
func (k *serviceKey) isRuntimeKey() bool {
	u, err := url.Parse(k.URL)
	if err != nil {
		return false
	}
	for _, label := range strings.Split(u.Hostname(), ".") {
		if strings.HasSuffix(label, "-rt") {
			return true
		}
	}
	return false
}

// hostOf is the setting for a host: the host alone for https, the whole URL
// for plain http (a local mock tenant).
func hostOf(raw string) (string, string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", "", fmt.Errorf("%q is not a URL", raw)
	}
	host := u.Host
	if u.Scheme == "http" {
		host = "http://" + u.Host
	}
	return host, u.Path, nil
}

// profileSettings are the settings profile add writes.
type profileSettings struct {
	values  map[string]string // non-secret settings, in write order
	order   []string
	secrets map[string]string // secret settings
}

func (p *profileSettings) set(k, v string) {
	if p.values == nil {
		p.values = map[string]string{}
	}
	if _, ok := p.values[k]; !ok {
		p.order = append(p.order, k)
	}
	p.values[k] = v
}

func (p *profileSettings) setSecret(k, v string) {
	if p.secrets == nil {
		p.secrets = map[string]string{}
	}
	p.secrets[k] = v
}

func (p *profileSettings) fromAPIKey(k *serviceKey) error {
	if k.isRuntimeKey() {
		return output.Usagef("this is a runtime key (plan integration-flow, url …-rt…): give it as --runtime-key, and the API key (plan api) as --service-key")
	}
	host, _, err := hostOf(k.URL)
	if err != nil {
		return output.Usage(err)
	}
	oauthHost, path, err := hostOf(k.TokenURL)
	if err != nil {
		return output.Usage(err)
	}
	p.set("tmn-host", host)
	p.set("oauth-host", oauthHost)
	if path != "" && path != "/oauth/token" {
		p.set("oauth-path", path)
	}
	p.set("oauth-clientid", k.ClientID)
	p.setSecret("oauth-clientsecret", k.ClientSecret)
	return nil
}

func (p *profileSettings) fromRuntimeKey(k *serviceKey) error {
	if !k.isRuntimeKey() && !strings.HasPrefix(k.URL, "http://") {
		return output.Usagef("--runtime-key is not a runtime key (plan integration-flow): its url has no -rt host")
	}
	oauthHost, _, err := hostOf(k.TokenURL)
	if err != nil {
		return output.Usage(err)
	}
	if oauthHost != p.values["oauth-host"] {
		p.set("runtime-oauth-host", oauthHost)
	}
	p.set("runtime-oauth-clientid", k.ClientID)
	p.setSecret("runtime-oauth-clientsecret", k.ClientSecret)
	return nil
}

// prompter asks for settings on a terminal (secrets without echo) or reads
// them line by line from a pipe.
type prompter struct {
	in  *bufio.Reader
	fd  int
	tty bool
	out io.Writer
}

func newPrompter(cmd *cobra.Command) *prompter {
	p := &prompter{in: bufio.NewReader(cmd.InOrStdin()), out: cmd.ErrOrStderr()}
	if f, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		p.fd, p.tty = int(f.Fd()), true
	}
	return p
}

func (p *prompter) ask(label string, secret bool) (string, error) {
	fmt.Fprintf(p.out, "%s: ", label)
	if secret && p.tty {
		b, err := term.ReadPassword(p.fd)
		fmt.Fprintln(p.out)
		return strings.TrimSpace(string(b)), err
	}
	line, err := p.in.ReadString('\n')
	if err != nil && (err != io.EOF || line == "") {
		return "", fmt.Errorf("no answer for %s", label)
	}
	return strings.TrimSpace(line), nil
}

// checkProfile proves the settings work before they are stored: a token
// from each key, and the API key reads the package list.
func checkProfile(ctx context.Context, s *profileSettings) error {
	if gw := s.values["gateway"]; gw != "" {
		return nil // checked on auth login
	}
	sd := &cpi.ServiceDetails{Host: s.values["tmn-host"], OauthHost: s.values["oauth-host"], OauthPath: cmpOrPath(s.values["oauth-path"]),
		OauthClientId: s.values["oauth-clientid"], OauthClientSecret: s.secrets["oauth-clientsecret"]}
	exe := cpi.InitHTTPExecuter(sd).WithContext(ctx)
	resp, err := exe.Exec(http.MethodGet, "/api/v1/IntegrationPackages?$top=1", http.NoBody, map[string]string{"Accept": "application/json"})
	if err != nil {
		return fmt.Errorf("the API key does not work: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_, err := exe.LogError(resp, "Check the API key")
		return fmt.Errorf("the API key does not work: %w", err)
	}
	_, _ = exe.ReadRespBody(resp)
	if id := s.values["runtime-oauth-clientid"]; id != "" {
		oauthHost := cmpOrStr(s.values["runtime-oauth-host"], s.values["oauth-host"])
		scheme, host, port := cpi.ParseHost(oauthHost)
		tokenURL := fmt.Sprintf("%s://%s:%d/oauth/token", scheme, host, port)
		// as form fields: XSUAA refuses URL-escaped Basic credentials (401),
		// and client IDs carry '!' and '|' (verified on a tenant)
		form := url.Values{"grant_type": {"client_credentials"}, "client_id": {id}, "client_secret": {s.secrets["runtime-oauth-clientsecret"]}}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := httpclnt.NewDefaultClient().Do(req)
		if err != nil {
			return fmt.Errorf("the runtime key does not work: %w", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("the runtime key does not work: the token server answered %d", resp.StatusCode)
		}
	}
	return nil
}

func cmpOrPath(p string) string {
	if p == "" {
		return "/oauth/token"
	}
	return p
}

func cmpOrStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// writeProfileFile writes the profile file (0600, directory 0700) and puts the
// secrets into the OS keychain when there is one; otherwise into the file.
func writeProfileFile(name string, s *profileSettings) (path, store string, err error) {
	path, err = profilePath(name)
	if err != nil {
		return "", "", err
	}
	dir, _ := profilesDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	doc := &yaml.Node{Kind: yaml.MappingNode}
	add := func(k, v string) {
		doc.Content = append(doc.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, &yaml.Node{Kind: yaml.ScalarNode, Value: v, Style: yaml.DoubleQuotedStyle})
	}
	var login *yaml.Node
	for _, k := range s.order {
		if sub, ok := strings.CutPrefix(k, "login."); ok {
			if login == nil {
				login = &yaml.Node{Kind: yaml.MappingNode}
				doc.Content = append(doc.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "login"}, login)
			}
			login.Content = append(login.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: sub},
				&yaml.Node{Kind: yaml.ScalarNode, Value: s.values[k], Style: yaml.DoubleQuotedStyle})
			continue
		}
		add(k, s.values[k])
	}
	keys := make([]string, 0, len(s.secrets))
	for _, k := range secretConfigKeys {
		if _, ok := s.secrets[k]; ok {
			keys = append(keys, k)
		}
	}
	store = "file"
	if kr := profileKeyring(); kr != nil && len(keys) > 0 {
		stored := true
		for _, k := range keys {
			if err := kr.Set(auth.KeyringService, profileKeychainUser(name, k), s.secrets[k]); err != nil {
				log.Debug().Err(err).Msg("OS keychain not available, the secrets go into the profile file")
				stored = false
				break
			}
		}
		if stored {
			store = "keychain"
			seq := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
			for _, k := range keys {
				seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k})
			}
			doc.Content = append(doc.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: keychainSecretsKey,
				HeadComment: "secrets in the OS keychain (service cpictl, account profile:" + name + ":<setting>)"}, seq)
		}
	}
	if store == "file" {
		for _, k := range keys {
			add(k, s.secrets[k])
		}
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return "", "", err
	}
	header := "# Profile " + name + ", written by cpictl profile add. Personal: never commit it.\n"
	tmp, err := os.CreateTemp(dir, "."+name+".*.tmp")
	if err != nil {
		return "", "", err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", "", err
	}
	if _, err := tmp.Write(append([]byte(header), data...)); err != nil {
		tmp.Close()
		return "", "", err
	}
	if err := tmp.Close(); err != nil {
		return "", "", err
	}
	return path, store, os.Rename(tmp.Name(), path)
}

// removeProfileSecrets removes a profile's secrets from the OS keychain.
func removeProfileSecrets(name string, keys []string) {
	kr := profileKeyring()
	if kr == nil {
		kr = auth.OSKeyring() // CPICTL_TOKEN_STORE=file: still clean up what an earlier add stored
	}
	for _, k := range keys {
		if err := kr.Delete(auth.KeyringService, profileKeychainUser(name, k)); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			log.Debug().Err(err).Msgf("removing %s of profile %s from the OS keychain", k, name)
		}
	}
}

// ProfileAddResult is the outcome of profile add (never a secret).
type ProfileAddResult struct {
	Name        string   `json:"name"`
	File        string   `json:"file"`
	SecretStore string   `json:"secretStore"` // keychain or file
	Settings    []string `json:"settings"`
	Checked     bool     `json:"checked"`
	Active      bool     `json:"active"`
}

func newProfileAddCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "add <name>",
		Short: "Create a profile from a BTP service key, or interactively; secrets go to the OS keychain",
		Long: `Creates $HOME/.cpictl/<name>.yaml for one tenant.

From service keys, as the BTP cockpit downloads them (Instances and
Subscriptions > the Process Integration Runtime instance > Service Keys):
--service-key is the key of plan "api", --runtime-key (optional) the key of
plan "integration-flow" for sending test messages. Without them cpictl asks
for the settings (a secret without echo on a terminal).

Before anything is stored, the profile is checked: a token from each key and
the package list. The client secrets go to the OS keychain (macOS Keychain,
Windows Credential Manager, Linux Secret Service); the profile file names
them under keychain-secrets. Without a keychain, or with
CPICTL_TOKEN_STORE=file, they go into the file (mode 0600). Delete the
downloaded service key files afterwards.

--gateway creates a profile for a cpictl gateway instead (no SAP key); sign
in with cpictl auth login.`,
		Example: `  cpictl profile add dev --service-key ~/Downloads/dev-api.json --use
  cpictl profile add dev --service-key dev-api.json --runtime-key dev-runtime.json --force
  cpictl profile add qa                       # asks for the settings
  cpictl profile add dev --gateway https://gw.example.com/t/dev --login-tenant-id <tid> --login-client-id <id> --login-scope api://<gateway>/access`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			path, err := profilePath(name)
			if err != nil {
				return output.Usage(err)
			}
			force, _ := cmd.Flags().GetBool("force")
			if fileExists(path) && !force {
				return output.Usagef("profile %s exists (%s): use --force to replace it", name, path)
			}
			apiKeyFile, _ := cmd.Flags().GetString("service-key")
			rtKeyFile, _ := cmd.Flags().GetString("runtime-key")
			gw, _ := cmd.Flags().GetString("gateway")
			s := &profileSettings{}
			switch {
			case gw != "":
				if apiKeyFile != "" || rtKeyFile != "" {
					return output.Usagef("--gateway excludes --service-key and --runtime-key: the gateway holds the SAP key")
				}
				if !strings.HasPrefix(gw, "https://") && !strings.HasPrefix(gw, "http://127.0.0.1") && !strings.HasPrefix(gw, "http://localhost") {
					return output.Usagef("--gateway must be an https URL ending in /t/<SAP tenant>")
				}
				s.set("gateway", gw)
				tid, _ := cmd.Flags().GetString("login-tenant-id")
				cid, _ := cmd.Flags().GetString("login-client-id")
				scope, _ := cmd.Flags().GetString("login-scope")
				if tid != "" || cid != "" || scope != "" {
					if tid == "" || cid == "" || scope == "" {
						return output.Usagef("--login-tenant-id, --login-client-id and --login-scope go together")
					}
					s.set("login.tenantId", tid)
					s.set("login.clientId", cid)
					s.set("login.scope", scope)
				}
			case apiKeyFile != "":
				data, err := os.ReadFile(apiKeyFile)
				if err != nil {
					return output.Usage(err)
				}
				k, err := parseServiceKey(data)
				if err != nil {
					return output.Usagef("--service-key %s: %v", apiKeyFile, err)
				}
				if err := s.fromAPIKey(k); err != nil {
					return err
				}
			default:
				if err := askProfile(cmd, s); err != nil {
					return err
				}
			}
			if rtKeyFile != "" {
				data, err := os.ReadFile(rtKeyFile)
				if err != nil {
					return output.Usage(err)
				}
				k, err := parseServiceKey(data)
				if err != nil {
					return output.Usagef("--runtime-key %s: %v", rtKeyFile, err)
				}
				if err := s.fromRuntimeKey(k); err != nil {
					return err
				}
			}
			res := &ProfileAddResult{Name: name}
			if noCheck, _ := cmd.Flags().GetBool("no-check"); !noCheck && gw == "" {
				if err := checkProfile(cmd.Context(), s); err != nil {
					return fmt.Errorf("%w (nothing was stored; --no-check stores it anyway)", err)
				}
				res.Checked = true
			}
			if fileExists(path) {
				// replaced: secrets of the old profile go
				if old, err := os.ReadFile(path); err == nil {
					var m map[string]any
					if yaml.Unmarshal(old, &m) == nil {
						if list, ok := m[keychainSecretsKey].([]any); ok {
							var keys []string
							for _, k := range list {
								if ks, ok := k.(string); ok {
									keys = append(keys, ks)
								}
							}
							removeProfileSecrets(name, keys)
						}
					}
				}
			}
			file, store, err := writeProfileFile(name, s)
			if err != nil {
				return err
			}
			res.File, res.SecretStore = file, store
			res.Settings = append(res.Settings, s.order...)
			for _, k := range secretConfigKeys {
				if _, ok := s.secrets[k]; ok {
					res.Settings = append(res.Settings, k)
				}
			}
			if use, _ := cmd.Flags().GetBool("use"); use {
				dir, _ := profilesDir()
				if err := os.WriteFile(dir+string(os.PathSeparator)+"current", []byte(name+"\n"), 0o600); err != nil {
					return err
				}
				res.Active = true
			}
			output.SetResult(cmd.Context(), res)
			where := map[string]string{"keychain": "the OS keychain", "file": "the profile file (mode 0600)"}[store]
			log.Info().Msgf("Profile %s written to %s; secrets in %s", name, file, where)
			if res.Checked {
				log.Info().Msg("Checked: the key gets a token and reads the package list")
			}
			if apiKeyFile != "" || rtKeyFile != "" {
				log.Info().Msg("Delete the downloaded service key files: the profile has what it needs")
			}
			if gw != "" {
				log.Info().Msgf("Sign in: cpictl --profile %s auth login", name)
			}
			if !res.Active {
				log.Info().Msgf("Use it: cpictl profile use %s (or --profile %s)", name, name)
			}
			return nil
		},
	}
	c.Flags().String("service-key", "", "Service key file (JSON) of plan \"api\"")
	c.Flags().String("runtime-key", "", "Service key file (JSON) of plan \"integration-flow\", for test messages")
	c.Flags().String("gateway", "", "A cpictl gateway URL (…/t/<SAP tenant>) instead of a SAP key")
	c.Flags().String("login-tenant-id", "", "Gateway sign-in: Entra tenant ID")
	c.Flags().String("login-client-id", "", "Gateway sign-in: client ID of cpictl's app registration")
	c.Flags().String("login-scope", "", "Gateway sign-in: the gateway's API scope")
	c.Flags().Bool("force", false, "Replace an existing profile")
	c.Flags().Bool("use", false, "Make it the active profile")
	c.Flags().Bool("no-check", false, "Store the profile without checking the keys")
	return c
}

// askProfile asks for the settings of an API key.
func askProfile(cmd *cobra.Command, s *profileSettings) error {
	p := newPrompter(cmd)
	if !p.tty {
		log.Debug().Msg("stdin is not a terminal: reading the answers line by line")
	}
	fmt.Fprintln(p.out, "The values are in the service key of plan \"api\" (BTP cockpit > Process Integration Runtime > Service Keys).")
	tmn, err := p.ask("Tenant URL (\"url\", https://….it-cpi….hana.ondemand.com)", false)
	if err != nil {
		return output.Usage(err)
	}
	tok, err := p.ask("Token URL (\"tokenurl\", https://….authentication….hana.ondemand.com/oauth/token)", false)
	if err != nil {
		return output.Usage(err)
	}
	id, err := p.ask("Client ID (\"clientid\")", false)
	if err != nil {
		return output.Usage(err)
	}
	secret, err := p.ask("Client secret (\"clientsecret\", not shown)", true)
	if err != nil {
		return output.Usage(err)
	}
	k := &serviceKey{URL: withScheme(tmn), TokenURL: withScheme(tok), ClientID: id, ClientSecret: secret}
	if k.URL == "" || k.TokenURL == "" || k.ClientID == "" || k.ClientSecret == "" {
		return output.Usagef("tenant URL, token URL, client ID and client secret are all needed")
	}
	return s.fromAPIKey(k)
}

func withScheme(v string) string {
	if v == "" || strings.Contains(v, "://") {
		return v
	}
	return "https://" + v
}

func newProfileShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "show [name]",
		Short:        "Show a profile's settings (secrets only as where they are kept)",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			} else if _, active, _ := resolveConfigSource("", ""); active != "" {
				name = active
			}
			if name == "" {
				return output.Usagef("no active profile: name one")
			}
			path, err := profilePath(name)
			if err != nil {
				return output.Usage(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return output.Usagef("profile %q not found (%s)", name, path)
			}
			var m map[string]any
			if err := yaml.Unmarshal(data, &m); err != nil {
				return output.Usagef("profile %s: %v", name, err)
			}
			inKeychain := map[string]bool{}
			if list, ok := m[keychainSecretsKey].([]any); ok {
				for _, k := range list {
					if ks, ok := k.(string); ok {
						inKeychain[ks] = true
					}
				}
			}
			delete(m, keychainSecretsKey)
			for _, k := range secretConfigKeys {
				switch {
				case m[k] != nil:
					m[k] = "(in the profile file)"
				case inKeychain[k]:
					m[k] = "(in the OS keychain)"
				}
			}
			output.SetResult(cmd.Context(), map[string]any{"name": name, "file": path, "settings": m})
			log.Info().Msgf("Profile %s (%s)", name, path)
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				log.Info().Msgf("  %-28s %v", k, m[k])
			}
			return nil
		},
	}
}

func newProfileRemoveCommand() *cobra.Command {
	c := &cobra.Command{
		Use:          "remove <name>",
		Short:        "Delete a profile with its keychain secrets and its gateway sign-in (requires --confirm)",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			path, err := profilePath(name)
			if err != nil {
				return output.Usage(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return output.Usagef("profile %q not found (%s)", name, path)
			}
			if confirm, _ := cmd.Flags().GetBool("confirm"); !confirm {
				return output.Usagef("removing deletes %s and its secrets; repeat with --confirm", path)
			}
			var m map[string]any
			_ = yaml.Unmarshal(data, &m)
			if list, ok := m[keychainSecretsKey].([]any); ok {
				var keys []string
				for _, k := range list {
					if ks, ok := k.(string); ok {
						keys = append(keys, ks)
					}
				}
				removeProfileSecrets(name, keys)
			}
			if store, err := auth.DefaultStore(); err == nil {
				_ = store.Delete(name) // the gateway sign-in of this profile, if any
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			wasActive := activeProfile() == name
			if wasActive {
				dir, _ := profilesDir()
				_ = os.Remove(dir + string(os.PathSeparator) + "current")
			}
			output.SetResult(cmd.Context(), map[string]any{"name": name, "removed": path, "wasActive": wasActive})
			log.Info().Msgf("Profile %s removed (file, keychain secrets, gateway sign-in)", name)
			return nil
		},
	}
	c.Flags().Bool("confirm", false, "Confirm deleting the profile")
	return c
}
