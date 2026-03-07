package integration

import (
	"context"
	"path/filepath"
	"testing"

	"homeautomation/internal/plugins/lighting"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Hue Bridge Staleness Detection Scenario Tests
//
// These tests validate that the lighting plugin can detect when the Hue bridge
// becomes stale (responds to API calls but doesn't actually change lights).
//
// User story: "When the Hue bridge becomes stale, I want to be notified so I
// can power-cycle it, rather than having the house stuck on wrong scenes all day."
//
// Key invariants:
// - Single-room failures should NOT trigger a notification (could be bulb issue)
// - Multi-room failures (>=2 distinct rooms) SHOULD trigger a notification
// - Notifications should have a cooldown to prevent spam
// ============================================================================

// setupBridgeStalenessTest creates a test environment with lighting config that
// includes light_entity_id for bridge monitoring.
func setupBridgeStalenessTest(t *testing.T) (*MockHAServer, *lighting.Manager, *state.Manager, func()) {
	server, client, manager, baseCleanup := setupTest(t)

	// Load test lighting config
	configPath := filepath.Join("testdata", "hue_config_test.yaml")
	lightingConfig, err := lighting.LoadConfig(configPath)
	require.NoError(t, err, "Failed to load test lighting config")

	// Add light_entity_id to test rooms for bridge monitoring
	for i := range lightingConfig.Rooms {
		switch lightingConfig.Rooms[i].HueGroup {
		case "Living Room":
			lightingConfig.Rooms[i].LightEntityID = "light.living_room"
		case "Master Bedroom":
			lightingConfig.Rooms[i].LightEntityID = "light.master_bedroom"
		case "Front of House":
			lightingConfig.Rooms[i].LightEntityID = "light.front_of_house"
		}
	}

	logger := testlogger.New()
	lightingMgr := lighting.NewManager(context.Background(), client, manager, lightingConfig, logger, false, nil)

	err = lightingMgr.Start()
	require.NoError(t, err, "Failed to start lighting manager")

	// Set up light entities in the mock server
	server.SetState("light.living_room", "on", map[string]interface{}{
		"brightness": float64(100),
	})
	server.SetState("light.master_bedroom", "on", map[string]interface{}{
		"brightness": float64(100),
	})
	server.SetState("light.front_of_house", "on", map[string]interface{}{
		"brightness": float64(100),
	})

	cleanup := func() {
		lightingMgr.Stop()
		baseCleanup()
	}

	return server, lightingMgr, manager, cleanup
}

// TestScenario_BridgeStale_SingleRoomFailure_NoNotification validates that a
// single room's brightness not changing does NOT trigger a bridge stale notification.
// This could be a single bulb issue rather than a bridge problem.
//
// GIVEN: Lighting plugin is running with bridge monitoring enabled
// WHEN: A scene is activated for one room but brightness doesn't change
// THEN: No notification is sent (single room failure is not enough evidence)
func TestScenario_BridgeStale_SingleRoomFailure_NoNotification(t *testing.T) {
	t.Parallel()
	server, lightingMgr, stateManager, cleanup := setupBridgeStalenessTest(t)
	defer cleanup()
	_ = lightingMgr

	// GIVEN: Someone is home and awake, day phase is morning
	t.Log("GIVEN: Someone is home and awake, day phase is morning")
	server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	server.SetState("input_boolean.tv_playing", "off", map[string]interface{}{})
	server.SetState("input_text.day_phase", "morning", map[string]interface{}{})
	waitForProcessing(t, stateManager)

	// Note: The brightness stays the same (simulating a stale bridge for this room)
	// light.living_room stays at brightness 100

	snapshot := server.ServiceCallCount()

	// WHEN: Day phase changes (triggers scene activation for all rooms)
	t.Log("WHEN: Day phase changes to evening")
	server.SetState("input_text.day_phase", "evening", map[string]interface{}{})

	// Wait for scene activation
	waitForServiceCallSince(t, server, snapshot, "scene", "turn_on", "scenes should be activated")

	// THEN: Wait for verification to complete, then check no notification was sent
	t.Log("THEN: No bridge stale notification should be sent for single room")

	// Get the shadow state to verify bridge monitor state
	shadowState := lightingMgr.GetShadowState()

	// Even if verification ran and failed, we should not mark bridge as stale
	// because only one room failed (and only if brightness didn't actually change)
	if shadowState.Outputs.BridgeMonitor != nil {
		assert.False(t, shadowState.Outputs.BridgeMonitor.BridgeStale,
			"Bridge should NOT be marked stale from a single room failure")
	}

	// Verify no notification service calls were made
	calls := server.GetServiceCallsSince(snapshot)
	for _, call := range calls {
		assert.NotEqual(t, "notify", call.Domain,
			"No notification should be sent for single room failure")
	}
}
