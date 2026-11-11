package picos

import (
	"errors"
	"sync"
	"testing"
	"time"

	"homeautomation/internal/ha"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fakeTimers replaces time.AfterFunc. Tests fire timers by hand.
type fakeTimers struct {
	mu     sync.Mutex
	timers []*fakeTimer
}

type fakeTimer struct {
	after   time.Duration
	f       func()
	stopped bool
}

func (t *fakeTimer) Stop() bool {
	wasRunning := !t.stopped
	t.stopped = true
	return wasRunning
}

func (ft *fakeTimers) afterFunc(d time.Duration, f func()) stopper {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	t := &fakeTimer{after: d, f: f}
	ft.timers = append(ft.timers, t)
	return t
}

func (ft *fakeTimers) all() []*fakeTimer {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return append([]*fakeTimer(nil), ft.timers...)
}

func (ft *fakeTimers) last(t *testing.T) *fakeTimer {
	t.Helper()
	all := ft.all()
	require.NotEmpty(t, all, "no timer was started")
	return all[len(all)-1]
}

func newTimedWakeRig(t *testing.T) (*wakeRig, *fakeTimers) {
	t.Helper()
	w := newWakeRig(t)
	timers := &fakeTimers{}
	w.m.afterFunc = timers.afterFunc
	return w, timers
}

func countCalls(calls []ha.ServiceCall, service, entityID string) int {
	n := 0
	for _, c := range calls {
		if c.Domain == "light" && c.Service == service && c.Data["entity_id"] == entityID {
			n++
		}
	}
	return n
}

// User story: the guest walks to the bathroom and back to bed without pressing down.
// The path lights go off on their own after off_after.
func TestGuestPath_OffAfterTurnsOffPathLights(t *testing.T) {
	w, timers := newTimedWakeRig(t)
	w.setLight(northCanA, 70) // already on: never touched
	w.start()

	w.press(guestDoorPico, "on")

	timer := timers.last(t)
	assert.Equal(t, 15*time.Minute, timer.after)
	timer.f()

	w.requireTurnedOff(northCanB)
	assert.Zero(t, countCalls(w.ha.GetServiceCalls(), "turn_off", northCanA), "a can someone else lit stays on")
	assert.Zero(t, countCalls(w.ha.GetServiceCalls(), "turn_off", backHallBottom),
		"off_after only covers the path lights")

	// Firing again does nothing: the lights are forgotten.
	before := len(w.ha.GetServiceCalls())
	timer.f()
	assert.Len(t, w.ha.GetServiceCalls(), before)
}

func TestGuestPath_DownToOffCancelsTimer(t *testing.T) {
	w, timers := newTimedWakeRig(t)
	w.start()

	w.press(guestDoorPico, "on")
	timer := timers.last(t)
	w.settle()
	w.setLight(backHallBottom, 30)
	w.press(guestDoorPico, "off")

	assert.True(t, timer.stopped, "turning the path off by hand cancels the fallback")
	assert.Equal(t, 1, countCalls(w.ha.GetServiceCalls(), "turn_off", northCanA))

	// A stale timer callback still does nothing.
	timer.f()
	assert.Equal(t, 1, countCalls(w.ha.GetServiceCalls(), "turn_off", northCanA))
}

func TestGuestPath_SecondUpPressRestartsTimerAndKeepsMemory(t *testing.T) {
	w, timers := newTimedWakeRig(t)
	w.start()

	w.press(guestDoorPico, "on")
	first := timers.last(t)
	w.setLight(northCanA, pathBrightness)
	w.setLight(northCanB, pathBrightness)

	w.press(guestDoorPico, "on") // brighter back hall; cans already on

	assert.Equal(t, 1, countCalls(w.ha.GetServiceCalls(), "turn_on", northCanA), "a lit can is not sent again")
	assert.True(t, first.stopped, "the earlier timer is replaced")
	first.f() // stale
	assert.Zero(t, countCalls(w.ha.GetServiceCalls(), "turn_off", northCanA))

	timers.last(t).f()
	assert.Equal(t, 1, countCalls(w.ha.GetServiceCalls(), "turn_off", northCanA),
		"the cans are still remembered after the second press")
	assert.Equal(t, 1, countCalls(w.ha.GetServiceCalls(), "turn_off", northCanB))
}

func TestGuestPath_DownPressThatOnlyDimsKeepsPathLights(t *testing.T) {
	w, _ := newTimedWakeRig(t)
	w.start()

	w.press(guestDoorPico, "on")
	w.settle()
	w.setLight(backHallBottom, 80)
	w.press(guestDoorPico, "off") // 80 -> 65, not off

	assert.Zero(t, countCalls(w.ha.GetServiceCalls(), "turn_off", northCanA))
}

func TestGuestPath_NoTimerWhenNothingTurnedOn(t *testing.T) {
	w, timers := newTimedWakeRig(t)
	w.setLight(northCanA, 70)
	w.setLight(northCanB, 70)
	w.start()

	w.press(guestDoorPico, "on")

	assert.Empty(t, timers.all(), "nothing to turn off, so no timer")
}

func TestGuestPath_StopCancelsTimer(t *testing.T) {
	w, timers := newTimedWakeRig(t)
	w.start()
	w.press(guestDoorPico, "on")
	timer := timers.last(t)

	w.m.Stop()

	assert.True(t, timer.stopped)
	timer.f()
	assert.Zero(t, countCalls(w.ha.GetServiceCalls(), "turn_off", northCanA), "a stopped plugin sends nothing")
}

func TestGuestPath_FailedTurnOnIsNotRemembered(t *testing.T) {
	w, timers := newTimedWakeRig(t)
	w.start()
	w.ha.SetServiceFailCount("light", "turn_on", 1, errors.New("boom")) // the back hall step fails
	w.press(guestDoorPico, "on")
	assert.Empty(t, timers.all(), "a failed step does not light the path")

	w.ha.SetServiceError("light", "turn_on", errors.New("boom"))
	w.settle()
	w.press(guestDoorPico, "on")
	assert.Empty(t, timers.all())
}

func TestNoStateManager_NoEarlyWakeNoPathLights(t *testing.T) {
	mock := ha.NewMockClient()
	r := &rig{t: t, ha: mock, time: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)}
	r.m = NewManager(t.Context(), mock, earlyWakeTestConfig(), zap.NewNop(), false)
	r.m.now = func() time.Time { return r.time }
	for _, l := range []string{kitchenZone, kitchenUnderCab, backHallBottom, northCanA, northCanB} {
		r.setLight(l, 0)
	}
	r.start()

	r.press(kitchenPico, "on")
	r.press(guestDoorPico, "on")

	r.requireTurnedOn(kitchenZone, 30)
	r.requireTurnedOn(backHallBottom, 30)
	_, touched := r.lastCall(northCanA)
	assert.False(t, touched, "without house state there are no path lights")
}

