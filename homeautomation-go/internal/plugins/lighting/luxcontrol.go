package lighting

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"

	"go.uber.org/zap"
)

// Lux-target lighting for the ceiling cans. Design: docs/flows/LUX_LIGHTING.md.

const (
	// luxTransition is the transition on routine lux commands that dim, turn off, or only
	// change color temperature, so changes are not visible as steps.
	luxTransition = 60 * time.Second
	// luxTransitionUp is the transition on lux commands that raise brightness. A room should
	// brighten quickly when its target rises, and dim gently when it falls.
	luxTransitionUp = luxTransition / 2
	// luxDefaultTick is the control loop period when tick_seconds is not set.
	luxDefaultTick = 60 * time.Second
	// luxDefaultSmoothing is the outdoor smoothing window when smoothing_minutes is not set.
	luxDefaultSmoothing = 5 * time.Minute
	// luxDefaultGuestFactor multiplies the target lux when increase_brightness_if_true holds.
	luxDefaultGuestFactor = 1.5
	// fadeReportLag is how late a state report may describe the lights. A report may show where
	// a fade was this long ago, or the level from just before the command.
	fadeReportLag = 1 * time.Second
	// manualBrightnessTolerance and manualKelvinTolerance bound how far the cans may sit past
	// the commanded values before the change counts as manual.
	manualBrightnessTolerance = 5.0
	manualKelvinTolerance     = 150
	// manualReverseBrightnessTolerance and manualReverseKelvinTolerance bound how far a report
	// may sit behind a fade, against its direction, before the change counts as manual. They are
	// small: a fade never moves backward, so a move against it is a person.
	manualReverseBrightnessTolerance = 1.5
	manualReverseKelvinTolerance     = 50
	// luxHoldConfirmDelay is how long after an off-path brightness or color report the plugin reads
	// the cans again before it holds the room. A light group's report can arrive late or out of
	// order while overlapping fades settle. A person's change stays off the fade path.
	luxHoldConfirmDelay = 5 * time.Second
	// luxHoldConfirmReads is the most re-reads made after the fade has ended while the cans are
	// still moving. When the last one is still off the fade path, the room is held.
	luxHoldConfirmReads = 4
	// luxFadeSettleMargin is how long after the latest commanded transition ends the cans may still
	// be settling. Until then an off-path report that lies within the range the running fades span
	// is not held: a group's brightness or color can lag a fade, most of all when fades overlap.
	// The confirmation waits and then compares the light with the final commanded values.
	luxFadeSettleMargin = 5 * time.Second
	// luxFreshReportWait is how long after the latest commanded transition ends a hold check waits
	// for a fresh report: one that arrived after the fade ended. A bridge can report a light group
	// minutes late, so a reading that arrived during the fade may still show a level from early in
	// it. Such a stale reading never holds the room. When no fresh report arrives in this time,
	// the check gives up without holding and the loop resumes.
	luxFreshReportWait = 3 * time.Minute
)

// luxCommand is the last command sent (or, in log_only/READ_ONLY mode, that would be sent) to a room's cans.
type luxCommand struct {
	at         time.Time
	transition time.Duration
	on         bool
	pct        float64
	kelvin     int
	// Where the fade starts: the level the plugin expected the light at when it sent the command.
	// fromKelvin is 0 when unknown (the light was off, or is brightness-only).
	fromOn     bool
	fromPct    float64
	fromKelvin int
	// Follower commands only: the cans values the strip was computed from, and the strip's
	// estimated color temperature from its cct_curve (0 without a curve).
	cansPct         float64
	cansKelvin      int
	cctFloorPct     float64
	cctCeilingPct   float64
	estimatedKelvin int
}

// levelAt returns the brightness percent and color temperature the light shows at t, on a
// straight-line fade from the start level to the commanded one. An off light is at 0%.
// The color temperature is the commanded one when the start color temperature is unknown.
func (c *luxCommand) levelAt(t time.Time) (float64, int) {
	frac := 1.0
	if c.transition > 0 {
		frac = math.Max(0, math.Min(1, t.Sub(c.at).Seconds()/c.transition.Seconds()))
	}
	from, to := 0.0, 0.0
	if c.fromOn {
		from = c.fromPct
	}
	if c.on {
		to = c.pct
	}
	kelvin := c.kelvin
	if c.fromKelvin != 0 && c.kelvin != 0 {
		kelvin = c.fromKelvin + int(math.Round(float64(c.kelvin-c.fromKelvin)*frac))
	}
	return from + (to-from)*frac, kelvin
}

// luxRoomState is the per-room control state.
type luxRoomState struct {
	cmd               *luxCommand
	previous          *luxCommand            // prior command, retained until its last delayed report can arrive
	followers         map[string]*luxCommand // last command per follower light
	previousFollowers map[string]*luxCommand // prior command per follower, for overlapping fade reports
	// held is the room's manual hold. A hand change to the cans or to a follower strip sets it.
	// It is stored in the room's hold helper (luxHoldHelper) so that it survives a restart.
	held       bool
	heldSince  time.Time
	heldReason string
	// checking is set while an off-path brightness or color report waits for a fresh read of the
	// cans (confirmLuxHold). The loop sends nothing meanwhile, so a person's change is kept.
	checking *luxHoldCheck
	// lastReportAt is when the latest report from the cans arrived, on the plugin's clock. A hold
	// check uses it to tell a fresh reading (arrived after the fade ended) from a stale one.
	lastReportAt time.Time
}

// luxHoldCheck is a pending confirmation of a possible manual change to the cans.
type luxHoldCheck struct {
	timer  clock.Timer
	reason string    // why the first report looked manual
	last   *ha.State // the most recent report or read
	reads  int       // fresh reads made after the fade ended
	// stale is the latest reading that arrived before the fade ended, or nil. A later fresh
	// reading that is no farther from the commanded values may be a light group whose members
	// have not all reported yet, so the check keeps waiting for them.
	stale *ha.State
	// skipped is set when an evaluation (a tick, a presence or condition change) was skipped
	// during the check. A check that clears runs it then, not at the next tick.
	skipped bool
}

// cancelHoldCheck drops a pending confirmation. Caller must hold luxMu.
func (st *luxRoomState) cancelHoldCheck() {
	if st.checking != nil {
		st.checking.timer.Stop()
		st.checking = nil
	}
}

// luxHoldHelper returns the Home Assistant input_boolean that stores a room's manual hold,
// for example input_boolean.lux_hold_kitchen for the Kitchen.
func luxHoldHelper(room *RoomConfig) string {
	slug := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		}
		return '_'
	}, room.HueGroup)
	return "input_boolean.lux_hold_" + slug
}

