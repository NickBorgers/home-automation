package tv

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"homeautomation/internal/bravia"
	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"

	"go.uber.org/zap"
)

// TV and Hue Sync box brightness follow the TV room's target lux (see docs/flows/TV_BRIGHTNESS.md).
// The lighting plugin publishes that target as tvRoomTargetLux. Both devices map it to their own
// range on a log scale, because the eye sees brightness roughly logarithmically.
const (
	// SyncBoxBrightnessEntity is the Hue Sync box brightness (1-100).
	SyncBoxBrightnessEntity = "number.sync_box_brightness"

	// BrightnessDarkLux and BrightnessBrightLux span the Living room target (curve × lux_scale 0.5):
	// 2.5 lux at night, 150 lux by day. At or below the dark end both devices are at their dimmest.
	BrightnessDarkLux   = 2.5
	BrightnessBrightLux = 150.0

	// TVBrightnessDarkFraction is the TV's dimmest setting, as a share of the range the TV reports
	// (0-50 on the A80K, so 15). GUESS: tune by eye.
	TVBrightnessDarkFraction = 0.3

	// syncBoxMinStep is the smallest sync box change worth a command, in percentage points.
	syncBoxMinStep = 2

	// syncBoxManualTolerance absorbs rounding in the sync box's reported value.
	syncBoxManualTolerance = 1

	// BrightnessPowerOnDelay is how long to wait after the TV turns on. The Bravia API answers
	// "not power-on" for a few seconds while the panel starts.
	BrightnessPowerOnDelay = 10 * time.Second

	// braviaCallTimeout bounds one read-then-write against the TV.
	braviaCallTimeout = 25 * time.Second
)

// PictureClient reads and sets the TV's picture brightness. *bravia.Client implements it.
type PictureClient interface {
	GetBrightness(ctx context.Context) (bravia.Setting, error)
	SetBrightness(ctx context.Context, value int) error
}

// deviceBrightness is what the plugin knows about one device's brightness.
type deviceBrightness struct {
	held       bool
	heldReason string
	// last is the value the plugin last sent, or the value it found and accepted. A different
	// reading later means a person changed it. Nil means unknown (after start or power-on).
	last *int
}

// brightnessControl holds the TV and sync box brightness state. mu serializes every apply, so the
// TV never gets two overlapping read-then-write sequences.
type brightnessControl struct {
	mu           sync.Mutex
	client       PictureClient
	powerOnDelay time.Duration
	// powerOnGen is incremented each time the TV turns on, so a delayed goroutine from a prior
	// power-on cycle can detect that it has been superseded and exit without clearing state.
	powerOnGen uint64
	tv         deviceBrightness
	syncBox    deviceBrightness
}

// SetPictureClient enables TV brightness control. Without it, only the sync box follows the target.
func (m *Manager) SetPictureClient(c PictureClient) {
	m.bright.mu.Lock()
	defer m.bright.mu.Unlock()
	m.bright.client = c
}

// SetBrightnessPowerOnDelay sets how long to wait after the TV turns on before setting brightness.
func (m *Manager) SetBrightnessPowerOnDelay(d time.Duration) {
	m.bright.mu.Lock()
	defer m.bright.mu.Unlock()
	m.bright.powerOnDelay = d
}

// handleRoomBrightnessChange runs after lighting publishes a new curve output. At that point its
// target lux has also been published. State handlers run on the publisher's goroutine, so the slow
// TV call runs on its own.
func (m *Manager) handleRoomBrightnessChange(_ string, _, _ interface{}) {
	go m.applyBrightness("target_lux")
}

func (m *Manager) handleSyncBoxLightSyncChange(_ string, _, newState *ha.State) {
	if newState != nil && newState.State == "on" {
		go m.applyBrightness("sync_active")
	}
}

