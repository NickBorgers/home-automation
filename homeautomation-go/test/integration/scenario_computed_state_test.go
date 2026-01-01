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
// - isAnyoneHomeAndAwake = isAnyoneHome && !isAnyoneAsleep
// ============================================================================

// setupComputedStateTest creates a test environment with computed state initialized
func setupComputedStateTest(t *testing.T) (*MockHAServer, *ha.Client, *state.Manager, func()) {
	logger := testlogger.New()

	// Start mock HA server
	server := NewMockHAServer(testAddr, testToken)
	server.InitializeStates()

	err := server.Start()
	require.NoError(t, err)

	// Create and connect client
	client := ha.NewClient(fmt.Sprintf("ws://%s/api/websocket", testAddr), testToken, logger)
	err = client.Connect()
	require.NoError(t, err)

	// Create state manager
	manager := state.NewManager(client, logger, false)
	err = manager.SyncFromHA()
	require.NoError(t, err)

	// Initialize computed state - this is the key addition
	err = manager.SetupComputedState()
	require.NoError(t, err)

	// Allow time for subscriptions to be established
	time.Sleep(100 * time.Millisecond)

	cleanup := func() {
		client.Disconnect()
		server.Stop()
	}

	return server, client, manager, cleanup
}

// TestScenario_ComputedState_IsAnyoneHomeAndAwake_InitialComputation validates
// that isAnyoneHomeAndAwake is correctly computed on startup
func TestScenario_ComputedState_IsAnyoneHomeAndAwake_InitialComputation(t *testing.T) {
	testCases := []struct {
		name           string
		isAnyoneHome   string
		isAnyoneAsleep string
		expected       bool
	}{
		{
			name:           "home and awake should be true",
			isAnyoneHome:   "on",
			isAnyoneAsleep: "off",
			expected:       true,
		},
		{
			name:           "home but asleep should be false",
			isAnyoneHome:   "on",
			isAnyoneAsleep: "on",
			expected:       false,
		},
		{
			name:           "not home and awake should be false",
			isAnyoneHome:   "off",
			isAnyoneAsleep: "off",
			expected:       false,
		},
		{
			name:           "not home and asleep should be false",
			isAnyoneHome:   "off",
			isAnyoneAsleep: "on",
			expected:       false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logger := testlogger.New()

			// Start mock HA server with specific initial state
			server := NewMockHAServer(testAddr, testToken)
			server.InitializeStates()

			// Set the initial states before connecting
			server.SetState("input_boolean.anyone_home", tc.isAnyoneHome, map[string]interface{}{})
			server.SetState("input_boolean.anyone_asleep", tc.isAnyoneAsleep, map[string]interface{}{})
			server.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})

			err := server.Start()
			require.NoError(t, err)
			defer server.Stop()

			// Create and connect client
			client := ha.NewClient(fmt.Sprintf("ws://%s/api/websocket", testAddr), testToken, logger)
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

			time.Sleep(50 * time.Millisecond)

			// THEN: isAnyoneHomeAndAwake should be computed correctly
			value, err := manager.GetBool("isAnyoneHomeAndAwake")
			require.NoError(t, err)
			assert.Equal(t, tc.expected, value,
				"isAnyoneHomeAndAwake should be %v when isAnyoneHome=%s and isAnyoneAsleep=%s",
				tc.expected, tc.isAnyoneHome, tc.isAnyoneAsleep)
		})
	}
}

// TestScenario_ComputedState_ReactsToIsAnyoneHomeChange validates that
// isAnyoneHomeAndAwake updates when isAnyoneHome changes
func TestScenario_ComputedState_ReactsToIsAnyoneHomeChange(t *testing.T) {
	server, _, manager, cleanup := setupComputedStateTest(t)
	defer cleanup()

	// GIVEN: Nobody is home and nobody is asleep
	t.Log("GIVEN: Nobody is home and nobody is asleep")
	server.SetState("input_boolean.anyone_home", "off", map[string]interface{}{})
	server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	time.Sleep(50 * time.Millisecond)

	// Verify initial state
	value, err := manager.GetBool("isAnyoneHomeAndAwake")
	require.NoError(t, err)
	assert.False(t, value, "Initially should be false when nobody is home")

	// WHEN: Someone comes home (still awake)
	t.Log("WHEN: Someone comes home")
	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	time.Sleep(50 * time.Millisecond)

	// THEN: isAnyoneHomeAndAwake should become true
	t.Log("THEN: isAnyoneHomeAndAwake should become true")
	value, err = manager.GetBool("isAnyoneHomeAndAwake")
	require.NoError(t, err)
	assert.True(t, value, "Should be true when someone is home and awake")

	// WHEN: Everyone leaves
	t.Log("WHEN: Everyone leaves")
	server.SetState("input_boolean.anyone_home", "off", map[string]interface{}{})
	time.Sleep(50 * time.Millisecond)

	// THEN: isAnyoneHomeAndAwake should become false
	t.Log("THEN: isAnyoneHomeAndAwake should become false")
	value, err = manager.GetBool("isAnyoneHomeAndAwake")
	require.NoError(t, err)
	assert.False(t, value, "Should be false when nobody is home")
}

