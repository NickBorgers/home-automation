package music

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	// socoAttemptTimeout is the per-attempt timeout for most SoCo-CLI operations.
	// soco-cli waits up to 20s for a speaker and does not stop a command when we
	// abandon its request. The attempt waits longer than that, so when it ends
	// soco-cli has finished the command too and the next request cannot overlap it.
	socoAttemptTimeout = 25 * time.Second

	// socoLongTimeout is the per-attempt timeout for operations that are
	// inherently slower, such as sharelink (downloads and enqueues content).
	// Like socoAttemptTimeout, it must stay above soco-cli's 20s speaker timeout.
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
// It also reads current volume and playback status, so speakers need no HA entity.
//
// The client sends one request at a time to a SoCo-CLI server (issue #1431).
// soco-cli captures each command's output by swapping the process-wide stdout
// with no lock, so overlapping requests can come back with an empty result.
type SoCoClient struct {
	baseURL    string
	httpClient *http.Client
	logger     *zap.Logger
	readOnly   bool
	// requestLock holds one token while a request is in flight. Every client of
	// the same baseURL shares it, because several plugins make their own client.
	// It is nil when AllowOverlappingRequests was called.
	requestLock chan struct{}
}

// AllowOverlappingRequests stops this client from waiting for other SoCo-CLI
// requests to finish. Use it only with a soco-cli that captures each request's
// output separately (avantrec/soco-cli#90); older versions return empty results
// when requests overlap. Call it before the client is used.
func (c *SoCoClient) AllowOverlappingRequests() {
	c.requestLock = nil
}

// socoRequestLocks maps a SoCo-CLI base URL to the request lock its clients share.
var socoRequestLocks sync.Map

// socoRequestLock returns the request lock for a SoCo-CLI base URL.
func socoRequestLock(baseURL string) chan struct{} {
	lock, _ := socoRequestLocks.LoadOrStore(baseURL, make(chan struct{}, 1))
	return lock.(chan struct{})
}

// NewSoCoClient creates a new SoCo-CLI HTTP client.
// Returns nil if baseURL is empty.
func NewSoCoClient(baseURL string, logger *zap.Logger, readOnly bool) *SoCoClient {
	if baseURL == "" {
		return nil
	}
	return &SoCoClient{
		baseURL:     baseURL,
		httpClient:  &http.Client{}, // Per-attempt timeouts via context in doGetResultCtx
		logger:      logger.Named("sococli"),
		readOnly:    readOnly,
		requestLock: socoRequestLock(baseURL),
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

	return c.doGet(endpoint, "volume_write", speakerName)
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

// UngroupSpeakerCtx removes a speaker from its current group in one attempt, with no retries.
// ctx can stop the call only while it waits for its turn. Once sent, the request runs until
// soco-cli answers or socoAttemptTimeout passes, because soco-cli would keep running it anyway.
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

	_, err := c.doGetResultCtx(ctx, endpoint, "ungroup", speakerName, socoAttemptTimeout)
	return err
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

// =============================================================================
// State Reads (safe in READ_ONLY mode)
// =============================================================================

// GetVolume returns the speaker volume as a percentage (0-100).
func (c *SoCoClient) GetVolume(speakerName string) (int, error) {
	// GET /{speaker}/volume
	endpoint := fmt.Sprintf("%s/%s/volume", c.baseURL, url.PathEscape(speakerName))

	result, err := c.getResult(endpoint, "volume_read", speakerName)
	if err != nil {
		return 0, err
	}
	volume, err := strconv.Atoi(result)
	if err != nil {
		return 0, fmt.Errorf("sococli volume_read: unexpected result %q for %s: %w", result, speakerName, err)
	}
	if volume < 0 || volume > 100 {
		return 0, fmt.Errorf("sococli volume_read: result %d for %s is outside range 0-100", volume, speakerName)
	}
	return volume, nil
}

// GetPlaybackState returns the Sonos transport state, such as "PLAYING",
// "PAUSED_PLAYBACK", "STOPPED" or "TRANSITIONING".
func (c *SoCoClient) GetPlaybackState(speakerName string) (string, error) {
	// GET /{speaker}/playback
	endpoint := fmt.Sprintf("%s/%s/playback", c.baseURL, url.PathEscape(speakerName))

	return c.getResult(endpoint, "playback", speakerName)
}

// GetCurrentURI returns the URI of the track the speaker's group is playing.
// It is empty when nothing is loaded. For a Tidal track it looks like
// "x-sonos-http:track%2f312527385.flac?sid=174&sn=9".
func (c *SoCoClient) GetCurrentURI(speakerName string) (string, error) {
	// GET /{speaker}/get_uri
	endpoint := fmt.Sprintf("%s/%s/get_uri", c.baseURL, url.PathEscape(speakerName))

	return c.getResult(endpoint, "get_uri", speakerName)
}

// GetGroupMembers returns the names of the speakers in speakerName's group, coordinator first.
// A speaker that is not grouped returns only itself.
func (c *SoCoClient) GetGroupMembers(speakerName string) ([]string, error) {
	// GET /{speaker}/groups lists every group, one "Coordinator: Member, Member" line each.
	endpoint := fmt.Sprintf("%s/%s/groups", c.baseURL, url.PathEscape(speakerName))

	result, err := c.getResult(endpoint, "groups", speakerName)
	if err != nil {
		return nil, err
	}
	return parseSoCoGroup(result, speakerName), nil
}

// parseSoCoGroup finds the group that contains speakerName in the output of the groups action.
func parseSoCoGroup(groups, speakerName string) []string {
	for _, line := range strings.Split(groups, "\n") {
		coordinator, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		members := []string{strings.TrimSpace(coordinator)}
		for _, member := range strings.Split(rest, ",") {
			if member = strings.TrimSpace(member); member != "" {
				members = append(members, member)
			}
		}
		for _, member := range members {
			if member == speakerName {
				return members
			}
		}
	}
	return []string{speakerName}
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

// doGetWithRetry performs a GET request and retries only when the failed attempt
// surely did not run (see isRetryableError). Each attempt uses attemptTimeout.
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

		_, lastErr = c.doGetResultCtx(context.Background(), endpoint, action, speaker, attemptTimeout)

		if lastErr == nil {
			return nil
		}

		if !isRetryableError(lastErr) {
			return lastErr
		}
	}
	return fmt.Errorf("sococli %s: all %d attempts failed: %w", action, socoMaxRetries+1, lastErr)
}

// isRetryableError reports whether a failed attempt surely did not run in soco-cli,
// so that sending it again cannot run the command twice or overlap it.
//
// Retried: the client could not connect (connection refused, DNS failure), and
// HTTP 502, 503 and 429. soco-cli never ran the command for these; a proxy, if
// one is deployed in front of soco-cli, sends them.
//
// Not retried: a client-side timeout, because soco-cli keeps running an abandoned
// command; HTTP 500 and 504, because soco-cli may have started the command; any
// other HTTP status; SoCo application errors (non-zero exit_code); and a failure
// to read or parse a response, because the command already ran.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return false
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "returned HTTP 502") ||
		strings.Contains(s, "returned HTTP 503") ||
		strings.Contains(s, "returned HTTP 429")
}