// setLuxHoldHelper stores a room's hold in its helper. A failure is logged: the hold still works
// in memory, but it will not survive a restart. Caller must not hold luxMu.
func (m *Manager) setLuxHoldHelper(room *RoomConfig, held bool) {
	if m.readOnly {
		return
	}
	service := "turn_off"
	if held {
		service = "turn_on"
	}
	helper := luxHoldHelper(room)
	if err := m.haClient.CallService(m.ctx, "input_boolean", service, map[string]interface{}{"entity_id": helper}); err != nil {
		m.logger.Warn("Failed to store manual hold in Home Assistant",
			zap.String("room", room.HueGroup), zap.String("entity_id", helper), zap.Bool("held", held), zap.Error(err))
	}
}

// restoreLuxHold reads a room's stored hold at startup. The hold comes back only while the
// room's conditions still turn it on. Otherwise an event that ends a hold (everyone asleep,
// nobody home, the room off) happened while the app was down, so the stored hold is cleared.
// Assumes: called from startLux before subHelper.CaptureInitialInputs(), so the state manager
// must already hold condition variables (e.g. isAnyoneHomeAndAwake). Start() guarantees this:
// it populates the state manager from HA before calling startLux().
func (m *Manager) restoreLuxHold(room *RoomConfig) {
	helper := luxHoldHelper(room)
	st, err := m.haClient.GetState(helper)
	if err != nil || st == nil {
		m.logger.Warn("Manual hold helper not found; holds in this room will not survive a restart",
			zap.String("room", room.HueGroup), zap.String("entity_id", helper), zap.Error(err))
		return
	}
	if st.State != "on" {
		return
	}
	if action, _ := m.evaluateConditions(room); action != "on" {
		m.logger.Info("Clearing stored manual hold: the room is not on",
			zap.String("room", room.HueGroup), zap.String("action", action))
		m.setLuxHoldHelper(room, false)
		return
	}
	m.luxMu.Lock()
	rs := m.luxRoom(room.HueGroup)
	rs.held, rs.heldSince, rs.heldReason = true, st.LastChanged, "restored from "+helper
	m.luxMu.Unlock()
	m.logger.Info("Restored manual hold", zap.String("room", room.HueGroup), zap.Time("held_since", st.LastChanged))
}

// handleLuxHoldHelperChange follows a person's change to the hold helper in Home Assistant:
// off releases the room, on holds it. The plugin's own writes find the hold already matching.
func (m *Manager) handleLuxHoldHelperChange(room *RoomConfig, newState *ha.State) {
	if newState == nil || m.readOnly {
		return
	}
	if newState.State != "on" && newState.State != "off" {
		return
	}
	want := newState.State == "on"
	m.luxMu.Lock()
	st := m.luxRoom(room.HueGroup)
	if st.held == want {
		m.luxMu.Unlock()
		return
	}
	if want {
		st.held, st.heldSince, st.heldReason = true, m.clock.Now(), "hold helper turned on"
		m.luxMu.Unlock()
		m.logger.Info("Manual hold set from Home Assistant", zap.String("room", room.HueGroup))
		return
	}
	m.luxMu.Unlock()
	m.clearLuxHold(room, "hold helper turned off")
	m.luxEvaluate(room, luxHoldHelper(room))
}

// SetAnchorResolver sets the source of sun and schedule times for the target curve.
// Without one, lux control stays off.
func (m *Manager) SetAnchorResolver(r AnchorResolver) {
	m.anchorResolver = r
}

// luxManaged reports whether the room has lux control in any mode.
func luxManaged(room *RoomConfig) bool { return room.LuxControl != nil }

// luxActive reports whether the plugin drives this room's cans.
func luxActive(room *RoomConfig) bool {
	return room.LuxControl != nil && room.LuxControl.Mode == LuxModeActive
}

// luxRoom returns the room's control state, creating it on first use. Caller must hold luxMu.
func (m *Manager) luxRoom(name string) *luxRoomState {
	st, ok := m.luxRooms[name]
	if !ok {
		st = &luxRoomState{
			followers:         map[string]*luxCommand{},
			previousFollowers: map[string]*luxCommand{},
		}
		m.luxRooms[name] = st
	}
	return st
}

// startLux subscribes to the cans of active rooms and starts the control loop.
func (m *Manager) startLux() error {
	var rooms []*RoomConfig
	for i := range m.config.Rooms {
		if luxManaged(&m.config.Rooms[i]) {
			rooms = append(rooms, &m.config.Rooms[i])
		}
	}
	if len(rooms) == 0 || m.config.LuxCurve == nil {
		return nil
	}
	if m.anchorResolver == nil {
		m.logger.Warn("Lux control configured but no anchor resolver set; lux control disabled")
		return nil
	}
	window := luxDefaultSmoothing
	if mins := m.config.LuxCurve.SmoothingMinutes; mins > 0 {
		window = time.Duration(mins) * time.Minute
	}
	m.luxOutdoor = &smoother{window: window}
	// Publish darkness synchronously so subscribers never observe the stale helper value at startup.
	m.sampleOutdoor(m.clock.Now())

	for _, room := range rooms {
		if !luxActive(room) {
			continue
		}
		r := room
		m.restoreLuxHold(r)
		if err := m.subHelper.SubscribeToEntity(luxHoldHelper(r), func(_ string, _, newState *ha.State) {
			m.handleLuxHoldHelperChange(r, newState)
		}); err != nil {
			return fmt.Errorf("failed to subscribe to %s: %w", luxHoldHelper(r), err)
		}
		if err := m.subHelper.SubscribeToEntity(r.LuxControl.Cans, func(_ string, _, newState *ha.State) {
			m.handleCansChange(r, newState)
		}); err != nil {
			return fmt.Errorf("failed to subscribe to %s: %w", r.LuxControl.Cans, err)
		}
		for _, f := range r.LuxControl.FollowerLights {
			if err := m.subHelper.SubscribeToEntity(f.Entity, func(_ string, _, newState *ha.State) {
				m.handleFollowerChange(r, newState)
			}); err != nil {
				return fmt.Errorf("failed to subscribe to %s: %w", f.Entity, err)
			}
		}
	}

	tick := luxDefaultTick
	if s := m.config.LuxCurve.TickSeconds; s > 0 {
		tick = time.Duration(s) * time.Second
	}
	m.luxStop = make(chan struct{})
	m.luxDone = make(chan struct{})
	go m.runLuxLoop(tick)
	return nil
}

// stopLux stops the control loop and waits for it to exit.
func (m *Manager) stopLux() {
	if m.luxStop == nil {
		return
	}
	close(m.luxStop)
	<-m.luxDone
	m.luxStop = nil
}

