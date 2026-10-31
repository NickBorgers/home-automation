package picos

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"homeautomation/internal/ha"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const (
	pantryDoorPico    = "pico-pantry-door"
	pantryInsidePico  = "pico-pantry-inside"
	kitchenPico       = "pico-kitchen"
	pantryZone        = "light.pantry_area"
	pantryUnderCab    = "light.pantry_under_cabinet"
	kitchenZone       = "light.kitchen_cans"
	kitchenUnderCab   = "light.kitchen_main_lights"
	unconfiguredPico  = "pico-somewhere-else"
	pantryDoorButton  = "Pantry Primary Suite Door"
	kitchenPicoButton = "Pantry Kitchen Lights"
)

func testConfig() *Config {
	cfg := &Config{
		Targets: []Target{
			{Name: "pantry", Lights: []string{pantryZone, pantryUnderCab}},
			{Name: "kitchen", Lights: []string{kitchenZone, kitchenUnderCab}},
		},
		Buttons: []Button{
			{Name: pantryDoorButton, DeviceID: pantryDoorPico, Target: "pantry"},
			{Name: "Pantry Pantry Lights", DeviceID: pantryInsidePico, Target: "pantry"},
			{Name: kitchenPicoButton, DeviceID: kitchenPico, Target: "kitchen"},
		},
	}
	cfg.applyDefaults()
	return cfg
}

// rig is a Manager wired to a mock Home Assistant and a hand-set clock.
type rig struct {
	t    *testing.T
	ha   *ha.MockClient
	m    *Manager
	time time.Time
}

func newRig(t *testing.T, readOnly bool) *rig {
	t.Helper()
	mock := ha.NewMockClient()
	r := &rig{t: t, ha: mock, time: time.Date(2026, 10, 31, 19, 0, 0, 0, time.UTC)}
	r.m = NewManager(context.Background(), mock, testConfig(), zap.NewNop(), readOnly)
	r.m.now = func() time.Time { return r.time }
	return r
}

func (r *rig) start() {
	r.t.Helper()
	require.NoError(r.t, r.m.Start())
	r.t.Cleanup(r.m.Stop)
}

// setLight sets what Home Assistant reports for a light. A pct of 0 means off.
func (r *rig) setLight(entityID string, pct int) {
	if pct == 0 {
		r.ha.SetState(entityID, "off", map[string]interface{}{})
		return
	}
	r.ha.SetState(entityID, "on", map[string]interface{}{"brightness": float64(pct) * 255 / 100})
}

func (r *rig) press(device, buttonType string) {
	r.ha.SimulateEvent(buttonEventType, map[string]interface{}{
		"device_id":   device,
		"device_name": "test pico",
		"button_type": buttonType,
		"action":      "press",
	})
	r.ha.SimulateEvent(buttonEventType, map[string]interface{}{
		"device_id":   device,
		"button_type": buttonType,
		"action":      "release",
	})
}

// settle moves the clock past the window in which a press trusts its own last command.
func (r *rig) settle() {
	r.time = r.time.Add(time.Duration(r.m.cfg.RecentSeconds+1) * time.Second)
}

// lastCall returns the service call made to a light since the rig started, and whether there was one.
func (r *rig) lastCall(entityID string) (ha.ServiceCall, bool) {
	calls := r.ha.GetServiceCalls()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Domain == "light" && calls[i].Data["entity_id"] == entityID {
			return calls[i], true
		}
	}
	return ha.ServiceCall{}, false
}

func (r *rig) requireTurnedOn(entityID string, pct int) {
	r.t.Helper()
	call, ok := r.lastCall(entityID)
	require.True(r.t, ok, "no command for %s", entityID)
	assert.Equal(r.t, "turn_on", call.Service, entityID)
	assert.Equal(r.t, pct, call.Data["brightness_pct"], entityID)
}

func (r *rig) requireTurnedOff(entityID string) {
	r.t.Helper()
	call, ok := r.lastCall(entityID)
	require.True(r.t, ok, "no command for %s", entityID)
	assert.Equal(r.t, "turn_off", call.Service, entityID)
}

