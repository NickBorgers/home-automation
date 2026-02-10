package integration

import (
	"fmt"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Computed State Scenario Tests
//
// These tests validate that computed state variables are correctly derived
// from their dependencies and automatically updated when dependencies change.
//
// Computed state variables:
// - isAnyoneHomeAndAwake = (isAnyOwnerHome && !isAnyoneAsleep) || isToriHere
//
// Note: Tori doesn't have a sleep state tracked, so her presence means someone
// is home AND awake by definition.
// ============================================================================

// setupComputedStateTest creates a test environment with computed state initialized
func setupComputedStateTest(t *testing.T) (*MockHAServer, *ha.Client, *state.Manager, func()) {
	logger := testlogger.New()

	// Start mock HA server with dynamic port allocation
	server := NewMockHAServer(testAddr, testToken)
	server.InitializeStates()

	err := server.Start()
	require.NoError(t, err)

	// Get the actual address after dynamic port allocation
	actualAddr := server.Addr()

	// Create and connect client using the actual address
	client := ha.NewClient(fmt.Sprintf("ws://%s/api/websocket", actualAddr), testToken, logger)
	err = client.Connect()
	require.NoError(t, err)

	// Create state manager
	manager := state.NewManager(client, logger, false)
	err = manager.SyncFromHA()
	require.NoError(t, err)

	// Initialize computed state - this is the key addition
	err = manager.SetupComputedState()
	require.NoError(t, err)

	// Allow subscriptions to be established
	time.Sleep(50 * time.Millisecond)

	cleanup := func() {
		client.Disconnect()
		server.Stop()
	}

	return server, client, manager, cleanup
}

// TestScenario_ComputedState_IsAnyoneHomeAndAwake_InitialComputation validates
// that isAnyoneHomeAndAwake is correctly computed on startup
// Formula: (isAnyOwnerHome && !isAnyoneAsleep) || isToriHere
func TestScenario_ComputedState_IsAnyoneHomeAndAwake_InitialComputation(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name           string
		isAnyOwnerHome string
		isAnyoneAsleep string
		isToriHere     string
		expected       bool
	}{
		{
			name:           "owner home and awake should be true",
			isAnyOwnerHome: "on",
			isAnyoneAsleep: "off",
			isToriHere:     "off",
			expected:       true,
		},
		{
			name:           "owner home but asleep should be false",
			isAnyOwnerHome: "on",
			isAnyoneAsleep: "on",
			isToriHere:     "off",
			expected:       false,
		},
		{
			name:           "no owner home and awake should be false",
			isAnyOwnerHome: "off",
			isAnyoneAsleep: "off",
			isToriHere:     "off",
			expected:       false,
		},
		{
			name:           "no owner home and asleep should be false",
			isAnyOwnerHome: "off",
			isAnyoneAsleep: "on",
			isToriHere:     "off",
			expected:       false,
		},
		{
			name:           "tori here alone should be true",
			isAnyOwnerHome: "off",
			isAnyoneAsleep: "off",
			isToriHere:     "on",
			expected:       true,
		},
		{
			name:           "tori here while owner asleep should be true (BUG FIX)",
			isAnyOwnerHome: "on",
			isAnyoneAsleep: "on",
			isToriHere:     "on",
			expected:       true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logger := testlogger.New()

			// Start mock HA server with dynamic port allocation
			server := NewMockHAServer(testAddr, testToken)
			server.InitializeStates()

			// Set the initial states before connecting
			server.SetState("input_boolean.any_owner_home", tc.isAnyOwnerHome, map[string]interface{}{})
			server.SetState("input_boolean.anyone_asleep", tc.isAnyoneAsleep, map[string]interface{}{})
			server.SetState("input_boolean.tori_here", tc.isToriHere, map[string]interface{}{})
			server.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})

			err := server.Start()
			require.NoError(t, err)
			defer server.Stop()

			// Get the actual address after dynamic port allocation
			actualAddr := server.Addr()

			// Create and connect client using the actual address
			client := ha.NewClient(fmt.Sprintf("ws://%s/api/websocket", actualAddr), testToken, logger)
			err = client.Connect()
			require.NoError(t, err)
			defer client.Disconnect()

			// Create state manager and sync
			manager := state.NewManager(client, logger, false)
			err = manager.SyncFromHA()
			require.NoError(t, err)

			// Initialize computed state
			err = manager.SetupComputedState()
			require.NoError(t, err)

			// Allow computed state to be calculated
			time.Sleep(50 * time.Millisecond)

			// THEN: isAnyoneHomeAndAwake should be computed correctly
			value, err := manager.GetBool("isAnyoneHomeAndAwake")
			require.NoError(t, err)
			assert.Equal(t, tc.expected, value,
				"isAnyoneHomeAndAwake should be %v when isAnyOwnerHome=%s, isAnyoneAsleep=%s, isToriHere=%s",
				tc.expected, tc.isAnyOwnerHome, tc.isAnyoneAsleep, tc.isToriHere)
		})
	}
}