func (m *Manager) runLuxLoop(tick time.Duration) {
	defer close(m.luxDone)
	ticker := m.clock.NewTicker(tick)
	defer ticker.Stop()
	m.luxTick()
	for {
		select {
		case <-m.luxStop:
			return
		case <-m.ctx.Done():
			return
		case <-ticker.C():
			m.luxTick()
		}
	}
}

// luxTick evaluates every lux-managed room once.
func (m *Manager) luxTick() {
	for i := range m.config.Rooms {
		room := &m.config.Rooms[i]
		if luxManaged(room) {
			m.luxEvaluate(room, "lux_tick")
		}
	}
}

// sampleOutdoor reads the outdoor sensor into the smoother. It returns the smoothed lux,
// and false when the reading is missing. A missing reading clears the smoother so old
// data cannot hide the gap; the caller then treats daylight as 0.
func (m *Manager) sampleOutdoor(now time.Time) (float64, bool) {
	st, err := m.haClient.GetState(m.config.LuxCurve.OutdoorSensor)

	m.luxMu.Lock()
	if err != nil || st == nil {
		m.luxOutdoor.reset()
		m.luxMu.Unlock()
		m.updateDarkOutside(0, false)
		return 0, false
	}
	v, perr := strconv.ParseFloat(st.State, 64)
	if perr != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		m.luxOutdoor.reset()
		m.luxMu.Unlock()
		m.updateDarkOutside(0, false)
		return 0, false
	}
	m.luxOutdoor.add(now, v)
	smoothed, ok := m.luxOutdoor.mean()
	m.luxMu.Unlock()
	m.updateDarkOutside(smoothed, ok)
	return smoothed, ok
}

// updateDarkOutside does a GetBool → decide → SetBool without a lock. luxSensorOffline uses
// atomic.Bool because sampleOutdoor is also called from debounce-timer goroutines via luxEvaluate.
func (m *Manager) updateDarkOutside(outdoorLux float64, haveOutdoor bool) {
	darkBelow := m.config.LuxCurve.darkBelowLux()
	lightAbove := m.config.LuxCurve.lightAboveLux()
	isDark, _ := m.stateManager.GetBool("isDarkOutside")
	source := "smoothed_outdoor_lux"

	if haveOutdoor {
		m.luxSensorOffline.Store(false)
		if outdoorLux < darkBelow {
			isDark = true
		} else if outdoorLux > lightAbove {
			isDark = false
		}
	} else {
		dayPhase, err := m.stateManager.GetString("dayPhase")
		isDark = err == nil && (dayPhase == "dusk" || dayPhase == "winddown" || dayPhase == "night")
		source = "day_phase_fallback"
		if !m.luxSensorOffline.Load() {
			m.logger.Warn("Outdoor illuminance unavailable; using day phase for darkness",
				zap.String("day_phase", dayPhase), zap.Bool("is_dark_outside", isDark))
			m.luxSensorOffline.Store(true)
		}
	}

	if err := m.stateManager.SetBool("isDarkOutside", isDark); err != nil {
		m.logger.Error("Failed to publish dark outside state", zap.Error(err))
	}
	m.shadowTracker.UpdateDarkOutside(shadowstate.DarkOutsideState{
		IsDark: isDark, OutdoorLux: outdoorLux, Source: source, DarkBelow: darkBelow, LightAbove: lightAbove,
	})
}

