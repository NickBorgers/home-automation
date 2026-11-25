package lighting

import (
	"testing"
	"time"

	"homeautomation/internal/ha"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// User story: "The lights fade on their own and I did not touch them. The system must keep
// following the curve, even when the bulbs in a light group tell Home Assistant about the fade
// one at a time, minutes apart."
//
// Invariant: a light group's level is the average of its members' last reports. A group report
// that arrives after the fade can still average members whose own last report is from before the
// fade ended. Such a member is stale: it is never enough to hold the room. The hold check judges
// each member. It holds only when a member is off the fade path with a report from after the fade
// ended, and no off-path member is stale. While a stale member is off the path, the check waits
// for it, and it gives up without holding when luxFreshReportWait ends. A person's change reaches
// every member it touches with fresh reports, so it still holds.
//
// Regression: a Hue room or zone light group whose member bulbs report one at a time. After a fade
// ended, one member reported the target, so the group's average moved and arrived as a fresh
// report off the path. Most members still showed levels from during the fade, and the room held.

// groupMembers are the member bulbs of the test cans group.
var groupMembers = []string{
	"light.test_bulb_1", "light.test_bulb_2", "light.test_bulb_3", "light.test_bulb_4",
	"light.test_bulb_5", "light.test_bulb_6", "light.test_bulb_7",
}

// groupFade installs a command to the cans, sent at t0. Each member's last report is pct,
// reported at updated. The cans group is set to the members' average, as Home Assistant
// computes it.
func groupFade(t *testing.T, cmd *luxCommand, members []string, pct float64,
	updated time.Duration) (*luxFixture, time.Time) {
	t.Helper()
	f := newLuxFixture(t, LuxModeActive, false)
	t0 := f.clock.Now()
	cmd.at = t0
	f.manager.luxMu.Lock()
	f.manager.luxRoom(f.room.HueGroup).cmd = cmd
	f.manager.luxMu.Unlock()
	for _, id := range members {
		f.setMember(id, pct, t0.Add(updated))
	}
	f.setGroup(members)
	return f, t0
}

// setMember stores a member bulb's last report: on at pct, reported at updated. A pct of 0 is off.
func (f *luxFixture) setMember(id string, pct float64, updated time.Time) {
	s := &ha.State{EntityID: id, State: "off", Attributes: map[string]interface{}{},
		LastChanged: updated, LastUpdated: updated}
	if pct > 0 {
		s.State = "on"
		s.Attributes["brightness"] = pct255(pct)
		s.Attributes["color_temp_kelvin"] = 5000.0
	}
	f.env.MockHA.SetMockState(id, s)
}

// setGroup stores the cans group as Home Assistant computes it from the members' last reports:
// on when any member is on, at the average brightness of the members that are on. It returns
// the group's state.
func (f *luxFixture) setGroup(members []string) *ha.State {
	ids := make([]interface{}, len(members))
	sum, on := 0.0, 0
	for i, id := range members {
		ids[i] = id
		s, err := f.env.MockHA.GetState(id)
		if err != nil || s.State != "on" {
			continue
		}
		b, _ := numberAttr(s.Attributes, "brightness")
		sum += b
		on++
	}
	g := &ha.State{EntityID: luxTestCans, State: "off", Attributes: map[string]interface{}{"entity_id": ids},
		LastChanged: f.clock.Now(), LastUpdated: f.clock.Now()}
	if on > 0 {
		g.State = "on"
		g.Attributes["brightness"] = sum / float64(on)
		g.Attributes["color_temp_kelvin"] = 5000.0
	}
	f.env.MockHA.SetMockState(luxTestCans, g)
	return g
}

// memberReport moves the clock to an offset from t0, where a member reports pct. Home Assistant
// recomputes the group, and the group's report reaches the plugin.
func (f *luxFixture) memberReport(t0 time.Time, at time.Duration, id string, pct float64, members []string) {
	f.advanceTo(t0, at)
	f.setMember(id, pct, f.clock.Now())
	f.manager.handleCansChange(f.room, f.setGroup(members))
}

func TestScenario_UserStory_GroupReportAveragingStaleMembersDoesNotHold(t *testing.T) {
	t.Parallel()
	// GIVEN: the plugin fades a 7-member group from 77% to 52% over 60 s. Three members last
	// reported 52% long before the command.
	t.Log("GIVEN: the plugin fades a 7-member group from 77% to 52%")
	members := groupMembers
	f, t0 := groupFade(t, &luxCommand{transition: luxTransition, on: true, pct: 52, kelvin: 5000,
		fromOn: true, fromPct: 77, fromKelvin: 5000}, members, 52, -10*time.Minute)
	for _, id := range members[:4] {
		f.setMember(id, 77, t0.Add(-10*time.Minute))
	}
	f.setGroup(members)

	// WHEN: four members report during the fade, each on the path at its own time. Then nothing
	// comes until a second round after the fade, one member at a time.
	t.Log("WHEN: four members report mid-fade levels, then report the target one at a time after the fade")
	f.memberReport(t0, 3*time.Second, members[0], 76, members)
	f.memberReport(t0, 11*time.Second, members[1], 72, members)
	f.memberReport(t0, 18*time.Second, members[2], 69, members)
	f.memberReport(t0, 27*time.Second, members[3], 66, members)
	require.False(t, f.checking(), "the group stays on the fade path during the fade")
	f.env.MockHA.ClearServiceCalls()

	// The first member of the second round reports the target. The group's average moves, but
	// three members still show mid-fade levels, so the fresh group report is off the path.
	f.memberReport(t0, 122*time.Second, members[0], 52, members)
	require.True(t, f.checking(), "the group's average is off the path, so a check starts")
	f.advanceTo(t0, 128*time.Second)
	f.manager.luxTick() // skipped while the check runs
	assert.False(t, f.manager.luxHeld(f.room), "members whose last report is from the fade are not enough to hold")
	assert.True(t, f.checking(), "the check waits for the stale members")
	assert.Empty(t, f.lightCalls(), "no command while the check waits")

	// The next member reports the target, and the group comes back to the path.
	f.memberReport(t0, 131*time.Second, members[1], 52, members)
	f.advanceTo(t0, 140*time.Second)

	// THEN: the room is not held, and the skipped evaluation runs as soon as the check clears
	t.Log("THEN: the room is not held and the loop resumes")
	assert.False(t, f.manager.luxHeld(f.room), "nobody touched the lights")
	assert.False(t, f.checking())
	assert.NotContains(t, f.decision(), "checking")
	assert.NotContains(t, f.decision(), "held")
}

func TestScenario_UserStory_SilentStaleMembersGiveUpWithoutHolding(t *testing.T) {
	t.Parallel()
	// GIVEN: the plugin turns a 4-member group on from off, to 72% over 5 s
	t.Log("GIVEN: the plugin turns a 4-member group on to 72% with a 5 s fade")
	members := groupMembers[:4]
	f, t0 := groupFade(t, &luxCommand{transition: 5 * time.Second, on: true, pct: 72, kelvin: 5000},
		members, 0, -time.Hour)
	core, logs := observer.New(zap.InfoLevel)
	f.manager.logger = zap.New(core)

	// WHEN: every member reports on during the fade. After the fade two members report the
	// target, and the other two never report again.
	t.Log("WHEN: two members report the target after the fade, two stay at their turn-on level")
	f.advanceTo(t0, 4*time.Second)
	for _, id := range members {
		f.setMember(id, 56, f.clock.Now())
	}
	f.manager.handleCansChange(f.room, f.setGroup(members))
	require.False(t, f.checking(), "the turn-on report is on the fade path")
	f.memberReport(t0, 7*time.Second, members[0], 72, members)
	require.True(t, f.checking(), "the group's average is off the path, so a check starts")
	f.memberReport(t0, 9*time.Second, members[1], 72, members)
	f.env.MockHA.ClearServiceCalls()
	for _, at := range []time.Duration{20 * time.Second, 80 * time.Second, 170 * time.Second} {
		f.advanceTo(t0, at)
		f.manager.luxTick() // skipped while the check runs
		assert.False(t, f.manager.luxHeld(f.room), "stale members are not enough to hold (at %s)", at)
		assert.True(t, f.checking(), "the check waits for the stale members (at %s)", at)
		assert.Empty(t, f.lightCalls(), "no command while the check waits (at %s)", at)
	}
	f.advanceTo(t0, 5*time.Second+luxFreshReportWait+10*time.Second)

	// THEN: the check gives up without holding, and the skipped evaluation runs at once
	t.Log("THEN: the room is not held and the loop resumes")
	assert.False(t, f.manager.luxHeld(f.room), "stale members never hold the room")
	assert.False(t, f.checking())
	assert.NotContains(t, f.decision(), "checking")
	assert.NotContains(t, f.decision(), "held")
	gaveUp := logs.FilterMessageSnippet("no fresh report").All()
	require.Len(t, gaveUp, 1, "the give-up is logged")
	assert.Equal(t, int64(2), gaveUp[0].ContextMap()["stale_members"], "the log counts the stale members")
}

func TestScenario_UserStory_ManualChangeToGroupMembersHolds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// changed are the members a person sets to 80%, and gap is the time between their reports.
		changed int
		gap     time.Duration
	}{
		{"every member reports at once", 4, 0},
		{"members report one at a time", 4, 2 * time.Second},
		{"a person changes some of the members", 2, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// GIVEN: the plugin faded a 4-member group from 77% to 52%, and every member reported 52%
			t.Log("GIVEN: a 4-member group faded to 52%, and every member reported it")
			members := groupMembers[:4]
			f, t0 := groupFade(t, &luxCommand{transition: luxTransition, on: true, pct: 52, kelvin: 5000,
				fromOn: true, fromPct: 77, fromKelvin: 5000}, members, 52, 40*time.Second)
			core, logs := observer.New(zap.InfoLevel)
			f.manager.logger = zap.New(core)
			f.env.MockHA.ClearServiceCalls()

			// WHEN: a person sets members to 80% after the fade, and they report it
			t.Logf("WHEN: a person sets %d members to 80%% after the fade", tt.changed)
			at := 200 * time.Second
			if tt.gap == 0 {
				f.advanceTo(t0, at)
				for _, id := range members[:tt.changed] {
					f.setMember(id, 80, f.clock.Now())
				}
				f.manager.handleCansChange(f.room, f.setGroup(members))
			} else {
				for _, id := range members[:tt.changed] {
					f.memberReport(t0, at, id, 80, members)
					at += tt.gap
				}
			}
			f.advanceTo(t0, at+25*time.Second)

			// THEN: the room is held promptly, and the plugin never overwrote the person's level
			t.Log("THEN: the room is held")
			assert.True(t, f.manager.luxHeld(f.room), "a person's change must hold the room")
			f.manager.luxTick()
			assert.Empty(t, f.lightCalls(), "no command after the hold")
			assert.Contains(t, f.decision(), "held")
			held := logs.FilterMessageSnippet("holding room").All()
			require.Len(t, held, 1, "the hold is logged")
			assert.Equal(t, int64(len(members)), held[0].ContextMap()["members"], "the members decided the hold")
			assert.Equal(t, int64(0), held[0].ContextMap()["stale_members"])
		})
	}
}

func TestLux_GroupMemberReadFailureKeepsGroupLevelCheck(t *testing.T) {
	t.Parallel()
	// GIVEN: the cans list a member that Home Assistant cannot read
	members := groupMembers[:4]
	f, t0 := groupFade(t, &luxCommand{transition: luxTransition, on: true, pct: 52, kelvin: 5000,
		fromOn: true, fromPct: 77, fromKelvin: 5000}, members, 52, 40*time.Second)
	g := f.setGroup(append([]string{"light.test_missing_bulb"}, members...))

	// WHEN: a fresh group report after the fade is off the path and steady
	f.advanceTo(t0, 120*time.Second)
	g.Attributes["brightness"] = pct255(80)
	g.LastUpdated = f.clock.Now()
	f.env.MockHA.SetMockState(luxTestCans, g)
	f.manager.handleCansChange(f.room, g)
	f.advanceTo(t0, 126*time.Second)

	// THEN: the group-level check decides, as for a single light: the room is held
	assert.True(t, f.manager.luxHeld(f.room), "without every member's state, the group's fresh report decides")
}