// brightnessOnPowerOn starts a fresh session when the TV turns on: holds and remembered values
// are cleared, then both devices are set after the panel has had time to start.
//
// If the TV turns off and on again within the power-on delay, two goroutines are outstanding.
// powerOnGen lets the earlier goroutine detect it has been superseded and exit without clearing
// the state that the later goroutine (and any intervening lux handler) may have already set.
func (m *Manager) brightnessOnPowerOn() {
	m.bright.mu.Lock()
	delay := m.bright.powerOnDelay
	m.bright.powerOnGen++
	gen := m.bright.powerOnGen
	m.bright.mu.Unlock()
	go func() {
		if delay > 0 {
			m.sleepFunc(delay)
		}
		m.bright.mu.Lock()
		defer m.bright.mu.Unlock()
		if m.bright.powerOnGen != gen {
			return // superseded by a later power-on
		}
		m.bright.tv = deviceBrightness{}
		m.bright.syncBox = deviceBrightness{}
		m.applyBrightnessLocked("tv_power_on")
	}()
}

// applyBrightness sets the TV and sync box brightness for the current target.
func (m *Manager) applyBrightness(trigger string) {
	m.bright.mu.Lock()
	defer m.bright.mu.Unlock()
	m.applyBrightnessLocked(trigger)
}

// applyBrightnessLocked does the work of applyBrightness. Caller holds m.bright.mu.
func (m *Manager) applyBrightnessLocked(trigger string) {
	rec := shadowstate.TVBrightnessState{Trigger: trigger}
	defer func() {
		rec.LastUpdate = m.timeNow()
		m.shadowTracker.UpdateBrightness(rec)
		m.logger.Debug("TV brightness decision",
			zap.String("trigger", trigger),
			zap.Float64("target_lux", rec.TargetLux),
			zap.String("decision", rec.Decision),
			zap.String("tv", rec.TV.Decision),
			zap.String("sync_box", rec.SyncBox.Decision))
	}()

	m.tvRemoteMu.RLock()
	on := m.tvRemoteOn
	m.tvRemoteMu.RUnlock()
	if !on {
		rec.Decision = "TV off"
		return
	}
	lux, err := m.stateManager.GetNumber("tvRoomTargetLux")
	if err != nil || lux <= 0 {
		rec.Decision = "no target lux yet"
		return
	}
	frac := brightnessFraction(lux)
	rec.TargetLux, rec.Fraction = lux, frac
	rec.Decision = "applied"
	rec.SyncBox = m.applySyncBoxBrightness()
	rec.TV = m.applyTVBrightness(frac)
}

// brightnessFraction places lux on a log scale between the dark and bright ends: 0 at or below
// BrightnessDarkLux, 1 at or above BrightnessBrightLux.
func brightnessFraction(lux float64) float64 {
	if lux <= BrightnessDarkLux {
		return 0
	}
	f := math.Log(lux/BrightnessDarkLux) / math.Log(BrightnessBrightLux/BrightnessDarkLux)
	return math.Min(1, f)
}

// applySyncBoxBrightness sets number.sync_box_brightness through Home Assistant.
func (m *Manager) applySyncBoxBrightness() shadowstate.DeviceBrightnessState {
	dev := &m.bright.syncBox
	pct, err := m.stateManager.GetNumber("tvRoomBrightnessPct")
	if err != nil {
		return shadowstate.DeviceBrightnessState{Decision: "no curve brightness yet"}
	}
	goal := syncBoxBrightnessGoal(pct)
	out := shadowstate.DeviceBrightnessState{Goal: goal}

	syncState, err := m.haClient.GetState(SyncBoxLightSyncEntity)
	if err != nil || syncState == nil || syncState.State != "on" {
		out.Decision = "sync inactive"
		return out
	}

	st, err := m.haClient.GetState(SyncBoxBrightnessEntity)
	if err != nil || st == nil {
		out.Decision = "sync box brightness unknown"
		return out
	}
	v, perr := strconv.ParseFloat(st.State, 64)
	if perr != nil {
		out.Decision = fmt.Sprintf("sync box brightness is %q", st.State)
		return out
	}
	cur := int(math.Round(v))
	out.Current = cur
	return m.decideAndSend(dev, out, cur, syncBoxManualTolerance, syncBoxMinStep, "sync box", func() error {
		return m.haClient.CallService(m.ctx, "number", "set_value", map[string]interface{}{
			"entity_id": SyncBoxBrightnessEntity,
			"value":     goal,
		})
	})
}

