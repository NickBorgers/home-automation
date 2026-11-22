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
			f.changeByHand(canReport("on", tt.brightness, tt.kelvin))

			// THEN: the room is held once a fresh read a few seconds later confirms the change (no
			// command is sent in between), and later ticks leave the lights alone
			t.Log("THEN: the room is held and the plugin sends nothing more")
			assert.True(t, f.manager.luxHeld(f.room), "the dial turn must be noticed")
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

// User story: "The lights fade on their own. I did not touch them, so the system must keep
// following the curve."
//
// Invariant: one late or out-of-order report from a light group does not hold the room. A report
// off the fade path holds only when a fresh read a few seconds later is still off the path.
//
// Regression: a group's brightness wandered while two overlapping fades settled, went quiet, then
// sent one report 8 points past the target long after the fade had ended. The next reports
// stepped down to the commanded value, but that one report had already held the room.

// overlappingFades installs the commands the loop sends after a reset: a 60 s fade from the
// adopted 96% to 75%, and 42 s later a second 60 s fade to 63% that overlaps the first.
func overlappingFades(t *testing.T) (*luxFixture, time.Time) {
	t.Helper()
	return fadesOverlapping(t, 96, 75, 42*time.Second, 63)
}

// fadesOverlapping installs a 60 s fade from startPct to firstPct, and after overlap a second
// 60 s fade to secondPct that starts where the loop expects the first one to be.
func fadesOverlapping(t *testing.T, startPct, firstPct float64, overlap time.Duration, secondPct float64) (*luxFixture, time.Time) {
	t.Helper()
	f := newLuxFixture(t, LuxModeActive, false)
	t0 := f.clock.Now()
	first := &luxCommand{at: t0, transition: luxTransition, on: true, pct: firstPct, kelvin: 5000,
		fromOn: true, fromPct: startPct, fromKelvin: 5000}
	second := &luxCommand{at: t0.Add(overlap), transition: luxTransition, on: true, pct: secondPct, kelvin: 5000,
		fromOn: true, fromKelvin: 5000}
	second.fromPct, _ = first.levelAt(second.at)
	f.manager.luxMu.Lock()
	st := f.manager.luxRoom(f.room.HueGroup)
	st.cmd = first
	f.manager.luxMu.Unlock()
	f.env.MockHA.SetState(luxTestCans, "on", map[string]interface{}{"brightness": startPct / 100 * 255, "color_temp_kelvin": 5000.0})

	f.clock.Advance(overlap)
	f.manager.luxMu.Lock()
	st.previous, st.cmd = first, second
	f.manager.luxMu.Unlock()
	return f, t0
}

// cansReport is one brightness report from the cans, at an offset from the first command.
type cansReport struct {
	at         time.Duration
	brightness float64 // 0-255
}

// replayReports moves the clock to each report, stores it in Home Assistant and delivers it.
func replayReports(f *luxFixture, t0 time.Time, reports []cansReport) {
	for _, r := range reports {
		f.clock.Advance(t0.Add(r.at).Sub(f.clock.Now()))
		s := canReport("on", r.brightness, 5000)
		f.env.MockHA.SetState(luxTestCans, s.State, s.Attributes)
		f.manager.handleCansChange(f.room, s)
	}
}

func TestScenario_UserStory_LateGroupReportAfterOverlappingFadesDoesNotHold(t *testing.T) {
	t.Parallel()
	// GIVEN: two overlapping fades, 96% -> 75% and then -> 63%
	t.Log("GIVEN: the plugin sent overlapping fades to 75% and then 63%")
	f, t0 := overlappingFades(t)

	// WHEN: the group's reports wander while the fades settle and then go quiet. One late report
	// lands 8 points past the target about 80 s after the second fade ended, and the following
	// reports step down to the commanded value.
	t.Log("WHEN: a late report shows 71% long after the fade to 63% ended, then the light settles")
	replayReports(f, t0, []cansReport{
		{43 * time.Second, 189},  // 74%
		{45 * time.Second, 161},  // 63%
		{72 * time.Second, 188},  // 74%
		{181 * time.Second, 181}, // 71%: off the path
		{185 * time.Second, 176}, // 69%: still off the path, but moving
		{189 * time.Second, 171}, // 67%
		{193 * time.Second, 166}, // 65%
		{196 * time.Second, 161}, // 63%, as commanded
	})
	f.clock.Advance(time.Minute)

	// THEN: the room is not held, and the loop keeps following the curve
	t.Log("THEN: the room is not held")
	assert.False(t, f.manager.luxHeld(f.room), "nobody touched the lights")
	f.manager.luxTick()
	assert.NotContains(t, f.decision(), "held")
	assert.NotContains(t, f.decision(), "checking")
}

