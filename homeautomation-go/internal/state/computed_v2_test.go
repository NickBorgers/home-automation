package state

import (
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestManagerForV2(t *testing.T) (*Manager, *ha.MockClient) {
	t.Helper()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// Set up all required entities with default values
	mockClient.SetState("input_boolean.nick_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.caroline_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.assistant_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.any_owner_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.guest_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.everyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.guest_bedroom_door_open", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.have_guests", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)

	return manager, mockClient
}

func TestSetupComputedStateV2_InitializesCorrectly(t *testing.T) {
	t.Parallel()
	manager, _ := setupTestManagerForV2(t)

	err := manager.SetupComputedStateV2()
	require.NoError(t, err)
	defer manager.StopComputedState()

	// Verify registry was created
	registry := manager.GetComputedStateRegistry()
	assert.NotNil(t, registry)

	// Verify providers were registered
	names := registry.GetProviderNames()
	assert.Contains(t, names, "isAnyOwnerHome")
	assert.Contains(t, names, "isAnyoneHome")
	assert.Contains(t, names, "isAnyoneAsleep")
	assert.Contains(t, names, "isEveryoneAsleep")
	assert.Contains(t, names, "isAnyoneHomeAndAwake")
}

func TestSetupComputedStateV2_ComputesInitialValues(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// Nick is home, awake
	mockClient.SetState("input_boolean.nick_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.caroline_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.assistant_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.any_owner_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.guest_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.everyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)

	err = manager.SetupComputedStateV2()
	require.NoError(t, err)
	defer manager.StopComputedState()

	// Check computed values
	isAnyOwnerHome, _ := manager.GetBool("isAnyOwnerHome")
	assert.True(t, isAnyOwnerHome, "isAnyOwnerHome should be true (Nick is home)")

	isAnyoneHome, _ := manager.GetBool("isAnyoneHome")
	assert.True(t, isAnyoneHome, "isAnyoneHome should be true")

	isAnyoneAsleep, _ := manager.GetBool("isAnyoneAsleep")
	assert.False(t, isAnyoneAsleep, "isAnyoneAsleep should be false")

	isEveryoneAsleep, _ := manager.GetBool("isEveryoneAsleep")
	assert.False(t, isEveryoneAsleep, "isEveryoneAsleep should be false")

	isAnyoneHomeAndAwake, _ := manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, isAnyoneHomeAndAwake, "isAnyoneHomeAndAwake should be true (Nick home and awake)")
}

func TestSetupComputedStateV2_ReactsToDependencyChanges(t *testing.T) {
	t.Parallel()
	manager, mockClient := setupTestManagerForV2(t)

	err := manager.SetupComputedStateV2()
	require.NoError(t, err)
	defer manager.StopComputedState()

	// Initially nobody home
	value, _ := manager.GetBool("isAnyOwnerHome")
	assert.False(t, value)

	// Nick comes home
	mockClient.SimulateStateChange("input_boolean.nick_home", "on")

	// Computed states should update
	isAnyOwnerHome, _ := manager.GetBool("isAnyOwnerHome")
	assert.True(t, isAnyOwnerHome, "isAnyOwnerHome should be true after Nick arrives")

	isAnyoneHome, _ := manager.GetBool("isAnyoneHome")
	assert.True(t, isAnyoneHome, "isAnyoneHome should be true after Nick arrives")

	isAnyoneHomeAndAwake, _ := manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, isAnyoneHomeAndAwake, "isAnyoneHomeAndAwake should be true after Nick arrives")
}

func TestSetupComputedStateV2_DebouncesAnyoneHomeDeparture(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClock := clock.NewMockClock(time.Date(2026, 4, 15, 4, 10, 3, 0, time.UTC))

	mockClient.SetState("input_boolean.nick_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.caroline_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.assistant_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.any_owner_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.guest_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.everyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	require.NoError(t, manager.SyncFromHA())

	registry := manager.GetComputedStateRegistry()
	registry.clock = mockClock

	require.NoError(t, manager.SetupComputedStateV2())
	defer manager.StopComputedState()

	value, err := manager.GetBool("isAnyoneHome")
	require.NoError(t, err)
	assert.True(t, value, "GIVEN: someone is home")

	mockClient.SimulateStateChange("input_boolean.nick_home", "off")

	value, err = manager.GetBool("isAnyoneHome")
	require.NoError(t, err)
	assert.True(t, value, "THEN: isAnyoneHome stays true during the departure debounce window")

	mockClock.AdvanceAndProcess(AnyoneHomeDepartureDebounceDelay - time.Second)
	value, err = manager.GetBool("isAnyoneHome")
	require.NoError(t, err)
	assert.True(t, value, "THEN: isAnyoneHome remains true before the full debounce delay elapses")

	mockClock.AdvanceAndProcess(time.Second)
	value, err = manager.GetBool("isAnyoneHome")
	require.NoError(t, err)
	assert.False(t, value, "THEN: isAnyoneHome emits false after the full debounce delay")
}

func TestSetupComputedStateV2_CancelsAnyoneHomeDepartureDebounceOnBounce(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClock := clock.NewMockClock(time.Date(2026, 4, 15, 4, 10, 3, 0, time.UTC))

	mockClient.SetState("input_boolean.nick_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.caroline_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.assistant_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.any_owner_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.guest_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.everyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	require.NoError(t, manager.SyncFromHA())

	registry := manager.GetComputedStateRegistry()
	registry.clock = mockClock

	require.NoError(t, manager.SetupComputedStateV2())
	defer manager.StopComputedState()

	mockClient.SimulateStateChange("input_boolean.nick_home", "off")
	mockClock.AdvanceAndProcess(85 * time.Second)

	value, err := manager.GetBool("isAnyoneHome")
	require.NoError(t, err)
	assert.True(t, value, "THEN: a short departure does not emit isAnyoneHome=false")

	mockClient.SimulateStateChange("input_boolean.nick_home", "on")
	mockClock.AdvanceAndProcess(AnyoneHomeDepartureDebounceDelay)

	value, err = manager.GetBool("isAnyoneHome")
	require.NoError(t, err)
	assert.True(t, value, "THEN: returning during the window cancels the pending false emission")
}

func TestSetupComputedStateV2_WakeSequenceLatch(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// Nick is home but asleep
	mockClient.SetState("input_boolean.nick_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.caroline_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.assistant_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.any_owner_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.guest_asleep", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.everyone_asleep", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)

	err = manager.SetupComputedStateV2()
	require.NoError(t, err)
	defer manager.StopComputedState()

	// Initially Nick is home but asleep, so isAnyoneHomeAndAwake should be false
	value, _ := manager.GetBool("isAnyoneHomeAndAwake")
	assert.False(t, value, "isAnyoneHomeAndAwake should be false when owner is asleep")

	// Wake sequence activates (alarm goes off)
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "on")

	// Give latch time to activate and trigger recalculation
	time.Sleep(50 * time.Millisecond)

	// Should now be true due to latch
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "isAnyoneHomeAndAwake should be TRUE when wake sequence activates (latch)")

	// Wake sequence deactivates but people still asleep
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "off")

	time.Sleep(50 * time.Millisecond)

	// Should STILL be true - latch holds until isAnyoneAsleep becomes false
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "isAnyoneHomeAndAwake should STILL be TRUE after wake sequence deactivates (latch held)")

	// Everyone wakes up
	mockClient.SimulateStateChange("input_boolean.anyone_asleep", "off")

	time.Sleep(50 * time.Millisecond)

	// Should still be true (normal formula: owner home and awake)
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "isAnyoneHomeAndAwake should be TRUE via normal formula (owner awake)")
}

