package picos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"

	"go.uber.org/zap"
)

// buttonEventType is the event Home Assistant fires for a Lutron Caseta Pico press or release.
const buttonEventType = "lutron_caseta_button_event"

// Direction of a press. A paddle Pico reports its two buttons as button_type "on" and "off".
const (
	directionUp   = "up"
	directionDown = "down"
)

// buttonEvent is the part of lutron_caseta_button_event this plugin reads.
type buttonEvent struct {
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	ButtonType string `json:"button_type"`
	Action     string `json:"action"`
}

// lastCommand is the level a target was last told to reach.
type lastCommand struct {
	pct int
	at  time.Time
}

// Manager is the picos plugin.
type Manager struct {
	ctx      context.Context
	haClient ha.HAClient
	logger   *zap.Logger
	readOnly bool
	cfg      *Config

	shadowTracker *shadowstate.PicosTracker

	buttons map[string]Button // by device ID
	targets map[string]Target // by name

	// mu serializes presses, so a quick second press sees the level the first one set.
	mu   sync.Mutex
	last map[string]lastCommand

	sub ha.Subscription

	// Injectable for tests.
	now func() time.Time
}

// NewManager builds a Manager. It does not subscribe until Start.
func NewManager(ctx context.Context, haClient ha.HAClient, cfg *Config, logger *zap.Logger, readOnly bool) *Manager {
	m := &Manager{
		ctx:           ctx,
		haClient:      haClient,
		logger:        logger.Named("picos"),
		readOnly:      readOnly,
		cfg:           cfg,
		shadowTracker: shadowstate.NewPicosTracker(),
		buttons:       make(map[string]Button, len(cfg.Buttons)),
		targets:       make(map[string]Target, len(cfg.Targets)),
		last:          make(map[string]lastCommand),
		now:           time.Now,
	}
	for _, b := range cfg.Buttons {
		m.buttons[b.DeviceID] = b
	}
	for _, t := range cfg.Targets {
		m.targets[t.Name] = t
	}
	return m
}

// GetShadowState returns the latest shadow state snapshot.
func (m *Manager) GetShadowState() *shadowstate.PicosShadowState {
	return m.shadowTracker.GetState()
}

// Start subscribes to Pico button events.
func (m *Manager) Start() error {
	sub, err := m.haClient.SubscribeEvents(buttonEventType, m.handleEvent)
	if err != nil {
		return fmt.Errorf("subscribe to %s: %w", buttonEventType, err)
	}
	m.sub = sub

	m.recordReferenceLevels()

	m.logger.Info("Picos started",
		zap.Int("buttons", len(m.buttons)),
		zap.Int("targets", len(m.targets)))
	return nil
}

// Stop ends the subscription.
func (m *Manager) Stop() {
	if m.sub != nil {
		_ = m.sub.Unsubscribe()
		m.sub = nil
	}
}

// recordReferenceLevels seeds shadow-state inputs with each target's current level.
func (m *Manager) recordReferenceLevels() {
	for name, t := range m.targets {
		if pct, err := m.reportedPct(t); err == nil {
			m.shadowTracker.UpdateCurrentInputs(map[string]interface{}{name + ".referencePct": pct})
		}
	}
}

// handleEvent reacts to one button event.
func (m *Manager) handleEvent(_ string, data json.RawMessage) {
	var ev buttonEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		m.logger.Warn("Cannot parse button event", zap.Error(err))
		return
	}

	button, matched := m.buttons[ev.DeviceID]
	m.shadowTracker.RecordEvent(shadowstate.PicoEvent{
		At:         m.now(),
		DeviceID:   ev.DeviceID,
		DeviceName: ev.DeviceName,
		ButtonType: ev.ButtonType,
		Action:     ev.Action,
		Matched:    matched,
	})
	if !matched {
		m.logger.Debug("Ignoring button event from unconfigured device",
			zap.String("device_id", ev.DeviceID),
			zap.String("device_name", ev.DeviceName))
		return
	}

	// A press is followed by a release. Act on the press only.
	if ev.Action != "press" {
		return
	}

	var direction string
	switch ev.ButtonType {
	case "on":
		direction = directionUp
	case "off":
		direction = directionDown
	default:
		m.logger.Debug("Ignoring unsupported button",
			zap.String("button", button.Name),
			zap.String("button_type", ev.ButtonType))
		return
	}

	m.step(button, m.targets[button.Target], direction)
}

