package integration

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Vacuum Fencing and Stranded Robot Scenario Tests
//
// User story:
//   "The robot never drives through a room a person is in. When it plans its
//    own way around something, it must still stay out of those rooms. If it
//    ends up somewhere it must not be, it stops there, and it goes home as
//    soon as its way home is clear. It is never left standing in the house."
//
// What happened on 2026-11-15: the scheduler started a vacuum-only run for
// one room. People were in another part of the house, which the plan kept
// the robot out of. The robot's own path planner drove into that part
// anyway, maybe around a dog lying in a doorway. The scheduler paused it,
// the robot then reported idle (not paused) with the flag resumable, and
// the run would have ended as stalled after 30 minutes with the robot
// stranded and nothing ever sending it home.
//
// Same example house as scenario_vacuum_test.go. The dock is in the lounge.
// The way from the bedroom home is bedroom, hall, kitchen, lounge.
//
// INVARIANTS:
//   - Before the run starts, the doors from reachable rooms into no-enter
//     rooms get Valetudo virtual walls. The owner's own restrictions stay.
//   - A robot found in a no-enter room makes the scheduler react at once.
//   - A robot the scheduler paused is waiting for its way home, whether it
//     reports paused or idle. That run never ends by stall.
//   - It is sent home as soon as the way home is clear.
//   - When the run ends, the owner's restrictions are back exactly.
// ============================================================================

const vacuumRestrictionsPath = "/api/v2/robot/capabilities/CombinedVirtualRestrictionsCapability"

// vacuumOwnerZone is the owner's own restriction: one no-go zone, no walls.
const vacuumOwnerZone = `{"virtualWalls":[],"restrictedZones":[{"type":"regular","points":{"pA":{"x":3005,"y":4150},` +
	`"pB":{"x":3085,"y":4150},"pC":{"x":3085,"y":4205},"pD":{"x":3005,"y":4205}}}]}`

func (v *vacuumScenario) basicCommands() []string {
	var out []string
	for _, c := range v.robot.Commands() {
		if c.Path == "/api/v2/robot/capabilities/BasicControlCapability" {
			out = append(out, fmt.Sprint(c.Body["action"]))
		}
	}
	return out
}

// walkFromHallOntoPorch sends the Frigate events of the person in the hall walking out onto the porch.
func (v *vacuumScenario) walkFromHallOntoPorch() {
	publishFrigate(v.t, v.server, "update", "p", "cam_hall", []string{"hall_to_kitchen", "hall", "hall_to_porch"},
		"hall_to_porch")
	publishFrigate(v.t, v.server, "end", "p", "cam_hall", []string{"hall_to_kitchen", "hall", "hall_to_porch"},
		"hall_to_porch")
	require.Eventually(v.t, func() bool {
		occ, known := v.hub.Occupied("hall")
		return !occ && known
	}, 5*time.Second, 10*time.Millisecond, "room presence publishes the hall as clear")
}