// luxEvaluate runs one control step for a room: compute the target, work out what the cans
// must supply, record the decision in shadow state, and send a command when one is due.
func (m *Manager) luxEvaluate(room *RoomConfig, trigger string) {
	lc := room.LuxControl
	now := m.clock.Now()
	rec := shadowstate.LuxControlState{Mode: lc.Mode}

	m.luxMu.Lock()
	st := m.luxRoom(room.HueGroup)
	rec.Held, rec.HeldSince, rec.HeldReason = st.held, st.heldSince, st.heldReason
	checking := st.checking != nil
	if checking {
		st.checking.skipped = true // confirmLuxHold evaluates again if the check clears
	}
	m.luxMu.Unlock()

	finish := func(decision string) {
		rec.Decision = decision
		rec.Followers = m.followerStates(room)
		m.shadowTracker.RecordLuxControl(room.HueGroup, rec)
		m.logger.Debug("Lux control decision",
			zap.String("room", room.HueGroup),
			zap.String("mode", lc.Mode),
			zap.String("trigger", trigger),
			zap.String("decision", decision),
			zap.Float64("target_lux", rec.TargetLux),
			zap.Float64("needed_lux", rec.NeededLux),
			zap.Float64("brightness_pct", rec.BrightnessPct))
	}

	points, err := resolveCurve(m.config.LuxCurve, now, m.anchorResolver)
	if err != nil {
		finish("curve error, lights left alone: " + err.Error())
		return
	}
	target, ok := targetAt(points, now)
	if !ok {
		finish("curve has no points")
		return
	}
	scale := lc.LuxScale
	if scale <= 0 {
		scale = 1
	}
	rec.TargetLux = target.Lux * scale
	rec.TargetSource = "lux_curve"
	kelvin := target.Kelvin
	if !lc.HasColorTemp() {
		kelvin = 0 // brightness-only light: no kelvin is sent, compared or recorded
	}
	rec.TargetKelvin = kelvin
	rec.CurvePoint = fmt.Sprintf("%s -> %s @ %.2f", target.From, target.To, target.Pos)
	if p := lc.PresenceOverride; p != nil {
		dayPhase, phaseErr := m.stateManager.GetString("dayPhase")
		phaseMatches := false
		for _, phase := range p.Phases {
			if phase == dayPhase {
				phaseMatches = true
				break
			}
		}
		occupied, err := m.stateManager.GetBool(p.Variable)
		if phaseErr == nil && err == nil && phaseMatches && occupied {
			// Multiply the curve's cans brightness, then convert back to lux so daylight still counts.
			pct := luxToBrightness(lc.Calibration, rec.TargetLux) * p.BrightnessMultiplierPct / 100
			rec.TargetLux = brightnessToLux(lc.Calibration, math.Min(100, pct))
			rec.TargetSource = "presence_override"
		}
	}

	if m.luxGuestsBoost(room) {
		factor := lc.GuestFactor
		if factor <= 0 {
			factor = luxDefaultGuestFactor
		}
		rec.TargetLux *= factor
	}
	if lc.TVRoom {
		// The TV picture follows the target even while the room is in skip because Hue Sync owns the lights.
		m.publishTVRoomTargetLux(rec.TargetLux)
	}

	outdoor, haveOutdoor := m.sampleOutdoor(now)
	rec.OutdoorLux = outdoor
	rec.DaylightEstimate = outdoor * lc.DaylightFactor
	rec.NeededLux = rec.TargetLux - rec.DaylightEstimate
	if lc.TVRoom {
		// Hue Sync gets the curve's calibrated, post-ambient percentage even while the cans are skipped.
		m.publishTVRoomBrightnessPct(luxToBrightness(lc.Calibration, rec.NeededLux))
	}
	note := ""
	if !haveOutdoor {
		note = " (outdoor sensor missing; daylight treated as 0)"
	}

	if rec.Held {
		finish("held: manual change, lights left alone" + note)
		return
	}
	if checking {
		finish("checking a possible manual change, lights left alone" + note)
		return
	}
	if action, _ := m.evaluateConditions(room); action != "on" {
		finish(fmt.Sprintf("room not on (conditions say %q)%s", action, note))
		return
	}

	m.luxMu.Lock()
	prev := st.cmd
	m.luxMu.Unlock()
	if prev == nil {
		// After a restart or reset the last command is gone. Lights that are already on are the
		// starting point, so the room fades from where it is instead of popping to the target.
		if prev = m.adoptLuxState(room, now, kelvin); prev != nil {
			m.luxMu.Lock()
			st.cmd = prev
			m.luxMu.Unlock()
		}
	}

	wantOn := shouldBeOn(lc.Calibration, rec.NeededLux, prev != nil && prev.on)
	cmd := &luxCommand{at: now, transition: luxTransition, on: wantOn, kelvin: kelvin}
	if prev != nil && prev.on {
		cmd.fromOn = true
		cmd.fromPct, cmd.fromKelvin = prev.levelAt(now)
	}
	if wantOn {
		cmd.pct = math.Max(1, math.Min(100, math.Round(luxToBrightness(lc.Calibration, rec.NeededLux))))
		rec.GoalBrightnessPct = cmd.pct
		if prev != nil && prev.on {
			cmd.pct = clampStep(prev.pct, cmd.pct, maxBrightnessStep)
			if kelvin != 0 {
				cmd.kelvin = int(clampStep(float64(prev.kelvin), float64(kelvin), maxKelvinStep))
			}
		}
		rec.BrightnessPct = cmd.pct
	}
	// A rise of less than the minimum step is a color-only change (or rounding noise) and keeps
	// the long transition.
	if wantOn && cmd.fromOn && cmd.pct-cmd.fromPct >= minBrightnessDelta {
		cmd.transition = luxTransitionUp
	}
	if prev == nil || !prev.on {
		// Fast profile: a room that was dark lights up with a short transition, so entering a room is
		// quick. Long ones can fail on Hue.
		cmd.transition = maxReturnHomeTransition * time.Second
	}

	// For brightness-only rooms cmd.kelvin == prev.kelvin == 0, so the kelvin delta is always zero.
	if prev != nil && prev.on == cmd.on &&
		(!cmd.on || (math.Abs(prev.pct-cmd.pct) < minBrightnessDelta && abs(prev.kelvin-cmd.kelvin) < minKelvinDelta)) {
		m.luxDriveFollowers(room, st, cmd)
		finish("no change: below minimum step" + note)
		return
	}

	// Re-read under lock before sending: a handleCansChange between the first unlock and here
	// may have started a hold check. Sending now would overwrite the person's change, and the
	// confirming read would see our own command and clear the check without holding.
	m.luxMu.Lock()
	if st.checking != nil {
		st.checking.skipped = true
		m.luxMu.Unlock()
		finish("checking a possible manual change, lights left alone" + note)
		return
	}
	m.luxMu.Unlock()

	if !luxActive(room) || m.readOnly {
		m.luxMu.Lock()
		st.previous = st.cmd
		st.cmd = cmd
		m.luxMu.Unlock()
		m.luxDriveFollowers(room, st, cmd)
		finish(fmt.Sprintf("would send %s (%s mode, read-only=%t)%s", describeLuxCommand(cmd), lc.Mode, m.readOnly, note))
		return
	}

	if err := m.sendLuxCommand(room, cmd); err != nil {
		finish("send failed: " + err.Error() + note)
		return
	}
	m.luxMu.Lock()
	st.previous = st.cmd
	st.cmd = cmd
	m.luxMu.Unlock()
	m.luxDriveFollowers(room, st, cmd)
	finish("sent " + describeLuxCommand(cmd) + note)
}

// clampStep moves from toward goal by at most step.
func clampStep(from, goal, step float64) float64 {
	return from + math.Max(-step, math.Min(step, goal-from))
}

