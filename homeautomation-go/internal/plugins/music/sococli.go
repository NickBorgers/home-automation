package music

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"
)

const (
	// socoAttemptTimeout is the per-attempt timeout for most SoCo-CLI operations.
	// Kept short so that a hung request during volume fades (~1s steps) doesn't
	// block the entire fade sequence.
	socoAttemptTimeout = 5 * time.Second

	// socoLongTimeout is the per-attempt timeout for operations that are
	// inherently slower, such as sharelink (downloads and enqueues content).
	socoLongTimeout = 30 * time.Second

	// socoMaxRetries is the number of retry attempts after the initial call.
	// Total attempts = socoMaxRetries + 1.
	socoMaxRetries = 2

	// socoRetryDelay is the fixed delay between retry attempts.
	socoRetryDelay = 500 * time.Millisecond
)

// SoCoResponse represents the JSON response from the SoCo-CLI HTTP API.
type SoCoResponse struct {
	Result   string `json:"result"`
	ErrorMsg string `json:"error_msg"`
	ExitCode int    `json:"exit_code"`
	Speaker  string `json:"speaker"`
	Action   string `json:"action"`
}

// SoCoClient is an HTTP client for the SoCo-CLI HTTP API.
// It provides direct Sonos speaker control via UPnP, bypassing Home Assistant
// for time-sensitive operations like volume fades, group join/unjoin, and playback.
// State reads (current volume, playback status) still go through HA.
type SoCoClient struct {
	baseURL    string
	httpClient *http.Client
	logger     *zap.Logger
	readOnly   bool
}

// NewSoCoClient creates a new SoCo-CLI HTTP client.
// Returns nil if baseURL is empty.
func NewSoCoClient(baseURL string, logger *zap.Logger, readOnly bool) *SoCoClient {
	if baseURL == "" {
		return nil
	}
	return &SoCoClient{
		baseURL:    baseURL,
		httpClient: &http.Client{}, // Per-operation timeouts via context in doGetWithRetry
		logger:     logger.Named("sococli"),
		readOnly:   readOnly,
	}
}

// =============================================================================
// Tidal Playback (existing)
// =============================================================================

// ShareLink sends a Tidal share link to the specified speaker's queue.
// Uses a longer per-attempt timeout since sharelink downloads and enqueues content.
func (c *SoCoClient) ShareLink(speakerName, tidalURL string) error {
	if c.readOnly {
		c.logger.Info("READ_ONLY: Would send sharelink to SoCo-CLI",
			zap.String("speaker", speakerName),
			zap.String("url", tidalURL))
		return nil
	}

	// GET /{speaker}/sharelink/{url}
	endpoint := fmt.Sprintf("%s/%s/sharelink/%s",
		c.baseURL,
		url.PathEscape(speakerName),
		url.PathEscape(tidalURL))

	return c.doGetWithRetry(endpoint, "sharelink", speakerName, socoLongTimeout)
}

// PlayFromQueue starts playback from the speaker's queue.
func (c *SoCoClient) PlayFromQueue(speakerName string) error {
	if c.readOnly {
		c.logger.Info("READ_ONLY: Would send play_from_queue to SoCo-CLI",
			zap.String("speaker", speakerName))
		return nil
	}

	// GET /{speaker}/play_from_queue
	endpoint := fmt.Sprintf("%s/%s/play_from_queue",
		c.baseURL,
		url.PathEscape(speakerName))

	return c.doGet(endpoint, "play_from_queue", speakerName)
}

// ClearQueue removes all items from the specified speaker's queue.
// This should be called before ShareLink to prevent stale content from playing.
func (c *SoCoClient) ClearQueue(speakerName string) error {
	if c.readOnly {
		c.logger.Info("READ_ONLY: Would send clear_queue to SoCo-CLI",
			zap.String("speaker", speakerName))
		return nil
	}

	// GET /{speaker}/clear_queue
	endpoint := fmt.Sprintf("%s/%s/clear_queue",
		c.baseURL,
		url.PathEscape(speakerName))

	return c.doGet(endpoint, "clear_queue", speakerName)
}

// PlayShareLink clears the queue, adds a Tidal share link, and starts playback.
// This is the main entry point for Tidal playlist playback.
func (c *SoCoClient) PlayShareLink(speakerName, tidalURL string) error {
	if err := c.ClearQueue(speakerName); err != nil {
		return fmt.Errorf("clear_queue failed: %w", err)
	}
	if err := c.ShareLink(speakerName, tidalURL); err != nil {
		return fmt.Errorf("sharelink failed: %w", err)
	}
	if err := c.PlayFromQueue(speakerName); err != nil {
		return fmt.Errorf("play_from_queue failed: %w", err)
	}
	return nil
}

// =============================================================================
// Direct Speaker Commands (bypasses HA for time-sensitive operations)
// =============================================================================

