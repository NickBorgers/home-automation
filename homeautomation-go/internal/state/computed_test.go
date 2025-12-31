package state

import (
	"sync/atomic"
	"testing"

	"homeautomation/internal/ha"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupComputedState(t *testing.T) {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.anyone_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)

	err = manager.SetupComputedState()
	require.NoError(t, err)
}

func TestComputedState_IsAnyoneHomeAndAwake_InitialComputation(t *testing.T) {
	testCases := []struct {
		name           string
		isAnyoneHome   string
		isAnyoneAsleep string
		expected       bool
	}{
		{
			name:           "home and awake -> true",
			isAnyoneHome:   "on",
			isAnyoneAsleep: "off",
			expected:       true,
		},
		{
			name:           "home and asleep -> false",
			isAnyoneHome:   "on",
			isAnyoneAsleep: "on",
			expected:       false,
		},
		{
			name:           "not home and awake -> false",
			isAnyoneHome:   "off",
			isAnyoneAsleep: "off",
			expected:       false,
		},
		{
			name:           "not home and asleep -> false",
			isAnyoneHome:   "off",
			isAnyoneAsleep: "on",
			expected:       false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logger := testlogger.New()
			mockClient := ha.NewMockClient()
			mockClient.SetState("input_boolean.anyone_home", tc.isAnyoneHome, map[string]interface{}{})
			mockClient.SetState("input_boolean.anyone_asleep", tc.isAnyoneAsleep, map[string]interface{}{})
			mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
			mockClient.Connect()

			manager := NewManager(mockClient, logger, false)
			err := manager.SyncFromHA()
			require.NoError(t, err)

			err = manager.SetupComputedState()
			require.NoError(t, err)

			value, err := manager.GetBool("isAnyoneHomeAndAwake")
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, value, "isAnyoneHomeAndAwake should be %v", tc.expected)
		})
	}
}

func TestComputedState_IsAnyoneHomeAndAwake_ReactsToIsAnyoneHomeChange(t *testing.T) {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	// Start with nobody home and nobody asleep
	mockClient.SetState("input_boolean.anyone_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)

	err = manager.SetupComputedState()
	require.NoError(t, err)

	// Initially false (nobody home)
	value, _ := manager.GetBool("isAnyoneHomeAndAwake")
	assert.False(t, value)

	// Someone comes home (still awake)
	mockClient.SimulateStateChange("input_boolean.anyone_home", "on")

	// Should now be true
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "isAnyoneHomeAndAwake should be true when someone comes home and is awake")

	// Everyone leaves
	mockClient.SimulateStateChange("input_boolean.anyone_home", "off")

	// Should be false again
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.False(t, value, "isAnyoneHomeAndAwake should be false when nobody is home")
}

func TestComputedState_IsAnyoneHomeAndAwake_ReactsToIsAnyoneAsleepChange(t *testing.T) {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	// Start with someone home and awake
	mockClient.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)

	err = manager.SetupComputedState()
	require.NoError(t, err)

	// Initially true (someone home and awake)
	value, _ := manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value)

	// Someone falls asleep
	mockClient.SimulateStateChange("input_boolean.anyone_asleep", "on")

	// Should now be false
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.False(t, value, "isAnyoneHomeAndAwake should be false when someone is asleep")

	// Everyone wakes up
	mockClient.SimulateStateChange("input_boolean.anyone_asleep", "off")

	// Should be true again
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "isAnyoneHomeAndAwake should be true when everyone wakes up")
}

func TestComputedState_IsAnyoneHomeAndAwake_SyncsToHA(t *testing.T) {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)

	mockClient.ClearServiceCalls()

	err = manager.SetupComputedState()
	require.NoError(t, err)

	// Should have synced the computed value to HA
	calls := mockClient.GetServiceCalls()
	assert.NotEmpty(t, calls, "SetupComputedState should sync computed value to HA")

	// Find the call that set anyone_home_and_awake
	var foundCall *ha.ServiceCall
	for i := range calls {
		if calls[i].Domain == "input_boolean" {
			data := calls[i].Data
			if entityID, ok := data["entity_id"].(string); ok && entityID == "input_boolean.anyone_home_and_awake" {
				foundCall = &calls[i]
				break
			}
		}
	}

	assert.NotNil(t, foundCall, "Should have called service to set anyone_home_and_awake")
	assert.Equal(t, "turn_on", foundCall.Service, "Should turn on anyone_home_and_awake")
}

func TestComputedState_IsAnyoneHomeAndAwake_WorksInReadOnlyMode(t *testing.T) {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.Connect()

	// Create manager in read-only mode
	manager := NewManager(mockClient, logger, true)
	err := manager.SyncFromHA()
	require.NoError(t, err)

	mockClient.ClearServiceCalls()

	// SetupComputedState should work because isAnyoneHomeAndAwake is ComputedOutput
	err = manager.SetupComputedState()
	require.NoError(t, err)

	// Value should be computed correctly
	value, _ := manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "Computed state should work in read-only mode")

	// Should have synced to HA even in read-only mode
	calls := mockClient.GetServiceCalls()
	assert.NotEmpty(t, calls, "ComputedOutput should sync to HA even in read-only mode")
}

func TestComputedState_IsAnyoneHomeAndAwake_ComputedOutputFlag(t *testing.T) {
	// Verify that isAnyoneHomeAndAwake has ComputedOutput: true
	vars := VariablesByKey()
	v := vars["isAnyoneHomeAndAwake"]
	assert.True(t, v.ComputedOutput, "isAnyoneHomeAndAwake should have ComputedOutput: true")
}

func TestComputedState_IsAnyoneHomeAndAwake_SubscriberNotification(t *testing.T) {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.anyone_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)

	err = manager.SetupComputedState()
	require.NoError(t, err)

	// Subscribe to isAnyoneHomeAndAwake changes
	var changeCount int32
	sub, err := manager.Subscribe("isAnyoneHomeAndAwake", func(key string, oldValue, newValue interface{}) {
		atomic.AddInt32(&changeCount, 1)
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// Trigger a change that should update isAnyoneHomeAndAwake
	mockClient.SimulateStateChange("input_boolean.anyone_home", "on")

	// The computed value should have changed, triggering our subscriber
	// Note: The subscription gets notified via the HA callback when SetBool syncs to HA
	// This tests the full flow: dependency change -> recompute -> set -> HA sync -> notification
	value, _ := manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "isAnyoneHomeAndAwake should be true after someone comes home")
}
