package vacuum

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"homeautomation/internal/housemap"
	"homeautomation/internal/valetudo"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fanPath = "/api/v2/robot/capabilities/FanSpeedControlCapability/preset"

// quietTravel turns quiet travel on with the "low" preset. The test treatments use "medium".
func quietTravel(cfg *SchedulerConfig, _ *housemap.Map) {
	cfg.TravelFanSpeed = "low"
}

// fanCmds lists the fan presets the robot accepted, in order.
func (e *schedEnv) fanCmds() []string {
	out := []string{}
	for _, c := range e.robot.Commands() {
		if c.Path == fanPath {
			name, _ := c.Body["name"].(string)
			out = append(out, name)
		}
	}
	return out
}

// step moves the robot and lets 30 seconds pass.
func (e *schedEnv) step(room string, battery int) {
	e.moveTo(room, battery)
	e.advance(30 * time.Second)
}

func (e *schedEnv) interval() time.Duration {
	return e.s.currentPollInterval()
}

func (e *schedEnv) hasWarning(part string) bool {
	for _, w := range e.sched().Warnings {
		if strings.Contains(w, part) {
			return true
		}
	}
	return false
}

func TestQuietTravelSwitchesFanByRoom(t *testing.T) {
	e := newSchedEnv(t, ModeActive, quietTravel)
	e.start()
	assert.Equal(t, 30*time.Second, e.interval(), "no run: normal polling")

	e.startRun("kitchen", "lounge") // tour: lounge, then kitchen
	assert.Equal(t, []string{
		"/api/v2/robot/capabilities/OperationModeControlCapability/preset map[name:vacuum_and_mop]",
		"/api/v2/robot/capabilities/FanSpeedControlCapability/preset map[name:low]",
		"/api/v2/robot/capabilities/WaterUsageControlCapability/preset map[name:medium]",
		"/api/v2/robot/capabilities/MapSegmentationCapability map[action:start_segment_action customOrder:true " +
			"iterations:1 segment_ids:[7 3]]",
	}, e.commandPaths(), "the run starts at the travel fan speed")
	run := e.sched().Run
	assert.True(t, run.QuietTravel)
	assert.Equal(t, "low", run.FanSpeed)
	assert.Equal(t, 5*time.Second, e.interval(), "travelling: fast polling")

	e.step("pantry", 98) // driving through
	assert.Equal(t, []string{"low"}, e.fanCmds())
	assert.Equal(t, 5*time.Second, e.interval())

	e.step("lounge", 97) // first target
	assert.Equal(t, []string{"low", "medium"}, e.fanCmds(), "entering the first target raises the fan")
	assert.Equal(t, "medium", e.sched().Run.FanSpeed)
	assert.Equal(t, 30*time.Second, e.interval(), "in the target room: normal polling")

	e.step("lounge", 95)
	e.step("pantry", 94) // leaving the lounge
	assert.Equal(t, []string{"low", "medium", "low"}, e.fanCmds(), "leaving it drops the fan")
	assert.Equal(t, 5*time.Second, e.interval())

	e.step("kitchen", 93) // second target
	assert.Equal(t, []string{"low", "medium", "low", "medium"}, e.fanCmds(), "the second target raises it")

	e.robot.SetStatus("returning", "none")
	e.step("kitchen", 90)
	assert.Equal(t, []string{"low", "medium", "low", "medium", "low"}, e.fanCmds(), "the trip home is low")
	assert.Equal(t, 5*time.Second, e.interval())
	e.step("pantry", 89)
	e.robot.SetStatus("docked", "none")
	e.step("mud", 89)
	e.step("mud", 89)
	require.Nil(t, e.sched().Run)
	assert.Equal(t, "completed", e.sched().LastRun.Outcome)
	assert.Equal(t, []string{"low", "medium", "low", "medium", "low"}, e.fanCmds(),
		"no other fan level is restored after the run")
	assert.Equal(t, 30*time.Second, e.interval(), "run over: normal polling")
}

func TestQuietTravelNoRepeatedWrites(t *testing.T) {
	e := newSchedEnv(t, ModeActive, quietTravel)
	e.start()
	e.startRun("kitchen", "lounge")
	for i := 0; i < 5; i++ {
		e.step("pantry", 98)
	}
	for i := 0; i < 10; i++ {
		e.step("lounge", 97)
	}
	for i := 0; i < 5; i++ {
		e.step("pantry", 96)
	}
	assert.Equal(t, []string{"low", "medium", "low"}, e.fanCmds())
}

