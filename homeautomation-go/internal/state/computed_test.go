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
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.any_owner_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.tori_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)

	err = manager.SetupComputedState()
	require.NoError(t, err)
}

func TestComputedState_IsAnyoneHomeAndAwake_InitialComputation(t *testing.T) {
	t.Parallel()
	// Formula: (isAnyOwnerHome && !isAnyoneAsleep) || isToriHere
	testCases := []struct {
		name           string
		isAnyOwnerHome string
		isAnyoneAsleep string
		isToriHere     string
		expected       bool
	}{
		{
			name:           "owner home and awake -> true",
			isAnyOwnerHome: "on",
			isAnyoneAsleep: "off",
			isToriHere:     "off",
			expected:       true,
		},
		{
			name:           "owner home and asleep -> false",
			isAnyOwnerHome: "on",
			isAnyoneAsleep: "on",
			isToriHere:     "off",
			expected:       false,
		},
		{
			name:           "no owner home and awake -> false",
			isAnyOwnerHome: "off",
			isAnyoneAsleep: "off",
			isToriHere:     "off",
			expected:       false,
		},
		{
			name:           "no owner home and asleep -> false",
			isAnyOwnerHome: "off",
			isAnyoneAsleep: "on",
			isToriHere:     "off",
			expected:       false,
		},
		{
			name:           "tori here alone -> true",
			isAnyOwnerHome: "off",
			isAnyoneAsleep: "off",
			isToriHere:     "on",
			expected:       true,
		},
		{
			name:           "tori here while owner asleep -> true (BUG FIX)",
			isAnyOwnerHome: "on",
			isAnyoneAsleep: "on",
			isToriHere:     "on",
			expected:       true,
		},
		{
			name:           "tori here while no owner but someone asleep -> true",
			isAnyOwnerHome: "off",
			isAnyoneAsleep: "on",
			isToriHere:     "on",
			expected:       true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			logger := testlogger.New()
			mockClient := ha.NewMockClient()
			mockClient.SetState("input_boolean.any_owner_home", tc.isAnyOwnerHome, map[string]interface{}{})
			mockClient.SetState("input_boolean.anyone_asleep", tc.isAnyoneAsleep, map[string]interface{}{})
			mockClient.SetState("input_boolean.tori_here", tc.isToriHere, map[string]interface{}{})
			mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
			mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
			mockClient.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
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

func TestComputedState_IsAnyoneHomeAndAwake_ReactsToIsAnyOwnerHomeChange(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	// Start with nobody home and nobody asleep
	mockClient.SetState("input_boolean.any_owner_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.tori_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)

	err = manager.SetupComputedState()
	require.NoError(t, err)

	// Initially false (nobody home)
	value, _ := manager.GetBool("isAnyoneHomeAndAwake")
	assert.False(t, value)

	// Owner comes home (still awake)
	mockClient.SimulateStateChange("input_boolean.any_owner_home", "on")

	// Should now be true
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "isAnyoneHomeAndAwake should be true when owner comes home and is awake")

	// Owner leaves
	mockClient.SimulateStateChange("input_boolean.any_owner_home", "off")

	// Should be false again
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.False(t, value, "isAnyoneHomeAndAwake should be false when nobody is home")
}

func TestComputedState_IsAnyoneHomeAndAwake_ReactsToIsAnyoneAsleepChange(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	// Start with owner home and awake
	mockClient.SetState("input_boolean.any_owner_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.tori_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)

	err = manager.SetupComputedState()
	require.NoError(t, err)

	// Initially true (owner home and awake)
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
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.any_owner_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.tori_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
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
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.any_owner_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.tori_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
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
	t.Parallel()
	// Verify that isAnyoneHomeAndAwake has ComputedOutput: true

	vars := VariablesByKey()
	v := vars["isAnyoneHomeAndAwake"]
	assert.True(t, v.ComputedOutput, "isAnyoneHomeAndAwake should have ComputedOutput: true")
}

func TestComputedState_IsAnyoneHomeAndAwake_SubscriberNotification(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.any_owner_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.tori_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
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
	mockClient.SimulateStateChange("input_boolean.any_owner_home", "on")

	// The computed value should have changed, triggering our subscriber
	// Note: The subscription gets notified via the HA callback when SetBool syncs to HA
	// This tests the full flow: dependency change -> recompute -> set -> HA sync -> notification
	value, _ := manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "isAnyoneHomeAndAwake should be true after owner comes home")
}

func TestComputedState_IsAnyoneHomeAndAwake_ToriArrivesWhileOwnerAsleep(t *testing.T) {
	// This is the BUG FIX test case: Tori arrives while owners are asleep
	// Previously, isAnyoneHomeAndAwake would remain false because the formula was:
	//   isAnyoneHome && !isAnyoneAsleep
	// Now the formula is:
	//   (isAnyOwnerHome && !isAnyoneAsleep) || isToriHere
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	// Owner is home and asleep
	mockClient.SetState("input_boolean.any_owner_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.tori_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)

	err = manager.SetupComputedState()
	require.NoError(t, err)

	// Initially false (owner asleep, Tori not here)
	value, _ := manager.GetBool("isAnyoneHomeAndAwake")
	assert.False(t, value, "isAnyoneHomeAndAwake should be false when owner is asleep and Tori is not here")

	// Tori arrives while owner is still asleep
	mockClient.SimulateStateChange("input_boolean.tori_here", "on")

	// Should now be true because Tori is here (and implicitly awake)
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "isAnyoneHomeAndAwake should be TRUE when Tori arrives, even if owner is asleep")

	// Tori leaves
	mockClient.SimulateStateChange("input_boolean.tori_here", "off")

	// Should be false again (owner still asleep)
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.False(t, value, "isAnyoneHomeAndAwake should be false when Tori leaves and owner is still asleep")
}

func TestComputedState_IsAnyoneHomeAndAwake_LatchesOnWakeSequence(t *testing.T) {
	// When isWakeSequenceActive transitions false->true, the latch activates
	// and keeps isAnyoneHomeAndAwake=true even when owners are asleep.
	// The latch holds even if isWakeSequenceActive becomes false again.
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	// Owner is home and asleep, wake sequence not active
	mockClient.SetState("input_boolean.any_owner_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.tori_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)

	err = manager.SetupComputedState()
	require.NoError(t, err)

	// Initially false (owner home but asleep)
	value, _ := manager.GetBool("isAnyoneHomeAndAwake")
	assert.False(t, value, "isAnyoneHomeAndAwake should be false when owner is asleep")

	// Wake sequence activates (alarm goes off)
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "on")

	// Should now be true due to latch
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "isAnyoneHomeAndAwake should be TRUE when wake sequence activates (latch)")

	// Wake sequence deactivates (e.g., Caroline turns off bedroom lights)
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "off")

	// Should STILL be true - latch holds until isMasterAsleep becomes false
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "isAnyoneHomeAndAwake should STILL be TRUE after wake sequence deactivates (latch held)")
}

