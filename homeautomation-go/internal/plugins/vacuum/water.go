package vacuum

import (
	"fmt"
	"strings"
	"time"

	"homeautomation/internal/valetudo"

	"go.uber.org/zap"
)

// Out of water: when the mop dock's clean water tank runs dry, the robot reports an error and cannot mop. The
// scheduler then turns waterEntity on and plans vacuum-only runs until the tank is refilled. The robot gives
// no signal when the tank is refilled, so the helper turns off when someone comes home, when the water error
// clears while someone is home and no run of ours is going, or by hand. If that guess is wrong, the next full
// run hits the error again and the helper turns back on.

// waterEntity is on while the clean water tank is empty. It survives restarts, and the owner may turn it off
// by hand.
const waterEntity = "input_boolean.vacuum_water_empty"

// anyoneHomeKey is the state variable whose false-to-true change means someone came home.
const anyoneHomeKey = "isAnyoneHome"

// anyoneHomeEvent is the boolEvent entity used for anyoneHomeKey changes.
const anyoneHomeEvent = "state:" + anyoneHomeKey

// outcomeNoWater ends a run that is not vacuum only when the robot reports the water error.
const outcomeNoWater = "no_water"

// isWaterError reports whether an error sensor state is the configured water error.
func (s *Scheduler) isWaterError(state string) bool {
	text := strings.TrimSpace(s.cfg.WaterEmptyError)
	return text != "" && strings.Contains(strings.ToLower(state), strings.ToLower(text))
}

// startWater subscribes to the error sensor, the water helper and anyoneHomeKey, and reads the error sensor
// once. Without an error sensor the feature is off.
func (s *Scheduler) startWater() {
	if s.cfg.errorSensorID == "" {
		return
	}
	for _, entity := range []string{s.cfg.errorSensorID, waterEntity} {
		if err := s.subHelper.SubscribeToEntity(entity, s.queueBoolEvent); err != nil {
			s.logger.Warn("Cannot subscribe for the out-of-water rule", zap.String("entity", entity), zap.Error(err))
		}
	}
	if err := s.subHelper.SubscribeToState(anyoneHomeKey, func(_ string, oldValue, newValue interface{}) {
		s.queueEvent(boolEvent{entity: anyoneHomeEvent, from: fmt.Sprint(oldValue), to: fmt.Sprint(newValue),
			at: s.timeProvider.Now()})
	}); err != nil {
		s.logger.Warn("Cannot subscribe to isAnyoneHome; the water helper will not turn off on arrival",
			zap.Error(err))
	}
	if st, ok := s.getState(s.cfg.errorSensorID); ok && s.isWaterError(st.State) {
		s.tickMu.Lock()
		s.waterErrorNow = true
		s.setWater(true, "the robot reports: "+st.State)
		s.tickMu.Unlock()
	}
}

// handleWaterEvent handles an event for the out-of-water rule. It reports whether the event was one. The
// caller holds tickMu.
func (s *Scheduler) handleWaterEvent(ev boolEvent) bool {
	switch {
	case ev.entity == anyoneHomeEvent:
		if ev.from == "false" && ev.to == "true" {
			s.setWater(false, "someone came home")
		}
		return true
	case ev.entity == waterEntity:
		delete(s.boolOverlay, waterEntity) // Home Assistant has spoken
		return true
	case s.cfg.errorSensorID == "" || ev.entity != s.cfg.errorSensorID:
		return false
	}
	switch strings.TrimSpace(ev.to) {
	case "", "unknown", "unavailable":
		return true // a blip says nothing about the water
	}
	was := s.waterErrorNow
	s.waterErrorNow = s.isWaterError(ev.to)
	switch {
	case s.waterErrorNow && !was:
		s.setWater(true, "the robot reports: "+ev.to)
	case was && ev.to == s.cfg.noErrorValue && s.run == nil:
		home, err := s.stateManager.GetBool(anyoneHomeKey)
		if err == nil && home {
			s.setWater(false, "the water error cleared while someone is home")
		}
	}
	return true
}

// waterEmpty reports whether runs must be vacuum only. Without the helper, the value is kept in memory only.
func (s *Scheduler) waterEmpty() bool {
	if s.cfg.errorSensorID == "" {
		return false
	}
	st, ok := s.getState(waterEntity)
	if !ok {
		if s.waterFallback {
			s.warn("missing:"+waterEntity, "missing helper "+waterEntity+"; out of water is kept in memory only")
		}
		return s.waterFallback
	}
	return s.effectiveBool(waterEntity, st.State == "on")
}

// setWater turns the out-of-water helper on or off. The caller holds tickMu.
func (s *Scheduler) setWater(on bool, why string) {
	if s.waterEmpty() == on {
		return
	}
	s.waterFallback = on
	if on {
		s.logger.Info("Vacuum is out of clean water; runs are vacuum only until it is refilled", zap.String("why", why))
	} else {
		s.logger.Info("Vacuum clean water counts as refilled; full runs may start again", zap.String("why", why))
	}
	if _, ok := s.getState(waterEntity); ok {
		_ = s.setBool(waterEntity, on)
	}
}

// robotCanStart reports whether the robot may start a run: on the dock, or in the water error, which the
// robot reports while it stands on the dock and which does not stop a vacuum-only run.
func (s *Scheduler) robotCanStart() bool {
	st := s.robotState
	return st.Docked() || (s.waterErrorNow && st.Status == valetudo.StatusError)
}

// vacuumOnly turns a plan's items into vacuum-only items while the clean water is out. A room due for a full
// clean gets one vacuum pass per full interval and stays dirty for its full clean. A room with its own vacuum
// interval relies on that instead. The caller holds tickMu.
func (s *Scheduler) vacuumOnly(now time.Time, items []PlanItem) ([]PlanItem, []Skip) {
	var out []PlanItem
	var skips []Skip
	for _, it := range items {
		if it.Treatment == TreatmentVacuum {
			out = append(out, it)
			continue
		}
		rc := s.cfg.Rooms[it.Room]
		last := s.dryVacuumed[it.Room]
		switch {
		case rc.Vacuum > 0:
			skips = append(skips, Skip{Room: it.Room, Treatment: it.Treatment,
				Reason: "out of water; the room's own vacuum interval covers it"})
		case !last.IsZero() && now.Sub(last) < rc.Full:
			skips = append(skips, Skip{Room: it.Room, Treatment: it.Treatment, Reason: fmt.Sprintf(
				"out of water; vacuumed while out of water at %s, again after %s",
				last.Format(time.RFC3339), last.Add(rc.Full).Format(time.RFC3339))})
		default:
			k := itemKey{Room: it.Room, Treatment: TreatmentVacuum}
			out = append(out, PlanItem{Room: it.Room, Treatment: TreatmentVacuum, Score: it.Score,
				Manual: it.Manual, Cost: s.itemCost(k, nil, false)})
		}
	}
	return out, skips
}
