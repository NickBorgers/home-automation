package lighting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for imperceptible lux changes: lights already on fade after a restart or reset,
// and no command moves a lit room more than one slew step. See docs/flows/LUX_LIGHTING.md.

// darkOutsideFixture is the lux fixture at noon with no daylight: the goal is 100% at 5000 K.
func darkOutsideFixture(t *testing.T) *luxFixture {
	t.Helper()
	f := newLuxFixture(t, LuxModeActive, false)
	f.setOutdoor("0")
	return f
}

func TestLux_RestartWithCansOnFadesFromTheirCurrentState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		state      string
		brightness float64
		kelvin     float64
		wantPct    int
		wantKelvin int
		wantSecs   float64
	}{
		{"cans on at 50% and 3000 K fade", "on", 128, 3000, 55, 3200, 60},
		{"cans on at 98% and 4900 K close the small gap", "on", 250, 4900, 100, 5000, 60},
		{"cans off turn on directly", "off", 0, 3000, 100, 5000, float64(maxReturnHomeTransition)},
		{"cans on without brightness count as dark", "on", 0, 3000, 100, 5000, float64(maxReturnHomeTransition)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// GIVEN: the service just restarted (no remembered command) and the cans show tt
			f := darkOutsideFixture(t)
			attrs := map[string]interface{}{"color_temp_kelvin": tt.kelvin}
			if tt.brightness > 0 {
				attrs["brightness"] = tt.brightness
			}
			f.env.MockHA.SetState(luxTestCans, tt.state, attrs)

			// WHEN: the first tick runs
			f.manager.luxTick()

			// THEN: the first command starts from the lights' own state
			calls := f.lightCalls()
			require.Len(t, calls, 1)
			assert.Equal(t, tt.wantPct, calls[0].Data["brightness_pct"])
			assert.Equal(t, tt.wantKelvin, calls[0].Data["color_temp_kelvin"])
			assert.Equal(t, tt.wantSecs, calls[0].Data["transition"])
		})
	}
}

func TestLux_LargeGapClosesOverSeveralTicksAndEndsAtTheGoal(t *testing.T) {
	t.Parallel()
	// GIVEN: cans on at 50% and 3000 K, goal 100% at 5000 K
	f := darkOutsideFixture(t)
	f.env.MockHA.SetState(luxTestCans, "on", map[string]interface{}{"brightness": 128.0, "color_temp_kelvin": 3000.0})

	// WHEN: the loop ticks once a minute
	prevPct, prevKelvin := 50, 3000
	var steps int
	for i := 0; i < 20; i++ {
		f.env.MockHA.ClearServiceCalls()
		f.manager.luxTick()
		f.clock.Advance(time.Minute)
		calls := f.lightCalls()
		if len(calls) == 0 {
			break
		}
		steps++
		// THEN: no command moves more than one step, and every one is a long transition
		pct, kelvin := calls[0].Data["brightness_pct"].(int), calls[0].Data["color_temp_kelvin"].(int)
		assert.LessOrEqual(t, pct-prevPct, int(maxBrightnessStep))
		assert.LessOrEqual(t, kelvin-prevKelvin, maxKelvinStep)
		assert.Equal(t, 60.0, calls[0].Data["transition"])
		prevPct, prevKelvin = pct, kelvin
	}

	// AND: the room ends within the minimum step of the goal (the curve drifts a little meanwhile)
	assert.Greater(t, steps, 3, "the gap closed over several ticks")
	st := f.manager.GetShadowState().Outputs.LuxControl["Living Room"]
	assert.InDelta(t, st.GoalBrightnessPct, float64(prevPct), minBrightnessDelta)
	assert.InDelta(t, st.TargetKelvin, prevKelvin, minKelvinDelta)
	assert.Contains(t, st.Decision, "no change")
}

func TestLux_ShadowShowsCommandedAndGoalBrightness(t *testing.T) {
	t.Parallel()
	// GIVEN: cans at 50%, goal 100%
	f := darkOutsideFixture(t)
	f.env.MockHA.SetState(luxTestCans, "on", map[string]interface{}{"brightness": 128.0, "color_temp_kelvin": 5000.0})

	// WHEN: the first tick runs
	f.manager.luxTick()

	// THEN: BrightnessPct is the clamped command and GoalBrightnessPct the unclamped goal
	st := f.manager.GetShadowState().Outputs.LuxControl["Living Room"]
	assert.InDelta(t, 55, st.BrightnessPct, 0.6)
	assert.Equal(t, 100.0, st.GoalBrightnessPct)
}

