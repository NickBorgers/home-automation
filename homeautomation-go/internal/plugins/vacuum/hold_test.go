package vacuum

import (
	"testing"
	"time"

	"homeautomation/internal/housemap"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func holdWhile(conds ...HoldCondition) func(cfg *SchedulerConfig, _ *housemap.Map) {
	return func(cfg *SchedulerConfig, _ *housemap.Map) { cfg.HoldWhile = conds }
}

var tvOn = HoldCondition{Variable: "isTVPlaying", Value: true}

func TestBuildPlanHold(t *testing.T) {
	in := baseInput(loadTestMap(t).Graph(), full("kitchen", 2, 5))
	in.Hold = "held: isTVPlaying is true"
	p := BuildPlan(in)
	assert.True(t, p.Empty())
	assert.Equal(t, "held: isTVPlaying is true", p.Reason)
	require.Len(t, p.Skipped, 1)
	assert.Equal(t, "held: isTVPlaying is true", p.Skipped[0].Reason)

	in.Items = nil
	assert.Equal(t, "nothing is dirty", BuildPlan(in).Reason, "nothing dirty says so, held or not")
}

func TestHoldWhileHeldThenReleased(t *testing.T) {
	e := newSchedEnv(t, ModeActive, holdWhile(tvOn))
	e.start()
	require.NoError(t, e.sm.SetBool("isTVPlaying", true))
	e.markDirty("kitchen")
	e.s.TickForTest(true)

	p := e.sched().Plan
	assert.Empty(t, p.Rooms)
	assert.Equal(t, "held: isTVPlaying is true", p.Reason)
	assert.Nil(t, p.PendingSince)
	e.advance(30 * time.Minute)
	assert.Nil(t, e.sched().Run, "no run while the TV is on")
	assert.Empty(t, e.robot.Commands())

	t.Log("the TV goes off: the plan must hold for stable_for from now")
	require.NoError(t, e.sm.SetBool("isTVPlaying", false))
	e.s.TickForTest(true)
	p = e.sched().Plan
	assert.Equal(t, []string{"kitchen"}, p.Rooms)
	require.NotNil(t, p.PendingSince)
	assert.Equal(t, e.clk.Now(), *p.PendingSince)
	e.advance(9 * time.Minute)
	assert.Nil(t, e.sched().Run)
	e.advance(time.Minute)
	assert.NotNil(t, e.sched().Run)
}

func TestHoldWhileOnAgainRestartsTheWait(t *testing.T) {
	e := newSchedEnv(t, ModeActive, holdWhile(tvOn))
	e.start()
	e.markDirty("kitchen")
	e.s.TickForTest(true)
	require.NotNil(t, e.sched().Plan.PendingSince)
	e.advance(9 * time.Minute)

	require.NoError(t, e.sm.SetBool("isTVPlaying", true))
	e.s.TickForTest(true)
	require.NoError(t, e.sm.SetBool("isTVPlaying", false))
	e.s.TickForTest(true)
	e.advance(time.Minute)
	assert.Nil(t, e.sched().Run, "the hold dropped the plan, so the wait started over")
	e.advance(9 * time.Minute)
	assert.NotNil(t, e.sched().Run)
}

func TestHoldWhileDoesNotStopARun(t *testing.T) {
	e := newSchedEnv(t, ModeActive, holdWhile(tvOn))
	e.start()
	e.startRun("kitchen")
	e.robot.ClearCommands()
	require.NoError(t, e.sm.SetBool("isTVPlaying", true))
	e.s.TickForTest(true)
	e.moveTo("kitchen", 95)
	e.advance(30 * time.Second)
	require.NotNil(t, e.sched().Run)
	assert.False(t, e.sched().Run.Aborted)
	assert.Empty(t, e.robot.Commands(), "a run already going goes on")
}

func TestHoldWhileUnreadableVariableDoesNotHold(t *testing.T) {
	e := newSchedEnv(t, ModeActive, holdWhile(HoldCondition{Variable: "isNoSuchVariable", Value: true}))
	e.start()
	e.startRun("kitchen")
	assert.True(t, e.hasWarning("cannot read hold_while variable isNoSuchVariable"), "%v", e.sched().Warnings)
}

func TestHoldWhileMultipleConditions(t *testing.T) {
	e := newSchedEnv(t, ModeActive, holdWhile(tvOn, HoldCondition{Variable: "isHaveGuests", Value: true},
		HoldCondition{Variable: "isNickHome", Value: false}))
	e.start()
	require.NoError(t, e.sm.SetBool("isNickHome", true))
	require.NoError(t, e.sm.SetBool("isHaveGuests", true))
	e.markDirty("kitchen")
	e.s.TickForTest(true)
	assert.Equal(t, "held: isHaveGuests is true", e.sched().Plan.Reason, "any one condition holds")

	require.NoError(t, e.sm.SetBool("isHaveGuests", false))
	require.NoError(t, e.sm.SetBool("isNickHome", false))
	e.s.TickForTest(true)
	assert.Equal(t, "held: isNickHome is false", e.sched().Plan.Reason, "a false value can hold too")

	require.NoError(t, e.sm.SetBool("isNickHome", true))
	e.s.TickForTest(true)
	assert.Equal(t, []string{"kitchen"}, e.sched().Plan.Rooms)
}

func TestHoldWhileLogOnly(t *testing.T) {
	e := newSchedEnv(t, ModeLogOnly, holdWhile(tvOn))
	e.start()
	require.NoError(t, e.sm.SetBool("isTVPlaying", true))
	e.markDirty("kitchen")
	e.s.TickForTest(true)
	e.advance(30 * time.Minute)
	p := e.sched().Plan
	assert.Equal(t, "held: isTVPlaying is true", p.Reason)
	assert.Nil(t, p.WouldStartAt, "log_only would not have started either")
}