func TestScenario_UserStory_GroupLaggingAFadeDownDoesNotHold(t *testing.T) {
	t.Parallel()
	// GIVEN: a 60 s fade from 63% to 38%, and 34 s later a 60 s fade to 14% that overlaps it.
	// The loop expects the second fade to start at about 49%.
	t.Log("GIVEN: the plugin sent overlapping fades down to 38% and then 14%")
	f, t0 := fadesOverlapping(t, 63, 38, 34*time.Second, 14)

	// WHEN: the group lags the fade by several seconds. 44 s into the second fade it reports 28%,
	// where the modeled path is near 23%, more than the reverse tolerance behind it. The light
	// keeps falling at the fade's rate and ends at the commanded value.
	t.Log("WHEN: the group reports 28% 44 s into the fade to 14%, then keeps falling behind the path")
	replayReports(f, t0, []cansReport{
		{78 * time.Second, 71},  // 28%: behind the path
		{82 * time.Second, 64},  // 25%
		{87 * time.Second, 56},  // 22%
		{92 * time.Second, 48},  // 19%
		{97 * time.Second, 41},  // 16%
		{101 * time.Second, 36}, // 14%, as commanded
	})
	f.clock.Advance(time.Minute)

	// THEN: the room is not held
	t.Log("THEN: the room is not held")
	assert.False(t, f.manager.luxHeld(f.room), "a report that lags the fade is not a person")
	f.manager.luxTick()
	assert.NotContains(t, f.decision(), "held")
}

func TestScenario_UserStory_ManualChangeConfirmedByFreshReadHolds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		reports []cansReport
	}{
		{"turned down and left there", []cansReport{
			{181 * time.Second, 102}, // 40%
		}},
		{"turned up while the dial is still moving", []cansReport{
			{181 * time.Second, 204}, // 80%
			{184 * time.Second, 217}, // 85%
			{188 * time.Second, 230}, // 90%
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// GIVEN: the overlapping fades have ended at 63%
			t.Log("GIVEN: the plugin's fades to 63% have ended")
			f, t0 := overlappingFades(t)

			// WHEN: a person changes the cans, and a tick runs before the change is confirmed
			t.Log("WHEN: a person changes the cans and the light stays off the fade path")
			replayReports(f, t0, tt.reports[:1])
			f.env.MockHA.ClearServiceCalls()
			f.manager.luxTick()
			assert.Empty(t, f.lightCalls(), "no command while the change is being confirmed")
			assert.Contains(t, f.decision(), "checking")
			replayReports(f, t0, tt.reports[1:])
			for i := 0; i < luxHoldConfirmReads; i++ {
				f.clock.Advance(luxHoldConfirmDelay) // the mock clock fires one re-read per Advance
			}

			// THEN: the room is held and later ticks leave the cans alone
			t.Log("THEN: the room is held")
			assert.True(t, f.manager.luxHeld(f.room), "a person's change must hold the room")
			f.clock.Advance(time.Minute)
			f.manager.luxTick()
			assert.Empty(t, f.lightCalls())
			assert.Contains(t, f.decision(), "held")
		})
	}
}