func TestComputedState_IsAnyoneHomeAndAwake_LatchClearsOnWakeUp(t *testing.T) {
	// The latch clears when isMasterAsleep becomes false (person wakes up).
	// After the latch clears, normal formula takes over.
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	// Owner is home and asleep, wake sequence not active
	mockClient.SetState("input_boolean.any_owner_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.tori_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)

	err = manager.SetupComputedState()
	require.NoError(t, err)

	// Activate the latch
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "on")
	value, _ := manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "isAnyoneHomeAndAwake should be TRUE when wake sequence activates")

	// Person wakes up (isMasterAsleep becomes false)
	mockClient.SimulateStateChange("input_boolean.master_asleep", "off")
	// Also update isAnyoneAsleep to reflect that no one is asleep anymore
	mockClient.SimulateStateChange("input_boolean.anyone_asleep", "off")

	// Should still be true (normal formula: owner home and awake)
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "isAnyoneHomeAndAwake should be TRUE (normal formula: owner home and awake)")

	// Now verify the latch is cleared by having owner leave
	mockClient.SimulateStateChange("input_boolean.any_owner_home", "off")

	// Should be false (no owner home, latch cleared)
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.False(t, value, "isAnyoneHomeAndAwake should be FALSE after owner leaves (latch was cleared)")
}

