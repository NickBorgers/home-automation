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
	"homeautomation/internal/state"

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

// stopper is a pending timer. *time.Timer satisfies it.
type stopper interface {
	Stop() bool
}

// litPath is the set of path lights one button turned on, and the timer that turns them off.
type litPath struct {
	lights []string
	timer  stopper
	gen    int // bumps each time the timer is replaced, so a stale timer does nothing
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

	// stateManager enables early wake and path lights. Nil turns both off.
	stateManager *state.Manager

	shadowTracker *shadowstate.PicosTracker

	buttons map[string]Button // by device ID
	targets map[string]Target // by name

	// mu serializes presses, so a quick second press sees the level the first one set.
	// It also guards paths.
	mu   sync.Mutex
	last map[string]lastCommand

	// paths holds, per button device ID, the path lights this plugin turned on.
	paths map[string]*litPath

	sub ha.Subscription

	// Injectable for tests.
	now       func() time.Time
	afterFunc func(d time.Duration, f func()) stopper
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
		paths:         make(map[string]*litPath),
		now:           time.Now,
		afterFunc: func(d time.Duration, f func()) stopper {
			return time.AfterFunc(d, f)
		},
	}
	for _, b := range cfg.Buttons {
		m.buttons[b.DeviceID] = b
	}
	for _, t := range cfg.Targets {
		m.targets[t.Name] = t
	}
	return m
}