// adoptLuxState turns the cans' reported state into a starting command when the plugin has none
// (after a restart or reset). It returns nil when the cans are off or the report has no usable
// brightness, and the room is then treated as dark. A missing color temperature takes the
// target's, so it is not slewed. The command is stamped now with no transition, so manual-change
// detection compares later reports with what the lights were already showing.
func (m *Manager) adoptLuxState(room *RoomConfig, now time.Time, targetKelvin int) *luxCommand {
	s, err := m.haClient.GetState(room.LuxControl.Cans)
	if err != nil || s == nil || s.State != "on" {
		return nil
	}
	b, ok := numberAttr(s.Attributes, "brightness")
	if !ok || b <= 0 {
		return nil
	}
	cmd := &luxCommand{at: now, on: true, pct: math.Max(1, math.Min(100, b/255*100)), kelvin: targetKelvin}
	if room.LuxControl.HasColorTemp() {
		if k, ok := numberAttr(s.Attributes, "color_temp_kelvin"); ok && k > 0 {
			cmd.kelvin = int(k)
		}
	} else {
		cmd.kelvin = 0
	}
	return cmd
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func describeLuxCommand(c *luxCommand) string {
	if !c.on {
		return fmt.Sprintf("off, transition %s", c.transition)
	}
	if c.kelvin == 0 {
		return fmt.Sprintf("%.0f%%, transition %s", c.pct, c.transition)
	}
	return fmt.Sprintf("%.0f%% at %dK, transition %s", c.pct, c.kelvin, c.transition)
}

// luxGuestsBoost reports whether any increase_brightness_if_true variable is true.
func (m *Manager) luxGuestsBoost(room *RoomConfig) bool {
	for _, name := range room.GetIncreaseBrightnessIfTrueConditions() {
		if v, err := m.stateManager.GetBool(name); err == nil && v {
			return true
		}
	}
	return false
}

// sendLuxCommand sends the cans a light.turn_on or light.turn_off.
func (m *Manager) sendLuxCommand(room *RoomConfig, cmd *luxCommand) error {
	data := map[string]interface{}{
		"entity_id":  room.LuxControl.Cans,
		"transition": cmd.transition.Seconds(),
	}
	service := "turn_off"
	if cmd.on {
		service = "turn_on"
		data["brightness_pct"] = int(cmd.pct)
		if room.LuxControl.HasColorTemp() {
			data["color_temp_kelvin"] = cmd.kelvin
		}
	}
	m.logger.Info("Lux control sending command",
		zap.String("room", room.HueGroup),
		zap.String("entity_id", room.LuxControl.Cans),
		zap.String("service", service),
		zap.Any("data", data))
	return m.haClient.CallService(m.ctx, "light", service, data)
}

// handleCansChange checks every report from the cans the moment it arrives. While a
// brightness-or-color hold check is running (st.checking != nil), subsequent reports for the same
// room only record their arrival time (the check reads the light itself); only the first off-path
// report triggers the loop. During a fade, a report
// counts as manual when it is not on the fade's path (manualChangeReason). A brightness or color
// report off the path is confirmed by a fresh read before the room is held (confirmLuxHold),
// because a light group's report can arrive late or out of order while fades settle. The loop
// sends nothing while it waits. A turn-on or turn-off against the command holds at once. A
// turn-on after a commanded off is stabilized so the stale off transition cannot win.
func (m *Manager) handleCansChange(room *RoomConfig, newState *ha.State) {
	if newState == nil || m.readOnly || !luxActive(room) {
		return
	}
	action, _ := m.evaluateConditions(room)
	now := m.clock.Now()

	m.luxMu.Lock()
	st := m.luxRoom(room.HueGroup)
	// Every report counts toward freshness, also one that arrives while a check runs (the check
	// reads the light itself) or while another plugin owns the lights.
	st.lastReportAt = now
	// Hue Sync and other plugins own the lights when the room is not "on".
	if action != "on" || st.held || st.cmd == nil || st.checking != nil {
		m.luxMu.Unlock()
		return
	}
	cmd := st.cmd
	contraryTurnOn := !cmd.on && newState.State == "on"
	reason := cansChangeReason(room, st, newState, now)
	if reason == "" {
		m.luxMu.Unlock()
		return
	}
	if cmd.on && newState.State == "on" {
		// A brightness or color difference: confirm it with a fresh read before holding.
		check := &luxHoldCheck{reason: reason, last: newState}
		if !luxReportFresh(cmd, now) {
			check.stale = newState
		}
		m.scheduleLuxHoldCheck(room, st, check)
		m.luxMu.Unlock()
		m.logger.Info("Possible manual change; reading the cans again before holding",
			zap.String("room", room.HueGroup),
			zap.String("reason", reason),
			zap.Duration("delay", luxHoldConfirmDelay))
		return
	}
	st.held, st.heldSince, st.heldReason = true, now, reason
	var stabilizing *luxCommand
	if contraryTurnOn {
		stabilizing = &luxCommand{at: now, on: true, kelvin: cmd.kelvin}
		if b, ok := numberAttr(newState.Attributes, "brightness"); ok {
			stabilizing.pct = math.Max(1, math.Min(100, math.Round(b/255*100)))
		}
		if k, ok := numberAttr(newState.Attributes, "color_temp_kelvin"); ok && k > 0 {
			stabilizing.kelvin = int(k)
		}
		st.cmd = stabilizing
		st.previous = nil
	}
	m.luxMu.Unlock()
	m.logger.Info("Manual change detected; holding room",
		zap.String("room", room.HueGroup),
		zap.String("reason", reason))
	m.setLuxHoldHelper(room, true)
	if stabilizing != nil {
		if err := m.sendLuxCommand(room, stabilizing); err != nil {
			m.logger.Error("Failed to stabilize manual turn-on", zap.String("room", room.HueGroup), zap.Error(err))
		}
	}
}

// cansChangeReason is manualChangeReason for the cans, with the previous command's guard.
// Caller must hold luxMu.
func cansChangeReason(room *RoomConfig, st *luxRoomState, s *ha.State, now time.Time) string {
	reason := manualChangeReason(st.cmd, s, room.LuxControl.HasColorTemp(), now)
	// Suppress a false manual-hold when a delayed bridge report from the previous
	// fade arrives just after a new command is sent. The guard window is
	// previous.transition + fadeReportLag, which is correct when the lux loop
	// sends commands sequentially (new command arrives as the old one ends).
	// A mid-transition command override would leave the window open for the
	// remaining transition duration — today the lux loop never does this.
	if reason != "" && st.previous != nil &&
		now.Before(st.previous.at.Add(st.previous.transition+fadeReportLag)) &&
		manualChangeReason(st.previous, s, room.LuxControl.HasColorTemp(),
			st.previous.at.Add(fadeReportLag)) == "" {
		reason = ""
	}
	return reason
}

// scheduleLuxHoldCheck reads the cans again after luxHoldConfirmDelay. Caller must hold luxMu.
func (m *Manager) scheduleLuxHoldCheck(room *RoomConfig, st *luxRoomState, check *luxHoldCheck) {
	st.checking = check
	check.timer = m.clock.AfterFunc(luxHoldConfirmDelay, func() { m.confirmLuxHold(room, check) })
}

// confirmLuxHold reads the cans again after an off-path brightness or color report. Back on the
// fade path, the report was a late one from the plugin's own fade, and nothing is held. Still off
// the path and steady, it is a person's change, and the room is held. Still off the path but
// moving, the light may be lagging the fade or settling, so it reads again: as often as needed
// while the fade runs, then up to luxHoldConfirmReads times. While the latest transition (plus
// luxFadeSettleMargin) is still running, a read within the range the fades span is not held; the
// check waits and then compares the light with the final commanded values. A jump outside that
// range is decided at once. After the fade, a reading within that range holds only when it
// arrived after the fade ended (luxReportFresh): a bridge can report a light group minutes late.
// The check waits up to luxFreshReportWait for such a report and gives up without holding when
// none comes. When the read fails, the first report decides, so a person's change is not lost.
func (m *Manager) confirmLuxHold(room *RoomConfig, check *luxHoldCheck) {
	s, err := m.haClient.GetState(room.LuxControl.Cans)
	action, _ := m.evaluateConditions(room)
	now := m.clock.Now()

	m.luxMu.Lock()
	st := m.luxRoom(room.HueGroup)
	if st.checking != check {
		m.luxMu.Unlock() // cancelled: the hold was cleared or the command was forgotten
		return
	}
	st.checking = nil
	if st.held || st.cmd == nil || action != "on" {
		m.luxMu.Unlock()
		return
	}
	reason := check.reason
	if err == nil && s != nil {
		settled := luxFadeSettled(st.cmd, now)
		fresh := luxReportFresh(st.cmd, st.lastReportAt)
		if !fresh {
			check.stale = s
		}
		if reason = cansChangeReason(room, st, s, now); reason == "" {
			skipped := check.skipped
			m.luxMu.Unlock()
			m.logger.Info("Possible manual change not confirmed; the cans are on the fade path",
				zap.String("room", room.HueGroup), zap.String("first_reason", check.reason))
			if skipped {
				m.luxEvaluate(room, "hold check cleared")
			}
			return
		}
		within := s.State == "on" && withinRunningFades(st.cmd, st.previous, s, room.LuxControl.HasColorTemp())
		if within && !settled {
			// The fade is still running and the light is somewhere the fades pass through. Wait
			// for the fade to end before deciding. A jump outside that range is decided below.
			check.last = s
			m.scheduleLuxHoldCheck(room, st, check)
			m.luxMu.Unlock()
			return
		}
		waiting := now.Before(luxFadeEnd(st.cmd).Add(luxFreshReportWait))
		if within && !fresh {
			// The reading arrived before the fade ended, so it may show a level from early in the
			// fade. It is never enough to hold. Wait for a fresh report, and give up without
			// holding when none comes.
			if waiting {
				check.last = s
				m.scheduleLuxHoldCheck(room, st, check)
				m.luxMu.Unlock()
				return
			}
			skipped := check.skipped
			m.luxMu.Unlock()
			m.logger.Info("Possible manual change not confirmed; no fresh report from the cans after the fade",
				zap.String("room", room.HueGroup), zap.String("first_reason", check.reason),
				zap.Duration("waited", luxFreshReportWait))
			if skipped {
				m.luxEvaluate(room, "hold check gave up")
			}
			return
		}
		if within && waiting && check.stale != nil && luxReportConverging(st.cmd, check.stale, s) {
			// A light group's state is built from its members' reports. When a bridge reports the
			// members one at a time, minutes apart, a fresh group report may update only some of
			// them, so it can sit between the stale level and the commanded one. Such a fresh
			// report is not enough to hold while it is closer to the commanded values than the
			// stale reading: the check keeps waiting until the group reaches the fade path (no
			// hold) or luxFreshReportWait ends. By then every member has had time to report, so a
			// fresh reading still off the path is decided below. A fresh report that did not move
			// toward the commanded values confirms the stale level, so it is decided below at once.
			check.last = s
			m.scheduleLuxHoldCheck(room, st, check)
			m.luxMu.Unlock()
			return
		}
		if settled {
			check.reads++ // a group may lag a running fade by several seconds, so only reads after it count
		}
		if s.State == "on" && check.reads < luxHoldConfirmReads && luxReportMoved(check.last, s) {
			check.last = s
			m.scheduleLuxHoldCheck(room, st, check)
			m.luxMu.Unlock()
			return
		}
	}
	st.held, st.heldSince, st.heldReason = true, now, reason
	m.luxMu.Unlock()
	m.logger.Info("Manual change detected; holding room",
		zap.String("room", room.HueGroup),
		zap.String("reason", reason))
	m.setLuxHoldHelper(room, true)
}

// luxFadeEnd is when the command's transition ends.
func luxFadeEnd(cmd *luxCommand) time.Time { return cmd.at.Add(cmd.transition) }

// luxReportFresh reports whether a report that arrived at the given time is fresh: it arrived
// after the command's fade ended, give or take the report lag. A report from during the fade can
// show a level from early in it, so only a fresh one may be compared with the final commanded
// values. The fade end is a better test than the command time: a stale level from early in the
// fade arrives after the command, too.
func luxReportFresh(cmd *luxCommand, arrived time.Time) bool {
	return !arrived.Before(luxFadeEnd(cmd).Add(fadeReportLag))
}

// luxReportConverging reports whether report b is closer than report a to the command's final
// brightness or color temperature, and farther from neither, by more than the jitter a report
// may have. A light group whose members report one at a time moves toward the commanded values.
func luxReportConverging(cmd *luxCommand, a, b *ha.State) bool {
	closer, farther := false, false
	compare := func(key string, target, scale, tolerance float64) {
		va, okA := numberAttr(a.Attributes, key)
		vb, okB := numberAttr(b.Attributes, key)
		if !okA || !okB {
			return
		}
		d := math.Abs(vb*scale-target) - math.Abs(va*scale-target)
		closer = closer || d < -tolerance
		farther = farther || d > tolerance
	}
	compare("brightness", cmd.pct, 100.0/255, manualReverseBrightnessTolerance)
	if cmd.kelvin != 0 {
		compare("color_temp_kelvin", float64(cmd.kelvin), 1, manualReverseKelvinTolerance)
	}
	return closer && !farther
}

// luxFadeSettled reports whether the latest command's transition, plus luxFadeSettleMargin, has ended.
func luxFadeSettled(cmd *luxCommand, now time.Time) bool {
	return !now.Before(cmd.at.Add(cmd.transition + luxFadeSettleMargin))
}

// withinRunningFades reports whether a report lies within the range of brightness, and of color
// temperature, that the running fades pass through, with the normal tolerance. The range runs
// from each fade's start to its target. It covers the latest command, and the previous one when
// the latest was sent before the previous fade ended, since overlapping fades blend on the light.
// A report outside it is far off any fade, so it is not a light lagging the fade.
// Caller must hold luxMu.
func withinRunningFades(cmd, previous *luxCommand, s *ha.State, colorTemp bool) bool {
	cmds := []*luxCommand{cmd}
	if p := previous; p != nil && cmd.at.Before(p.at.Add(p.transition)) {
		cmds = append(cmds, p)
	}
	if b, ok := numberAttr(s.Attributes, "brightness"); ok {
		var ends []float64
		for _, c := range cmds {
			from, to := 0.0, 0.0
			if c.fromOn {
				from = c.fromPct
			}
			if c.on {
				to = c.pct
			}
			ends = append(ends, from, to)
		}
		if !withinRange(b/255*100, ends, manualBrightnessTolerance) {
			return false
		}
	}
	if !colorTemp {
		return true
	}
	if k, ok := numberAttr(s.Attributes, "color_temp_kelvin"); ok {
		var ends []float64
		for _, c := range cmds {
			for _, v := range []int{c.fromKelvin, c.kelvin} {
				if v != 0 { // 0 is an unknown color temperature
					ends = append(ends, float64(v))
				}
			}
		}
		if len(ends) > 0 && !withinRange(k, ends, manualKelvinTolerance) {
			return false
		}
	}
	return true
}

// withinRange reports whether v lies between the smallest and largest of ends, give or take tolerance.
func withinRange(v float64, ends []float64, tolerance float64) bool {
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, e := range ends {
		lo, hi = math.Min(lo, e), math.Max(hi, e)
	}
	return v >= lo-tolerance && v <= hi+tolerance
}

// luxReportMoved reports whether brightness or color temperature changed between two reports by
// more than the reverse tolerance, the jitter a report may have.
func luxReportMoved(a, b *ha.State) bool {
	ba, okA := numberAttr(a.Attributes, "brightness")
	bb, okB := numberAttr(b.Attributes, "brightness")
	if okA != okB || math.Abs(ba-bb)/255*100 > manualReverseBrightnessTolerance {
		return true
	}
	ka, okA := numberAttr(a.Attributes, "color_temp_kelvin")
	kb, okB := numberAttr(b.Attributes, "color_temp_kelvin")
	return okA != okB || math.Abs(ka-kb) > manualReverseKelvinTolerance
}

// handleFollowerChange is handleCansChange for a follower light: a report that is not on the
// path of what the plugin last sent it holds the whole room, the moment it arrives. A strip is
// part of its room. Followers are brightness-only, so color temperature is never compared.
func (m *Manager) handleFollowerChange(room *RoomConfig, newState *ha.State) {
	if newState == nil || m.readOnly || !luxActive(room) {
		return
	}
	if action, _ := m.evaluateConditions(room); action != "on" {
		return
	}
	now := m.clock.Now()

	m.luxMu.Lock()
	st := m.luxRoom(room.HueGroup)
	last := st.followers[newState.EntityID]
	if st.held || last == nil {
		m.luxMu.Unlock()
		return
	}
	reason := manualChangeReason(last, newState, false, now)
	if reason == "" {
		m.luxMu.Unlock()
		return
	}
	if newState.State == "on" && !luxFadeSettled(last, now) &&
		withinRunningFades(last, st.previousFollowers[newState.EntityID], newState, false) {
		m.luxMu.Unlock()
		return
	}
	reason = newState.EntityID + ": " + reason
	st.held, st.heldSince, st.heldReason = true, now, reason
	m.luxMu.Unlock()
	m.logger.Info("Manual change to follower light detected; holding room",
		zap.String("entity_id", newState.EntityID),
		zap.String("room", room.HueGroup),
		zap.String("reason", reason))
	m.setLuxHoldHelper(room, true)
}

// luxDriveFollowers sets each follower light from the cans command: floor + gain * cans percent,
// raised to the brightness its cct_curve needs for the cans' color temperature and capped at the
// brightest setting that is not cooler than it, or off when the cans are off. The minimum-step rule
// compares against the follower's own last command, so a strip is not told to move on every tick.
// In log_only and READ_ONLY mode the command is recorded and not sent.
func (m *Manager) luxDriveFollowers(room *RoomConfig, st *luxRoomState, cans *luxCommand) {
	send := luxActive(room) && !m.readOnly
	for _, f := range room.LuxControl.FollowerLights {
		next := &luxCommand{at: cans.at, transition: cans.transition, on: cans.on}
		if cans.on {
			next.pct = f.followerPct(cans.pct)
			next.cansPct, next.cansKelvin = cans.pct, cans.kelvin
			next.cctFloorPct = f.cctFloorPct(cans.kelvin)
			next.cctCeilingPct = f.cctCeilingPct(cans.kelvin)
			next.pct = math.Min(math.Round(next.cctCeilingPct), math.Max(next.pct, math.Round(next.cctFloorPct)))
			next.estimatedKelvin = f.estimateKelvin(next.pct)
		}
		m.luxMu.Lock()
		prev := st.followers[f.Entity]
		m.luxMu.Unlock()
		if prev == nil {
			if pct, on := m.luxLightLevel(f.Entity); on {
				next.fromOn, next.fromPct = true, pct
			}
		} else if prev.on {
			next.fromOn = true
			next.fromPct, _ = prev.levelAt(cans.at)
		}
		if !next.fromOn {
			next.transition = maxReturnHomeTransition * time.Second // a dark strip lights up quickly
		}
		if prev != nil && prev.on == next.on && (!next.on || math.Abs(prev.pct-next.pct) < minBrightnessDelta) {
			continue
		}
		if send {
			if err := m.sendFollowerCommand(room, f.Entity, next); err != nil {
				m.logger.Warn("Lux control follower command failed",
					zap.String("room", room.HueGroup), zap.String("entity_id", f.Entity), zap.Error(err))
				continue
			}
		}
		m.luxMu.Lock()
		st.previousFollowers[f.Entity] = prev
		st.followers[f.Entity] = next
		m.luxMu.Unlock()
	}
}

// luxLightLevel returns the light's brightness percent, and whether Home Assistant shows it on.
// A strip already lit after a restart keeps the cans' long transition instead of popping, and
// its fade starts from that brightness (100% when the report has none).
func (m *Manager) luxLightLevel(entity string) (float64, bool) {
	s, err := m.haClient.GetState(entity)
	if err != nil || s == nil || s.State != "on" {
		return 0, false
	}
	if b, ok := numberAttr(s.Attributes, "brightness"); ok {
		return b / 255 * 100, true
	}
	return 100, true
}

// sendFollowerCommand sends a follower light a light.turn_on (brightness only) or light.turn_off.
func (m *Manager) sendFollowerCommand(room *RoomConfig, entity string, cmd *luxCommand) error {
	data := map[string]interface{}{
		"entity_id":  entity,
		"transition": cmd.transition.Seconds(),
	}
	service := "turn_off"
	if cmd.on {
		service = "turn_on"
		data["brightness_pct"] = int(cmd.pct)
	}
	m.logger.Info("Lux control sending follower command",
		zap.String("room", room.HueGroup),
		zap.String("entity_id", entity),
		zap.String("service", service),
		zap.Any("data", data),
		zap.Float64("cans_pct", cmd.cansPct),
		zap.Int("target_kelvin", cmd.cansKelvin),
		zap.Float64("cct_floor_pct", cmd.cctFloorPct),
		zap.Float64("cct_ceiling_pct", cmd.cctCeilingPct),
		zap.Int("estimated_kelvin", cmd.estimatedKelvin))
	return m.haClient.CallService(m.ctx, "light", service, data)
}

// followerStates returns each follower's last commanded percent (0 when off) and hold for shadow state.
func (m *Manager) followerStates(room *RoomConfig) map[string]shadowstate.FollowerState {
	if len(room.LuxControl.FollowerLights) == 0 {
		return nil
	}
	m.luxMu.Lock()
	defer m.luxMu.Unlock()
	st := m.luxRoom(room.HueGroup)
	out := make(map[string]shadowstate.FollowerState, len(room.LuxControl.FollowerLights))
	for _, f := range room.LuxControl.FollowerLights {
		fs := shadowstate.FollowerState{}
		if c := st.followers[f.Entity]; c != nil && c.on {
			fs.Pct = c.pct
			fs.CansPct = c.cansPct
			fs.TargetKelvin = c.cansKelvin
			fs.CCTFloorPct = c.cctFloorPct
			// CCTCeilingPct needs an explicit guard: cctCeilingPct returns 100 (not 0) when there
			// is no curve, and 100 is non-zero so omitempty would not suppress it.
			if len(f.CCTCurve) > 0 {
				fs.CCTCeilingPct = c.cctCeilingPct
			}
			fs.EstimatedKelvin = c.estimatedKelvin
		}
		out[f.Entity] = fs
	}
	return out
}

// manualChangeReason compares a light's report, received at now, with the last command. It
// returns "" when the report is on the command's fade path. That path runs from where the fade
// was fadeReportLag ago (reports can be late) to the commanded value. A report may sit up to
// the small reverse tolerance behind that path, or up to the normal tolerance past the target.
// Anything else is a person. Reports are never ignored because a fade is running.
//
// One gap remains: when the light was off, its starting color temperature is unknown, so color
// temperature is not compared until that turn-on fade (maxReturnHomeTransition) has ended.
// Brightness is compared throughout.
func manualChangeReason(cmd *luxCommand, s *ha.State, colorTemp bool, now time.Time) string {
	// A report sent just before the command can arrive just after it.
	early := now.Before(cmd.at.Add(fadeReportLag))
	behindPct, behindKelvin := cmd.levelAt(now.Add(-fadeReportLag))
	switch s.State {
	case "on":
		if !cmd.on {
			if early && cmd.fromOn {
				return ""
			}
			return "cans turned on by hand"
		}
		if b, ok := numberAttr(s.Attributes, "brightness"); ok {
			pct := b / 255 * 100
			if !onFadePath(pct, behindPct, cmd.pct, manualReverseBrightnessTolerance, manualBrightnessTolerance) {
				return fmt.Sprintf("brightness %.0f%% differs from commanded %.0f%%", pct, cmd.pct)
			}
		}
		if !colorTemp {
			return ""
		}
		if cmd.fromKelvin == 0 && now.Before(cmd.at.Add(cmd.transition+fadeReportLag)) {
			return "" // turn-on fade from an unknown color temperature
		}
		if k, ok := numberAttr(s.Attributes, "color_temp_kelvin"); ok {
			if !onFadePath(k, float64(behindKelvin), float64(cmd.kelvin),
				manualReverseKelvinTolerance, manualKelvinTolerance) {
				return fmt.Sprintf("color %.0fK differs from commanded %dK", k, cmd.kelvin)
			}
		}
	case "off":
		if cmd.on {
			if early && !cmd.fromOn {
				return ""
			}
			return "cans turned off by hand"
		}
	}
	return ""
}

// onFadePath reports whether v lies between behind (where the fade may still be) and target.
// It allows reverse past behind, against the fade's direction, and tolerance past target.
// Note: lo = min(behind-reverse, target-tolerance). During a down-fade, the binding lower bound is
// usually target-tolerance. So any value from target-tolerance up to the starting level counts as on
// the path. Example: start 100%, target 75%: a turn-down to 85% is not caught until the next tick.
func onFadePath(v, behind, target, reverse, tolerance float64) bool {
	lo := math.Min(behind-reverse, target-tolerance)
	hi := math.Max(behind+reverse, target+tolerance)
	return v >= lo && v <= hi
}

func numberAttr(attrs map[string]interface{}, key string) (float64, bool) {
	switch v := attrs[key].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	}
	return 0, false
}