// GIVEN the study is dirty, master is asleep in the bedroom, and a person is in the hall
// WHEN the run starts, the robot's own path planner takes it into the bedroom, the scheduler pauses it, and
// the robot then reports idle with the flag resumable for 45 minutes
// THEN the kitchen-hall door was walled before the run, the run is still ours after 45 minutes, and the
// robot is sent home the moment the person leaves the hall; when it docks, the owner's zone is all that is left
func TestScenario_Vacuum_OffRouteIntoNoEnterRoomPausesThenGoesHome(t *testing.T) {
	v := setupVacuumScenario(t, map[string]time.Duration{"study": 30 * time.Hour}, nil)
	v.server.SetState("input_text.vacuum_fence_saved", "", map[string]interface{}{"max": float64(255)})
	v.waitClient("input_text.vacuum_fence_saved", "")
	v.robot.SetRestrictions(vacuumOwnerZone)

	t.Log("GIVEN: master is asleep and a person is in the hall")
	v.server.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	waitForBoolState(t, v.sm, "isMasterAsleep", true)
	v.walkIntoHall()

	t.Log("WHEN: the run for the study starts")
	v.startRun()
	assert.Equal(t, []string{"study"}, v.out().Run.Targets)
	assert.Equal(t, []string{"[3]"}, v.segmentCleans())

	t.Log("THEN: the kitchen-hall door was walled first, and the owner's zone stayed")
	var puts []string
	for _, c := range v.robot.Commands() {
		puts = append(puts, c.Path)
	}
	require.GreaterOrEqual(t, len(puts), 2)
	assert.Equal(t, vacuumRestrictionsPath, puts[0], "the walls go up before anything else")
	// kitchen_hall spans [7,5]-[8,5]; 0.25 m past each jamb is [6.75,5]-[8.25,5], or x 675..825 cm, y 1500 cm.
	assert.JSONEq(t, `{"virtualWalls":[{"points":{"pA":{"x":675,"y":1500},"pB":{"x":825,"y":1500}}}],`+
		`"restrictedZones":[{"type":"regular","points":{"pA":{"x":3005,"y":4150},"pB":{"x":3085,"y":4150},`+
		`"pC":{"x":3085,"y":4205},"pD":{"x":3005,"y":4205}}}]}`, v.robot.Restrictions())
	assert.Equal(t, `{"w":[],"z":[["r",3005,4150,3085,4205]]}`,
		v.server.GetState("input_text.vacuum_fence_saved").State, "the owner's set is saved before the walls go up")
	assert.Equal(t, []string{"kitchen_hall"}, v.out().Run.FencedDoors)

	t.Log("WHEN: the robot leaves the dock and its own path planner takes it into the bedroom")
	v.step("lounge", 99)
	v.step("bedroom", 98)

	t.Log("THEN: it is paused at once: its way home runs through the hall, where the person is")
	assert.Equal(t, []string{"pause"}, v.basicCommands())
	run := v.out().Run
	require.NotNil(t, run)
	assert.True(t, run.Paused)
	assert.Contains(t, run.AbortReason, "robot is in bedroom, which it must not enter")

	t.Log("WHEN: the robot reports idle with the flag resumable, as a Dreame does after a pause, for 45 minutes")
	v.robot.SetStatus("idle", "resumable")
	for i := 0; i < 90; i++ {
		v.step("bedroom", 98)
	}

	t.Log("THEN: the run is still ours, still paused, and nothing else was sent")
	run = v.out().Run
	require.NotNil(t, run, "a robot waiting for its way home is not stalled")
	assert.True(t, run.Paused)
	assert.Nil(t, v.out().LastRun, "no run ended")
	assert.Equal(t, []string{"pause"}, v.basicCommands())

	t.Log("WHEN: the person walks out of the hall onto the porch")
	v.walkFromHallOntoPorch()
	v.tick()

	t.Log("THEN: the robot is sent home at once")
	assert.Equal(t, []string{"pause", "home"}, v.basicCommands())
	assert.False(t, v.out().Run.Paused)

	t.Log("WHEN: it drives home and docks")
	v.robot.SetStatus("returning", "none")
	v.step("hall", 97)
	v.step("kitchen", 96)
	v.robot.SetStatus("docked", "none")
	v.step("lounge", 96)
	v.step("lounge", 96)

	t.Log("THEN: the run ended as aborted, the study stays dirty, and only the owner's zone is left")
	last := v.out().LastRun
	require.NotNil(t, last)
	assert.Equal(t, "aborted", last.Outcome)
	assert.Equal(t, "on", v.server.GetState("input_boolean.vacuum_dirty_study").State)
	var got, want map[string]any
	require.NoError(t, json.Unmarshal([]byte(v.robot.Restrictions()), &got))
	require.NoError(t, json.Unmarshal([]byte(vacuumOwnerZone), &want))
	assert.Equal(t, want, got)
	waitForServerState(t, v.server, "input_text.vacuum_fence_saved", "")
}

// GIVEN no run is tracked and the robot stands idle in the bedroom, away from the dock, with the flag
// resumable, as it was left after tonight's pause
// WHEN ten minutes pass while a person is in the hall, on its way home, and then the person leaves the hall
// THEN it stays put while the way is blocked and is sent home as soon as the way is clear
func TestScenario_Vacuum_StrandedRobotIsSentHome(t *testing.T) {
	v := setupVacuumScenario(t, nil, nil)
	v.walkIntoHall()
	v.tick()

	t.Log("GIVEN: the robot stands idle in the bedroom with no run")
	v.moveTo("bedroom", 60)
	v.robot.SetStatus("idle", "resumable")
	v.tick()
	require.Nil(t, v.out().Run)

	t.Log("WHEN: ten minutes pass with a person in the hall")
	for i := 0; i < 20; i++ {
		v.advance(30 * time.Second)
	}

	t.Log("THEN: it stays put")
	assert.Empty(t, v.basicCommands())

	t.Log("WHEN: the person walks out onto the porch")
	v.walkFromHallOntoPorch()
	v.tick()

	t.Log("THEN: it is sent home")
	assert.Equal(t, []string{"home"}, v.basicCommands())
}