func TestNextLevel(t *testing.T) {
	t.Parallel()
	m := &Manager{cfg: testConfig()}

	tests := []struct {
		name      string
		from      int
		direction string
		wantPct   int
		wantOff   bool
	}{
		{"up from off lands on on_pct", 0, directionUp, 30, false},
		{"up steps by step_pct", 30, directionUp, 45, false},
		{"up caps at 100", 95, directionUp, 100, false},
		{"up at 100 stays at 100", 100, directionUp, 100, false},
		{"down from bright steps down", 100, directionDown, 85, false},
		{"down above the threshold steps down", 40, directionDown, 25, false},
		{"down just above the threshold steps down", 31, directionDown, 16, false},
		{"down at the threshold turns off", 30, directionDown, 0, true},
		{"down below the threshold turns off", 20, directionDown, 0, true},
		{"down when already off stays off", 0, directionDown, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pct, off := m.nextLevel(tt.from, tt.direction)
			assert.Equal(t, tt.wantPct, pct)
			assert.Equal(t, tt.wantOff, off)
		})
	}
}

func TestNextLevel_StepNeverDropsBelowOne(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.StepPct = 90
	m := &Manager{cfg: cfg}

	pct, off := m.nextLevel(50, directionDown)
	assert.Equal(t, 1, pct)
	assert.False(t, off)
}

// User story: walking into the pantry from the primary suite, I press up on the Pico by the door.
func TestPress_UpFromOffTurnsOnPantryLightsAt30(t *testing.T) {
	r := newRig(t, false)
	r.setLight(pantryZone, 0)
	r.setLight(pantryUnderCab, 0)
	r.start()

	r.press(pantryDoorPico, "on")

	r.requireTurnedOn(pantryZone, 30)
	r.requireTurnedOn(pantryUnderCab, 30)
	_, touchedKitchen := r.lastCall(kitchenZone)
	assert.False(t, touchedKitchen, "a pantry Pico must not touch the kitchen")
}

func TestPress_SecondPantryPicoDoesTheSame(t *testing.T) {
	r := newRig(t, false)
	r.setLight(pantryZone, 0)
	r.setLight(pantryUnderCab, 0)
	r.start()

	r.press(pantryInsidePico, "on")

	r.requireTurnedOn(pantryZone, 30)
	r.requireTurnedOn(pantryUnderCab, 30)
}

func TestPress_KitchenPicoDrivesKitchenIncludingUnderCabinet(t *testing.T) {
	r := newRig(t, false)
	r.setLight(kitchenZone, 0)
	r.setLight(kitchenUnderCab, 0)
	r.start()

	r.press(kitchenPico, "on")

	r.requireTurnedOn(kitchenZone, 30)
	r.requireTurnedOn(kitchenUnderCab, 30)
	_, touchedPantry := r.lastCall(pantryZone)
	assert.False(t, touchedPantry, "the kitchen Pico must not touch the pantry")
}

func TestPress_StepsUpThenDownThenOff(t *testing.T) {
	r := newRig(t, false)
	r.setLight(pantryZone, 0)
	r.setLight(pantryUnderCab, 0)
	r.start()

	// GIVEN lights that are off. WHEN up is pressed repeatedly, THEN each press climbs one step.
	for _, want := range []int{30, 45, 60, 75, 90, 100, 100} {
		r.press(pantryDoorPico, "on")
		r.requireTurnedOn(pantryZone, want)
		r.requireTurnedOn(pantryUnderCab, want)
	}

	// WHEN down is pressed repeatedly, THEN each press drops one step, and the last turns them off.
	for _, want := range []int{85, 70, 55, 40, 25} {
		r.press(pantryDoorPico, "off")
		r.requireTurnedOn(pantryZone, want)
		r.requireTurnedOn(pantryUnderCab, want)
	}
	r.press(pantryDoorPico, "off")
	r.requireTurnedOff(pantryZone)
	r.requireTurnedOff(pantryUnderCab)
}

func TestPress_BothPantryPicosShareOneLevel(t *testing.T) {
	r := newRig(t, false)
	r.setLight(pantryZone, 0)
	r.setLight(pantryUnderCab, 0)
	r.start()

	r.press(pantryDoorPico, "on")
	r.press(pantryInsidePico, "on")

	r.requireTurnedOn(pantryZone, 45)
}

