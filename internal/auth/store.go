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

	"golang.org/x/oauth2"
)

// ErrNotLoggedIn is returned when no token is stored for a key, i.e. the user
// has not run `cpictl auth login` for that profile.
var ErrNotLoggedIn = errors.New("not logged in: run cpictl auth login")

// Store keeps tokens on disk, one file per key (the profile name), readable
// only by the user. It is the headless/CI fallback; an OS-keychain backend can
// be added behind the same Load/Save/Delete/TokenSource shape.
type Store struct {
	Dir string
}

// DefaultStore keeps tokens under $HOME/.cpictl/tokens.
func DefaultStore() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &Store{Dir: filepath.Join(home, ".cpictl", "tokens")}, nil
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
	var tok oauth2.Token
	if err := json.Unmarshal(b, &tok); err != nil {
		return nil, fmt.Errorf("reading stored token %s: %w", p, err)
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
	b, err := json.Marshal(tok)
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

// Delete removes the stored token for key; a missing one is not an error.
func (s *Store) Delete(key string) error {
	p, err := s.path(key)
	if err != nil {
		return err
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
	base := c.oauth2Config("").TokenSource(ctx, tok)
	return &persistingSource{store: s, key: key, src: base, last: tok.AccessToken}, nil
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
}

func (p *persistingSource) Token() (*oauth2.Token, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	tok, err := p.src.Token()
	if err != nil {
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
