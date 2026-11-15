package integration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Vacuum Next Room Dwell Scenario Tests
//
// User story:
//   "The lounge and the kitchen are one open space. When the robot cleans the
//    lounge near the opening, its position may fall into the kitchen for a
//    moment. That must not end the lounge: it must stay the room being
//    cleaned, at the cleaning fan, until the robot really moves on."
//
// Same example house as scenario_vacuum_test.go. The dock is in the lounge,
// and the lounge and the kitchen are joined by an opening at x = 6 m.
//
// INVARIANTS:
//   - The next room of the tour counts only after the robot has been in it
//     for next_room_dwell (1 minute) without going back to the current room.
//   - Until then, the current room is not finished: it stays dirty and no
//     battery cost sample is learned for it.
//   - With quiet travel on, the fan stays at the treatment's level both in the
//     current room and in the next room of the tour.
// ============================================================================

// moveToPoint puts the robot at a house point, in metres, and sets its battery.
func (v *vacuumScenario) moveToPoint(x, y float64, battery int) {
	px, py := vacuumPixel(x, y)
	v.robot.SetRobotPixel(px, py, true)
	v.robot.SetBattery(battery)
}

// GIVEN the lounge and the kitchen are dirty and quiet travel is set to "low"
// WHEN the robot cleans the lounge, its position falls into the kitchen for two polls near the opening,
// and it goes on cleaning the lounge for 9 minutes before it moves to the kitchen
// THEN the lounge is finished only after the real move, and the fan stays high the whole time
func TestScenario_Vacuum_NextRoomDwell_BlipOverOpenBoundary(t *testing.T) {
	v := setupQuietTravelScenario(t)

	t.Log("GIVEN: the lounge and the kitchen are dirty")
	v.markDirty("lounge", "kitchen")
	v.startRun()
	require.Equal(t, []string{"lounge", "kitchen"}, v.out().Run.Targets)

	t.Log("WHEN: the robot starts on the lounge")
	v.step("lounge", 99)
	require.Equal(t, "lounge", v.out().Run.Current)
	require.Equal(t, []string{"low", "high"}, v.fanCommands())

	t.Log("AND: near the opening, its position falls just inside the kitchen for two polls")
	v.moveToPoint(6.3, 2.5, 99)
	v.advance(30 * time.Second)
	v.moveToPoint(6.2, 3.0, 99)
	v.advance(30 * time.Second)

	t.Log("THEN: the lounge is still the room being cleaned, and the fan stays high")
	assert.Equal(t, "lounge", v.out().Run.Current)
	assert.Empty(t, v.out().Run.Done)
	assert.Equal(t, "on", v.server.GetState("input_boolean.vacuum_dirty_lounge").State)
	assert.Equal(t, []string{"low", "high"}, v.fanCommands())

	t.Log("WHEN: the robot goes on cleaning the lounge for 9 minutes")
	battery := 98
	for i := 0; i < 18; i++ {
		v.moveToPoint(5.5, 1+float64(i%4), battery)
		v.advance(30 * time.Second)
		if i%3 == 0 {
			battery--
		}
	}

	t.Log("THEN: the lounge is still not finished, and it was cleaned at the treatment's fan throughout")
	assert.Equal(t, "lounge", v.out().Run.Current)
	assert.Equal(t, "on", v.server.GetState("input_boolean.vacuum_dirty_lounge").State)
	assert.Equal(t, "0.0", v.server.GetState("input_number.vacuum_cost_lounge").State, "no cost sample yet")
	assert.Equal(t, []string{"low", "high"}, v.fanCommands())

	t.Log("WHEN: the robot moves to the kitchen for good")
	v.step("kitchen", battery)
	v.step("kitchen", battery)
	assert.Equal(t, "on", v.server.GetState("input_boolean.vacuum_dirty_lounge").State,
		"30 seconds in the kitchen do not yet confirm it")
	v.step("kitchen", battery)

	t.Log("THEN: the lounge is finished now, and the kitchen is the room being cleaned")
	waitForServerState(t, v.server, "input_boolean.vacuum_dirty_lounge", "off")
	assert.Equal(t, "kitchen", v.out().Run.Current)
	assert.Equal(t, []string{"lounge"}, v.out().Run.Done)
	assert.NotEqual(t, "0.0", v.server.GetState("input_number.vacuum_cost_lounge").State,
		"the lounge's cost sample is learned from its real drop")
	assert.Equal(t, []string{"low", "high"}, v.fanCommands(), "the fan never went down on the whole tour")

	t.Log("AND: the run ends normally with both rooms done")
	v.robot.SetStatus("returning", "none")
	v.step("lounge", battery)
	v.robot.SetStatus("docked", "none")
	v.step("lounge", battery)
	v.step("lounge", battery)
	waitForServerState(t, v.server, "input_boolean.vacuum_dirty_kitchen", "off")
	require.NotNil(t, v.out().LastRun)
	assert.Equal(t, "completed", v.out().LastRun.Outcome)
	assert.Equal(t, []string{"lounge", "kitchen"}, v.out().LastRun.Done)
}