// luxHeld reports whether the room is held after a manual change.
func (m *Manager) luxHeld(room *RoomConfig) bool {
	if !luxActive(room) {
		return false
	}
	m.luxMu.Lock()
	defer m.luxMu.Unlock()
	return m.luxRoom(room.HueGroup).held
}

// clearLuxHold ends the hold on one room and forgets the last command, so the next tick sends fresh values.
func (m *Manager) clearLuxHold(room *RoomConfig, reason string) {
	if !luxManaged(room) {
		return
	}
	m.luxMu.Lock()
	st := m.luxRoom(room.HueGroup)
	wasHeld := st.held
	st.held, st.heldReason, st.heldSince = false, "", time.Time{}
	st.cancelHoldCheck()
	st.cmd = nil
	st.previous = nil
	st.followers = map[string]*luxCommand{}
	st.previousFollowers = map[string]*luxCommand{}
	m.luxMu.Unlock()
	if wasHeld {
		m.logger.Info("Clearing manual hold",
			zap.String("room", room.HueGroup),
			zap.String("reason", reason))
		m.setLuxHoldHelper(room, false)
	}
}

// clearAllLuxHolds ends every hold. Used when everyone is asleep or the house is empty.
func (m *Manager) clearAllLuxHolds(reason string) {
	for i := range m.config.Rooms {
		m.clearLuxHold(&m.config.Rooms[i], reason)
	}
}

