package dayphase

import (
	"fmt"
	"sync"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/config"

	"github.com/sixdouglas/suncalc"
	"go.uber.org/zap"
)

// SunEvent represents the simplified sun event state
type SunEvent string

const (
	SunEventMorning SunEvent = "morning"
	SunEventDay     SunEvent = "day"
	SunEventSunset  SunEvent = "sunset"
	SunEventDusk    SunEvent = "dusk"
	SunEventNight   SunEvent = "night"
)

// DayPhase represents the current day phase
type DayPhase string

const (
	DayPhaseMorning  DayPhase = "morning"
	DayPhaseDay      DayPhase = "day"
	DayPhaseSunset   DayPhase = "sunset"
	DayPhaseDusk     DayPhase = "dusk"
	DayPhaseWinddown DayPhase = "winddown"
	DayPhaseNight    DayPhase = "night"
)

// Calculator manages sun event tracking and day phase calculation
type Calculator struct {
	latitude  float64
	longitude float64
	logger    *zap.Logger
	clock     clock.Clock

	// Mutex protects sunTimes and lastUpdate from concurrent access
	mu sync.RWMutex

	// Cached sun times from suncalc (updated every 6 hours)
	// These match Node-RED's suncalc exactly
	sunTimes   map[string]time.Time
	lastUpdate time.Time
}

// NewCalculator creates a new day phase calculator
// Default coordinates are for Austin, TX area (32.85486, -97.50515)
func NewCalculator(latitude, longitude float64, logger *zap.Logger) *Calculator {
	return &Calculator{
		latitude:  latitude,
		longitude: longitude,
		logger:    logger,
		clock:     clock.NewRealClock(),
		sunTimes:  make(map[string]time.Time),
	}
}

// SetClock allows injection of a mock clock for testing
func (c *Calculator) SetClock(clk clock.Clock) {
	c.clock = clk
}

// UpdateSunTimes calculates sun event times for today using suncalc
// This uses the same algorithm as Node-RED's suncalc library
func (c *Calculator) UpdateSunTimes() error {
	now := c.clock.Now()

	// Get sun times using suncalc - this matches Node-RED exactly
	// The library uses the same sun angle calculations:
	// - sunrise/sunset: -0.833°
	// - dawn/dusk: -6° (civil twilight)
	// - nauticalDawn/nauticalDusk: -12°
	// - nightEnd/night: -18° (astronomical twilight)
	// - goldenHourEnd/goldenHour: 6°
	times := suncalc.GetTimes(now, c.latitude, c.longitude)

	// Lock for writing to sunTimes map
	c.mu.Lock()

	// Store all the times we need
	c.sunTimes["dawn"] = times[suncalc.Dawn].Value
	c.sunTimes["sunrise"] = times[suncalc.Sunrise].Value
	c.sunTimes["sunriseEnd"] = times[suncalc.SunriseEnd].Value
	c.sunTimes["goldenHourEnd"] = times[suncalc.GoldenHourEnd].Value
	c.sunTimes["solarNoon"] = times[suncalc.SolarNoon].Value
	c.sunTimes["goldenHour"] = times[suncalc.GoldenHour].Value
	c.sunTimes["sunsetStart"] = times[suncalc.SunsetStart].Value
	c.sunTimes["sunset"] = times[suncalc.Sunset].Value
	c.sunTimes["dusk"] = times[suncalc.Dusk].Value
	c.sunTimes["nauticalDusk"] = times[suncalc.NauticalDusk].Value
	c.sunTimes["night"] = times[suncalc.Night].Value
	c.sunTimes["nadir"] = times[suncalc.Nadir].Value
	c.sunTimes["nightEnd"] = times[suncalc.NightEnd].Value
	c.sunTimes["nauticalDawn"] = times[suncalc.NauticalDawn].Value

	c.lastUpdate = now

	// Copy values for logging (avoid holding lock during I/O)
	dawn := c.sunTimes["dawn"]
	sunrise := c.sunTimes["sunrise"]
	sunriseEnd := c.sunTimes["sunriseEnd"]
	goldenHourEnd := c.sunTimes["goldenHourEnd"]
	goldenHour := c.sunTimes["goldenHour"]
	sunsetStart := c.sunTimes["sunsetStart"]
	sunset := c.sunTimes["sunset"]
	dusk := c.sunTimes["dusk"]
	nauticalDusk := c.sunTimes["nauticalDusk"]
	night := c.sunTimes["night"]

	c.mu.Unlock()

	c.logger.Info("Sun times updated (using suncalc)",
		zap.Time("dawn", dawn),
		zap.Time("sunrise", sunrise),
		zap.Time("sunriseEnd", sunriseEnd),
		zap.Time("goldenHourEnd", goldenHourEnd),
		zap.Time("goldenHour", goldenHour),
		zap.Time("sunsetStart", sunsetStart),
		zap.Time("sunset", sunset),
		zap.Time("dusk", dusk),
		zap.Time("nauticalDusk", nauticalDusk),
		zap.Time("night", night))

	return nil
}

