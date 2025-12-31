package state

import (
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
)

func TestDerivedStateHelper_IsAnyoneHome(t *testing.T) {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	manager := NewManager(mockClient, logger, false)

	// Initialize state
	manager.SetBool("isNickHome", false)
	manager.SetBool("isCarolineHome", false)
	manager.SetBool("isAnyoneHome", false)

	helper := NewDerivedStateHelper(manager, logger)
	err := helper.Start()
	assert.NoError(t, err)
	defer helper.Stop()

	// Give subscriptions time to initialize
	time.Sleep(100 * time.Millisecond)

	// Test: Both away -> isAnyoneHome should be false
	isAnyoneHome, _ := manager.GetBool("isAnyoneHome")
	assert.False(t, isAnyoneHome, "isAnyoneHome should be false when both away")

	// Test: Nick arrives -> isAnyoneHome should be true
	manager.SetBool("isNickHome", true)
	time.Sleep(100 * time.Millisecond)
	isAnyoneHome, _ = manager.GetBool("isAnyoneHome")
	assert.True(t, isAnyoneHome, "isAnyoneHome should be true when Nick home")

	// Test: Nick leaves, Caroline arrives -> isAnyoneHome should still be true
	manager.SetBool("isNickHome", false)
	manager.SetBool("isCarolineHome", true)
	time.Sleep(100 * time.Millisecond)
	isAnyoneHome, _ = manager.GetBool("isAnyoneHome")
	assert.True(t, isAnyoneHome, "isAnyoneHome should be true when Caroline home")

	// Test: Both home -> isAnyoneHome should be true
	manager.SetBool("isNickHome", true)
	manager.SetBool("isCarolineHome", true)
	time.Sleep(100 * time.Millisecond)
	isAnyoneHome, _ = manager.GetBool("isAnyoneHome")
	assert.True(t, isAnyoneHome, "isAnyoneHome should be true when both home")

	// Test: Both leave -> isAnyoneHome should be false
	manager.SetBool("isNickHome", false)
	manager.SetBool("isCarolineHome", false)
	time.Sleep(100 * time.Millisecond)
	isAnyoneHome, _ = manager.GetBool("isAnyoneHome")
	assert.False(t, isAnyoneHome, "isAnyoneHome should be false when both away")
}

func TestDerivedStateHelper_IsEveryoneAsleep(t *testing.T) {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	manager := NewManager(mockClient, logger, false)

	// Initialize state - set isHaveGuests to true to test independent sleep states
	manager.SetBool("isHaveGuests", true)
	manager.SetBool("isMasterAsleep", false)
	manager.SetBool("isGuestAsleep", false)
	manager.SetBool("isEveryoneAsleep", false)

	helper := NewDerivedStateHelper(manager, logger)
	err := helper.Start()
	assert.NoError(t, err)
	defer helper.Stop()

	// Give subscriptions time to initialize
	time.Sleep(100 * time.Millisecond)

	// Test: No one asleep -> isEveryoneAsleep should be false
	isEveryoneAsleep, _ := manager.GetBool("isEveryoneAsleep")
	assert.False(t, isEveryoneAsleep, "isEveryoneAsleep should be false when no one asleep")

	// Test: Only master asleep -> isEveryoneAsleep should be false
	manager.SetBool("isMasterAsleep", true)
	time.Sleep(100 * time.Millisecond)
	isEveryoneAsleep, _ = manager.GetBool("isEveryoneAsleep")
	assert.False(t, isEveryoneAsleep, "isEveryoneAsleep should be false when only master asleep")

	// Test: Only guest asleep -> isEveryoneAsleep should be false
	manager.SetBool("isMasterAsleep", false)
	manager.SetBool("isGuestAsleep", true)
	time.Sleep(100 * time.Millisecond)
	isEveryoneAsleep, _ = manager.GetBool("isEveryoneAsleep")
	assert.False(t, isEveryoneAsleep, "isEveryoneAsleep should be false when only guest asleep")

	// Test: Both asleep -> isEveryoneAsleep should be true
	manager.SetBool("isMasterAsleep", true)
	manager.SetBool("isGuestAsleep", true)
	time.Sleep(100 * time.Millisecond)
	isEveryoneAsleep, _ = manager.GetBool("isEveryoneAsleep")
	assert.True(t, isEveryoneAsleep, "isEveryoneAsleep should be true when both asleep")

	// Test: Master wakes up -> isEveryoneAsleep should be false
	manager.SetBool("isMasterAsleep", false)
	time.Sleep(100 * time.Millisecond)
	isEveryoneAsleep, _ = manager.GetBool("isEveryoneAsleep")
	assert.False(t, isEveryoneAsleep, "isEveryoneAsleep should be false when master wakes")
}

