package vacuum

import (
	"testing"
	"time"

	"homeautomation/internal/housemap"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// segmentCleans counts the segment cleans the robot was sent.
func (e *schedEnv) segmentCleans() int {
	n := 0
	for _, c := range e.robot.Commands() {
		if c.Path == "/api/v2/robot/capabilities/MapSegmentationCapability" {
			n++
		}
	}
	return n
}

// externalRunInKitchen starts a run elsewhere in the given operation mode and lets the robot clean the
// kitchen long enough for it to be the current room.
func (e *schedEnv) externalRunInKitchen(mode string) {
	e.t.Helper()
	e.start()
	e.tick()
	e.robot.SetPreset("operation_mode", mode)
	e.robot.SetStatus("cleaning", "segment")
	for i := 0; i < 5; i++ {
		e.moveTo("kitchen", 99-i)
		e.advance(30 * time.Second)
	}
	require.NotNil(e.t, e.sched().Run)
	require.Equal(e.t, "kitchen", e.sched().Run.Current)
}

// A run that makes no progress ends after exactly stall_timeout: error when the robot reported an error, and
// stalled otherwise. The room it was in stays unfinished.
func TestSchedulerStallTimeoutBoundary(t *testing.T) {
	tests := []struct {
		name, status, flag, outcome, reason string
	}{
		{"error", "error", "none", "error", "robot in error for 30m: status error, flag none"},
		{"idle with a job", "idle", "resumable", "stalled", "robot made no progress for 30m: status idle, flag resumable"},
		{"paused by someone", "paused", "resumable", "stalled",
			"robot made no progress for 30m: status paused, flag resumable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newSchedEnv(t, ModeLogOnly)
			e.externalRunInKitchen("vacuum_and_mop")
			e.robot.SetStatus(tt.status, tt.flag)
			e.robot.SetBattery(90)
			e.tick() // the stall begins
			e.advance(30*time.Minute - time.Second)
			require.NotNil(t, e.sched().Run, "one second short of stall_timeout")
			e.advance(time.Second)
			require.Nil(t, e.sched().Run, "stalled for stall_timeout")
			last := e.sched().LastRun
			assert.Equal(t, tt.outcome, last.Outcome)
			assert.Equal(t, tt.reason, last.EndReason)
			assert.Empty(t, last.Done, "the kitchen, where it stalled, is unfinished")
			assert.Equal(t, "", last.Current)
			assert.False(t, e.cleanedAt(itemKey{"kitchen", TreatmentFull}).After(schedT0))
			assert.InDelta(t, 0.0, e.cost(tripEntity), 1e-9, "no trip sample")
		})
	}
}

// A shorter stall_timeout from the config is used.
func TestSchedulerStallTimeoutFromConfig(t *testing.T) {
	e := newSchedEnv(t, ModeLogOnly, func(cfg *SchedulerConfig, _ *housemap.Map) { cfg.StallTimeout = 5 * time.Minute })
	e.externalRunInKitchen("vacuum")
	e.robot.SetStatus("error", "none")
	e.tick()
	e.advance(5 * time.Minute)
	require.Nil(t, e.sched().Run)
	assert.Equal(t, "robot in error for 5m: status error, flag none", e.sched().LastRun.EndReason)
}

// Progress resets the stall clock: two short errors with cleaning in between do not add up.
func TestSchedulerStallClockResetsOnProgress(t *testing.T) {
	e := newSchedEnv(t, ModeLogOnly)
	e.externalRunInKitchen("vacuum_and_mop")
	e.robot.SetStatus("error", "none")
	e.tick()
	e.advance(20 * time.Minute)
	e.robot.SetStatus("cleaning", "segment")
	e.advance(30 * time.Second)
	e.robot.SetStatus("error", "none")
	e.advance(20 * time.Minute)
	assert.NotNil(t, e.sched().Run, "each stretch was 20 minutes")
}

