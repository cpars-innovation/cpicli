package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
)

// ErrNotLoggedIn is returned when no token is stored for a key, i.e. the user
// has not run `cpictl auth login` for that profile.
var ErrNotLoggedIn = errors.New("not logged in: run cpictl auth login")

// Store keeps tokens per key (the profile name). The refresh token, the
// long-lived credential, goes to the OS keychain (macOS Keychain, Windows
// Credential Manager, Linux Secret Service) when there is one; the rest of
// the token (the access token, valid for about an hour, and its expiry) goes
// to a file readable only by the user. Without a keychain (CI, headless
// Linux, SSH) the whole token is in the file.
//
// Only the refresh token goes to the keychain: Windows takes at most 2.5 KB
// per entry, and an Entra access token with its refresh token can be larger.
type Store struct {
	Dir string
	// Keyring is the OS keychain; nil keeps everything in the file.
	Keyring Keyring
}

// Keyring is an OS keychain (go-keyring's shape).
type Keyring interface {
	Get(service, user string) (string, error)
	Set(service, user, secret string) error
	Delete(service, user string) error
}

// KeyringService names cpictl's entries in the OS keychain.
const KeyringService = "cpictl"

func keyringUser(key string) string { return "gateway-refresh-token:" + key }

// osKeyring is the OS keychain through go-keyring.
type osKeyring struct{}

func (osKeyring) Get(service, user string) (string, error) { return keyring.Get(service, user) }
func (osKeyring) Set(service, user, secret string) error   { return keyring.Set(service, user, secret) }
func (osKeyring) Delete(service, user string) error        { return keyring.Delete(service, user) }

// DefaultStore keeps tokens in the OS keychain with the file under
// $HOME/.cpictl/tokens. CPICTL_TOKEN_STORE=file keeps them in the file only.
func DefaultStore() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	s := &Store{Dir: filepath.Join(home, ".cpictl", "tokens")}
	if os.Getenv("CPICTL_TOKEN_STORE") != "file" {
		s.Keyring = osKeyring{}
	}
	return s, nil
}

// fileToken is the file's content: the token, without the refresh token
// when that is in the keychain.
type fileToken struct {
	oauth2.Token
	RefreshInKeychain bool `json:"refresh_in_keychain,omitempty"`
}

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func (s *Store) path(key string) (string, error) {
	if !keyPattern.MatchString(key) {
		return "", fmt.Errorf("invalid token key %q", key)
	}
	return filepath.Join(s.Dir, key+".json"), nil
}

// Load returns the stored token for key, or (nil, ErrNotLoggedIn) if none.
func (s *Store) Load(key string) (*oauth2.Token, error) {
	p, err := s.path(key)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotLoggedIn
	}
	if err != nil {
		return nil, err
	}
	var ft fileToken
	if err := json.Unmarshal(b, &ft); err != nil {
		return nil, fmt.Errorf("reading stored token %s: %w", p, err)
	}
	tok := ft.Token
	switch {
	case ft.RefreshInKeychain:
		if s.Keyring == nil {
			return nil, fmt.Errorf("the refresh token for profile %s is in the OS keychain, which is switched off (CPICTL_TOKEN_STORE=file): run cpictl auth login", key)
		}
		rt, err := s.Keyring.Get(KeyringService, keyringUser(key))
		if err != nil {
			// gone from the keychain, or no keychain on this host: the
			// access token still works until it expires
			log.Debug().Err(err).Msgf("reading the refresh token of profile %s from the OS keychain", key)
		}
		tok.RefreshToken = rt
	case tok.RefreshToken != "" && s.Keyring != nil:
		// a token stored before the keychain was used: move its refresh
		// token there (kept in the file when the keychain refuses)
		if err := s.Save(key, &tok); err != nil {
			log.Debug().Err(err).Msgf("moving the refresh token of profile %s to the OS keychain", key)
		}
	}
	return &tok, nil
}

// Save writes the token for key with 0600 permissions (directory 0700),
// replacing it atomically.
func (s *Store) Save(key string, tok *oauth2.Token) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	ft := fileToken{Token: *tok}
	if s.Keyring != nil && tok.RefreshToken != "" {
		if err := s.Keyring.Set(KeyringService, keyringUser(key), tok.RefreshToken); err != nil {
			// no keychain here (CI, headless Linux): the file holds it all
			log.Debug().Err(err).Msg("OS keychain not available, the refresh token stays in the token file")
		} else {
			ft.RefreshToken, ft.RefreshInKeychain = "", true
		}
	}
	b, err := json.Marshal(ft)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, "."+key+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// Delete removes the stored token for key, from the file and the keychain;
// a missing one is not an error.
func (s *Store) Delete(key string) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if s.Keyring != nil {
		if err := s.Keyring.Delete(KeyringService, keyringUser(key)); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			log.Debug().Err(err).Msgf("removing the refresh token of profile %s from the OS keychain", key)
		}
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// TokenSource returns a source seeded with the stored token for key that
// refreshes it with the refresh token when expired and writes any refreshed
// token back to the store. It errors with ErrNotLoggedIn if nothing is stored.
func (s *Store) TokenSource(ctx context.Context, c Config, key string) (oauth2.TokenSource, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	tok, err := s.Load(key)
	if err != nil {
		return nil, err
	}
	base := c.oauth2Config("").TokenSource(withClient(ctx), tok)
	return &persistingSource{store: s, key: key, src: base, last: tok.AccessToken, noRefresh: tok.RefreshToken == ""}, nil
}

// persistingSource writes a refreshed token back so the next process starts
// from it instead of a fresh sign-in. Token is called per request, also from
// parallel requests (cpictl mcp, parallel uploads).
type persistingSource struct {
	mu    sync.Mutex
	store *Store
	key   string
	src   oauth2.TokenSource
	last  string
	// noRefresh: the stored token has no refresh token (it was removed from
	// the keychain, or the keychain is not reachable on this host)
	noRefresh bool
}

func (p *persistingSource) Token() (*oauth2.Token, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	tok, err := p.src.Token()
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) || p.noRefresh {
			// the refresh token expired or was revoked: say what to do, not
			// Entra's invalid_grant (still an auth error, exit code 3)
			return nil, fmt.Errorf("sign-in to the cpictl gateway expired for profile %s: run cpictl auth login (%w)", p.key, err)
		}
		return nil, err
	}
	if tok.AccessToken != p.last {
		if err := p.store.Save(p.key, tok); err != nil {
			return nil, fmt.Errorf("saving the refreshed token: %w", err)
		}
		p.last = tok.AccessToken
	}
	return tok, nil
}
