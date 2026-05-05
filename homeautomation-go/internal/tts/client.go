// Package tts provides text-to-speech synthesis (via a Kokoro server) and a
// local HTTP file server that exposes the synthesized MP3s to LAN media
// players (Sonos) so Home Assistant can tell them to play
// http://<lan-ip>:<port>/audio/<id>.mp3.
package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// Client speaks to an OpenAI-compatible TTS server (we use Kokoro). Stateless;
// safe for concurrent use.
type Client struct {
	endpoint string
	voice    string
	http     *http.Client
	logger   *zap.Logger
}

// NewClient builds a Kokoro client. The 30s timeout covers paragraph-length
// synthesis on a moderately loaded server; short phrases return in ~2s.
func NewClient(endpoint, voice string, logger *zap.Logger) *Client {
	return &Client{
		endpoint: endpoint,
		voice:    voice,
		http:     &http.Client{Timeout: 30 * time.Second},
		logger:   logger.Named("tts.client"),
	}
}

type kokoroRequest struct {
	Input          string `json:"input"`
	Voice          string `json:"voice"`
	Model          string `json:"model"`
	ResponseFormat string `json:"response_format"`
}

// Synthesize returns the MP3 bytes spoken from text. Returns an error on
// non-2xx response or empty body. No retry — Kokoro outages are rare and
// announcement loss is acceptable.
func (c *Client) Synthesize(ctx context.Context, text string) ([]byte, error) {
	body, err := json.Marshal(kokoroRequest{
		Input:          text,
		Voice:          c.voice,
		Model:          "kokoro",
		ResponseFormat: "mp3",
	})
	if err != nil {
		return nil, fmt.Errorf("marshal kokoro request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build kokoro request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kokoro request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("kokoro returned %d: %s", resp.StatusCode, string(snippet))
	}

	mp3, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read kokoro response: %w", err)
	}
	if len(mp3) == 0 {
		return nil, fmt.Errorf("kokoro returned empty body")
	}
	return mp3, nil
}