func TestDerivedStateHelper_AutoGuestSleep(t *testing.T) {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	manager := NewManager(mockClient, logger, false)

	// Initialize states in mock client
	mockClient.SetState("input_boolean.nick_home", "on", nil)
	mockClient.SetState("input_boolean.caroline_home", "off", nil)
	mockClient.SetState("input_boolean.have_guests", "on", nil)
	mockClient.SetState("input_boolean.guest_asleep", "off", nil)
	mockClient.SetState("input_boolean.guest_bedroom_door_open", "on", nil)

	// Sync states from mock HA
	err := manager.SyncFromHA()
	assert.NoError(t, err)

	helper := NewDerivedStateHelper(manager, logger)
	err = helper.Start()
	assert.NoError(t, err)
	defer helper.Stop()

	// Give subscriptions time to initialize
	time.Sleep(100 * time.Millisecond)

	// Test: Door closes with all conditions met -> guest should auto-sleep
	mockClient.SimulateStateChange("input_boolean.guest_bedroom_door_open", "off")
	time.Sleep(100 * time.Millisecond)

	isGuestAsleep, _ := manager.GetBool("isGuestAsleep")
	assert.True(t, isGuestAsleep, "Guest should be auto-detected as asleep when door closes")
}

func TestDerivedStateHelper_AutoGuestSleepNoOneHome(t *testing.T) {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	manager := NewManager(mockClient, logger, false)

	// Initialize state - NO ONE HOME
	mockClient.SetState("input_boolean.nick_home", "off", nil)
	mockClient.SetState("input_boolean.caroline_home", "off", nil)
	mockClient.SetState("input_boolean.have_guests", "on", nil)
	mockClient.SetState("input_boolean.guest_asleep", "off", nil)
	mockClient.SetState("input_boolean.guest_bedroom_door_open", "on", nil)

	err := manager.SyncFromHA()
	assert.NoError(t, err)

	helper := NewDerivedStateHelper(manager, logger)
	err = helper.Start()
	assert.NoError(t, err)
	defer helper.Stop()

	time.Sleep(100 * time.Millisecond)

	// Test: Door closes but no one home -> guest should NOT auto-sleep
	mockClient.SimulateStateChange("input_boolean.guest_bedroom_door_open", "off")
	time.Sleep(100 * time.Millisecond)

	isGuestAsleep, _ := manager.GetBool("isGuestAsleep")
	assert.False(t, isGuestAsleep, "Guest should NOT auto-sleep when no one is home")
}

func TestDerivedStateHelper_AutoGuestSleepNoGuests(t *testing.T) {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	manager := NewManager(mockClient, logger, false)

	// Initialize state - NO GUESTS
	mockClient.SetState("input_boolean.nick_home", "on", nil)
	mockClient.SetState("input_boolean.caroline_home", "off", nil)
	mockClient.SetState("input_boolean.have_guests", "off", nil)
	mockClient.SetState("input_boolean.guest_asleep", "off", nil)
	mockClient.SetState("input_boolean.guest_bedroom_door_open", "on", nil)

	err := manager.SyncFromHA()
	assert.NoError(t, err)

	helper := NewDerivedStateHelper(manager, logger)
	err = helper.Start()
	assert.NoError(t, err)
	defer helper.Stop()

	time.Sleep(100 * time.Millisecond)

	// Test: Door closes but no guests -> guest should NOT auto-sleep
	mockClient.SimulateStateChange("input_boolean.guest_bedroom_door_open", "off")
	time.Sleep(100 * time.Millisecond)

	isGuestAsleep, _ := manager.GetBool("isGuestAsleep")
	assert.False(t, isGuestAsleep, "Guest should NOT auto-sleep when no guests present")
}

func TestDerivedStateHelper_CallbackFiresOnStartup(t *testing.T) {
	// This test verifies that the callback fires on startup even when
	// derived values already match current state (issue #155)
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	manager := NewManager(mockClient, logger, false)

	// Pre-populate states with values that match what derived computation produces
	// This simulates the startup scenario where HA sync already has correct values
	manager.SetBool("isNickHome", true)
	manager.SetBool("isCarolineHome", false)
	manager.SetBool("isAnyOwnerHome", true) // Already matches: isNickHome || isCarolineHome
	manager.SetBool("isAnyoneHome", true)   // Already matches derived value
	manager.SetBool("isMasterAsleep", false)
	manager.SetBool("isGuestAsleep", false)
	manager.SetBool("isAnyoneAsleep", false)   // Already matches derived value
	manager.SetBool("isEveryoneAsleep", false) // Already matches derived value

	// Track callback invocations
	callbackCount := 0
	var lastAnyOwnerHome, lastAnyoneHome, lastAnyoneAsleep, lastEveryoneAsleep bool

	helper := NewDerivedStateHelper(manager, logger)
	helper.SetUpdateCallback(func(anyOwnerHome, anyoneHome, anyoneAsleep, everyoneAsleep bool) {
		callbackCount++
		lastAnyOwnerHome = anyOwnerHome
		lastAnyoneHome = anyoneHome
		lastAnyoneAsleep = anyoneAsleep
		lastEveryoneAsleep = everyoneAsleep
	})

	err := helper.Start()
	assert.NoError(t, err)
	defer helper.Stop()

	// Give startup time to complete
	time.Sleep(100 * time.Millisecond)

	// Callback should have fired at least once during startup
	assert.GreaterOrEqual(t, callbackCount, 1, "Callback should fire on startup even if values match")

	// Verify callback received correct values
	assert.True(t, lastAnyOwnerHome, "Callback should receive correct isAnyOwnerHome value")
	assert.True(t, lastAnyoneHome, "Callback should receive correct isAnyoneHome value")
	assert.False(t, lastAnyoneAsleep, "Callback should receive correct isAnyoneAsleep value")
	assert.False(t, lastEveryoneAsleep, "Callback should receive correct isEveryoneAsleep value")
}