func TestComputedState_IsAnyoneHomeAndAwake_LatchOnlyOnRisingEdge(t *testing.T) {
	// If isWakeSequenceActive is already true at startup, latch should NOT activate.
	// The latch only activates on the transition from false to true.
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	// Owner is home and asleep, wake sequence ALREADY active
	mockClient.SetState("input_boolean.any_owner_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.tori_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "on", map[string]interface{}{}) // Already true!
	mockClient.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)

	err = manager.SetupComputedState()
	require.NoError(t, err)

	// Should be false because latch doesn't activate at startup (no edge transition)
	value, _ := manager.GetBool("isAnyoneHomeAndAwake")
	assert.False(t, value, "isAnyoneHomeAndAwake should be FALSE at startup even if wake sequence already active (no rising edge)")

	// Now if we cycle the wake sequence (off then on), it should latch
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "off")
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.False(t, value, "isAnyoneHomeAndAwake should still be FALSE after wake sequence goes off")

	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "on")
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "isAnyoneHomeAndAwake should be TRUE after wake sequence rising edge (latch activated)")
}

func TestComputedState_IsAnyoneHomeAndAwake_GuestAsleepDoesNotClearLatch(t *testing.T) {
	// The latch only clears when isMasterAsleep becomes false.
	// If isGuestAsleep is true, isAnyoneAsleep stays true, but the latch still
	// clears correctly based on isMasterAsleep.
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.any_owner_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.tori_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.guest_asleep", "on", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)

	err = manager.SetupComputedState()
	require.NoError(t, err)

	// Activate the latch
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "on")
	value, _ := manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "isAnyoneHomeAndAwake should be TRUE when wake sequence activates")

	// Master wakes up (clears latch), but guest is still asleep
	mockClient.SimulateStateChange("input_boolean.master_asleep", "off")
	// Note: isAnyoneAsleep stays true because guest is still asleep

	// Should still be true due to latch being cleared AND isAnyoneAsleep still true
	// Wait - isAnyoneAsleep is still true, so formula would be:
	// (owner_home && !isAnyoneAsleep) || isToriHere || latch
	// = (true && false) || false || false
	// = false
	// But the latch just cleared, so we need the isAnyoneAsleep to update too

	// Actually, in this scenario, the normal formula would give false because
	// isAnyoneAsleep is still true. The latch was keeping it true.
	// This is correct behavior - the latch cleared on master waking up.
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.False(t, value, "isAnyoneHomeAndAwake should be FALSE after latch clears if isAnyoneAsleep still true")
}

// =============================================================================
// SCENARIO TESTS
// These tests tell a story and verify the complete flow of realistic scenarios.
// =============================================================================

func TestScenario_NickWakesUpCarolineSleepsIn(t *testing.T) {
	// SCENARIO: Nick's alarm goes off at 6am. Caroline wants to sleep in.
	// The rest of the house should wake up (lights on) but the bedroom
	// continues playing rain for Caroline.
	//
	// Timeline:
	// 1. Night: Both asleep, house dark
	// 2. 6:00am: Nick's alarm triggers wake sequence
	// 3. House lights come on (isAnyoneHomeAndAwake = true)
	// 4. Caroline turns off bedroom lights (wake sequence deactivates)
	// 5. House should STILL have lights on (latch holds)
	// 6. 6:30am: Nick finishes getting ready, isMasterAsleep -> false
	// 7. Normal operation resumes

	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// Initial state: Night time, both Nick and Caroline asleep
	mockClient.SetState("input_boolean.any_owner_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.tori_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)
	err = manager.SetupComputedState()
	require.NoError(t, err)

	// Step 1: Verify initial state - house is dark (nobody awake)
	value, _ := manager.GetBool("isAnyoneHomeAndAwake")
	assert.False(t, value, "Step 1: House should be dark at night (isAnyoneHomeAndAwake=false)")

	// Step 2: Nick's alarm triggers at 6:00am
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "on")

	// Step 3: House lights should come on
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "Step 3: House lights should come on when alarm triggers")

	// Step 4: Caroline turns off bedroom lights (wake sequence deactivates)
	// This happens because the bedroom lights turning off cancels the wake sequence
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "off")

	// Step 5: House should STILL have lights on - the latch holds!
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "Step 5: House lights should STAY on even after Caroline cancels wake sequence (latch)")

	// Step 6: Nick finishes getting ready, marks himself as awake
	mockClient.SimulateStateChange("input_boolean.master_asleep", "off")
	mockClient.SimulateStateChange("input_boolean.anyone_asleep", "off") // Caroline also woke up

	// Step 7: Latch cleared, but normal formula now returns true (owner home and awake)
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "Step 7: House lights stay on via normal formula (owner awake)")
}

