package lighting

import (
	"context"
	"testing"

	"homeautomation/internal/ha"
	"homeautomation/pkg/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testColorSource = "light.main_living_space"
	testColorTarget = "light.back_hall"
)

func newColorTrackingManager(t *testing.T) (*Manager, *ha.MockClient) {
	t.Helper()
	env := testutil.NewEnv(t)
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, &HueConfig{
		ColorTracking: &ColorTrackingConfig{SourceEntity: testColorSource, TargetEntity: testColorTarget},
	}, env.Logger, false, nil)
	require.NoError(t, manager.Start())
	t.Cleanup(manager.Stop)
	return manager, env.MockHA
}

func TestColorTracking_CachesColorTemperatureWhileTargetOff(t *testing.T) {
	manager, client := newColorTrackingManager(t)
	client.SetState(testColorTarget, "off", map[string]interface{}{"brightness": float64(90)})

	client.SetState(testColorSource, "on", map[string]interface{}{
		"color_mode": "color_temp", "color_temp_kelvin": float64(2700), "brightness": float64(200),
	})
	require.Empty(t, client.GetServiceCalls())

	client.SetState(testColorTarget, "on", map[string]interface{}{"brightness": float64(90)})
	calls := client.GetServiceCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "light", calls[0].Domain)
	assert.Equal(t, "turn_on", calls[0].Service)
	assert.Equal(t, map[string]interface{}{
		"entity_id": testColorTarget, "color_temp_kelvin": 2700,
	}, calls[0].Data)
	assert.NotContains(t, calls[0].Data, "brightness")

	shadow := manager.shadowTracker.GetState().Outputs.ColorTracking
	require.NotNil(t, shadow.CachedColor)
	require.NotNil(t, shadow.LastApplied)
	assert.Equal(t, 2700, shadow.CachedColor.ColorTempKelvin)
	assert.Equal(t, 2700, shadow.LastApplied.ColorTempKelvin)
}

func TestColorTracking_CachesXYAndHSWhileTargetOff(t *testing.T) {
	tests := []struct {
		name      string
		mode      string
		attribute string
		value     []interface{}
		expected  []float64
	}{
		{name: "XY", mode: "xy", attribute: "xy_color", value: []interface{}{0.41, 0.36}, expected: []float64{0.41, 0.36}},
		{name: "HS", mode: "hs", attribute: "hs_color", value: []interface{}{32.0, 68.0}, expected: []float64{32, 68}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, client := newColorTrackingManager(t)
			client.SetState(testColorTarget, "off", nil)
			client.SetState(testColorSource, "on", map[string]interface{}{
				"color_mode": tt.mode, tt.attribute: tt.value,
			})
			client.SetState(testColorTarget, "on", nil)

			calls := client.GetServiceCalls()
			require.Len(t, calls, 1)
			assert.Equal(t, tt.expected, calls[0].Data[tt.attribute])
			assert.NotContains(t, calls[0].Data, "brightness")
		})
	}
}

func TestColorTracking_RetainsColorWhenSourceTurnsOff(t *testing.T) {
	_, client := newColorTrackingManager(t)
	client.SetState(testColorTarget, "off", nil)
	client.SetState(testColorSource, "on", map[string]interface{}{
		"color_mode": "xy", "xy_color": []interface{}{0.3, 0.4},
	})
	client.SetState(testColorSource, "off", map[string]interface{}{
		"color_mode": nil, "xy_color": nil,
	})
	client.SetState(testColorTarget, "on", nil)

	calls := client.GetServiceCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, []float64{0.3, 0.4}, calls[0].Data["xy_color"])
}

func TestColorTracking_TargetActivationWithoutObservedColorIsNoOp(t *testing.T) {
	manager, client := newColorTrackingManager(t)
	client.SetState(testColorTarget, "off", nil)
	client.SetState(testColorTarget, "on", map[string]interface{}{"brightness": float64(120)})

	assert.Empty(t, client.GetServiceCalls())
	assert.Nil(t, manager.shadowTracker.GetState().Outputs.ColorTracking.CachedColor)
}

func TestColorTracking_SourceUpdateAppliesWhileTargetOn(t *testing.T) {
	_, client := newColorTrackingManager(t)
	client.SetState(testColorTarget, "on", map[string]interface{}{"brightness": float64(77)})
	client.SetState(testColorSource, "on", map[string]interface{}{
		"color_mode": "hs", "hs_color": []interface{}{210.0, 45.0}, "brightness": float64(230),
	})

	calls := client.GetServiceCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, map[string]interface{}{
		"entity_id": testColorTarget, "hs_color": []float64{210, 45},
	}, calls[0].Data)
	assert.NotContains(t, calls[0].Data, "brightness")
	assert.NotContains(t, calls[0].Data, "state")
}

func TestColorTracking_DoesNotMirrorPowerOrReactToTargetColorChange(t *testing.T) {
	_, client := newColorTrackingManager(t)
	client.SetState(testColorTarget, "off", nil)
	client.SetState(testColorSource, "on", map[string]interface{}{
		"color_mode": "color_temp", "color_temp_kelvin": float64(3000),
	})
	client.SetState(testColorSource, "off", nil)
	assert.Empty(t, client.GetServiceCalls(), "source power changes must not control target power")

	client.SetState(testColorTarget, "on", map[string]interface{}{"color_temp_kelvin": float64(2200)})
	require.Len(t, client.GetServiceCalls(), 1)
	client.SetState(testColorTarget, "on", map[string]interface{}{"color_temp_kelvin": float64(4000)})
	assert.Len(t, client.GetServiceCalls(), 1, "manual target color changes must not be immediately overwritten")
	client.SetState(testColorSource, "on", map[string]interface{}{
		"color_mode": "color_temp", "color_temp_kelvin": float64(3000), "brightness": float64(100),
	})
	assert.Len(t, client.GetServiceCalls(), 1, "source re-fire with same color (3000K) must not trigger re-apply — DeepEqual short-circuits")
}

func TestColorFromState_PrefersActiveColorMode(t *testing.T) {
	color, ok := colorFromState(&ha.State{State: "on", Attributes: map[string]interface{}{
		"color_mode": "xy", "color_temp_kelvin": float64(2700), "xy_color": []interface{}{0.2, 0.3},
	}})
	require.True(t, ok)
	assert.Equal(t, "xy", color.Mode)
	assert.Equal(t, []float64{0.2, 0.3}, color.XYColor)
}
