package earlywake

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const (
	appUser    = "app-user"
	personUser = "person-user"
	livingWake = "light.living"

	timeout = time.Second
	tick    = 5 * time.Millisecond
)

type rig struct {
	t  *testing.T
	ha *ha.MockClient
	sm *state.Manager
	m  *Manager
}

// newRig builds a Manager whose house is asleep and whose early wake is off.
func newRig(t *testing.T, readOnly bool) *rig {
	t.Helper()
	mock := ha.NewMockClient()
	mock.SetCurrentUserID(appUser, nil)
	sm := state.NewManager(mock, zap.NewNop(), false)
	require.NoError(t, sm.SetBool("isAnyoneAsleep", true))
	require.NoError(t, sm.SetBool("isEarlyWakeActive", false))
	m := NewManager(context.Background(), mock, sm, &Config{WakeLights: []string{livingWake}}, zap.NewNop(), readOnly)
	return &rig{t: t, ha: mock, sm: sm, m: m}
}

func (r *rig) start() {
	r.t.Helper()
	require.NoError(r.t, r.m.Start())
	r.t.Cleanup(r.m.Stop)
}

func (r *rig) earlyWake() bool {
	r.t.Helper()
	v, err := r.sm.GetBool("isEarlyWakeActive")
	require.NoError(r.t, err)
	return v
}

func (r *rig) turnOn(oldState string, ctx *ha.Context) {
	var old *ha.State
	if oldState != "" {
		old = &ha.State{EntityID: livingWake, State: oldState}
	}
	r.m.handleLightChange(livingWake, old, &ha.State{EntityID: livingWake, State: "on", Context: ctx})
}

func TestHandleLightChange(t *testing.T) {
	tests := []struct {
		name     string
		oldState string
		ctx      *ha.Context
		asleep   bool
		want     bool
	}{
		{"Hue app or wall switch while asleep wakes", "off", &ha.Context{ID: "c"}, true, true},
		{"a person's dashboard tap while asleep wakes", "off", &ha.Context{ID: "c", UserID: personUser}, true, true},
		// Hue bulbs come back "on" after a power cut, with no user in the context.
		{"unavailable to on (power restored) does not wake", "unavailable", &ha.Context{ID: "c"}, true, false},
		{"first state ever does not wake", "", &ha.Context{ID: "c"}, true, false},
		{"the app's own command does not wake", "off", &ha.Context{ID: "c", UserID: appUser}, true, false},
		{"an automation does not wake", "off", &ha.Context{ID: "c", ParentID: "p"}, true, false},
		{"an automation run by a person does not wake", "off",
			&ha.Context{ID: "c", ParentID: "p", UserID: personUser}, true, false},
		{"no context does not wake", "off", nil, true, false},
		{"a brightness change does not wake", "on", &ha.Context{ID: "c"}, true, false},
		{"nobody asleep does not wake", "off", &ha.Context{ID: "c"}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, false)
			require.NoError(t, r.sm.SetBool("isAnyoneAsleep", tt.asleep))
			r.start()

			r.turnOn(tt.oldState, tt.ctx)

			assert.Equal(t, tt.want, r.earlyWake())
			if tt.oldState == "off" {
				last := r.m.GetShadowState().Outputs.LastLightChange
				require.NotNil(t, last, "every off->on change is recorded")
				assert.Equal(t, livingWake, last.EntityID)
				assert.NotEmpty(t, last.Decision)
			}
		})
	}
}

func TestHandleLightChange_TurnOffIsIgnored(t *testing.T) {
	r := newRig(t, false)
	r.start()

	r.m.handleLightChange(livingWake, &ha.State{State: "on"}, &ha.State{State: "off", Context: &ha.Context{ID: "c"}})
	r.m.handleLightChange(livingWake, &ha.State{State: "on"}, nil)

	assert.False(t, r.earlyWake())
	assert.Nil(t, r.m.GetShadowState().Outputs.LastLightChange)
}

func TestHandleLightChange_RecordsActivation(t *testing.T) {
	r := newRig(t, false)
	r.start()

	r.turnOn("off", &ha.Context{ID: "c"})

	out := r.m.GetShadowState().Outputs
	assert.Equal(t, appUser, out.AppUserID)
	assert.Equal(t, []string{livingWake}, out.WakeLights)
	assert.Equal(t, livingWake, out.LastActivatedBy)
	assert.False(t, out.LastActivatedAt.IsZero())
	require.NotNil(t, out.LastLightChange)
	assert.True(t, out.LastLightChange.Manual)

	// A second manual turn-on finds it already on.
	r.turnOn("off", &ha.Context{ID: "c2"})
	assert.Equal(t, "manual, early wake already on", r.m.GetShadowState().Outputs.LastLightChange.Decision)
}

func TestHandleLightChange_ReadOnlyDoesNotSet(t *testing.T) {
	r := newRig(t, true)
	r.start()

	r.turnOn("off", &ha.Context{ID: "c"})

	assert.False(t, r.earlyWake(), "read-only mode must not set isEarlyWakeActive")
	assert.True(t, r.m.GetShadowState().Outputs.ReadOnly)
}

// Without the app's user ID, the app's own commands would look like a person's. Detection waits.
func TestHandleLightChange_UnknownAppUserDoesNotWake(t *testing.T) {
	r := newRig(t, false)
	r.ha.SetCurrentUserID("", errors.New("not connected"))
	r.start()

	r.turnOn("off", &ha.Context{ID: "c", UserID: appUser})
	assert.False(t, r.earlyWake(), "an unknown app user must not count as a person")
	r.turnOn("off", &ha.Context{ID: "c"})
	assert.False(t, r.earlyWake(), "detection waits until the app user is known")

	// Home Assistant answers later: detection resumes.
	r.ha.SetCurrentUserID(appUser, nil)
	r.turnOn("off", &ha.Context{ID: "c", UserID: appUser})
	assert.False(t, r.earlyWake())
	r.turnOn("off", &ha.Context{ID: "c"})
	assert.True(t, r.earlyWake())
	assert.Equal(t, appUser, r.m.appUserID)
}

