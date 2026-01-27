package dayphase

import (
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/config"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
)

func TestCalculator_UpdateSunTimes(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	// Austin, TX coordinates with UTC timezone for testing
	calc := NewCalculator(32.85486, -97.50515, time.UTC, logger)

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
	t.Parallel()
	logger := testlogger.New()
	calc := NewCalculator(32.85486, -97.50515, time.UTC, logger)

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
	t.Parallel()
	logger := testlogger.New()
	calc := NewCalculator(32.85486, -97.50515, time.UTC, logger)

	// Use a fixed reference time: January 15, 2024 at 10:00 AM in UTC
	// Using UTC ensures the test is deterministic regardless of the CI machine's timezone.
	fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	mockClock := clock.NewMockClock(fixedTime)
	calc.SetClock(mockClock)

	err := calc.UpdateSunTimes()
	assert.NoError(t, err)

	// Create a sample schedule based on the fixed time (using same UTC timezone)
	schedule := &config.ParsedSchedule{
		BeginBackupWake: time.Date(2024, 1, 15, 5, 0, 0, 0, time.UTC),
		BackupWakeTime:  time.Date(2024, 1, 15, 7, 0, 0, 0, time.UTC),
		Dusk:            time.Date(2024, 1, 15, 18, 0, 0, 0, time.UTC),
		Winddown:        time.Date(2024, 1, 15, 21, 0, 0, 0, time.UTC),
		StopScreens:     time.Date(2024, 1, 15, 22, 0, 0, 0, time.UTC),
		GoToBed:         time.Date(2024, 1, 15, 22, 30, 0, 0, time.UTC),
		Night:           time.Date(2024, 1, 15, 23, 0, 0, 0, time.UTC),
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
	t.Parallel()
	logger := testlogger.New()
	calc := NewCalculator(32.85486, -97.50515, time.UTC, logger)

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
	t.Parallel()
	logger := testlogger.New()

	// Different reference times for different test scenarios:
	// - Morning tests: 10:00 AM (before noon)
	// - Afternoon/evening tests: 3:00 PM (after noon)
	// - Night tests: 11:00 PM (late evening) or 5:00 AM (pre-dawn)
	morningTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	afternoonTime := time.Date(2024, 1, 15, 15, 0, 0, 0, time.UTC)
	eveningTime := time.Date(2024, 1, 15, 20, 0, 0, 0, time.UTC)
	preDawnTime := time.Date(2024, 1, 15, 5, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		testTime      time.Time
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
			testTime:      preDawnTime, // 5:00 AM - before dawn
			dawn:          preDawnTime.Add(2 * time.Hour),
			sunrise:       preDawnTime.Add(3 * time.Hour),
			sunriseEnd:    preDawnTime.Add(3*time.Hour + 30*time.Minute),
			goldenHourEnd: preDawnTime.Add(4 * time.Hour),
			goldenHour:    preDawnTime.Add(12 * time.Hour),
			sunsetStart:   preDawnTime.Add(12*time.Hour + 30*time.Minute),
			sunset:        preDawnTime.Add(13 * time.Hour),
			dusk:          preDawnTime.Add(13*time.Hour + 30*time.Minute),
			nauticalDusk:  preDawnTime.Add(14 * time.Hour),
			night:         preDawnTime.Add(15 * time.Hour),
			expected:      SunEventNight,
		},
		{
			name:          "dawn period - between dawn and sunrise",
			testTime:      morningTime, // 10:00 AM - morning
			dawn:          morningTime.Add(-30 * time.Minute),
			sunrise:       morningTime.Add(30 * time.Minute),
			sunriseEnd:    morningTime.Add(1 * time.Hour),
			goldenHourEnd: morningTime.Add(90 * time.Minute),
			goldenHour:    morningTime.Add(10 * time.Hour),
			sunsetStart:   morningTime.Add(10*time.Hour + 30*time.Minute),
			sunset:        morningTime.Add(11 * time.Hour),
			dusk:          morningTime.Add(11*time.Hour + 30*time.Minute),
			nauticalDusk:  morningTime.Add(12 * time.Hour),
			night:         morningTime.Add(13 * time.Hour),
			expected:      SunEventMorning,
		},
		{
			name:          "sunrise period - between sunrise and golden hour end",
			testTime:      morningTime, // 10:00 AM - morning
			dawn:          morningTime.Add(-2 * time.Hour),
			sunrise:       morningTime.Add(-1 * time.Hour),
			sunriseEnd:    morningTime.Add(-30 * time.Minute),
			goldenHourEnd: morningTime.Add(1 * time.Hour),
			goldenHour:    morningTime.Add(10 * time.Hour),
			sunsetStart:   morningTime.Add(10*time.Hour + 30*time.Minute),
			sunset:        morningTime.Add(11 * time.Hour),
			dusk:          morningTime.Add(11*time.Hour + 30*time.Minute),
			nauticalDusk:  morningTime.Add(12 * time.Hour),
			night:         morningTime.Add(13 * time.Hour),
			expected:      SunEventMorning,
		},
		{
			name:          "day - between noon and golden hour start",
			testTime:      afternoonTime, // 3:00 PM - afternoon (after noon)
			dawn:          afternoonTime.Add(-9 * time.Hour),
			sunrise:       afternoonTime.Add(-8 * time.Hour),
			sunriseEnd:    afternoonTime.Add(-7*time.Hour - 30*time.Minute),
			goldenHourEnd: afternoonTime.Add(-7 * time.Hour),
			goldenHour:    afternoonTime.Add(2 * time.Hour),
			sunsetStart:   afternoonTime.Add(2*time.Hour + 30*time.Minute),
			sunset:        afternoonTime.Add(3 * time.Hour),
			dusk:          afternoonTime.Add(3*time.Hour + 30*time.Minute),
			nauticalDusk:  afternoonTime.Add(4 * time.Hour),
			night:         afternoonTime.Add(5 * time.Hour),
			expected:      SunEventDay,
		},
		{
			name:          "sunset - golden hour",
			testTime:      eveningTime, // 8:00 PM - evening
			dawn:          eveningTime.Add(-14 * time.Hour),
			sunrise:       eveningTime.Add(-13 * time.Hour),
			sunriseEnd:    eveningTime.Add(-12*time.Hour - 30*time.Minute),
			goldenHourEnd: eveningTime.Add(-12 * time.Hour),
			goldenHour:    eveningTime.Add(-1 * time.Hour),
			sunsetStart:   eveningTime.Add(-30 * time.Minute),
			sunset:        eveningTime.Add(30 * time.Minute),
			dusk:          eveningTime.Add(1 * time.Hour),
			nauticalDusk:  eveningTime.Add(1*time.Hour + 30*time.Minute),
			night:         eveningTime.Add(2 * time.Hour),
			expected:      SunEventSunset,
		},
		{
			name:          "dusk - civil twilight (between dusk and night)",
			testTime:      eveningTime, // 8:00 PM - evening
			dawn:          eveningTime.Add(-14 * time.Hour),
			sunrise:       eveningTime.Add(-13 * time.Hour),
			sunriseEnd:    eveningTime.Add(-12*time.Hour - 30*time.Minute),
			goldenHourEnd: eveningTime.Add(-12 * time.Hour),
			goldenHour:    eveningTime.Add(-3 * time.Hour),
			sunsetStart:   eveningTime.Add(-2*time.Hour - 30*time.Minute),
			sunset:        eveningTime.Add(-2 * time.Hour),
			dusk:          eveningTime.Add(-1 * time.Hour),
			nauticalDusk:  eveningTime.Add(-30 * time.Minute),
			night:         eveningTime.Add(1 * time.Hour), // Night is in future, so we're in dusk period
			expected:      SunEventDusk,
		},
		{
			name:          "after night starts - night",
			testTime:      eveningTime, // 8:00 PM - evening (after sunset/night has started)
			dawn:          eveningTime.Add(-16 * time.Hour),
			sunrise:       eveningTime.Add(-15 * time.Hour),
			sunriseEnd:    eveningTime.Add(-14*time.Hour - 30*time.Minute),
			goldenHourEnd: eveningTime.Add(-14 * time.Hour),
			goldenHour:    eveningTime.Add(-5 * time.Hour),
			sunsetStart:   eveningTime.Add(-4*time.Hour - 30*time.Minute),
			sunset:        eveningTime.Add(-4 * time.Hour),
			dusk:          eveningTime.Add(-3 * time.Hour),
			nauticalDusk:  eveningTime.Add(-2 * time.Hour),
			night:         eveningTime.Add(-1 * time.Hour), // Night has already started
			expected:      SunEventNight,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calc := NewCalculator(32.85486, -97.50515, time.UTC, logger)
			mockClock := clock.NewMockClock(tt.testTime)
			calc.SetClock(mockClock)

			// Set sun times to create the desired test scenario
			setSunTimesForTest(calc, tt.testTime,
				tt.dawn, tt.sunrise, tt.sunriseEnd, tt.goldenHourEnd,
				tt.goldenHour, tt.sunsetStart, tt.sunset,
				tt.dusk, tt.nauticalDusk, tt.night)

			sunEvent := calc.GetSunEvent()
			assert.Equal(t, tt.expected, sunEvent, "Expected %s, got %s", tt.expected, sunEvent)
		})
	}
}