// TestScenario_ComputedState_ReactsToIsAnyOwnerHomeChange validates that
// isAnyoneHomeAndAwake updates when isAnyOwnerHome changes
func TestScenario_ComputedState_ReactsToIsAnyOwnerHomeChange(t *testing.T) {
	t.Parallel()
	server, _, manager, cleanup := setupComputedStateTest(t)
	defer cleanup()

	// GIVEN: No owner home and nobody is asleep
	t.Log("GIVEN: No owner home and nobody is asleep")
	server.SetState("input_boolean.any_owner_home", "off", map[string]interface{}{})
	server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	server.SetState("input_boolean.tori_here", "off", map[string]interface{}{})

	// Verify initial state
	waitForBoolState(t, manager, "isAnyoneHomeAndAwake", false, "Initially should be false when no owner is home")

	// WHEN: Owner comes home (still awake)
	t.Log("WHEN: Owner comes home")
	server.SetState("input_boolean.any_owner_home", "on", map[string]interface{}{})

	// THEN: isAnyoneHomeAndAwake should become true
	t.Log("THEN: isAnyoneHomeAndAwake should become true")
	waitForBoolState(t, manager, "isAnyoneHomeAndAwake", true, "Should be true when owner is home and awake")

	// WHEN: Owner leaves
	t.Log("WHEN: Owner leaves")
	server.SetState("input_boolean.any_owner_home", "off", map[string]interface{}{})

	// THEN: isAnyoneHomeAndAwake should become false
	t.Log("THEN: isAnyoneHomeAndAwake should become false")
	waitForBoolState(t, manager, "isAnyoneHomeAndAwake", false, "Should be false when no owner is home")
}

// TestScenario_ComputedState_ReactsToIsAnyoneAsleepChange validates that
// isAnyoneHomeAndAwake updates when isAnyoneAsleep changes
func TestScenario_ComputedState_ReactsToIsAnyoneAsleepChange(t *testing.T) {
	t.Parallel()
	server, _, manager, cleanup := setupComputedStateTest(t)
	defer cleanup()

	// GIVEN: Owner is home and awake
	t.Log("GIVEN: Owner is home and awake")
	server.SetState("input_boolean.any_owner_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	server.SetState("input_boolean.tori_here", "off", map[string]interface{}{})

	// Verify initial state
	waitForBoolState(t, manager, "isAnyoneHomeAndAwake", true, "Initially should be true when owner is home and awake")

	// WHEN: Someone falls asleep
	t.Log("WHEN: Someone falls asleep")
	server.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})

	// THEN: isAnyoneHomeAndAwake should become false
	t.Log("THEN: isAnyoneHomeAndAwake should become false")
	waitForBoolState(t, manager, "isAnyoneHomeAndAwake", false, "Should be false when someone is asleep")

	// WHEN: Everyone wakes up
	t.Log("WHEN: Everyone wakes up")
	server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})

	// THEN: isAnyoneHomeAndAwake should become true again
	t.Log("THEN: isAnyoneHomeAndAwake should become true again")
	waitForBoolState(t, manager, "isAnyoneHomeAndAwake", true, "Should be true again when everyone wakes up")
}

// TestScenario_ComputedState_SyncsToHomeAssistant validates that computed
// state changes are synced back to Home Assistant
func TestScenario_ComputedState_SyncsToHomeAssistant(t *testing.T) {
	t.Parallel()
	server, _, _, cleanup := setupComputedStateTest(t)
	defer cleanup()

	// GIVEN: No owner is home initially
	t.Log("GIVEN: No owner is home initially")
	server.SetState("input_boolean.any_owner_home", "off", map[string]interface{}{})
	server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	server.SetState("input_boolean.tori_here", "off", map[string]interface{}{})
	// Allow async handlers to process before clearing
	time.Sleep(50 * time.Millisecond)

	// Clear service calls to track new ones
	server.ClearServiceCalls()

	// WHEN: Owner comes home (triggering computed state change)
	t.Log("WHEN: Owner comes home")
	server.SetState("input_boolean.any_owner_home", "on", map[string]interface{}{})

	// Wait for sync to HA
	waitForServiceCall(t, server, "input_boolean", "turn_on", "computed state should sync to HA")

	// THEN: A service call should be made to update isAnyoneHomeAndAwake in HA
	t.Log("THEN: Computed state should be synced to HA")
	calls := server.GetServiceCalls()

	// Find the call that updated anyone_home_and_awake
	var foundCall *ServiceCall
	for i := range calls {
		if calls[i].Domain == "input_boolean" {
			if entityID, ok := calls[i].ServiceData["entity_id"].(string); ok {
				if entityID == "input_boolean.anyone_home_and_awake" {
					foundCall = &calls[i]
					break
				}
			}
		}
	}

	assert.NotNil(t, foundCall, "Should have made a service call to update anyone_home_and_awake")
	if foundCall != nil {
		assert.Equal(t, "turn_on", foundCall.Service, "Should have called turn_on for anyone_home_and_awake")
		t.Logf("Found service call: %s.%s for %v", foundCall.Domain, foundCall.Service, foundCall.ServiceData["entity_id"])
	}
}