func syncBoxBrightnessGoal(pct float64) int {
	return int(math.Max(1, math.Min(100, math.Round(pct))))
}

// applyTVBrightness reads the TV's brightness and range, then sets it.
func (m *Manager) applyTVBrightness(frac float64) shadowstate.DeviceBrightnessState {
	dev := &m.bright.tv
	out := shadowstate.DeviceBrightnessState{}
	if m.bright.client == nil {
		out.Decision = "not configured (BRAVIA_IP or BRAVIA_PSK unset)"
		return out
	}
	if dev.held {
		// Goal is not set here: computing it requires a live read of the TV's range (Min/Max),
		// which costs a network round-trip. Shadow state shows Goal=0 while held.
		out.Held, out.HeldReason, out.Decision = true, dev.heldReason, "held: manual change"
		return out
	}
	ctx, cancel := context.WithTimeout(m.ctx, braviaCallTimeout)
	defer cancel()
	s, err := m.bright.client.GetBrightness(ctx)
	if errors.Is(err, bravia.ErrTurnedOff) {
		out.Decision = "TV screen not ready"
		return out
	}
	if err != nil {
		m.logger.Warn("Failed to read TV brightness", zap.Error(err))
		out.Decision = "read failed: " + err.Error()
		return out
	}
	share := TVBrightnessDarkFraction + (1-TVBrightnessDarkFraction)*frac
	out.Goal = s.Min + int(math.Round(share*float64(s.Max-s.Min)))
	out.Current = s.Current
	goal := out.Goal
	return m.decideAndSend(dev, out, s.Current, 0, 1, "TV", func() error {
		return m.bright.client.SetBrightness(ctx, goal)
	})
}

// decideAndSend applies the shared rules: hold on a manual change, skip small changes, respect
// read-only mode, then send and remember the value.
func (m *Manager) decideAndSend(dev *deviceBrightness, out shadowstate.DeviceBrightnessState, cur, tolerance,
	minStep int, name string, send func() error) shadowstate.DeviceBrightnessState {
	if !dev.held && dev.last != nil && absInt(cur-*dev.last) > tolerance {
		dev.held = true
		dev.heldReason = fmt.Sprintf("found %d, last set %d", cur, *dev.last)
		m.logger.Info("Manual brightness change, holding until the TV turns off",
			zap.String("device", name), zap.Int("current", cur), zap.Int("last_set", *dev.last))
	}
	if dev.held {
		out.Held, out.HeldReason, out.Decision = true, dev.heldReason, "held: manual change"
		return out
	}
	if absInt(out.Goal-cur) < minStep {
		if dev.last == nil {
			dev.last = &cur
		}
		out.Decision = "no change"
		return out
	}
	if m.readOnly {
		dev.last = &cur
		out.Decision = fmt.Sprintf("would set %d (read-only)", out.Goal)
		m.logger.Info("READ-ONLY: Would set brightness", zap.String("device", name), zap.Int("value", out.Goal))
		return out
	}
	if err := send(); err != nil {
		if errors.Is(err, bravia.ErrTurnedOff) {
			// The display is off. That is not a fault: the next lux change tries again.
			m.logger.Debug("TV display is off, brightness not set", zap.String("device", name))
			out.Decision = "TV screen not ready"
			return out
		}
		m.logger.Warn("Failed to set brightness", zap.String("device", name), zap.Error(err))
		out.Decision = "send failed: " + err.Error()
		return out
	}
	goal := out.Goal
	dev.last = &goal
	out.Decision = fmt.Sprintf("set %d", goal)
	m.logger.Info("Set brightness from room target lux",
		zap.String("device", name), zap.Int("from", cur), zap.Int("to", goal))
	return out
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
