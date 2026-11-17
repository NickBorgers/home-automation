package integration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// INVARIANTS:
//   - A failed target stays dirty.
//   - A failed first target moves behind the other eligible targets.
//   - A lone failed target waits before another run is launched.

// GIVEN a run for the study and kitchen fails before reaching the study
// WHEN the robot returns to its dock
// THEN the next plan tries the kitchen before retrying the still-dirty study
func TestScenario_Vacuum_UnreachableFirstTargetRotatesBehindOtherTargets(t *testing.T) {
	v := setupVacuumScenario(t, nil, nil)
	v.markDirty("kitchen", "study")
	v.startRun()
	require.Equal(t, []string{"study", "kitchen"}, v.out().Run.Targets)

	t.Log("GIVEN: the robot cannot reach the first target")
	v.robot.SetStatus("error", "none")
	for i := 0; i <= 60; i++ {
		v.advance(30 * time.Second)
	}
	require.Nil(t, v.out().Run)
	assert.Equal(t, "on", v.server.GetState("input_boolean.vacuum_dirty_study").State)

	t.Log("WHEN: the robot is ready at its dock again")
	v.robot.SetStatus("docked", "none")
	v.moveTo("lounge", 100)
	v.tick()

	t.Log("THEN: the other eligible target is first and the failed target remains last")
	require.NotNil(t, v.out().Plan)
	assert.Equal(t, []string{"kitchen", "study"}, v.out().Plan.Rooms)
}

// GIVEN a run for one dirty room fails before reaching it
// WHEN the robot returns to its dock and the scheduler replans
// THEN the room stays dirty but no identical run is immediately pending
func TestScenario_Vacuum_LoneUnreachableTargetWaitsBeforeRetry(t *testing.T) {
	v := setupVacuumScenario(t, nil, nil)
	v.markDirty("kitchen")
	v.startRun()

	t.Log("GIVEN: the robot cannot reach the only target")
	v.robot.SetStatus("error", "none")
	for i := 0; i <= 60; i++ {
		v.advance(30 * time.Second)
	}
	require.Nil(t, v.out().Run)

	t.Log("WHEN: the robot is ready at its dock again")
	v.robot.SetStatus("docked", "none")
	v.moveTo("lounge", 100)
	v.tick()

	t.Log("THEN: the target remains dirty and waits for its retry cooldown")
	assert.Equal(t, "on", v.server.GetState("input_boolean.vacuum_dirty_kitchen").State)
	require.NotNil(t, v.out().Plan)
	assert.Empty(t, v.out().Plan.Rooms)
	assert.Contains(t, v.out().Plan.Reason, "retry")

	t.Log("AND: the target becomes eligible again when the retry cooldown expires")
	v.advance(30 * time.Minute)
	require.NotNil(t, v.out().Plan)
	assert.Equal(t, []string{"kitchen"}, v.out().Plan.Rooms)
}
