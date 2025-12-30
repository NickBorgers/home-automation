package dayphase

import (
	"testing"
	"time"

	"homeautomation/internal/config"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestCalculator_UpdateSunTimes(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	// Austin, TX coordinates
	calc := NewCalculator(32.85486, -97.50515, logger)

	err := calc.UpdateSunTimes()
	assert.NoError(t, err)

	// Verify sun times are set using the map
	sunTimes := calc.GetSunTimes()
	assert.False(t, sunTimes["sunrise"].IsZero(), "Sunrise should be set")
	assert.False(t, sunTimes["sunriseEnd"].IsZero(), "SunriseEnd should be set")
	assert.False(t, sunTimes["goldenHourEnd"].IsZero(), "GoldenHourEnd should be set")
	assert.False(t, sunTimes["sunset"].IsZero(), "Sunset should be set")
	assert.False(t, sunTimes["dawn"].IsZero(), "Dawn should be set")
	assert.False(t, sunTimes["dusk"].IsZero(), "Dusk should be set")
	assert.False(t, sunTimes["nauticalDusk"].IsZero(), "NauticalDusk should be set")
	assert.False(t, sunTimes["night"].IsZero(), "Night should be set")

	// Verify times are in reasonable order
	assert.True(t, sunTimes["dawn"].Before(sunTimes["sunrise"]), "Dawn should be before sunrise")
	assert.True(t, sunTimes["sunrise"].Before(sunTimes["sunriseEnd"]), "Sunrise should be before sunriseEnd")
	assert.True(t, sunTimes["sunriseEnd"].Before(sunTimes["goldenHourEnd"]), "SunriseEnd should be before goldenHourEnd")
	assert.True(t, sunTimes["goldenHourEnd"].Before(sunTimes["sunset"]), "GoldenHourEnd should be before sunset")
	assert.True(t, sunTimes["sunset"].Before(sunTimes["dusk"]), "Sunset should be before dusk")
	assert.True(t, sunTimes["dusk"].Before(sunTimes["nauticalDusk"]), "Dusk should be before nauticalDusk")
	assert.True(t, sunTimes["nauticalDusk"].Before(sunTimes["night"]), "NauticalDusk should be before night")
}

func TestCalculator_GetSunEvent(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	calc := NewCalculator(32.85486, -97.50515, logger)

	err := calc.UpdateSunTimes()
	assert.NoError(t, err)

	// Get current sun event
	sunEvent := calc.GetSunEvent()
	assert.NotEmpty(t, sunEvent)

	// Verify it's one of the valid sun events
	validEvents := []SunEvent{SunEventMorning, SunEventDay, SunEventSunset, SunEventDusk, SunEventNight}
	found := false
	for _, valid := range validEvents {
		if sunEvent == valid {
			found = true
			break
		}
	}
	assert.True(t, found, "Sun event should be one of the valid values")
}

func TestCalculator_CalculateDayPhase(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	calc := NewCalculator(32.85486, -97.50515, logger)

	err := calc.UpdateSunTimes()
	assert.NoError(t, err)

	// Create a sample schedule
	now := time.Now()
	schedule := &config.ParsedSchedule{
		BeginBackupWake: time.Date(now.Year(), now.Month(), now.Day(), 5, 0, 0, 0, now.Location()),
		BackupWakeTime:  time.Date(now.Year(), now.Month(), now.Day(), 7, 0, 0, 0, now.Location()),
		Dusk:            time.Date(now.Year(), now.Month(), now.Day(), 18, 0, 0, 0, now.Location()),
		Winddown:        time.Date(now.Year(), now.Month(), now.Day(), 21, 0, 0, 0, now.Location()),
		StopScreens:     time.Date(now.Year(), now.Month(), now.Day(), 22, 0, 0, 0, now.Location()),
		GoToBed:         time.Date(now.Year(), now.Month(), now.Day(), 22, 30, 0, 0, now.Location()),
		Night:           time.Date(now.Year(), now.Month(), now.Day(), 23, 0, 0, 0, now.Location()),
	}

	dayPhase := calc.CalculateDayPhase(schedule)
	assert.NotEmpty(t, dayPhase)

	// Verify it's one of the valid day phases
	validPhases := []DayPhase{
		DayPhaseMorning,
		DayPhaseDay,
		DayPhaseSunset,
		DayPhaseDusk,
		DayPhaseWinddown,
		DayPhaseNight,
	}
	found := false
	for _, valid := range validPhases {
		if dayPhase == valid {
			found = true
			break
		}
	}
	assert.True(t, found, "Day phase should be one of the valid values")
}

func TestCalculator_CalculateDayPhaseWithoutSchedule(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	calc := NewCalculator(32.85486, -97.50515, logger)

	err := calc.UpdateSunTimes()
	assert.NoError(t, err)

	// Calculate without schedule (should use fallback logic)
	dayPhase := calc.CalculateDayPhase(nil)
	assert.NotEmpty(t, dayPhase)
}

// setSunTimesForTest is a helper to set up sun times for testing
func setSunTimesForTest(calc *Calculator, now time.Time, dawn, sunrise, sunriseEnd, goldenHourEnd, goldenHour, sunsetStart, sunset, dusk, nauticalDusk, night time.Time) {
	calc.sunTimes["dawn"] = dawn
	calc.sunTimes["sunrise"] = sunrise
	calc.sunTimes["sunriseEnd"] = sunriseEnd
	calc.sunTimes["goldenHourEnd"] = goldenHourEnd
	calc.sunTimes["goldenHour"] = goldenHour
	calc.sunTimes["sunsetStart"] = sunsetStart
	calc.sunTimes["sunset"] = sunset
	calc.sunTimes["dusk"] = dusk
	calc.sunTimes["nauticalDusk"] = nauticalDusk
	calc.sunTimes["night"] = night
	calc.sunTimes["nightEnd"] = dawn.Add(-1 * time.Hour) // approximate
	calc.sunTimes["nauticalDawn"] = dawn.Add(-30 * time.Minute)
	calc.lastUpdate = now
}

func TestCalculator_GetSunEventAllPeriods(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	calc := NewCalculator(32.85486, -97.50515, logger)

	now := time.Now()

	tests := []struct {
		name          string
		dawn          time.Time
		sunrise       time.Time
		sunriseEnd    time.Time
		goldenHourEnd time.Time
		goldenHour    time.Time
		sunsetStart   time.Time
		sunset        time.Time
		dusk          time.Time
		nauticalDusk  time.Time
		night         time.Time
		expected      SunEvent
	}{
		{
			name:          "before dawn - night",
			dawn:          now.Add(2 * time.Hour),
			sunrise:       now.Add(3 * time.Hour),
			sunriseEnd:    now.Add(3*time.Hour + 30*time.Minute),
			goldenHourEnd: now.Add(4 * time.Hour),
			goldenHour:    now.Add(12 * time.Hour),
			sunsetStart:   now.Add(12*time.Hour + 30*time.Minute),
			sunset:        now.Add(13 * time.Hour),
			dusk:          now.Add(13*time.Hour + 30*time.Minute),
			nauticalDusk:  now.Add(14 * time.Hour),
			night:         now.Add(15 * time.Hour),
			expected:      SunEventNight,
		},
		{
			name:          "dawn period - between dawn and sunrise",
			dawn:          now.Add(-30 * time.Minute),
			sunrise:       now.Add(30 * time.Minute),
			sunriseEnd:    now.Add(1 * time.Hour),
			goldenHourEnd: now.Add(90 * time.Minute),
			goldenHour:    now.Add(10 * time.Hour),
			sunsetStart:   now.Add(10*time.Hour + 30*time.Minute),
			sunset:        now.Add(11 * time.Hour),
			dusk:          now.Add(11*time.Hour + 30*time.Minute),
			nauticalDusk:  now.Add(12 * time.Hour),
			night:         now.Add(13 * time.Hour),
			expected:      SunEventMorning,
		},
		{
			name:          "sunrise period - between sunrise and golden hour end",
			dawn:          now.Add(-2 * time.Hour),
			sunrise:       now.Add(-1 * time.Hour),
			sunriseEnd:    now.Add(-30 * time.Minute),
			goldenHourEnd: now.Add(1 * time.Hour),
			goldenHour:    now.Add(10 * time.Hour),
			sunsetStart:   now.Add(10*time.Hour + 30*time.Minute),
			sunset:        now.Add(11 * time.Hour),
			dusk:          now.Add(11*time.Hour + 30*time.Minute),
			nauticalDusk:  now.Add(12 * time.Hour),
			night:         now.Add(13 * time.Hour),
			expected:      SunEventMorning,
		},
		{
			name:          "day - between golden hour end and golden hour start",
			dawn:          now.Add(-6 * time.Hour),
			sunrise:       now.Add(-5 * time.Hour),
			sunriseEnd:    now.Add(-4*time.Hour - 30*time.Minute),
			goldenHourEnd: now.Add(-4 * time.Hour),
			goldenHour:    now.Add(4 * time.Hour),
			sunsetStart:   now.Add(4*time.Hour + 30*time.Minute),
			sunset:        now.Add(5 * time.Hour),
			dusk:          now.Add(5*time.Hour + 30*time.Minute),
			nauticalDusk:  now.Add(6 * time.Hour),
			night:         now.Add(7 * time.Hour),
			expected:      SunEventDay,
		},
		{
			name:          "sunset - golden hour",
			dawn:          now.Add(-12 * time.Hour),
			sunrise:       now.Add(-11 * time.Hour),
			sunriseEnd:    now.Add(-10*time.Hour - 30*time.Minute),
			goldenHourEnd: now.Add(-10 * time.Hour),
			goldenHour:    now.Add(-1 * time.Hour),
			sunsetStart:   now.Add(-30 * time.Minute),
			sunset:        now.Add(30 * time.Minute),
			dusk:          now.Add(1 * time.Hour),
			nauticalDusk:  now.Add(1*time.Hour + 30*time.Minute),
			night:         now.Add(2 * time.Hour),
			expected:      SunEventSunset,
		},
		{
			name:          "dusk - civil twilight (between dusk and night)",
			dawn:          now.Add(-14 * time.Hour),
			sunrise:       now.Add(-13 * time.Hour),
			sunriseEnd:    now.Add(-12*time.Hour - 30*time.Minute),
			goldenHourEnd: now.Add(-12 * time.Hour),
			goldenHour:    now.Add(-3 * time.Hour),
			sunsetStart:   now.Add(-2*time.Hour - 30*time.Minute),
			sunset:        now.Add(-2 * time.Hour),
			dusk:          now.Add(-1 * time.Hour),
			nauticalDusk:  now.Add(-30 * time.Minute),
			night:         now.Add(1 * time.Hour), // Night is in future, so we're in dusk period
			expected:      SunEventDusk,
		},
		{
			name:          "after night starts - night",
			dawn:          now.Add(-16 * time.Hour),
			sunrise:       now.Add(-15 * time.Hour),
			sunriseEnd:    now.Add(-14*time.Hour - 30*time.Minute),
			goldenHourEnd: now.Add(-14 * time.Hour),
			goldenHour:    now.Add(-5 * time.Hour),
			sunsetStart:   now.Add(-4*time.Hour - 30*time.Minute),
			sunset:        now.Add(-4 * time.Hour),
			dusk:          now.Add(-3 * time.Hour),
			nauticalDusk:  now.Add(-2 * time.Hour),
			night:         now.Add(-1 * time.Hour), // Night has already started
			expected:      SunEventNight,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set sun times to create the desired test scenario
			setSunTimesForTest(calc, now,
				tt.dawn, tt.sunrise, tt.sunriseEnd, tt.goldenHourEnd,
				tt.goldenHour, tt.sunsetStart, tt.sunset,
				tt.dusk, tt.nauticalDusk, tt.night)

			sunEvent := calc.GetSunEvent()
			assert.Equal(t, tt.expected, sunEvent, "Expected %s, got %s", tt.expected, sunEvent)
		})
	}
}

