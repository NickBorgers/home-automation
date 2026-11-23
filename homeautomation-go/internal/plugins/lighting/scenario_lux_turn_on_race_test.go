package lighting

import (
	"testing"
	"time"

	"homeautomation/internal/ha"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// User story: "I turn the lights on with a control that talks to the Hue bridge directly. The
// system answers by fading them to its own level. That is not me overriding it, so it must not
// hold the room, and the lights must end at the system's level."
//
// Invariant: a command sent within luxFreshTurnOnWindow of the cans turning on answers that
// turn-on. When a confirmed mismatch follows it, the command is sent once more instead of holding.
// The re-sent command does not answer a turn-on, so the same mismatch after it holds the room.
// A turn-on older than the window holds on a confirmed mismatch, as before.
//
// Regression: the plugin had no last command for a room and the cans were off. A light turned on
// by hand outside Home Assistant, and the turn-on reached Home Assistant a moment before the
// plugin's command. The plugin adopted the turn-on level and faded from it. The bridge then
// finished applying the turn-on's own color after the command, so the cans kept the plugin's
// brightness but the turn-on's color. The hold check confirmed that color and held the room.

const (
	// raceTurnOnPct and raceTurnOnKelvin are the level the cans turn on at, outside the plugin.
	raceTurnOnPct    = 30
	raceTurnOnKelvin = 4500
	// The plugin's answer at noon: 44% from the curve, and the color slewed one maxKelvinStep
	// from the adopted 4500 K toward the curve's 5000 K.
	raceCmdPct    = 44
	raceCmdKelvin = 4900
)

// cansOn builds an "on" report for the cans that last turned on at turnedOn.
func cansOn(pct, kelvin float64, turnedOn time.Time) *ha.State {
	s := canReport("on", pct255(pct), kelvin)
	s.LastChanged = turnedOn
	return s
}

// deliver stores a report in Home Assistant and hands it to the plugin.
func (f *luxFixture) deliver(s *ha.State) {
	f.env.MockHA.SetMockState(luxTestCans, s)
	f.manager.handleCansChange(f.room, s)
}

// holdHelperTurnedOn reports whether the plugin turned the room's hold helper on.
func (f *luxFixture) holdHelperTurnedOn() bool {
	for _, c := range f.env.MockHA.GetServiceCalls() {
		if c.Domain == "input_boolean" && c.Service == "turn_on" && c.Data["entity_id"] == luxHoldHelper(f.room) {
			return true
		}
	}
	return false
}

// turnOnRace runs the race up to the plugin's answer: the cans turn on by hand at turnedOn,
// the plugin has no last command, and the loop evaluates the room at t0. It returns t0.
func turnOnRace(t *testing.T, f *luxFixture, turnedOn time.Time) time.Time {
	t.Helper()
	f.deliver(cansOn(raceTurnOnPct, raceTurnOnKelvin, turnedOn))
	t0 := f.clock.Now()
	f.manager.luxTick()
	calls := f.lightCalls()
	require.Len(t, calls, 1, "the plugin answers the turn-on")
	assert.Equal(t, raceCmdPct, calls[0].Data["brightness_pct"])
	assert.Equal(t, raceCmdKelvin, calls[0].Data["color_temp_kelvin"])
	assert.Equal(t, luxTransitionUp.Seconds(), calls[0].Data["transition"], "fades from the adopted level")
	f.env.MockHA.ClearServiceCalls()
	return t0
}

// bridgeKeepsTurnOnColor plays what the bridge does after the plugin's command: the cans take the
// plugin's brightness but keep the turn-on color. A report mid-fade is off the fade path, and a
// fresh report after the fade shows the turn-on color. It returns when the hold check has decided.
func bridgeKeepsTurnOnColor(t *testing.T, f *luxFixture, t0, turnedOn time.Time) {
	t.Helper()
	f.advanceTo(t0, 28*time.Second)
	f.deliver(cansOn(raceCmdPct, 4700, turnedOn))
	require.True(t, f.checking(), "the mid-fade color is off the fade path")
	f.env.MockHA.SetMockState(luxTestCans, cansOn(raceCmdPct, raceTurnOnKelvin, turnedOn))
	f.advanceTo(t0, 40*time.Second)
	f.deliver(cansOn(raceCmdPct, raceTurnOnKelvin, turnedOn))
	f.advanceTo(t0, 60*time.Second)
	require.False(t, f.checking(), "the check has decided")
}

func TestScenario_UserStory_TurnOnRaceSendsTheCommandAgainInsteadOfHolding(t *testing.T) {
	t.Parallel()
	// GIVEN: the plugin has no last command, and the cans turn on by hand a moment before the
	// loop evaluates the room
	t.Log("GIVEN: the cans turn on by hand just before the plugin's command")
	f := newLuxFixture(t, LuxModeActive, false)
	turnedOn := f.clock.Now()
	f.clock.Advance(330 * time.Millisecond)
	t0 := turnOnRace(t, f, turnedOn)

	// WHEN: the bridge applies the turn-on's color after the command
	t.Log("WHEN: the cans keep the turn-on color after the fade")
	bridgeKeepsTurnOnColor(t, f, t0, turnedOn)

	// THEN: the room is not held, and the command is sent once more with the same targets
	t.Log("THEN: the command is sent again, and the room is not held")
	assert.False(t, f.manager.luxHeld(f.room), "the mismatch is a race with the turn-on, not a person")
	assert.False(t, f.holdHelperTurnedOn(), "the hold helper stays off")
	calls := f.lightCalls()
	require.Len(t, calls, 1, "one re-sent command")
	assert.Equal(t, "turn_on", calls[0].Service)
	assert.Equal(t, raceCmdPct, calls[0].Data["brightness_pct"])
	assert.Equal(t, raceCmdKelvin, calls[0].Data["color_temp_kelvin"])
	assert.Equal(t, float64(maxReturnHomeTransition), calls[0].Data["transition"], "a short transition")

	// The bridge takes the re-sent command: the cans reach the plugin's level and stay unheld.
	f.env.MockHA.ClearServiceCalls()
	f.advanceTo(t0, 70*time.Second)
	f.deliver(cansOn(raceCmdPct, raceCmdKelvin, turnedOn))
	f.advanceTo(t0, 2*time.Minute)
	assert.False(t, f.checking())
	assert.False(t, f.manager.luxHeld(f.room))
	assert.False(t, f.holdHelperTurnedOn())
}

func TestScenario_UserStory_TurnOnRaceMismatchAfterResendHolds(t *testing.T) {
	t.Parallel()
	// GIVEN: the plugin sent its answer to a fresh turn-on again after the race
	t.Log("GIVEN: the plugin re-sent its command after a turn-on race")
	f := newLuxFixture(t, LuxModeActive, false)
	turnedOn := f.clock.Now()
	f.clock.Advance(330 * time.Millisecond)
	t0 := turnOnRace(t, f, turnedOn)
	bridgeKeepsTurnOnColor(t, f, t0, turnedOn)
	require.False(t, f.manager.luxHeld(f.room))
	require.Len(t, f.lightCalls(), 1)
	f.env.MockHA.ClearServiceCalls()

	// WHEN: after the re-sent fade, the cans still show the turn-on color
	t.Log("WHEN: the cans show the turn-on color again after the re-sent command")
	f.advanceTo(t0, 70*time.Second)
	f.deliver(cansOn(raceCmdPct, raceTurnOnKelvin, turnedOn))
	f.advanceTo(t0, 2*time.Minute)

	// THEN: that is a person's change, so the room is held and nothing more is sent
	t.Log("THEN: the room is held")
	assert.True(t, f.manager.luxHeld(f.room), "a mismatch after the re-send is a person")
	assert.True(t, f.holdHelperTurnedOn())
	assert.Empty(t, f.lightCalls(), "the command is sent again only once")
}

func TestScenario_UserStory_OlderTurnOnMismatchStillHolds(t *testing.T) {
	t.Parallel()
	// GIVEN: the cans turned on well before the plugin's command
	t.Log("GIVEN: the cans turned on by hand 10 s before the plugin's command")
	f := newLuxFixture(t, LuxModeActive, false)
	turnedOn := f.clock.Now()
	f.clock.Advance(10 * time.Second)
	require.Greater(t, 10*time.Second, luxFreshTurnOnWindow)
	t0 := turnOnRace(t, f, turnedOn)

	// WHEN: the cans show a color off the commanded one after the fade
	t.Log("WHEN: the cans show another color after the fade")
	bridgeKeepsTurnOnColor(t, f, t0, turnedOn)

	// THEN: the room is held, as for any confirmed manual change
	t.Log("THEN: the room is held")
	assert.True(t, f.manager.luxHeld(f.room), "a turn-on older than the window is not a race")
	assert.True(t, f.holdHelperTurnedOn())
	assert.Empty(t, f.lightCalls())
}
