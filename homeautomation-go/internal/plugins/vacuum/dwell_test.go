package vacuum

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noRoom puts the robot in no room of the house map, such as a gap at a room boundary.
func (e *schedEnv) noRoom(battery int) {
	e.robot.SetRobotPixel(pixelAtOK(50, 50))
	e.robot.SetBattery(battery)
}

// As seen on a real run: the robot entered the first room of the tour, its position fell inside the next
// room for a moment near an open boundary, and it went back and cleaned the first room for minutes. The first
// room must stay current, at the treatment's fan, until the robot really moves on. The pantry and kitchen
// stand in for two rooms joined by an opening.
func TestSchedulerBlipIntoNextRoomDoesNotFinishCurrent(t *testing.T) {
	e := newSchedEnv(t, ModeActive, quietTravel)
	e.start()
	e.startRun("pantry", "kitchen") // tour: pantry, then kitchen
	require.Equal(t, []string{"pantry", "kitchen"}, e.sched().Run.Targets)
	costBefore := e.cost("input_number.vacuum_cost_pantry") // 0: no sample yet

	e.step("pantry", 99) // first target: counts at once
	require.Equal(t, "pantry", e.sched().Run.Current)
	require.Equal(t, []string{"low", "medium"}, e.fanCmds())

	// Two position samples in the kitchen, 30 seconds apart: less than next_room_dwell.
	e.step("kitchen", 99)
	e.step("kitchen", 99)
	run := e.sched().Run
	assert.Equal(t, "pantry", run.Current, "a blip into the next room does not end the pantry")
	assert.Empty(t, run.Done)
	assert.Equal(t, "on", e.state("input_boolean.vacuum_dirty_pantry"))
	assert.Equal(t, []string{"low", "medium"}, e.fanCmds(), "the fan stays at the treatment level in the kitchen")

	// Back in the pantry for 9 minutes, with one sample in no room.
	battery := 98
	for i := 0; i < 18; i++ {
		if i == 7 {
			e.noRoom(battery)
			e.advance(30 * time.Second)
			continue
		}
		e.step("pantry", battery)
		if i%3 == 0 {
			battery--
		}
		assert.Equal(t, "pantry", e.sched().Run.Current, "poll %d", i)
		assert.Equal(t, "on", e.state("input_boolean.vacuum_dirty_pantry"), "poll %d", i)
	}
	assert.Equal(t, []string{"low", "medium"}, e.fanCmds(), "the pantry is cleaned at the treatment level throughout")
	assert.Equal(t, costBefore, e.cost("input_number.vacuum_cost_pantry"), "no cost sample is learned early")

	// Now the robot moves to the kitchen for good.
	movedAt := e.clk.Now()
	e.step("kitchen", battery)
	e.step("kitchen", battery)
	assert.Equal(t, "pantry", e.sched().Run.Current, "30 seconds in the kitchen are not yet next_room_dwell")
	e.step("kitchen", battery)
	run = e.sched().Run
	assert.Equal(t, "kitchen", run.Current)
	assert.Equal(t, []string{"pantry"}, run.Done)
	assert.Equal(t, "off", e.state("input_boolean.vacuum_dirty_pantry"))
	cleaned := e.cleanedAt(itemKey{"pantry", TreatmentFull})
	assert.False(t, cleaned.Before(movedAt.Add(time.Minute)), "the pantry is done only after the real move")
	assert.Equal(t, []string{"low", "medium"}, e.fanCmds(), "no fan change on the whole move")

	// The pantry's sample is its real drop, and not the 0% the blip produced in production. The robot went
	// 99 -> 92 while in the pantry, but the drop to the first pantry poll after the blip goes to the kitchen,
	// the room at the earlier poll, so the sample is 6. The first guess is 16 m2 * 0.6 = 9.6.
	require.Equal(t, 92, battery)
	assert.InDelta(t, 9.6+0.3*(6-9.6), e.cost("input_number.vacuum_cost_pantry"), 0.05)
}

