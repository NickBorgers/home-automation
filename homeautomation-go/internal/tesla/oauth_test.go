package tesla

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tokenServer stands in for Tesla's token endpoint. It records the last form it
// received so tests can assert on the grant that was sent.
type tokenServer struct {
	*httptest.Server

	mu       sync.Mutex
	lastForm url.Values
	requests int
	// statuses is served one entry per request, then status is used for the
	// rest. It lets a test make the first attempt fail and the second succeed.
	statuses []int
	status   int
	body     map[string]any

	// delay holds each response open, widening the window in which a second
	// caller can observe a refresh already in flight.
	delay time.Duration
}

// Requests returns how many times the token endpoint was called.
func (ts *tokenServer) Requests() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.requests
}

// Form returns the most recent posted form.
func (ts *tokenServer) Form() url.Values {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.lastForm
}

func newTokenServer(t *testing.T) *tokenServer {
	t.Helper()

	ts := &tokenServer{
		status: http.StatusOK,
		body: map[string]any{
			"access_token":  "access-1",
			"refresh_token": "refresh-1",
			"expires_in":    28800,
		},
	}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())

		ts.mu.Lock()
		ts.lastForm = r.PostForm
		ts.requests++
		status := ts.status
		if len(ts.statuses) > 0 {
			status = ts.statuses[0]
			ts.statuses = ts.statuses[1:]
		}
		body := ts.body
		delay := ts.delay
		ts.mu.Unlock()

		if delay > 0 {
			// A sleep, not a channel: the point is to keep this handler from
			// returning, so the HTTP response stays unwritten while other
			// callers pile up behind the refresh. Signalling on a channel would
			// need a response-writer shim to achieve the same thing, and the
			// wait is bounded and local to one test.
			time.Sleep(delay)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		require.NoError(t, json.NewEncoder(w).Encode(body))
	}))
	t.Cleanup(ts.Close)
	return ts
}

func newTestAuthenticator(t *testing.T, authBase string) (*Authenticator, *TokenStore) {
	t.Helper()

	store := NewTokenStore(filepath.Join(t.TempDir(), "tokens.json"))
	cfg := Config{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		Domain:       "tesla.example.ts.net",
		RedirectURI:  "https://home-automation.example.ts.net/api/tesla/callback",
		AuthBase:     authBase,
		Audience:     DefaultAudience,
	}
	return NewAuthenticator(cfg, store), store
}

func TestAuthorizeURLCarriesStateAndScopes(t *testing.T) {
	auth, _ := newTestAuthenticator(t, DefaultAuthBase)

	raw := auth.AuthorizeURL("state-123")

	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	assert.Equal(t, "auth.tesla.com", parsed.Host)
	assert.Equal(t, "/oauth2/v3/authorize", parsed.Path)

	query := parsed.Query()
	assert.Equal(t, "code", query.Get("response_type"))
	assert.Equal(t, "client-id", query.Get("client_id"))
	assert.Equal(t, "state-123", query.Get("state"))
	assert.Equal(t, Scopes, query.Get("scope"))
	assert.Contains(t, query.Get("scope"), "offline_access", "without offline_access there is no refresh token")
}

func TestExchangeStoresTokens(t *testing.T) {
	server := newTokenServer(t)
	auth, store := newTestAuthenticator(t, server.URL)

	require.NoError(t, auth.Exchange(context.Background(), "the-code"))

	assert.Equal(t, "authorization_code", server.Form().Get("grant_type"))
	assert.Equal(t, "the-code", server.Form().Get("code"))
	assert.Equal(t, DefaultAudience, server.Form().Get("audience"))

	stored, err := store.Load()
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "access-1", stored.AccessToken)
	assert.Equal(t, "refresh-1", stored.RefreshToken)
	assert.True(t, stored.Expiry.After(time.Now().Add(7*time.Hour)))
	assert.True(t, auth.Authorized())
}

func TestExchangeReportsServerError(t *testing.T) {
	server := newTokenServer(t)
	server.status = http.StatusBadRequest
	server.body = map[string]any{"error": "invalid_grant"}
	auth, _ := newTestAuthenticator(t, server.URL)

	err := auth.Exchange(context.Background(), "stale-code")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid_grant")
}

func TestAccessTokenRefreshesAndRotates(t *testing.T) {
	server := newTokenServer(t)
	auth, store := newTestAuthenticator(t, server.URL)

	// An expired access token with a usable refresh token.
	require.NoError(t, store.Save(&Tokens{
		AccessToken:  "stale",
		RefreshToken: "refresh-0",
		Expiry:       time.Now().Add(-time.Minute),
	}))

	token, err := auth.AccessToken(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "access-1", token)
	assert.Equal(t, "refresh_token", server.Form().Get("grant_type"))
	assert.Equal(t, "refresh-0", server.Form().Get("refresh_token"))

	stored, err := store.Load()
	require.NoError(t, err)
	assert.Equal(t, "refresh-1", stored.RefreshToken, "Tesla rotates the refresh token and the new one must be persisted")
}

func TestAccessTokenKeepsRefreshTokenWhenResponseOmitsIt(t *testing.T) {
	server := newTokenServer(t)
	server.body = map[string]any{"access_token": "access-2", "expires_in": 3600}
	auth, store := newTestAuthenticator(t, server.URL)

	require.NoError(t, store.Save(&Tokens{
		AccessToken:  "stale",
		RefreshToken: "keep-me",
		Expiry:       time.Now().Add(-time.Minute),
	}))

	_, err := auth.AccessToken(context.Background())
	require.NoError(t, err)

	stored, err := store.Load()
	require.NoError(t, err)
	assert.Equal(t, "keep-me", stored.RefreshToken, "an empty refresh token must never overwrite a working one")
}

func TestAccessTokenReusesValidToken(t *testing.T) {
	server := newTokenServer(t)
	auth, store := newTestAuthenticator(t, server.URL)

	require.NoError(t, store.Save(&Tokens{
		AccessToken:  "still-good",
		RefreshToken: "refresh-0",
		Expiry:       time.Now().Add(time.Hour),
	}))

	token, err := auth.AccessToken(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "still-good", token)
	assert.Nil(t, server.Form(), "a valid token must not cost a refresh call")
}

func TestAccessTokenFailsBeforeAuthorization(t *testing.T) {
	auth, _ := newTestAuthenticator(t, DefaultAuthBase)

	_, err := auth.AccessToken(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not authorized")
	assert.False(t, auth.Authorized())
}