func TestLux_RestartWithCansOnDoesNotTripManualHold(t *testing.T) {
	t.Parallel()
	// GIVEN: a restart with the cans on at 50% and 3000 K, and a first (slewed) command
	f := darkOutsideFixture(t)
	f.env.MockHA.SetState(luxTestCans, "on", map[string]interface{}{"brightness": 128.0, "color_temp_kelvin": 3000.0})
	f.manager.luxTick()
	f.env.MockHA.ClearServiceCalls()

	// WHEN: the lights report their own state during and after the fade, and the next tick runs
	f.clock.Advance(5 * time.Second)
	f.manager.handleCansChange(f.room, canReport("on", 140, 3100)) // mid-fade
	f.clock.Advance(time.Minute)
	f.manager.handleCansChange(f.room, canReport("on", 140, 3200)) // 55%, as commanded
	f.manager.luxTick()

	// THEN: the room is not held and the plugin keeps fading
	assert.False(t, f.manager.luxHeld(f.room))
	assert.NotContains(t, f.decision(), "held")
	assert.NotEmpty(t, f.lightCalls())
}

func TestLux_RestartWithCansOnStillCatchesALaterManualChange(t *testing.T) {
	t.Parallel()
	// GIVEN: a restart where the cans already sit at the goal (no command is sent)
	f := darkOutsideFixture(t)
	f.env.MockHA.SetState(luxTestCans, "on", map[string]interface{}{"brightness": 255.0, "color_temp_kelvin": 5000.0})
	f.manager.luxTick()
	require.Empty(t, f.lightCalls())
	f.clock.Advance(time.Minute)

	// WHEN: a person turns the cans down
	f.manager.handleCansChange(f.room, canReport("on", 100, 5000))

	// THEN: the adopted state is the baseline, so the change holds the room
	assert.True(t, f.manager.luxHeld(f.room))
}

func TestLux_ResetFadesLitCansInsteadOfPopping(t *testing.T) {
	t.Parallel()
	// GIVEN: a room that has been running, whose cans now show 50% at 3000 K
	f := darkOutsideFixture(t)
	f.manager.luxTick()
	f.env.MockHA.SetState(luxTestCans, "on", map[string]interface{}{"brightness": 128.0, "color_temp_kelvin": 3000.0})
	f.manager.clearLuxHold(f.room, "reset")
	f.env.MockHA.ClearServiceCalls()
	f.clock.Advance(time.Minute)

	// WHEN: the next tick runs after the hold and last command were cleared
	f.manager.luxTick()

	// THEN: the cans fade from where they are
	calls := f.lightCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, 55, calls[0].Data["brightness_pct"])
	assert.Equal(t, 60.0, calls[0].Data["transition"])
}

func TestLux_FollowersGetTheClampedValueAndFadeWhenAlreadyOn(t *testing.T) {
	t.Parallel()
	// GIVEN: a strip following the cans; after a restart both are on (cans 50%, strip 60%)
	f := darkOutsideFixture(t)
	const strip = "light.living_strip"
	f.room.LuxControl.FollowerLights = []LuxFollower{{Entity: strip, FloorPct: 20, Gain: 0.8}}
	f.env.MockHA.SetState(luxTestCans, "on", map[string]interface{}{"brightness": 128.0, "color_temp_kelvin": 5000.0})
	f.env.MockHA.SetState(strip, "on", map[string]interface{}{"brightness": 153.0})

	// WHEN: the first tick runs
	f.manager.luxTick()

	// THEN: the strip is derived from the clamped 55%, not the 100% goal, with the long transition
	calls := callsTo(f.env.MockHA, "turn_on", strip)
	require.Len(t, calls, 1)
	assert.Equal(t, 64, calls[0].Data["brightness_pct"], "20 + 0.8 * 55")
	assert.Equal(t, 60.0, calls[0].Data["transition"])
	assert.Equal(t, 64.0, f.manager.GetShadowState().Outputs.LuxControl["Living Room"].Followers[strip].Pct)
}

func TestLux_DarkStripStillLightsWithTheShortTransition(t *testing.T) {
	t.Parallel()
	// GIVEN: cans on and a dark strip
	f := darkOutsideFixture(t)
	const strip = "light.living_strip"
	f.room.LuxControl.FollowerLights = []LuxFollower{{Entity: strip, FloorPct: 20, Gain: 0.8}}
	f.env.MockHA.SetState(luxTestCans, "on", map[string]interface{}{"brightness": 128.0, "color_temp_kelvin": 5000.0})
	f.env.MockHA.SetState(strip, "off", nil)

	// WHEN: the first tick runs
	f.manager.luxTick()

	// THEN: the strip turns on with the short transition
	calls := callsTo(f.env.MockHA, "turn_on", strip)
	require.Len(t, calls, 1)
	assert.Equal(t, float64(maxReturnHomeTransition), calls[0].Data["transition"])
}

func TestLux_StepConstantsStayAboveTheMinimumStep(t *testing.T) {
	t.Parallel()
	assert.GreaterOrEqual(t, maxBrightnessStep, minBrightnessDelta)
	assert.GreaterOrEqual(t, maxKelvinStep, minKelvinDelta)
}
