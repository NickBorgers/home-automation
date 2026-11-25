package roompresence

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestOffDelay_OnIsImmediate(t *testing.T) {
	d := newOffDelay(30 * time.Second)
	assert.Equal(t, map[string]bool{"isAOccupied": false}, d.apply(at(0), map[string]bool{"isAOccupied": false}))
	assert.Equal(t, map[string]bool{"isAOccupied": true}, d.apply(at(1), map[string]bool{"isAOccupied": true}))
	assert.Nil(t, d.pending())
}

func TestOffDelay_ShortOffIsSuppressed(t *testing.T) {
	d := newOffDelay(30 * time.Second)
	d.apply(at(0), map[string]bool{"isAOccupied": true})

	assert.True(t, d.apply(at(1), map[string]bool{"isAOccupied": false})["isAOccupied"])
	assert.Equal(t, map[string]time.Time{"isAOccupied": at(31)}, d.pending())
	assert.True(t, d.apply(at(20), map[string]bool{"isAOccupied": false})["isAOccupied"])
	assert.Equal(t, map[string]time.Time{"isAOccupied": at(31)}, d.pending(), "the deadline counts from the first off")

	// On again during the pending off cancels it.
	assert.True(t, d.apply(at(25), map[string]bool{"isAOccupied": true})["isAOccupied"])
	assert.Nil(t, d.pending())

	// The next off waits the whole delay again.
	assert.True(t, d.apply(at(40), map[string]bool{"isAOccupied": false})["isAOccupied"])
	assert.True(t, d.apply(at(69), map[string]bool{"isAOccupied": false})["isAOccupied"])
	assert.False(t, d.apply(at(70), map[string]bool{"isAOccupied": false})["isAOccupied"])
	assert.Nil(t, d.pending())
}

func TestOffDelay_ZeroPassesEveryValue(t *testing.T) {
	d := newOffDelay(0)
	for i, v := range []bool{true, false, true, false} {
		assert.Equal(t, v, d.apply(at(float64(i)), map[string]bool{"isAOccupied": v})["isAOccupied"])
		assert.Nil(t, d.pending())
	}
	d.hold(at(5), "isAOccupied")
	assert.False(t, d.apply(at(5), map[string]bool{"isAOccupied": false})["isAOccupied"], "hold does nothing at 0")
}

func TestOffDelay_OutputsAreIndependent(t *testing.T) {
	d := newOffDelay(30 * time.Second)
	d.apply(at(0), map[string]bool{"isAOccupied": true, "isBOccupied": false})
	got := d.apply(at(1), map[string]bool{"isAOccupied": false, "isBOccupied": true})
	assert.Equal(t, map[string]bool{"isAOccupied": true, "isBOccupied": true}, got)
	got = d.apply(at(31), map[string]bool{"isAOccupied": false, "isBOccupied": true})
	assert.Equal(t, map[string]bool{"isAOccupied": false, "isBOccupied": true}, got)
}

func TestOffDelay_HoldStartsAPendingOff(t *testing.T) {
	d := newOffDelay(30 * time.Second)
	d.hold(at(0), "isAOccupied")
	assert.True(t, d.apply(at(1), map[string]bool{"isAOccupied": false})["isAOccupied"])
	assert.Equal(t, map[string]time.Time{"isAOccupied": at(30)}, d.pending())
	assert.False(t, d.apply(at(30), map[string]bool{"isAOccupied": false})["isAOccupied"])
}

func TestConfig_OutputOffDelay(t *testing.T) {
	cfg, err := ParseConfig([]byte("room_presence:\n  areas: {a: {}}\n"))
	require.NoError(t, err)
	assert.Zero(t, cfg.OutputOffDelay, "off by default")

	cfg, err = ParseConfig([]byte("room_presence:\n  output_off_delay: 30s\n  areas: {a: {}}\n"))
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, cfg.OutputOffDelay)

	_, err = ParseConfig([]byte("room_presence:\n  output_off_delay: -1s\n  areas: {a: {}}\n"))
	assert.ErrorContains(t, err, "output_off_delay must not be negative")
}

const offDelayConfig = `room_presence:
  output_off_delay: 30s
  areas:
    a: {seen: true, neighbors: [b]}
    b: {seen: true}
  outputs:
    isAOccupied: [a]
`

// offDelayManager is a log_only manager for two neighbor areas a and b, with a clock the test sets.
type offDelayManager struct {
	t     *testing.T
	m     *Manager
	clock time.Time
	logs  *observer.ObservedLogs
}

func newOffDelayManager(t *testing.T, delay time.Duration) *offDelayManager {
	t.Helper()
	cfg, err := ParseConfig([]byte(offDelayConfig))
	require.NoError(t, err)
	cfg.OutputOffDelay = delay
	mockHA := ha.NewMockClient()
	sm := state.NewManager(mockHA, zap.NewNop(), false)
	core, logs := observer.New(zap.InfoLevel)
	m := NewManager(context.Background(), mockHA, sm, cfg, "", zap.New(core), false, nil)
	o := &offDelayManager{t: t, m: m, clock: t0, logs: logs}
	m.now = func() time.Time { return o.clock }
	return o
}

// at sets the clock to sec seconds after t0.
func (o *offDelayManager) at(sec float64) *offDelayManager {
	o.clock = at(sec)
	return o
}

// in sends an update for one track that has entered both areas and is now in area.
func (o *offDelayManager) in(area string) {
	o.m.handleMQTT(frigateEvent(o.t, "update", "p", "cam", []string{"a", "b"}, area))
}

func (o *offDelayManager) tick() { o.m.apply(func(t *Tracker, now time.Time) { t.Tick(now) }) }

