package lighting

import (
	"path/filepath"
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/config"
	dayphaselib "homeautomation/internal/dayphase"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression tests for the local-midnight bug of 2026-10-31 (CDT, UTC-5): suncalc handed back
// the previous day's sun times between 00:00 and about 01:30 local, so the lux curve mixed
// Oct 30 sun anchors with Oct 31 schedule anchors and the target jumped to about 150 lux.
//
// INVARIANT: the lux target never rises between 22:00 and sunrise.

// midnightSite is the production site (run.go defaults) with the production curve and schedule.
type midnightSite struct {
	tz      *time.Location
	clk     *clock.MockClock
	anchors *solarAnchors
	calc    *dayphaselib.Calculator
	cfg     *HueConfig
}

func newMidnightSite(t *testing.T, start time.Time) *midnightSite {
	t.Helper()
	tz, err := time.LoadLocation("America/Chicago")
	require.NoError(t, err)
	logger := testlogger.New()
	clk := clock.NewMockClock(start)

	calc := dayphaselib.NewCalculator(32.85486, -97.50515, tz, logger)
	calc.SetClock(clk)

	loader := config.NewLoader(filepath.Join("..", "..", "..", "..", "configs"), logger)
	loader.SetTimezone(tz)
	loader.SetClock(clk)
	require.NoError(t, loader.LoadScheduleConfig())

	return &midnightSite{
		tz:      tz,
		clk:     clk,
		anchors: newSolarAnchors(calc, loader, tz, logger),
		calc:    calc,
		cfg:     loadProductionConfig(t),
	}
}

func (s *midnightSite) targetNow(t *testing.T) curveTarget {
	t.Helper()
	now := s.clk.Now()
	points, err := resolveCurve(s.cfg.LuxCurve, now, s.anchors.resolve)
	require.NoError(t, err)
	target, ok := targetAt(points, now)
	require.True(t, ok)
	return target
}

func TestSolarAnchors_TargetAfterMidnightIsTheNightValue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		at   [3]int // Oct 31 hour, minute, second
	}{
		{"00:00:09 as in the log", [3]int{0, 0, 9}},
		{"00:04", [3]int{0, 4, 0}},
		{"00:30", [3]int{0, 30, 0}},
		{"01:00", [3]int{1, 0, 0}},
		{"01:30", [3]int{1, 30, 0}},
		{"01:45 around the solar rollover", [3]int{1, 45, 0}},
		{"02:00", [3]int{2, 0, 0}},
		{"05:00", [3]int{5, 0, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// GIVEN: a fresh process that starts at this instant
			site := newMidnightSite(t, time.Date(2026, 10, 31, tt.at[0], tt.at[1], tt.at[2], 0, mustChicago(t)))

			// WHEN: the curve is evaluated
			target := site.targetNow(t)

			// THEN: it is the night value (5 lux), held from schedule.night, not a daytime value
			assert.InDelta(t, 5, target.Lux, 0.001)
			assert.Equal(t, "schedule.night", target.From)
		})
	}
}

func TestSolarAnchors_CacheBuiltTheEveningBeforeIsRefreshedAtMidnight(t *testing.T) {
	t.Parallel()
	tz := mustChicago(t)

	// GIVEN: anchors resolved at 19:00 on Oct 30, so the day cache holds Oct 30
	site := newMidnightSite(t, time.Date(2026, 10, 30, 19, 0, 0, 0, tz))
	assert.Greater(t, site.targetNow(t).Lux, 5.0, "evening is above the night value")

	// WHEN: the clock walks through local midnight and the solar rollover
	// THEN: the target never rises, and from 23:00 (after Friday's 22:59 night) it is the night value
	prev := site.targetNow(t).Lux
	for site.clk.Now().Before(time.Date(2026, 10, 31, 6, 0, 0, 0, tz)) {
		site.clk.Advance(5 * time.Minute)
		got := site.targetNow(t)
		assert.LessOrEqual(t, got.Lux, prev+1e-9, "target rose at %s", site.clk.Now().In(tz).Format("15:04:05"))
		prev = got.Lux
		if !site.clk.Now().Before(time.Date(2026, 10, 30, 23, 0, 0, 0, tz)) {
			assert.InDelta(t, 5, got.Lux, 0.001, "at %s", site.clk.Now().In(tz).Format("15:04:05"))
		}
	}
	// and the cached sun anchors are for Oct 31 once the date has changed
	dawn, ok := site.anchors.resolve("sun.dawn", site.clk.Now())
	require.True(t, ok)
	assert.Equal(t, "2026-10-31", dawn.In(tz).Format("2006-01-02"))
}

func TestSolarAnchors_SunAnchorsAreForTheLocalDateAtMidnight(t *testing.T) {
	t.Parallel()
	tz := mustChicago(t)
	site := newMidnightSite(t, time.Date(2026, 10, 31, 0, 0, 9, 0, tz))

	for _, anchor := range []string{"sun.dawn", "sun.noon", "sun.goldenHour", "schedule.dusk", "schedule.night"} {
		at, ok := site.anchors.resolve(anchor, site.clk.Now())
		require.True(t, ok, anchor)
		assert.Equal(t, "2026-10-31", at.In(tz).Format("2006-01-02"), anchor)
	}
}

func mustChicago(t *testing.T) *time.Location {
	t.Helper()
	tz, err := time.LoadLocation("America/Chicago")
	require.NoError(t, err)
	return tz
}
