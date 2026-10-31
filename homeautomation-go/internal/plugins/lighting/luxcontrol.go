package lighting

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"

	"go.uber.org/zap"
)

// Lux-target lighting for the ceiling cans. Design: docs/flows/LUX_LIGHTING.md.

const (
	// luxTransition is the transition on routine lux commands, so changes are not visible as steps.
	luxTransition = 60 * time.Second
	// luxDefaultTick is the control loop period when tick_seconds is not set.
	luxDefaultTick = 60 * time.Second
	// luxDefaultSmoothing is the outdoor smoothing window when smoothing_minutes is not set.
	luxDefaultSmoothing = 5 * time.Minute
	// luxDefaultGuestFactor multiplies the target lux when increase_brightness_if_true holds.
	luxDefaultGuestFactor = 1.5
	// manualGrace is extra time after a command's transition during which state reports are ignored.
	manualGrace = 10 * time.Second
	// manualBrightnessTolerance and manualKelvinTolerance bound how far the cans may sit from
	// the commanded values before the change counts as manual.
	manualBrightnessTolerance = 5.0
	manualKelvinTolerance     = 150
)

// luxCommand is the last command sent (or, in log_only/READ_ONLY mode, that would be sent) to a room's cans.
type luxCommand struct {
	at         time.Time
	transition time.Duration
	on         bool
	pct        float64
	kelvin     int
}

