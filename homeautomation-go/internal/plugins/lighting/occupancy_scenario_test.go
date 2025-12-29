package lighting

// =============================================================================
// OCCUPANCY-BASED LIGHTING SCENARIO TESTS
// =============================================================================
//
// PURPOSE:
// These tests validate that the Lighting Manager responds correctly to room
// occupancy changes. When a room becomes occupied, its lights should turn on
// with the appropriate scene. When a room becomes unoccupied, its lights
// should turn off.
//
// CURRENT STATUS: PASSING
// The Lighting Manager now subscribes to occupancy variables and responds
// to state changes by activating scenes or turning off lights.
//
// NODE-RED REFERENCE:
// - Flow: Lighting Control (16cd74edb3f2c03d)
// - URL: https://node-red.featherback-mermaid.ts.net/#flow/16cd74edb3f2c03d
// - The Node-RED flow subscribes to occupancy state changes and triggers
//   scene activations or light turn-offs based on room configuration.
//
// CONFIGURATION REFERENCE:
// - File: configs/hue_config.yaml
// - Relevant room configs:
//   - N Office: on_if_true: isNickOfficeOccupied, off_if_false: isNickOfficeOccupied
//   - Kitchen: on_if_true: isKitchenOccupied, off_if_false: isAnyoneHomeAndAwake
//
// IMPLEMENTATION:
// The Lighting Manager (internal/plugins/lighting/manager.go) implements:
//
// 1. collectConditionVariables(): Parses all unique variables from room configs
//    - Extracts variables from OnIfTrue, OnIfFalse, OffIfTrue, OffIfFalse fields
//    - Example: "isNickOfficeOccupied", "isKitchenOccupied", "isAnyoneHomeAndAwake"
//
// 2. Subscribe to state changes for these variables in Start()
//    - Calls stateManager.Subscribe() for each condition variable
//    - The subscription callback triggers room re-evaluation
//
// 3. handleOccupancyChange(): When a subscribed variable changes
//    - Identifies which rooms use that variable in their conditions
//    - For each affected room, evaluates the on/off conditions
//    - If on_if_true matches: activates the appropriate scene (scene.turn_on)
//    - If off_if_false matches: turns off lights (light.turn_off)
//
// 4. Scene naming convention:
//    - Scene entity_id format: scene.{snake_case(hue_group + " " + dayPhase)}
//    - Example: scene.n_office_day, scene.kitchen_evening
//
// =============================================================================

