package integration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Vacuum Quiet For Scenario Tests
//
// User story:
//   "When we sit in one room, do not clean the room next door: it is behind a
//    door, but the robot is still too loud there. Driving through it quietly
//    is fine, so the rooms beyond still get cleaned."
//
// Same example house as scenario_vacuum_test.go. The porch is behind a door
// from the hall, and the bedroom is behind another door from the hall. The
// hall is configured with quiet_for: [porch].
//
// INVARIANTS:
//   - While a quiet_for area is occupied or unknown, the room is not cleaned.
//   - The room may still be driven through, so rooms beyond it are cleaned.
//   - When the area is clear again, the room is cleaned.
// ============================================================================

const quietForRooms = `    rooms:
      lounge:  {segment_id: "1", full: 24h}
      kitchen: {segment_id: "2", full: 24h}
      study:   {segment_id: "3", full: 24h}
      hall:    {segment_id: "4", full: 24h, quiet_for: [porch]}
      bedroom: {segment_id: "5", full: 24h}
      porch:   {segment_id: "6", full: 24h}
`

// GIVEN the hall and the bedroom are dirty, and the hall is quiet for the porch
// WHEN someone sits on the porch
// THEN the hall is not cleaned, but the robot drives through it to clean the bedroom
// AND WHEN the person leaves the porch, the hall is cleaned
func TestScenario_Vacuum_QuietFor_RoomNextToOccupiedAreaSkippedButDrivenThrough(t *testing.T) {
	v := setupVacuumScenarioWith(t, nil, nil, quietForRooms, nil)

	t.Log("GIVEN: the hall and the bedroom are dirty")
	v.markDirty("hall", "bedroom")

	t.Log("WHEN: a person walks through the hall and sits on the porch")
	v.walkIntoHall()
	v.walkFromHallOntoPorch()
	require.Eventually(t, func() bool {
		occ, known := v.hub.Occupied("porch")
		return occ && known
	}, 5*time.Second, 10*time.Millisecond, "room presence publishes the porch as occupied")
	v.startRun()

	t.Log("THEN: only the bedroom is cleaned; the hall is skipped because it is within earshot of the porch")
	assert.Equal(t, []string{"bedroom"}, v.out().Run.Targets)
	assert.Equal(t, []string{"[5]"}, v.segmentCleans())
	assert.Equal(t, "within earshot of area porch, which is occupied or unknown", v.skipReason("hall"))
	assert.NotContains(t, v.out().Plan.NoEnter, "hall", "the hall may be driven through")
	assert.Contains(t, v.out().Plan.NoClean, "hall")

	t.Log("AND: driving through the hall does not stop the run")
	v.step("kitchen", 95)
	v.step("hall", 94)
	v.step("bedroom", 93)
	v.step("bedroom", 92)
	v.robot.SetStatus("returning", "none")
	v.step("hall", 91)
	v.step("kitchen", 90)
	v.robot.SetStatus("docked", "none")
	v.step("lounge", 90)
	v.step("lounge", 90)
	require.NotNil(t, v.out().LastRun)
	assert.Equal(t, "completed", v.out().LastRun.Outcome)
	assert.False(t, v.out().LastRun.Aborted, "the hall may be driven through")
	waitForServerState(t, v.server, "input_boolean.vacuum_dirty_bedroom", "off")
	assert.Equal(t, "on", v.server.GetState("input_boolean.vacuum_dirty_hall").State, "the hall stays dirty")
	for _, c := range v.robot.Commands() {
		if c.Path == "/api/v2/robot/capabilities/BasicControlCapability" {
			assert.NotEqual(t, "pause", c.Body["action"], "no pause")
			assert.NotEqual(t, "home", c.Body["action"], "no early return")
		}
	}

	t.Log("WHEN: the person leaves the porch for the yard")
	v.server.SetState("binary_sensor.example_yard_person", "on", nil)
	require.Eventually(t, func() bool {
		occ, known := v.hub.Occupied("porch")
		return !occ && known
	}, 5*time.Second, 10*time.Millisecond, "room presence publishes the porch as clear")
	v.robot.ClearCommands()
	v.robot.SetBattery(95)
	v.startRun()

	t.Log("THEN: the hall is cleaned")
	assert.Equal(t, []string{"hall"}, v.out().Run.Targets)
	assert.Equal(t, []string{"[4]"}, v.segmentCleans())
}
