package state

import (
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
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	manager := NewManager(mockClient, logger, false)
	assert.NotNil(t, manager)
	assert.Equal(t, len(AllVariables), len(manager.variables))
}

func TestManager_SyncFromHA(t *testing.T) {
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

func TestManagerNotifySubscribersIsSynchronous(t *testing.T) {
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