// Mop washes, recharges and a pause the live-safety rules sent are not stalls, however long they last. A
// recharge to resume a job can take hours, so a docked robot with a job in progress is never a stall unless
// the stall began with an error.
func TestSchedulerMopWashAndRechargeDoNotStall(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.startRun("kitchen", "lounge")
	e.moveTo("lounge", 95)
	e.advance(30 * time.Second)
	require.Equal(t, "lounge", e.sched().Run.Current)

	t.Log("a long mop wash with the robot idle or paused on the washing dock")
	e.robot.SetDockStatus("cleaning")
	for _, status := range []string{"idle", "paused", "docked"} {
		e.robot.SetStatus(status, "resumable")
		e.moveTo("mud", 94)
		e.advance(30 * time.Second)
		e.advance(45 * time.Minute)
		require.NotNil(t, e.sched().Run, "%s on a washing dock is not a stall", status)
	}
	e.robot.SetDockStatus("idle")

	t.Log("a three-hour recharge before the job goes on")
	e.robot.SetStatus("docked", "resumable")
	for i := 0; i < 6; i++ {
		e.advance(30 * time.Minute)
	}
	require.NotNil(t, e.sched().Run, "recharging to resume is not a stall")
	assert.Equal(t, "on", e.state("input_boolean.vacuum_dirty_lounge"))

	t.Log("the robot goes on and finishes")
	e.robot.SetStatus("cleaning", "segment")
	e.moveTo("lounge", 100)
	e.advance(30 * time.Second)
	e.moveTo("kitchen", 98)
	e.advance(30 * time.Second)
	e.robot.SetStatus("docked", "none")
	e.moveTo("mud", 97)
	e.advance(30 * time.Second)
	e.advance(30 * time.Second)
	require.Nil(t, e.sched().Run)
	assert.Equal(t, "completed", e.sched().LastRun.Outcome)
	assert.Equal(t, []string{"lounge", "kitchen"}, e.sched().LastRun.Done)
}

// An error that leaves the robot on its dock with the job still resumable keeps counting as a stall.
func TestSchedulerErrorThenDockedResumableStalls(t *testing.T) {
	e := newSchedEnv(t, ModeLogOnly)
	e.externalRunInKitchen("vacuum_and_mop")
	e.robot.SetStatus("error", "none")
	e.tick()
	e.advance(10 * time.Minute)
	e.robot.SetStatus("docked", "resumable")
	e.advance(19 * time.Minute)
	require.NotNil(t, e.sched().Run)
	e.advance(time.Minute)
	require.Nil(t, e.sched().Run)
	assert.Equal(t, "error", e.sched().LastRun.Outcome)
	assert.Equal(t, "robot in error for 30m: status docked, flag resumable", e.sched().LastRun.EndReason)
}

// The live-safety rules paused the robot because its way home is blocked. The scheduler still owns the run:
// it is not a stall, and the robot is sent home once the way clears.
func TestSchedulerSafetyPauseDoesNotStall(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.startRun("kitchen", "lounge")
	e.moveTo("lounge", 97)
	e.advance(30 * time.Second)
	clearAreas(e.hub, "pantry")
	e.s.TickForTest(true)
	require.True(t, e.sched().Run.Paused)
	e.advance(2 * time.Hour)
	require.NotNil(t, e.sched().Run, "waiting for the way home is not a stall")

	e.robot.ClearCommands()
	clearAreas(e.hub)
	e.s.TickForTest(true)
	assert.Equal(t, []string{cmdHome}, e.commandPaths())
}

// After the robot stopped, a new job with another operation mode is a new run, even well within
// stall_timeout. The same mode after a stop is the same job going on.
func TestSchedulerModeChangeWhileDockedSplitsRuns(t *testing.T) {
	e := newSchedEnv(t, ModeLogOnly)
	e.externalRunInKitchen("vacuum")

	t.Log("a recharge in the same mode is the same run")
	e.robot.SetStatus("docked", "resumable")
	e.moveTo("mud", 90)
	e.advance(30 * time.Second)
	e.advance(5 * time.Minute)
	e.robot.SetStatus("cleaning", "segment")
	e.moveTo("kitchen", 90)
	e.advance(30 * time.Second)
	require.NotNil(t, e.sched().Run)
	assert.Nil(t, e.sched().LastRun)
	assert.Equal(t, "kitchen", e.sched().Run.Current)

	t.Log("the robot stops; someone switches to vacuum_and_mop and starts the office")
	e.robot.SetStatus("docked", "resumable")
	e.moveTo("mud", 89)
	e.advance(30 * time.Second)
	e.robot.SetPreset("operation_mode", "vacuum_and_mop")
	e.advance(30 * time.Second)
	require.NotNil(t, e.sched().Run, "a mode change alone ends nothing")
	e.robot.SetStatus("cleaning", "segment")
	e.moveTo("office", 100)
	e.advance(30 * time.Second)

	last := e.sched().LastRun
	require.NotNil(t, last)
	assert.Equal(t, "superseded", last.Outcome)
	assert.Contains(t, last.EndReason, `operation mode "vacuum_and_mop"`)
	assert.Equal(t, "vacuum", last.Treatment)
	assert.Empty(t, last.Done, "the kitchen, where it stopped, is unfinished")
	assert.False(t, e.cleanedAt(itemKey{"kitchen", TreatmentVacuum}).After(schedT0))
	run := e.sched().Run
	require.NotNil(t, run, "the new job is tracked at once")
	assert.Equal(t, "full", run.Treatment)
	assert.Equal(t, 100, run.StartBattery)

	for i := 0; i < 5; i++ {
		e.moveTo("office", 99-i)
		e.advance(30 * time.Second)
	}
	e.robot.SetStatus("docked", "none")
	e.moveTo("mud", 94)
	e.advance(30 * time.Second)
	e.advance(30 * time.Second)
	assert.Equal(t, "completed", e.sched().LastRun.Outcome)
	assert.Equal(t, []string{"office"}, e.sched().LastRun.Done)
	assert.True(t, e.cleanedAt(itemKey{"office", TreatmentFull}).After(schedT0), "full clean of the office")
}

