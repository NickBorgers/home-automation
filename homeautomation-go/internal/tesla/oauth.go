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
	tokens, err := a.current()
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

	tokens, err := a.postToken(ctx, form)
	if err != nil {
		return fmt.Errorf("exchange authorization code: %w", err)
	}
	return a.persist(tokens)
}

// AccessToken returns a usable access token, refreshing it when needed.
func (a *Authenticator) AccessToken(ctx context.Context) (string, error) {
	tokens, err := a.current()
	if err != nil {
		return "", err
	}
	if tokens == nil || tokens.RefreshToken == "" {
		return "", fmt.Errorf("tesla account is not authorized yet: open %s", a.cfg.RedirectURI)
	}
	if tokens.Valid(a.now(), refreshSkew) {
		return tokens.AccessToken, nil
	}
	refreshed, err := a.refresh(ctx, tokens.RefreshToken)
	if err != nil {
		return "", err
	}
	return refreshed.AccessToken, nil
}

// refresh swaps the refresh token for a new pair. Tesla rotates the refresh
// token, so the new one is persisted before it is used.
func (a *Authenticator) refresh(ctx context.Context, refreshToken string) (*Tokens, error) {
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
	if err := a.persist(tokens); err != nil {
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

func (a *Authenticator) postToken(ctx context.Context, form url.Values) (*Tokens, error) {
	endpoint := a.cfg.AuthBase + "/oauth2/v3/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call token endpoint: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed tokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}
	if parsed.AccessToken == "" {
		return nil, fmt.Errorf("token response contained no access token")
	}

	expiresIn := parsed.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 8 * 60 * 60
	}
	return &Tokens{
		AccessToken:  parsed.AccessToken,
		RefreshToken: parsed.RefreshToken,
		Expiry:       a.now().Add(time.Duration(expiresIn) * time.Second),
	}, nil
}

// current returns the in-memory tokens, loading them from disk on first use.
func (a *Authenticator) current() (*Tokens, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

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

func (a *Authenticator) persist(tokens *Tokens) error {
	if err := a.store.Save(tokens); err != nil {
		return err
	}
	a.mu.Lock()
	a.tokens = tokens
	a.mu.Unlock()
	return nil
}
