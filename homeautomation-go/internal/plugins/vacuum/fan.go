package vacuum

import (
	"context"
	"fmt"
	"time"

	"homeautomation/internal/valetudo"

	"go.uber.org/zap"
)

// Quiet travel keeps the fan on scheduler.travel_fan_speed while the robot drives between rooms, and sets the
// treatment's fan speed only while the robot is in the room it is cleaning. The robot applies a fan change at
// once, in the middle of a segment clean. It applies only to runs this scheduler started in active mode.

// maxRaiseFails is how many raises in a row may fail. A failed raise is tried again on the next poll; after
// the second failure the scheduler stops switching the fan for the rest of the run.
const maxRaiseFails = 2

// quietFan is the fan switching of one run.
type quietFan struct {
	travel string // fan preset between rooms
	treat  string // the treatment's fan preset, for the room being cleaned

	// travelling is true while the robot is not in the room it is cleaning. Polls are then faster.
	travelling bool
	// raiseFails counts failed raises since the last raise that worked.
	raiseFails int
	// gaveUp is true once raising failed maxRaiseFails times. The fan then stays at, or is brought back to,
	// the treatment's level for the rest of the run.
	gaveUp bool
	// lowerFailed is true after a lower failed. The fan stays at the treatment's level and lowering is not
	// tried again until the robot has been in the room it cleans again.
	lowerFailed bool
}

// checkTravelFan checks scheduler.travel_fan_speed against the robot's fan presets. A robot that cannot be
// asked is asked again before the next run. A name the robot does not list turns quiet travel off. The caller
// holds tickMu.
func (s *Scheduler) checkTravelFan() {
	if s.cfg.TravelFanSpeed == "" || s.travelFanChecked {
		return
	}
	var names []string
	err := s.withCommandTimeout(func(ctx context.Context) error {
		var err error
		names, err = s.robot.Presets(ctx, valetudo.CapabilityFanSpeed)
		return err
	})
	if err != nil {
		s.logger.Warn("Cannot list the robot's fan presets to check scheduler.travel_fan_speed; will check again "+
			"before the next run", zap.String("travel_fan_speed", s.cfg.TravelFanSpeed), zap.Error(err))
		return
	}
	s.travelFanChecked = true
	for _, n := range names {
		if n == s.cfg.TravelFanSpeed {
			return
		}
	}
	s.travelFanProblem = fmt.Sprintf("scheduler.travel_fan_speed %q is not one of the robot's fan presets %v; "+
		"quiet travel is off", s.cfg.TravelFanSpeed, names)
	s.logger.Error("Vacuum scheduler: " + s.travelFanProblem)
}

// quietFanFor returns the fan switching for a run of a treatment, or nil when quiet travel does not apply:
// travel_fan_speed is not set or not valid, the treatment sets no fan speed or the same one, or the house map
// has no valetudo frame, so the room being cleaned can never be known.
func (s *Scheduler) quietFanFor(tc TreatmentConfig) *quietFan {
	travel := s.cfg.TravelFanSpeed
	if travel == "" || tc.FanSpeed == "" || travel == tc.FanSpeed || s.travelFanProblem != "" ||
		s.houseMap == nil || s.houseMap.Valetudo == nil {
		return nil
	}
	return &quietFan{travel: travel, treat: tc.FanSpeed, travelling: true}
}

// setFan selects a fan preset. Only an active, writable scheduler sends it.
func (s *Scheduler) setFan(name string) error {
	if s.cfg.Mode != ModeActive || s.readOnly {
		s.logger.Info("Vacuum would set the fan speed", zap.String("fan_speed", name),
			zap.String("mode", s.cfg.Mode), zap.Bool("read_only", s.readOnly))
		return nil
	}
	return s.withCommandTimeout(func(ctx context.Context) error {
		return s.robot.SetPreset(ctx, valetudo.CapabilityFanSpeed, name)
	})
}

