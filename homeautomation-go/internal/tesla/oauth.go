package tesla

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// refreshSkew is how long before expiry the access token is refreshed. Tesla
// access tokens last 8 hours, so a few minutes of margin is plenty.
const refreshSkew = 5 * time.Minute

// Authenticator owns the OAuth tokens: the browser authorization exchange, the
// refresh cycle, and persistence. It is safe for concurrent use.
type Authenticator struct {
	cfg    Config
	store  *TokenStore
	client *http.Client
	now    func() time.Time

	mu     sync.Mutex
	tokens *Tokens
}

// NewAuthenticator returns an Authenticator that persists tokens through store.
func NewAuthenticator(cfg Config, store *TokenStore) *Authenticator {
	return &Authenticator{
		cfg:    cfg,
		store:  store,
		client: &http.Client{Timeout: 30 * time.Second},
		now:    time.Now,
	}
}

// AuthorizeURL builds the URL the owner opens in a browser to grant access.
func (a *Authenticator) AuthorizeURL(state string) string {
	query := url.Values{}
	query.Set("response_type", "code")
	query.Set("client_id", a.cfg.ClientID)
	query.Set("redirect_uri", a.cfg.RedirectURI)
	query.Set("scope", Scopes)
	query.Set("state", state)
	return a.cfg.AuthBase + "/oauth2/v3/authorize?" + query.Encode()
}

// Authorized reports whether a refresh token is on hand.
func (a *Authenticator) Authorized() bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	tokens, err := a.currentLocked()
	if err != nil {
		return false
	}
	return tokens != nil && tokens.RefreshToken != ""
}

// Exchange trades an authorization code for tokens and stores them.
func (a *Authenticator) Exchange(ctx context.Context, code string) error {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", a.cfg.ClientID)
	form.Set("client_secret", a.cfg.ClientSecret)
	form.Set("code", code)
	form.Set("audience", a.cfg.Audience)
	form.Set("redirect_uri", a.cfg.RedirectURI)

	a.mu.Lock()
	defer a.mu.Unlock()

	tokens, err := a.postToken(ctx, form)
	if err != nil {
		return fmt.Errorf("exchange authorization code: %w", err)
	}
	return a.persistLocked(tokens)
}

// AccessToken returns a usable access token, refreshing it when needed.
//
// The expiry check and the refresh happen under one lock. Tesla invalidates a
// refresh token the moment it issues a replacement, so two callers refreshing
// at the same time would leave one of them holding a dead token and the
// integration needing a fresh browser authorization. A caller that arrives
// mid-refresh waits, then reuses the token the first caller fetched.
func (a *Authenticator) AccessToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	tokens, err := a.currentLocked()
	if err != nil {
		return "", err
	}
	if tokens == nil || tokens.RefreshToken == "" {
		return "", fmt.Errorf("tesla account is not authorized yet: start at /api/tesla/login")
	}
	if tokens.Valid(a.now(), refreshSkew) {
		return tokens.AccessToken, nil
	}
	refreshed, err := a.refreshLocked(ctx, tokens.RefreshToken)
	if err != nil {
		return "", err
	}
	return refreshed.AccessToken, nil
}

// refreshLocked swaps the refresh token for a new pair. Tesla rotates the
// refresh token, so the new one is persisted before it is used.
//
// The caller must hold a.mu.
func (a *Authenticator) refreshLocked(ctx context.Context, refreshToken string) (*Tokens, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", a.cfg.ClientID)
	form.Set("refresh_token", refreshToken)

	tokens, err := a.postToken(ctx, form)
	if err != nil {
		return nil, fmt.Errorf("refresh tesla token: %w", err)
	}
	if tokens.RefreshToken == "" {
		// Tesla normally returns a new refresh token. Keep the old one rather
		// than writing an empty value that would strand the integration.
		tokens.RefreshToken = refreshToken
	}
	if err := a.persistLocked(tokens); err != nil {
		return nil, err
	}
	return tokens, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// postToken calls the token endpoint, retrying once on a failure that looks
// transient. Tesla does not bill the auth endpoint, so retrying a 5xx here
// costs nothing — unlike the Fleet API, where a retry is a second charge.
func (a *Authenticator) postToken(ctx context.Context, form url.Values) (*Tokens, error) {
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(retryDelay):
			}
		}

		tokens, retry, err := a.postTokenOnce(ctx, form)
		if err == nil {
			return tokens, nil
		}
		lastErr = err
		if !retry || ctx.Err() != nil {
			break
		}
	}
	return nil, lastErr
}

// postTokenOnce makes a single token request. It reports retry=true when the
// call never got an answer, or when Tesla answered with a server-side error.
func (a *Authenticator) postTokenOnce(ctx context.Context, form url.Values) (tokens *Tokens, retry bool, err error) {
	endpoint := a.cfg.AuthBase + "/oauth2/v3/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, false, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, true, fmt.Errorf("call token endpoint: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, true, fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode >= 500,
			fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed tokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, false, fmt.Errorf("parse token response: %w", err)
	}
	if parsed.AccessToken == "" {
		return nil, false, fmt.Errorf("token response contained no access token")
	}

	expiresIn := parsed.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 8 * 60 * 60
	}
	return &Tokens{
		AccessToken:  parsed.AccessToken,
		RefreshToken: parsed.RefreshToken,
		Expiry:       a.now().Add(time.Duration(expiresIn) * time.Second),
	}, false, nil
}

// currentLocked returns the in-memory tokens, loading them from disk on first
// use. The caller must hold a.mu.
func (a *Authenticator) currentLocked() (*Tokens, error) {
	if a.tokens != nil {
		return a.tokens, nil
	}
	loaded, err := a.store.Load()
	if err != nil {
		return nil, err
	}
	a.tokens = loaded
	return a.tokens, nil
}

// persistLocked writes tokens to disk and adopts them in memory. The caller
// must hold a.mu.
func (a *Authenticator) persistLocked(tokens *Tokens) error {
	if err := a.store.Save(tokens); err != nil {
		return err
	}
	a.tokens = tokens
	return nil
}