func TestScenario_UserStory_PresenceDuringAClearedHoldCheckIsAppliedAtOnce(t *testing.T) {
	t.Parallel()
	// GIVEN: the fades to 63% have ended, the room boosts its brightness while occupied in the
	// day phase, and nobody is in it. A late off-path report starts a hold check.
	t.Log("GIVEN: a late off-path report is being checked in an empty room")
	f, t0 := overlappingFades(t)
	// A 75 lux target with no daylight: about 36% empty, about 72% occupied.
	f.room.LuxControl.LuxScale = 0.25
	f.setOutdoor("0")
	f.room.LuxControl.PresenceOverride = &LuxPresenceOverride{
		Variable: "isKitchenOccupied", Phases: []string{"day"}, BrightnessMultiplierPct: 200,
	}
	require.NoError(t, f.env.StateMgr.SetString("dayPhase", "day"))
	require.NoError(t, f.env.StateMgr.SetBool("isKitchenOccupied", false))
	replayReports(f, t0, []cansReport{{181 * time.Second, 181}}) // 71%: off the path
	f.env.MockHA.ClearServiceCalls()

	// WHEN: someone walks in during the check, and the confirming read lands on the fade path
	t.Log("WHEN: someone walks in during the check, then the light reads 63% as commanded")
	require.NoError(t, f.env.StateMgr.SetBool("isKitchenOccupied", true))
	f.manager.handleLuxPresenceChange(f.room, "isKitchenOccupied", false, true)
	assert.Empty(t, f.lightCalls(), "no command while the check runs")
	f.env.MockHA.SetState(luxTestCans, "on", map[string]interface{}{"brightness": 161.0, "color_temp_kelvin": 5000.0})
	f.clock.Advance(luxHoldConfirmDelay) // the confirming read; no tick runs in this fixture

	// THEN: the boosted target is sent right away, not at the next tick
	t.Log("THEN: the presence boost is sent as soon as the check clears")
	assert.False(t, f.manager.luxHeld(f.room))
	calls := f.lightCalls()
	require.Len(t, calls, 1, "the skipped evaluation runs when the check clears")
	st := f.manager.GetShadowState().Outputs.LuxControl["Living Room"]
	assert.Equal(t, "presence_override", st.TargetSource)
	assert.Greater(t, calls[0].Data["brightness_pct"], 63, "brighter than the unboosted command")
	assert.Equal(t, int(st.BrightnessPct), calls[0].Data["brightness_pct"])
}

func TestLux_HoldCheckCancelledWhenEveryoneLeaves(t *testing.T) {
	t.Parallel()
	// GIVEN: an off-path report is waiting for its confirming read
	f, t0 := overlappingFades(t)
	replayReports(f, t0, []cansReport{{181 * time.Second, 102}})

	// WHEN: everyone leaves before the read
	f.manager.handlePresenceChange("isAnyoneHome", true, false)
	f.clock.Advance(luxHoldConfirmReads * luxHoldConfirmDelay)

	// THEN: the stale check does not hold the room
	assert.False(t, f.manager.luxHeld(f.room))
}

// User story: "The lights change color on their own. I did not touch them, so the system must
// keep following the curve."
//
// Invariant: a color temperature report is judged like a brightness report. While a commanded
// transition is still running, a report within the range the fades pass through does not hold
// the room. The check waits for the fade to end and then compares the light with the final
// commanded values. A jump far off the fades, or a change after the fade, still holds.
//
// Regression: after a reset the plugin sent a color fade, and 25 s later a second color fade that
// overlapped it. Near the end of the second fade the group reported a color a little behind the
// modeled path. The confirming read 5 s later showed the same value, so the room was held while
// the fade still had seconds to run.

// overlappingColorFades installs the commands the loop sends after a reset: a 60 s fade from the
// adopted 99% at 4000 K to 3600 K, and 25 s later a second 60 s fade to 3200 K that starts where
// the loop expects the first one to be (about 3833 K). The second fade ends 85 s after the first
// command.
func overlappingColorFades(t *testing.T) (*luxFixture, time.Time) {
	t.Helper()
	f := newLuxFixture(t, LuxModeActive, false)
	t0 := f.clock.Now()
	first := &luxCommand{at: t0, transition: luxTransition, on: true, pct: 99, kelvin: 3600,
		fromOn: true, fromPct: 99, fromKelvin: 4000}
	second := &luxCommand{at: t0.Add(25 * time.Second), transition: luxTransition, on: true, pct: 99, kelvin: 3200,
		fromOn: true}
	second.fromPct, second.fromKelvin = first.levelAt(second.at)
	f.manager.luxMu.Lock()
	st := f.manager.luxRoom(f.room.HueGroup)
	st.cmd = first
	f.manager.luxMu.Unlock()
	f.env.MockHA.SetState(luxTestCans, "on", map[string]interface{}{"brightness": 252.0, "color_temp_kelvin": 4000.0})

	f.clock.Advance(25 * time.Second)
	f.manager.luxMu.Lock()
	st.previous, st.cmd = first, second
	f.manager.luxMu.Unlock()
	return f, t0
}

// colorReport is one color temperature report from the cans at 99%, at an offset from the first
// command. Home Assistant keeps it as the light's state until the next one.
type colorReport struct {
	at     time.Duration
	kelvin float64
}

