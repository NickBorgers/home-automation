package lighting

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// This file holds the pure math of lux-target lighting (see docs/flows/LUX_LIGHTING.md).
// Nothing here talks to Home Assistant, so every function is easy to test.

// AnchorResolver returns the time today (in the caller's day) of a curve anchor such as
// "sun.dawn" or "schedule.winddown". It reports false when the anchor is unknown.
type AnchorResolver func(anchor string, now time.Time) (time.Time, bool)

// curvePoint is one resolved point of the target curve.
type curvePoint struct {
	Anchor string
	At     time.Time
	Lux    float64
	Kelvin int
}

// curveTarget is the target at one moment, plus where it sits on the curve.
type curveTarget struct {
	Lux    float64
	Kelvin int
	From   string  // anchor of the earlier point
	To     string  // anchor of the later point
	Pos    float64 // 0..1 between From and To
}

// resolveCurve turns the configured points into dated points, sorted by time.
// A point whose anchor cannot be resolved is an error: a silent gap would bend the curve.
func resolveCurve(cfg *LuxCurveConfig, now time.Time, resolve AnchorResolver) ([]curvePoint, error) {
	points := make([]curvePoint, 0, len(cfg.Points))
	for _, p := range cfg.Points {
		at, ok := resolve(p.Anchor, now)
		if !ok {
			return nil, fmt.Errorf("cannot resolve curve anchor %q", p.Anchor)
		}
		points = append(points, curvePoint{Anchor: p.Anchor, At: at, Lux: p.Lux, Kelvin: p.Kelvin})
	}
	sort.SliceStable(points, func(i, j int) bool { return points[i].At.Before(points[j].At) })
	return points, nil
}

// targetAt returns the target at time now. Between two points, lux slides on a log scale
// and kelvin slides in mired. Before the first point of the day, and after the last, the
// target holds the last point's values (the "night, awake" values).
func targetAt(points []curvePoint, now time.Time) (curveTarget, bool) {
	if len(points) == 0 {
		return curveTarget{}, false
	}
	last := points[len(points)-1]
	if now.Before(points[0].At) || !now.Before(last.At) {
		return curveTarget{Lux: last.Lux, Kelvin: last.Kelvin, From: last.Anchor, To: last.Anchor}, true
	}
	for i := 0; i < len(points)-1; i++ {
		a, b := points[i], points[i+1]
		if now.Before(b.At) {
			span := b.At.Sub(a.At).Seconds()
			pos := 0.0
			if span > 0 {
				pos = now.Sub(a.At).Seconds() / span
			}
			return curveTarget{
				Lux:    interpolateLux(a.Lux, b.Lux, pos),
				Kelvin: interpolateKelvin(a.Kelvin, b.Kelvin, pos),
				From:   a.Anchor,
				To:     b.Anchor,
				Pos:    pos,
			}, true
		}
	}
	return curveTarget{Lux: last.Lux, Kelvin: last.Kelvin, From: last.Anchor, To: last.Anchor}, true
}

// interpolateLux slides between two lux values on a log scale. The eye sees brightness
// roughly logarithmically. A value of zero or less falls back to a straight line.
func interpolateLux(a, b, pos float64) float64 {
	if a <= 0 || b <= 0 {
		return a + (b-a)*pos
	}
	return math.Exp(math.Log(a) + (math.Log(b)-math.Log(a))*pos)
}

// interpolateKelvin slides between two color temperatures in mired (1,000,000 / kelvin),
// because equal mired steps look like equal color steps.
func interpolateKelvin(a, b int, pos float64) int {
	if a <= 0 || b <= 0 {
		return a
	}
	ma, mb := 1e6/float64(a), 1e6/float64(b)
	return int(math.Round(1e6 / (ma + (mb-ma)*pos)))
}

// brightnessToLux returns the lux the cans give at a brightness, by linear interpolation
// through the calibration table. Zero brightness gives zero lux.
func brightnessToLux(cal []CalibrationPoint, pct float64) float64 {
	if len(cal) == 0 || pct <= 0 {
		return 0
	}
	prevPct, prevLux := 0.0, 0.0
	for _, c := range cal {
		cp := float64(c.BrightnessPct)
		if pct <= cp {
			return prevLux + (c.Lux-prevLux)*(pct-prevPct)/(cp-prevPct)
		}
		prevPct, prevLux = cp, c.Lux
	}
	return prevLux
}

// luxToBrightness is the inverse of brightnessToLux. It returns 100 when the needed lux is
// more than the cans can give, and 0 when no light is needed.
func luxToBrightness(cal []CalibrationPoint, lux float64) float64 {
	if len(cal) == 0 || lux <= 0 {
		return 0
	}
	prevPct, prevLux := 0.0, 0.0
	for _, c := range cal {
		cp := float64(c.BrightnessPct)
		if lux <= c.Lux {
			if c.Lux == prevLux {
				return cp
			}
			return prevPct + (cp-prevPct)*(lux-prevLux)/(c.Lux-prevLux)
		}
		prevPct, prevLux = cp, c.Lux
	}
	return 100
}

// Hysteresis for the cans' off threshold (see "Avoiding flicker and churn" in the design).
const (
	offThresholdPct    = 1.0 // cans turn off below the lux at this brightness
	turnOnHysteresis   = 1.5 // cans turn back on above threshold × this
	minBrightnessDelta = 3.0 // percentage points
	minKelvinDelta     = 100
	// Slew limit: the most one command may move a lit room's cans from the last command. A larger
	// gap closes over several ticks, so a change is never a visible step. Each must stay above its
	// minimum delta, or a gap smaller than the minimum could never close.
	maxBrightnessStep = 5.0 // percentage points per tick
	maxKelvinStep     = 200
)

// shouldBeOn applies the off threshold with hysteresis.
func shouldBeOn(cal []CalibrationPoint, neededLux float64, currentlyOn bool) bool {
	threshold := brightnessToLux(cal, offThresholdPct)
	if currentlyOn {
		return neededLux >= threshold
	}
	return neededLux > threshold*turnOnHysteresis
}

// smoother averages outdoor lux readings over a time window.
type smoother struct {
	window  time.Duration
	samples []smoothSample
}

type smoothSample struct {
	at  time.Time
	lux float64
}

// add records a reading and drops readings older than the window.
func (s *smoother) add(at time.Time, lux float64) {
	s.samples = append(s.samples, smoothSample{at: at, lux: lux})
	cutoff := at.Add(-s.window)
	i := 0
	for i < len(s.samples)-1 && s.samples[i].at.Before(cutoff) {
		i++
	}
	s.samples = s.samples[i:]
}

// reset forgets every reading. Used when the sensor goes missing, so old data
// cannot hide the gap.
func (s *smoother) reset() { s.samples = nil }

// mean returns the average reading, and false when there is none.
func (s *smoother) mean() (float64, bool) {
	if len(s.samples) == 0 {
		return 0, false
	}
	sum := 0.0
	for _, v := range s.samples {
		sum += v.lux
	}
	return sum / float64(len(s.samples)), true
}
