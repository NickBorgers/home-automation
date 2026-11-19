package vacuum

import (
	"testing"

	"homeautomation/internal/housemap"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testErrSensor = "sensor.robot_error"

// newWaterEnv is a scheduler with the out-of-water rule on. helper says whether waterEntity exists.
func newWaterEnv(t *testing.T, helper bool, initialError string) *schedEnv {
	t.Helper()
	e := newSchedEnv(t, ModeActive, func(cfg *SchedulerConfig, _ *housemap.Map) {
		cfg.errorSensorID, cfg.noErrorValue = testErrSensor, "No error"
	})
	e.ha.SetState(testErrSensor, initialError, map[string]interface{}{})
	if helper {
		e.ha.SetState(waterEntity, "off", map[string]interface{}{})
	}
	e.start()
	e.tick()
	return e
}

func (e *schedEnv) setError(text string) {
	e.ha.SetState(testErrSensor, text, map[string]interface{}{})
	e.tick()
}

func TestWaterErrorMatchIgnoresCase(t *testing.T) {
	e := newWaterEnv(t, true, "No error")
	assert.True(t, e.s.isWaterError("Mop Dock Clean Water Tank empty"))
	assert.True(t, e.s.isWaterError("mop dock clean water tank EMPTY"))
	assert.False(t, e.s.isWaterError("Mop pads not in the dock. Attach failed."))
	assert.False(t, e.s.isWaterError("No error"))
}

func TestWaterLatchSurvivesBlipsAndClearsOnlyWhenSomeoneIsHome(t *testing.T) {
	e := newWaterEnv(t, true, "No error")
	require.NoError(t, e.sm.SetBool(anyoneHomeKey, false))

	e.setError("Mop Dock Clean Water Tank empty")
	assert.Equal(t, "on", e.state(waterEntity))
	assert.True(t, e.sched().WaterEmpty)

	e.setError("unavailable")
	assert.True(t, e.s.waterErrorNow, "a blip says nothing about the water")

	e.setError("No error")
	assert.Equal(t, "on", e.state(waterEntity), "nobody is home, so nobody refilled the tank")

	e.setError("Mop Dock Clean Water Tank empty")
	require.NoError(t, e.sm.SetBool(anyoneHomeKey, true))
	e.tick() // the arrival itself clears it
	assert.Equal(t, "off", e.state(waterEntity))

	e.setError("Mop Dock Clean Water Tank empty")
	e.setError("Mop Dock Clean Water Tank empty")
	assert.Equal(t, "off", e.state(waterEntity), "the same error again is not a new water error")

	e.setError("Cannot reach target")
	e.setError("Mop Dock Clean Water Tank empty")
	assert.Equal(t, "on", e.state(waterEntity))
	e.setError("No error")
	assert.Equal(t, "off", e.state(waterEntity), "someone is home and the error cleared")
}

func TestWaterLatchAtStartupAndWithoutHelper(t *testing.T) {
	e := newWaterEnv(t, false, "Mop Dock Clean Water Tank empty")
	assert.True(t, e.s.waterEmpty(), "kept in memory")
	assert.True(t, e.sched().WaterEmpty)
	assert.Contains(t, e.sched().Warnings, "missing helper "+waterEntity+"; out of water is kept in memory only")
}

func TestWaterVacuumOnlyPlan(t *testing.T) {
	e := newWaterEnv(t, true, "No error")
	e.setError("Mop Dock Clean Water Tank empty")
	e.markDirty("kitchen", "pantry")
	e.s.TickForTest(true)

	p := e.sched().Plan
	require.NotNil(t, p)
	assert.Equal(t, "vacuum", p.Treatment)
	assert.Equal(t, []string{"pantry"}, p.Rooms)
	var kitchen string
	for _, sk := range p.Skipped {
		if sk.Room == "kitchen" {
			kitchen = sk.Reason
		}
	}
	assert.Equal(t, "out of water; the room's own vacuum interval covers it", kitchen)
}
