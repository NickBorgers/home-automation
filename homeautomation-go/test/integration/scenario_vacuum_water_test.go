package integration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Vacuum Out-Of-Water Scenario Tests
//
// User story:
//   "When we are away for a weekend, the mop dock's clean water runs out on
//    the first day. The robot should keep vacuuming until someone refills
//    the tank, instead of sitting in error until we get home."
//
// Same example house as scenario_vacuum_test.go. The dock is in the lounge.
//
// INVARIANTS:
//   - When the error sensor reports the clean water tank empty, the run ends
//     at once. It does not wait for stall_timeout.
//   - input_boolean.vacuum_water_empty turns on and stays on until someone
//     comes home, or someone turns it off by hand.
//   - While it is on, every run is vacuum only, even while the robot still
//     reports the water error.
//   - A room due for a full clean gets one vacuum pass per full interval. It
//     stays dirty for its full clean, and its last full clean time does not
//     change.
// ============================================================================

const (
	waterHelper     = "input_boolean.vacuum_water_empty"
	vacuumErrSensor = "sensor.valetudo_mellowslimyloris_error"
	waterEmptyText  = "Mop Dock Clean Water Tank empty"
)

// setWaterError sets the robot's error sensor and waits until the app sees it.
func (v *vacuumScenario) setWaterError(text string) {
	v.server.SetState(vacuumErrSensor, text, map[string]interface{}{})
	v.waitClient(vacuumErrSensor, text)
}

// finishRunAt lets the robot clean the given rooms in order and dock for good.
func (v *vacuumScenario) finishRunAt(rooms ...string) {
	battery := 95
	for _, room := range rooms {
		for i := 0; i < 4; i++ {
			v.step(room, battery)
			battery--
		}
	}
	v.robot.SetStatus("docked", "none")
	v.step("lounge", battery)
	v.step("lounge", battery)
	require.Nil(v.t, v.out().Run, "the run ended")
}

// GIVEN nobody is home and the study and the porch are dirty
// WHEN the clean water runs out during a full run
// THEN the run ends at once, the next runs vacuum only with one pass per room per full interval, and the next
// full run waits until someone comes home
func TestScenario_Vacuum_OutOfWaterVacuumsOnly(t *testing.T) {
	v := setupVacuumScenario(t, nil, nil)
	require.NoError(t, v.sm.SetBool("isAnyoneHome", false))
	studyCleaned := v.server.GetState("input_datetime.vacuum_cleaned_study").Attributes["timestamp"]

	t.Log("GIVEN: nobody is home, and the study and the porch are dirty")
	v.markDirty("study", "porch")
	v.startRun()
	require.Equal(t, "full", v.out().Run.Treatment)

	t.Log("WHEN: the robot cleans the study, docks to wash its mop, and the clean water tank is empty")
	v.step("study", 97)
	v.step("study", 95)
	v.robot.SetStatus("error", "none")
	v.setWaterError(waterEmptyText)
	v.moveTo("lounge", 95)
	v.advance(30 * time.Second)

	t.Log("THEN: the run ends at once, without waiting 30 minutes")
	require.Nil(t, v.out().Run, "the run ended")
	assert.Equal(t, "no_water", v.out().LastRun.Outcome)
	assert.Empty(t, v.out().LastRun.Done, "the study was not finished")
	waitForServerState(t, v.server, waterHelper, "on")

	t.Log("AND: the next run vacuums the study and the porch, while the robot still reports the error")
	v.startRun()
	assert.Equal(t, "vacuum", v.out().Run.Treatment)
	assert.ElementsMatch(t, []string{"study", "porch"}, v.out().Run.Targets)
	assert.Equal(t, "vacuum", v.robot.Presets()["operation_mode"])

	v.finishRunAt("study", "hall", "porch")
	assert.Equal(t, "completed", v.out().LastRun.Outcome)
	assert.ElementsMatch(t, []string{"study", "porch"}, v.out().LastRun.Done)

	t.Log("AND: both rooms still need their full clean, and their last full clean time is unchanged")
	assert.Equal(t, "on", v.server.GetState("input_boolean.vacuum_dirty_study").State)
	assert.Equal(t, "on", v.server.GetState("input_boolean.vacuum_dirty_porch").State)
	assert.Equal(t, studyCleaned, v.server.GetState("input_datetime.vacuum_cleaned_study").Attributes["timestamp"])

	t.Log("AND: no new run starts for those rooms within their full interval (24 hours)")
	v.advance(12 * time.Hour)
	v.tick()
	assert.Nil(t, v.out().Run)
	assert.Empty(t, v.out().Plan.Rooms, "plan: %+v", v.out().Plan)
	assert.Contains(t, v.skipReason("study"), "vacuumed while out of water")

	t.Log("AND: after 24 hours they get another vacuum pass")
	v.advance(12 * time.Hour)
	v.robot.SetBattery(100)
	v.startRun()
	assert.Equal(t, "vacuum", v.out().Run.Treatment)
	v.finishRunAt("study", "hall", "porch")

	t.Log("WHEN: someone comes home (and refills the tank)")
	require.NoError(t, v.sm.SetBool("isAnyoneHome", true))
	v.setWaterError("No error")
	v.robot.SetBattery(100)

	t.Log("THEN: the helper turns off, and the next run is a full clean")
	v.tick()
	waitForServerState(t, v.server, waterHelper, "off")
	v.startRun()
	assert.Equal(t, "full", v.out().Run.Treatment, "every room is due by now; the run mops again")
}

// GIVEN the clean water ran out and the helper is on
// WHEN someone turns the helper off by hand
// THEN the next run is a full clean
func TestScenario_Vacuum_OutOfWaterClearedByHand(t *testing.T) {
	v := setupVacuumScenario(t, nil, nil)
	v.markDirty("study")
	v.robot.SetStatus("error", "none")
	v.setWaterError(waterEmptyText)
	v.tick()
	waitForServerState(t, v.server, waterHelper, "on")
	v.waitClient(waterHelper, "on")

	v.setWaterError("No error")
	v.robot.SetStatus("docked", "none")
	v.server.SetState(waterHelper, "off", map[string]interface{}{})
	v.waitClient(waterHelper, "off")

	v.startRun()
	assert.Equal(t, "full", v.out().Run.Treatment)
}