// Blips to no room or to a room that is not a target do not restart the wait in the next room. Only going
// back to the current room does.
func TestSchedulerNextRoomDwellSurvivesBlipsAway(t *testing.T) {
	e := newSchedEnv(t, ModeActive, quietTravel)
	e.start()
	e.startRun("pantry", "kitchen")
	e.step("pantry", 99)
	e.step("pantry", 98)

	e.moveTo("kitchen", 97)
	e.advance(20 * time.Second) // first seen in the kitchen: k0
	e.noRoom(97)
	e.advance(20 * time.Second) // k0+20s
	e.moveTo("bath", 97)        // a neighbour that is not a target
	e.advance(20 * time.Second) // k0+40s
	assert.Equal(t, []string{"low", "medium", "low"}, e.fanCmds(), "a room that is neither current nor next is travel")
	e.moveTo("kitchen", 97)
	e.advance(15 * time.Second) // k0+55s
	assert.Equal(t, "pantry", e.sched().Run.Current, "55 seconds since the robot first entered the kitchen")
	assert.Equal(t, []string{"low", "medium", "low", "medium"}, e.fanCmds(), "the next room gets the treatment fan")
	e.advance(5 * time.Second)
	assert.Equal(t, "kitchen", e.sched().Run.Current, "a minute since the robot first entered the kitchen")
	assert.Equal(t, []string{"pantry"}, e.sched().Run.Done)

	// Going back to the current room restarts the wait.
	e2 := newSchedEnv(t, ModeActive)
	e2.start()
	e2.startRun("pantry", "kitchen")
	e2.step("pantry", 99)
	e2.step("kitchen", 99)
	e2.step("pantry", 99)
	e2.step("kitchen", 99)
	e2.step("kitchen", 99)
	assert.Equal(t, "pantry", e2.sched().Run.Current, "the wait started again at the second kitchen sample")
	e2.step("kitchen", 99)
	assert.Equal(t, "kitchen", e2.sched().Run.Current)
}

// Time not cleaning, such as a mop wash, does not count toward next_room_dwell.
func TestSchedulerNextRoomDwellSkipsMopWash(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.startRun("pantry", "kitchen")
	e.step("pantry", 99)
	e.step("kitchen", 98)

	e.robot.SetStatus("returning", "resumable")
	e.step("kitchen", 98)
	e.robot.SetStatus("docked", "resumable")
	e.robot.SetDockStatus("cleaning")
	for i := 0; i < 4; i++ {
		e.step("mud", 98)
	}
	e.robot.SetDockStatus("idle")
	e.robot.SetStatus("cleaning", "segment")
	e.step("kitchen", 97)
	e.step("kitchen", 96)
	assert.Equal(t, "pantry", e.sched().Run.Current, "the mop wash did not count as time in the kitchen")
	e.step("kitchen", 95)
	assert.Equal(t, "kitchen", e.sched().Run.Current)
}

// A last room cleaned in less than next_room_dwell still counts when the job ends there, as it did when the
// next room counted at once.
func TestSchedulerShortLastRoomCountsAtTheEnd(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.startRun("pantry", "kitchen")
	e.step("pantry", 99)
	e.step("pantry", 98)
	e.step("kitchen", 97)
	require.Equal(t, "pantry", e.sched().Run.Current)

	e.robot.SetStatus("returning", "none")
	e.step("pantry", 96) // driving home through the pantry is not cleaning it
	e.robot.SetStatus("docked", "none")
	e.step("mud", 96)
	e.step("mud", 96)
	require.Nil(t, e.sched().Run)
	last := e.sched().LastRun
	assert.Equal(t, "completed", last.Outcome)
	assert.Equal(t, []string{"pantry", "kitchen"}, last.Done)
	assert.Equal(t, "off", e.state("input_boolean.vacuum_dirty_kitchen"))
}

// A run that does not complete leaves an unconfirmed next room dirty, like its current room.
func TestSchedulerPendingNextRoomStaysDirtyOnStall(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.startRun("pantry", "kitchen")
	e.step("pantry", 99)
	e.step("kitchen", 98)

	e.robot.SetStatus("error", "none")
	for i := 0; i < 61 && e.sched().Run != nil; i++ {
		e.step("kitchen", 98)
	}
	require.Nil(t, e.sched().Run)
	assert.Equal(t, "error", e.sched().LastRun.Outcome)
	assert.Empty(t, e.sched().LastRun.Done)
	assert.Equal(t, "on", e.state("input_boolean.vacuum_dirty_pantry"))
	assert.Equal(t, "on", e.state("input_boolean.vacuum_dirty_kitchen"))
}

// A run started elsewhere still needs min_dwell for every room; next_room_dwell is only for our own tour.
func TestSchedulerExternalRunKeepsMinDwell(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.robot.SetStatus("cleaning", "segment")
	e.step("pantry", 99)
	for i := 0; i < 4; i++ {
		e.step("pantry", 99)
	}
	require.NotNil(t, e.sched().Run)
	assert.Equal(t, "pantry", e.sched().Run.Current, "two minutes in the pantry")
	e.step("kitchen", 99)
	e.step("kitchen", 99)
	e.step("kitchen", 99)
	assert.Equal(t, "pantry", e.sched().Run.Current, "a minute in the kitchen is not min_dwell")
	e.step("kitchen", 99)
	e.step("kitchen", 99)
	assert.Equal(t, "kitchen", e.sched().Run.Current)
}