// doGetResultCtx performs one GET attempt and returns the parsed SoCo-CLI response.
// It waits until no other request to the same SoCo-CLI server is in flight, and
// holds that turn until the attempt ends. ctx can stop the wait. Once the request
// is sent, only attemptTimeout ends it: soco-cli keeps running an abandoned command,
// so giving up early would let the next request overlap it.
func (c *SoCoClient) doGetResultCtx(
	ctx context.Context, endpoint, action, speaker string, attemptTimeout time.Duration,
) (*SoCoResponse, error) {
	if c.requestLock != nil {
		select {
		case c.requestLock <- struct{}{}:
		case <-ctx.Done():
			return nil, fmt.Errorf("sococli %s: not sent: %w", action, ctx.Err())
		}
		defer func() { <-c.requestLock }()
	}

	c.logger.Debug("SoCo-CLI request",
		zap.String("action", action),
		zap.String("speaker", speaker),
		zap.String("url", endpoint))

	attemptCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), attemptTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(attemptCtx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("sococli %s: failed to create request: %w", action, err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sococli %s request failed: %w", action, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("sococli %s: failed to read response body: %w", action, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("sococli %s returned HTTP %d: %s", action, resp.StatusCode, string(body))
	}

	var socoResp SoCoResponse
	if err := json.Unmarshal(body, &socoResp); err != nil {
		return nil, fmt.Errorf("sococli %s: failed to parse response: %w", action, err)
	}

	if socoResp.ExitCode != 0 {
		return nil, fmt.Errorf("sococli %s failed (exit_code=%d): %s", action, socoResp.ExitCode, socoResp.ErrorMsg)
	}

	c.logger.Debug("SoCo-CLI response",
		zap.String("action", action),
		zap.String("speaker", speaker),
		zap.String("result", socoResp.Result))

	return &socoResp, nil
}

// getResult performs a GET, retrying as doGetWithRetry does, and returns the SoCo-CLI
// result string. Used by state reads, which are safe in READ_ONLY mode.
func (c *SoCoClient) getResult(endpoint, action, speaker string) (string, error) {
	var lastErr error
	for attempt := 0; attempt <= socoMaxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(socoRetryDelay)
		}

		resp, err := c.doGetResultCtx(context.Background(), endpoint, action, speaker, socoAttemptTimeout)
		if err == nil {
			return strings.TrimSpace(resp.Result), nil
		}

		lastErr = err
		if !isRetryableError(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("sococli %s: all %d attempts failed: %w", action, socoMaxRetries+1, lastErr)
}