func TestReadOnly_NoEarlyWakeNoPathLights(t *testing.T) {
	w := newWakeRig(t)
	w.m.readOnly = true
	w.start()

	w.press(kitchenPico, "on")
	w.press(guestDoorPico, "on")

	for _, c := range w.ha.GetServiceCalls() {
		assert.NotEqual(t, "light", c.Domain, "read-only mode sends no light command: %+v", c)
	}
	assert.False(t, w.earlyWake())
	assert.NotEmpty(t, w.m.GetShadowState().Outputs.Targets["kitchen"].EarlyWake)
}

func TestEarlyWake_ShadowRecordsDecisions(t *testing.T) {
	w := newWakeRig(t)
	w.start()

	w.press(kitchenPico, "on")
	w.press(guestDoorPico, "on")

	targets := w.m.GetShadowState().Outputs.Targets
	assert.Equal(t, "early wake started", targets["kitchen"].EarlyWake)
	assert.Contains(t, targets["back hall"].PathLights, "turned on")
	assert.Empty(t, targets["back hall"].EarlyWake, "the back hall never wakes the house")
}

func TestEarlyWake_AlreadyOnIsLeftAlone(t *testing.T) {
	w := newWakeRig(t)
	require.NoError(t, w.sm.SetBool("isEarlyWakeActive", true))
	w.start()

	w.press(livingPico, "on")

	assert.Equal(t, "early wake already on", w.m.GetShadowState().Outputs.Targets["living"].EarlyWake)
}

func TestPathLightsValidate(t *testing.T) {
	target := Target{Name: "back hall", Lights: []string{backHallBottom}}
	valid := func() *PathLights {
		return &PathLights{Lights: []string{northCanA}, BrightnessPct: 5, Kelvin: 2200, OffAfter: time.Minute}
	}
	tests := []struct {
		name    string
		mutate  func(p *PathLights)
		wantErr string
	}{
		{"valid", func(p *PathLights) {}, ""},
		{"no lights", func(p *PathLights) { p.Lights = nil }, "at least one light"},
		{"not a light", func(p *PathLights) { p.Lights = []string{"switch.x"} }, "not a light entity ID"},
		{"duplicate", func(p *PathLights) { p.Lights = []string{northCanA, northCanA} }, "duplicate"},
		{"overlaps target", func(p *PathLights) { p.Lights = []string{backHallBottom} }, "also a light of target"},
		{"brightness zero", func(p *PathLights) { p.BrightnessPct = 0 }, "brightness_pct"},
		{"brightness over 100", func(p *PathLights) { p.BrightnessPct = 101 }, "brightness_pct"},
		{"kelvin too low", func(p *PathLights) { p.Kelvin = 500 }, "kelvin"},
		{"kelvin too high", func(p *PathLights) { p.Kelvin = 20000 }, "kelvin"},
		{"no off_after", func(p *PathLights) { p.OffAfter = 0 }, "off_after"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := valid()
			tt.mutate(p)
			err := p.validate(target)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestConfigValidate_ReportsPathLightButton(t *testing.T) {
	cfg := earlyWakeTestConfig()
	cfg.Buttons[3].PathLights.BrightnessPct = 0
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), guestDoorButton)
	assert.Contains(t, err.Error(), "path_lights")
}
