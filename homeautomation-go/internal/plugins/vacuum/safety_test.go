package vacuum

import (
	"testing"
	"time"

	"homeautomation/internal/housemap"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	cmdHome  = "/api/v2/robot/capabilities/BasicControlCapability map[action:home]"
	cmdPause = "/api/v2/robot/capabilities/BasicControlCapability map[action:pause]"
)

// After an abort the robot heads home. If someone then steps into its way home, it pauses, and it goes home
// once the way clears.
func TestSchedulerRechecksWayHomeAfterAbort(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.startRun("kitchen", "lounge")
	e.moveTo("lounge", 97)
	e.advance(30 * time.Second)
	e.robot.ClearCommands()

	clearAreas(e.hub, "kitchen") // next room taken: go home through the pantry
	e.s.TickForTest(true)
	require.Equal(t, []string{cmdHome}, e.commandPaths())

	clearAreas(e.hub, "kitchen", "pantry") // now the way home is blocked too
	e.s.TickForTest(true)
	assert.Equal(t, []string{cmdHome, cmdPause}, e.commandPaths())
	assert.True(t, e.sched().Run.Paused)

	clearAreas(e.hub, "kitchen")
	e.s.TickForTest(true)
	assert.Equal(t, []string{cmdHome, cmdPause, cmdHome}, e.commandPaths())
	assert.False(t, e.sched().Run.Paused)
}

// At the normal end of a run the robot returns by itself. The way home is still checked.
func TestSchedulerRechecksWayHomeOnNormalReturn(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.startRun("kitchen")
	e.moveTo("kitchen", 95)
	e.advance(30 * time.Second)
	e.robot.SetStatus("returning", "none")
	e.advance(30 * time.Second)
	e.robot.ClearCommands()

	clearAreas(e.hub, "pantry")
	e.s.TickForTest(true)
	assert.Equal(t, []string{cmdPause}, e.commandPaths(), "a person in the pantry: stop before driving through")
	assert.False(t, e.sched().Run.Aborted, "the rooms were finished; this is not an abort")

	e.advance(30 * time.Second)
	assert.Len(t, e.robot.Commands(), 1, "still blocked")

	clearAreas(e.hub)
	e.s.TickForTest(true)
	assert.Equal(t, []string{cmdPause, cmdHome}, e.commandPaths())

	e.robot.SetStatus("docked", "none")
	e.moveTo("mud", 94)
	e.advance(30 * time.Second)
	e.advance(30 * time.Second)
	assert.Equal(t, "completed", e.sched().LastRun.Outcome)
	assert.Equal(t, "off", e.state("input_boolean.vacuum_dirty_kitchen"))
}

// The live check uses open regions too. The robot cleans the study, whose way home runs through the open
// family-strip-den wing. A person in the family blocks the den as well, so the robot pauses in the study
// instead of driving home past them.
func TestSchedulerLiveCheckBlocksWholeOpenRegion(t *testing.T) {
	e := newSchedEnv(t, ModeActive, func(cfg *SchedulerConfig, _ *housemap.Map) {
		cfg.Rooms["study"] = RoomConfig{SegmentID: "11", FullRaw: "48h"}
		cfg.applyDefaultsAndValidate()
	})
	e.start()
	e.startRun("study")
	e.moveTo("study", 95)
	e.advance(30 * time.Second)
	e.robot.ClearCommands()

	clearAreas(e.hub, "family")
	e.s.TickForTest(true)
	assert.Equal(t, []string{cmdPause}, e.commandPaths(), "the way home enters the den, which is open to the family")
	assert.True(t, e.sched().Run.Paused)
	assert.Equal(t, "way from study to mud enters den (open to family: area family is occupied or unknown)",
		e.sched().Run.AbortReason)

	clearAreas(e.hub)
	e.s.TickForTest(true)
	assert.Equal(t, []string{cmdPause, cmdHome}, e.commandPaths(), "home once the open region is clear")
}

// A person who walks into the far end of an open region makes its near end unsafe to clean, even though
// the robot's way there does not touch the room they are in.
func TestSchedulerLiveCheckAbortsWhenNextRoomIsOpenToAPerson(t *testing.T) {
	e := newSchedEnv(t, ModeActive, func(cfg *SchedulerConfig, _ *housemap.Map) {
		cfg.Rooms["family"] = RoomConfig{SegmentID: "8", FullRaw: "48h"}
		cfg.applyDefaultsAndValidate()
	})
	e.start()
	e.startRun("kitchen", "family")
	require.Equal(t, []string{"kitchen", "family"}, e.sched().Run.Targets)
	e.moveTo("kitchen", 95)
	e.advance(30 * time.Second)
	e.robot.ClearCommands()

	clearAreas(e.hub, "den")
	e.s.TickForTest(true)
	assert.Equal(t, []string{cmdHome}, e.commandPaths(), "the way home from the kitchen is clear")
	assert.True(t, e.sched().Run.Aborted)
	assert.Equal(t, "room family: open to den: area den is occupied or unknown", e.sched().Run.AbortReason)
}

// While the robot cleans normally and nothing changes, the scheduler sends nothing.
func TestSchedulerQuietWhileSafe(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.startRun("kitchen", "lounge")
	e.robot.ClearCommands()
	for _, room := range []string{"pantry", "lounge", "lounge", "pantry", "kitchen"} {
		e.moveTo(room, 90)
		e.advance(30 * time.Second)
	}
	assert.Empty(t, e.robot.Commands())
}

// Without a trusted position the robot is assumed to be in the last room seen. A blocked way from there
// pauses it.
func TestSchedulerUsesLastKnownRoomWhenPositionIsLost(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.startRun("kitchen")
	e.moveTo("kitchen", 95)
	e.advance(30 * time.Second)
	e.robot.SetRobotPixel(0, 0, false) // the map has no robot position any more
	e.robot.ClearCommands()

	clearAreas(e.hub, "pantry")
	e.s.TickForTest(true)
	assert.Equal(t, []string{cmdPause}, e.commandPaths(), "last seen in the kitchen: the way home crosses the pantry")
}