// SetVolume sets the speaker volume directly via UPnP.
// level is 0-100 (percentage).
func (c *SoCoClient) SetVolume(speakerName string, level int) error {
	if c.readOnly {
		c.logger.Debug("READ_ONLY: Would set volume via SoCo-CLI",
			zap.String("speaker", speakerName),
			zap.Int("level", level))
		return nil
	}

	// GET /{speaker}/volume/{level}
	endpoint := fmt.Sprintf("%s/%s/volume/%d",
		c.baseURL,
		url.PathEscape(speakerName),
		level)

	return c.doGet(endpoint, "volume", speakerName)
}

// Mute mutes the speaker.
func (c *SoCoClient) Mute(speakerName string) error {
	if c.readOnly {
		c.logger.Debug("READ_ONLY: Would mute via SoCo-CLI",
			zap.String("speaker", speakerName))
		return nil
	}

	// GET /{speaker}/mute/on (explicit mute-on, not toggle)
	endpoint := fmt.Sprintf("%s/%s/mute/on",
		c.baseURL,
		url.PathEscape(speakerName))

	return c.doGet(endpoint, "mute", speakerName)
}

// Unmute unmutes the speaker.
func (c *SoCoClient) Unmute(speakerName string) error {
	if c.readOnly {
		c.logger.Debug("READ_ONLY: Would unmute via SoCo-CLI",
			zap.String("speaker", speakerName))
		return nil
	}

	// GET /{speaker}/mute/off — SoCo-CLI uses "mute off", not "unmute"
	endpoint := fmt.Sprintf("%s/%s/mute/off",
		c.baseURL,
		url.PathEscape(speakerName))

	return c.doGet(endpoint, "unmute", speakerName)
}

// GroupSpeaker adds a follower speaker to the lead speaker's group.
// In SoCo/UPnP semantics, the follower joins the lead's group.
func (c *SoCoClient) GroupSpeaker(followerName, leadName string) error {
	if c.readOnly {
		c.logger.Debug("READ_ONLY: Would group speaker via SoCo-CLI",
			zap.String("follower", followerName),
			zap.String("lead", leadName))
		return nil
	}

	// GET /{follower}/group/{lead}
	endpoint := fmt.Sprintf("%s/%s/group/%s",
		c.baseURL,
		url.PathEscape(followerName),
		url.PathEscape(leadName))

	return c.doGet(endpoint, "group", followerName)
}

// UngroupSpeaker removes a speaker from its current group.
func (c *SoCoClient) UngroupSpeaker(speakerName string) error {
	if c.readOnly {
		c.logger.Debug("READ_ONLY: Would ungroup speaker via SoCo-CLI",
			zap.String("speaker", speakerName))
		return nil
	}

	// GET /{speaker}/ungroup
	endpoint := fmt.Sprintf("%s/%s/ungroup",
		c.baseURL,
		url.PathEscape(speakerName))

	return c.doGet(endpoint, "ungroup", speakerName)
}

// UngroupSpeakerCtx removes a speaker from its current group with context support.
// Use this for best-effort operations where a timeout is preferred over extended retries.
func (c *SoCoClient) UngroupSpeakerCtx(ctx context.Context, speakerName string) error {
	if c.readOnly {
		c.logger.Debug("READ_ONLY: Would ungroup speaker via SoCo-CLI",
			zap.String("speaker", speakerName))
		return nil
	}

	// GET /{speaker}/ungroup
	endpoint := fmt.Sprintf("%s/%s/ungroup",
		c.baseURL,
		url.PathEscape(speakerName))

	return c.doGetCtx(ctx, endpoint, "ungroup", speakerName)
}

// Play resumes playback on a speaker.
func (c *SoCoClient) Play(speakerName string) error {
	if c.readOnly {
		c.logger.Debug("READ_ONLY: Would play via SoCo-CLI",
			zap.String("speaker", speakerName))
		return nil
	}

	// GET /{speaker}/play
	endpoint := fmt.Sprintf("%s/%s/play",
		c.baseURL,
		url.PathEscape(speakerName))

	return c.doGet(endpoint, "play", speakerName)
}

// AddURIToQueue adds a media URI to the speaker's queue.
// Uses a longer per-attempt timeout since Sonos may need to fetch and parse M3U playlists.
func (c *SoCoClient) AddURIToQueue(speakerName, uri string) error {
	if c.readOnly {
		c.logger.Info("READ_ONLY: Would add_uri_to_queue via SoCo-CLI",
			zap.String("speaker", speakerName),
			zap.String("uri", uri))
		return nil
	}

	// GET /{speaker}/add_uri_to_queue/{uri}
	endpoint := fmt.Sprintf("%s/%s/add_uri_to_queue/%s",
		c.baseURL,
		url.PathEscape(speakerName),
		url.PathEscape(uri))

	return c.doGetWithRetry(endpoint, "add_uri_to_queue", speakerName, socoLongTimeout)
}

// PlayURIFromQueue clears the queue, adds a URI to the queue, and starts playback.
// This ensures the content is in the Sonos queue so that repeat mode works correctly.
func (c *SoCoClient) PlayURIFromQueue(speakerName, uri string) error {
	if err := c.ClearQueue(speakerName); err != nil {
		return fmt.Errorf("clear_queue failed: %w", err)
	}
	if err := c.AddURIToQueue(speakerName, uri); err != nil {
		return fmt.Errorf("add_uri_to_queue failed: %w", err)
	}
	if err := c.PlayFromQueue(speakerName); err != nil {
		return fmt.Errorf("play_from_queue failed: %w", err)
	}
	return nil
}