// Our own run in active mode ends in error. The scheduler may plan again, but the planner needs a docked
// robot, so nothing starts while the robot is in error. Once it is docked and clear, a new run starts, and the
// room it stalled in is still dirty.
func TestSchedulerOwnRunStallsThenPlansAgainWhenDocked(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.startRun("kitchen", "lounge")
	require.Equal(t, 1, e.segmentCleans())
	e.moveTo("lounge", 95)
	e.advance(30 * time.Second)
	require.Equal(t, "lounge", e.sched().Run.Current)

	e.robot.SetStatus("error", "none")
	e.tick()
	e.advance(30 * time.Minute)
	require.Nil(t, e.sched().Run)
	assert.Equal(t, "scheduler", e.sched().LastRun.Origin)
	assert.Equal(t, "error", e.sched().LastRun.Outcome)
	assert.Equal(t, "on", e.state("input_boolean.vacuum_dirty_lounge"), "the lounge is unfinished")

	t.Log("the robot is still in error: the plan says why nothing starts")
	e.advance(time.Minute)
	require.NotNil(t, e.sched().Plan)
	assert.Equal(t, "robot is not docked", e.sched().Plan.Reason)
	e.advance(20 * time.Minute)
	assert.Equal(t, 1, e.segmentCleans(), "no new run for a robot in error")

	t.Log("the owner clears the error; the robot is docked")
	e.robot.SetStatus("docked", "none")
	e.moveTo("mud", 95)
	e.s.TickForTest(true)
	require.NotNil(t, e.sched().Plan.PendingSince, "plan: %+v", e.sched().Plan)
	assert.ElementsMatch(t, []string{"lounge", "kitchen"}, e.sched().Plan.Rooms)
	e.advance(10 * time.Minute)
	require.NotNil(t, e.sched().Run)
	assert.Equal(t, 2, e.segmentCleans(), "a new run started")
}

// Battery used while the robot is stopped in error, idle or paused counts neither for the room nor the trip.
func TestSchedulerStallBatteryIsLeftOut(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.startRun("kitchen")
	step := func(status, flag string, battery int) {
		e.robot.SetStatus(status, flag)
		e.moveTo("kitchen", battery)
		e.advance(30 * time.Second)
	}
	step("cleaning", "segment", 100)
	step("cleaning", "segment", 96) // 4 for the kitchen
	step("error", "none", 93)       // stuck: left out
	step("error", "none", 90)
	step("paused", "resumable", 88) // freed, waits: left out
	step("cleaning", "segment", 88)
	step("cleaning", "segment", 84) // 4 for the kitchen
	e.robot.SetStatus("docked", "none")
	e.moveTo("mud", 84)
	e.advance(30 * time.Second)
	e.advance(30 * time.Second)
	require.Nil(t, e.sched().Run)
	assert.Equal(t, "completed", e.sched().LastRun.Outcome)
	assert.InDelta(t, 9.1, e.cost("input_number.vacuum_cost_kitchen"), 1e-9, "9.6 + 0.3 * (8 - 9.6) = 9.12 -> 9.1")
	assert.InDelta(t, 3.5, e.cost(tripEntity), 1e-9, "trip sample 0: 5 + 0.3 * (0 - 5)")
}

func TestShortDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Minute:                       "30m",
		4 * time.Hour:                          "4h",
		90 * time.Minute:                       "1h30m",
		30*time.Minute + 30*time.Second:        "30m30s",
		30*time.Minute + 1500*time.Millisecond: "30m1s",
		0:                                      "0s",
	} {
		assert.Equal(t, want, shortDuration(d), d.String())
	}
}