import (
	"testing"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// createOccupancyTestConfig creates a configuration matching the actual hue_config.yaml
// with Nick Office and Kitchen rooms that respond to occupancy.
//
// This mirrors the real configuration where:
// - N Office turns on when isNickOfficeOccupied becomes true
// - N Office turns off when isNickOfficeOccupied becomes false
// - Kitchen turns on when isKitchenOccupied becomes true
// - Kitchen turns off when isAnyoneHomeAndAwake becomes false (different trigger!)
func createOccupancyTestConfig() *HueConfig {
	transition2 := 2
	transition5 := 5

	return &HueConfig{
		Rooms: []RoomConfig{
			{
				HueGroup:          "N Office",
				HASSAreaID:        "n_office",
				OnIfTrue:          "isNickOfficeOccupied",
				OnIfFalse:         nil,
				OffIfTrue:         nil,
				OffIfFalse:        "isNickOfficeOccupied",
				TransitionSeconds: &transition2,
			},
			{
				HueGroup:          "Kitchen",
				HASSAreaID:        "kitchen",
				OnIfTrue:          "isKitchenOccupied",
				OnIfFalse:         nil,
				OffIfTrue:         nil,
				OffIfFalse:        "isAnyoneHomeAndAwake",
				TransitionSeconds: &transition5,
			},
		},
	}
}

// =============================================================================
// TEST: Nick Office Occupied -> Lights Turn On
// =============================================================================
//
// SCENARIO:
// Nick enters his office (occupancy sensor triggers isNickOfficeOccupied = true)
//
// EXPECTED BEHAVIOR:
// The Lighting Manager should activate the office scene based on current dayPhase.
// - Service call: scene.turn_on
// - Entity: scene.n_office_day (when dayPhase = "day")
// - Data: { entity_id: "scene.n_office_day", transition: 2 }
//
// NODE-RED BEHAVIOR:
// In Node-RED, the "Nick Office Occupied" node watches for state changes and
// triggers the "Determine what action" function, which then activates the scene.
func TestScenario_NickOfficeOccupied_TurnsOnLights(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createOccupancyTestConfig()

	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Initialize required state variables
	// dayPhase determines which scene to activate (e.g., "day" -> scene.n_office_day)
	_ = stateManager.SetString("dayPhase", "day")
	_ = stateManager.SetString("sunevent", "day")
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isTVPlaying", false)
	_ = stateManager.SetBool("isEveryoneAsleep", false)
	_ = stateManager.SetBool("isMasterAsleep", false)
	_ = stateManager.SetBool("isHaveGuests", false)
	_ = stateManager.SetBool("isNickOfficeOccupied", false) // Office starts unoccupied
	_ = stateManager.SetBool("isKitchenOccupied", false)
	_ = stateManager.SetBool("isAnyoneHomeAndAwake", true)

	// Start manager - this is where subscriptions should be set up
	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Clear any initial service calls from manager startup
	mockClient.ClearServiceCalls()

	// ==========================================================
	// ACTION: Nick enters his office (occupancy sensor triggers)
	// ==========================================================
	err = stateManager.SetBool("isNickOfficeOccupied", true)
	assert.NoError(t, err)

	// ==========================================================
	// VERIFICATION: Office lights should turn on with "day" scene
	// ==========================================================
	calls := mockClient.GetServiceCalls()

	// Look for scene.turn_on call for the office
	foundSceneActivation := false
	for _, call := range calls {
		if call.Domain == "scene" && call.Service == "turn_on" {
			entityID, ok := call.Data["entity_id"].(string)
			if ok && entityID == "scene.n_office_day" {
				foundSceneActivation = true
				// Note: area_id is intentionally NOT passed for scene.turn_on to match Node-RED behavior
				assert.Nil(t, call.Data["area_id"],
					"Scene activation should NOT include area_id")
				// Verify transition time from config
				assert.Equal(t, 2, call.Data["transition"],
					"Scene activation should use configured transition time")
			}
		}
	}

	assert.True(t, foundSceneActivation,
		"Expected scene.turn_on for scene.n_office_day when Nick Office becomes occupied. "+
			"Calls received: %+v", calls)
}

// =============================================================================
// TEST: Nick Office Unoccupied -> Lights Turn Off
// =============================================================================
//
// SCENARIO:
// Nick leaves his office (occupancy sensor clears, isNickOfficeOccupied = false)
//
// EXPECTED BEHAVIOR:
// The Lighting Manager should turn off the office lights.
// - Service call: light.turn_off
// - Target: area_id: "n_office"
// - Data: { area_id: "n_office", transition: 2 }
//
// CONFIG REFERENCE:
// The room config has: off_if_false: "isNickOfficeOccupied"
// This means: when isNickOfficeOccupied becomes FALSE, turn off lights.
func TestScenario_NickOfficeUnoccupied_TurnsOffLights(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createOccupancyTestConfig()

	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Initialize required state variables - office is currently OCCUPIED
	_ = stateManager.SetString("dayPhase", "day")
	_ = stateManager.SetString("sunevent", "day")
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isTVPlaying", false)
	_ = stateManager.SetBool("isEveryoneAsleep", false)
	_ = stateManager.SetBool("isMasterAsleep", false)
	_ = stateManager.SetBool("isHaveGuests", false)
	_ = stateManager.SetBool("isNickOfficeOccupied", true) // Office starts occupied
	_ = stateManager.SetBool("isKitchenOccupied", false)
	_ = stateManager.SetBool("isAnyoneHomeAndAwake", true)

	// Start manager
	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Clear any initial service calls
	mockClient.ClearServiceCalls()

	// ==========================================================
	// ACTION: Nick leaves his office (occupancy sensor clears)
	// ==========================================================
	err = stateManager.SetBool("isNickOfficeOccupied", false)
	assert.NoError(t, err)

	// ==========================================================
	// VERIFICATION: Office lights should turn off
	// ==========================================================
	calls := mockClient.GetServiceCalls()

	// Look for light.turn_off call for the office area
	foundLightOff := false
	for _, call := range calls {
		if call.Domain == "light" && call.Service == "turn_off" {
			areaID, ok := call.Data["area_id"].(string)
			if ok && areaID == "n_office" {
				foundLightOff = true
				// Note: turn_off intentionally does NOT include transition_seconds
				// so lights turn off immediately (especially important for sleep scenarios)
				assert.Nil(t, call.Data["transition"],
					"turn_off should not include transition")
			}
		}
	}

	assert.True(t, foundLightOff,
		"Expected light.turn_off for n_office when Nick Office becomes unoccupied. "+
			"Calls received: %+v", calls)
}

// =============================================================================
// TEST: Kitchen Occupied -> Lights Turn On
// =============================================================================
//
// SCENARIO:
// Someone enters the kitchen (occupancy sensor triggers isKitchenOccupied = true)
//
// EXPECTED BEHAVIOR:
// The Lighting Manager should activate the kitchen scene based on current dayPhase.
// - Service call: scene.turn_on
// - Entity: scene.kitchen_evening (when dayPhase = "evening")
// - Data: { entity_id: "scene.kitchen_evening", transition: 5 }
//
// NOTE: Kitchen has DIFFERENT off condition!
// - on_if_true: isKitchenOccupied (turns on when someone enters)
// - off_if_false: isAnyoneHomeAndAwake (turns off when everyone leaves/sleeps)
// This is different from the office which uses the same variable for both.
func TestScenario_KitchenOccupied_TurnsOnLights(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createOccupancyTestConfig()

	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Initialize required state variables
	// Using "evening" dayPhase to verify correct scene selection
	_ = stateManager.SetString("dayPhase", "evening")
	_ = stateManager.SetString("sunevent", "sunset")
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isTVPlaying", false)
	_ = stateManager.SetBool("isEveryoneAsleep", false)
	_ = stateManager.SetBool("isMasterAsleep", false)
	_ = stateManager.SetBool("isHaveGuests", false)
	_ = stateManager.SetBool("isNickOfficeOccupied", false)
	_ = stateManager.SetBool("isKitchenOccupied", false) // Kitchen starts unoccupied
	_ = stateManager.SetBool("isAnyoneHomeAndAwake", true)

	// Start manager
	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Clear any initial service calls
	mockClient.ClearServiceCalls()

	// ==========================================================
	// ACTION: Someone enters the kitchen (occupancy sensor triggers)
	// ==========================================================
	err = stateManager.SetBool("isKitchenOccupied", true)
	assert.NoError(t, err)

	// ==========================================================
	// VERIFICATION: Kitchen lights should turn on with "evening" scene
	// ==========================================================
	calls := mockClient.GetServiceCalls()

	// Look for scene.turn_on call for the kitchen
	foundSceneActivation := false
	for _, call := range calls {
		if call.Domain == "scene" && call.Service == "turn_on" {
			entityID, ok := call.Data["entity_id"].(string)
			if ok && entityID == "scene.kitchen_evening" {
				foundSceneActivation = true
				// Note: area_id is intentionally NOT passed for scene.turn_on to match Node-RED behavior
				assert.Nil(t, call.Data["area_id"],
					"Scene activation should NOT include area_id")
				// Verify transition time from config (kitchen uses 5 seconds)
				assert.Equal(t, 5, call.Data["transition"],
					"Scene activation should use configured transition time")
			}
		}
	}

	assert.True(t, foundSceneActivation,
		"Expected scene.turn_on for scene.kitchen_evening when Kitchen becomes occupied. "+
			"Calls received: %+v", calls)
}

// =============================================================================
// TEST: Occupancy Change Only Affects Relevant Room
// =============================================================================
//
// SCENARIO:
// Nick enters his office (isNickOfficeOccupied = true)
//
// EXPECTED BEHAVIOR:
// ONLY the office lights should be affected. The kitchen lights should NOT
// change, even though it's also configured for occupancy-based control.
//
// WHY THIS TEST MATTERS:
// This validates that the implementation correctly maps variables to rooms.
// When isNickOfficeOccupied changes, only rooms that reference that variable
// in their on/off conditions should be affected.
func TestScenario_OccupancyChangeOnlyAffectsRelevantRoom(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createOccupancyTestConfig()

	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Initialize required state variables
	_ = stateManager.SetString("dayPhase", "day")
	_ = stateManager.SetString("sunevent", "day")
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isTVPlaying", false)
	_ = stateManager.SetBool("isEveryoneAsleep", false)
	_ = stateManager.SetBool("isMasterAsleep", false)
	_ = stateManager.SetBool("isHaveGuests", false)
	_ = stateManager.SetBool("isNickOfficeOccupied", false)
	_ = stateManager.SetBool("isKitchenOccupied", false)
	_ = stateManager.SetBool("isAnyoneHomeAndAwake", true)

	// Start manager
	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Clear any initial service calls
	mockClient.ClearServiceCalls()

	// ==========================================================
	// ACTION: Nick enters his office
	// ==========================================================
	err = stateManager.SetBool("isNickOfficeOccupied", true)
	assert.NoError(t, err)

	// ==========================================================
	// VERIFICATION: Only office should be affected, NOT kitchen
	// ==========================================================
	calls := mockClient.GetServiceCalls()

	for _, call := range calls {
		if call.Domain == "scene" && call.Service == "turn_on" {
			entityID, ok := call.Data["entity_id"].(string)
			if ok {
				// Should NOT see any kitchen scene activation
				assert.NotEqual(t, "scene.kitchen_day", entityID,
					"Kitchen lights should NOT be affected by Nick Office occupancy change. "+
						"The implementation must correctly map variables to rooms.")
			}
		}
		if call.Domain == "light" && call.Service == "turn_off" {
			areaID, ok := call.Data["area_id"].(string)
			if ok {
				// Should NOT see kitchen turn off
				assert.NotEqual(t, "kitchen", areaID,
					"Kitchen lights should NOT be turned off by Nick Office occupancy change. "+
						"The implementation must correctly map variables to rooms.")
			}
		}
	}
}
