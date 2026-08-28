package tesla

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Tokens is the OAuth state that must outlive the process. Tesla issues a new
// refresh token on every refresh and invalidates the old one, so losing this
// file means re-running the browser authorization.
type Tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
}

// Valid reports whether the access token is present and not within skew of
// expiring.
func (t *Tokens) Valid(now time.Time, skew time.Duration) bool {
	if t == nil || t.AccessToken == "" {
		return false
	}
	return now.Add(skew).Before(t.Expiry)
}

// TokenStore persists Tokens to a single file with owner-only permissions.
type TokenStore struct {
	path string
	mu   sync.Mutex
}

// NewTokenStore returns a store backed by path.
func NewTokenStore(path string) *TokenStore {
	return &TokenStore{path: path}
}

// Path returns the file the store writes to.
func (s *TokenStore) Path() string { return s.path }

// Load reads the stored tokens. A missing file is not an error: it returns
// (nil, nil), which means "not authorized yet".
func (s *TokenStore) Load() (*Tokens, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read token store %s: %w", s.path, err)
	}

	var tokens Tokens
	if err := json.Unmarshal(data, &tokens); err != nil {
		return nil, fmt.Errorf("parse token store %s: %w", s.path, err)
	}
	return &tokens, nil
}

// Save writes tokens atomically with mode 0600. The write-then-rename avoids
// leaving a truncated file behind if the process dies mid-write, which would
// otherwise cost a re-authorization.
func (s *TokenStore) Save(tokens *Tokens) error {
	if tokens == nil {
		return fmt.Errorf("refusing to save nil tokens")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(tokens, "", "  ")
	if err != nil {
		return fmt.Errorf("encode tokens: %w", err)
	}

	dir := filepath.Dir(s.path)
	temp, err := os.CreateTemp(dir, ".tesla-tokens-*")
	if err != nil {
		return fmt.Errorf("create temp token file in %s: %w", dir, err)
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()

	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("chmod temp token file: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write temp token file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temp token file: %w", err)
	}
	if err := os.Rename(tempName, s.path); err != nil {
		return fmt.Errorf("replace token store %s: %w", s.path, err)
	}
	return nil
}