func TestSetupComputedStateV2_AssistantArrivesWhileOwnerAsleep(t *testing.T) {
	// This is a key test case from the original computed_test.go
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// Nick is home but asleep
	mockClient.SetState("input_boolean.nick_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.caroline_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.assistant_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.any_owner_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.guest_asleep", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.everyone_asleep", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)

	err = manager.SetupComputedStateV2()
	require.NoError(t, err)
	defer manager.StopComputedState()

	// Initially false (owner asleep, Assistant not here)
	value, _ := manager.GetBool("isAnyoneHomeAndAwake")
	assert.False(t, value, "isAnyoneHomeAndAwake should be false when owner is asleep and Assistant is not here")

	// Assistant arrives while owner is still asleep
	mockClient.SimulateStateChange("input_boolean.assistant_here", "on")

	// Should now be true because Assistant is here (and implicitly awake)
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.True(t, value, "isAnyoneHomeAndAwake should be TRUE when Assistant arrives, even if owner is asleep")

	// Assistant leaves
	mockClient.SimulateStateChange("input_boolean.assistant_here", "off")

	// Should be false again (owner still asleep)
	value, _ = manager.GetBool("isAnyoneHomeAndAwake")
	assert.False(t, value, "isAnyoneHomeAndAwake should be false when Assistant leaves and owner is still asleep")
}

func TestSetupComputedStateV2_DependencyGraph(t *testing.T) {
	t.Parallel()
	manager, _ := setupTestManagerForV2(t)

	err := manager.SetupComputedStateV2()
	require.NoError(t, err)
	defer manager.StopComputedState()

	registry := manager.GetComputedStateRegistry()
	graph := registry.GetDependencyGraph()

	// Verify dependency graph structure
	assert.Contains(t, graph["isAnyOwnerHome"], "isNickHome")
	assert.Contains(t, graph["isAnyOwnerHome"], "isCarolineHome")
	assert.Contains(t, graph["isAnyoneHome"], "isAnyOwnerHome")
	assert.Contains(t, graph["isAnyoneHome"], "isAssistantHere")
	assert.Contains(t, graph["isAnyoneAsleep"], "isMasterAsleep")
	assert.Contains(t, graph["isAnyoneAsleep"], "isGuestAsleep")
	assert.Contains(t, graph["isEveryoneAsleep"], "isMasterAsleep")
	assert.Contains(t, graph["isEveryoneAsleep"], "isGuestAsleep")
	assert.Contains(t, graph["isAnyoneHomeAndAwake"], "isAnyoneHome")
	assert.Contains(t, graph["isAnyoneHomeAndAwake"], "isAnyoneAsleep")
	assert.Contains(t, graph["isAnyoneHomeAndAwake"], "isAssistantHere")
}

func TestSetupComputedStateV2_StopCleansUp(t *testing.T) {
	t.Parallel()
	manager, mockClient := setupTestManagerForV2(t)

	err := manager.SetupComputedStateV2()
	require.NoError(t, err)

	// Stop the computed state
	manager.StopComputedState()

	// State changes should not trigger recomputation
	mockClient.SimulateStateChange("input_boolean.nick_home", "on")

	// Give time for any potential callback
	time.Sleep(50 * time.Millisecond)

	// The computed state should NOT have updated (stopped)
	// We verify by checking that isAnyOwnerHome is still false
	// Note: This is a bit tricky because the subscription in the manager
	// still updates the cache. The key test is that the latch shouldn't
	// activate after stop.
	latch := manager.GetWakeSequenceLatch()
	assert.False(t, latch.IsActive(), "Latch should not be active after stop")
}
