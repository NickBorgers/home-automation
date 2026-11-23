package lighting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// User story: "The lights dim on their own and I did not touch them. The system must keep
// following the curve, even when the bridge tells Home Assistant about the dimming minutes late."
//
// Invariant: a stale reading never holds the room. After the fade ends, the hold check decides
// only on a report that arrived after the fade ended. When none arrives within
// luxFreshReportWait, the check gives up without holding and the loop resumes. While the check
// waits, the loop sends nothing, so a person's change is never overwritten. A person's change
// still holds once a fresh report shows it, also when that report arrives minutes late.
//
// Regression: a light group whose bridge reports each member minutes apart. The
// plugin faded the group from 80% to 55%. Early in the fade the group read 78%, off the path, so
// a check started. When the fade had ended, no newer report had arrived, so the check still read
// 78% and held the room. The members reported the target more than a minute later: the fade had worked.

// staleReportFade installs a 60 s fade of the cans from 80% to 55% at 5000 K, sent at t0.
func staleReportFade(t *testing.T) (*luxFixture, time.Time) {
	t.Helper()
	f := newLuxFixture(t, LuxModeActive, false)
	t0 := f.clock.Now()
	f.manager.luxMu.Lock()
	f.manager.luxRoom(f.room.HueGroup).cmd = &luxCommand{at: t0, transition: luxTransition, on: true, pct: 55,
		kelvin: 5000, fromOn: true, fromPct: 80, fromKelvin: 5000}
	f.manager.luxMu.Unlock()
	f.env.MockHA.SetState(luxTestCans, "on", map[string]interface{}{"brightness": pct255(80), "color_temp_kelvin": 5000.0})
	return f, t0
}

// pct255 converts a brightness percent to Home Assistant's 0-255 scale.
func pct255(pct float64) float64 { return pct / 100 * 255 }

// checking reports whether a hold check is running for the fixture's room.
func (f *luxFixture) checking() bool {
	f.manager.luxMu.Lock()
	defer f.manager.luxMu.Unlock()
	return f.manager.luxRoom(f.room.HueGroup).checking != nil
}

// advanceTo moves the clock to an offset from t0, one second at a time, so that every re-read
// the hold check schedules runs at its own time.
func (f *luxFixture) advanceTo(t0 time.Time, at time.Duration) {
	for f.clock.Now().Before(t0.Add(at)) {
		f.clock.Advance(min(time.Second, t0.Add(at).Sub(f.clock.Now())))
	}
}

// report moves the clock to an offset from t0 and delivers a brightness report, as Home
// Assistant does when the bridge reports the cans.
func (f *luxFixture) report(t0 time.Time, at time.Duration, pct float64) {
	f.advanceTo(t0, at)
	replayReports(f, t0, []cansReport{{at, pct255(pct)}})
}

func TestScenario_UserStory_StaleGroupReadingAfterTheFadeDoesNotHold(t *testing.T) {
	t.Parallel()
	// GIVEN: the plugin fades the cans from 80% to 55% over 60 s
	t.Log("GIVEN: the plugin fades the cans from 80% to 55%")
	f, t0 := staleReportFade(t)

	// WHEN: 22 s in the group reads 78%, off the path, and nothing newer arrives until well after
	// the fade. Then the members report one at a time and the group steps down to 54%.
	t.Log("WHEN: the group reads 78% early in the fade, then reports 54% minutes later")
	f.report(t0, 22*time.Second, 78)
	require.True(t, f.checking(), "the early report is off the fade path")
	f.env.MockHA.ClearServiceCalls()
	for _, at := range []time.Duration{30 * time.Second, 70 * time.Second, 120 * time.Second} {
		f.advanceTo(t0, at)
		f.manager.luxTick()
		assert.False(t, f.manager.luxHeld(f.room), "a stale reading is not enough to hold (at %s)", at)
		assert.Empty(t, f.lightCalls(), "no command while the check waits (at %s)", at)
		assert.Contains(t, f.decision(), "checking")
	}
	// Some members have reported: the group sits between the stale level and the target.
	f.report(t0, 126*time.Second, 74)
	f.report(t0, 134*time.Second, 66)
	f.advanceTo(t0, 140*time.Second)
	assert.False(t, f.manager.luxHeld(f.room), "a group still converging on the target is not held")
	assert.True(t, f.checking())
	f.report(t0, 142*time.Second, 54)
	f.advanceTo(t0, 150*time.Second)

	// THEN: the room is not held, and the skipped evaluation runs as soon as the check clears
	t.Log("THEN: the room is not held and the loop resumes")
	assert.False(t, f.manager.luxHeld(f.room), "nobody touched the lights")
	assert.False(t, f.checking())
	assert.NotContains(t, f.decision(), "checking")
	assert.NotContains(t, f.decision(), "held")
}