// TestScenario_ComputedState_RapidChanges validates that rapid state changes
// are handled correctly without race conditions
func TestScenario_ComputedState_RapidChanges(t *testing.T) {
	t.Parallel()
	server, _, manager, cleanup := setupComputedStateTest(t)
	defer cleanup()

	// GIVEN: Initial state - owner home and awake
	t.Log("GIVEN: Initial state - owner home and awake")
	server.SetState("input_boolean.any_owner_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	server.SetState("input_boolean.tori_here", "off", map[string]interface{}{})
	// Allow computed state to settle
	time.Sleep(50 * time.Millisecond)

	// WHEN: Rapid state changes occur
	t.Log("WHEN: Rapid state changes occur")

	// Simulate rapid toggling - keep short delays to test rapid change handling
	for i := 0; i < 5; i++ {
		server.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
		time.Sleep(20 * time.Millisecond)
		server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
		time.Sleep(20 * time.Millisecond)
	}

	// Allow final state to settle
	time.Sleep(50 * time.Millisecond)

	// THEN: Final computed state should be correct
	t.Log("THEN: Final computed state should be correct")
	value, err := manager.GetBool("isAnyoneHomeAndAwake")
	require.NoError(t, err)
	assert.True(t, value, "Should be true after rapid changes settle (owner home and awake)")

	// Test completed without deadlock or panic
	t.Log("SUCCESS: Handled rapid changes without errors")
}

// TestScenario_ComputedState_BothDependenciesChange validates behavior when
// both dependencies change in quick succession
func TestScenario_ComputedState_BothDependenciesChange(t *testing.T) {
	t.Parallel()
	server, _, manager, cleanup := setupComputedStateTest(t)
	defer cleanup()

	// GIVEN: No owner home, nobody asleep
	t.Log("GIVEN: No owner home, nobody asleep")
	server.SetState("input_boolean.any_owner_home", "off", map[string]interface{}{})
	server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	server.SetState("input_boolean.tori_here", "off", map[string]interface{}{})

	waitForBoolState(t, manager, "isAnyoneHomeAndAwake", false, "Initial state: should be false")

	// WHEN: Both dependencies change almost simultaneously
	t.Log("WHEN: Owner comes home AND someone falls asleep almost simultaneously")
	server.SetState("input_boolean.any_owner_home", "on", map[string]interface{}{})
	// Brief delay to simulate near-simultaneous changes
	time.Sleep(20 * time.Millisecond)
	server.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})

	// THEN: Final state should be false (owner home but asleep)
	t.Log("THEN: Should be false (owner home but asleep)")
	waitForBoolState(t, manager, "isAnyoneHomeAndAwake", false, "Should be false when owner home but asleep")

	// WHEN: Wake up then leave
	t.Log("WHEN: Everyone wakes up then owner leaves")
	server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	// Brief delay to simulate near-simultaneous changes
	time.Sleep(20 * time.Millisecond)
	server.SetState("input_boolean.any_owner_home", "off", map[string]interface{}{})

	// THEN: Final state should be false (no owner home)
	t.Log("THEN: Should be false (no owner home)")
	waitForBoolState(t, manager, "isAnyoneHomeAndAwake", false, "Should be false when no owner is home")
}

// TestScenario_ComputedState_ToriArrivesWhileOwnerAsleep validates the bug fix:
// when Tori arrives while owners are asleep, isAnyoneHomeAndAwake should become true
func TestScenario_ComputedState_ToriArrivesWhileOwnerAsleep(t *testing.T) {
	t.Parallel()
	server, _, manager, cleanup := setupComputedStateTest(t)
	defer cleanup()

	// GIVEN: Owner is home and asleep
	t.Log("GIVEN: Owner is home and asleep")
	server.SetState("input_boolean.any_owner_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	server.SetState("input_boolean.tori_here", "off", map[string]interface{}{})

	// Verify initial state: should be false (owner asleep)
	waitForBoolState(t, manager, "isAnyoneHomeAndAwake", false, "Initially should be false when owner is home but asleep")

	// WHEN: Tori arrives while owner is still asleep
	t.Log("WHEN: Tori arrives while owner is still asleep")
	server.SetState("input_boolean.tori_here", "on", map[string]interface{}{})

	// THEN: isAnyoneHomeAndAwake should become TRUE (Tori is awake!)
	t.Log("THEN: isAnyoneHomeAndAwake should become TRUE")
	waitForBoolState(t, manager, "isAnyoneHomeAndAwake", true, "Should be TRUE when Tori arrives, even if owner is asleep (BUG FIX)")

	// WHEN: Tori leaves
	t.Log("WHEN: Tori leaves")
	server.SetState("input_boolean.tori_here", "off", map[string]interface{}{})

	// THEN: isAnyoneHomeAndAwake should become false again (owner still asleep)
	t.Log("THEN: isAnyoneHomeAndAwake should become false again")
	waitForBoolState(t, manager, "isAnyoneHomeAndAwake", false, "Should be false when Tori leaves and owner is still asleep")
}
