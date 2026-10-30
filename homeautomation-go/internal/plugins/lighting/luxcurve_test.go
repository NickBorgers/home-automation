package lighting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var luxTestDay = time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

func at(h, m int) time.Time {
	return luxTestDay.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
}

// luxTestAnchors is a fixed day: dawn 06:00, noon 12:00, golden hour 18:00,
// dusk 19:30, winddown 21:15, night 22:00.
func luxTestAnchors(anchor string, _ time.Time) (time.Time, bool) {
	times := map[string]time.Time{
		"sun.dawn":          at(6, 0),
		"sun.noon":          at(12, 0),
		"sun.goldenHour":    at(18, 0),
		"schedule.dusk":     at(19, 30),
		"schedule.winddown": at(21, 15),
		"schedule.night":    at(22, 0),
	}
	t, ok := times[anchor]
	return t, ok
}

func luxTestCurve() *LuxCurveConfig {
	return &LuxCurveConfig{
		Points: []LuxCurvePoint{
			{Anchor: "sun.dawn", Lux: 300, Kelvin: 4000},
			{Anchor: "sun.noon", Lux: 300, Kelvin: 5000},
			{Anchor: "sun.goldenHour", Lux: 150, Kelvin: 3000},
			{Anchor: "schedule.dusk", Lux: 100, Kelvin: 2700},
			{Anchor: "schedule.winddown", Lux: 30, Kelvin: 2200},
			{Anchor: "schedule.night", Lux: 5, Kelvin: 2000},
		},
		OutdoorSensor: "sensor.outdoor_lux",
	}
}

func TestTargetAt(t *testing.T) {
	t.Parallel()
	points, err := resolveCurve(luxTestCurve(), at(12, 0), luxTestAnchors)
	require.NoError(t, err)

	tests := []struct {
		name       string
		now        time.Time
		wantLux    float64
		wantKelvin int
	}{
		{"before dawn holds night values", at(3, 0), 5, 2000},
		{"at dawn", at(6, 0), 300, 4000},
		{"at noon", at(12, 0), 300, 5000},
		{"halfway noon to golden hour: log lux, mired kelvin", at(15, 0), 212.13, 3750},
		{"at winddown", at(21, 15), 30, 2200},
		{"after night holds night values", at(23, 30), 5, 2000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := targetAt(points, tt.now)
			require.True(t, ok)
			assert.InDelta(t, tt.wantLux, got.Lux, 0.1)
			assert.Equal(t, tt.wantKelvin, got.Kelvin)
		})
	}
}

func TestTargetAtReportsCurvePosition(t *testing.T) {
	t.Parallel()
	points, _ := resolveCurve(luxTestCurve(), at(12, 0), luxTestAnchors)
	got, _ := targetAt(points, at(15, 0))
	assert.Equal(t, "sun.noon", got.From)
	assert.Equal(t, "sun.goldenHour", got.To)
	assert.InDelta(t, 0.5, got.Pos, 0.001)
}

func TestResolveCurveSortsByTimeAndRejectsUnknownAnchor(t *testing.T) {
	t.Parallel()
	cfg := &LuxCurveConfig{Points: []LuxCurvePoint{
		{Anchor: "sun.noon", Lux: 300, Kelvin: 5000},
		{Anchor: "sun.dawn", Lux: 300, Kelvin: 4000},
	}}
	points, err := resolveCurve(cfg, at(12, 0), luxTestAnchors)
	require.NoError(t, err)
	assert.Equal(t, "sun.dawn", points[0].Anchor)

	cfg.Points = append(cfg.Points, LuxCurvePoint{Anchor: "sun.bogus", Lux: 1, Kelvin: 2000})
	_, err = resolveCurve(cfg, at(12, 0), luxTestAnchors)
	assert.Error(t, err)
}

var luxTestCalibration = []CalibrationPoint{
	{BrightnessPct: 25, Lux: 40},
	{BrightnessPct: 50, Lux: 120},
	{BrightnessPct: 100, Lux: 300},
}

func TestBrightnessLuxMapping(t *testing.T) {
	t.Parallel()
	assert.InDelta(t, 40, brightnessToLux(luxTestCalibration, 25), 0.001)
	assert.InDelta(t, 80, brightnessToLux(luxTestCalibration, 37.5), 0.001)
	assert.InDelta(t, 300, brightnessToLux(luxTestCalibration, 100), 0.001)
	assert.InDelta(t, 1.6, brightnessToLux(luxTestCalibration, 1), 0.001)

	assert.InDelta(t, 43.75, luxToBrightness(luxTestCalibration, 100), 0.001)
	assert.Equal(t, 100.0, luxToBrightness(luxTestCalibration, 500), "more than the cans can give")
	assert.Equal(t, 0.0, luxToBrightness(luxTestCalibration, 0))
	assert.Equal(t, 0.0, luxToBrightness(luxTestCalibration, -50))
	assert.Equal(t, 0.0, luxToBrightness(nil, 100), "no calibration")
}

func TestShouldBeOnHysteresis(t *testing.T) {
	t.Parallel()
	threshold := brightnessToLux(luxTestCalibration, 1) // 1.6 lux
	// On: stays on until needed falls below the threshold.
	assert.True(t, shouldBeOn(luxTestCalibration, threshold+0.1, true))
	assert.False(t, shouldBeOn(luxTestCalibration, threshold-0.1, true))
	// Off: comes back only 50% above the threshold.
	assert.False(t, shouldBeOn(luxTestCalibration, threshold*1.4, false))
	assert.True(t, shouldBeOn(luxTestCalibration, threshold*1.6, false))
}

func TestSmoother(t *testing.T) {
	t.Parallel()
	s := &smoother{window: 5 * time.Minute}
	_, ok := s.mean()
	assert.False(t, ok)

	s.add(at(12, 0), 1000)
	s.add(at(12, 2), 2000)
	m, _ := s.mean()
	assert.InDelta(t, 1500, m, 0.001)

	// The first sample leaves the window.
	s.add(at(12, 7), 3000)
	m, _ = s.mean()
	assert.InDelta(t, 2500, m, 0.001)

	s.reset()
	_, ok = s.mean()
	assert.False(t, ok)
}