func TestCalculator_CalculateDayPhaseAllCases(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	now := time.Now()

	// Create a schedule for testing
	schedule := &config.ParsedSchedule{
		BeginBackupWake: time.Date(now.Year(), now.Month(), now.Day(), 5, 0, 0, 0, now.Location()),
		BackupWakeTime:  time.Date(now.Year(), now.Month(), now.Day(), 7, 0, 0, 0, now.Location()),
		Dusk:            time.Date(now.Year(), now.Month(), now.Day(), 18, 0, 0, 0, now.Location()),
		Winddown:        time.Date(now.Year(), now.Month(), now.Day(), 21, 0, 0, 0, now.Location()),
		StopScreens:     time.Date(now.Year(), now.Month(), now.Day(), 22, 0, 0, 0, now.Location()),
		GoToBed:         time.Date(now.Year(), now.Month(), now.Day(), 22, 30, 0, 0, now.Location()),
		Night:           time.Date(now.Year(), now.Month(), now.Day(), 23, 0, 0, 0, now.Location()),
	}

	tests := []struct {
		name          string
		setupSunTimes func(c *Calculator)
		schedule      *config.ParsedSchedule
		expected      DayPhase
	}{
		{
			name: "morning period",
			setupSunTimes: func(c *Calculator) {
				// Set sun times so current time falls in morning sun event
				setSunTimesForTest(c, now,
					now.Add(-2*time.Hour),                // dawn
					now.Add(-1*time.Hour),                // sunrise
					now.Add(-30*time.Minute),             // sunriseEnd
					now.Add(1*time.Hour),                 // goldenHourEnd (still in morning)
					now.Add(10*time.Hour),                // goldenHour
					now.Add(10*time.Hour+30*time.Minute), // sunsetStart
					now.Add(11*time.Hour),                // sunset
					now.Add(11*time.Hour+30*time.Minute), // dusk
					now.Add(12*time.Hour),                // nauticalDusk
					now.Add(13*time.Hour),                // night
				)
			},
			schedule: schedule,
			// Early morning (before 6am) always returns night; otherwise morning
			expected: func() DayPhase {
				if now.Hour() < 6 {
					return DayPhaseNight
				}
				return DayPhaseMorning
			}(),
		},
		{
			name: "day phase",
			setupSunTimes: func(c *Calculator) {
				// Set sun times so current time falls in day period
				setSunTimesForTest(c, now,
					now.Add(-8*time.Hour),                // dawn
					now.Add(-7*time.Hour),                // sunrise
					now.Add(-6*time.Hour-30*time.Minute), // sunriseEnd
					now.Add(-6*time.Hour),                // goldenHourEnd
					now.Add(6*time.Hour),                 // goldenHour
					now.Add(6*time.Hour+30*time.Minute),  // sunsetStart
					now.Add(7*time.Hour),                 // sunset
					now.Add(7*time.Hour+30*time.Minute),  // dusk
					now.Add(8*time.Hour),                 // nauticalDusk
					now.Add(9*time.Hour),                 // night
				)
			},
			schedule: schedule,
			// Early morning (before 6am) always returns night; otherwise day
			expected: func() DayPhase {
				if now.Hour() < 6 {
					return DayPhaseNight
				}
				return DayPhaseDay
			}(),
		},
		{
			name: "sunset phase",
			setupSunTimes: func(c *Calculator) {
				// Set sun times so current time falls in sunset period (golden hour)
				setSunTimesForTest(c, now,
					now.Add(-14*time.Hour),                // dawn
					now.Add(-13*time.Hour),                // sunrise
					now.Add(-12*time.Hour-30*time.Minute), // sunriseEnd
					now.Add(-12*time.Hour),                // goldenHourEnd
					now.Add(-1*time.Hour),                 // goldenHour (in golden hour)
					now.Add(-30*time.Minute),              // sunsetStart
					now.Add(30*time.Minute),               // sunset
					now.Add(1*time.Hour),                  // dusk (future)
					now.Add(1*time.Hour+30*time.Minute),   // nauticalDusk
					now.Add(2*time.Hour),                  // night
				)
			},
			schedule: schedule,
			// Early morning (before 6am) always returns night; otherwise sunset
			expected: func() DayPhase {
				if now.Hour() < 6 {
					return DayPhaseNight
				}
				return DayPhaseSunset
			}(),
		},
		{
			name: "dusk phase - after scheduled dusk time",
			setupSunTimes: func(c *Calculator) {
				// Set sun times so current time falls in dusk period (between dusk and night)
				setSunTimesForTest(c, now,
					now.Add(-16*time.Hour),                // dawn
					now.Add(-15*time.Hour),                // sunrise
					now.Add(-14*time.Hour-30*time.Minute), // sunriseEnd
					now.Add(-14*time.Hour),                // goldenHourEnd
					now.Add(-3*time.Hour),                 // goldenHour
					now.Add(-2*time.Hour-30*time.Minute),  // sunsetStart
					now.Add(-2*time.Hour),                 // sunset
					now.Add(-1*time.Hour),                 // dusk (past)
					now.Add(-30*time.Minute),              // nauticalDusk (past)
					now.Add(1*time.Hour),                  // night (future - so we're in dusk)
				)
			},
			// Set schedule.Dusk in the past so we're "after" the scheduled dusk time
			schedule: &config.ParsedSchedule{
				BeginBackupWake: time.Date(now.Year(), now.Month(), now.Day(), 5, 0, 0, 0, now.Location()),
				BackupWakeTime:  time.Date(now.Year(), now.Month(), now.Day(), 7, 0, 0, 0, now.Location()),
				Dusk:            now.Add(-2 * time.Hour), // Schedule dusk in the past
				Winddown:        time.Date(now.Year(), now.Month(), now.Day(), 21, 0, 0, 0, now.Location()),
				Night:           now.Add(2 * time.Hour), // Night in the future
			},
			// Expected: dusk if it's daytime hours (6am+), night if it's early morning (before 6am)
			expected: func() DayPhase {
				if now.Hour() < 6 {
					return DayPhaseNight // Early morning hours always return night
				}
				return DayPhaseDusk
			}(),
		},
		{
			name: "sunset phase - after scheduled dusk but sun still at sunset",
			setupSunTimes: func(c *Calculator) {
				// Sun is in sunset period (golden hour to dusk)
				// schedule.Dusk has passed but astronomical dusk hasn't
				setSunTimesForTest(c, now,
					now.Add(-14*time.Hour),                // dawn
					now.Add(-13*time.Hour),                // sunrise
					now.Add(-12*time.Hour-30*time.Minute), // sunriseEnd
					now.Add(-12*time.Hour),                // goldenHourEnd
					now.Add(-1*time.Hour),                 // goldenHour (past - in sunset period)
					now.Add(-30*time.Minute),              // sunsetStart
					now.Add(30*time.Minute),               // sunset
					now.Add(1*time.Hour),                  // dusk (future - still in sunset)
					now.Add(1*time.Hour+30*time.Minute),   // nauticalDusk
					now.Add(2*time.Hour),                  // night
				)
			},
			// Schedule dusk has passed, but astronomical sunset is still in progress
			schedule: &config.ParsedSchedule{
				BeginBackupWake: time.Date(now.Year(), now.Month(), now.Day(), 5, 0, 0, 0, now.Location()),
				BackupWakeTime:  time.Date(now.Year(), now.Month(), now.Day(), 7, 0, 0, 0, now.Location()),
				Dusk:            now.Add(-30 * time.Minute), // Schedule dusk in the past
				Winddown:        now.Add(1 * time.Hour),
				Night:           now.Add(3 * time.Hour), // Night in the future
			},
			// After schedule.Dusk but sun says sunset -> should return Sunset
			expected: func() DayPhase {
				if now.Hour() < 6 {
					return DayPhaseNight
				}
				return DayPhaseSunset
			}(),
		},
		{
			name: "dusk override - sun says dusk but before scheduled dusk",
			setupSunTimes: func(c *Calculator) {
				// Set sun times so sun event is dusk (past dusk, before night)
				setSunTimesForTest(c, now,
					now.Add(-16*time.Hour),                // dawn
					now.Add(-15*time.Hour),                // sunrise
					now.Add(-14*time.Hour-30*time.Minute), // sunriseEnd
					now.Add(-14*time.Hour),                // goldenHourEnd
					now.Add(-3*time.Hour),                 // goldenHour
					now.Add(-2*time.Hour-30*time.Minute),  // sunsetStart
					now.Add(-2*time.Hour),                 // sunset
					now.Add(-1*time.Hour),                 // dusk (past - sun says dusk)
					now.Add(-30*time.Minute),              // nauticalDusk (past)
					now.Add(1*time.Hour),                  // night (future)
				)
			},
			// Schedule dusk is in the FUTURE - we should delay to sunset
			schedule: &config.ParsedSchedule{
				BeginBackupWake: time.Date(now.Year(), now.Month(), now.Day(), 5, 0, 0, 0, now.Location()),
				BackupWakeTime:  time.Date(now.Year(), now.Month(), now.Day(), 7, 0, 0, 0, now.Location()),
				Dusk:            now.Add(1 * time.Hour), // Schedule dusk in the future
				Winddown:        now.Add(2 * time.Hour),
				Night:           now.Add(3 * time.Hour),
			},
			// Early morning (before 6am) always returns night; otherwise delayed to sunset
			expected: func() DayPhase {
				if now.Hour() < 6 {
					return DayPhaseNight
				}
				return DayPhaseSunset
			}(),
		},
		{
			name: "night override - sun says night but before scheduled dusk (evening)",
			setupSunTimes: func(c *Calculator) {
				// Set sun times so sun event is night (astronomical night in evening)
				setSunTimesForTest(c, now,
					now.Add(6*time.Hour),                 // dawn (next morning)
					now.Add(7*time.Hour),                 // sunrise
					now.Add(7*time.Hour+30*time.Minute),  // sunriseEnd
					now.Add(8*time.Hour),                 // goldenHourEnd
					now.Add(-4*time.Hour),                // goldenHour
					now.Add(-3*time.Hour-30*time.Minute), // sunsetStart
					now.Add(-3*time.Hour),                // sunset
					now.Add(-2*time.Hour),                // dusk (past)
					now.Add(-1*time.Hour-30*time.Minute), // nauticalDusk (past)
					now.Add(-1*time.Hour),                // night (past - sun says night)
				)
			},
			// Schedule dusk is in the FUTURE - we should delay to sunset even though sun says night
			// BUT only if it's evening (after noon). In the morning, return night.
			schedule: &config.ParsedSchedule{
				BeginBackupWake: time.Date(now.Year(), now.Month(), now.Day(), 5, 0, 0, 0, now.Location()),
				BackupWakeTime:  time.Date(now.Year(), now.Month(), now.Day(), 7, 0, 0, 0, now.Location()),
				Dusk:            now.Add(1 * time.Hour), // Schedule dusk in the future
				Winddown:        now.Add(2 * time.Hour),
				Night:           now.Add(3 * time.Hour),
			},
			// Before 6am: night, 6am-noon: night (pre-dawn), noon+: sunset (evening delay)
			expected: func() DayPhase {
				if now.Hour() < 12 {
					return DayPhaseNight // Pre-dawn or early morning
				}
				return DayPhaseSunset // Evening delay
			}(),
		},
		{
			name: "pre-dawn morning - sun says night but it's 6-11am (before noon)",
			setupSunTimes: func(c *Calculator) {
				// Set sun times so sun event is night (before dawn)
				// This simulates winter morning at 6:30am when dawn is at 7:05am
				setSunTimesForTest(c, now,
					now.Add(30*time.Minute),              // dawn (future - sun says night)
					now.Add(1*time.Hour),                 // sunrise
					now.Add(1*time.Hour+30*time.Minute),  // sunriseEnd
					now.Add(2*time.Hour),                 // goldenHourEnd
					now.Add(10*time.Hour),                // goldenHour
					now.Add(10*time.Hour+30*time.Minute), // sunsetStart
					now.Add(11*time.Hour),                // sunset
					now.Add(11*time.Hour+30*time.Minute), // dusk
					now.Add(12*time.Hour),                // nauticalDusk
					now.Add(13*time.Hour),                // night
				)
			},
			// Schedule times relative to now to ensure consistent behavior across test run times
			// Schedule dusk is always in the future so we test the "before scheduled dusk" path
			schedule: &config.ParsedSchedule{
				BeginBackupWake: time.Date(now.Year(), now.Month(), now.Day(), 5, 0, 0, 0, now.Location()),
				BackupWakeTime:  time.Date(now.Year(), now.Month(), now.Day(), 7, 0, 0, 0, now.Location()),
				Dusk:            now.Add(4 * time.Hour), // Schedule dusk in the future
				Winddown:        now.Add(5 * time.Hour), // Schedule winddown in the future
				Night:           now.Add(6 * time.Hour), // Schedule night in the future
			},
			// Before 6am: always night
			// 6am-noon + sun says night = pre-dawn = Night (tested with schedule.Dusk in future)
			// After noon + sun says night = evening delay = Sunset (schedule.Dusk still in future)
			expected: func() DayPhase {
				if now.Hour() < 12 {
					return DayPhaseNight // Pre-dawn or early morning
				}
				return DayPhaseSunset // Afternoon - this sun config doesn't happen in reality
			}(),
		},
		{
			name: "night with schedule - after schedule.Night",
			setupSunTimes: func(c *Calculator) {
				// Set sun times so current time falls in night period
				setSunTimesForTest(c, now,
					now.Add(4*time.Hour),                 // dawn (next morning)
					now.Add(5*time.Hour),                 // sunrise
					now.Add(5*time.Hour+30*time.Minute),  // sunriseEnd
					now.Add(6*time.Hour),                 // goldenHourEnd
					now.Add(14*time.Hour),                // goldenHour
					now.Add(14*time.Hour+30*time.Minute), // sunsetStart
					now.Add(15*time.Hour),                // sunset
					now.Add(15*time.Hour+30*time.Minute), // dusk
					now.Add(16*time.Hour),                // nauticalDusk
					now.Add(-1*time.Hour),                // night (past - we're in night)
				)
			},
			schedule: &config.ParsedSchedule{
				BeginBackupWake: time.Date(now.Year(), now.Month(), now.Day(), 5, 0, 0, 0, now.Location()),
				BackupWakeTime:  time.Date(now.Year(), now.Month(), now.Day(), 7, 0, 0, 0, now.Location()),
				Dusk:            now.Add(-4 * time.Hour), // Schedule dusk in the past
				Night:           now.Add(-2 * time.Hour), // Schedule night time in the past
			},
			expected: DayPhaseNight,
		},
		{
			name: "winddown with schedule - before schedule.Night",
			setupSunTimes: func(c *Calculator) {
				// Set sun times so current time falls in night period
				setSunTimesForTest(c, now,
					now.Add(4*time.Hour),                 // dawn
					now.Add(5*time.Hour),                 // sunrise
					now.Add(5*time.Hour+30*time.Minute),  // sunriseEnd
					now.Add(6*time.Hour),                 // goldenHourEnd
					now.Add(14*time.Hour),                // goldenHour
					now.Add(14*time.Hour+30*time.Minute), // sunsetStart
					now.Add(15*time.Hour),                // sunset
					now.Add(15*time.Hour+30*time.Minute), // dusk
					now.Add(16*time.Hour),                // nauticalDusk
					now.Add(-1*time.Hour),                // night (past - we're in night sun event)
				)
			},
			schedule: &config.ParsedSchedule{
				BeginBackupWake: time.Date(now.Year(), now.Month(), now.Day(), 5, 0, 0, 0, now.Location()),
				BackupWakeTime:  time.Date(now.Year(), now.Month(), now.Day(), 7, 0, 0, 0, now.Location()),
				Dusk:            now.Add(-2 * time.Hour), // Schedule dusk in the past (we're past dusk)
				Night:           now.Add(2 * time.Hour),  // Schedule night time in the future
			},
			// Expected phase depends on current time: Night if hour < 6, otherwise Winddown
			expected: func() DayPhase {
				if now.Hour() < 6 {
					return DayPhaseNight
				}
				return DayPhaseWinddown
			}(),
		},
		{
			name: "night without schedule - late night",
			setupSunTimes: func(c *Calculator) {
				// Set sun times so current time falls in night period
				setSunTimesForTest(c, now,
					now.Add(4*time.Hour),                 // dawn
					now.Add(5*time.Hour),                 // sunrise
					now.Add(5*time.Hour+30*time.Minute),  // sunriseEnd
					now.Add(6*time.Hour),                 // goldenHourEnd
					now.Add(14*time.Hour),                // goldenHour
					now.Add(14*time.Hour+30*time.Minute), // sunsetStart
					now.Add(15*time.Hour),                // sunset
					now.Add(15*time.Hour+30*time.Minute), // dusk
					now.Add(16*time.Hour),                // nauticalDusk
					now.Add(-1*time.Hour),                // night (past)
				)
			},
			schedule: nil, // No schedule
			// Expected phase depends on current time: Night if hour >= 23 or < 6, otherwise Winddown
			expected: func() DayPhase {
				if now.Hour() >= 23 || now.Hour() < 6 {
					return DayPhaseNight
				}
				return DayPhaseWinddown
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calc := NewCalculator(32.85486, -97.50515, logger)
			tt.setupSunTimes(calc)

			phase := calc.CalculateDayPhase(tt.schedule)
			assert.Equal(t, tt.expected, phase, "Expected %s, got %s", tt.expected, phase)
		})
	}
}

