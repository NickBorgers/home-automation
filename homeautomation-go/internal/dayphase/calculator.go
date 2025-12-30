package dayphase

import (
	"fmt"
	"time"

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
		sunTimes:  make(map[string]time.Time),
	}
}

// UpdateSunTimes calculates sun event times for today using suncalc
// This uses the same algorithm as Node-RED's suncalc library
func (c *Calculator) UpdateSunTimes() error {
	now := time.Now()

	// Get sun times using suncalc - this matches Node-RED exactly
	// The library uses the same sun angle calculations:
	// - sunrise/sunset: -0.833°
	// - dawn/dusk: -6° (civil twilight)
	// - nauticalDawn/nauticalDusk: -12°
	// - nightEnd/night: -18° (astronomical twilight)
	// - goldenHourEnd/goldenHour: 6°
	times := suncalc.GetTimes(now, c.latitude, c.longitude)

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

	c.logger.Info("Sun times updated (using suncalc)",
		zap.Time("dawn", c.sunTimes["dawn"]),
		zap.Time("sunrise", c.sunTimes["sunrise"]),
		zap.Time("sunriseEnd", c.sunTimes["sunriseEnd"]),
		zap.Time("goldenHourEnd", c.sunTimes["goldenHourEnd"]),
		zap.Time("goldenHour", c.sunTimes["goldenHour"]),
		zap.Time("sunsetStart", c.sunTimes["sunsetStart"]),
		zap.Time("sunset", c.sunTimes["sunset"]),
		zap.Time("dusk", c.sunTimes["dusk"]),
		zap.Time("nauticalDusk", c.sunTimes["nauticalDusk"]),
		zap.Time("night", c.sunTimes["night"]))

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
	now := time.Now()

	// Ensure we have recent sun times
	if c.lastUpdate.IsZero() || time.Since(c.lastUpdate) > 6*time.Hour {
		c.UpdateSunTimes()
	}

	// Match Node-RED's Sun State Summarizer logic
	// The summarizer receives raw sun events and maps them to simplified states
	switch {
	// Night period: night, nightEnd, nauticalDawn, dawn, nadir
	// Before dawn (civil twilight starts), we're in "night"
	case now.Before(c.sunTimes["dawn"]):
		return SunEventNight

	// Morning period: from dawn until goldenHourEnd (sun reaches 6° elevation)
	case now.Before(c.sunTimes["goldenHourEnd"]):
		return SunEventMorning

	// Day period: from goldenHourEnd until goldenHour starts (evening)
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

// GetSunTimes returns the cached sun times for debugging/logging
func (c *Calculator) GetSunTimes() map[string]time.Time {
	return c.sunTimes
}

// CalculateDayPhase determines the current day phase based on sun event and schedule
// This implements the logic from Node-RED's Configuration tab
//
// Schedule overrides allow delaying transitions past astronomical times:
// - schedule.Dusk: Don't transition to dusk/winddown until this time (even if sun has set)
// - schedule.Night: Don't transition to night until this time
func (c *Calculator) CalculateDayPhase(schedule *config.ParsedSchedule) DayPhase {
	sunEvent := c.GetSunEvent()
	now := time.Now()

	c.logger.Debug("Calculating day phase",
		zap.String("sun_event", string(sunEvent)),
		zap.Time("now", now))

	// If schedule overrides are available, use them to delay transitions
	// This prevents lights from getting too dim too early
	if schedule != nil {
		c.logger.Debug("Schedule comparison",
			zap.Time("now", now),
			zap.Time("schedule_dusk", schedule.Dusk),
			zap.Time("schedule_night", schedule.Night),
			zap.Bool("before_dusk", now.Before(schedule.Dusk)),
			zap.Bool("before_night", now.Before(schedule.Night)))
		// Early morning (before 6am) is always night, regardless of schedule
		// This prevents returning sunset phase at 3am when schedule.Dusk is "20:00"
		// (which would be "in the future" relative to 3am today)
		if now.Hour() < 6 {
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
				if now.Hour() < 12 {
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
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
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
