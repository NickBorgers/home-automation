package music

import (
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
// It supports Tidal playlist playback via the sharelink mechanism.
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

// PlayShareLink is a convenience method that adds a Tidal share link to the queue
// and starts playback. This is the main entry point for Tidal playlist playback.
func (c *SoCoClient) PlayShareLink(speakerName, tidalURL string) error {
	if err := c.ShareLink(speakerName, tidalURL); err != nil {
		return fmt.Errorf("sharelink failed: %w", err)
	}
	if err := c.PlayFromQueue(speakerName); err != nil {
		return fmt.Errorf("play_from_queue failed: %w", err)
	}
	return nil
}

// doGet performs a GET request and checks the SoCo-CLI response for errors.
func (c *SoCoClient) doGet(endpoint, action, speaker string) error {
	c.logger.Debug("SoCo-CLI request",
		zap.String("action", action),
		zap.String("speaker", speaker),
		zap.String("url", endpoint))

	resp, err := c.httpClient.Get(endpoint)
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