// TestCalculator_CalculateDayPhaseEdgeCases tests edge cases in day phase calculation
func TestCalculator_CalculateDayPhaseEdgeCases(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	calc := NewCalculator(32.85486, -97.50515, logger)

	now := time.Now()

	// Test day sun event - sun has already risen (goldenHourEnd in past)
	setSunTimesForTest(calc, now,
		now.Add(-8*time.Hour),                // dawn
		now.Add(-7*time.Hour),                // sunrise
		now.Add(-6*time.Hour-30*time.Minute), // sunriseEnd
		now.Add(-6*time.Hour),                // goldenHourEnd
		now.Add(6*time.Hour),                 // goldenHour
		now.Add(6*time.Hour+30*time.Minute),  // sunsetStart
		now.Add(7*time.Hour),                 // sunset
		now.Add(7*time.Hour+30*time.Minute),  // dusk
		now.Add(8*time.Hour),                 // nauticalDusk
		now.Add(9*time.Hour),                 // night
	)

	// Since sun times show we're in "day" period (after goldenHourEnd), it should be day
	phase := calc.CalculateDayPhase(nil)
	assert.Equal(t, DayPhaseDay, phase)
}

func TestCalculator_AutoUpdateSunTimes(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	calc := NewCalculator(32.85486, -97.50515, logger)

	// Test that GetSunEvent auto-updates if lastUpdate is zero
	assert.True(t, calc.lastUpdate.IsZero())
	sunEvent := calc.GetSunEvent()
	assert.NotEmpty(t, sunEvent)
	assert.False(t, calc.lastUpdate.IsZero(), "GetSunEvent should trigger auto-update")

	// Test that it doesn't update if recent
	lastUpdate := calc.lastUpdate
	_ = calc.GetSunEvent()
	assert.Equal(t, lastUpdate, calc.lastUpdate, "Should not update if recent")
}

