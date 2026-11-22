package lighting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A room brightens quickly when its target rises and dims gently when it falls.
// Fades up take luxTransitionUp (30 s), fades down and color-only changes take luxTransition (60 s).
// A light that was dark keeps the short maxReturnHomeTransition. See docs/flows/LUX_LIGHTING.md.

const luxUpSecs = float64(luxTransitionUp / time.Second)

func TestLux_RaisingTheTargetFadesUpInHalfTheTime(t *testing.T) {
	t.Parallel()
	// GIVEN: cans and a follower strip on at 50%, and a goal of 100% at 5000 K
	f := darkOutsideFixture(t)
	const strip = "light.living_strip"
	f.room.LuxControl.FollowerLights = []LuxFollower{{Entity: strip, FloorPct: 20, Gain: 0.8}}
	f.env.MockHA.SetState(luxTestCans, "on", map[string]interface{}{"brightness": 128.0, "color_temp_kelvin": 5000.0})
	f.env.MockHA.SetState(strip, "on", map[string]interface{}{"brightness": 100.0})

	// WHEN: the loop ticks
	f.manager.luxTick()

	// THEN: the cans and the strip both use the fade-up transition
	cans := f.lightCalls()
	require.NotEmpty(t, cans)
	assert.Equal(t, luxUpSecs, cans[0].Data["transition"])
	strips := callsTo(f.env.MockHA, "turn_on", strip)
	require.Len(t, strips, 1)
	assert.Equal(t, luxUpSecs, strips[0].Data["transition"])
	assert.Contains(t, f.decision(), "transition 30s", "the decision text reports the actual transition")
}

func TestLux_LoweringTheTargetKeepsTheLongTransition(t *testing.T) {
	t.Parallel()
	// GIVEN: cans and a strip on at 100%, and a goal below that
	f := newLuxFixture(t, LuxModeActive, false)
	const strip = "light.living_strip"
	f.room.LuxControl.FollowerLights = []LuxFollower{{Entity: strip, FloorPct: 20, Gain: 0.8}}
	f.env.MockHA.SetState(luxTestCans, "on", map[string]interface{}{"brightness": 255.0, "color_temp_kelvin": 5000.0})
	f.env.MockHA.SetState(strip, "on", map[string]interface{}{"brightness": 255.0})

	// WHEN: the loop ticks
	f.manager.luxTick()

	// THEN: the cans and the strip dim over 60 s
	cans := f.lightCalls()
	require.NotEmpty(t, cans)
	require.Less(t, cans[0].Data["brightness_pct"].(int), 100)
	assert.Equal(t, 60.0, cans[0].Data["transition"])
	strips := callsTo(f.env.MockHA, "turn_on", strip)
	require.Len(t, strips, 1)
	assert.Equal(t, 60.0, strips[0].Data["transition"])
	assert.Contains(t, f.decision(), "transition 1m0s")
}

func TestLux_ColorOnlyChangeKeepsTheLongTransition(t *testing.T) {
	t.Parallel()
	// GIVEN: cans already at the goal brightness (100%) but warmer than the goal color
	f := darkOutsideFixture(t)
	f.env.MockHA.SetState(luxTestCans, "on", map[string]interface{}{"brightness": 255.0, "color_temp_kelvin": 3000.0})

	// WHEN: the loop ticks
	f.manager.luxTick()

	// THEN: only the color moves, so the fade keeps 60 s
	calls := f.lightCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, 100, calls[0].Data["brightness_pct"])
	assert.Greater(t, calls[0].Data["color_temp_kelvin"].(int), 3000)
	assert.Equal(t, 60.0, calls[0].Data["transition"])
}

func TestLux_TurningOnFromOffIsNeverTheLongTransition(t *testing.T) {
	t.Parallel()
	// GIVEN: cans that are off, with no remembered command
	f := darkOutsideFixture(t)
	f.env.MockHA.SetState(luxTestCans, "off", nil)

	// WHEN: the loop ticks
	f.manager.luxTick()

	// THEN: the light turns on with the existing short entry profile, faster than a fade up
	calls := f.lightCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, float64(maxReturnHomeTransition), calls[0].Data["transition"])
	assert.Less(t, calls[0].Data["transition"].(float64), luxUpSecs)
}

func TestLux_HoldDetectionFollowsA30sFadeUp(t *testing.T) {
	t.Parallel()
	// GIVEN: a fade from 50% up to 75% over 30 s has just been sent
	f := darkOutsideFixture(t)
	f.env.MockHA.SetState(luxTestCans, "on", map[string]interface{}{"brightness": 128.0, "color_temp_kelvin": 5000.0})
	f.manager.luxTick()
	calls := f.lightCalls()
	require.Len(t, calls, 1)
	require.Equal(t, 75, calls[0].Data["brightness_pct"])
	require.Equal(t, luxUpSecs, calls[0].Data["transition"])
	held := func() bool { return f.manager.luxHeld(f.room) }

	// WHEN: the bridge reports a point halfway along the 30 s path, then the end state
	f.clock.Advance(15 * time.Second)
	f.manager.handleCansChange(f.room, canReport("on", 160, 5000)) // about 63%
	// THEN: neither is a manual change
	require.False(t, held(), "halfway report is on the path")

	f.clock.Advance(16 * time.Second)
	f.manager.handleCansChange(f.room, canReport("on", 191, 5000)) // 75%
	require.False(t, held(), "end-of-fade report is on the path")
}

func TestLux_HoldDetectionStillCatchesAChangeAgainstA30sFadeUp(t *testing.T) {
	t.Parallel()
	// GIVEN: a fade from 50% up to 75% over 30 s, 10 s in
	f := darkOutsideFixture(t)
	f.env.MockHA.SetState(luxTestCans, "on", map[string]interface{}{"brightness": 128.0, "color_temp_kelvin": 5000.0})
	f.manager.luxTick()
	f.clock.Advance(10 * time.Second)

	// WHEN: the cans report a level below the start of the fade and stay there
	f.changeByHand(canReport("on", 90, 5000)) // about 35%

	// THEN: the confirming read still sees 35% (off the path of the fade up), so it is manual
	assert.True(t, f.manager.luxHeld(f.room))
}