// luxRoomState is the per-room control state.
type luxRoomState struct {
	cmd       *luxCommand
	followers map[string]*luxCommand // last command per follower light
	// followerHeld marks strips a person changed by hand. Only the strip is held, never the room.
	followerHeld map[string]bool
	held         bool
	heldSince    time.Time
	heldReason   string
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
		st = &luxRoomState{followers: map[string]*luxCommand{}, followerHeld: map[string]bool{}}
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

	for _, room := range rooms {
		if !luxActive(room) {
			continue
		}
		r := room
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
	defer m.luxMu.Unlock()
	if err != nil || st == nil {
		m.luxOutdoor.reset()
		return 0, false
	}
	v, perr := strconv.ParseFloat(st.State, 64)
	if perr != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		m.luxOutdoor.reset()
		return 0, false
	}
	m.luxOutdoor.add(now, v)
	return m.luxOutdoor.mean()
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
	kelvin := target.Kelvin
	if !lc.HasColorTemp() {
		kelvin = 0 // brightness-only light: no kelvin is sent, compared or recorded
	}
	rec.TargetKelvin = kelvin
	rec.CurvePoint = fmt.Sprintf("%s -> %s @ %.2f", target.From, target.To, target.Pos)

	if m.luxGuestsBoost(room) {
		factor := lc.GuestFactor
		if factor <= 0 {
			factor = luxDefaultGuestFactor
		}
		rec.TargetLux *= factor
	}

	outdoor, haveOutdoor := m.sampleOutdoor(now)
	rec.OutdoorLux = outdoor
	rec.DaylightEstimate = outdoor * lc.DaylightFactor
	rec.NeededLux = rec.TargetLux - rec.DaylightEstimate
	note := ""
	if !haveOutdoor {
		note = " (outdoor sensor missing; daylight treated as 0)"
	}

	if rec.Held {
		finish("held: manual change, lights left alone" + note)
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

	if !luxActive(room) || m.readOnly {
		m.luxMu.Lock()
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

// handleCansChange detects a manual change to the cans: any report that differs from what
// the plugin last commanded, after the command's transition has finished.
func (m *Manager) handleCansChange(room *RoomConfig, newState *ha.State) {
	if newState == nil || m.readOnly || !luxActive(room) {
		return
	}
	// Hue Sync and other plugins own the lights when the room is not "on".
	if action, _ := m.evaluateConditions(room); action != "on" {
		return
	}
	now := m.clock.Now()

	m.luxMu.Lock()
	defer m.luxMu.Unlock()
	st := m.luxRoom(room.HueGroup)
	if st.held || st.cmd == nil {
		return
	}
	cmd := st.cmd
	if now.Before(cmd.at.Add(cmd.transition + manualGrace)) {
		return
	}

	reason := manualChangeReason(cmd, newState, room.LuxControl.HasColorTemp())
	if reason == "" {
		return
	}
	st.held, st.heldSince, st.heldReason = true, now, reason
	m.logger.Info("Manual change detected; holding room",
		zap.String("room", room.HueGroup),
		zap.String("reason", reason))
}

// handleFollowerChange is handleCansChange for a follower light: a report that differs from
// what the plugin last sent it, after the transition has finished, holds that strip only. The
// room and its cans keep following the curve. Followers are brightness-only, so color
// temperature is never compared. The strip hold clears with the room hold (clearLuxHold).
func (m *Manager) handleFollowerChange(room *RoomConfig, newState *ha.State) {
	if newState == nil || m.readOnly || !luxActive(room) {
		return
	}
	if action, _ := m.evaluateConditions(room); action != "on" {
		return
	}
	now := m.clock.Now()

	m.luxMu.Lock()
	defer m.luxMu.Unlock()
	st := m.luxRoom(room.HueGroup)
	last := st.followers[newState.EntityID]
	if st.followerHeld[newState.EntityID] || last == nil || now.Before(last.at.Add(last.transition+manualGrace)) {
		return
	}
	reason := manualChangeReason(last, newState, false)
	if reason == "" {
		return
	}
	st.followerHeld[newState.EntityID] = true
	m.logger.Info("Manual change to follower light detected; holding that strip only",
		zap.String("entity_id", newState.EntityID),
		zap.String("room", room.HueGroup),
		zap.String("reason", reason))
}

// luxDriveFollowers sets each follower light from the cans command: floor + gain * cans percent,
// or off when the cans are off. The minimum-step rule compares against the follower's own last
// command, so a strip is not told to move on every tick. In log_only and READ_ONLY mode the
// command is recorded and not sent.
func (m *Manager) luxDriveFollowers(room *RoomConfig, st *luxRoomState, cans *luxCommand) {
	send := luxActive(room) && !m.readOnly
	for _, f := range room.LuxControl.FollowerLights {
		next := &luxCommand{at: cans.at, transition: cans.transition, on: cans.on}
		if cans.on {
			next.pct = f.followerPct(cans.pct)
		}
		m.luxMu.Lock()
		prev := st.followers[f.Entity]
		held := st.followerHeld[f.Entity]
		m.luxMu.Unlock()
		if held {
			continue
		}
		if (prev == nil && !m.luxLightIsOn(f.Entity)) || (prev != nil && !prev.on) {
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
		st.followers[f.Entity] = next
		m.luxMu.Unlock()
	}
}

// luxLightIsOn reports whether Home Assistant shows the light on. A strip already lit after a
// restart keeps the cans' long transition instead of popping.
func (m *Manager) luxLightIsOn(entity string) bool {
	s, err := m.haClient.GetState(entity)
	return err == nil && s != nil && s.State == "on"
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
		zap.Any("data", data))
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
		fs := shadowstate.FollowerState{Held: st.followerHeld[f.Entity]}
		if c := st.followers[f.Entity]; c != nil && c.on {
			fs.Pct = c.pct
		}
		out[f.Entity] = fs
	}
	return out
}

// manualChangeReason compares a cans report with the last command. It returns "" when they agree.
func manualChangeReason(cmd *luxCommand, s *ha.State, colorTemp bool) string {
	switch s.State {
	case "on":
		if !cmd.on {
			return "cans turned on by hand"
		}
		if b, ok := numberAttr(s.Attributes, "brightness"); ok {
			if pct := b / 255 * 100; math.Abs(pct-cmd.pct) > manualBrightnessTolerance {
				return fmt.Sprintf("brightness %.0f%% differs from commanded %.0f%%", pct, cmd.pct)
			}
		}
		if !colorTemp {
			return ""
		}
		if k, ok := numberAttr(s.Attributes, "color_temp_kelvin"); ok {
			if math.Abs(k-float64(cmd.kelvin)) > manualKelvinTolerance {
				return fmt.Sprintf("color %.0fK differs from commanded %dK", k, cmd.kelvin)
			}
		}
	case "off":
		if cmd.on {
			return "cans turned off by hand"
		}
	}
	return ""
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
	defer m.luxMu.Unlock()
	st := m.luxRoom(room.HueGroup)
	if st.held {
		m.logger.Info("Clearing manual hold",
			zap.String("room", room.HueGroup),
			zap.String("reason", reason))
	}
	st.held, st.heldReason, st.heldSince = false, "", time.Time{}
	st.cmd = nil
	st.followers = map[string]*luxCommand{}
	st.followerHeld = map[string]bool{}
}

// clearAllLuxHolds ends every hold. Used when everyone is asleep or the house is empty.
func (m *Manager) clearAllLuxHolds(reason string) {
	for i := range m.config.Rooms {
		m.clearLuxHold(&m.config.Rooms[i], reason)
	}
}

// forgetLuxCommand drops the remembered command, so the next tick sends fresh values.
// Used when another controller (Hue Sync) has had the lights.
// followerHeld is intentionally kept: a strip hold set by a person survives a Hue Sync takeover.
// Only clearLuxHold resets followerHeld, matching the room-hold semantics.
func (m *Manager) forgetLuxCommand(room *RoomConfig) {
	if !luxManaged(room) {
		return
	}
	m.luxMu.Lock()
	defer m.luxMu.Unlock()
	st := m.luxRoom(room.HueGroup)
	st.cmd = nil
	st.followers = map[string]*luxCommand{}
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
	st.cmd = &luxCommand{at: m.clock.Now(), on: false, transition: 0}
	for _, f := range room.LuxControl.FollowerLights {
		st.followers[f.Entity] = &luxCommand{at: m.clock.Now(), on: false, transition: 0}
	}
}
