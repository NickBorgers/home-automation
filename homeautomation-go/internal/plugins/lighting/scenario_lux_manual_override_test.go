package lighting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// User story: "When the lights start to dim and I don't want that, I turn the dial up. The system
// must stop the moment I touch the control, whenever that is."
//
// Invariant: no period exists in which a person's change to the cans is ignored. A report during
// a fade counts as manual when it is not on the path from the fade's start to its target.
//
// Regression for 2026-11-08 19:26–19:29: the plugin ignored reports for 70 s after each command
// (60 s fade + 10 s grace). It sent a new command every 60 s, so a dial turn made during the
// dimming was overwritten by the next tick.

// startDimmingFade returns a fixture whose cans were at 100% and that has just sent a 60 s fade
// down to 75% at 5000 K.
func startDimmingFade(t *testing.T) *luxFixture {
	t.Helper()
	f := newLuxFixture(t, LuxModeActive, false)
	f.env.MockHA.SetState(luxTestCans, "on", map[string]interface{}{"brightness": 255.0, "color_temp_kelvin": 5000.0})
	f.manager.luxTick()
	calls := f.lightCalls()
	require.Len(t, calls, 1)
	require.Equal(t, 75, calls[0].Data["brightness_pct"], "the plugin starts dimming")
	require.Equal(t, 60.0, calls[0].Data["transition"])
	f.env.MockHA.ClearServiceCalls()
	return f
}

func TestScenario_UserStory_DialTurnDuringDimmingStopsTheDimming(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		after      time.Duration
		brightness float64 // 0-255
		kelvin     float64
	}{
		// A turn to 100% within the first second is indistinguishable from a late report of the
		// starting level (100%). A person reacts to visible dimming, so 5 s is the earliest case.
		{"turned back to full a few seconds into the fade", 5 * time.Second, 255, 5000},
		{"turned all the way up mid-fade", 20 * time.Second, 255, 5000},
		{"turned up a little mid-fade", 30 * time.Second, 235, 5000}, // fade is at ~87%; dial sets 92%
		{"turned up as the fade ends", 59 * time.Second, 230, 5000},  // fade is at ~75%; dial sets 90%
		{"turned up in the old grace window", 65 * time.Second, 230, 5000},
		{"turned far down mid-fade", 20 * time.Second, 50, 5000},
		{"color changed mid-fade", 20 * time.Second, 235, 2700},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// GIVEN: the plugin is dimming the cans from 100% to 75% over 60 s
			t.Log("GIVEN: the plugin is dimming the cans")
			f := startDimmingFade(t)

			// WHEN: a person turns the dial while the plugin's command is still in effect
			t.Logf("WHEN: a person turns the dial %s after the command", tt.after)
			f.clock.Advance(tt.after)
			f.manager.handleCansChange(f.room, canReport("on", tt.brightness, tt.kelvin))

			// THEN: the room is held at once, and later ticks leave the lights alone
			t.Log("THEN: the room is held and the plugin sends nothing more")
			assert.True(t, f.manager.luxHeld(f.room), "the dial turn must be noticed immediately")
			for i := 0; i < 3; i++ {
				f.clock.Advance(time.Minute)
				f.manager.luxTick()
			}
			assert.Empty(t, f.lightCalls(), "no command may override the person's setting")
			assert.Contains(t, f.decision(), "held")
		})
	}
}

func TestScenario_UserStory_TheFadesOwnReportsDoNotStopTheDimming(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		after      time.Duration
		brightness float64 // 0-255
	}{
		{"late report of the starting level", 500 * time.Millisecond, 255},
		{"report of the target as the fade starts", 1 * time.Second, 191},
		{"report on the fade's path", 30 * time.Second, 223}, // ~87.5%
		{"report a little behind the fade", 30 * time.Second, 226},
		{"report of the target when the fade ends", 61 * time.Second, 191},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// GIVEN: the plugin is dimming the cans from 100% to 75% over 60 s
			f := startDimmingFade(t)

			// WHEN: the cans report a level that the fade itself produces
			f.clock.Advance(tt.after)
			f.manager.handleCansChange(f.room, canReport("on", tt.brightness, 5000))

			// THEN: the room is not held, and the next tick keeps dimming
			assert.False(t, f.manager.luxHeld(f.room))
			f.clock.Advance(time.Minute - tt.after)
			f.manager.luxTick()
			calls := f.lightCalls()
			require.Len(t, calls, 1, "the plugin keeps dimming")
			assert.Equal(t, 50, calls[0].Data["brightness_pct"])
		})
	}
}