// replayColorReports moves the clock to each report, stores it in Home Assistant and delivers it.
func replayColorReports(f *luxFixture, t0 time.Time, reports []colorReport) {
	for _, r := range reports {
		f.clock.Advance(t0.Add(r.at).Sub(f.clock.Now()))
		s := canReport("on", 252, r.kelvin)
		f.env.MockHA.SetState(luxTestCans, s.State, s.Attributes)
		f.manager.handleCansChange(f.room, s)
	}
}

func TestScenario_UserStory_ColorBehindOverlappingFadesDoesNotHold(t *testing.T) {
	t.Parallel()
	// GIVEN: overlapping color fades, 4000 K -> 3600 K and then -> 3200 K, ending at 85 s
	t.Log("GIVEN: the plugin sent overlapping color fades to 3600 K and then 3200 K")
	f, t0 := overlappingColorFades(t)

	// WHEN: 78 s in, the group reports 3365 K, a little behind the modeled path (about 3284 K).
	// The confirming read 5 s later shows the same value. The light reaches 3200 K as the fade ends.
	t.Log("WHEN: the cans report 3365 K near the end of the fade, unchanged on the next read, then 3200 K")
	replayColorReports(f, t0, []colorReport{{78 * time.Second, 3365}})
	f.manager.luxMu.Lock()
	checking := f.manager.luxRoom(f.room.HueGroup).checking != nil
	f.manager.luxMu.Unlock()
	require.True(t, checking, "the report is off the modeled path, so it is checked")
	f.clock.Advance(t0.Add(83 * time.Second).Sub(f.clock.Now())) // the first confirming read: still 3365 K
	assert.False(t, f.manager.luxHeld(f.room), "the fade is still running")
	replayColorReports(f, t0, []colorReport{{86 * time.Second, 3200}})
	for i := 0; i < luxHoldConfirmReads; i++ {
		f.clock.Advance(luxHoldConfirmDelay)
	}

	// THEN: the room is not held, and the loop keeps following the curve
	t.Log("THEN: the room is not held")
	assert.False(t, f.manager.luxHeld(f.room), "nobody touched the lights")
	f.clock.Advance(time.Minute)
	f.manager.luxTick()
	assert.NotContains(t, f.decision(), "held")
	assert.NotContains(t, f.decision(), "checking")
}

func TestScenario_UserStory_ManualColorChangeDuringOrAfterOverlappingFadesHolds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		at   time.Duration
		// kelvin is the color a person sets and leaves.
		kelvin float64
		// holdBy is the latest offset from the first command at which the room must be held.
		holdBy time.Duration
	}{
		// Far outside any color the fades pass through: held on the first confirming read, while
		// the fade still runs.
		{"warm color set mid-fade", 50 * time.Second, 2200, 55 * time.Second},
		{"cool color set mid-fade", 50 * time.Second, 5000, 55 * time.Second},
		// Within the fades' range while the fade runs: held once the fade has ended and the light
		// still differs from the final commanded 3200 K.
		{"color a little off set mid-fade", 65 * time.Second, 3800, 95 * time.Second},
		// After the fade: the same value as the regression's report is a person's change.
		{"color changed after the fade", 120 * time.Second, 3365, 125 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// GIVEN: overlapping color fades to 3600 K and then 3200 K
			t.Log("GIVEN: the plugin sent overlapping color fades")
			f, t0 := overlappingColorFades(t)

			// WHEN: a person sets a color and leaves it
			t.Logf("WHEN: a person sets %.0f K %s after the first command", tt.kelvin, tt.at)
			replayColorReports(f, t0, []colorReport{{tt.at, tt.kelvin}})
			f.env.MockHA.ClearServiceCalls()
			f.manager.luxTick()
			assert.Empty(t, f.lightCalls(), "no command while the change is being confirmed")
			f.clock.Advance(t0.Add(tt.holdBy).Sub(f.clock.Now()))

			// THEN: the room is held in time, and later ticks leave the cans alone
			t.Log("THEN: the room is held")
			assert.True(t, f.manager.luxHeld(f.room), "a person's change must hold the room")
			f.clock.Advance(time.Minute)
			f.manager.luxTick()
			assert.Empty(t, f.lightCalls())
			assert.Contains(t, f.decision(), "held")
		})
	}
}