// SetStateManager gives the plugin access to house state. It turns on the wakes_house and
// path_lights behaviors. Call it before Start.
func (m *Manager) SetStateManager(sm *state.Manager) {
	m.stateManager = sm
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

// Stop ends the subscription and cancels path light timers. Path lights that are on stay on.
func (m *Manager) Stop() {
	if m.sub != nil {
		_ = m.sub.Unsubscribe()
		m.sub = nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.paths {
		if p.timer != nil {
			p.timer.Stop()
			p.timer = nil
		}
		p.gen++
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

	turnsOnFromOff := direction == directionUp && from <= 0 && !off

	if m.readOnly {
		if turnsOnFromOff && target.WakesHouse && m.stateManager != nil {
			press.EarlyWake = "read-only: would check for an early wake"
		}
		m.shadowTracker.RecordPress(target.Name, press)
		return
	}

	if err := m.send(target, to, off); err != nil {
		press.Error = err.Error()
		m.logger.Error("Pico command failed", zap.String("target", target.Name), zap.Error(err))
	}
	if press.Error == "" && m.stateManager != nil {
		if turnsOnFromOff && target.WakesHouse {
			press.EarlyWake = m.wakeHouse(button, target)
		}
		if button.PathLights != nil {
			switch {
			case direction == directionUp:
				press.PathLights = m.lightPath(button)
			case off:
				press.PathLights = m.clearPath(button.DeviceID, "the target turned off")
			}
		}
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

// wakeHouse sets isEarlyWakeActive after an up-press turned a wakes_house target on from off,
// if someone is asleep. It returns what it did, for shadow state. Caller holds m.mu.
func (m *Manager) wakeHouse(button Button, target Target) string {
	asleep, err := m.stateManager.GetBool("isAnyoneAsleep")
	if err != nil {
		m.logger.Error("early wake: cannot read isAnyoneAsleep", zap.Error(err))
		return "cannot read isAnyoneAsleep: " + err.Error()
	}
	m.shadowTracker.UpdateCurrentInputs(map[string]interface{}{"isAnyoneAsleep": asleep})
	if !asleep {
		return "nobody asleep"
	}
	if active, err := m.stateManager.GetBool("isEarlyWakeActive"); err == nil && active {
		return "early wake already on"
	}
	if err := m.stateManager.SetBool("isEarlyWakeActive", true); err != nil {
		m.logger.Error("early wake: failed to set isEarlyWakeActive",
			zap.String("button", button.Name), zap.Error(err))
		return "failed to start early wake: " + err.Error()
	}
	m.logger.Info("early wake started: Pico turned on a wakes_house target while someone is asleep",
		zap.String("button", button.Name),
		zap.String("target", target.Name))
	return "early wake started"
}

// lightPath turns on, dim and warm, each path light of the button that is off, if the house is
// asleep. It remembers what it turned on and (re)starts the off_after timer. Caller holds m.mu.
func (m *Manager) lightPath(button Button) string {
	pl := button.PathLights
	awake, err := m.stateManager.GetBool("isAnyoneHomeAndAwake")
	if err != nil {
		// Unknown: leave the lights alone rather than light a path in an awake house.
		m.logger.Error("Path lights: cannot read isAnyoneHomeAndAwake", zap.Error(err))
		return "cannot read isAnyoneHomeAndAwake: " + err.Error()
	}
	m.shadowTracker.UpdateCurrentInputs(map[string]interface{}{"isAnyoneHomeAndAwake": awake})
	if awake {
		return "house awake: path lights skipped"
	}

	path := m.paths[button.DeviceID]
	if path == nil {
		path = &litPath{}
		m.paths[button.DeviceID] = path
	}

	var turnedOn, failed []string
	for _, entityID := range pl.Lights {
		st, err := m.haClient.GetState(entityID)
		if err != nil || st == nil || st.State != "off" {
			continue // already on, unavailable, or unknown: leave it alone
		}
		if err := m.haClient.CallService(m.ctx, "light", "turn_on", map[string]interface{}{
			"entity_id":         entityID,
			"brightness_pct":    pl.BrightnessPct,
			"color_temp_kelvin": pl.Kelvin,
		}); err != nil {
			failed = append(failed, entityID)
			m.logger.Error("Path light turn_on failed", zap.String("entity_id", entityID), zap.Error(err))
			continue
		}
		turnedOn = append(turnedOn, entityID)
		if !contains(path.lights, entityID) {
			path.lights = append(path.lights, entityID)
		}
	}

	if len(path.lights) > 0 {
		m.restartPathTimer(button.DeviceID, path, pl.OffAfter)
	}
	m.logger.Info("Path lights",
		zap.String("button", button.Name),
		zap.Strings("turned_on", turnedOn),
		zap.Strings("failed", failed),
		zap.Strings("remembered", path.lights),
		zap.Int("brightness_pct", pl.BrightnessPct),
		zap.Int("kelvin", pl.Kelvin),
		zap.Duration("off_after", pl.OffAfter))
	return fmt.Sprintf("turned on %v, remembered %v", turnedOn, path.lights)
}

// restartPathTimer replaces the button's off_after timer. Caller holds m.mu.
func (m *Manager) restartPathTimer(deviceID string, path *litPath, after time.Duration) {
	if path.timer != nil {
		path.timer.Stop()
	}
	path.gen++
	gen := path.gen
	path.timer = m.afterFunc(after, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if p := m.paths[deviceID]; p == nil || p.gen != gen {
			return // replaced or cancelled
		}
		m.clearPath(deviceID, "off_after elapsed")
	})
}

// clearPath turns off the path lights this plugin turned on for a button and forgets them.
// Caller holds m.mu.
func (m *Manager) clearPath(deviceID, reason string) string {
	path := m.paths[deviceID]
	if path == nil || len(path.lights) == 0 {
		return "no path lights to turn off"
	}
	if path.timer != nil {
		path.timer.Stop()
		path.timer = nil
	}
	path.gen++

	var errs []error
	for _, entityID := range path.lights {
		if err := m.haClient.CallService(m.ctx, "light", "turn_off", map[string]interface{}{
			"entity_id": entityID,
		}); err != nil {
			errs = append(errs, fmt.Errorf("turn_off %s: %w", entityID, err))
		}
	}
	lights := path.lights
	path.lights = nil
	if err := errors.Join(errs...); err != nil {
		m.logger.Error("Path lights turn_off failed", zap.String("reason", reason), zap.Error(err))
		return fmt.Sprintf("turning off %v (%s) failed: %v", lights, reason, err)
	}
	m.logger.Info("Path lights off", zap.Strings("lights", lights), zap.String("reason", reason))
	return fmt.Sprintf("turned off %v (%s)", lights, reason)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
