package integration

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Vacuum Stalled Run Scenario Tests
//
// User story:
//   "When the robot stops with an error in the middle of a job, and a
//    different job starts hours later, treat them as two runs. Do not mark the
//    room it was stuck in as clean, do not let the second job be judged by the
//    first job's operation mode, and do not learn a trip cost from the hours
//    it sat on the dock."
//
// Same example house as scenario_vacuum_test.go. The dock is in the lounge.
//
// INVARIANTS:
//   - A run that makes no progress for stall_timeout (30m) ends.
//   - The room the robot was in when it stalled stays unfinished.
//   - The next job is a new run, and its treatment comes from the operation
//     mode at that time.
//   - A stalled run teaches no trip cost.
// ============================================================================

// GIVEN a Valetudo timer starts a vacuum-only job
// WHEN the robot cleans the study and the kitchen, enters the hall, docks and goes into error for four hours,
// and then a timer switches to vacuum_and_mop and cleans the porch
// THEN there are two runs: a vacuum-only run that ends in error with the hall unfinished, and a full run that
// clears the porch; the four hours on the dock teach no trip cost
func TestScenario_Vacuum_ErrorStallThenNewJobIsANewRun(t *testing.T) {
	v := setupVacuumScenario(t, nil, map[string]float64{"porch": 4, "trip": 1.3})
	v.tick()

	t.Log("GIVEN: a timer starts a vacuum-only job (06:00)")
	v.robot.SetPreset("operation_mode", "vacuum")
	v.robot.SetStatus("cleaning", "segment")
	battery := 100
	dwell := func(room string, polls int) {
		for i := 0; i < polls; i++ {
			v.step(room, battery)
			battery--
		}
	}
	dwell("study", 6)
	require.NotNil(t, v.out().Run)
	assert.Equal(t, "vacuum", v.out().Run.Treatment)
	dwell("kitchen", 6)
	dwell("hall", 6)
	require.Equal(t, "hall", v.out().Run.Current, "the robot is cleaning the hall")
	require.Equal(t, []string{"study", "kitchen"}, v.out().Run.Done)

	t.Log("WHEN: the robot heads home, docks and goes into error (dust bag full)")
	v.robot.SetStatus("returning", "none")
	v.step("kitchen", battery)
	v.robot.SetStatus("docked", "none")
	v.moveTo("lounge", battery)
	v.clk.Advance(2 * time.Second)
	v.sched.TickForTest(false)
	v.robot.SetStatus("error", "none")
	for i := 0; i < 60; i++ { // the first error poll starts the clock; 29.5 minutes in error
		v.advance(30 * time.Second)
	}
	require.NotNil(t, v.out().Run, "not yet stalled for 30 minutes")

	v.advance(30 * time.Second)
	t.Log("THEN: after 30 minutes in error the first run ends")
	require.Nil(t, v.out().Run, "the run stalled")
	first := v.out().LastRun
	require.NotNil(t, first)
	assert.Equal(t, "error", first.Outcome)
	assert.Contains(t, first.EndReason, "robot in error for 30m")
	assert.Equal(t, "vacuum", first.Treatment)
	assert.Equal(t, []string{"study", "kitchen"}, first.Done, "the hall, where it stalled, is unfinished")
	assert.Equal(t, "1.3", v.server.GetState("input_number.vacuum_cost_trip").State,
		"a stalled run teaches no trip cost")

	t.Log("AND: the robot stays in error for four hours; nothing is tracked")
	for i := 0; i < 7; i++ {
		v.robot.SetBattery(100)
		v.advance(30 * time.Minute)
	}
	assert.Nil(t, v.out().Run)

	t.Log("WHEN: the office timer switches to vacuum_and_mop and cleans the porch (11:00)")
	v.robot.SetPreset("operation_mode", "vacuum_and_mop")
	v.robot.SetStatus("cleaning", "segment")
	battery = 100
	v.step("lounge", battery)
	second := v.out().Run
	require.NotNil(t, second, "the new job is a new run")
	assert.Equal(t, "full", second.Treatment, "read from the operation mode now")
	assert.Empty(t, second.Done)
	battery--
	dwell("kitchen", 1)
	dwell("hall", 1)
	dwell("porch", 6)
	assert.Equal(t, "porch", v.out().Run.Current)
	v.robot.SetStatus("returning", "none")
	v.step("hall", battery)
	battery--
	v.robot.SetStatus("docked", "none")
	v.step("lounge", battery)
	v.step("lounge", battery)

	t.Log("THEN: the second run completes and clears the porch's full taint")
	last := v.out().LastRun
	require.NotNil(t, last)
	assert.Equal(t, "completed", last.Outcome)
	assert.Equal(t, "full", last.Treatment)
	assert.Equal(t, []string{"porch"}, last.Done)
	ts, ok := v.server.GetState("input_datetime.vacuum_cleaned_porch").Attributes["timestamp"].(float64)
	require.True(t, ok)
	assert.Greater(t, int64(ts), vacuumT0.Add(4*time.Hour).Unix(), "the porch was fully cleaned by the second run")

	t.Log("AND: the trip sample comes from the second run only")
	// Drops: lounge 1, kitchen 1, hall 1 on the way out; porch 6 (five in the porch, one leaving it); the last
	// point is lost on the dock. Total 9, porch 6, trip 3: 1.3 + 0.3 * (3 - 1.3) = 1.81 -> 1.8.
	trip, err := strconv.ParseFloat(v.server.GetState("input_number.vacuum_cost_trip").State, 64)
	require.NoError(t, err)
	assert.InDelta(t, 1.8, trip, 1e-9)
}
