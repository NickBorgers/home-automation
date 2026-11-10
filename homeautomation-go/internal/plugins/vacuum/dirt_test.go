package vacuum

import (
	"testing"
	"time"

	"homeautomation/internal/ha"

	"github.com/stretchr/testify/assert"
)

func TestHelperNames(t *testing.T) {
	f := itemKey{Room: "kitchen", Treatment: TreatmentFull}
	v := itemKey{Room: "kitchen", Treatment: TreatmentVacuum}
	assert.Equal(t, "input_boolean.vacuum_dirty_kitchen", dirtyEntity(f))
	assert.Equal(t, "input_boolean.vacuum_dirty_kitchen_vacuum", dirtyEntity(v))
	assert.Equal(t, "input_datetime.vacuum_cleaned_kitchen", cleanedEntity(f))
	assert.Equal(t, "input_datetime.vacuum_cleaned_kitchen_vacuum", cleanedEntity(v))
	assert.Equal(t, "input_number.vacuum_cost_kitchen", costEntity(f))
	assert.Equal(t, "input_number.vacuum_cost_kitchen_vacuum", costEntity(v))
	assert.Equal(t, "input_number.vacuum_cost_trip", tripEntity)
	assert.Equal(t, "kitchen/vacuum", v.String())
}

func TestEvaluateTaint(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	tests := []struct {
		name          string
		boolOn        bool
		cleaned       time.Time
		dirty, manual bool
		score         float64
	}{
		{"fresh", false, now.Add(-12 * time.Hour), false, false, 0.5},
		{"exactly due", false, now.Add(-day), true, false, 1},
		{"overdue", false, now.Add(-36 * time.Hour), true, false, 1.5},
		{"never cleaned", false, time.Time{}, true, false, neverCleanedScore},
		{"due and boolean on", true, now.Add(-day), true, false, 1},
		{"manual taint", true, now.Add(-time.Hour), true, true, manualScore},
		{"manual taint on a never-cleaned room is not manual", true, time.Time{}, true, false, neverCleanedScore},
		{"clean time in the future", false, now.Add(time.Hour), false, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dirty, manual, score := evaluateTaint(now, day, tt.boolOn, tt.cleaned)
			assert.Equal(t, tt.dirty, dirty)
			assert.Equal(t, tt.manual, manual)
			assert.InDelta(t, tt.score, score, 1e-9)
		})
	}
	_, _, score := evaluateTaint(now, day, true, now.Add(-20*day))
	assert.InDelta(t, 20.0, score, 1e-9, "a far overdue item can outrank a manual one")
}

func TestParseDatetime(t *testing.T) {
	ts := time.Date(2026, 11, 9, 3, 0, 0, 0, time.UTC)
	loc := time.FixedZone("test", -6*3600)
	tests := []struct {
		name string
		st   *ha.State
		want time.Time
		ok   bool
	}{
		{"nil", nil, time.Time{}, false},
		{"timestamp attribute", &ha.State{State: "ignored",
			Attributes: map[string]interface{}{"timestamp": float64(ts.Unix())}}, ts, true},
		{"timestamp as string", &ha.State{Attributes: map[string]interface{}{"timestamp": "1762657200"}},
			time.Unix(1762657200, 0), true},
		{"state text in local zone", &ha.State{State: "2026-11-09 03:00:00"},
			time.Date(2026, 11, 9, 3, 0, 0, 0, loc), true},
		{"never set", &ha.State{State: "1970-01-01 00:00:00"}, time.Time{}, true},
		{"never set timestamp", &ha.State{Attributes: map[string]interface{}{"timestamp": float64(0)}},
			time.Time{}, true},
		{"unknown", &ha.State{State: "unknown"}, time.Time{}, true},
		{"time only helper", &ha.State{State: "03:00:00"}, time.Time{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseDatetime(tt.st, loc)
			assert.Equal(t, tt.ok, ok)
			assert.True(t, tt.want.Equal(got), "got %v want %v", got, tt.want)
		})
	}
}

func TestParseNumber(t *testing.T) {
	v, ok := parseNumber(&ha.State{State: "4.50"})
	assert.True(t, ok)
	assert.Equal(t, 4.5, v)
	for _, s := range []string{"unknown", "", "NaN", "abc"} {
		_, ok := parseNumber(&ha.State{State: s})
		assert.False(t, ok, s)
	}
	_, ok = parseNumber(nil)
	assert.False(t, ok)
}

func TestLearnCost(t *testing.T) {
	assert.InDelta(t, 8.2, learnCost(10, 4, 0.3), 1e-9)
	assert.InDelta(t, 4.0, learnCost(10, 4, 1), 1e-9)
	assert.InDelta(t, 9.6, firstGuessCost(16, 0.6), 1e-9)
}
