package integration

import (
	"context"
	"path/filepath"
	"testing"

	"homeautomation/internal/plugins/lighting"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScenario_LivingCansColorTracksToBackHall(t *testing.T) {
	server, client, stateManager, cleanup := setupTest(t)
	defer cleanup()

	config, err := lighting.LoadConfig(filepath.Join("..", "..", "..", "configs", "hue_config.yaml"))
	require.NoError(t, err)
	require.NotNil(t, config.ColorTracking)
	require.Equal(t, "light.living_cans", config.ColorTracking.SourceEntity,
		"production color tracking must follow the lux-controlled Living cans")

	manager := lighting.NewManager(context.Background(), client, stateManager, config, testlogger.New(), false, nil)
	require.NoError(t, manager.Start())
	defer manager.Stop()

	// GIVEN: Back Hall is off while the Living cans settle at their lux-controlled color.
	t.Log("GIVEN: Back Hall is off and Living cans change color temperature")
	server.SetState("light.back_hall", "off", map[string]interface{}{"brightness": float64(90)})
	snapshot := server.ServiceCallCount()
	server.SetState("light.living_cans", "on", map[string]interface{}{
		"color_mode": "color_temp", "color_temp_kelvin": float64(3003), "brightness": float64(200),
	})
	assert.Empty(t, server.GetServiceCallsSince(snapshot), "color changes must not turn on Back Hall")

	// WHEN: Back Hall turns on, THEN: the cached cans color is applied without brightness or power mirroring.
	t.Log("WHEN: Back Hall turns on")
	server.SetState("light.back_hall", "on", map[string]interface{}{"brightness": float64(90)})
	waitForServiceCallWithEntitySince(t, server, snapshot, "light", "turn_on", "light.back_hall")
	calls := filterServiceCalls(server.GetServiceCallsSince(snapshot), "light", "turn_on")
	require.Len(t, calls, 1)
	assert.Equal(t, float64(3003), calls[0].ServiceData["color_temp_kelvin"])
	assert.NotContains(t, calls[0].ServiceData, "brightness")

	// WHEN: The cans switch to a non-color-temperature representation while Back Hall remains on,
	// THEN: Back Hall follows that color only.
	t.Log("WHEN: Living cans change to XY color while Back Hall is on")
	snapshot = server.ServiceCallCount()
	server.SetState("light.living_cans", "on", map[string]interface{}{
		"color_mode": "xy", "xy_color": []interface{}{0.41, 0.36}, "brightness": float64(230),
	})
	waitForServiceCallWithEntitySince(t, server, snapshot, "light", "turn_on", "light.back_hall")
	calls = filterServiceCalls(server.GetServiceCallsSince(snapshot), "light", "turn_on")
	require.Len(t, calls, 1)
	assert.Equal(t, []interface{}{0.41, 0.36}, calls[0].ServiceData["xy_color"])
	assert.NotContains(t, calls[0].ServiceData, "brightness")
}
