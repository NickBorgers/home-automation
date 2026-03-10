package music

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"go.uber.org/zap"
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
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger:   logger.Named("sococli"),
		readOnly: readOnly,
	}
}

// =============================================================================
// Tidal Playback (existing)
// =============================================================================

// ShareLink sends a Tidal share link to the specified speaker's queue.
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

	return c.doGet(endpoint, "sharelink", speakerName)
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

	// GET /{speaker}/mute
	endpoint := fmt.Sprintf("%s/%s/mute",
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

// PlayURI plays a media URI on a speaker.
func (c *SoCoClient) PlayURI(speakerName, uri string) error {
	if c.readOnly {
		c.logger.Debug("READ_ONLY: Would play_uri via SoCo-CLI",
			zap.String("speaker", speakerName),
			zap.String("uri", uri))
		return nil
	}

	// GET /{speaker}/play_uri/{uri}
	endpoint := fmt.Sprintf("%s/%s/play_uri/%s",
		c.baseURL,
		url.PathEscape(speakerName),
		url.PathEscape(uri))

	return c.doGet(endpoint, "play_uri", speakerName)
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

// doGet performs a GET request and checks the SoCo-CLI response for errors.
func (c *SoCoClient) doGet(endpoint, action, speaker string) error {
	return c.doGetCtx(context.Background(), endpoint, action, speaker)
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