func TestQuietTravelPassingThroughKeepsFanLow(t *testing.T) {
	e := newSchedEnv(t, ModeActive, quietTravel)
	e.start()
	e.startRun("pantry", "kitchen") // tour: pantry, then kitchen

	e.step("kitchen", 99) // a later target, seen once
	e.step("office", 99)  // not a target
	e.step("mud", 99)     // the dock room
	assert.Equal(t, []string{"low"}, e.fanCmds(), "passing through a later target or another room keeps it low")

	e.step("pantry", 98)
	assert.Equal(t, []string{"low", "medium"}, e.fanCmds())
	e.step("kitchen", 97) // the next target, next door: the fan is already up
	assert.Equal(t, []string{"low", "medium"}, e.fanCmds())
	assert.Equal(t, "pantry", e.sched().Run.Current, "the kitchen is not yet confirmed")
	e.step("kitchen", 96)
	e.step("kitchen", 95)
	assert.Equal(t, "kitchen", e.sched().Run.Current)
	assert.Equal(t, []string{"low", "medium"}, e.fanCmds())
}

// A later target the robot stays in for min_dwell counts as the room being cleaned, so it is not cleaned at
// the travel level.
func TestQuietTravelOutOfOrderRoomAfterDwellIsRaised(t *testing.T) {
	e := newSchedEnv(t, ModeActive, quietTravel)
	e.start()
	e.startRun("pantry", "kitchen")
	e.step("kitchen", 99)
	e.step("kitchen", 99)
	e.step("kitchen", 99)
	e.step("kitchen", 99)
	assert.Equal(t, []string{"low"}, e.fanCmds(), "not yet min_dwell")
	e.step("kitchen", 99)
	assert.Equal(t, "kitchen", e.sched().Run.Current)
	assert.Equal(t, []string{"low", "medium"}, e.fanCmds())
}

func TestQuietTravelRaiseFailsTwiceStopsSwitching(t *testing.T) {
	e := newSchedEnv(t, ModeActive, quietTravel)
	e.start()
	e.startRun("kitchen", "lounge")
	e.robot.FailPreset("FanSpeedControlCapability", "medium", http.StatusInternalServerError)

	e.step("lounge", 97)
	assert.Equal(t, []string{"low"}, e.fanCmds())
	assert.False(t, e.hasWarning("could not raise"), "one failure is retried first")
	assert.True(t, e.sched().Run.QuietTravel)

	e.step("lounge", 96) // the retry fails too
	assert.True(t, e.hasWarning("could not raise the fan to \"medium\" twice"))
	assert.False(t, e.sched().Run.QuietTravel, "switching stopped")
	assert.Equal(t, 30*time.Second, e.interval(), "no fast polling once switching stopped")

	// The robot takes commands again: the fan goes to the treatment level and stays there.
	e.robot.FailPreset("FanSpeedControlCapability", "medium", 0)
	e.step("lounge", 95)
	assert.Equal(t, []string{"low", "medium"}, e.fanCmds())
	e.step("pantry", 94)
	e.step("kitchen", 93)
	e.robot.SetStatus("returning", "none")
	e.step("pantry", 92)
	assert.Equal(t, []string{"low", "medium"}, e.fanCmds(), "never lowered again in this run")
	assert.Equal(t, "medium", e.sched().Run.FanSpeed)
	assert.True(t, e.hasWarning("could not raise"), "the warning holds for the rest of the run")

	e.robot.SetStatus("docked", "none")
	e.step("mud", 92)
	e.step("mud", 92)
	require.Nil(t, e.sched().Run)
	assert.False(t, e.hasWarning("could not raise"), "the warning ends with the run")
}

func TestQuietTravelRaiseRetrySucceeds(t *testing.T) {
	e := newSchedEnv(t, ModeActive, quietTravel)
	e.start()
	e.startRun("kitchen", "lounge")
	e.robot.FailPreset("FanSpeedControlCapability", "medium", http.StatusInternalServerError)
	e.step("lounge", 97)
	e.robot.FailPreset("FanSpeedControlCapability", "medium", 0)
	e.step("lounge", 96)
	assert.Equal(t, []string{"low", "medium"}, e.fanCmds())
	assert.True(t, e.sched().Run.QuietTravel)
	assert.Empty(t, e.sched().Warnings)
	e.step("pantry", 95)
	assert.Equal(t, []string{"low", "medium", "low"}, e.fanCmds(), "switching goes on")
}

