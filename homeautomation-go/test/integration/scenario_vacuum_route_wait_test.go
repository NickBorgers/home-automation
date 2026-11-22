package integration

import (
	"testing"
	"time"

	"homeautomation/pkg/occupancy"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Vacuum Route Blocked Wait Scenario Tests
//
// User story:
//   "When someone crosses the robot's way for a few seconds, the robot should wait for them, then carry on.
//    It should not go home and start again from the dock, so we do not hear start, stop, start all evening."
//
// Same example house as scenario_vacuum_test.go. The dock is in the lounge.
//
// INVARIANTS:
//   - A run whose route is blocked by an occupied area, with the way home blocked too, pauses the robot.
//   - If the area clears within route_blocked_wait (default 5m), the robot resumes and the run is not aborted.
//   - If it stays blocked longer, the run aborts as before: home once the way is clear, rooms stay dirty.
// ============================================================================

const vacuumBasicControl = "/api/v2/robot/capabilities/BasicControlCapability"

// basicActions lists the basic control actions (pause, start, home) the robot received, in order.
func (v *vacuumScenario) basicActions() []string {
	var out []string
	for _, c := range v.robot.Commands() {
		if c.Path == vacuumBasicControl {
			out = append(out, c.Body["action"].(string))
		}
	}
	return out
}

// clearOccupancy publishes every area as empty.
func (v *vacuumScenario) clearOccupancy() {
	areas := map[string]occupancy.Status{}
	for _, a := range v.hub.Areas() {
		areas[a] = occupancy.Status{}
	}
	v.hub.Publish(areas)
}

// GIVEN a run of the study and then the kitchen, the robot cleaning the study
// WHEN a person crosses the kitchen and leaves again within seconds
// THEN the robot pauses, then resumes by itself; the run is not aborted and still completes both rooms
func TestScenario_Vacuum_BriefBlockResumesRun(t *testing.T) {
	v := setupVacuumScenario(t, nil, nil)
	v.markDirty("study", "kitchen")
	v.startRun()
	require.Equal(t, []string{"study", "kitchen"}, v.out().Run.Targets)
	v.step("study", 97)
	v.robot.ClearCommands()

	t.Log("WHEN: a person walks into the kitchen, so the way home is blocked")
	v.walkIntoKitchen()
	v.tick()
	require.Equal(t, []string{"pause"}, v.basicActions())
	v.robot.SetStatus("paused", "resumable")
	assert.True(t, v.out().Run.Paused)

	t.Log("AND: they leave 3 seconds later; the next poll sees the way clear")
	v.clearOccupancy()
	v.advance(3 * time.Second)

	t.Log("THEN: the robot resumes cleaning; it does not go home and the run is not aborted")
	assert.Equal(t, []string{"pause", "start"}, v.basicActions())
	assert.False(t, v.out().Run.Paused)
	assert.False(t, v.out().Run.Aborted)
	v.robot.SetStatus("cleaning", "segment")

	t.Log("AND: the run carries on and completes both rooms")
	v.step("study", 93)
	v.step("lounge", 92)
	v.step("kitchen", 91)
	v.step("kitchen", 88)
	v.step("kitchen", 85)
	v.robot.SetStatus("returning", "none")
	v.step("kitchen", 84)
	v.robot.SetStatus("docked", "none")
	v.step("lounge", 83)
	v.step("lounge", 83)
	waitForServerState(t, v.server, "input_boolean.vacuum_dirty_kitchen", "off")
	waitForServerState(t, v.server, "input_boolean.vacuum_dirty_study", "off")
	assert.Equal(t, "completed", v.out().LastRun.Outcome)
	assert.NotContains(t, v.basicActions(), "home", "never sent home mid-run")
}

// GIVEN a run of the study and then the bedroom, where the bedroom is quiet for the kitchen
// WHEN a person crosses the kitchen while the robot cleans the study, and leaves again within seconds
// THEN the bedroom being within earshot counts as a block by an occupied area: the robot waits, not aborts,
// and resumes once the kitchen is clear
func TestScenario_Vacuum_BriefQuietForBlockResumesRun(t *testing.T) {
	rooms := `    rooms:
      lounge:  {segment_id: "1", full: 24h}
      kitchen: {segment_id: "2", full: 24h}
      study:   {segment_id: "3", full: 24h}
      hall:    {segment_id: "4", full: 24h}
      bedroom: {segment_id: "5", full: 24h, quiet_for: [kitchen]}
      porch:   {segment_id: "6", full: 24h}
`
	v := setupVacuumScenarioWith(t, nil, nil, rooms, nil)
	v.markDirty("study", "bedroom")
	v.startRun()
	require.Equal(t, []string{"study", "bedroom"}, v.out().Run.Targets)
	v.step("study", 97)
	v.robot.ClearCommands()

	t.Log("WHEN: a person walks into the kitchen: the bedroom must not be cleaned, and the way home is blocked")
	v.walkIntoKitchen()
	v.tick()
	require.Equal(t, []string{"pause"}, v.basicActions())
	v.robot.SetStatus("paused", "resumable")
	assert.Contains(t, v.out().Run.AbortReason, "within earshot of area kitchen")
	assert.True(t, v.out().Run.Paused)
	assert.False(t, v.out().Run.Aborted, "an earshot block comes from an occupied area: wait, do not abort")

	t.Log("AND: they leave 3 seconds later")
	v.clearOccupancy()
	v.advance(3 * time.Second)

	t.Log("THEN: the robot resumes the run")
	assert.Equal(t, []string{"pause", "start"}, v.basicActions())
	assert.False(t, v.out().Run.Paused)
	assert.False(t, v.out().Run.Aborted)
}

// GIVEN a run of the study and then the kitchen, paused because a person stands in the kitchen
// WHEN they stay longer than route_blocked_wait (5m), and then leave
// THEN the run aborts as before: the robot goes home once the way is clear, and the rooms stay dirty
func TestScenario_Vacuum_LongBlockStillAborts(t *testing.T) {
	v := setupVacuumScenario(t, nil, nil)
	v.markDirty("study", "kitchen")
	v.startRun()
	v.step("study", 97)
	v.robot.ClearCommands()

	t.Log("GIVEN: a person walks into the kitchen and the robot pauses")
	v.walkIntoKitchen()
	v.tick()
	require.Equal(t, []string{"pause"}, v.basicActions())
	v.robot.SetStatus("paused", "resumable")

	t.Log("WHEN: they stay for 4 minutes: the robot keeps waiting")
	v.advance(4 * time.Minute)
	assert.Equal(t, []string{"pause"}, v.basicActions())
	assert.False(t, v.out().Run.Aborted)

	t.Log("AND: past 5 minutes the run gives up, and then the person leaves")
	v.advance(2 * time.Minute)
	assert.True(t, v.out().Run.Aborted)
	assert.Equal(t, []string{"pause"}, v.basicActions(), "still blocked: no way home yet")
	v.clearOccupancy()
	v.advance(3 * time.Second)

	t.Log("THEN: the robot goes home, and the run ends aborted with its rooms dirty")
	assert.Equal(t, []string{"pause", "home"}, v.basicActions())
	v.robot.SetStatus("docked", "none")
	v.step("lounge", 90)
	v.step("lounge", 90)
	assert.Equal(t, "aborted", v.out().LastRun.Outcome)
	assert.Equal(t, "on", v.server.GetState("input_boolean.vacuum_dirty_kitchen").State)
}