func TestAnyoneAsleepFallingEdgeClearsEarlyWake(t *testing.T) {
	r := newRig(t, false)
	r.start()
	r.turnOn("off", &ha.Context{ID: "c"})
	require.True(t, r.earlyWake())

	require.NoError(t, r.sm.SetBool("isAnyoneAsleep", false))

	assert.Eventually(t, func() bool { return !r.earlyWake() }, timeout, tick,
		"a normal wake must clear early wake")
	assert.False(t, r.m.GetShadowState().Outputs.LastClearedAt.IsZero())
}

func TestAnyoneAsleepFallingEdgeReadOnlyKeepsEarlyWake(t *testing.T) {
	r := newRig(t, true)
	require.NoError(t, r.sm.SetBool("isEarlyWakeActive", true))
	r.start()

	require.NoError(t, r.sm.SetBool("isAnyoneAsleep", false))

	assert.True(t, r.earlyWake(), "read-only mode must not clear isEarlyWakeActive")
}

func TestAnyoneAsleepRisingEdgeDoesNothing(t *testing.T) {
	r := newRig(t, false)
	require.NoError(t, r.sm.SetBool("isAnyoneAsleep", false))
	require.NoError(t, r.sm.SetBool("isEarlyWakeActive", true))
	r.start()

	require.NoError(t, r.sm.SetBool("isAnyoneAsleep", true))

	assert.True(t, r.earlyWake())
}

// Bedtime clears early wake, even while a guest keeps isAnyoneAsleep true.
func TestMasterAsleepChange(t *testing.T) {
	tests := []struct {
		name          string
		readOnly      bool
		masterBefore  bool
		masterAfter   bool
		wantEarlyWake bool
		wantCleared   bool
		why           string
	}{
		{"rising edge clears", false, false, true, false, true, "bedtime must clear early wake"},
		{"rising edge read-only keeps", true, false, true, true, false,
			"read-only mode must not clear isEarlyWakeActive"},
		{"falling edge does nothing", false, true, false, true, false,
			"the primary bedroom waking must not clear early wake"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, tc.readOnly)
			require.NoError(t, r.sm.SetBool("isMasterAsleep", tc.masterBefore))
			require.NoError(t, r.sm.SetBool("isEarlyWakeActive", true))
			r.start()

			require.NoError(t, r.sm.SetBool("isMasterAsleep", tc.masterAfter))

			if tc.wantEarlyWake {
				assert.True(t, r.earlyWake(), tc.why)
			} else {
				assert.Eventually(t, func() bool { return !r.earlyWake() }, timeout, tick, tc.why)
			}
			assert.Equal(t, tc.wantCleared, !r.m.GetShadowState().Outputs.LastClearedAt.IsZero())
			assert.True(t, mustBool(t, r.sm, "isAnyoneAsleep"),
				"the clear must not depend on isAnyoneAsleep falling")
		})
	}
}

func mustBool(t *testing.T, sm *state.Manager, key string) bool {
	t.Helper()
	v, err := sm.GetBool(key)
	require.NoError(t, err)
	return v
}

func TestStart_CapturesShadowInputs(t *testing.T) {
	r := newRig(t, false)
	r.ha.SetState(livingWake, "off", map[string]interface{}{})
	require.NoError(t, r.sm.SetBool("isMasterAsleep", true))
	r.start()

	inputs := r.m.GetShadowState().Inputs.Current
	assert.Contains(t, inputs, "isAnyoneAsleep")
	assert.Contains(t, inputs, "isMasterAsleep")
	assert.Contains(t, inputs, livingWake)
}

func TestStart_SubscribesToWakeLights(t *testing.T) {
	r := newRig(t, false)
	r.start()

	assert.Contains(t, r.ha.GetSubscribedEntities(), livingWake)

	r.m.Stop()
	assert.NotContains(t, r.ha.GetSubscribedEntities(), livingWake)
}

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		want    []string
		wantErr string
	}{
		{"valid", "wake_lights:\n  - light.a\n  - light.b\n", []string{"light.a", "light.b"}, ""},
		{"empty", "wake_lights: []\n", nil, "at least one light"},
		{"not a light", "wake_lights:\n  - switch.a\n", nil, "not a light entity ID"},
		{"bare prefix", "wake_lights:\n  - light.\n", nil, "not a light entity ID"},
		{"duplicate", "wake_lights:\n  - light.a\n  - light.a\n", nil, "duplicate"},
		{"bad yaml", "wake_lights: [\n", nil, "parse early wake config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "early_wake_config.yaml")
			require.NoError(t, os.WriteFile(path, []byte(tt.yaml), 0o600))

			cfg, err := LoadConfig(path)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, cfg.WakeLights)
		})
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	_, err := LoadConfig(filepath.Join(t.TempDir(), "missing.yaml"))
	assert.Error(t, err)
}

func TestLoadConfig_ShippedConfig(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join("..", "..", "..", "..", "configs", "early_wake_config.yaml"))
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{
		"light.living",
		"light.living_accent",
		"light.sitting",
		"light.sitting_chandelier",
		"light.kitchen_cans",
		"light.kitchen_main_lights",
		"light.breakfast_nook_island_can",
		"light.breakfast_nook_pantry_can",
	}, cfg.WakeLights)
	for _, l := range cfg.WakeLights {
		assert.NotContains(t, l, "pantry_area", "the pantry is the night path and must not wake the house")
	}
}