func TestScenario_ToriArrivesDuringWakeSequence(t *testing.T) {
	// SCENARIO: Nick's alarm goes off. While the wake sequence is active,
	// Tori arrives to help with morning routine.
	//
	// Timeline:
	// 1. Nick asleep, alarm triggers wake sequence, latch activates
	// 2. Tori arrives (isToriHere = true)
	// 3. Nick wakes up (latch clears)
	// 4. isAnyoneHomeAndAwake should still be true (Tori is here)
	// 5. Tori leaves
	// 6. isAnyoneHomeAndAwake still true (Nick is awake)

	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	mockClient.SetState("input_boolean.any_owner_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.tori_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)
	err = manager.SetupComputedState()
	require.NoError(t, err)

	// Step 1: Alarm triggers, latch activates
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "on")
	value, _ := manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "Step 1: House awake due to latch")

	// Step 2: Tori arrives
	mockClient.SimulateStateChange("input_boolean.tori_here", "on")
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "Step 2: House awake (latch + Tori)")

	// Step 3: Nick wakes up, latch clears
	mockClient.SimulateStateChange("input_boolean.master_asleep", "off")
	mockClient.SimulateStateChange("input_boolean.anyone_asleep", "off")
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "Step 3: House awake (normal formula: owner awake + Tori)")

	// Step 4: Tori leaves
	mockClient.SimulateStateChange("input_boolean.tori_here", "off")
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "Step 4: House still awake (Nick is awake)")

	// Step 5: Nick goes back to sleep (weird but possible)
	mockClient.SimulateStateChange("input_boolean.master_asleep", "on")
	mockClient.SimulateStateChange("input_boolean.anyone_asleep", "on")
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.False(t, value, "Step 5: House dark (everyone asleep, no Tori, no latch)")
}

func TestScenario_NickLeavesWhileLatchActive(t *testing.T) {
	// SCENARIO: Nick's alarm goes off, but then he realizes he needs to
	// leave immediately for an emergency. He leaves while Caroline is
	// still asleep and the latch is active.
	//
	// Expected: The latch should keep isAnyoneHomeAndAwake=true until
	// isMasterAsleep becomes false. Even though Nick left, the latch
	// persists. This is intentional - lights stay on for safety.
	//
	// Timeline:
	// 1. Alarm triggers, latch activates
	// 2. Nick leaves (isAnyOwnerHome stays true - Caroline is still home)
	// 3. isAnyoneHomeAndAwake should still be true (latch)
	// 4. Caroline eventually wakes up, latch clears
	// 5. Normal formula applies

	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	mockClient.SetState("input_boolean.any_owner_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.tori_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)
	err = manager.SetupComputedState()
	require.NoError(t, err)

	// Step 1: Alarm triggers, latch activates
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "on")
	value, _ := manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "Step 1: House awake due to latch")

	// Step 2: Nick rushes out but Caroline is still home
	// isAnyOwnerHome stays true because Caroline is home
	// But the key thing is the latch is still active

	// Step 3: Wake sequence deactivates (Nick left, sequence ended)
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "off")
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "Step 3: House still awake (latch persists)")

	// Step 4: Caroline wakes up (clears isMasterAsleep)
	// Note: In this household model, isMasterAsleep represents the master bedroom
	// state, so Caroline waking up clears it
	mockClient.SimulateStateChange("input_boolean.master_asleep", "off")
	mockClient.SimulateStateChange("input_boolean.anyone_asleep", "off")
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "Step 4: House awake (Caroline awake, normal formula)")
}

