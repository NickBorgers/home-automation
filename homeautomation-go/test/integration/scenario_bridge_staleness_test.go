package integration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

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

// immediateSleepForTest is a sleep function that returns immediately (for integration tests).
func immediateSleepForTest(_ context.Context, _ time.Duration) error {
	return nil
}

// setupBridgeStalenessTest creates a test environment with ONE room configured
// for bridge monitoring. This is intentional: the single-room test needs exactly
// one monitored room so that a failure in that room cannot reach the 2-room threshold.
func setupBridgeStalenessTest(t *testing.T) (*MockHAServer, *lighting.Manager, *state.Manager, func()) {
	server, client, manager, baseCleanup := setupTest(t)

	// Load test lighting config
	configPath := filepath.Join("testdata", "hue_config_test.yaml")
	lightingConfig, err := lighting.LoadConfig(configPath)
	require.NoError(t, err, "Failed to load test lighting config")

	// Add light_entity_id to only ONE room (Living Room) so that a single failure
	// can never reach the BridgeStaleRoomThreshold of 2. This matches the test
	// scenario: verifying that a single-room brightness failure is NOT enough
	// to declare the bridge stale.
	for i := range lightingConfig.Rooms {
		if lightingConfig.Rooms[i].HueGroup == "Living Room" {
			lightingConfig.Rooms[i].LightEntityID = "light.living_room"
		}
	}

	logger := testlogger.New()
	lightingMgr := lighting.NewManager(context.Background(), client, manager, lightingConfig, logger, false, nil)

	err = lightingMgr.Start()
	require.NoError(t, err, "Failed to start lighting manager")

	// Set up the monitored light entity in the mock server
	server.SetState("light.living_room", "on", map[string]interface{}{
		"brightness": float64(100),
	})

	cleanup := func() {
		lightingMgr.Stop()
		baseCleanup()
	}

	return server, lightingMgr, manager, cleanup
}

// waitForBridgeMonitor polls until the bridge monitor shadow state is populated,
// or the timeout is reached. Requires immediate sleep to be injected so the
// goroutine completes quickly.
func waitForBridgeMonitor(t *testing.T, lightingMgr *lighting.Manager) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if lightingMgr.GetShadowState().Outputs.BridgeMonitor != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Timed out waiting for bridge monitor shadow state to be populated")
}

// TestScenario_BridgeStale_SingleRoomFailure_NoNotification validates that a
// single room's brightness not changing does NOT trigger a bridge stale notification.
// This could be a single bulb issue rather than a bridge problem.
//
// GIVEN: Lighting plugin is running with bridge monitoring enabled for one room
// WHEN: A scene is activated for that room but brightness doesn't change
// THEN: No notification is sent (single room failure is not enough evidence)
func TestScenario_BridgeStale_SingleRoomFailure_NoNotification(t *testing.T) {
	t.Parallel()
	server, lightingMgr, stateManager, cleanup := setupBridgeStalenessTest(t)
	defer cleanup()

	// Override the sleep func so verification completes without a 15-second wait.
	// Light entity brightness is set to 100 and never changed, so verification
	// will record a failure (brightness unchanged). With only one monitored room,
	// the failure count stays at 1 — below the 2-room stale threshold.
	lightingMgr.SetBridgeMonitorSleepFunc(immediateSleepForTest)

	// GIVEN: Someone is home and awake, day phase is morning
	t.Log("GIVEN: Someone is home and awake, day phase is morning")
	server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	server.SetState("input_boolean.tv_playing", "off", map[string]interface{}{})
	server.SetState("input_text.day_phase", "morning", map[string]interface{}{})
	waitForProcessing(t, stateManager)

	// light.living_room stays at brightness 100 (simulating a stale bridge for this room)

	snapshot := server.ServiceCallCount()

	// WHEN: Day phase changes (triggers scene activation for all rooms)
	t.Log("WHEN: Day phase changes to evening")
	server.SetState("input_text.day_phase", "evening", map[string]interface{}{})

	// Wait for scene activation
	waitForServiceCallSince(t, server, snapshot, "scene", "turn_on", "scenes should be activated")

	// THEN: Wait for verification goroutine to complete (immediate sleep, so fast)
	t.Log("THEN: Waiting for bridge verification goroutine to complete")
	waitForBridgeMonitor(t, lightingMgr)

	// Bridge monitor must be populated but must NOT mark bridge as stale.
	// (Multiple failures may be recorded if multiple scene activations triggered
	// verification, but all failures are from the same room — below the 2-room threshold.)
	shadowState := lightingMgr.GetShadowState()
	require.NotNil(t, shadowState.Outputs.BridgeMonitor, "Bridge monitor should be populated after verification")
	assert.False(t, shadowState.Outputs.BridgeMonitor.BridgeStale,
		"Bridge should NOT be marked stale from a single room failure")

	// Verify no notification service calls were made
	calls := server.GetServiceCallsSince(snapshot)
	for _, call := range calls {
		assert.NotEqual(t, "notify", call.Domain,
			"No notification should be sent for single room failure")
	}
}