func TestCalculator_StartPeriodicUpdate(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	calc := NewCalculator(32.85486, -97.50515, logger)

	// Start periodic updates
	stopChan := calc.StartPeriodicUpdate()
	assert.NotNil(t, stopChan)

	// Verify initial update happened
	assert.False(t, calc.lastUpdate.IsZero())
	sunTimes := calc.GetSunTimes()
	assert.False(t, sunTimes["sunrise"].IsZero())

	// Stop the periodic updates
	close(stopChan)

	// Give it a moment to stop
	time.Sleep(100 * time.Millisecond)
}

func TestValidateDayPhase(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		expected  DayPhase
		shouldErr bool
	}{
		{"valid morning", "morning", DayPhaseMorning, false},
		{"valid day", "day", DayPhaseDay, false},
		{"valid sunset", "sunset", DayPhaseSunset, false},
		{"valid dusk", "dusk", DayPhaseDusk, false},
		{"valid winddown", "winddown", DayPhaseWinddown, false},
		{"valid night", "night", DayPhaseNight, false},
		{"invalid", "invalid", "", true},
		{"empty", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phase, err := ValidateDayPhase(tt.input)
			if tt.shouldErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, phase)
			}
		})
	}
}

func TestSunEventConstants(t *testing.T) {
	// Verify sun event constants are defined correctly
	assert.Equal(t, SunEvent("morning"), SunEventMorning)
	assert.Equal(t, SunEvent("day"), SunEventDay)
	assert.Equal(t, SunEvent("sunset"), SunEventSunset)
	assert.Equal(t, SunEvent("dusk"), SunEventDusk)
	assert.Equal(t, SunEvent("night"), SunEventNight)
}

func TestDayPhaseConstants(t *testing.T) {
	// Verify day phase constants are defined correctly
	assert.Equal(t, DayPhase("morning"), DayPhaseMorning)
	assert.Equal(t, DayPhase("day"), DayPhaseDay)
	assert.Equal(t, DayPhase("sunset"), DayPhaseSunset)
	assert.Equal(t, DayPhase("dusk"), DayPhaseDusk)
	assert.Equal(t, DayPhase("winddown"), DayPhaseWinddown)
	assert.Equal(t, DayPhase("night"), DayPhaseNight)
}

func TestCalculator_GetSunTimes(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	calc := NewCalculator(32.85486, -97.50515, logger)

	// Before update, should return empty map
	sunTimes := calc.GetSunTimes()
	assert.Empty(t, sunTimes)

	// After update, should return populated map
	calc.UpdateSunTimes()
	sunTimes = calc.GetSunTimes()
	assert.NotEmpty(t, sunTimes)
	assert.Contains(t, sunTimes, "sunrise")
	assert.Contains(t, sunTimes, "sunset")
	assert.Contains(t, sunTimes, "dusk")
	assert.Contains(t, sunTimes, "nauticalDusk")
	assert.Contains(t, sunTimes, "night")
}