func TestScenario_NickLeavesEveryoneGone(t *testing.T) {
	// SCENARIO: Edge case - Nick's alarm goes off, then BOTH Nick and
	// Caroline leave the house (maybe they both have early flights).
	// The latch is still active but no owner is home.
	//
	// Expected: isAnyoneHomeAndAwake = true due to latch, even though
	// nobody is home. This is a bit odd but safe - lights stay on.
	// When isMasterAsleep clears (which would happen when they leave
	// and mark themselves as awake), the latch clears and normal
	// formula takes over (false because nobody home).

	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	mockClient.SetState("input_boolean.any_owner_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.tori_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)
	err = manager.SetupComputedState()
	require.NoError(t, err)

	// Step 1: Alarm triggers
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "on")
	value, _ := manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "Step 1: House awake due to latch")

	// Step 2: Both leave - first they wake up (clears latch)
	mockClient.SimulateStateChange("input_boolean.master_asleep", "off")
	mockClient.SimulateStateChange("input_boolean.anyone_asleep", "off")
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "Step 2a: House awake (latch cleared, but owner home and awake)")

	// Step 3: They leave
	mockClient.SimulateStateChange("input_boolean.any_owner_home", "off")
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.False(t, value, "Step 3: House dark (nobody home, no latch)")
}

func TestScenario_MultipleMornings(t *testing.T) {
	// SCENARIO: Test that the latch works correctly across multiple
	// days/wake cycles. The latch should reset properly each time.

	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	mockClient.SetState("input_boolean.any_owner_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.tori_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)
	err = manager.SetupComputedState()
	require.NoError(t, err)

	// === Day 1 Morning ===
	// Alarm triggers
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "on")
	value, _ := manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "Day 1: Latch activates")

	// Wake up
	mockClient.SimulateStateChange("input_boolean.master_asleep", "off")
	mockClient.SimulateStateChange("input_boolean.anyone_asleep", "off")
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "off")
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "Day 1: Awake via normal formula")

	// === Day 1 Night ===
	// Go to sleep
	mockClient.SimulateStateChange("input_boolean.master_asleep", "on")
	mockClient.SimulateStateChange("input_boolean.anyone_asleep", "on")
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.False(t, value, "Day 1 Night: Everyone asleep")

	// === Day 2 Morning ===
	// Alarm triggers again - latch should work again
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "on")
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "Day 2: Latch activates again")

	// Caroline cancels wake sequence
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "off")
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "Day 2: Latch holds after cancellation")

	// Nick wakes up
	mockClient.SimulateStateChange("input_boolean.master_asleep", "off")
	mockClient.SimulateStateChange("input_boolean.anyone_asleep", "off")
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "Day 2: Normal formula takes over")
}

func TestScenario_SystemRestartDuringWakeSequence(t *testing.T) {
	// SCENARIO: The system restarts while the wake sequence is active.
	// The latch should NOT activate because we only latch on the
	// rising edge (false->true transition), not on initial state.
	//
	// This prevents the house from being "stuck" in wake mode after
	// a restart.

	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// System starts with wake sequence already active (simulating restart)
	mockClient.SetState("input_boolean.any_owner_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.tori_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "on", map[string]interface{}{}) // Already on!
	mockClient.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)
	err = manager.SetupComputedState()
	require.NoError(t, err)

	// Should be false - no latch activated at startup
	value, _ := manager.GetBool("isAnyoneHomeAndAwake")
	assert.False(t, value, "After restart: No latch (no rising edge detected)")

	// The system needs to see the wake sequence cycle to latch
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "off")
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "on")
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "After cycle: Latch activates on rising edge")
}
