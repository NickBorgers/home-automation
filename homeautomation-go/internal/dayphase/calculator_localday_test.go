package dayphase

import (
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression tests for the local-midnight sun-time bug (2026-10-31, CDT).
//
// suncalc rolls its "day" over at solar midnight, about 01:30 local at this site. Passed
// "now" at 00:00 local it returns the PREVIOUS day's sun times. The calculator must ask for
// the LOCAL calendar day instead.
//
// INVARIANT: from 00:00 local onward, every cached sun event belongs to the local date.

const (
	localDayLat = 32.85486
	localDayLon = -97.50515
)

func chicago(t *testing.T) *time.Location {
	t.Helper()
	tz, err := time.LoadLocation("America/Chicago")
	require.NoError(t, err)
	return tz
}

func newLocalDayCalc(t *testing.T, tz *time.Location, now time.Time) (*Calculator, *clock.MockClock) {
	t.Helper()
	calc := NewCalculator(localDayLat, localDayLon, tz, testlogger.New())
	clk := clock.NewMockClock(now)
	calc.SetClock(clk)
	return calc, clk
}

func TestCalculator_SunTimesAreForTheLocalDate(t *testing.T) {
	t.Parallel()
	tz := chicago(t)
	events := []string{"dawn", "sunrise", "solarNoon", "goldenHour", "sunset", "dusk", "night"}

	tests := []struct {
		name     string
		now      time.Time
		wantDate string
	}{
		{"23:30 the evening before", time.Date(2026, 10, 30, 23, 30, 0, 0, tz), "2026-10-30"},
		{"00:00 local midnight", time.Date(2026, 10, 31, 0, 0, 0, 0, tz), "2026-10-31"},
		{"00:00:09 as in the log", time.Date(2026, 10, 31, 0, 0, 9, 0, tz), "2026-10-31"},
		{"01:00", time.Date(2026, 10, 31, 1, 0, 0, 0, tz), "2026-10-31"},
		{"01:30 just before solar rollover", time.Date(2026, 10, 31, 1, 30, 0, 0, tz), "2026-10-31"},
		{"02:00 after solar rollover", time.Date(2026, 10, 31, 2, 0, 0, 0, tz), "2026-10-31"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// GIVEN: the clock reads this instant
			calc, _ := newLocalDayCalc(t, tz, tt.now)

			// WHEN: sun times are updated
			require.NoError(t, calc.UpdateSunTimes())

			// THEN: every event falls on the local date
			got := calc.GetSunTimes()
			for _, ev := range events {
				assert.Equal(t, tt.wantDate, got[ev].In(tz).Format("2006-01-02"), "event %s = %s", ev, got[ev].In(tz))
			}
		})
	}
}

func TestCalculator_SunTimesIdenticalAcrossTheLocalDay(t *testing.T) {
	t.Parallel()
	tz := chicago(t)
	base := time.Date(2026, 10, 31, 0, 0, 0, 0, tz)

	// GIVEN: times computed at 00:00
	ref, _ := newLocalDayCalc(t, tz, base)
	require.NoError(t, ref.UpdateSunTimes())
	want := ref.GetSunTimes()

	// WHEN/THEN: any other instant of the same local day gives the same times
	for _, h := range []int{1, 2, 6, 12, 18, 23} {
		calc, _ := newLocalDayCalc(t, tz, base.Add(time.Duration(h)*time.Hour+30*time.Minute))
		require.NoError(t, calc.UpdateSunTimes())
		assert.Equal(t, want, calc.GetSunTimes(), "hour %d", h)
	}
}

func TestCalculator_GetSunEventStaysNightAfterMidnight(t *testing.T) {
	t.Parallel()
	tz := chicago(t)
	// INVARIANT: between local midnight and dawn the sun event is night, never morning.
	tests := []struct {
		name string
		at   time.Time
	}{
		{"00:00", time.Date(2026, 10, 31, 0, 0, 0, 0, tz)},
		{"00:04 as in the log", time.Date(2026, 10, 31, 0, 4, 0, 0, tz)},
		{"01:00", time.Date(2026, 10, 31, 1, 0, 0, 0, tz)},
		{"02:00", time.Date(2026, 10, 31, 2, 0, 0, 0, tz)},
		{"04:54 periodic refresh", time.Date(2026, 10, 31, 4, 54, 0, 0, tz)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			calc, _ := newLocalDayCalc(t, tz, tt.at)
			assert.Equal(t, SunEventNight, calc.GetSunEvent())
		})
	}
}

func TestCalculator_StaleCacheFromYesterdayEveningIsRefreshedAtMidnight(t *testing.T) {
	t.Parallel()
	tz := chicago(t)

	// GIVEN: sun times cached at 18:54 on Oct 30 (the 6-hourly refresh)
	calc, clk := newLocalDayCalc(t, tz, time.Date(2026, 10, 30, 18, 54, 0, 0, tz))
	require.NoError(t, calc.UpdateSunTimes())

	// WHEN: the clock passes local midnight (only 5 h later, so the 6 h age limit has not tripped)
	clk.Set(time.Date(2026, 10, 31, 0, 4, 0, 0, tz))

	// THEN: the cache is refreshed for the new date and the event is night, not morning
	assert.Equal(t, SunEventNight, calc.GetSunEvent())
	assert.Equal(t, "2026-10-31", calc.GetSunTimes()["dawn"].In(tz).Format("2006-01-02"))
}
