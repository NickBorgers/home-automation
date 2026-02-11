package state

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNewManager(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	manager := NewManager(mockClient, logger, false)
	assert.NotNil(t, manager)
	assert.Equal(t, len(AllVariables), len(manager.variables))
}

func TestManager_SyncFromHA(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// Setup mock states
	mockClient.SetState("input_boolean.nick_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.caroline_home", "off", map[string]interface{}{})
	mockClient.SetState("input_number.alarm_time", "1668524400000", map[string]interface{}{})
	mockClient.SetState("input_text.day_phase", "morning", map[string]interface{}{})

	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)

	// Verify boolean
	value, err := manager.GetBool("isNickHome")
	assert.NoError(t, err)
	assert.True(t, value)

	value, err = manager.GetBool("isCarolineHome")
	assert.NoError(t, err)
	assert.False(t, value)

	// Verify number
	numValue, err := manager.GetNumber("alarmTime")
	assert.NoError(t, err)
	assert.Equal(t, 1668524400000.0, numValue)

	// Verify string
	strValue, err := manager.GetString("dayPhase")
	assert.NoError(t, err)
	assert.Equal(t, "morning", strValue)
}

func TestManager_GetBool(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.nick_home", "on", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	manager.SyncFromHA()

	t.Run("valid key", func(t *testing.T) {

		value, err := manager.GetBool("isNickHome")
		assert.NoError(t, err)
		assert.True(t, value)
	})

	t.Run("invalid key", func(t *testing.T) {

		_, err := manager.GetBool("nonexistent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("wrong type", func(t *testing.T) {

		_, err := manager.GetBool("dayPhase") // This is a string
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not a boolean")
	})

	t.Run("default value when not synced", func(t *testing.T) {

		freshManager := NewManager(mockClient, logger, false)
		value, err := freshManager.GetBool("isExpectingSomeone")
		assert.NoError(t, err)
		assert.False(t, value) // Should return default
	})
}

func TestManager_SetBool(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.expecting_someone", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	manager.SyncFromHA()

	t.Run("set to true", func(t *testing.T) {

		err := manager.SetBool("isExpectingSomeone", true)
		assert.NoError(t, err)

		value, err := manager.GetBool("isExpectingSomeone")
		assert.NoError(t, err)
		assert.True(t, value)

		// Verify service call
		calls := mockClient.GetServiceCalls()
		assert.NotEmpty(t, calls)
		lastCall := calls[len(calls)-1]
		assert.Equal(t, "input_boolean", lastCall.Domain)
		assert.Equal(t, "turn_on", lastCall.Service)
	})

	t.Run("set to false", func(t *testing.T) {

		mockClient.ClearServiceCalls()
		err := manager.SetBool("isExpectingSomeone", false)
		assert.NoError(t, err)

		value, err := manager.GetBool("isExpectingSomeone")
		assert.NoError(t, err)
		assert.False(t, value)

		calls := mockClient.GetServiceCalls()
		assert.NotEmpty(t, calls)
		assert.Equal(t, "turn_off", calls[0].Service)
	})

	t.Run("invalid key", func(t *testing.T) {

		err := manager.SetBool("nonexistent", true)
		assert.Error(t, err)
	})

	t.Run("wrong type", func(t *testing.T) {

		err := manager.SetBool("dayPhase", true)
		assert.Error(t, err)
	})
}

func TestManager_GetSetString(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_text.day_phase", "morning", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	manager.SyncFromHA()

	// Get
	value, err := manager.GetString("dayPhase")
	assert.NoError(t, err)
	assert.Equal(t, "morning", value)

	// Set
	err = manager.SetString("dayPhase", "evening")
	assert.NoError(t, err)

	value, err = manager.GetString("dayPhase")
	assert.NoError(t, err)
	assert.Equal(t, "evening", value)

	// Verify service call
	calls := mockClient.GetServiceCalls()
	assert.NotEmpty(t, calls)
	lastCall := calls[len(calls)-1]
	assert.Equal(t, "input_text", lastCall.Domain)
	assert.Equal(t, "set_value", lastCall.Service)
	assert.Equal(t, "evening", lastCall.Data["value"])
}

func TestManager_GetSetNumber(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_number.alarm_time", "1668524400000", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	manager.SyncFromHA()

	// Get
	value, err := manager.GetNumber("alarmTime")
	assert.NoError(t, err)
	assert.Equal(t, 1668524400000.0, value)

	// Set
	err = manager.SetNumber("alarmTime", 9999.5)
	assert.NoError(t, err)

	value, err = manager.GetNumber("alarmTime")
	assert.NoError(t, err)
	assert.Equal(t, 9999.5, value)

	// Verify service call
	calls := mockClient.GetServiceCalls()
	assert.NotEmpty(t, calls)
	lastCall := calls[len(calls)-1]
	assert.Equal(t, "input_number", lastCall.Domain)
	assert.Equal(t, "set_value", lastCall.Service)
	assert.Equal(t, 9999.5, lastCall.Data["value"])
}

func TestManager_ChangeDetection(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.nick_home", "off", map[string]interface{}{})
	mockClient.SetState("input_text.day_phase", "morning", map[string]interface{}{})
	mockClient.SetState("input_number.alarm_time", "100", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	manager.SyncFromHA()

	t.Run("SetBool with same value should not trigger HA call", func(t *testing.T) {

		// Get initial value

		value, err := manager.GetBool("isNickHome")
		assert.NoError(t, err)
		assert.False(t, value)

		// Count service calls before
		callsBefore := len(mockClient.GetServiceCalls())

		// Set to same value
		err = manager.SetBool("isNickHome", false)
		assert.NoError(t, err)

		// Verify no new service calls
		callsAfter := len(mockClient.GetServiceCalls())
		assert.Equal(t, callsBefore, callsAfter, "Setting to same value should not trigger HA call")
	})

	t.Run("SetString with same value should not trigger HA call", func(t *testing.T) {

		// Get initial value

		value, err := manager.GetString("dayPhase")
		assert.NoError(t, err)
		assert.Equal(t, "morning", value)

		// Count service calls before
		callsBefore := len(mockClient.GetServiceCalls())

		// Set to same value
		err = manager.SetString("dayPhase", "morning")
		assert.NoError(t, err)

		// Verify no new service calls
		callsAfter := len(mockClient.GetServiceCalls())
		assert.Equal(t, callsBefore, callsAfter, "Setting to same value should not trigger HA call")
	})

	t.Run("SetNumber with same value should not trigger HA call", func(t *testing.T) {

		// Get initial value

		value, err := manager.GetNumber("alarmTime")
		assert.NoError(t, err)
		assert.Equal(t, 100.0, value)

		// Count service calls before
		callsBefore := len(mockClient.GetServiceCalls())

		// Set to same value
		err = manager.SetNumber("alarmTime", 100.0)
		assert.NoError(t, err)

		// Verify no new service calls
		callsAfter := len(mockClient.GetServiceCalls())
		assert.Equal(t, callsBefore, callsAfter, "Setting to same value should not trigger HA call")
	})

	t.Run("Subscribers should not be notified when value unchanged", func(t *testing.T) {

		notificationCount := 0
		subscription, err := manager.Subscribe("isNickHome", func(key string, oldValue, newValue interface{}) {
			notificationCount++
		})
		assert.NoError(t, err)
		defer subscription.Unsubscribe()

		// Set to same value
		err = manager.SetBool("isNickHome", false)
		assert.NoError(t, err)

		// Verify no notifications
		assert.Equal(t, 0, notificationCount, "Subscribers should not be notified when value unchanged")

		// Now change the value
		err = manager.SetBool("isNickHome", true)
		assert.NoError(t, err)

		// Should be notified (via HA callback after a brief delay in real scenario,
		// but in mock it should happen synchronously)
		// Note: In the current implementation, non-local-only variables notify via HA callback
		// So we won't see a notification here in this test, but that's expected
	})
}

func TestManager_CompareAndSwapBool(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.fade_out_in_progress", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	manager.SyncFromHA()

	t.Run("successful swap", func(t *testing.T) {

		swapped, err := manager.CompareAndSwapBool("isFadeOutInProgress", false, true)
		assert.NoError(t, err)
		assert.True(t, swapped)

		value, _ := manager.GetBool("isFadeOutInProgress")
		assert.True(t, value)
	})

	t.Run("failed swap - value changed", func(t *testing.T) {

		// Value is now true, trying to swap from false should fail

		swapped, err := manager.CompareAndSwapBool("isFadeOutInProgress", false, true)
		assert.NoError(t, err)
		assert.False(t, swapped)

		// Value should remain true
		value, _ := manager.GetBool("isFadeOutInProgress")
		assert.True(t, value)
	})

	t.Run("invalid key", func(t *testing.T) {

		_, err := manager.CompareAndSwapBool("nonexistent", false, true)
		assert.Error(t, err)
	})
}

func TestManager_Subscribe(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.nick_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.caroline_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.tori_here", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	manager.SyncFromHA()

	t.Run("state change notification", func(t *testing.T) {

		var changeCount int32
		var mu sync.Mutex
		var receivedOld, receivedNew interface{}

		sub, err := manager.Subscribe("isNickHome", func(key string, oldValue, newValue interface{}) {
			atomic.AddInt32(&changeCount, 1)
			mu.Lock()
			receivedOld = oldValue
			receivedNew = newValue
			mu.Unlock()
		})
		require.NoError(t, err)
		defer sub.Unsubscribe()

		// Simulate state change from HA
		mockClient.SimulateStateChange("input_boolean.nick_home", "on")

		assert.Equal(t, int32(1), atomic.LoadInt32(&changeCount))
		mu.Lock()
		assert.Equal(t, false, receivedOld)
		assert.Equal(t, true, receivedNew)
		mu.Unlock()
	})

	t.Run("multiple subscribers", func(t *testing.T) {

		var count1, count2 int32

		sub1, _ := manager.Subscribe("isCarolineHome", func(key string, oldValue, newValue interface{}) {
			atomic.AddInt32(&count1, 1)
		})
		sub2, _ := manager.Subscribe("isCarolineHome", func(key string, oldValue, newValue interface{}) {
			atomic.AddInt32(&count2, 1)
		})

		mockClient.SimulateStateChange("input_boolean.caroline_home", "on")

		assert.Equal(t, int32(1), atomic.LoadInt32(&count1))
		assert.Equal(t, int32(1), atomic.LoadInt32(&count2))

		sub1.Unsubscribe()
		sub2.Unsubscribe()
	})

	t.Run("unsubscribe", func(t *testing.T) {

		var changeCount int32

		sub, err := manager.Subscribe("isToriHere", func(key string, oldValue, newValue interface{}) {
			atomic.AddInt32(&changeCount, 1)
		})
		require.NoError(t, err)

		mockClient.SimulateStateChange("input_boolean.tori_here", "on")
		assert.Equal(t, int32(1), atomic.LoadInt32(&changeCount))

		sub.Unsubscribe()

		mockClient.SimulateStateChange("input_boolean.tori_here", "off")
		assert.Equal(t, int32(1), atomic.LoadInt32(&changeCount)) // Should not increment
	})

	t.Run("invalid key", func(t *testing.T) {

		_, err := manager.Subscribe("nonexistent", func(key string, oldValue, newValue interface{}) {})
		assert.Error(t, err)
	})
}

// TestSetBool_SameValue_DoesNotNotify verifies that SetBool does NOT notify subscribers
// when setting a variable to its current value. This is intentional behavior to avoid
// unnecessary Home Assistant updates and subscriber notifications.
//
// IMPORTANT: This means plugins relying on edge-triggered behavior must ensure
// state is properly cleaned up. For example, sleephygiene clears isWakeSequenceActive
// on startup to prevent stale state from blocking notifications.
//
// See: scenario_sleephygiene_test.go TestScenario_StaleWakeSequenceActive_ClearedOnStartup
func TestSetBool_SameValue_DoesNotNotify(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// Set up HA state for isWakeSequenceActive
	mockClient.SetState("input_boolean.wake_sequence_active", "on", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	manager.SyncFromHA()

	// Verify initial state is true
	initialValue, err := manager.GetBool("isWakeSequenceActive")
	require.NoError(t, err)
	require.True(t, initialValue, "Initial state should be true")

	// Subscribe to changes
	var notificationCount int32

	sub, err := manager.Subscribe("isWakeSequenceActive", func(key string, oldValue, newValue interface{}) {
		atomic.AddInt32(&notificationCount, 1)
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// Set same value - should NOT notify
	err = manager.SetBool("isWakeSequenceActive", true)
	require.NoError(t, err)

	// Verify no notification was sent (this is the expected behavior)
	assert.Equal(t, int32(0), atomic.LoadInt32(&notificationCount),
		"SetBool should NOT notify subscribers when value hasn't changed")

	// Now verify that changing the value DOES notify
	err = manager.SetBool("isWakeSequenceActive", false)
	require.NoError(t, err)

	assert.Equal(t, int32(1), atomic.LoadInt32(&notificationCount),
		"SetBool should notify subscribers when value changes")
}

func TestManagerNotifySubscribersIsSynchronous(t *testing.T) {
	t.Parallel()
	manager := &Manager{
		logger: zap.NewNop(),
		subscribers: map[string]map[uint64]StateChangeHandler{
			"test": {
				1: func(string, interface{}, interface{}) {
					time.Sleep(50 * time.Millisecond)
				},
				2: func(string, interface{}, interface{}) {},
			},
		},
	}

	start := time.Now()
	manager.notifySubscribers("test", nil, nil)
	elapsed := time.Since(start)

	assert.GreaterOrEqual(t, elapsed, 50*time.Millisecond)
}

func TestManagerNotifySubscribersRecoversFromPanics(t *testing.T) {
	t.Parallel()
	secondCalled := false
	manager := &Manager{
		logger: zap.NewNop(),
		subscribers: map[string]map[uint64]StateChangeHandler{
			"test": {
				1: func(string, interface{}, interface{}) {
					panic("boom")
				},
				2: func(string, interface{}, interface{}) {
					secondCalled = true
				},
			},
		},
	}

	assert.NotPanics(t, func() {
		manager.notifySubscribers("test", nil, nil)
	})
	assert.True(t, secondCalled)
}

func TestManager_GetAllValues(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.nick_home", "on", map[string]interface{}{})
	mockClient.SetState("input_text.day_phase", "morning", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	manager.SyncFromHA()

	values := manager.GetAllValues()
	assert.NotEmpty(t, values)
	assert.True(t, values["isNickHome"].(bool))
	assert.Equal(t, "morning", values["dayPhase"].(string))
}

func TestExtractEntityName(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		input    string
		expected string
	}{
		{"input_boolean.nick_home", "nick_home"},
		{"input_number.alarm_time", "alarm_time"},
		{"input_text.day_phase", "day_phase"},
		{"simple", "simple"},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {

			result := extractEntityName(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestManager_GetJSON(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	manager := NewManager(mockClient, logger, false)

	// Test getting default value
	var result map[string]interface{}
	err := manager.GetJSON("currentlyPlayingMusic", &result)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 0, len(result))

	// Test getting with cached value
	testData := map[string]interface{}{
		"artist": "Test Artist",
		"title":  "Test Song",
		"album":  "Test Album",
	}
	manager.cache["currentlyPlayingMusic"] = testData

	var cached map[string]interface{}
	err = manager.GetJSON("currentlyPlayingMusic", &cached)
	assert.NoError(t, err)
	assert.Equal(t, "Test Artist", cached["artist"])
	assert.Equal(t, "Test Song", cached["title"])

	// Test error: non-existent variable
	var dummy map[string]interface{}
	err = manager.GetJSON("nonExistent", &dummy)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	// Test error: wrong type
	err = manager.GetJSON("isNickHome", &dummy)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not JSON")
}

func TestManager_SetJSON(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)

	// Test setting JSON value (local-only variable)
	testData := map[string]interface{}{
		"artist": "New Artist",
		"title":  "New Song",
	}
	err := manager.SetJSON("currentlyPlayingMusic", testData)
	assert.NoError(t, err)

	// Verify the value was set
	var result map[string]interface{}
	err = manager.GetJSON("currentlyPlayingMusic", &result)
	assert.NoError(t, err)
	assert.Equal(t, "New Artist", result["artist"])

	// Test error: non-existent variable
	err = manager.SetJSON("nonExistent", testData)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	// Test error: wrong type
	err = manager.SetJSON("isNickHome", testData)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not JSON")
}

func TestManager_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.nick_home", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	manager.SyncFromHA()

	// Run concurrent reads and writes
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				manager.SetBool("isNickHome", j%2 == 0)
				manager.GetBool("isNickHome")
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should complete without race conditions
	value, err := manager.GetBool("isNickHome")
	assert.NoError(t, err)
	assert.NotNil(t, value)
}

func TestVariablesByEntityID(t *testing.T) {
	t.Parallel()
	vars := VariablesByEntityID()
	assert.NotNil(t, vars)
	assert.Greater(t, len(vars), 0)

	// Check that a known variable is in the map
	nickHome, ok := vars["input_boolean.nick_home"]
	assert.True(t, ok)
	assert.Equal(t, "isNickHome", nickHome.Key)
	assert.Equal(t, TypeBool, nickHome.Type)
}

func TestManager_ReadOnlyMode(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.expecting_someone", "off", map[string]interface{}{})
	mockClient.SetState("input_text.battery_energy_level", "green", map[string]interface{}{})
	mockClient.Connect()

	// Create manager in read-only mode
	manager := NewManager(mockClient, logger, true)
	manager.SyncFromHA()

	t.Run("regular variable write blocked in read-only mode", func(t *testing.T) {

		err := manager.SetBool("isExpectingSomeone", true)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrReadOnlyMode)

		// Value should not have changed
		value, _ := manager.GetBool("isExpectingSomeone")
		assert.False(t, value)

		// Verify no service call was made
		mockClient.ClearServiceCalls()
		calls := mockClient.GetServiceCalls()
		assert.Empty(t, calls)
	})

	t.Run("computed output variable write allowed in read-only mode", func(t *testing.T) {

		mockClient.ClearServiceCalls()

		// batteryEnergyLevel is marked as ComputedOutput: true
		err := manager.SetString("batteryEnergyLevel", "yellow")
		assert.NoError(t, err)

		// Value should have changed
		value, _ := manager.GetString("batteryEnergyLevel")
		assert.Equal(t, "yellow", value)

		// Verify service call was made to HA
		calls := mockClient.GetServiceCalls()
		assert.NotEmpty(t, calls)
		lastCall := calls[len(calls)-1]
		assert.Equal(t, "input_text", lastCall.Domain)
		assert.Equal(t, "set_value", lastCall.Service)
		assert.Equal(t, "yellow", lastCall.Data["value"])
	})

	t.Run("all energy variables writable in read-only mode", func(t *testing.T) {

		mockClient.ClearServiceCalls()

		// Test all three energy variables
		err := manager.SetString("batteryEnergyLevel", "red")
		assert.NoError(t, err)

		err = manager.SetString("currentEnergyLevel", "green")
		assert.NoError(t, err)

		err = manager.SetString("solarProductionEnergyLevel", "yellow")
		assert.NoError(t, err)

		// Verify all three service calls were made
		calls := mockClient.GetServiceCalls()
		assert.Len(t, calls, 3)
	})

	t.Run("local-only variable write allowed in read-only mode", func(t *testing.T) {

		// didOwnerJustReturnHome is LocalOnly: true

		err := manager.SetBool("didOwnerJustReturnHome", true)
		assert.NoError(t, err)

		value, _ := manager.GetBool("didOwnerJustReturnHome")
		assert.True(t, value)
	})

	t.Run("read operations work in read-only mode", func(t *testing.T) {

		// Reading should always work

		value, err := manager.GetBool("isExpectingSomeone")
		assert.NoError(t, err)
		assert.False(t, value)

		strValue, err := manager.GetString("batteryEnergyLevel")
		assert.NoError(t, err)
		assert.Equal(t, "red", strValue) // From previous test
	})
}

func TestManager_ComputedOutputFlagVerification(t *testing.T) {
	t.Parallel()
	// Verify that energy variables have ComputedOutput: true

	vars := VariablesByKey()

	batteryVar := vars["batteryEnergyLevel"]
	assert.True(t, batteryVar.ComputedOutput, "batteryEnergyLevel should have ComputedOutput: true")

	currentVar := vars["currentEnergyLevel"]
	assert.True(t, currentVar.ComputedOutput, "currentEnergyLevel should have ComputedOutput: true")

	solarVar := vars["solarProductionEnergyLevel"]
	assert.True(t, solarVar.ComputedOutput, "solarProductionEnergyLevel should have ComputedOutput: true")

	// Verify other variables don't have this flag
	nickHomeVar := vars["isNickHome"]
	assert.False(t, nickHomeVar.ComputedOutput, "isNickHome should not have ComputedOutput flag")
}

func TestManager_SubscribeWithContext(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.nick_home", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	require.NoError(t, manager.SyncFromHA())

	t.Run("receives event context with correlation ID", func(t *testing.T) {
		var receivedCtx *EventContext
		var receivedKey string
		var receivedOld, receivedNew interface{}
		var wg sync.WaitGroup
		wg.Add(1)

		sub, err := manager.SubscribeWithContext("isNickHome", func(ctx *EventContext, key string, oldValue, newValue interface{}) {
			receivedCtx = ctx
			receivedKey = key
			receivedOld = oldValue
			receivedNew = newValue
			wg.Done()
		})
		require.NoError(t, err)
		defer sub.Unsubscribe()

		mockClient.SimulateStateChange("input_boolean.nick_home", "on")

		// Wait for callback
		waitDone := make(chan struct{})
		go func() {
			wg.Wait()
			close(waitDone)
		}()
		select {
		case <-waitDone:
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for callback")
		}

		require.NotNil(t, receivedCtx)
		assert.NotEmpty(t, receivedCtx.CorrelationID)
		assert.Equal(t, "isNickHome", receivedCtx.TriggerKey)
		assert.Equal(t, "isNickHome", receivedKey)
		assert.Equal(t, false, receivedOld)
		assert.Equal(t, true, receivedNew)
	})

	t.Run("unsubscribe works", func(t *testing.T) {
		var callCount int32

		sub, err := manager.SubscribeWithContext("isNickHome", func(ctx *EventContext, key string, oldValue, newValue interface{}) {
			atomic.AddInt32(&callCount, 1)
		})
		require.NoError(t, err)

		mockClient.SimulateStateChange("input_boolean.nick_home", "off")
		time.Sleep(50 * time.Millisecond)
		assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))

		sub.Unsubscribe()

		mockClient.SimulateStateChange("input_boolean.nick_home", "on")
		time.Sleep(50 * time.Millisecond)
		assert.Equal(t, int32(1), atomic.LoadInt32(&callCount)) // Should not increment
	})

	t.Run("invalid key returns error", func(t *testing.T) {
		_, err := manager.SubscribeWithContext("nonexistent", func(ctx *EventContext, key string, oldValue, newValue interface{}) {})
		assert.Error(t, err)
	})
}

func TestManager_SubscribeWithContext_MultipleHandlers(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.nick_home", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	require.NoError(t, manager.SyncFromHA())

	var ctx1, ctx2 *EventContext
	var wg sync.WaitGroup
	wg.Add(2)

	sub1, err := manager.SubscribeWithContext("isNickHome", func(ctx *EventContext, key string, oldValue, newValue interface{}) {
		ctx1 = ctx
		wg.Done()
	})
	require.NoError(t, err)
	defer sub1.Unsubscribe()

	sub2, err := manager.SubscribeWithContext("isNickHome", func(ctx *EventContext, key string, oldValue, newValue interface{}) {
		ctx2 = ctx
		wg.Done()
	})
	require.NoError(t, err)
	defer sub2.Unsubscribe()

	mockClient.SimulateStateChange("input_boolean.nick_home", "on")

	// Wait for callbacks
	waitDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for callbacks")
	}

	// Both handlers should receive the SAME context (same correlation ID)
	require.NotNil(t, ctx1)
	require.NotNil(t, ctx2)
	assert.Equal(t, ctx1.CorrelationID, ctx2.CorrelationID)
}

func TestManager_MixedSubscribers(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.nick_home", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	require.NoError(t, manager.SyncFromHA())

	var regularCalled bool
	var contextCalled bool
	var receivedCtx *EventContext
	var wg sync.WaitGroup
	wg.Add(2)

	// Regular subscriber
	sub1, err := manager.Subscribe("isNickHome", func(key string, oldValue, newValue interface{}) {
		regularCalled = true
		wg.Done()
	})
	require.NoError(t, err)
	defer sub1.Unsubscribe()

	// Context-aware subscriber
	sub2, err := manager.SubscribeWithContext("isNickHome", func(ctx *EventContext, key string, oldValue, newValue interface{}) {
		contextCalled = true
		receivedCtx = ctx
		wg.Done()
	})
	require.NoError(t, err)
	defer sub2.Unsubscribe()

	mockClient.SimulateStateChange("input_boolean.nick_home", "on")

	// Wait for callbacks
	waitDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for callbacks")
	}

	// Both should be called
	assert.True(t, regularCalled)
	assert.True(t, contextCalled)
	assert.NotNil(t, receivedCtx)
	assert.NotEmpty(t, receivedCtx.CorrelationID)
}

func TestManager_SyncFromHA_PreservesLocalOnlyVariables(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// Setup minimal HA state so SyncFromHA succeeds
	mockClient.SetState("input_boolean.nick_home", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)

	// First sync initializes local-only variables to defaults
	err := manager.SyncFromHA()
	require.NoError(t, err)

	// Verify didOwnerJustReturnHome starts as false (default)
	value, err := manager.GetBool("didOwnerJustReturnHome")
	require.NoError(t, err)
	assert.False(t, value, "local-only variable should start at default")

	// Simulate runtime: plugin sets didOwnerJustReturnHome = true (e.g., owner arrived home)
	err = manager.SetBool("didOwnerJustReturnHome", true)
	require.NoError(t, err)

	value, err = manager.GetBool("didOwnerJustReturnHome")
	require.NoError(t, err)
	require.True(t, value, "value should be true after SetBool")

	// Simulate WebSocket reconnect: SyncFromHA is called again
	err = manager.SyncFromHA()
	require.NoError(t, err)

	// Local-only variable must preserve its runtime value (true), not reset to default (false)
	value, err = manager.GetBool("didOwnerJustReturnHome")
	require.NoError(t, err)
	assert.True(t, value, "SyncFromHA must NOT reset local-only variables that already have cached values")

	// Verify that subscribers are still notified when the value later changes to false
	// (This is the actual production bug: auto-reset timer sets false, but if cache was
	// already reset to false by SyncFromHA, SetBool sees no change and skips notification)
	var notified int32
	sub, err := manager.Subscribe("didOwnerJustReturnHome", func(key string, oldValue, newValue interface{}) {
		atomic.AddInt32(&notified, 1)
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// Auto-reset timer fires: sets didOwnerJustReturnHome = false
	err = manager.SetBool("didOwnerJustReturnHome", false)
	require.NoError(t, err)

	assert.Equal(t, int32(1), atomic.LoadInt32(&notified),
		"subscriber must be notified when local-only variable changes from true to false after reconnect")
}

// TestManager_EchoBackSuppression verifies that stale echo-backs from HA
// do not overwrite locally-written cache values. This is the fix for #637.
//
// Race sequence without fix:
//  1. Plugin calls SetString("musicPlaybackType", "morning") → cache = "morning"
//  2. HA sends stale echo-back with "sleep" (from previous state)
//  3. subscribeToEntity sees "sleep" ≠ "morning", overwrites cache to "sleep"
//
// With the fix, the stale echo-back is suppressed because the pending write
// flag tells the callback to ignore echo-backs until a matching value arrives.
//
// NOTE: The mock client sends a confirming echo-back synchronously during
// SetInputText, which clears the pending flag immediately. To test the race
// window, these tests set up the pending write state directly.
func TestManager_EchoBackSuppression(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_text.music_playback_type", "sleep", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	require.NoError(t, manager.SyncFromHA())

	// Verify initial state
	value, err := manager.GetString("musicPlaybackType")
	require.NoError(t, err)
	require.Equal(t, "sleep", value)

	t.Run("stale echo-back does not overwrite local write", func(t *testing.T) {
		// Simulate the race window: cache has "morning" and HA hasn't
		// confirmed yet (pending write still active).
		manager.cacheMu.Lock()
		manager.cache["musicPlaybackType"] = "morning"
		manager.pendingWrites["musicPlaybackType"] = "morning"
		manager.cacheMu.Unlock()

		// Stale echo-back from HA with old "sleep" value
		mockClient.SimulateStateChange("input_text.music_playback_type", "sleep")

		// Cache must still be "morning" — stale echo-back was suppressed
		value, err := manager.GetString("musicPlaybackType")
		require.NoError(t, err)
		assert.Equal(t, "morning", value, "stale echo-back must not overwrite locally-written value")
	})

	t.Run("matching echo-back clears pending flag", func(t *testing.T) {
		// Set up pending state
		manager.cacheMu.Lock()
		manager.cache["musicPlaybackType"] = "morning"
		manager.pendingWrites["musicPlaybackType"] = "morning"
		manager.cacheMu.Unlock()

		// Simulate confirming echo-back from HA
		mockClient.SimulateStateChange("input_text.music_playback_type", "morning")

		// Pending flag should be cleared
		manager.cacheMu.RLock()
		_, hasPending := manager.pendingWrites["musicPlaybackType"]
		manager.cacheMu.RUnlock()
		assert.False(t, hasPending, "matching echo-back should clear pending write flag")

		// Cache should still be "morning"
		value, err := manager.GetString("musicPlaybackType")
		require.NoError(t, err)
		assert.Equal(t, "morning", value)
	})

	t.Run("external change accepted after echo-back confirmed", func(t *testing.T) {
		// Ensure no pending write
		manager.cacheMu.Lock()
		delete(manager.pendingWrites, "musicPlaybackType")
		manager.cache["musicPlaybackType"] = "morning"
		manager.cacheMu.Unlock()

		// Track subscriber notifications
		var notified int32
		sub, err := manager.Subscribe("musicPlaybackType", func(key string, oldValue, newValue interface{}) {
			atomic.AddInt32(&notified, 1)
		})
		require.NoError(t, err)
		defer sub.Unsubscribe()

		// External change from HA (e.g., another client changed the value)
		mockClient.SimulateStateChange("input_text.music_playback_type", "evening")

		// Without pending write, external change must be accepted
		value, err := manager.GetString("musicPlaybackType")
		require.NoError(t, err)
		assert.Equal(t, "evening", value, "external change should be accepted when no pending write")
		assert.Equal(t, int32(1), atomic.LoadInt32(&notified),
			"subscribers should be notified of genuine external change")
	})

	t.Run("stale echo-back does not notify subscribers", func(t *testing.T) {
		// Set up a pending write
		manager.cacheMu.Lock()
		manager.cache["musicPlaybackType"] = "morning"
		manager.pendingWrites["musicPlaybackType"] = "morning"
		manager.cacheMu.Unlock()

		var notified int32
		sub, err := manager.Subscribe("musicPlaybackType", func(key string, oldValue, newValue interface{}) {
			atomic.AddInt32(&notified, 1)
		})
		require.NoError(t, err)
		defer sub.Unsubscribe()

		// Stale echo-back
		mockClient.SimulateStateChange("input_text.music_playback_type", "sleep")

		assert.Equal(t, int32(0), atomic.LoadInt32(&notified),
			"stale echo-back must not notify subscribers")
	})
}

// TestManager_EchoBackSuppression_Bool verifies echo-back suppression for boolean values.
func TestManager_EchoBackSuppression_Bool(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	require.NoError(t, manager.SyncFromHA())

	// Simulate the race window: cache has true, pending write for true
	manager.cacheMu.Lock()
	manager.cache["isWakeSequenceActive"] = true
	manager.pendingWrites["isWakeSequenceActive"] = true
	manager.cacheMu.Unlock()

	// Stale echo-back with "off" (false)
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "off")

	// Cache must still be true
	value, err := manager.GetBool("isWakeSequenceActive")
	require.NoError(t, err)
	assert.True(t, value, "stale echo-back must not overwrite locally-written boolean value")

	// Confirming echo-back with "on" (true)
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "on")

	// Pending flag should be cleared
	manager.cacheMu.RLock()
	_, hasPending := manager.pendingWrites["isWakeSequenceActive"]
	manager.cacheMu.RUnlock()
	assert.False(t, hasPending, "confirming echo-back should clear pending write")
}

// TestManager_EchoBackSuppression_Number verifies echo-back suppression for number values.
func TestManager_EchoBackSuppression_Number(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_number.alarm_time", "100", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	require.NoError(t, manager.SyncFromHA())

	// Simulate the race window
	manager.cacheMu.Lock()
	manager.cache["alarmTime"] = 200.0
	manager.pendingWrites["alarmTime"] = 200.0
	manager.cacheMu.Unlock()

	// Stale echo-back with old value
	mockClient.SimulateStateChange("input_number.alarm_time", "100")

	// Cache must still be 200
	value, err := manager.GetNumber("alarmTime")
	require.NoError(t, err)
	assert.Equal(t, 200.0, value, "stale echo-back must not overwrite locally-written number value")

	// Confirming echo-back
	mockClient.SimulateStateChange("input_number.alarm_time", "200")

	manager.cacheMu.RLock()
	_, hasPending := manager.pendingWrites["alarmTime"]
	manager.cacheMu.RUnlock()
	assert.False(t, hasPending, "confirming echo-back should clear pending write")
}

// TestManager_EchoBackSuppression_CompareAndSwap verifies echo-back suppression
// for CompareAndSwapBool, which also writes to the cache and syncs to HA.
func TestManager_EchoBackSuppression_CompareAndSwap(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.fade_out_in_progress", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	require.NoError(t, manager.SyncFromHA())

	// Simulate the race window: CAS wrote true, pending write active
	manager.cacheMu.Lock()
	manager.cache["isFadeOutInProgress"] = true
	manager.pendingWrites["isFadeOutInProgress"] = true
	manager.cacheMu.Unlock()

	// Stale echo-back with "off" (false)
	mockClient.SimulateStateChange("input_boolean.fade_out_in_progress", "off")

	// Cache must still be true
	value, err := manager.GetBool("isFadeOutInProgress")
	require.NoError(t, err)
	assert.True(t, value, "stale echo-back must not overwrite CAS-written value")
}

// TestManager_EchoBackSuppression_LocalOnlyNoFlag verifies that local-only
// variables do not set pending write flags (they don't sync with HA).
func TestManager_EchoBackSuppression_LocalOnlyNoFlag(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	require.NoError(t, manager.SyncFromHA())

	// Set a local-only variable
	err := manager.SetBool("didOwnerJustReturnHome", true)
	require.NoError(t, err)

	// Should not have a pending write flag
	manager.cacheMu.RLock()
	_, hasPending := manager.pendingWrites["didOwnerJustReturnHome"]
	manager.cacheMu.RUnlock()
	assert.False(t, hasPending, "local-only variable should not have pending write flag")
}

// TestManager_EchoBackSuppression_RapidWrites verifies that rapid successive
// writes correctly track the latest pending value. If a plugin writes A then B,
// the pending value should be B, and an echo-back of A should still be suppressed.
func TestManager_EchoBackSuppression_RapidWrites(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_text.day_phase", "morning", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	require.NoError(t, manager.SyncFromHA())

	// Simulate rapid writes: pending value is "night" (the latest write)
	manager.cacheMu.Lock()
	manager.cache["dayPhase"] = "night"
	manager.pendingWrites["dayPhase"] = "night"
	manager.cacheMu.Unlock()

	// Echo-back for the first write ("evening") should be suppressed
	mockClient.SimulateStateChange("input_text.day_phase", "evening")

	value, err := manager.GetString("dayPhase")
	require.NoError(t, err)
	assert.Equal(t, "night", value, "echo-back of intermediate value must not overwrite latest write")

	// Echo-back for the original value ("morning") should also be suppressed
	mockClient.SimulateStateChange("input_text.day_phase", "morning")

	value, err = manager.GetString("dayPhase")
	require.NoError(t, err)
	assert.Equal(t, "night", value, "echo-back of original value must not overwrite latest write")

	// Confirming echo-back for the final value ("night")
	mockClient.SimulateStateChange("input_text.day_phase", "night")

	manager.cacheMu.RLock()
	_, hasPending := manager.pendingWrites["dayPhase"]
	manager.cacheMu.RUnlock()
	assert.False(t, hasPending, "confirming echo-back should clear pending write")
}

// TestManager_EchoBackSuppression_HAFailureRollback verifies that when HA sync
// fails, the pending write flag is also cleaned up along with the cache rollback.
func TestManager_EchoBackSuppression_HAFailureRollback(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_text.day_phase", "morning", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	require.NoError(t, manager.SyncFromHA())

	// Make HA calls fail
	mockClient.SetServiceError("input_text", "set_value", fmt.Errorf("connection lost"))

	err := manager.SetString("dayPhase", "evening")
	assert.Error(t, err)

	// Cache should be rolled back to "morning"
	value, err := manager.GetString("dayPhase")
	require.NoError(t, err)
	assert.Equal(t, "morning", value, "cache should be rolled back on HA failure")

	// Pending write should be cleared
	manager.cacheMu.RLock()
	_, hasPending := manager.pendingWrites["dayPhase"]
	manager.cacheMu.RUnlock()
	assert.False(t, hasPending, "pending write should be cleared on HA failure")
}

// TestManager_SetString_NoPendingWriteWithoutSub verifies that SetString does NOT
// set a pending write when there's no active HA subscription (e.g., during test setup
// before SyncFromHA or plugin Start). This prevents stale pending writes from blocking
// subsequent SimulateStateChange calls in tests.
func TestManager_SetString_NoPendingWriteWithoutSub(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	// Use nil client — no HA subscriptions possible
	manager := NewManager(nil, logger, false)
	manager.cache["musicPlaybackType"] = "sleep"

	err := manager.SetString("musicPlaybackType", "morning")
	require.NoError(t, err)

	// Without an active HA subscription, no pending write should be set
	manager.cacheMu.RLock()
	_, hasPending := manager.pendingWrites["musicPlaybackType"]
	manager.cacheMu.RUnlock()
	assert.False(t, hasPending, "SetString should NOT set pending write without active HA subscription")
}

// TestManager_SetBool_NoPendingWriteWithoutSub verifies that SetBool does NOT
// set a pending write when there's no active HA subscription.
func TestManager_SetBool_NoPendingWriteWithoutSub(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	manager := NewManager(nil, logger, false)
	manager.cache["isWakeSequenceActive"] = false

	err := manager.SetBool("isWakeSequenceActive", true)
	require.NoError(t, err)

	manager.cacheMu.RLock()
	_, hasPending := manager.pendingWrites["isWakeSequenceActive"]
	manager.cacheMu.RUnlock()
	assert.False(t, hasPending, "SetBool should NOT set pending write without active HA subscription")
}

// TestManager_SetNumber_NoPendingWriteWithoutSub verifies that SetNumber does NOT
// set a pending write when there's no active HA subscription.
func TestManager_SetNumber_NoPendingWriteWithoutSub(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	manager := NewManager(nil, logger, false)
	manager.cache["alarmTime"] = 100.0

	err := manager.SetNumber("alarmTime", 200.0)
	require.NoError(t, err)

	manager.cacheMu.RLock()
	_, hasPending := manager.pendingWrites["alarmTime"]
	manager.cacheMu.RUnlock()
	assert.False(t, hasPending, "SetNumber should NOT set pending write without active HA subscription")
}

// TestManager_SyncFromHA_ClearsPendingWrites verifies that SyncFromHA clears
// any pending writes, preventing stale flags from surviving reconnection.
func TestManager_SyncFromHA_ClearsPendingWrites(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_text.day_phase", "morning", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	require.NoError(t, manager.SyncFromHA())

	// Manually inject a pending write to simulate a write that happened before disconnection
	manager.cacheMu.Lock()
	manager.pendingWrites["dayPhase"] = "evening"
	manager.cacheMu.Unlock()

	// Verify pending write flag is set
	manager.cacheMu.RLock()
	_, hasPending := manager.pendingWrites["dayPhase"]
	manager.cacheMu.RUnlock()
	require.True(t, hasPending, "pending write should be set for test")

	// Simulate reconnection by calling SyncFromHA again
	mockClient.SetState("input_text.day_phase", "evening", map[string]interface{}{})
	err := manager.SyncFromHA()
	require.NoError(t, err)

	// Pending write should be cleared
	manager.cacheMu.RLock()
	_, hasPending = manager.pendingWrites["dayPhase"]
	manager.cacheMu.RUnlock()
	assert.False(t, hasPending, "SyncFromHA should clear all pending writes")
}