func TestPress_StartsFromReportedLevelWhenNoRecentPress(t *testing.T) {
	r := newRig(t, false)
	r.setLight(pantryZone, 60)
	r.setLight(pantryUnderCab, 100)
	r.start()

	r.press(pantryDoorPico, "on")

	// The first light is the reference. The under-cabinet light follows it.
	r.requireTurnedOn(pantryZone, 75)
	r.requireTurnedOn(pantryUnderCab, 75)
}

func TestPress_RecentCommandBeatsLaggingReport(t *testing.T) {
	r := newRig(t, false)
	r.setLight(pantryZone, 0)
	r.setLight(pantryUnderCab, 0)
	r.start()

	// The mock never updates the light, like a Hue bridge that has not reported yet.
	r.press(pantryDoorPico, "on")
	r.time = r.time.Add(time.Second)
	r.press(pantryDoorPico, "on")
	r.requireTurnedOn(pantryZone, 45)

	// After the window, the light's own report wins again.
	r.settle()
	r.press(pantryDoorPico, "on")
	r.requireTurnedOn(pantryZone, 30)
}

func TestPress_ManualChangeAfterPressIsRespected(t *testing.T) {
	r := newRig(t, false)
	r.setLight(pantryZone, 0)
	r.setLight(pantryUnderCab, 0)
	r.start()

	r.press(pantryDoorPico, "on")
	r.settle()
	// Someone sets the lights to 80% in the Hue app.
	r.setLight(pantryZone, 80)

	r.press(pantryDoorPico, "off")

	r.requireTurnedOn(pantryZone, 65)
}

func TestPress_DownTurnsOffEvenWhenOnlyUnderCabinetIsOn(t *testing.T) {
	r := newRig(t, false)
	r.setLight(pantryZone, 0)
	r.setLight(pantryUnderCab, 100)
	r.start()

	r.press(pantryDoorPico, "off")

	r.requireTurnedOff(pantryZone)
	r.requireTurnedOff(pantryUnderCab)
}

func TestPress_SendsTransition(t *testing.T) {
	r := newRig(t, false)
	r.setLight(pantryZone, 0)
	r.start()

	r.press(pantryDoorPico, "on")

	call, ok := r.lastCall(pantryZone)
	require.True(t, ok)
	assert.Equal(t, 0.5, call.Data["transition"])
}

func TestEvents_ThatMustBeIgnored(t *testing.T) {
	tests := []struct {
		name  string
		event map[string]interface{}
	}{
		{"release only", map[string]interface{}{"device_id": pantryDoorPico, "button_type": "on", "action": "release"}},
		{"unconfigured device", map[string]interface{}{"device_id": unconfiguredPico, "button_type": "on", "action": "press"}},
		{"unsupported button", map[string]interface{}{"device_id": pantryDoorPico, "button_type": "stop", "action": "press"}},
		{"no fields", map[string]interface{}{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, false)
			r.setLight(pantryZone, 0)
			r.start()

			r.ha.SimulateEvent(buttonEventType, tt.event)

			assert.Zero(t, r.ha.ServiceCallCount())
		})
	}
}

func TestReadOnly_LogsButSendsNothing(t *testing.T) {
	r := newRig(t, true)
	r.setLight(pantryZone, 0)
	r.start()

	r.press(pantryDoorPico, "on")

	assert.Zero(t, r.ha.ServiceCallCount())
	press := r.m.GetShadowState().Outputs.Targets["pantry"]
	assert.True(t, press.ReadOnly)
	assert.Equal(t, 30, press.ToPct)
}

func TestPress_ReferenceLightUnreadableDoesNothing(t *testing.T) {
	r := newRig(t, false)
	r.start() // no state set for any light: the mock's GetState returns an error

	r.press(pantryDoorPico, "on")

	assert.Zero(t, r.ha.ServiceCallCount())
	press := r.m.GetShadowState().Outputs.Targets["pantry"]
	assert.NotEmpty(t, press.Error)
}

func TestPress_FailedCommandIsRecordedAndNotRemembered(t *testing.T) {
	r := newRig(t, false)
	r.setLight(pantryZone, 0)
	r.setLight(pantryUnderCab, 0)
	r.ha.SetServiceError("light", "turn_on", errors.New("bridge down"))
	r.start()

	r.press(pantryDoorPico, "on")

	press := r.m.GetShadowState().Outputs.Targets["pantry"]
	assert.Contains(t, press.Error, "bridge down")

	// The failed press must not make the next press step from a level no light reached.
	r.ha.SetServiceError("light", "turn_on", nil)
	r.press(pantryDoorPico, "on")
	r.requireTurnedOn(pantryZone, 30)
}

