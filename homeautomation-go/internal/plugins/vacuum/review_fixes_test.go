package vacuum

import (
	"testing"
	"time"

	"homeautomation/internal/housemap"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A missing cost helper is only a missing learning store: the room is still cleaned, with the floor-area
// guess.
func TestSchedulerMissingCostHelperUsesFloorAreaGuess(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.ha.SetMockState("input_number.vacuum_cost_office", nil)
	e.start()
	e.markDirty("office")
	e.tick()
	o := e.sched()
	assert.Contains(t, o.Warnings, "missing helper input_number.vacuum_cost_office; using the floor-area cost guess")
	assert.Equal(t, []string{"office"}, o.Plan.Rooms)
	assert.InDelta(t, 9.6, itemCostOf(e, "office"), 1e-9)

	// Learning has nowhere to go, and nothing breaks.
	e.advance(10 * time.Minute)
	require.NotNil(t, e.sched().Run)
	e.moveTo("office", 95)
	e.advance(30 * time.Second)
	e.robot.SetStatus("docked", "none")
	e.moveTo("mud", 94)
	e.advance(30 * time.Second)
	assert.NotPanics(t, e.tick)
	assert.Equal(t, []string{"office"}, e.sched().LastRun.Done)
	assert.Equal(t, "off", e.state("input_boolean.vacuum_dirty_office"))
}

// Turning off the full taint by hand counts as a full clean, which also clears the vacuum taint. Turning off
// only the vacuum taint leaves the full taint alone.
func TestSchedulerManualFullOffAlsoClearsVacuumTaint(t *testing.T) {
	e := newSchedEnv(t, ModeLogOnly)
	e.start()
	e.advance(13 * time.Hour) // the kitchen vacuum taint is due
	require.Equal(t, "on", e.state("input_boolean.vacuum_dirty_kitchen_vacuum"))
	e.markDirty("kitchen")
	e.tick()

	offAt := e.clk.Now()
	e.ha.SetState("input_boolean.vacuum_dirty_kitchen", "off", map[string]interface{}{})
	e.advance(time.Minute)
	assert.Equal(t, offAt.Unix(), e.cleanedAt(itemKey{"kitchen", TreatmentFull}).Unix())
	assert.Equal(t, offAt.Unix(), e.cleanedAt(itemKey{"kitchen", TreatmentVacuum}).Unix())
	assert.Equal(t, "off", e.state("input_boolean.vacuum_dirty_kitchen_vacuum"))

	before := e.cleanedAt(itemKey{"kitchen", TreatmentFull})
	e.advance(13 * time.Hour)
	require.Equal(t, "on", e.state("input_boolean.vacuum_dirty_kitchen_vacuum"))
	e.ha.SetState("input_boolean.vacuum_dirty_kitchen_vacuum", "off", map[string]interface{}{})
	e.advance(time.Minute)
	assert.Equal(t, before, e.cleanedAt(itemKey{"kitchen", TreatmentFull}), "vacuum off is vacuum only")
}

// Someone falls asleep in the pantry while the robot is in the lounge. The kitchen is next to the sleeper and
// the way home crosses the sleeper's room, even though room presence says the pantry is clear: pause.
func TestSchedulerPausesRatherThanCrossingASleepersRoom(t *testing.T) {
	e := newSchedEnv(t, ModeActive, func(cfg *SchedulerConfig, _ *housemap.Map) {
		cfg.Sleepers = []SleeperConfig{{State: "isMasterAsleep", Rooms: []string{"pantry"}}}
	})
	e.start()
	e.startRun("kitchen", "lounge")
	e.moveTo("lounge", 97)
	e.advance(30 * time.Second)
	e.robot.ClearCommands()

	require.NoError(t, e.sm.SetBool("isMasterAsleep", true))
	e.s.TickForTest(true)
	assert.Equal(t, []string{cmdPause}, e.commandPaths())
	assert.True(t, e.sched().Run.Aborted)

	require.NoError(t, e.sm.SetBool("isMasterAsleep", false))
	e.s.TickForTest(true)
	assert.Equal(t, []string{cmdPause, cmdHome}, e.commandPaths())
}

// A robot with a washing dock goes back in the middle of a job to wash its mop. Valetudo keeps the flag
// "resumable" while a job is in progress. The run goes on, the room being cleaned is not finished, and
// waiting at the dock does not count as cleaning the dock room.
func TestSchedulerMidJobMopWashIsPartOfTheRun(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.startRun("kitchen", "lounge") // tour: lounge, kitchen; the dock room is mud, also a configured room
	e.moveTo("lounge", 95)
	e.advance(30 * time.Second)
	require.Equal(t, "lounge", e.sched().Run.Current)

	t.Log("the robot heads to the dock to wash the mop")
	e.robot.SetStatus("returning", "resumable")
	e.moveTo("pantry", 94)
	e.advance(30 * time.Second)
	for _, s := range []struct{ status, flag, dock string }{
		{"docked", "resumable", "cleaning"},
		{"idle", "resumable", "cleaning"},
		{"docked", "none", "cleaning"}, // washing, even without the flag
		{"docked", "resumable", "idle"},
		{"docked", "resumable", "idle"},
		{"docked", "resumable", "idle"},
		{"docked", "resumable", "idle"},
		{"docked", "resumable", "idle"},
	} {
		e.robot.SetStatus(s.status, s.flag)
		e.robot.SetDockStatus(s.dock)
		e.moveTo("mud", 94)
		e.advance(30 * time.Second)
		require.NotNil(t, e.sched().Run, "%s/%s/%s is not the end of the run", s.status, s.flag, s.dock)
	}
	assert.Equal(t, "lounge", e.sched().Run.Current, "4 minutes at the dock do not make mud the current room")
	assert.Equal(t, "on", e.state("input_boolean.vacuum_dirty_lounge"), "the lounge is not finished yet")
	assert.Equal(t, "off", e.state("input_boolean.vacuum_dirty_mud"))

	t.Log("the robot goes back out and finishes")
	e.robot.SetDockStatus("idle")
	e.robot.SetStatus("cleaning", "segment")
	e.moveTo("lounge", 93)
	e.advance(30 * time.Second)
	e.moveTo("kitchen", 90)
	e.advance(30 * time.Second)
	assert.Equal(t, "off", e.state("input_boolean.vacuum_dirty_lounge"), "lounge done when the kitchen starts")
	e.robot.SetStatus("docked", "none")
	e.robot.SetDockStatus("cleaning") // washes the mop after the job
	e.moveTo("mud", 88)
	e.advance(30 * time.Second)
	e.advance(30 * time.Second)
	assert.NotNil(t, e.sched().Run, "still washing: not yet confirmed")
	e.robot.SetDockStatus("drying")
	e.advance(30 * time.Second)
	e.advance(30 * time.Second)
	require.Nil(t, e.sched().Run)
	assert.Equal(t, "completed", e.sched().LastRun.Outcome)
	assert.Equal(t, []string{"lounge", "kitchen"}, e.sched().LastRun.Done)
	assert.Empty(t, e.robot.Commands()[4:], "no pause and no home: the wash is not a safety problem")
}

// A mid-job recharge without positions must not trigger the "mark every target done" fallback.
func TestSchedulerMidJobRechargeWithoutPositionMarksNothing(t *testing.T) {
	e := newSchedEnv(t, ModeActive, func(_ *SchedulerConfig, m *housemap.Map) {
		m.Valetudo.PixelSizeM = 0.04 // position not trusted
	})
	e.start()
	e.startRun("kitchen")
	e.robot.SetStatus("cleaning", "segment")
	e.advance(30 * time.Second)
	e.robot.SetStatus("docked", "resumable")
	for i := 0; i < 4; i++ {
		e.advance(30 * time.Second)
	}
	assert.NotNil(t, e.sched().Run)
	assert.Equal(t, "on", e.state("input_boolean.vacuum_dirty_kitchen"))
}

// When the robot's state cannot be read during our run, the safety rules still work from the last state and
// the last room seen.
func TestSchedulerSafetyKeepsWorkingWhenRobotStateFails(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.startRun("kitchen", "lounge")
	e.moveTo("lounge", 97)
	e.advance(30 * time.Second)
	e.robot.ClearCommands()

	e.robot.Fail("/api/v2/robot/state/attributes", 502)
	clearAreas(e.hub, "kitchen")
	e.s.TickForTest(true)
	assert.Equal(t, []string{cmdHome}, e.commandPaths(), "the robot still takes commands")
	assert.Contains(t, e.sched().Robot.LastError, "502")
}

// A warning that cleared is logged again when it comes back.
func TestSchedulerForgetsClearedWarnings(t *testing.T) {
	e := newSchedEnv(t, ModeLogOnly)
	e.start()
	e.robot.Fail("/api/v2/robot/state/attributes", 502)
	e.tick()
	assert.True(t, e.s.warnedOnce["robot"])
	e.robot.Fail("/api/v2/robot/state/attributes", 0)
	e.tick()
	assert.False(t, e.s.warnedOnce["robot"])
}

// Battery used while the robot is docked in the middle of a job, for example for a mop wash, counts neither
// for the room nor for the trip.
func TestSchedulerMidJobDockBatteryIsLeftOut(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.startRun("kitchen")
	step := func(room, status, flag string, battery int) {
		e.robot.SetStatus(status, flag)
		e.moveTo(room, battery)
		e.advance(30 * time.Second)
	}
	step("kitchen", "cleaning", "segment", 100)
	step("kitchen", "cleaning", "segment", 92) // 8 for the kitchen
	step("mud", "docked", "resumable", 80)     // mop wash: left out
	step("mud", "idle", "resumable", 78)       // still washing: left out
	step("kitchen", "cleaning", "segment", 78)
	step("kitchen", "cleaning", "segment", 76) // 2 for the kitchen
	step("mud", "docked", "none", 76)
	step("mud", "docked", "none", 76)
	require.Nil(t, e.sched().Run)
	assert.InDelta(t, 9.7, e.cost("input_number.vacuum_cost_kitchen"), 1e-9, "battery drop 10 (8+2)")
	assert.InDelta(t, 3.5, e.cost(tripEntity), 1e-9, "trip sample 10 - 10 = 0: 5 + 0.3 * (0 - 5)")
}
