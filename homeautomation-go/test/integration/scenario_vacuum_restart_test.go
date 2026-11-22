package integration

import (
	"context"
	"path/filepath"
	"testing"

	"homeautomation/internal/housemap"
	"homeautomation/internal/plugins/vacuum"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/valetudo"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// ============================================================================
// Vacuum Restart Scenario Tests
//
// User story:
//   "The scheduler is the only thing that drives the robot. When the app
//    restarts in the middle of a run, the run goes on. The rooms the robot
//    cleans after the restart must be marked clean, the same as before it."
//
// Same example house as scenario_vacuum_test.go.
//
// INVARIANTS:
//   - A run found in progress is the scheduler's own run. Its targets come
//     from the segments the robot's map marks active for the current job.
//   - The room the robot is cleaning when the run is found counts as the
//     room being cleaned at once, and is marked clean when the run completes.
//   - A battery drop seen only in part is not learned: not for the room the
//     robot was already in, and not for the trip.
// ============================================================================

// restartScheduler stops the scheduler and starts a new one on the same Home Assistant, robot and occupancy
// hub, as a deploy does.
func (v *vacuumScenario) restartScheduler() {
	v.t.Helper()
	v.sched.Stop()
	m, err := housemap.Load(filepath.Join("..", "..", "internal", "housemap", "testdata", "example_house_map.yaml"))
	require.NoError(v.t, err)
	m.Valetudo = &housemap.Valetudo{PixelSizeM: 0.05, AnchorPx: []float64{0, 400}, AnchorM: []float64{0, 0},
		DockM: []float64{1, 1}}
	cfg := loadVacuumSchedulerConfig(v.t, v.robot.URL, "")
	v.shadow = shadowstate.NewVacuumTracker()
	logCore, logs := observer.New(zap.InfoLevel)
	v.logs = logs
	v.sched = vacuum.NewScheduler(context.Background(), v.client, v.sm, cfg, m,
		valetudo.NewClient(v.robot.URL, nil), v.hub, v.shadow, nil, zap.New(logCore), false, v.clk)
	v.sched.SetManualTicksForTest()
	require.NoError(v.t, v.sched.Start())
	v.t.Cleanup(v.sched.Stop)
}

// GIVEN a run of the kitchen and the hall, with the kitchen done and the robot cleaning the hall
// WHEN the app restarts, and the robot cleans the hall for 30 more seconds and docks
// THEN the found run is tracked as the scheduler's own, and the hall is marked clean
func TestScenario_Vacuum_RestartMidRunCreditsRemainingRoom(t *testing.T) {
	v := setupVacuumScenario(t, nil, nil)

	t.Log("GIVEN: the kitchen and the hall are dirty, and the run starts")
	v.markDirty("kitchen", "hall")
	v.startRun()
	require.Equal(t, []string{"kitchen", "hall"}, v.out().Run.Targets)

	t.Log("AND: the robot cleans the kitchen, then moves on to the hall")
	v.step("kitchen", 99)
	v.step("kitchen", 98)
	v.step("hall", 97)
	v.step("hall", 97)
	v.step("hall", 96)
	waitForServerState(t, v.server, "input_boolean.vacuum_dirty_kitchen", "off")
	require.Equal(t, "hall", v.out().Run.Current)
	v.step("hall", 95)

	t.Log("WHEN: the app restarts in the middle of the run")
	v.restartScheduler()
	v.robot.ClearCommands()
	v.tick()

	t.Log("THEN: the run in progress is tracked as the scheduler's own, with the hall as its target")
	run := v.out().Run
	require.NotNil(t, run, "the run in progress is tracked")
	assert.Equal(t, "resumed", run.Origin)
	assert.Equal(t, "full", run.Treatment)
	assert.Equal(t, []string{"hall", "kitchen"}, run.Targets,
		"the job's active segments, from the robot's room on; the kitchen was done before the restart")
	assert.Equal(t, "hall", run.Current, "the room the robot is cleaning counts at once")

	t.Log("WHEN: the robot cleans the hall for 30 more seconds, then goes home and docks")
	v.step("hall", 94)
	v.robot.SetStatus("returning", "resumable")
	v.step("kitchen", 94)
	v.step("lounge", 94)
	v.robot.SetStatus("docked", "none")
	v.step("lounge", 94)
	v.step("lounge", 94)

	t.Log("THEN: the run completed and the hall is marked clean")
	waitForServerState(t, v.server, "input_boolean.vacuum_dirty_hall", "off")
	last := v.out().LastRun
	require.NotNil(t, last)
	assert.Equal(t, "completed", last.Outcome)
	assert.Equal(t, []string{"hall"}, last.Done)
	assert.Equal(t, "off", v.server.GetState("input_boolean.vacuum_dirty_kitchen").State)

	t.Log("AND: no battery cost is learned from a drop seen only in part")
	assert.Equal(t, "0.0", v.server.GetState("input_number.vacuum_cost_hall").State)
	assert.Equal(t, "0.0", v.server.GetState("input_number.vacuum_cost_trip").State)

	t.Log("AND: the new scheduler sent the robot no new job")
	assert.Empty(t, v.segmentCleans())
}
