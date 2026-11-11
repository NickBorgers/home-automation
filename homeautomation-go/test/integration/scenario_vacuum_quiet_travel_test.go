package integration

import (
	"testing"

	"homeautomation/internal/valetudo/valetudotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Vacuum Quiet Travel Scenario Tests
//
// User story:
//   "The robot is loud when it drives between rooms at full cleaning power.
//    Keep the fan on the lowest level while it travels, and turn it up only
//    while it is in the room it is cleaning."
//
// Same example house as scenario_vacuum_test.go. The dock is in the lounge;
// the tour study -> kitchen drives through the lounge.
//
// INVARIANTS:
//   - A run the scheduler starts begins at the travel fan speed.
//   - The fan is at the treatment's level only while the robot is in the room
//     it is cleaning now. Travel, rooms driven through and the trip home are at
//     the travel fan speed.
//   - A fan command is sent only when the wanted level changes.
//   - When the live-safety rules send the robot home, it drives home quietly.
// ============================================================================

const quietTravelConfig = `    travel_fan_speed: low
    treatments:
      full: {operation_mode: vacuum_and_mop, fan_speed: high, water_grade: medium}
`

func setupQuietTravelScenario(t *testing.T) *vacuumScenario {
	t.Helper()
	return setupVacuumScenarioWith(t, nil, nil, quietTravelConfig, func(robot *valetudotest.Robot) {
		robot.SetPresetList("FanSpeedControlCapability", []string{"low", "medium", "high", "max"})
	})
}

// fanCommands lists the fan presets the robot was sent, in order.
func (v *vacuumScenario) fanCommands() []string {
	out := []string{}
	for _, c := range v.robot.Commands() {
		if c.Path == "/api/v2/robot/capabilities/FanSpeedControlCapability/preset" {
			name, _ := c.Body["name"].(string)
			out = append(out, name)
		}
	}
	return out
}

// GIVEN the study and the kitchen are dirty and quiet travel is set to "low"
// WHEN the robot cleans the study, drives through the lounge, cleans the kitchen and goes home
// THEN the fan is high only in the study and the kitchen, and low everywhere else
func TestScenario_Vacuum_QuietTravel_FanHighOnlyInTheRoomBeingCleaned(t *testing.T) {
	v := setupQuietTravelScenario(t)

	t.Log("GIVEN: the study and the kitchen are dirty")
	v.markDirty("study", "kitchen")

	t.Log("WHEN: the run starts")
	v.startRun()
	require.Equal(t, []string{"study", "kitchen"}, v.out().Run.Targets)

	t.Log("THEN: the run starts at the travel fan speed, not the treatment's")
	assert.Equal(t, []string{"low"}, v.fanCommands())
	assert.True(t, v.out().Run.QuietTravel)
	assert.Equal(t, "low", v.out().Run.FanSpeed)

	t.Log("WHEN: the robot drives out of the dock room")
	v.step("lounge", 99)
	assert.Equal(t, []string{"low"}, v.fanCommands(), "still travelling")

	t.Log("WHEN: the robot reaches the study, its first room")
	v.step("study", 97)
	assert.Equal(t, []string{"low", "high"}, v.fanCommands(), "the fan goes up in the room being cleaned")
	assert.Equal(t, "high", v.out().Run.FanSpeed)
	v.step("study", 94)
	v.step("study", 93)
	assert.Equal(t, []string{"low", "high"}, v.fanCommands(), "no repeated fan writes")

	t.Log("WHEN: the robot drives through the lounge to the kitchen")
	v.step("lounge", 92)
	assert.Equal(t, []string{"low", "high", "low"}, v.fanCommands(), "the fan goes down for the drive")
	v.step("kitchen", 91)
	assert.Equal(t, []string{"low", "high", "low", "high"}, v.fanCommands(), "and up in the kitchen")

	t.Log("WHEN: the robot heads home")
	v.step("kitchen", 85)
	v.robot.SetStatus("returning", "none")
	v.step("kitchen", 84)
	v.step("lounge", 84)
	v.robot.SetStatus("docked", "none")
	v.step("lounge", 83)
	v.step("lounge", 83)

	t.Log("THEN: the trip home is quiet, and nothing else is restored after the run")
	assert.Equal(t, []string{"low", "high", "low", "high", "low"}, v.fanCommands())
	require.NotNil(t, v.out().LastRun)
	assert.Equal(t, "completed", v.out().LastRun.Outcome)
	assert.Equal(t, "low", v.out().LastRun.FanSpeed)
}

// GIVEN a quiet-travel run of the study and then the kitchen, with the fan high in the study
// WHEN someone walks into the kitchen
// THEN the robot is sent home and drives home at the travel fan speed
func TestScenario_Vacuum_QuietTravel_SentHomeQuietly(t *testing.T) {
	v := setupQuietTravelScenario(t)
	v.markDirty("study", "kitchen")
	v.startRun()
	v.step("study", 97)
	require.Equal(t, []string{"low", "high"}, v.fanCommands())
	v.robot.ClearCommands()

	t.Log("WHEN: a person walks into the kitchen")
	v.walkIntoKitchen()
	v.tick()

	t.Log("THEN: the robot is sent home and the fan goes down for the trip")
	paths := []string{}
	for _, c := range v.robot.Commands() {
		paths = append(paths, c.Path)
	}
	assert.Equal(t, []string{
		"/api/v2/robot/capabilities/BasicControlCapability",
		"/api/v2/robot/capabilities/FanSpeedControlCapability/preset",
	}, paths)
	assert.Equal(t, []string{"low"}, v.fanCommands())

	t.Log("AND: it stays low all the way home, even through the study it was cleaning")
	v.step("study", 96)
	v.step("lounge", 95)
	v.robot.SetStatus("docked", "none")
	v.step("lounge", 95)
	v.step("lounge", 95)
	assert.Equal(t, "aborted", v.out().LastRun.Outcome)
	assert.Equal(t, []string{"low"}, v.fanCommands())
}