// forgetLuxCommand drops the remembered command, so the next tick sends fresh values.
// Used when another controller (Hue Sync) has had the lights.
func (m *Manager) forgetLuxCommand(room *RoomConfig) {
	if !luxManaged(room) {
		return
	}
	m.luxMu.Lock()
	defer m.luxMu.Unlock()
	st := m.luxRoom(room.HueGroup)
	st.cancelHoldCheck()
	st.cmd = nil
	st.previous = nil
	st.followers = map[string]*luxCommand{}
	st.previousFollowers = map[string]*luxCommand{}
}

// noteLuxRoomOff records that the plugin turned the room off, so the cans going dark
// is not mistaken for a manual change.
func (m *Manager) noteLuxRoomOff(room *RoomConfig) {
	if !luxManaged(room) {
		return
	}
	m.luxMu.Lock()
	defer m.luxMu.Unlock()
	st := m.luxRoom(room.HueGroup)
	st.cancelHoldCheck()
	st.cmd = &luxCommand{at: m.clock.Now(), on: false, transition: 0}
	st.previous = nil
	for _, f := range room.LuxControl.FollowerLights {
		st.followers[f.Entity] = &luxCommand{at: m.clock.Now(), on: false, transition: 0}
	}
}

// publishTVRoomTargetLux shares the TV room's target lux with the TV plugin. The value is rounded to
// 0.1 lux, so subscribers hear about real changes, not float noise.
func (m *Manager) publishTVRoomTargetLux(lux float64) {
	if err := m.stateManager.SetNumber("tvRoomTargetLux", math.Round(lux*10)/10); err != nil {
		m.logger.Warn("Failed to publish tvRoomTargetLux", zap.Error(err))
	}
}

// publishTVRoomBrightnessPct shares the TV room curve's post-ambient, calibrated output.
func (m *Manager) publishTVRoomBrightnessPct(pct float64) {
	if err := m.stateManager.SetNumber("tvRoomBrightnessPct", pct); err != nil {
		m.logger.Warn("Failed to publish tvRoomBrightnessPct", zap.Error(err))
	}
}