// TestScenario_ComputedState_ReactsToIsAnyoneAsleepChange validates that
// isAnyoneHomeAndAwake updates when isAnyoneAsleep changes
func TestScenario_ComputedState_ReactsToIsAnyoneAsleepChange(t *testing.T) {
	server, _, manager, cleanup := setupComputedStateTest(t)
	defer cleanup()

	// GIVEN: Someone is home and awake
	t.Log("GIVEN: Someone is home and awake")
	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	time.Sleep(50 * time.Millisecond)

	// Verify initial state
	value, err := manager.GetBool("isAnyoneHomeAndAwake")
	require.NoError(t, err)
	assert.True(t, value, "Initially should be true when someone is home and awake")

	// WHEN: Someone falls asleep
	t.Log("WHEN: Someone falls asleep")
	server.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	time.Sleep(50 * time.Millisecond)

	// THEN: isAnyoneHomeAndAwake should become false
	t.Log("THEN: isAnyoneHomeAndAwake should become false")
	value, err = manager.GetBool("isAnyoneHomeAndAwake")
	require.NoError(t, err)
	assert.False(t, value, "Should be false when someone is asleep")

	// WHEN: Everyone wakes up
	t.Log("WHEN: Everyone wakes up")
	server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	time.Sleep(50 * time.Millisecond)

	// THEN: isAnyoneHomeAndAwake should become true again
	t.Log("THEN: isAnyoneHomeAndAwake should become true again")
	value, err = manager.GetBool("isAnyoneHomeAndAwake")
	require.NoError(t, err)
	assert.True(t, value, "Should be true again when everyone wakes up")
}

// TestScenario_ComputedState_SyncsToHomeAssistant validates that computed
// state changes are synced back to Home Assistant
func TestScenario_ComputedState_SyncsToHomeAssistant(t *testing.T) {
	server, _, _, cleanup := setupComputedStateTest(t)
	defer cleanup()

	// GIVEN: Nobody is home initially
	t.Log("GIVEN: Nobody is home initially")
	server.SetState("input_boolean.anyone_home", "off", map[string]interface{}{})
	server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	time.Sleep(50 * time.Millisecond)

	// Clear service calls to track new ones
	server.ClearServiceCalls()

	// WHEN: Someone comes home (triggering computed state change)
	t.Log("WHEN: Someone comes home")
	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	time.Sleep(100 * time.Millisecond)

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
	server, _, manager, cleanup := setupComputedStateTest(t)
	defer cleanup()

	// GIVEN: Initial state - someone home and awake
	t.Log("GIVEN: Initial state - someone home and awake")
	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	time.Sleep(50 * time.Millisecond)

	// WHEN: Rapid state changes occur
	t.Log("WHEN: Rapid state changes occur")

	// Simulate rapid toggling
	for i := 0; i < 5; i++ {
		server.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
		time.Sleep(50 * time.Millisecond)
		server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
		time.Sleep(50 * time.Millisecond)
	}

	// Final state: home and awake
	time.Sleep(50 * time.Millisecond)

	// THEN: Final computed state should be correct
	t.Log("THEN: Final computed state should be correct")
	value, err := manager.GetBool("isAnyoneHomeAndAwake")
	require.NoError(t, err)
	assert.True(t, value, "Should be true after rapid changes settle (home and awake)")

	// Test completed without deadlock or panic
	t.Log("SUCCESS: Handled rapid changes without errors")
}

// TestScenario_ComputedState_BothDependenciesChange validates behavior when
// both dependencies change in quick succession
func TestScenario_ComputedState_BothDependenciesChange(t *testing.T) {
	server, _, manager, cleanup := setupComputedStateTest(t)
	defer cleanup()

	// GIVEN: Nobody home, nobody asleep
	t.Log("GIVEN: Nobody home, nobody asleep")
	server.SetState("input_boolean.anyone_home", "off", map[string]interface{}{})
	server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	time.Sleep(50 * time.Millisecond)

	value, _ := manager.GetBool("isAnyoneHomeAndAwake")
	assert.False(t, value, "Initial state: should be false")

	// WHEN: Both dependencies change almost simultaneously
	t.Log("WHEN: Someone comes home AND someone falls asleep almost simultaneously")
	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	time.Sleep(20 * time.Millisecond)
	server.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	time.Sleep(50 * time.Millisecond)

	// THEN: Final state should be false (home but asleep)
	t.Log("THEN: Should be false (home but asleep)")
	value, err := manager.GetBool("isAnyoneHomeAndAwake")
	require.NoError(t, err)
	assert.False(t, value, "Should be false when home but asleep")

	// WHEN: Wake up then leave
	t.Log("WHEN: Everyone wakes up then leaves")
	server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	time.Sleep(20 * time.Millisecond)
	server.SetState("input_boolean.anyone_home", "off", map[string]interface{}{})
	time.Sleep(50 * time.Millisecond)

	// THEN: Final state should be false (not home)
	t.Log("THEN: Should be false (not home)")
	value, err = manager.GetBool("isAnyoneHomeAndAwake")
	require.NoError(t, err)
	assert.False(t, value, "Should be false when nobody is home")
}
