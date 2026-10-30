package lighting

import (
	"sync"
	"time"

	"homeautomation/internal/config"
	dayphaselib "homeautomation/internal/dayphase"

	"go.uber.org/zap"
)

// solarAnchors resolves curve anchors ("sun.dawn", "schedule.winddown") to times for
// the current day. It owns a sun calculator and a schedule loader, because the day phase
// plugin keeps its own copies private. Both refresh once per calendar day.
type solarAnchors struct {
	mu       sync.Mutex
	calc     *dayphaselib.Calculator
	loader   *config.Loader
	tz       *time.Location
	logger   *zap.Logger
	day      string
	sun      map[string]time.Time
	schedule *config.ParsedSchedule
}

func newSolarAnchors(calc *dayphaselib.Calculator, loader *config.Loader, tz *time.Location, logger *zap.Logger) *solarAnchors {
	return &solarAnchors{calc: calc, loader: loader, tz: tz, logger: logger}
}

// refresh reloads sun times and today's schedule when the calendar day changes.
// Caller must hold mu.
func (s *solarAnchors) refresh(now time.Time) {
	day := now.In(s.tz).Format("2006-01-02")
	if day == s.day {
		return
	}
	if err := s.calc.UpdateSunTimes(); err != nil {
		s.logger.Warn("Failed to update sun times for lux curve", zap.Error(err))
		return
	}
	s.sun = s.calc.GetSunTimes()
	sched, err := s.loader.GetTodaysScheduleInTimezone(s.tz)
	if err != nil {
		s.logger.Warn("Failed to read today's schedule for lux curve", zap.Error(err))
		s.schedule = nil
	} else {
		s.schedule = sched
	}
	s.day = day
}

// resolve implements AnchorResolver.
func (s *solarAnchors) resolve(anchor string, now time.Time) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh(now)

	kind, name := splitAnchor(anchor)
	switch kind {
	case "sun":
		if name == "noon" {
			name = "solarNoon"
		}
		t, ok := s.sun[name]
		return t, ok && !t.IsZero()
	case "schedule":
		if s.schedule == nil {
			return time.Time{}, false
		}
		var t time.Time
		switch name {
		case "dusk":
			t = s.schedule.Dusk
		case "winddown":
			t = s.schedule.Winddown
		case "night":
			t = s.schedule.Night
		case "stop_screens":
			t = s.schedule.StopScreens
		case "go_to_bed":
			t = s.schedule.GoToBed
		}
		return t, !t.IsZero()
	}
	return time.Time{}, false
}

// splitAnchor splits "sun.dawn" into ("sun", "dawn").
func splitAnchor(anchor string) (string, string) {
	for i := 0; i < len(anchor); i++ {
		if anchor[i] == '.' {
			return anchor[:i], anchor[i+1:]
		}
	}
	return anchor, ""
}
