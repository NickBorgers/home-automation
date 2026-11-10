// Package valetudo is a small client for the REST API (v2) of Valetudo, the open firmware for robot
// vacuums. It reads the robot's state and map position and sends segment cleans, presets and basic
// commands. See https://valetudo.cloud/pages/development/rest-api.html.
package valetudo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Status values of StatusStateAttribute.
const (
	StatusError         = "error"
	StatusDocked        = "docked"
	StatusIdle          = "idle"
	StatusReturning     = "returning"
	StatusCleaning      = "cleaning"
	StatusPaused        = "paused"
	StatusManualControl = "manual_control"
	StatusMoving        = "moving"
)

// FlagResumable is the StatusStateAttribute flag of a job the robot paused and can resume, for example to
// recharge in the middle of a clean.
const FlagResumable = "resumable"

// Preset types of PresetSelectionStateAttribute.
const (
	PresetFanSpeed      = "fan_speed"
	PresetWaterGrade    = "water_grade"
	PresetOperationMode = "operation_mode"
)

// Preset capabilities.
const (
	CapabilityFanSpeed      = "FanSpeedControlCapability"
	CapabilityWaterUsage    = "WaterUsageControlCapability"
	CapabilityOperationMode = "OperationModeControlCapability"
)

// Basic control actions.
const (
	ActionStart = "start"
	ActionStop  = "stop"
	ActionPause = "pause"
	ActionHome  = "home"
)

// defaultTimeout bounds every request. The map reply is the largest, a few megabytes.
const defaultTimeout = 20 * time.Second

// maxBody caps how much of a reply is read.
const maxBody = 64 << 20

// Client talks to one robot.
type Client struct {
	base string
	http *http.Client
}

// NewClient returns a client for the robot at baseURL, such as http://192.0.2.10. httpClient may be nil.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{base: strings.TrimRight(baseURL, "/"), http: httpClient}
}

// State is what the robot reports about itself.
type State struct {
	// Battery is the charge in percent. HasBattery is false when the robot did not report it.
	Battery    int
	HasBattery bool
	// Status is a Status* value, such as "docked". Flag adds detail, such as "resumable".
	Status string
	Flag   string
	// Presets maps a preset type, such as "operation_mode", to its current value.
	Presets map[string]string
}

// Docked reports whether the robot sits on its dock.
func (s *State) Docked() bool {
	return s.Status == StatusDocked
}

type attribute struct {
	Class string  `json:"__class"`
	Level float64 `json:"level"`
	Value string  `json:"value"`
	Flag  string  `json:"flag"`
	Type  string  `json:"type"`
}

// State reads GET /api/v2/robot/state/attributes.
func (c *Client) State(ctx context.Context) (*State, error) {
	var attrs []attribute
	if err := c.get(ctx, "/api/v2/robot/state/attributes", &attrs); err != nil {
		return nil, err
	}
	s := &State{Presets: map[string]string{}}
	for _, a := range attrs {
		switch a.Class {
		case "BatteryStateAttribute":
			s.Battery, s.HasBattery = int(a.Level+0.5), true
		case "StatusStateAttribute":
			s.Status, s.Flag = a.Value, a.Flag
		case "PresetSelectionStateAttribute":
			if a.Type != "" {
				s.Presets[a.Type] = a.Value
			}
		}
	}
	if s.Status == "" {
		return nil, fmt.Errorf("valetudo state has no StatusStateAttribute")
	}
	return s, nil
}

// Segment is a room the robot knows. IDs are strings.
type Segment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Map is the part of the robot's map the scheduler needs. Valetudo map coordinates are in centimetres;
// a map pixel is PixelSize centimetres.
type Map struct {
	PixelSize float64
	// Robot and Charger are positions in centimetres, nil when the map has none.
	Robot   *[2]float64
	Charger *[2]float64
	// Segments are the segment layers, in map order.
	Segments []Segment
}

// RobotPixel is the robot's position in map pixels. ok is false without a position.
func (m *Map) RobotPixel() (px [2]float64, ok bool) {
	return m.pixel(m.Robot)
}