func (o *offDelayManager) output() bool {
	return o.m.GetShadowState().Outputs.Outputs["isAOccupied"]
}

func (o *offDelayManager) changes() []bool {
	var out []bool
	for _, e := range o.logs.FilterMessage("Room presence output changed").All() {
		out = append(out, e.ContextMap()["value"].(bool))
	}
	return out
}

func TestManager_OffDelayEndsOnTickWithoutEvents(t *testing.T) {
	o := newOffDelayManager(t, 30*time.Second)
	o.at(0).in("a")
	require.True(t, o.m.Occupied("a"))
	assert.True(t, o.output(), "on is immediate")

	o.at(1).in("b")
	require.False(t, o.m.Occupied("a"), "area occupancy is not delayed")
	assert.True(t, o.output())
	assert.Equal(t, map[string]time.Time{"isAOccupied": at(31)}, o.m.GetShadowState().Outputs.PendingOff)

	// No Frigate event from here on: only ticks.
	o.at(30.9).tick()
	assert.True(t, o.output())
	o.at(31.4).tick()
	assert.False(t, o.output(), "the first tick after the deadline ends the pending off")
	assert.Nil(t, o.m.GetShadowState().Outputs.PendingOff)
	assert.Equal(t, []bool{true, false}, o.changes())
}

func TestManager_OffDelayShortGapIsSuppressed(t *testing.T) {
	o := newOffDelayManager(t, 30*time.Second)
	o.at(0).in("a")
	for sec := 1.0; sec < 60; sec += 2 { // back and forth across the border, a second in each area
		o.at(sec).in("b")
		o.at(sec + 0.5).tick()
		assert.True(t, o.output())
		o.at(sec + 1).in("a")
		assert.Nil(t, o.m.GetShadowState().Outputs.PendingOff, "on cancels the pending off")
	}
	assert.Equal(t, []bool{true}, o.changes())
}

func TestManager_OffDelayZeroKeepsOldBehaviour(t *testing.T) {
	o := newOffDelayManager(t, 0)
	o.at(0).in("a")
	o.at(1).in("b")
	assert.False(t, o.output())
	assert.Nil(t, o.m.GetShadowState().Outputs.PendingOff)
	o.at(2).in("a")
	assert.Equal(t, []bool{true, false, true}, o.changes())
}

func TestManager_OffDelayActiveWritesOffAfterDelay(t *testing.T) {
	cfg := exampleConfig(t)
	cfg.Mode = ModeActive
	cfg.OutputOffDelay = 30 * time.Second
	m, _, sm := newTestManager(t, cfg, false)
	clock := t0
	m.now = func() time.Time { return clock }
	value := func() bool {
		v, err := sm.GetBool("isKitchenOccupied")
		require.NoError(t, err)
		return v
	}

	m.handleMQTT(frigateEvent(t, "update", "p", "cam_kitchen", []string{"kitchen_to_hall", "kitchen"}, "kitchen"))
	require.True(t, m.Occupied("kitchen"))
	assert.True(t, value(), "on is written at once")

	clock = at(1)
	m.handleMQTT(frigateEvent(t, "update", "p", "cam_kitchen", []string{"kitchen_to_hall", "kitchen", "lounge"}, "lounge"))
	require.False(t, m.Occupied("kitchen"))
	assert.True(t, value())

	clock = at(29)
	m.apply(func(t *Tracker, now time.Time) { t.Tick(now) })
	assert.True(t, value())
	clock = at(31)
	m.apply(func(t *Tracker, now time.Time) { t.Tick(now) })
	assert.False(t, value(), "off is written after the delay")
}

func TestManager_OffDelayHoldsAnOutputThatWasOnAtStartup(t *testing.T) {
	cfg := exampleConfig(t)
	cfg.Mode = ModeActive
	cfg.OutputOffDelay = 30 * time.Second
	m, mockHA, sm := newTestManager(t, cfg, false)
	mockHA.SetState("input_boolean.kitchen_occupied", "on", nil)
	require.NoError(t, sm.SyncFromHA())
	clock := t0
	m.now = func() time.Time { return clock }

	require.NoError(t, m.Start())
	defer m.Stop()
	assert.True(t, m.GetShadowState().Outputs.Outputs["isKitchenOccupied"])
	assert.Equal(t, map[string]time.Time{"isKitchenOccupied": at(30)}, m.GetShadowState().Outputs.PendingOff)

	// The person is found again within the delay: the output never turns off.
	clock = at(5)
	m.handleMQTT(frigateEvent(t, "update", "p", "cam_kitchen", []string{"kitchen_to_hall", "kitchen"}, "kitchen"))
	require.True(t, m.Occupied("kitchen"))
	clock = at(40)
	m.apply(func(t *Tracker, now time.Time) { t.Tick(now) })
	v, err := sm.GetBool("isKitchenOccupied")
	require.NoError(t, err)
	assert.True(t, v)
	assert.Nil(t, m.GetShadowState().Outputs.PendingOff)
}

func TestManager_OffDelayLogOnlyIgnoresHelperAtStartup(t *testing.T) {
	cfg := exampleConfig(t)
	cfg.OutputOffDelay = 30 * time.Second
	m, mockHA, sm := newTestManager(t, cfg, false)
	mockHA.SetState("input_boolean.kitchen_occupied", "on", nil)
	require.NoError(t, sm.SyncFromHA())

	require.NoError(t, m.Start())
	defer m.Stop()
	assert.False(t, m.GetShadowState().Outputs.Outputs["isKitchenOccupied"])
	assert.Nil(t, m.GetShadowState().Outputs.PendingOff)
}
