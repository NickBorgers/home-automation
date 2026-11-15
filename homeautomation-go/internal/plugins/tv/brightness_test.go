package tv

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"homeautomation/internal/bravia"
	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type fakePicture struct {
	mu      sync.Mutex
	setting bravia.Setting
	getErr  error
	setErr  error
	sets    []int
}

func (f *fakePicture) GetBrightness(context.Context) (bravia.Setting, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.setting, f.getErr
}

func (f *fakePicture) SetBrightness(_ context.Context, v int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil {
		return f.setErr
	}
	f.sets = append(f.sets, v)
	f.setting.Current = v
	return nil
}

func TestBrightnessFraction(t *testing.T) {
	tests := []struct {
		lux  float64
		want float64
	}{
		{0.5, 0},
		{BrightnessDarkLux, 0},
		{20, 0.508},
		{BrightnessBrightLux, 1},
		{1000, 1},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.lux), func(t *testing.T) {
			assert.InDelta(t, tt.want, brightnessFraction(tt.lux), 0.001)
		})
	}
}

// newBrightnessManager returns a manager with the TV on, a target lux, and a sync box reading.
func newBrightnessManager(t *testing.T, readOnly bool, lux float64, pic PictureClient) (*Manager, *ha.MockClient) {
	t.Helper()
	mockHA := ha.NewMockClient()
	stateMgr := state.NewManager(mockHA, zap.NewNop(), false)
	require.NoError(t, stateMgr.SetNumber("tvRoomTargetLux", lux))
	mockHA.SetState(SyncBoxBrightnessEntity, "46", nil)
	m := NewManager(context.Background(), mockHA, stateMgr, zap.NewNop(), readOnly, nil)
	m.tvRemoteOn = true
	if pic != nil {
		m.SetPictureClient(pic)
	}
	return m, mockHA
}

func syncBoxSets(mockHA *ha.MockClient) []interface{} {
	var vals []interface{}
	for _, c := range mockHA.GetServiceCalls() {
		if c.Domain == "number" && c.Service == "set_value" {
			vals = append(vals, c.Data["value"])
		}
	}
	return vals
}

func TestApplyBrightness(t *testing.T) {
	tests := []struct {
		name          string
		readOnly      bool
		tvOff         bool
		lux           float64
		pic           *fakePicture
		syncErr       error
		wantDecision  string
		wantTV        string
		wantSync      string
		wantTVSets    []int
		wantSyncCalls int
	}{
		{
			name: "day target sets both", lux: 150,
			pic:          &fakePicture{setting: bravia.Setting{Current: 25, Max: 50}},
			wantDecision: "applied", wantTV: "set 50", wantSync: "set 100",
			wantTVSets: []int{50}, wantSyncCalls: 1,
		},
		{
			name: "no Bravia credentials: sync box only", lux: 2.5,
			wantDecision: "applied", wantTV: "not configured (BRAVIA_IP or BRAVIA_PSK unset)", wantSync: "set 20",
			wantSyncCalls: 1,
		},
		{
			name: "read-only sends nothing", readOnly: true, lux: 150,
			pic:          &fakePicture{setting: bravia.Setting{Current: 25, Max: 50}},
			wantDecision: "applied", wantTV: "would set 50 (read-only)", wantSync: "would set 100 (read-only)",
		},
		{
			name: "screen still starting", lux: 150,
			pic:          &fakePicture{getErr: fmt.Errorf("wrapped: %w", bravia.ErrTurnedOff)},
			wantDecision: "applied", wantTV: "TV screen not ready", wantSync: "set 100", wantSyncCalls: 1,
		},
		{
			name: "TV set fails", lux: 150,
			pic:          &fakePicture{setting: bravia.Setting{Current: 25, Max: 50}, setErr: errors.New("boom")},
			wantDecision: "applied", wantTV: "send failed: boom", wantSync: "set 100", wantSyncCalls: 1,
		},
		{
			name: "sync box set fails", lux: 150, syncErr: errors.New("nope"),
			wantDecision: "applied", wantTV: "not configured (BRAVIA_IP or BRAVIA_PSK unset)", wantSync: "send failed: nope",
		},
		{
			name: "already at goal", lux: 20,
			pic:          &fakePicture{setting: bravia.Setting{Current: 33, Max: 50}},
			wantDecision: "applied", wantTV: "no change", wantSync: "set 61", wantSyncCalls: 1,
		},
		{name: "TV off", tvOff: true, lux: 150, wantDecision: "TV off"},
		{name: "no target yet", lux: 0, wantDecision: "no target lux yet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pic PictureClient
			if tt.pic != nil {
				pic = tt.pic
			}
			m, mockHA := newBrightnessManager(t, tt.readOnly, tt.lux, pic)
			if tt.syncErr != nil {
				mockHA.SetServiceError("number", "set_value", tt.syncErr)
			}
			m.tvRemoteOn = !tt.tvOff

			m.applyBrightness("test")

			got := m.GetShadowState().Outputs.Brightness
			require.NotNil(t, got)
			assert.Equal(t, tt.wantDecision, got.Decision)
			assert.Equal(t, tt.wantTV, got.TV.Decision)
			assert.Equal(t, tt.wantSync, got.SyncBox.Decision)
			if tt.pic != nil {
				assert.Equal(t, tt.wantTVSets, tt.pic.sets)
			}
			assert.Len(t, syncBoxSets(mockHA), tt.wantSyncCalls)
		})
	}
}