// GetSunEvent returns the current simplified sun event state
// This implements Node-RED's Sun State Summarizer logic exactly:
//
// Node-RED Sun State Summarizer maps:
//   - goldenHour, sunsetStart, sunset -> "sunset"
//   - dusk, nauticalDusk -> "dusk"
//   - night, nightEnd, nauticalDawn, dawn, nadir -> "night"
//   - sunrise, sunriseEnd -> "morning"
//   - goldenHourEnd -> "day"
//   - everything else -> "day"
func (c *Calculator) GetSunEvent() SunEvent {
	now := c.clock.Now()

	// Check if update is needed (read lock)
	c.mu.RLock()
	needsUpdate := c.lastUpdate.IsZero() || c.clock.Since(c.lastUpdate) > 6*time.Hour
	c.mu.RUnlock()

	// Ensure we have recent sun times
	if needsUpdate {
		c.UpdateSunTimes()
	}

	// Read sun times with lock
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Match Node-RED's Sun State Summarizer logic (with modified morning period)
	// The summarizer receives raw sun events and maps them to simplified states

	// Create noon for today in local time - morning lasts until noon
	noonToday := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())

	switch {
	// Night period: night, nightEnd, nauticalDawn, dawn, nadir
	// Before dawn (civil twilight starts), we're in "night"
	case now.Before(c.sunTimes["dawn"]):
		return SunEventNight

	// Morning period: from dawn until noon local time
	// (Extended from goldenHourEnd to accommodate late wake-up schedules)
	case now.Before(noonToday):
		return SunEventMorning

	// Day period: from noon until goldenHour starts (evening)
	case now.Before(c.sunTimes["goldenHour"]):
		return SunEventDay

	// Sunset period: goldenHour, sunsetStart, sunset - until civil dusk
	case now.Before(c.sunTimes["dusk"]):
		return SunEventSunset

	// Dusk period: from civil dusk until astronomical night (-18°)
	case now.Before(c.sunTimes["night"]):
		return SunEventDusk

	// Night period: after astronomical night starts
	default:
		return SunEventNight
	}
}

// GetSunTimes returns a copy of the cached sun times for debugging/logging
func (c *Calculator) GetSunTimes() map[string]time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Return a copy to avoid race conditions
	result := make(map[string]time.Time, len(c.sunTimes))
	for k, v := range c.sunTimes {
		result[k] = v
	}
	return result
}