// ChargerPixel is the dock's position in map pixels. ok is false without a dock.
func (m *Map) ChargerPixel() (px [2]float64, ok bool) {
	return m.pixel(m.Charger)
}

func (m *Map) pixel(cm *[2]float64) ([2]float64, bool) {
	if cm == nil || m.PixelSize <= 0 {
		return [2]float64{}, false
	}
	return [2]float64{cm[0] / m.PixelSize, cm[1] / m.PixelSize}, true
}

type mapReply struct {
	PixelSize float64 `json:"pixelSize"`
	Layers    []struct {
		Type     string `json:"type"`
		MetaData struct {
			SegmentID string `json:"segmentId"`
			Name      string `json:"name"`
		} `json:"metaData"`
	} `json:"layers"`
	Entities []struct {
		Type   string    `json:"type"`
		Points []float64 `json:"points"`
	} `json:"entities"`
}

// Map reads GET /api/v2/robot/state/map.
func (c *Client) Map(ctx context.Context) (*Map, error) {
	var r mapReply
	if err := c.get(ctx, "/api/v2/robot/state/map", &r); err != nil {
		return nil, err
	}
	m := &Map{PixelSize: r.PixelSize}
	for _, e := range r.Entities {
		if len(e.Points) < 2 {
			continue
		}
		p := [2]float64{e.Points[0], e.Points[1]}
		switch e.Type {
		case "robot_position":
			m.Robot = &p
		case "charger_location":
			m.Charger = &p
		}
	}
	for _, l := range r.Layers {
		if l.Type == "segment" {
			m.Segments = append(m.Segments, Segment{ID: l.MetaData.SegmentID, Name: l.MetaData.Name})
		}
	}
	return m, nil
}

// Segments reads GET /api/v2/robot/capabilities/MapSegmentationCapability.
func (c *Client) Segments(ctx context.Context) ([]Segment, error) {
	var segs []Segment
	if err := c.get(ctx, "/api/v2/robot/capabilities/MapSegmentationCapability", &segs); err != nil {
		return nil, err
	}
	return segs, nil
}

// CleanSegments starts one pass over the segments, in the given order.
func (c *Client) CleanSegments(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return fmt.Errorf("clean segments: no segment ids")
	}
	return c.put(ctx, "/api/v2/robot/capabilities/MapSegmentationCapability", map[string]any{
		"action":      "start_segment_action",
		"segment_ids": ids,
		"iterations":  1,
		"customOrder": true,
	})
}

// SetPreset selects a preset of a preset capability, such as CapabilityFanSpeed and "medium".
func (c *Client) SetPreset(ctx context.Context, capability, name string) error {
	switch capability {
	case CapabilityFanSpeed, CapabilityWaterUsage, CapabilityOperationMode:
	default:
		return fmt.Errorf("unknown preset capability %q", capability)
	}
	if name == "" {
		return fmt.Errorf("%s: empty preset name", capability)
	}
	return c.put(ctx, "/api/v2/robot/capabilities/"+capability+"/preset", map[string]string{"name": name})
}

// Basic sends a BasicControlCapability action: start, stop, pause or home.
func (c *Client) Basic(ctx context.Context, action string) error {
	switch action {
	case ActionStart, ActionStop, ActionPause, ActionHome:
	default:
		return fmt.Errorf("unknown basic control action %q", action)
	}
	return c.put(ctx, "/api/v2/robot/capabilities/BasicControlCapability", map[string]string{"action": action})
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	body, err := c.do(req)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("GET %s: decode: %w", path, err)
	}
	return nil
}

func (c *Client) put(ctx context.Context, path string, in any) error {
	data, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("PUT %s: encode: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.base+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("PUT %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	_, err = c.do(req)
	return err
}

func (c *Client) do(req *http.Request) ([]byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("%s %s: read: %w", req.Method, req.URL.Path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, fmt.Errorf("%s %s: HTTP %d: %s", req.Method, req.URL.Path, resp.StatusCode, snippet)
	}
	return body, nil
}
