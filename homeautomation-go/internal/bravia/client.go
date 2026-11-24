// Package bravia talks to a Sony Bravia TV over its local REST API (JSON-RPC over HTTP).
//
// It supports the small set of direct reads and writes needed by the TV and watchdog plugins.
package bravia

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrTurnedOff means the TV answered but its screen is off. Picture settings only work while it is on.
var ErrTurnedOff = errors.New("bravia: TV is turned off")

// defaultTimeout matches pybravia. The Bravia API is slow and sometimes stalls.
const defaultTimeout = 10 * time.Second

// Setting is a numeric picture setting and the range the TV accepts for it.
type Setting struct {
	Current int
	Min     int
	Max     int
}

// Client sends picture commands to one TV. It is safe for concurrent use.
type Client struct {
	baseURL string // e.g. http://10.0.0.5
	psk     string
	http    *http.Client
}

// NewClient returns a client for the TV at host (an IP address or host name, optionally with a port).
// psk is the TV's pre-shared key. It is sent exactly as given, in the X-Auth-PSK header.
func NewClient(host, psk string) *Client {
	base := strings.TrimRight(host, "/")
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "http://" + base
	}
	return &Client{
		baseURL: base,
		psk:     psk,
		http:    &http.Client{Timeout: defaultTimeout},
	}
}

// PowerStatus is the panel power state returned by getPowerStatus.
type PowerStatus string

const (
	PowerStatusActive  PowerStatus = "active"
	PowerStatusStandby PowerStatus = "standby"
)

// GetPowerStatus reads the panel's power state independently of Home Assistant.
func (c *Client) GetPowerStatus(ctx context.Context) (PowerStatus, error) {
	raw, err := c.call(ctx, "/sony/system", "getPowerStatus", nil)
	if err != nil {
		return "", err
	}
	var result []struct {
		Status PowerStatus `json:"status"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("bravia: decode power status: %w", err)
	}
	if len(result) != 1 || (result[0].Status != PowerStatusActive && result[0].Status != PowerStatusStandby) {
		return "", fmt.Errorf("bravia: unexpected power status response %s", string(raw))
	}
	return result[0].Status, nil
}

type rpcRequest struct {
	Method  string        `json:"method"`
	ID      int           `json:"id"`
	Version string        `json:"version"`
	Params  []interface{} `json:"params"`
}

type rpcResponse struct {
	Result json.RawMessage   `json:"result"`
	Error  []json.RawMessage `json:"error"`
}

type pictureSetting struct {
	Target       string `json:"target"`
	CurrentValue string `json:"currentValue"`
	IsAvailable  *bool  `json:"isAvailable"`
	Candidate    []struct {
		Min  *float64 `json:"min"`
		Max  *float64 `json:"max"`
		Step *float64 `json:"step"`
	} `json:"candidate"`
}

// GetBrightness reads the current brightness and the range the TV accepts.
func (c *Client) GetBrightness(ctx context.Context) (Setting, error) {
	raw, err := c.call(ctx, "/sony/video", "getPictureQualitySettings", map[string]string{"target": "brightness"})
	if err != nil {
		return Setting{}, err
	}
	// The result is a list holding one list of settings.
	var outer [][]pictureSetting
	if err := json.Unmarshal(raw, &outer); err != nil {
		return Setting{}, fmt.Errorf("bravia: decode picture settings: %w", err)
	}
	for _, group := range outer {
		for _, s := range group {
			if s.Target != "brightness" {
				continue
			}
			if s.IsAvailable != nil && !*s.IsAvailable {
				return Setting{}, errors.New("bravia: brightness is not available in the current picture mode")
			}
			cur, err := strconv.Atoi(strings.TrimSpace(s.CurrentValue))
			if err != nil {
				return Setting{}, fmt.Errorf("bravia: brightness value %q: %w", s.CurrentValue, err)
			}
			if len(s.Candidate) == 0 || s.Candidate[0].Min == nil || s.Candidate[0].Max == nil {
				return Setting{}, errors.New("bravia: brightness has no numeric range")
			}
			lo, hi := int(*s.Candidate[0].Min), int(*s.Candidate[0].Max)
			if lo > hi {
				return Setting{}, fmt.Errorf("bravia: brightness range is inverted (%d > %d)", lo, hi)
			}
			return Setting{Current: cur, Min: lo, Max: hi}, nil
		}
	}
	return Setting{}, errors.New("bravia: TV did not report brightness")
}

// SetBrightness sets the picture brightness. The TV takes the value as a string.
func (c *Client) SetBrightness(ctx context.Context, value int) error {
	params := map[string]interface{}{
		"settings": []map[string]string{{"target": "brightness", "value": strconv.Itoa(value)}},
	}
	_, err := c.call(ctx, "/sony/video", "setPictureQualitySettings", params)
	return err
}

func (c *Client) call(ctx context.Context, path, method string, param interface{}) (json.RawMessage, error) {
	params := []interface{}{}
	if param != nil {
		params = append(params, param)
	}
	body, err := json.Marshal(rpcRequest{Method: method, ID: 1, Version: "1.0", Params: params})
	if err != nil {
		return nil, fmt.Errorf("bravia: encode %s: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("bravia: build %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-PSK", c.psk)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bravia: %s: %w", method, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("bravia: read %s response: %w", method, err)
	}

	var out rpcResponse
	if jerr := json.Unmarshal(data, &out); jerr != nil {
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("bravia: %s: HTTP %d", method, resp.StatusCode)
		}
		return nil, fmt.Errorf("bravia: decode %s response: %w", method, jerr)
	}
	if len(out.Error) > 0 {
		return nil, rpcError(method, out.Error)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bravia: %s: HTTP %d", method, resp.StatusCode)
	}
	return out.Result, nil
}

// rpcError turns a Bravia error array, [code, "message"], into an error.
// Code 40005 means the screen is off. The message differs by call: reads say "not power-on" (pybravia
// maps that to BraviaTurnedOff), and writes on the A80K say "Display Is Turned off".
func rpcError(method string, parts []json.RawMessage) error {
	var code int
	var msg string
	_ = json.Unmarshal(parts[0], &code)
	if len(parts) > 1 {
		_ = json.Unmarshal(parts[1], &msg)
	}
	if code == 40005 || strings.Contains(strings.ToLower(msg), "not power-on") {
		return fmt.Errorf("%w (%s: %d %s)", ErrTurnedOff, method, code, msg)
	}
	if code == 401 || code == 403 {
		return fmt.Errorf("bravia: %s: not authorized (%d %s); check BRAVIA_PSK", method, code, msg)
	}
	return fmt.Errorf("bravia: %s: error %d %s", method, code, msg)
}