// wantFan is the fan preset the robot should have now. "" means keep what it has: the robot is in no room of
// the house map, such as a gap between two rooms.
func wantFan(r *run, st *valetudo.State, room string, posOK bool) string {
	f := r.fan
	switch {
	case f.gaveUp:
		return f.treat
	case r.aborted || r.paused || r.homeSent:
		// Sent home, or waiting for the way home to clear.
		return f.travel
	case st.Status != valetudo.StatusCleaning || st.Flag == valetudo.FlagResumable:
		// Leaving the dock, going to or from a mop wash, docked, or the trip home at the end.
		return f.travel
	case !posOK:
		// Where the robot is cannot be told. Never clean a room at the travel level on purpose.
		return f.treat
	case room == "":
		return ""
	case r.current != "" && room == r.current:
		return f.treat
	default:
		// Driving to the next room, or through a room that is not being cleaned now.
		return f.travel
	}
}

// steerFan sets the fan for where the robot is on our own run. A command is sent only when the wanted level
// differs from the one last set. The caller holds tickMu.
func (s *Scheduler) steerFan(r *run, st *valetudo.State, room string, posOK bool) {
	f := r.fan
	if f == nil {
		return
	}
	want := wantFan(r, st, room, posOK)
	if want == "" {
		return
	}
	f.travelling = want == f.travel
	if want == f.treat {
		// The robot is in the room it cleans. A lower that failed on the last leg is tried again on the next.
		f.lowerFailed = false
	}
	if want == r.fanSpeed {
		return
	}

	if want == f.travel {
		if f.lowerFailed {
			return
		}
		if err := s.setFan(want); err != nil {
			f.lowerFailed = true
			s.logger.Warn("Vacuum could not lower the fan for travel; cleaning goes on",
				zap.String("fan_speed", want), zap.String("room", room), zap.Error(err))
			return
		}
		r.fanSpeed = want
		s.logger.Info("Vacuum fan lowered for travel", zap.String("fan_speed", want), zap.String("room", room),
			zap.String("cleaning", r.current))
		return
	}

	if err := s.setFan(want); err != nil {
		f.raiseFails++
		switch {
		case f.gaveUp:
			s.logger.Debug("Vacuum still cannot set the treatment's fan speed", zap.String("fan_speed", want),
				zap.Error(err))
		case f.raiseFails < maxRaiseFails:
			s.logger.Warn("Vacuum could not raise the fan; trying again on the next poll",
				zap.String("fan_speed", want), zap.String("room", room), zap.Error(err))
		default:
			f.gaveUp, f.travelling = true, false
			s.logger.Warn("Vacuum could not raise the fan twice; quiet travel stops for the rest of this run",
				zap.String("fan_speed", want), zap.String("room", room), zap.Error(err))
		}
		return
	}
	r.fanSpeed, f.raiseFails, f.lowerFailed = want, 0, false
	s.logger.Info("Vacuum fan raised for cleaning", zap.String("fan_speed", want), zap.String("room", room))
}

// fanWarnings keeps the quiet travel warnings in shadow state for as long as they hold. The caller holds
// tickMu.
func (s *Scheduler) fanWarnings() {
	if s.travelFanProblem != "" {
		s.warn("fan:config", s.travelFanProblem)
	}
	r := s.run
	if r == nil || r.fan == nil {
		return
	}
	switch {
	case r.fan.gaveUp:
		s.warn("fan:raise", fmt.Sprintf("could not raise the fan to %q twice; quiet travel is off for the rest "+
			"of this run and the fan stays at %q", r.fan.treat, r.fan.treat))
	case r.fan.lowerFailed:
		s.warn("fan:lower", fmt.Sprintf("could not lower the fan to %q for travel; the robot travels at %q",
			r.fan.travel, r.fan.treat))
	}
}

// pollInterval is how long to wait for the next poll: travel_track_interval while our run's robot travels with
// quiet travel on, track_interval otherwise. The caller holds tickMu.
func (s *Scheduler) pollInterval() time.Duration {
	if r := s.run; r != nil && r.fan != nil && r.fan.travelling && !r.fan.gaveUp {
		return s.cfg.TravelTrackInterval
	}
	return s.cfg.TrackInterval
}

// currentPollInterval is pollInterval for the loop.
func (s *Scheduler) currentPollInterval() time.Duration {
	s.tickMu.Lock()
	defer s.tickMu.Unlock()
	return s.pollInterval()
}