func TestScenario_UserStory_NoFreshReportGivesUpWithoutHolding(t *testing.T) {
	t.Parallel()
	// GIVEN: the plugin fades the cans from 80% to 55%, and the group read 78% early in the fade
	t.Log("GIVEN: a check started on a 78% reading early in the fade")
	f, t0 := staleReportFade(t)
	f.report(t0, 22*time.Second, 78)
	f.manager.luxTick() // skipped while the check runs

	// WHEN: no newer report arrives at all
	t.Log("WHEN: the bridge sends nothing more")
	f.advanceTo(t0, luxTransition+luxFreshReportWait-5*time.Second)
	assert.True(t, f.checking(), "the check waits for a fresh report")
	assert.False(t, f.manager.luxHeld(f.room))
	f.env.MockHA.ClearServiceCalls()
	f.advanceTo(t0, luxTransition+luxFreshReportWait+10*time.Second)

	// THEN: the check gives up without holding, and the skipped evaluation runs at once
	t.Log("THEN: the room is not held and the loop resumes")
	assert.False(t, f.manager.luxHeld(f.room), "a stale reading never holds the room")
	assert.False(t, f.checking())
	assert.NotContains(t, f.decision(), "checking")
	assert.NotContains(t, f.decision(), "held")
}

func TestScenario_UserStory_ManualChangeMidFadeReportedLateHolds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// staleFirst is true when the group read 78% early in the fade, which started a check
		// before the person's change was reported.
		staleFirst bool
		// pct is the level a person sets 30 s into the fade. Its report arrives 150 s in.
		pct float64
		// holdBy is the latest offset from the command at which the room must be held.
		holdBy time.Duration
	}{
		// Within the range the fade passes through: the late report may be a group whose members
		// have not all reported, so the check waits out luxFreshReportWait before it holds.
		{"within the fade's range, after a stale reading", true, 65, luxTransition + luxFreshReportWait + 10*time.Second},
		// Far outside the fade's range: held at once.
		{"far below the fade, after a stale reading", true, 35, 165 * time.Second},
		// No earlier reading: the late report is the first one off the path, so it is confirmed
		// by the next read.
		{"within the fade's range, the only report", false, 65, 160 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// GIVEN: the plugin fades the cans from 80% to 55%
			t.Log("GIVEN: the plugin fades the cans from 80% to 55%")
			f, t0 := staleReportFade(t)
			if tt.staleFirst {
				f.report(t0, 22*time.Second, 78)
			}
			f.env.MockHA.ClearServiceCalls()

			// WHEN: a person sets a level 30 s in, and the bridge reports it minutes later
			t.Logf("WHEN: a person sets %.0f%% mid-fade and the report arrives 150 s in", tt.pct)
			for _, at := range []time.Duration{30 * time.Second, 100 * time.Second} {
				f.advanceTo(t0, at)
				if tt.staleFirst {
					f.manager.luxTick() // skipped: the check is waiting
				}
			}
			f.report(t0, 150*time.Second, tt.pct)
			f.advanceTo(t0, tt.holdBy)

			// THEN: the room is held, and the plugin never overwrote the person's level
			t.Log("THEN: the room is held")
			assert.True(t, f.manager.luxHeld(f.room), "a person's change must hold the room")
			f.clock.Advance(time.Minute)
			f.manager.luxTick()
			if tt.staleFirst {
				assert.Empty(t, f.lightCalls(), "no command while the check waits, nor after the hold")
			}
			assert.Contains(t, f.decision(), "held")
		})
	}
}

func TestScenario_UserStory_ManualChangeAfterTheFadeHolds(t *testing.T) {
	t.Parallel()
	// GIVEN: the plugin faded the cans from 80% to 55%, and the fade has ended
	t.Log("GIVEN: the fade to 55% has ended")
	f, t0 := staleReportFade(t)
	f.env.MockHA.ClearServiceCalls()

	// WHEN: a person turns the cans up to 78% two minutes in, and the report arrives at once
	t.Log("WHEN: a person sets 78% after the fade")
	f.report(t0, 120*time.Second, 78)
	f.advanceTo(t0, 125*time.Second)

	// THEN: the fresh report holds the room on the first confirming read
	t.Log("THEN: the room is held")
	assert.True(t, f.manager.luxHeld(f.room), "a fresh report off the commanded value is a person")
	f.manager.luxTick()
	assert.Empty(t, f.lightCalls())
	assert.Contains(t, f.decision(), "held")
}