// SetShuffle enables or disables shuffle mode on a speaker.
func (c *SoCoClient) SetShuffle(speakerName string, enabled bool) error {
	if c.readOnly {
		c.logger.Debug("READ_ONLY: Would set shuffle via SoCo-CLI",
			zap.String("speaker", speakerName),
			zap.Bool("enabled", enabled))
		return nil
	}

	state := "off"
	if enabled {
		state = "on"
	}

	// GET /{speaker}/shuffle/{on|off}
	endpoint := fmt.Sprintf("%s/%s/shuffle/%s",
		c.baseURL,
		url.PathEscape(speakerName),
		state)

	return c.doGet(endpoint, "shuffle", speakerName)
}

// SetRepeat sets the repeat mode on a speaker.
// mode should be "all", "one", or "off".
func (c *SoCoClient) SetRepeat(speakerName string, mode string) error {
	if c.readOnly {
		c.logger.Debug("READ_ONLY: Would set repeat via SoCo-CLI",
			zap.String("speaker", speakerName),
			zap.String("mode", mode))
		return nil
	}

	// GET /{speaker}/repeat/{all|one|off}
	endpoint := fmt.Sprintf("%s/%s/repeat/%s",
		c.baseURL,
		url.PathEscape(speakerName),
		url.PathEscape(mode))

	return c.doGet(endpoint, "repeat", speakerName)
}

// =============================================================================
// HTTP helpers
// =============================================================================

// doGet performs a GET request with retry logic and per-attempt timeouts.
func (c *SoCoClient) doGet(endpoint, action, speaker string) error {
	return c.doGetWithRetry(endpoint, action, speaker, socoAttemptTimeout)
}

// doGetWithRetry performs a GET request with retries on transient errors.
// Each attempt uses the specified timeout via context. Non-retryable errors
// (SoCo application errors, HTTP 4xx) are returned immediately without retry.
func (c *SoCoClient) doGetWithRetry(endpoint, action, speaker string, attemptTimeout time.Duration) error {
	var lastErr error
	for attempt := 0; attempt <= socoMaxRetries; attempt++ {
		if attempt > 0 {
			c.logger.Warn("SoCo-CLI retrying",
				zap.String("action", action),
				zap.String("speaker", speaker),
				zap.Int("attempt", attempt+1),
				zap.Error(lastErr))
			time.Sleep(socoRetryDelay)
		}

		ctx, cancel := context.WithTimeout(context.Background(), attemptTimeout)
		lastErr = c.doGetCtx(ctx, endpoint, action, speaker)
		cancel()

		if lastErr == nil {
			return nil
		}

		if !isRetryableError(lastErr) {
			return lastErr
		}
	}
	return fmt.Errorf("sococli %s: all %d attempts failed: %w", action, socoMaxRetries+1, lastErr)
}

// isRetryableError returns true for transient errors worth retrying:
// network errors (connection refused, timeouts) and HTTP 5xx/429 server errors.
// SoCo application errors (non-zero exit_code) and HTTP 4xx client errors are
// not retried since they indicate issues that won't resolve with a retry.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	// Network-level errors: "sococli <action> request failed: ..."
	if strings.Contains(s, "request failed") {
		return true
	}
	// Server errors: "sococli <action> returned HTTP 5xx"
	if strings.Contains(s, "returned HTTP 5") {
		return true
	}
	// Rate limiting: "sococli <action> returned HTTP 429"
	if strings.Contains(s, "returned HTTP 429") {
		return true
	}
	// Read body failures (connection reset during read)
	if strings.Contains(s, "failed to read response body") {
		return true
	}
	return false
}

// doGetCtx performs a GET request with context support.
// Use this for operations that need custom timeouts (e.g., best-effort unjoin).
func (c *SoCoClient) doGetCtx(ctx context.Context, endpoint, action, speaker string) error {
	c.logger.Debug("SoCo-CLI request",
		zap.String("action", action),
		zap.String("speaker", speaker),
		zap.String("url", endpoint))

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return fmt.Errorf("sococli %s: failed to create request: %w", action, err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sococli %s request failed: %w", action, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("sococli %s: failed to read response body: %w", action, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("sococli %s returned HTTP %d: %s", action, resp.StatusCode, string(body))
	}

	var socoResp SoCoResponse
	if err := json.Unmarshal(body, &socoResp); err != nil {
		return fmt.Errorf("sococli %s: failed to parse response: %w", action, err)
	}

	if socoResp.ExitCode != 0 {
		return fmt.Errorf("sococli %s failed (exit_code=%d): %s", action, socoResp.ExitCode, socoResp.ErrorMsg)
	}

	c.logger.Debug("SoCo-CLI response",
		zap.String("action", action),
		zap.String("speaker", speaker),
		zap.String("result", socoResp.Result))

	return nil
}