// step moves a target one step up or down.
func (m *Manager) step(button Button, target Target, direction string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	press := shadowstate.PicoTargetState{
		LastPressAt: m.now(),
		Button:      button.Name,
		Direction:   direction,
		ReadOnly:    m.readOnly,
	}

	from, err := m.currentPct(target)
	if err != nil {
		press.Decision = "could not read the reference light"
		press.Error = err.Error()
		m.shadowTracker.RecordPress(target.Name, press)
		m.logger.Error("Cannot read reference light",
			zap.String("target", target.Name),
			zap.String("entity_id", target.Lights[0]),
			zap.Error(err))
		return
	}
	to, off := m.nextLevel(from, direction)
	press.FromPct, press.ToPct = from, to
	press.Decision = describeStep(from, to, off)

	m.logger.Info("Pico press",
		zap.String("button", button.Name),
		zap.String("target", target.Name),
		zap.String("direction", direction),
		zap.Int("from_pct", from),
		zap.Int("to_pct", to),
		zap.Bool("off", off),
		zap.Bool("read_only", m.readOnly))

	if m.readOnly {
		m.shadowTracker.RecordPress(target.Name, press)
		return
	}

	if err := m.send(target, to, off); err != nil {
		press.Error = err.Error()
		m.logger.Error("Pico command failed", zap.String("target", target.Name), zap.Error(err))
	}
	m.shadowTracker.RecordPress(target.Name, press)
	m.shadowTracker.UpdateCurrentInputs(map[string]interface{}{target.Name + ".referencePct": from})
	if press.Error == "" {
		m.last[target.Name] = lastCommand{pct: to, at: m.now()}
	}
}

// nextLevel returns the brightness a press leads to, and whether that means off.
//
//	up:   off -> OnPct, then +StepPct per press, capped at 100.
//	down: at or below OffAtOrBelowPct -> off, otherwise -StepPct (never below 1).
func (m *Manager) nextLevel(from int, direction string) (to int, off bool) {
	if direction == directionUp {
		if from <= 0 {
			return m.cfg.OnPct, false
		}
		return min(100, from+m.cfg.StepPct), false
	}
	if from <= m.cfg.OffAtOrBelowPct {
		return 0, true
	}
	return max(1, from-m.cfg.StepPct), false
}

func describeStep(from, to int, off bool) string {
	if off {
		return fmt.Sprintf("off from %d%%", from)
	}
	return fmt.Sprintf("%d%% to %d%%", from, to)
}

// currentPct returns the level the next press steps from. A level this plugin set a moment ago
// beats the light's report, which lags.
func (m *Manager) currentPct(target Target) (int, error) {
	if c, ok := m.last[target.Name]; ok && m.now().Sub(c.at) < time.Duration(m.cfg.RecentSeconds)*time.Second {
		return c.pct, nil
	}
	return m.reportedPct(target)
}

// reportedPct reads the reference light. Off, unavailable, and unknown all count as 0.
func (m *Manager) reportedPct(target Target) (int, error) {
	st, err := m.haClient.GetState(target.Lights[0])
	if err != nil {
		return 0, err
	}
	if st == nil || st.State != "on" {
		return 0, nil
	}
	if b, ok := st.Attributes["brightness"].(float64); ok {
		return int(math.Round(b / 255 * 100)), nil
	}
	return 100, nil
}

// send sets every light of the target to the new level, or turns them all off.
// It tries every light and returns the errors together.
func (m *Manager) send(target Target, pct int, off bool) error {
	var errs []error
	for _, entityID := range target.Lights {
		data := map[string]interface{}{
			"entity_id":  entityID,
			"transition": m.cfg.TransitionSeconds,
		}
		service := "turn_off"
		if !off {
			service = "turn_on"
			data["brightness_pct"] = pct
		}
		if err := m.haClient.CallService(m.ctx, "light", service, data); err != nil {
			errs = append(errs, fmt.Errorf("%s %s: %w", service, entityID, err))
		}
	}
	return errors.Join(errs...)
}