func TestShadowState_RecordsLastEventAndTargets(t *testing.T) {
	r := newRig(t, false)
	r.setLight(pantryZone, 0)
	r.start()

	r.ha.SimulateEvent(buttonEventType, map[string]interface{}{
		"device_id": unconfiguredPico, "device_name": "Mystery Pico", "button_type": "on", "action": "press",
	})
	state := r.m.GetShadowState()
	require.NotNil(t, state.Outputs.LastEvent)
	assert.Equal(t, unconfiguredPico, state.Outputs.LastEvent.DeviceID)
	assert.Equal(t, "Mystery Pico", state.Outputs.LastEvent.DeviceName)
	assert.False(t, state.Outputs.LastEvent.Matched)

	r.press(pantryDoorPico, "on")
	state = r.m.GetShadowState()
	assert.True(t, state.Outputs.LastEvent.Matched)
	got := state.Outputs.Targets["pantry"]
	assert.Equal(t, pantryDoorButton, got.Button)
	assert.Equal(t, directionUp, got.Direction)
	assert.Equal(t, 0, got.FromPct)
	assert.Equal(t, 30, got.ToPct)
	assert.Contains(t, state.Inputs.Current, "pantry.referencePct")
}

func TestStop_EndsSubscription(t *testing.T) {
	r := newRig(t, false)
	r.setLight(pantryZone, 0)
	require.NoError(t, r.m.Start())

	r.m.Stop()
	r.press(pantryDoorPico, "on")

	assert.Zero(t, r.ha.ServiceCallCount())
}

func TestLoadConfig_ShippedConfigIsValid(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join("..", "..", "..", "..", "configs", "pico_config.yaml"))
	require.NoError(t, err)

	assert.Equal(t, 30, cfg.OnPct)
	byDevice := make(map[string]string)
	for _, b := range cfg.Buttons {
		byDevice[b.DeviceID] = b.Target
	}
	assert.Equal(t, "pantry", byDevice["53029c3f27ec026f77bdaa7057187bbe"])
	assert.Equal(t, "pantry", byDevice["7565da047cf5f8cd4a788e13b640cd6d"])
	assert.Equal(t, "kitchen", byDevice["cc81d6bde6cb377b0be9f9108735779d"])
	assert.Equal(t, "kids hall", byDevice["80f92d7f29fdbef7643ed1b002532267"])
	assert.Equal(t, "kids hall", byDevice["94ac4ddc87211f92e2ea35444a567bc5"])
	assert.Equal(t, "living", byDevice["b24af62680f649083e66166fc40faf76"])
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "valid minimal",
			yaml: "targets:\n  - {name: a, lights: [light.a]}\nbuttons:\n  - {name: b, device_id: d1, target: a}\n",
		},
		{
			name:    "unknown target",
			yaml:    "targets:\n  - {name: a, lights: [light.a]}\nbuttons:\n  - {name: b, device_id: d1, target: nope}\n",
			wantErr: "unknown target",
		},
		{
			name:    "duplicate device",
			yaml:    "targets:\n  - {name: a, lights: [light.a]}\nbuttons:\n  - {name: b, device_id: d1, target: a}\n  - {name: c, device_id: d1, target: a}\n",
			wantErr: "duplicate device_id",
		},
		{
			name:    "target without lights",
			yaml:    "targets:\n  - {name: a, lights: []}\n",
			wantErr: "at least one light",
		},
		{
			name:    "button without device id",
			yaml:    "targets:\n  - {name: a, lights: [light.a]}\nbuttons:\n  - {name: b, target: a}\n",
			wantErr: "device_id is required",
		},
		{
			name:    "step out of range",
			yaml:    "step_pct: 150\n",
			wantErr: "step_pct",
		},
		{
			name:    "duplicate target",
			yaml:    "targets:\n  - {name: a, lights: [light.a]}\n  - {name: a, lights: [light.b]}\n",
			wantErr: "duplicate name",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "pico.yaml")
			require.NoError(t, os.WriteFile(path, []byte(tt.yaml), 0o600))

			_, err := LoadConfig(path)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