func TestQuietTravelLowerFailureIsOnlyAWarning(t *testing.T) {
	e := newSchedEnv(t, ModeActive, quietTravel)
	e.start()
	e.startRun("kitchen", "lounge")
	e.step("lounge", 97)
	require.Equal(t, []string{"low", "medium"}, e.fanCmds())

	e.robot.FailPreset("FanSpeedControlCapability", "low", http.StatusInternalServerError)
	e.step("pantry", 96)
	assert.True(t, e.hasWarning("could not lower the fan to \"low\""))
	run := e.sched().Run
	require.NotNil(t, run, "cleaning goes on")
	assert.False(t, run.Aborted)
	assert.True(t, run.QuietTravel)
	assert.Equal(t, "medium", run.FanSpeed)

	e.robot.FailPreset("FanSpeedControlCapability", "low", 0)
	e.step("pantry", 95)
	assert.Equal(t, []string{"low", "medium"}, e.fanCmds(), "not retried on this leg")

	e.step("kitchen", 94) // the fan is already at the treatment level
	assert.Equal(t, []string{"low", "medium"}, e.fanCmds())
	assert.False(t, e.hasWarning("could not lower"))
	e.robot.SetStatus("returning", "none")
	e.step("kitchen", 93)
	assert.Equal(t, []string{"low", "medium", "low"}, e.fanCmds(), "the next leg is lowered again")
}

func TestQuietTravelStartFallsBackToTreatmentFan(t *testing.T) {
	e := newSchedEnv(t, ModeActive, quietTravel)
	e.start()
	e.robot.FailPreset("FanSpeedControlCapability", "low", http.StatusBadRequest)
	e.startRun("kitchen")
	assert.Equal(t, []string{
		"/api/v2/robot/capabilities/OperationModeControlCapability/preset map[name:vacuum_and_mop]",
		"/api/v2/robot/capabilities/FanSpeedControlCapability/preset map[name:medium]",
		"/api/v2/robot/capabilities/WaterUsageControlCapability/preset map[name:medium]",
		"/api/v2/robot/capabilities/MapSegmentationCapability map[action:start_segment_action customOrder:true " +
			"iterations:1 segment_ids:[3]]",
	}, e.commandPaths(), "a travel preset that fails does not stop the run")
	assert.Equal(t, "medium", e.sched().Run.FanSpeed)
	assert.True(t, e.hasWarning("could not lower"))
}

func TestQuietTravelSentHomeStaysLow(t *testing.T) {
	e := newSchedEnv(t, ModeActive, quietTravel)
	e.start()
	e.startRun("kitchen")
	e.step("kitchen", 31)
	require.Equal(t, []string{"low", "medium"}, e.fanCmds())
	e.robot.ClearCommands()

	e.step("kitchen", 30) // battery floor: sent home
	assert.Equal(t, []string{
		"/api/v2/robot/capabilities/BasicControlCapability map[action:home]",
		"/api/v2/robot/capabilities/FanSpeedControlCapability/preset map[name:low]",
	}, e.commandPaths())
	e.step("kitchen", 30)
	e.step("pantry", 30)
	assert.Equal(t, []string{"low"}, e.fanCmds())
}

func TestQuietTravelMopWashIsTravel(t *testing.T) {
	e := newSchedEnv(t, ModeActive, quietTravel)
	e.start()
	e.startRun("kitchen")
	e.step("kitchen", 95)
	e.robot.SetStatus("returning", "resumable")
	e.step("pantry", 94)
	e.robot.SetStatus("docked", "resumable")
	e.step("mud", 94)
	e.step("mud", 94)
	require.NotNil(t, e.sched().Run, "a mop wash is part of the run")
	e.robot.SetStatus("cleaning", "segment")
	e.step("pantry", 94)
	e.step("kitchen", 93)
	assert.Equal(t, []string{"low", "medium", "low", "medium"}, e.fanCmds())
}