func TestCalculator_CalculateDayPhaseAllCases(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()

	// Different reference times for different test scenarios:
	// - Morning tests: 10:00 AM (before noon)
	// - Day tests: 3:00 PM (afternoon, after noon)
	// - Evening tests: 7:00 PM (evening)
	morningTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	afternoonTime := time.Date(2024, 1, 15, 15, 0, 0, 0, time.UTC)
	eveningTime := time.Date(2024, 1, 15, 19, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		testTime      time.Time
		setupSunTimes func(c *Calculator, now time.Time)
		schedule      *config.ParsedSchedule
		expected      DayPhase
	}{
		{
			name:     "morning period",
			testTime: morningTime,
			setupSunTimes: func(c *Calculator, now time.Time) {
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
			schedule: &config.ParsedSchedule{
				BeginBackupWake: time.Date(2024, 1, 15, 5, 0, 0, 0, time.UTC),
				BackupWakeTime:  time.Date(2024, 1, 15, 7, 0, 0, 0, time.UTC),
				Dusk:            time.Date(2024, 1, 15, 18, 0, 0, 0, time.UTC),
				Winddown:        time.Date(2024, 1, 15, 21, 0, 0, 0, time.UTC),
				StopScreens:     time.Date(2024, 1, 15, 22, 0, 0, 0, time.UTC),
				GoToBed:         time.Date(2024, 1, 15, 22, 30, 0, 0, time.UTC),
				Night:           time.Date(2024, 1, 15, 23, 0, 0, 0, time.UTC),
			},
			// At 10am (fixed time), sun event is morning -> returns morning
			expected: DayPhaseMorning,
		},
		{
			name:     "day phase",
			testTime: afternoonTime, // Use afternoon time (after noon)
			setupSunTimes: func(c *Calculator, now time.Time) {
				// Set sun times so current time falls in day period
				setSunTimesForTest(c, now,
					now.Add(-9*time.Hour),                // dawn
					now.Add(-8*time.Hour),                // sunrise
					now.Add(-7*time.Hour-30*time.Minute), // sunriseEnd
					now.Add(-7*time.Hour),                // goldenHourEnd
					now.Add(3*time.Hour),                 // goldenHour
					now.Add(3*time.Hour+30*time.Minute),  // sunsetStart
					now.Add(4*time.Hour),                 // sunset
					now.Add(4*time.Hour+30*time.Minute),  // dusk
					now.Add(5*time.Hour),                 // nauticalDusk
					now.Add(6*time.Hour),                 // night
				)
			},
			schedule: &config.ParsedSchedule{
				BeginBackupWake: time.Date(2024, 1, 15, 5, 0, 0, 0, time.UTC),
				BackupWakeTime:  time.Date(2024, 1, 15, 7, 0, 0, 0, time.UTC),
				Dusk:            time.Date(2024, 1, 15, 18, 0, 0, 0, time.UTC),
				Winddown:        time.Date(2024, 1, 15, 21, 0, 0, 0, time.UTC),
				StopScreens:     time.Date(2024, 1, 15, 22, 0, 0, 0, time.UTC),
				GoToBed:         time.Date(2024, 1, 15, 22, 30, 0, 0, time.UTC),
				Night:           time.Date(2024, 1, 15, 23, 0, 0, 0, time.UTC),
			},
			// At 3pm (afternoon), sun event is day -> returns day
			expected: DayPhaseDay,
		},
		{
			name:     "sunset phase",
			testTime: eveningTime, // Use evening time
			setupSunTimes: func(c *Calculator, now time.Time) {
				// Set sun times so current time falls in sunset period (golden hour)
				setSunTimesForTest(c, now,
					now.Add(-13*time.Hour),                // dawn
					now.Add(-12*time.Hour),                // sunrise
					now.Add(-11*time.Hour-30*time.Minute), // sunriseEnd
					now.Add(-11*time.Hour),                // goldenHourEnd
					now.Add(-1*time.Hour),                 // goldenHour (in golden hour)
					now.Add(-30*time.Minute),              // sunsetStart
					now.Add(30*time.Minute),               // sunset
					now.Add(1*time.Hour),                  // dusk (future)
					now.Add(1*time.Hour+30*time.Minute),   // nauticalDusk
					now.Add(2*time.Hour),                  // night
				)
			},
			schedule: &config.ParsedSchedule{
				BeginBackupWake: time.Date(2024, 1, 15, 5, 0, 0, 0, time.UTC),
				BackupWakeTime:  time.Date(2024, 1, 15, 7, 0, 0, 0, time.UTC),
				Dusk:            time.Date(2024, 1, 15, 20, 0, 0, 0, time.UTC), // After 7pm
				Winddown:        time.Date(2024, 1, 15, 21, 0, 0, 0, time.UTC),
				StopScreens:     time.Date(2024, 1, 15, 22, 0, 0, 0, time.UTC),
				GoToBed:         time.Date(2024, 1, 15, 22, 30, 0, 0, time.UTC),
				Night:           time.Date(2024, 1, 15, 23, 0, 0, 0, time.UTC),
			},
			// At 7pm (evening), sun event is sunset -> returns sunset
			expected: DayPhaseSunset,
		},
		{
			name:     "dusk phase - after scheduled dusk time",
			testTime: eveningTime, // Use evening time
			setupSunTimes: func(c *Calculator, now time.Time) {
				// Set sun times so current time falls in dusk period (between dusk and night)
				setSunTimesForTest(c, now,
					now.Add(-13*time.Hour),                // dawn
					now.Add(-12*time.Hour),                // sunrise
					now.Add(-11*time.Hour-30*time.Minute), // sunriseEnd
					now.Add(-11*time.Hour),                // goldenHourEnd
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
				BeginBackupWake: time.Date(2024, 1, 15, 5, 0, 0, 0, time.UTC),
				BackupWakeTime:  time.Date(2024, 1, 15, 7, 0, 0, 0, time.UTC),
				Dusk:            time.Date(2024, 1, 15, 17, 0, 0, 0, time.UTC), // Before 7pm
				Winddown:        time.Date(2024, 1, 15, 21, 0, 0, 0, time.UTC),
				Night:           time.Date(2024, 1, 15, 21, 0, 0, 0, time.UTC), // Night in the future
			},
			// At 7pm (evening), sun event is dusk and schedule.Dusk is past -> returns dusk
			expected: DayPhaseDusk,
		},
		{
			name:     "sunset phase - after scheduled dusk but sun still at sunset",
			testTime: eveningTime, // Use evening time
			setupSunTimes: func(c *Calculator, now time.Time) {
				// Sun is in sunset period (golden hour to dusk)
				// schedule.Dusk has passed but astronomical dusk hasn't
				setSunTimesForTest(c, now,
					now.Add(-13*time.Hour),                // dawn
					now.Add(-12*time.Hour),                // sunrise
					now.Add(-11*time.Hour-30*time.Minute), // sunriseEnd
					now.Add(-11*time.Hour),                // goldenHourEnd
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
				BeginBackupWake: time.Date(2024, 1, 15, 5, 0, 0, 0, time.UTC),
				BackupWakeTime:  time.Date(2024, 1, 15, 7, 0, 0, 0, time.UTC),
				Dusk:            time.Date(2024, 1, 15, 18, 30, 0, 0, time.UTC), // Before 7pm
				Winddown:        time.Date(2024, 1, 15, 20, 0, 0, 0, time.UTC),
				Night:           time.Date(2024, 1, 15, 22, 0, 0, 0, time.UTC), // Night in the future
			},
			// At 7pm (evening), sun says sunset -> returns sunset
			expected: DayPhaseSunset,
		},
		{
			name:     "dusk override - sun says dusk but before scheduled dusk",
			testTime: eveningTime, // Use evening time
			setupSunTimes: func(c *Calculator, now time.Time) {
				// Set sun times so sun event is dusk (past dusk, before night)
				setSunTimesForTest(c, now,
					now.Add(-13*time.Hour),                // dawn
					now.Add(-12*time.Hour),                // sunrise
					now.Add(-11*time.Hour-30*time.Minute), // sunriseEnd
					now.Add(-11*time.Hour),                // goldenHourEnd
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
				BeginBackupWake: time.Date(2024, 1, 15, 5, 0, 0, 0, time.UTC),
				BackupWakeTime:  time.Date(2024, 1, 15, 7, 0, 0, 0, time.UTC),
				Dusk:            time.Date(2024, 1, 15, 20, 0, 0, 0, time.UTC), // After 7pm
				Winddown:        time.Date(2024, 1, 15, 21, 0, 0, 0, time.UTC),
				Night:           time.Date(2024, 1, 15, 22, 0, 0, 0, time.UTC),
			},
			// At 7pm (evening), sun says dusk but schedule.Dusk is future -> delayed to sunset
			expected: DayPhaseSunset,
		},
		{
			name:     "night override - sun says night but before scheduled dusk (evening)",
			testTime: morningTime, // Use morning time for this pre-dawn scenario
			setupSunTimes: func(c *Calculator, now time.Time) {
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
				BeginBackupWake: time.Date(2024, 1, 15, 5, 0, 0, 0, time.UTC),
				BackupWakeTime:  time.Date(2024, 1, 15, 7, 0, 0, 0, time.UTC),
				Dusk:            morningTime.Add(1 * time.Hour), // Schedule dusk in the future
				Winddown:        morningTime.Add(2 * time.Hour),
				Night:           morningTime.Add(3 * time.Hour),
			},
			// At 10am (fixed time, before noon), sun says night -> returns night (pre-dawn logic)
			expected: DayPhaseNight,
		},
		{
			name:     "pre-dawn morning - sun says night but it's 6-11am (before noon)",
			testTime: morningTime,
			setupSunTimes: func(c *Calculator, now time.Time) {
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
				BeginBackupWake: time.Date(2024, 1, 15, 5, 0, 0, 0, time.UTC),
				BackupWakeTime:  time.Date(2024, 1, 15, 7, 0, 0, 0, time.UTC),
				Dusk:            morningTime.Add(4 * time.Hour), // Schedule dusk in the future
				Winddown:        morningTime.Add(5 * time.Hour), // Schedule winddown in the future
				Night:           morningTime.Add(6 * time.Hour), // Schedule night in the future
			},
			// At 10am (fixed time, before noon), sun says night -> returns night (pre-dawn logic)
			expected: DayPhaseNight,
		},
		{
			name:     "night with schedule - after schedule.Night",
			testTime: eveningTime,
			setupSunTimes: func(c *Calculator, now time.Time) {
				// Set sun times so current time falls in night period
				setSunTimesForTest(c, now,
					now.Add(8*time.Hour),                 // dawn (next morning)
					now.Add(9*time.Hour),                 // sunrise
					now.Add(9*time.Hour+30*time.Minute),  // sunriseEnd
					now.Add(10*time.Hour),                // goldenHourEnd
					now.Add(18*time.Hour),                // goldenHour
					now.Add(18*time.Hour+30*time.Minute), // sunsetStart
					now.Add(19*time.Hour),                // sunset
					now.Add(19*time.Hour+30*time.Minute), // dusk
					now.Add(20*time.Hour),                // nauticalDusk
					now.Add(-1*time.Hour),                // night (past - we're in night)
				)
			},
			schedule: &config.ParsedSchedule{
				BeginBackupWake: time.Date(2024, 1, 15, 5, 0, 0, 0, time.UTC),
				BackupWakeTime:  time.Date(2024, 1, 15, 7, 0, 0, 0, time.UTC),
				Dusk:            time.Date(2024, 1, 15, 15, 0, 0, 0, time.UTC), // Before 7pm
				Night:           time.Date(2024, 1, 15, 17, 0, 0, 0, time.UTC), // Before 7pm
			},
			expected: DayPhaseNight,
		},
		{
			name:     "dusk phase - astronomical night but before scheduled winddown",
			testTime: eveningTime, // 7pm
			setupSunTimes: func(c *Calculator, now time.Time) {
				// Set sun times so current time is in astronomical night (sun event = night)
				// This happens in winter when astronomical night occurs before the scheduled winddown time
				setSunTimesForTest(c, now,
					now.Add(8*time.Hour),                 // dawn
					now.Add(9*time.Hour),                 // sunrise
					now.Add(9*time.Hour+30*time.Minute),  // sunriseEnd
					now.Add(10*time.Hour),                // goldenHourEnd
					now.Add(18*time.Hour),                // goldenHour
					now.Add(18*time.Hour+30*time.Minute), // sunsetStart
					now.Add(19*time.Hour),                // sunset
					now.Add(19*time.Hour+30*time.Minute), // dusk
					now.Add(20*time.Hour),                // nauticalDusk
					now.Add(-1*time.Hour),                // night (past - sun event is night)
				)
			},
			schedule: &config.ParsedSchedule{
				BeginBackupWake: time.Date(2024, 1, 15, 5, 0, 0, 0, time.UTC),
				BackupWakeTime:  time.Date(2024, 1, 15, 7, 0, 0, 0, time.UTC),
				Dusk:            time.Date(2024, 1, 15, 17, 0, 0, 0, time.UTC),  // Before 7pm - we're after this
				Winddown:        time.Date(2024, 1, 15, 22, 15, 0, 0, time.UTC), // 10:15pm - we're before this
				Night:           time.Date(2024, 1, 15, 23, 0, 0, 0, time.UTC),  // 11pm
			},
			// At 7pm, after schedule.Dusk but BEFORE schedule.Winddown -> stay at dusk
			// This fixes the bug where astronomical night caused immediate jump to winddown
			expected: DayPhaseDusk,
		},
		{
			name:     "winddown phase - after scheduled winddown time",
			testTime: eveningTime, // 7pm - we'll use a later winddown time to test the logic
			setupSunTimes: func(c *Calculator, now time.Time) {
				// Set sun times so current time is in astronomical night
				setSunTimesForTest(c, now,
					now.Add(8*time.Hour),                 // dawn
					now.Add(9*time.Hour),                 // sunrise
					now.Add(9*time.Hour+30*time.Minute),  // sunriseEnd
					now.Add(10*time.Hour),                // goldenHourEnd
					now.Add(18*time.Hour),                // goldenHour
					now.Add(18*time.Hour+30*time.Minute), // sunsetStart
					now.Add(19*time.Hour),                // sunset
					now.Add(19*time.Hour+30*time.Minute), // dusk
					now.Add(20*time.Hour),                // nauticalDusk
					now.Add(-1*time.Hour),                // night (past - sun event is night)
				)
			},
			schedule: &config.ParsedSchedule{
				BeginBackupWake: time.Date(2024, 1, 15, 5, 0, 0, 0, time.UTC),
				BackupWakeTime:  time.Date(2024, 1, 15, 7, 0, 0, 0, time.UTC),
				Dusk:            time.Date(2024, 1, 15, 17, 0, 0, 0, time.UTC), // Before 7pm
				Winddown:        time.Date(2024, 1, 15, 18, 0, 0, 0, time.UTC), // 6pm - we're AFTER this
				Night:           time.Date(2024, 1, 15, 21, 0, 0, 0, time.UTC), // 9pm - we're before this
			},
			// At 7pm, after schedule.Winddown (6pm), before schedule.Night (9pm) -> winddown
			expected: DayPhaseWinddown,
		},
		{
			name:     "night without schedule - late night",
			testTime: eveningTime,
			setupSunTimes: func(c *Calculator, now time.Time) {
				// Set sun times so current time falls in night period
				setSunTimesForTest(c, now,
					now.Add(8*time.Hour),                 // dawn
					now.Add(9*time.Hour),                 // sunrise
					now.Add(9*time.Hour+30*time.Minute),  // sunriseEnd
					now.Add(10*time.Hour),                // goldenHourEnd
					now.Add(18*time.Hour),                // goldenHour
					now.Add(18*time.Hour+30*time.Minute), // sunsetStart
					now.Add(19*time.Hour),                // sunset
					now.Add(19*time.Hour+30*time.Minute), // dusk
					now.Add(20*time.Hour),                // nauticalDusk
					now.Add(-1*time.Hour),                // night (past)
				)
			},
			schedule: nil, // No schedule
			// At 7pm (evening), no schedule, sun says night -> winddown (not late night hours)
			expected: DayPhaseWinddown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calc := NewCalculator(32.85486, -97.50515, time.UTC, logger)
			// Inject mock clock set to our test time
			mockClock := clock.NewMockClock(tt.testTime)
			calc.SetClock(mockClock)
			tt.setupSunTimes(calc, tt.testTime)

			phase := calc.CalculateDayPhase(tt.schedule)
			assert.Equal(t, tt.expected, phase, "Expected %s, got %s", tt.expected, phase)
		})
	}
}

// TestCalculator_CalculateDayPhaseEdgeCases tests edge cases in day phase calculation
func TestCalculator_CalculateDayPhaseEdgeCases(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	calc := NewCalculator(32.85486, -97.50515, time.UTC, logger)

	// Use a fixed reference time: January 15, 2024 at 3:00 PM in UTC (afternoon, after noon)
	// Using UTC ensures the test is deterministic regardless of the CI machine's timezone.
	now := time.Date(2024, 1, 15, 15, 0, 0, 0, time.UTC)
	mockClock := clock.NewMockClock(now)
	calc.SetClock(mockClock)

	// Test day sun event - afternoon time (after noon) with sun times showing day period
	setSunTimesForTest(calc, now,
		now.Add(-9*time.Hour),                // dawn
		now.Add(-8*time.Hour),                // sunrise
		now.Add(-7*time.Hour-30*time.Minute), // sunriseEnd
		now.Add(-7*time.Hour),                // goldenHourEnd
		now.Add(3*time.Hour),                 // goldenHour
		now.Add(3*time.Hour+30*time.Minute),  // sunsetStart
		now.Add(4*time.Hour),                 // sunset
		now.Add(4*time.Hour+30*time.Minute),  // dusk
		now.Add(5*time.Hour),                 // nauticalDusk
		now.Add(6*time.Hour),                 // night
	)

	// Since it's afternoon (after noon) and before golden hour, it should be day
	phase := calc.CalculateDayPhase(nil)
	assert.Equal(t, DayPhaseDay, phase)
}

func TestCalculator_AutoUpdateSunTimes(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	calc := NewCalculator(32.85486, -97.50515, time.UTC, logger)

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
	t.Parallel()
	logger := testlogger.New()
	calc := NewCalculator(32.85486, -97.50515, time.UTC, logger)

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
	t.Parallel()
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
	t.Parallel(
	// Verify sun event constants are defined correctly
	)

	assert.Equal(t, SunEvent("morning"), SunEventMorning)
	assert.Equal(t, SunEvent("day"), SunEventDay)
	assert.Equal(t, SunEvent("sunset"), SunEventSunset)
	assert.Equal(t, SunEvent("dusk"), SunEventDusk)
	assert.Equal(t, SunEvent("night"), SunEventNight)
}

func TestDayPhaseConstants(t *testing.T) {
	t.Parallel(
	// Verify day phase constants are defined correctly
	)

	assert.Equal(t, DayPhase("morning"), DayPhaseMorning)
	assert.Equal(t, DayPhase("day"), DayPhaseDay)
	assert.Equal(t, DayPhase("sunset"), DayPhaseSunset)
	assert.Equal(t, DayPhase("dusk"), DayPhaseDusk)
	assert.Equal(t, DayPhase("winddown"), DayPhaseWinddown)
	assert.Equal(t, DayPhase("night"), DayPhaseNight)
}

func TestCalculator_GetSunTimes(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	calc := NewCalculator(32.85486, -97.50515, time.UTC, logger)

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

// TestCalculator_GetSunEvent_MidnightUTCBug tests the bug where day phase jumped to
// "morning" at midnight UTC (6 PM CST) because the noon comparison used UTC instead
// of the configured local timezone.
//
// Bug: At 00:03 UTC (6:03 PM CST), the system incorrectly returned "morning" because:
// - now.Location() returned UTC (since the system runs in UTC)
// - noonToday was 12:00 UTC, not 12:00 CST
// - The check "00:03 < 12:00" was true, so it returned morning
//
// Fix: Use the configured timezone for the noon comparison.
func TestCalculator_GetSunEvent_MidnightUTCBug(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()

	// Load CST timezone (America/Chicago)
	cst, err := time.LoadLocation("America/Chicago")
	assert.NoError(t, err)

	// Create calculator with CST timezone - this is the fix
	calc := NewCalculator(32.85486, -97.50515, cst, logger)

	// Simulate the bug scenario: 00:03 UTC on Jan 10, 2026
	// This is 6:03 PM CST on Jan 9, 2026 (during evening, not morning!)
	utcTime := time.Date(2026, 1, 10, 0, 3, 0, 0, time.UTC)
	mockClock := clock.NewMockClock(utcTime)
	calc.SetClock(mockClock)

	// Set up sun times for Austin, TX on Jan 9, 2026 (in UTC)
	// Dawn ~7:05 AM CST = 13:05 UTC
	// Sunrise ~7:30 AM CST = 13:30 UTC
	// Sunset ~5:45 PM CST = 23:45 UTC
	// Dusk ~6:10 PM CST = 00:10 UTC Jan 10
	// Night ~6:45 PM CST = 00:45 UTC Jan 10
	setSunTimesForTest(calc, utcTime,
		time.Date(2026, 1, 9, 13, 5, 0, 0, time.UTC),  // dawn
		time.Date(2026, 1, 9, 13, 30, 0, 0, time.UTC), // sunrise
		time.Date(2026, 1, 9, 13, 45, 0, 0, time.UTC), // sunriseEnd
		time.Date(2026, 1, 9, 14, 30, 0, 0, time.UTC), // goldenHourEnd
		time.Date(2026, 1, 9, 23, 0, 0, 0, time.UTC),  // goldenHour
		time.Date(2026, 1, 9, 23, 30, 0, 0, time.UTC), // sunsetStart
		time.Date(2026, 1, 9, 23, 45, 0, 0, time.UTC), // sunset
		time.Date(2026, 1, 10, 0, 10, 0, 0, time.UTC), // dusk (future - at 00:03 we're in sunset)
		time.Date(2026, 1, 10, 0, 30, 0, 0, time.UTC), // nauticalDusk
		time.Date(2026, 1, 10, 0, 45, 0, 0, time.UTC), // night
	)

	// Get sun event - should be "sunset", NOT "morning"
	sunEvent := calc.GetSunEvent()

	// Before the fix, this would return SunEventMorning because:
	// - 00:03 UTC < 12:00 UTC (noon in UTC) = true
	// After the fix, it correctly returns SunEventSunset because:
	// - 6:03 PM CST is after 12:00 PM CST (noon in local time)
	// - And we're in the golden hour period (after goldenHour, before dusk)
	assert.Equal(t, SunEventSunset, sunEvent,
		"Expected sunset at 6:03 PM CST (00:03 UTC), not morning. "+
			"This validates the timezone fix for the noon comparison.")
}

// TestCalculator_GetSunEvent_TimezoneAwareness verifies that the calculator
// correctly uses the configured timezone for all time comparisons.
func TestCalculator_GetSunEvent_TimezoneAwareness(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()

	// Test with multiple timezones
	testCases := []struct {
		name           string
		timezone       string
		utcHour        int    // Hour in UTC
		expectedPhase  string // What we expect after the fix
		beforeFixPhase string // What the bug would have returned
	}{
		{
			name:           "CST evening looks like UTC morning",
			timezone:       "America/Chicago",
			utcHour:        0, // 6 PM CST
			expectedPhase:  "evening phase (sunset/dusk/night)",
			beforeFixPhase: "morning (bug)",
		},
		{
			name:           "EST evening looks like UTC morning",
			timezone:       "America/New_York",
			utcHour:        1, // 8 PM EST
			expectedPhase:  "evening phase (sunset/dusk/night)",
			beforeFixPhase: "morning (bug)",
		},
		{
			name:           "PST evening looks like UTC morning",
			timezone:       "America/Los_Angeles",
			utcHour:        3, // 7 PM PST
			expectedPhase:  "evening phase (sunset/dusk/night)",
			beforeFixPhase: "morning (bug)",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tz, err := time.LoadLocation(tc.timezone)
			assert.NoError(t, err)

			// Create calculator with the test timezone
			calc := NewCalculator(32.85486, -97.50515, tz, logger)

			// Set clock to a time that would trigger the bug
			utcTime := time.Date(2026, 1, 10, tc.utcHour, 0, 0, 0, time.UTC)
			mockClock := clock.NewMockClock(utcTime)
			calc.SetClock(mockClock)

			// Set up sun times (evening in the local timezone)
			// Dawn and sunrise in the past (morning is over)
			// Golden hour just started (it's evening)
			localTime := utcTime.In(tz)
			dawn := time.Date(localTime.Year(), localTime.Month(), localTime.Day(), 7, 0, 0, 0, tz)
			sunrise := time.Date(localTime.Year(), localTime.Month(), localTime.Day(), 7, 30, 0, 0, tz)
			sunriseEnd := time.Date(localTime.Year(), localTime.Month(), localTime.Day(), 7, 45, 0, 0, tz)
			goldenHourEnd := time.Date(localTime.Year(), localTime.Month(), localTime.Day(), 8, 30, 0, 0, tz)
			goldenHour := time.Date(localTime.Year(), localTime.Month(), localTime.Day(), 17, 0, 0, 0, tz)
			sunsetStart := time.Date(localTime.Year(), localTime.Month(), localTime.Day(), 17, 30, 0, 0, tz)
			sunset := time.Date(localTime.Year(), localTime.Month(), localTime.Day(), 17, 45, 0, 0, tz)
			dusk := time.Date(localTime.Year(), localTime.Month(), localTime.Day(), 18, 10, 0, 0, tz)
			nauticalDusk := time.Date(localTime.Year(), localTime.Month(), localTime.Day(), 18, 30, 0, 0, tz)
			night := time.Date(localTime.Year(), localTime.Month(), localTime.Day(), 18, 45, 0, 0, tz)

			setSunTimesForTest(calc, utcTime,
				dawn.UTC(), sunrise.UTC(), sunriseEnd.UTC(), goldenHourEnd.UTC(),
				goldenHour.UTC(), sunsetStart.UTC(), sunset.UTC(),
				dusk.UTC(), nauticalDusk.UTC(), night.UTC(),
			)

			sunEvent := calc.GetSunEvent()

			// Should NOT be morning - that was the bug
			assert.NotEqual(t, SunEventMorning, sunEvent,
				"Should not return morning in the evening. Timezone: %s, UTC hour: %d, Local time: %s",
				tc.timezone, tc.utcHour, localTime.Format("15:04 MST"))
		})
	}
}

// TestCalculator_CalculateDayPhase_TimezoneHandling verifies that the fallback path
// in CalculateDayPhase correctly uses local timezone for hour comparisons.
// This is a regression test for issue #449.
//
// Note: Issue #446 (fixed in PR #447) also fixed GetSunEvent timezone handling,
// so we need to use times after astronomical night to produce SunEventNight.
func TestCalculator_CalculateDayPhase_TimezoneHandling(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()

	// Load CST timezone (America/Chicago, UTC-6 in winter)
	cst, err := time.LoadLocation("America/Chicago")
	assert.NoError(t, err)

	// Create calculator with CST timezone
	calc := NewCalculator(32.85486, -97.50515, cst, logger)

	// Test Case 1: Evening time where UTC hour would incorrectly suggest night
	// Using 8 PM UTC = 2 PM CST (afternoon)
	// The bug was: at times like midnight UTC (6 PM CST), the code used UTC hour (0)
	// which is < 6, so it would return DayPhaseNight instead of DayPhaseWinddown.
	//
	// At 2 PM local (after noon), if sun event is night, should return Winddown (not Night)
	// because 14:00 is NOT >= 23 and NOT < 6.
	testTimeUTC := time.Date(2024, 1, 15, 20, 0, 0, 0, time.UTC) // 8 PM UTC = 2 PM CST
	mockClock := clock.NewMockClock(testTimeUTC)
	calc.SetClock(mockClock)

	// Set up sun times so that now is after sunTimes["night"]
	// This triggers the default case in GetSunEvent -> SunEventNight
	setSunTimesForTest(calc, testTimeUTC,
		testTimeUTC.Add(-14*time.Hour),   // dawn (past)
		testTimeUTC.Add(-13*time.Hour),   // sunrise (past)
		testTimeUTC.Add(-12*time.Hour),   // sunriseEnd (past)
		testTimeUTC.Add(-11*time.Hour),   // goldenHourEnd (past)
		testTimeUTC.Add(-3*time.Hour),    // goldenHour (past)
		testTimeUTC.Add(-2*time.Hour),    // sunsetStart (past)
		testTimeUTC.Add(-90*time.Minute), // sunset (past)
		testTimeUTC.Add(-60*time.Minute), // dusk (past)
		testTimeUTC.Add(-45*time.Minute), // nauticalDusk (past)
		testTimeUTC.Add(-30*time.Minute), // night (past - so we're after night = SunEventNight)
	)

	// Verify GetSunEvent returns night
	sunEvent := calc.GetSunEvent()
	assert.Equal(t, SunEventNight, sunEvent, "Expected SunEventNight at 8 PM UTC (after astronomical night)")

	// Without schedule (nil), it should use the fallback path with timezone
	dayPhase := calc.CalculateDayPhase(nil)

	// At 2 PM local time (14:00 CST), the fallback logic should return Winddown
	// because 14 is NOT >= 23 and NOT < 6
	assert.Equal(t, DayPhaseWinddown, dayPhase,
		"At 2 PM local time (8 PM UTC), with SunEventNight, should return Winddown")

	// Test Case 2: Late night in local time (11 PM CST = 5 AM UTC next day)
	// 5 AM UTC is before noon UTC, so we need dawn in the future for SunEventNight
	testTime11PM := time.Date(2024, 1, 16, 5, 0, 0, 0, time.UTC) // 11 PM CST on Jan 15
	mockClock = clock.NewMockClock(testTime11PM)
	calc.SetClock(mockClock)

	// Set sun times so that now is before dawn (SunEventNight returns from first case)
	setSunTimesForTest(calc, testTime11PM,
		testTime11PM.Add(2*time.Hour),  // dawn (future - before dawn = SunEventNight)
		testTime11PM.Add(3*time.Hour),  // sunrise (future)
		testTime11PM.Add(4*time.Hour),  // sunriseEnd (future)
		testTime11PM.Add(5*time.Hour),  // goldenHourEnd (future)
		testTime11PM.Add(13*time.Hour), // goldenHour (future)
		testTime11PM.Add(14*time.Hour), // sunsetStart (future)
		testTime11PM.Add(15*time.Hour), // sunset (future)
		testTime11PM.Add(16*time.Hour), // dusk (future)
		testTime11PM.Add(17*time.Hour), // nauticalDusk (future)
		testTime11PM.Add(18*time.Hour), // night (future)
	)

	// Verify GetSunEvent returns night (before dawn)
	sunEvent = calc.GetSunEvent()
	assert.Equal(t, SunEventNight, sunEvent, "Expected SunEventNight at 5 AM UTC (before dawn)")

	dayPhase = calc.CalculateDayPhase(nil)

	// At 11 PM local time (23:00 CST), the fallback logic should return Night
	// because 23 >= 23
	assert.Equal(t, DayPhaseNight, dayPhase,
		"At 11 PM local time (5 AM UTC), should return Night")

	// Test Case 3: Early morning in local time (3 AM CST = 9 AM UTC)
	// This tests the other branch of the night hour check (hour < 6)
	testTime3AM := time.Date(2024, 1, 15, 9, 0, 0, 0, time.UTC) // 3 AM CST
	mockClock = clock.NewMockClock(testTime3AM)
	calc.SetClock(mockClock)

	// Set sun times so that now is before dawn (SunEventNight returns from first case)
	setSunTimesForTest(calc, testTime3AM,
		testTime3AM.Add(4*time.Hour),  // dawn (future - so we're before dawn = SunEventNight)
		testTime3AM.Add(5*time.Hour),  // sunrise (future)
		testTime3AM.Add(6*time.Hour),  // sunriseEnd (future)
		testTime3AM.Add(7*time.Hour),  // goldenHourEnd (future)
		testTime3AM.Add(15*time.Hour), // goldenHour (future)
		testTime3AM.Add(16*time.Hour), // sunsetStart (future)
		testTime3AM.Add(17*time.Hour), // sunset (future)
		testTime3AM.Add(18*time.Hour), // dusk (future)
		testTime3AM.Add(19*time.Hour), // nauticalDusk (future)
		testTime3AM.Add(20*time.Hour), // night (future)
	)

	// Verify GetSunEvent returns night (before dawn)
	sunEvent = calc.GetSunEvent()
	assert.Equal(t, SunEventNight, sunEvent, "Expected SunEventNight at 9 AM UTC (before dawn)")

	dayPhase = calc.CalculateDayPhase(nil)

	// At 3 AM local time (03:00 CST), the fallback logic should return Night
	// because 3 < 6
	assert.Equal(t, DayPhaseNight, dayPhase,
		"At 3 AM local time (9 AM UTC), should return Night")
}