func TestBrightnessOnPowerOn_StaleGoroutineDoesNotClearHold(t *testing.T) {
	// GIVEN: G1 goroutine launched, then TV turns on again (G2 launched, superseding G1).
	// Between G1 and G2 waking, a manual change sets the hold.
	// THEN: G1 must exit without touching state; G2 clears the hold as expected.
	pic := &fakePicture{setting: bravia.Setting{Current: 25, Max: 50}}
	m, _ := newBrightnessManager(t, false, 150, pic)

	// Use a channel-controlled sleep so we can order goroutine wakeups precisely. The two
	// goroutines may reach sleep in either order, so each power-on uses a distinct delay and
	// the gate is keyed by that delay. That identifies which goroutine owns which gate.
	const g1Delay, g2Delay = time.Second, 2 * time.Second
	var mu sync.Mutex
	sleepGates := map[time.Duration]chan struct{}{}
	m.sleepFunc = func(d time.Duration) {
		ch := make(chan struct{})
		mu.Lock()
		sleepGates[d] = ch
		mu.Unlock()
		<-ch
	}

	// Apply once to establish dev.last=50.
	m.applyBrightness("first")
	require.Equal(t, []int{50}, pic.sets)

	// TV on twice quickly: G1 (gen=1) then G2 (gen=2).
	m.SetBrightnessPowerOnDelay(g1Delay)
	m.brightnessOnPowerOn() // G1
	m.SetBrightnessPowerOnDelay(g2Delay)
	m.brightnessOnPowerOn() // G2

	// A person changes brightness: manual hold detected.
	pic.mu.Lock()
	pic.setting.Current = 40
	pic.mu.Unlock()
	m.applyBrightness("manual_detected")
	require.True(t, m.GetShadowState().Outputs.Brightness.TV.Held, "must be held before G1 fires")

	// Wait for both goroutines to be blocked in sleep.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(sleepGates) == 2
	}, 2*time.Second, 5*time.Millisecond, "both goroutines must reach sleep")

	// Wake G1 — it must detect it is superseded and not clear the hold.
	mu.Lock()
	g1 := sleepGates[g1Delay]
	mu.Unlock()
	close(g1)
	// Give G1 time to run to completion.
	time.Sleep(100 * time.Millisecond)
	require.True(t, m.GetShadowState().Outputs.Brightness.TV.Held, "G1 must not clear the manual hold")

	// Wake G2 — it is the authoritative power-on; hold clears and brightness is set.
	mu.Lock()
	g2 := sleepGates[g2Delay]
	mu.Unlock()
	close(g2)
	require.Eventually(t, func() bool {
		pic.mu.Lock()
		defer pic.mu.Unlock()
		return len(pic.sets) == 2 && pic.sets[1] == 50
	}, 2*time.Second, 10*time.Millisecond, "G2 must set brightness after clearing hold")
}

func TestApplyBrightness_ManualChangeHoldsUntilPowerOn(t *testing.T) {
	pic := &fakePicture{setting: bravia.Setting{Current: 25, Max: 50}}
	m, mockHA := newBrightnessManager(t, false, 150, pic)
	m.SetBrightnessPowerOnDelay(0)

	m.applyBrightness("first")
	require.Equal(t, []int{50}, pic.sets)
	mockHA.SetState(SyncBoxBrightnessEntity, "100", nil) // Home Assistant reports the value it was sent

	// A person turns the TV down with the remote; the target falls.
	pic.mu.Lock()
	pic.setting.Current = 40
	pic.mu.Unlock()
	require.NoError(t, m.stateManager.SetNumber("tvRoomTargetLux", 2.5))
	m.applyBrightness("second")

	got := m.GetShadowState().Outputs.Brightness
	assert.True(t, got.TV.Held)
	assert.Equal(t, "found 40, last set 50", got.TV.HeldReason)
	assert.Equal(t, []int{50}, pic.sets, "no write while held")
	assert.Equal(t, "set 20", got.SyncBox.Decision, "the sync box is held separately")

	// The TV turns off and on: the hold is cleared and the night value is sent.
	m.handleTVRemoteChange(TVRemoteEntity, nil, &ha.State{State: "standby"})
	m.handleTVRemoteChange(TVRemoteEntity, nil, &ha.State{State: "on"})
	require.Eventually(t, func() bool {
		pic.mu.Lock()
		defer pic.mu.Unlock()
		return len(pic.sets) == 2 && pic.sets[1] == 15
	}, 2*time.Second, 10*time.Millisecond)
}