func TestQuietTravelWithoutPositionUsesTreatmentFan(t *testing.T) {
	e := newSchedEnv(t, ModeActive, quietTravel, func(_ *SchedulerConfig, m *housemap.Map) {
		m.Valetudo.PixelSizeM = 0.04 // the position is not trusted
	})
	e.start()
	e.startRun("kitchen")
	e.step("kitchen", 95)
	assert.Equal(t, []string{"low", "medium"}, e.fanCmds(),
		"without a position the robot may be in its room: never clean at the travel level")
}

func TestQuietTravelNoFrameIsOff(t *testing.T) {
	e := newSchedEnv(t, ModeActive, quietTravel, func(cfg *SchedulerConfig, m *housemap.Map) {
		cfg.DockRoom = "mud"
		m.Valetudo = nil
	})
	e.start()
	e.startRun("kitchen")
	assert.Equal(t, []string{"medium"}, e.fanCmds())
	assert.False(t, e.sched().Run.QuietTravel)
}

func TestQuietTravelExternalRunAndLogOnlySendNoFan(t *testing.T) {
	t.Run("external run in active mode", func(t *testing.T) {
		e := newSchedEnv(t, ModeActive, quietTravel)
		e.start()
		e.tick()
		e.robot.SetPreset("operation_mode", "vacuum_and_mop")
		e.robot.SetStatus("cleaning", "segment")
		for _, room := range []string{"pantry", "kitchen", "kitchen", "kitchen", "kitchen", "kitchen", "pantry"} {
			e.step(room, 90)
		}
		require.NotNil(t, e.sched().Run)
		assert.Equal(t, "external", e.sched().Run.Origin)
		assert.False(t, e.sched().Run.QuietTravel)
		assert.Equal(t, 30*time.Second, e.interval())
		assert.Empty(t, e.robot.Commands())
	})
	t.Run("log_only", func(t *testing.T) {
		e := newSchedEnv(t, ModeLogOnly, quietTravel)
		e.start()
		e.markDirty("kitchen")
		e.tick()
		e.advance(10 * time.Minute)
		require.Equal(t, "full:kitchen", e.s.loggedWould)
		e.robot.SetStatus("cleaning", "segment")
		e.step("kitchen", 90)
		e.step("pantry", 90)
		assert.Empty(t, e.robot.Commands())
	})
	t.Run("read-only", func(t *testing.T) {
		e := newSchedEnv(t, ModeActive, quietTravel)
		e.s.readOnly = true
		e.start()
		e.advance(13 * time.Hour)
		e.advance(11 * time.Minute)
		assert.Empty(t, e.robot.Commands())
	})
}

func TestQuietTravelOffKeepsTodaysBehaviour(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.startRun("kitchen", "lounge")
	for _, room := range []string{"pantry", "lounge", "lounge", "pantry", "kitchen"} {
		e.step(room, 90)
		assert.Equal(t, 30*time.Second, e.interval())
	}
	e.robot.SetStatus("returning", "none")
	e.step("pantry", 89)
	e.robot.SetStatus("docked", "none")
	e.step("mud", 89)
	e.step("mud", 89)
	assert.Equal(t, []string{
		"/api/v2/robot/capabilities/OperationModeControlCapability/preset map[name:vacuum_and_mop]",
		"/api/v2/robot/capabilities/FanSpeedControlCapability/preset map[name:medium]",
		"/api/v2/robot/capabilities/WaterUsageControlCapability/preset map[name:medium]",
		"/api/v2/robot/capabilities/MapSegmentationCapability map[action:start_segment_action customOrder:true " +
			"iterations:1 segment_ids:[7 3]]",
	}, e.commandPaths())
	assert.False(t, e.sched().LastRun.QuietTravel)
	assert.Equal(t, "medium", e.sched().LastRun.FanSpeed)
}

func TestQuietTravelTreatmentWithoutFanDoesNothing(t *testing.T) {
	e := newSchedEnv(t, ModeActive, quietTravel, func(cfg *SchedulerConfig, _ *housemap.Map) {
		cfg.Treatments[TreatmentVacuum] = TreatmentConfig{OperationMode: "vacuum"}
	})
	e.start()
	e.advance(13 * time.Hour) // only the kitchen vacuum taint is due
	e.advance(10 * time.Minute)
	require.NotNil(t, e.sched().Run)
	e.step("pantry", 95)
	e.step("kitchen", 94)
	assert.Empty(t, e.fanCmds())
	assert.False(t, e.sched().Run.QuietTravel)
	assert.Equal(t, 30*time.Second, e.interval())
}

