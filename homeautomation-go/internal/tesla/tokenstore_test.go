package tesla

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenStoreMissingFileIsNotAnError(t *testing.T) {
	store := NewTokenStore(filepath.Join(t.TempDir(), "tokens.json"))

	tokens, err := store.Load()

	require.NoError(t, err)
	assert.Nil(t, tokens, "an unauthorized deployment has no token file yet")
}

func TestTokenStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	store := NewTokenStore(path)
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	require.NoError(t, store.Save(&Tokens{
		AccessToken:  "access",
		RefreshToken: "refresh",
		Expiry:       expiry,
	}))

	loaded, err := store.Load()
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, "access", loaded.AccessToken)
	assert.Equal(t, "refresh", loaded.RefreshToken)
	assert.True(t, expiry.Equal(loaded.Expiry))
}

func TestTokenStoreWritesOwnerOnlyPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	store := NewTokenStore(path)

	require.NoError(t, store.Save(&Tokens{AccessToken: "a", RefreshToken: "r"}))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "refresh tokens must not be world-readable")
}

func TestTokenStoreOverwritesLeavingNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	store := NewTokenStore(filepath.Join(dir, "tokens.json"))

	require.NoError(t, store.Save(&Tokens{AccessToken: "first", RefreshToken: "r1"}))
	require.NoError(t, store.Save(&Tokens{AccessToken: "second", RefreshToken: "r2"}))

	loaded, err := store.Load()
	require.NoError(t, err)
	assert.Equal(t, "second", loaded.AccessToken)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "the atomic write must not leave temp files behind")
}

func TestTokenStoreRejectsNil(t *testing.T) {
	store := NewTokenStore(filepath.Join(t.TempDir(), "tokens.json"))

	assert.Error(t, store.Save(nil))
}

func TestTokenStoreReportsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	require.NoError(t, os.WriteFile(path, []byte("not json"), 0o600))

	_, err := NewTokenStore(path).Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse token store")
}

func TestTokensValid(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name   string
		tokens *Tokens
		want   bool
	}{
		{"nil", nil, false},
		{"no access token", &Tokens{Expiry: now.Add(time.Hour)}, false},
		{"expired", &Tokens{AccessToken: "a", Expiry: now.Add(-time.Minute)}, false},
		{"inside skew", &Tokens{AccessToken: "a", Expiry: now.Add(time.Minute)}, false},
		{"fresh", &Tokens{AccessToken: "a", Expiry: now.Add(time.Hour)}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.tokens.Valid(now, 5*time.Minute))
		})
	}
}
