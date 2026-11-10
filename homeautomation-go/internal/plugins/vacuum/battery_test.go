package vacuum

import (
	"testing"
	"time"

	"homeautomation/internal/housemap"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func itemCostOf(e *schedEnv, room string) float64 {
	e.t.Helper()
	for _, it := range e.sched().Items {
		if it.Room == room && it.Treatment == "full" {
			return it.Cost
		}
	}
	e.t.Fatalf("no item for %s", room)
	return 0
}

func TestSchedulerOwnerCostEditWinsOnceHelperCaughtUp(t *testing.T) {
	e := newSchedEnv(t, ModeLogOnly)
	e.start()
	entity := "input_number.vacuum_cost_office"
	e.s.numOverlay[entity] = 8.2 // learned, Home Assistant not caught up yet
	e.tick()
	assert.Equal(t, 8.2, itemCostOf(e, "office"))

	e.ha.SetState(entity, "8.20", map[string]interface{}{})
	e.tick()
	_, still := e.s.numOverlay[entity]
	assert.False(t, still, "Home Assistant shows the learned value: memory lets go")

	e.ha.SetState(entity, "3.0", map[string]interface{}{})
	e.tick()
	assert.Equal(t, 3.0, itemCostOf(e, "office"), "the owner's edit is used")
}

func TestSchedulerReadOnlyKeepsLearnedCostsInMemory(t *testing.T) {
	e := newSchedEnv(t, ModeLogOnly)
	e.s.readOnly = true
	e.start()
	e.robot.SetPreset("operation_mode", "vacuum_and_mop")
	e.robot.SetStatus("cleaning", "segment")
	for i := 0; i < 6; i++ {
		e.moveTo("office", 99-i)
		e.advance(30 * time.Second)
	}
	e.robot.SetStatus("docked", "none")
	e.moveTo("mud", 90)
	e.advance(30 * time.Second)
	require.Equal(t, []string{"office"}, e.sched().LastRun.Done)
	assert.Empty(t, haWrites(e.ha), "read-only writes nothing")
	assert.Less(t, itemCostOf(e, "office"), 9.6, "the sample still lowers the cost in memory")
	for _, it := range e.sched().Items {
		if it.Room == "office" {
			assert.False(t, it.Dirty)
			require.NotNil(t, it.LastCleaned)
			assert.True(t, it.LastCleaned.After(schedT0))
		}
	}
}

func TestSchedulerMissingTripHelperUsesDefault(t *testing.T) {
	e := newSchedEnv(t, ModeLogOnly)
	e.ha.SetMockState(tripEntity, nil)
	e.start()
	e.tick()
	assert.InDelta(t, 5.0, e.sched().TripCost, 1e-9)
	assert.Contains(t, e.sched().Warnings, "missing helper input_number.vacuum_cost_trip; using default trip cost")
}

func TestSchedulerLoopRunsOnTickerAndWake(t *testing.T) {
	e := newSchedEnv(t, ModeLogOnly, func(cfg *SchedulerConfig, _ *housemap.Map) {
		cfg.TrackInterval = 20 * time.Millisecond
	})
	e.s.manualTicks = false
	require.NoError(t, e.s.Start())
	require.Eventually(t, func() bool {
		o := e.sched()
		return o != nil && o.Robot.Status == "docked"
	}, 5*time.Second, 10*time.Millisecond)
	clearAreas(e.hub, "kitchen") // wakes the loop
	e.s.Stop()
	e.s.Stop() // a second Stop is harmless
}