func TestQuietTravelChecksFanPresets(t *testing.T) {
	presets := []string{"low", "medium", "high", "max"}
	t.Run("unknown preset turns it off", func(t *testing.T) {
		e := newSchedEnv(t, ModeActive, func(cfg *SchedulerConfig, _ *housemap.Map) { cfg.TravelFanSpeed = "quiet" })
		e.robot.SetPresetList("FanSpeedControlCapability", presets)
		e.start()
		e.s.TickForTest(true)
		assert.True(t, e.hasWarning(`scheduler.travel_fan_speed "quiet" is not one of the robot's fan presets`))
		e.startRunAfterTick("kitchen")
		assert.Equal(t, []string{"medium"}, e.fanCmds())
		assert.False(t, e.sched().Run.QuietTravel)
	})
	t.Run("known preset", func(t *testing.T) {
		e := newSchedEnv(t, ModeActive, quietTravel)
		e.robot.SetPresetList("FanSpeedControlCapability", presets)
		e.start()
		e.startRun("kitchen")
		assert.Equal(t, []string{"low"}, e.fanCmds())
		assert.Empty(t, e.sched().Warnings)
	})
	t.Run("list unavailable keeps it on and checks again before a run", func(t *testing.T) {
		e := newSchedEnv(t, ModeActive, func(cfg *SchedulerConfig, _ *housemap.Map) { cfg.TravelFanSpeed = "quiet" })
		e.start()
		e.s.TickForTest(true) // no presets endpoint: cannot check
		assert.False(t, e.s.travelFanChecked)
		assert.Empty(t, e.sched().Warnings)
		e.robot.SetPresetList("FanSpeedControlCapability", presets)
		e.startRunAfterTick("kitchen")
		assert.True(t, e.s.travelFanChecked)
		assert.Equal(t, []string{"medium"}, e.fanCmds(), "checked before the run: quiet is not a preset")
	})
}

// countingRobot counts state reads, one per poll.
type countingRobot struct {
	Robot
	polls atomic.Int64
}

func (c *countingRobot) State(ctx context.Context) (*valetudo.State, error) {
	c.polls.Add(1)
	return c.Robot.State(ctx)
}

// The loop polls at travel_track_interval while the robot travels, and at track_interval once it is in the
// room it cleans.
func TestQuietTravelLoopPollsFasterWhileTravelling(t *testing.T) {
	e := newSchedEnv(t, ModeActive, quietTravel, func(cfg *SchedulerConfig, _ *housemap.Map) {
		cfg.TrackInterval = time.Hour
		cfg.TravelTrackInterval = 10 * time.Millisecond
	})
	robot := &countingRobot{Robot: e.s.robot}
	e.s.robot = robot
	e.start()
	e.startRun("kitchen", "lounge")
	e.moveTo("pantry", 98)

	e.s.wg.Add(1)
	go e.s.loop()
	start := robot.polls.Load()
	require.Eventually(t, func() bool { return robot.polls.Load() >= start+5 }, 5*time.Second, 5*time.Millisecond,
		"travelling: polls every travel_track_interval")

	e.moveTo("lounge", 97)
	require.Eventually(t, func() bool { return e.sched().Run.FanSpeed == "medium" }, 5*time.Second,
		5*time.Millisecond)
	settled := robot.polls.Load()
	time.Sleep(300 * time.Millisecond)
	// At 10 ms this would be about 30 polls. One poll that was already due may still run.
	assert.LessOrEqual(t, robot.polls.Load()-settled, int64(1),
		"in the target room: back to the hour-long track_interval")
}

// startRunAfterTick is startRun for a scheduler that has already ticked: the dirty rooms wake it.
func (e *schedEnv) startRunAfterTick(rooms ...string) {
	e.t.Helper()
	e.markDirty(rooms...)
	e.s.TickForTest(true)
	require.NotNil(e.t, e.sched().Plan.PendingSince, "plan is pending: %+v", e.sched().Plan)
	e.advance(10 * time.Minute)
	require.NotNil(e.t, e.sched().Run, "run started")
}