// CalculateDayPhase determines the current day phase based on sun event and schedule
// This implements the logic from Node-RED's Configuration tab
//
// Schedule overrides allow delaying transitions past astronomical times:
// - schedule.Dusk: Don't transition to dusk/winddown until this time (even if sun has set)
// - schedule.Night: Don't transition to night until this time
func (c *Calculator) CalculateDayPhase(schedule *config.ParsedSchedule) DayPhase {
	sunEvent := c.GetSunEvent()
	now := c.clock.Now()

	c.logger.Debug("Calculating day phase",
		zap.String("sun_event", string(sunEvent)),
		zap.Time("now", now))

	// If schedule overrides are available, use them to delay transitions
	// This prevents lights from getting too dim too early
	if schedule != nil {
		// Convert now to the schedule's timezone for hour comparisons
		// Schedule times have the correct timezone embedded (e.g., America/Chicago)
		scheduleLocation := schedule.Dusk.Location()
		nowLocal := now.In(scheduleLocation)

		c.logger.Debug("Schedule comparison",
			zap.Time("now", now),
			zap.Time("now_local", nowLocal),
			zap.String("timezone", scheduleLocation.String()),
			zap.Time("schedule_dusk", schedule.Dusk),
			zap.Time("schedule_night", schedule.Night),
			zap.Bool("before_dusk", now.Before(schedule.Dusk)),
			zap.Bool("before_night", now.Before(schedule.Night)))
		// Early morning (before 6am local time) is always night, regardless of schedule
		// This prevents returning sunset phase at 3am when schedule.Dusk is "20:00"
		// (which would be "in the future" relative to 3am today)
		if nowLocal.Hour() < 6 {
			return DayPhaseNight
		}

		// Before scheduled dusk time: stay at day/sunset regardless of sun
		if now.Before(schedule.Dusk) {
			switch sunEvent {
			case SunEventMorning:
				return DayPhaseMorning
			case SunEventDay:
				return DayPhaseDay
			case SunEventSunset, SunEventDusk:
				// Sun has set but we're before the configured dusk time
				// Keep lights at sunset level (brighter than dusk)
				return DayPhaseSunset
			case SunEventNight:
				// SunEventNight can occur in two contexts:
				// 1. Pre-dawn morning (before sunrise) - should return Night
				// 2. Post-sunset evening (after astronomical night) - should delay to Sunset
				// Use noon as the boundary: if before noon, it's pre-dawn
				if nowLocal.Hour() < 12 {
					return DayPhaseNight
				}
				// Evening: delay transition to sunset per schedule
				return DayPhaseSunset
			default:
				return DayPhaseDay
			}
		}

		// After scheduled dusk, before scheduled night
		if now.Before(schedule.Night) {
			switch sunEvent {
			case SunEventMorning:
				return DayPhaseMorning
			case SunEventDay:
				return DayPhaseDay
			case SunEventSunset:
				return DayPhaseSunset
			case SunEventDusk:
				return DayPhaseDusk
			case SunEventNight:
				// Astronomical night but before scheduled night time
				return DayPhaseWinddown
			default:
				return DayPhaseDay
			}
		}

		// After scheduled night time
		return DayPhaseNight
	}

	// No schedule available, use sun event directly with simple time-based night logic
	switch sunEvent {
	case SunEventMorning:
		return DayPhaseMorning

	case SunEventDay:
		return DayPhaseDay

	case SunEventSunset:
		return DayPhaseSunset

	case SunEventDusk:
		return DayPhaseDusk

	case SunEventNight:
		if now.Hour() >= 23 || now.Hour() < 6 {
			return DayPhaseNight
		}
		return DayPhaseWinddown

	default:
		return DayPhaseDay
	}
}

// StartPeriodicUpdate starts a goroutine that updates sun times every 6 hours
func (c *Calculator) StartPeriodicUpdate() chan struct{} {
	stopChan := make(chan struct{})

	c.logger.Info("Starting periodic sun time updates (every 6 hours)")

	// Initial update
	c.UpdateSunTimes()

	go func() {
		for {
			select {
			case <-c.clock.After(6 * time.Hour):
				c.logger.Debug("Periodic sun time update")
				if err := c.UpdateSunTimes(); err != nil {
					c.logger.Error("Failed to update sun times", zap.Error(err))
				}

			case <-stopChan:
				c.logger.Info("Stopping periodic sun time updates")
				return
			}
		}
	}()

	return stopChan
}

// ValidateDayPhase checks if a string is a valid day phase
func ValidateDayPhase(phase string) (DayPhase, error) {
	switch phase {
	case string(DayPhaseMorning):
		return DayPhaseMorning, nil
	case string(DayPhaseDay):
		return DayPhaseDay, nil
	case string(DayPhaseSunset):
		return DayPhaseSunset, nil
	case string(DayPhaseDusk):
		return DayPhaseDusk, nil
	case string(DayPhaseWinddown):
		return DayPhaseWinddown, nil
	case string(DayPhaseNight):
		return DayPhaseNight, nil
	default:
		return "", fmt.Errorf("invalid day phase: %s", phase)
	}
}
